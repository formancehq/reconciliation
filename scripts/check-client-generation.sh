#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
source_client="$repo_root/pkg/client"
temporary_root="$(mktemp -d)"
trap 'rm -rf "$temporary_root"' EXIT
candidate="$temporary_root/client"

mkdir -p "$candidate"
if [[ -d "$source_client/.speakeasy" ]]; then
  mkdir -p "$candidate/.speakeasy"
  for generator_input in gen.yaml gen.lock; do
    if [[ -f "$source_client/.speakeasy/$generator_input" ]]; then
      cp "$source_client/.speakeasy/$generator_input" "$candidate/.speakeasy/$generator_input"
    fi
  done
fi
speakeasy generate sdk -s "$repo_root/openapi.yaml" -o "$candidate" -l go -y
(
  cd "$repo_root"
  go run ./internal/generateclient -client "$candidate"
)
(
  cd "$candidate"
  go mod tidy
)

diff -ru \
  --exclude=.claude-flow \
  --exclude=client_contract_test.go \
  --exclude=logs \
  "$source_client" "$candidate"
