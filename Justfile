set dotenv-load

# List available commands
default:
    @just --list

# Run pre-commit checks (generate, tidy, lint)
pre-commit: tidy lint generate-client-check fctl-sdk-check fctl-audit-check fctl-component-test
alias pc := pre-commit

# Run linter with auto-fix
lint:
    golangci-lint run --fix --build-tags it --timeout 5m
    cd plugins/fctl && ./scripts/with-fctl-sdk.sh golangci-lint run --fix --timeout 5m

# Tidy go modules
tidy:
    go mod tidy
    cd plugins/fctl && ./scripts/with-fctl-sdk.sh ./scripts/tidy-with-fctl-sdk.sh

# Generate the product-owned Go client directly from the repository OpenAPI.
generate-client:
    @speakeasy generate sdk -s openapi.yaml -o ./pkg/client -l go -y
    go run ./internal/generateclient -client ./pkg/client
    cd pkg/client && go mod tidy

# Regenerate in an isolated directory and fail on any byte-level drift.
generate-client-check:
    ./scripts/check-client-generation_test.sh
    ./scripts/check-client-generation.sh

# Generate code
generate: generate-client
    go generate ./...

# Run tests with race detector and coverage
tests: fctl-component-test
    go test -race -covermode atomic -tags it ./...
    cd pkg/client && go test -race -covermode atomic ./...

# Regenerate the committed fctl Reconciliation plugin operation inventory from
# openapi.yaml. Review the diff: a change here is a change to the plugin's
# source of truth.
fctl-audit:
    cd plugins/fctl && ./scripts/with-fctl-sdk.sh go run ./cmd/specaudit -spec ../../openapi.yaml -out .

# Fail if the committed inventory artefacts no longer match openapi.yaml.
fctl-audit-check:
    cd plugins/fctl && ./scripts/with-fctl-sdk.sh go run ./cmd/specaudit -spec ../../openapi.yaml -out . -check

# Verify the explicit fctl SDK source against the committed content lock.
fctl-sdk-check:
    cd plugins/fctl && ./scripts/with-fctl-sdk.sh ./scripts/tidy-with-fctl-sdk.sh --check

fctl-component-test:
    cd plugins/fctl && just test

fctl-component-build:
    cd plugins/fctl && just build-component

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
