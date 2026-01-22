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
generate:
    go generate ./...

# Run tests with race detector and coverage
tests:
    go test -race -covermode atomic -tags it ./...

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
