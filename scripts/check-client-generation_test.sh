#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

mkdir -p "$fixture/repository/scripts" "$fixture/repository/pkg/client" "$fixture/fresh" "$fixture/bin"
cp "$repo_root/scripts/check-client-generation.sh" "$fixture/repository/scripts/"
printf 'fresh\n' >"$fixture/repository/pkg/client/generated.go"
printf 'fresh\n' >"$fixture/fresh/generated.go"

cat >"$fixture/bin/speakeasy" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
output=""
while (($#)); do
  if [[ "$1" == "-o" ]]; then
    output="$2"
    break
  fi
  shift
done
[[ -n "$output" ]]
mkdir -p "$output"
cp -R "$FRESH_CLIENT/." "$output/"
EOF
cat >"$fixture/bin/go" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$fixture/bin/speakeasy" "$fixture/bin/go"

PATH="$fixture/bin:$PATH" FRESH_CLIENT="$fixture/fresh" \
  "$fixture/repository/scripts/check-client-generation.sh"

printf 'obsolete\n' >"$fixture/repository/pkg/client/obsolete.go"
if PATH="$fixture/bin:$PATH" FRESH_CLIENT="$fixture/fresh" \
  "$fixture/repository/scripts/check-client-generation.sh" >/dev/null 2>&1; then
  echo "generation check retained an obsolete file from the committed client" >&2
  exit 1
fi
