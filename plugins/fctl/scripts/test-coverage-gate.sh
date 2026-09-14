#!/usr/bin/env bash
set -euo pipefail

readonly script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
readonly checker="$script_dir/check-coverage.sh"

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/reconciliation-coverage-test.XXXXXXXX")"
readonly work_dir
trap 'rm -rf -- "$work_dir"' EXIT

profile() {
  local output="$1"
  local covered="$2"
  local total="$3"
  {
    printf 'mode: atomic\n'
    printf 'example.go:1.1,1.2 %s 1\n' "$covered"
    printf 'example.go:2.1,2.2 %s 0\n' "$((total - covered))"
  } >"$output"
}

profile "$work_dir/exact.cover" 8 10
"$checker" "$work_dir/exact.cover" 80 >"$work_dir/exact.out"
grep -Fx 'application coverage: 80.0% (minimum 80.0%)' "$work_dir/exact.out" >/dev/null

profile "$work_dir/below.cover" 7 10
if "$checker" "$work_dir/below.cover" 80 >"$work_dir/below.out" 2>"$work_dir/below.err"; then
  printf 'coverage below the minimum unexpectedly passed\n' >&2
  exit 1
fi
grep -Fx 'application coverage 70.0% is below minimum 80.0%' "$work_dir/below.err" >/dev/null

printf 'mode: atomic\nmalformed\n' >"$work_dir/malformed.cover"
if "$checker" "$work_dir/malformed.cover" 80 >"$work_dir/malformed.out" 2>"$work_dir/malformed.err"; then
  printf 'malformed coverage profile unexpectedly passed\n' >&2
  exit 1
fi
grep -F 'malformed coverage row' "$work_dir/malformed.err" >/dev/null

printf 'coverage gate contract: ok\n'
