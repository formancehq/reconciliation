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
tests:
    go test -race -covermode atomic ./...

# Run the serial integration suite against a live Ledger v3 on localhost:8888
tests-integration:
    go test -race -covermode atomic -tags it -p 1 ./...

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
