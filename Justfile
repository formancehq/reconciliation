set dotenv-load

# List available commands
default:
    @just --list

# Run pre-commit checks (generate, tidy, lint)
pre-commit: tidy lint
alias pc := pre-commit

# Run linter with auto-fix
lint:
    golangci-lint run --fix --build-tags it --timeout 5m

# Tidy go modules
tidy:
    go mod tidy

# Generate code
generate: generate-ledger-proto
    go generate ./...

# Re-vendor the Ledger v3 .proto sources from a local ledger checkout, then
# regenerate. The protos and internal/ledgerpb/grpcprotocol.Version are ONE
# contract: ledger renumbers proto fields between revisions, so vendoring new
# protos without bumping the revision (or vice versa) reintroduces the silent
# misdecode the EN-1851 gate exists to catch. This prints both for comparison.
sync-ledger-proto ledger_repo="../ledger":
    #!/usr/bin/env bash
    set -euo pipefail
    src="{{ ledger_repo }}/misc/proto"
    test -d "$src" || { echo "no proto dir at $src — pass the ledger checkout: just sync-ledger-proto /path/to/ledger"; exit 1; }
    for f in "$src"/*.proto; do
        sed 's#github.com/formancehq/ledger/v3/internal/proto/#github.com/formancehq/reconciliation/internal/ledgerpb/#' \
            "$f" > "proto/ledger/$(basename "$f")"
    done
    echo "synced from $(git -C "{{ ledger_repo }}" rev-parse --short HEAD) on $(git -C "{{ ledger_repo }}" rev-parse --abbrev-ref HEAD)"
    echo "ledger  protocol revision: $(grep -oE 'Version = "[0-9]+"' "{{ ledger_repo }}/pkg/grpcprotocol/protocol.go" | grep -oE '[0-9]+')"
    echo "vendored protocol revision: $(grep -oE 'Version = "[0-9]+"' internal/ledgerpb/grpcprotocol/protocol.go | grep -oE '[0-9]+')"
    echo "^ if these differ, update internal/ledgerpb/grpcprotocol/protocol.go before shipping"
    just generate-ledger-proto

# Generate the vendored Ledger v3 protobuf bindings
generate-ledger-proto:
    rm -f internal/ledgerpb/auditpb/*.pb.go internal/ledgerpb/clusterbootstrappb/*.pb.go internal/ledgerpb/clusterpb/*.pb.go internal/ledgerpb/commonpb/*.pb.go internal/ledgerpb/eventspb/*.pb.go internal/ledgerpb/proposalpb/*.pb.go internal/ledgerpb/raftcmdpb/*.pb.go internal/ledgerpb/rafttransportpb/*.pb.go internal/ledgerpb/restorepb/*.pb.go internal/ledgerpb/servicepb/*.pb.go internal/ledgerpb/signaturepb/*.pb.go internal/ledgerpb/snapshotpb/*.pb.go
    protoc --go_out=. --go_opt=module=github.com/formancehq/reconciliation \
        --go-grpc_out=. --go-grpc_opt=module=github.com/formancehq/reconciliation \
        --go-vtproto_out=. --go-vtproto_opt=module=github.com/formancehq/reconciliation \
        --go-vtproto_opt=features=marshal+unmarshal+size+clone+equal \
        -I proto/ledger \
        proto/ledger/raft_transport.proto \
        proto/ledger/common.proto \
        proto/ledger/cluster.proto \
        proto/ledger/cluster_bootstrap.proto \
        proto/ledger/bucket.proto \
        proto/ledger/raft_cmd.proto \
        proto/ledger/snapshot.proto \
        proto/ledger/audit.proto \
        proto/ledger/signature.proto \
        proto/ledger/events.proto \
        proto/ledger/restore.proto \
        proto/ledger/proposal.proto

# Run hermetic tests with race detector and coverage
#
# coverpkg excludes the 12 generated internal/ledgerpb/*pb packages: they are
# ~96% of the statements in this module and ~1.6% covered, so including them
# reports 4.5% where the hand-written figure is ~74%. grpcprotocol lives under
# the same tree but is hand-written, so the filter matches only the `*pb`
# package names. tools/ holds standalone tools with no Go tests
# (tools/bench-txlevel; tools/lettering-duckdb is SQL with its own
# lettering-duckdb-tests), excluded for the same reason cmd/ is in codecov.yml.
# codecov.yml applies the equivalent exclusions server-side.
#
# -count=1 defeats the test cache. A cached package is replayed without
# re-emitting its full coverage profile, so a warm-cache run silently understates
# the total and the figure moves between runs on an unchanged tree — 64.6% where
# a cold run reports a stable 73.9%. This recipe is what produces coverage.out
# for codecov, so it has to measure the same thing locally that CI (always cold)
# measures. The cost is that every package really re-runs; use plain `go test`
# for a fast inner loop.
tests:
    go test -count=1 -race -covermode atomic -coverprofile=coverage.out \
        -coverpkg=$(go list ./... | grep -vE '/internal/ledgerpb/[a-z]+pb$|/tools/' | paste -sd, -) ./...

# Test the DuckDB checks and query pack for lettering result files
# (tools/lettering-duckdb) on their golden files. SQL run by the DuckDB CLI, which
# the Nix shell provides; deliberately not part of `tests`, so the Go suite takes
# no DuckDB dependency.
lettering-duckdb-tests:
    tools/lettering-duckdb/test.sh

# Run the serial integration suite against a live Ledger v3 on localhost:8888
tests-integration:
    go test -race -covermode atomic -tags it -p 1 ./...

# Ledger image the integration suite runs against in CI. Pin a tag, never a
# branch head: its gRPC protocol revision must equal
# internal/ledgerpb/grpcprotocol.Version, which
# TestIntegration_ProtocolVersionMatchesServer checks. Bump it in the same commit
# as `just sync-ledger-proto`.
ledger_it_image := "ghcr.io/formancehq/ledger:v3.0.0-beta.7"

# Run the integration suite against a throwaway single-node ledger of the pinned
# tag, one per run, on a random local port. Data and WAL live on tmpfs: fast,
# and free of the ledger's disk-usage write block (80% by default) on a full
# Docker disk. The container is removed on exit, and its logs are printed when
# the suite fails.
tests-integration-ephemeral:
    #!/usr/bin/env bash
    set -euo pipefail
    name="recon-it-ledger-$$"
    docker run -d --name "$name" -p 127.0.0.1::8888 --tmpfs /var/lib/ledger:size=2g \
        {{ ledger_it_image }} run --node-id 1 --cluster-id recon-it --bootstrap \
        --bind-addr 127.0.0.1:7777 --data-dir /var/lib/ledger/data --wal-dir /var/lib/ledger/wal >/dev/null
    status=1
    trap '[ "$status" = 0 ] || docker logs --tail 100 "$name" >&2 || true; docker rm -f "$name" >/dev/null' EXIT
    addr="$(docker port "$name" 8888/tcp | head -1)"
    for _ in $(seq 1 60); do
        (exec 3<>"/dev/tcp/${addr%:*}/${addr##*:}") 2>/dev/null && break
        sleep 1
    done
    echo "ledger {{ ledger_it_image }} on $addr"
    RECON_LEDGER_ADDR="$addr" go test -count=1 -race -tags it -p 1 -run '^TestIntegration_' ./...
    status=0

# Build the binary locally
build:
    go build -o ./bin/reconciliation .

# Run local nightly build (skip publish)
release-local:
    goreleaser release --nightly --skip=publish

# Run CI nightly release
release-ci:
    goreleaser release --nightly

# Run standard release
release:
    goreleaser release
