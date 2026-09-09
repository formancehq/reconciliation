set dotenv-load

# List available commands
default:
    @just --list

# Run pre-commit checks (generate, tidy, lint)
pre-commit: tidy lint fctl-audit-check
alias pc := pre-commit

# Run linter with auto-fix
lint:
    golangci-lint run --fix --build-tags it --timeout 5m
    cd plugins/fctl && golangci-lint run --fix --timeout 5m

# Tidy go modules
tidy:
    go mod tidy
    cd plugins/fctl && go mod tidy

# Generate code
generate:
    go generate ./...

# Run tests with race detector and coverage
tests:
    go test -race -covermode atomic -tags it ./...
    cd plugins/fctl && go test -race -covermode atomic ./...

# Regenerate the committed fctl Reconciliation plugin operation inventory from
# openapi.yaml. Review the diff: a change here is a change to the plugin's
# source of truth.
fctl-audit:
    cd plugins/fctl && go run ./cmd/specaudit -spec ../../openapi.yaml -out .

# Fail if the committed inventory artefacts no longer match openapi.yaml.
fctl-audit-check:
    cd plugins/fctl && go run ./cmd/specaudit -spec ../../openapi.yaml -out . -check

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
