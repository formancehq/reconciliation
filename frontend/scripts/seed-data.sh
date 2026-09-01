#!/usr/bin/env bash
# Seed the demo DATA ledger that the reconciliation demo rules run against.
#
# The reconciliation module only writes its own `_recon` control ledger; the
# business data it reconciles lives on a separate ledger (normally populated by
# a real module such as mortgage). On a fresh local cluster that data does not
# exist, so this script builds a small, self-contained `mortgage` loan book on
# the local Ledger V3 whose balances produce the exact PASS/FAIL mix the rules
# in seed-demo.mjs expect.
#
# Run this FIRST, then `node scripts/seed-demo.mjs` to create the rules,
# evaluate them, and drive the alert lifecycle.
#
# Idempotent: each transaction carries a stable --reference, so re-running is a
# no-op (the ledger rejects a duplicate reference). For a full clean slate,
# reset the whole cluster with `ledger-local/start.sh --fresh` in the ledger repo.
#
# Env:
#   LEDGER      data ledger name              (default: mortgage)
#   ASSET       asset code                    (default: USD/2)
#   SERVER      ledger gRPC address           (default: 127.0.0.1:8888)
#   LEDGERCTL   path to the ledgerctl binary  (default: the sibling ledger repo build)
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LEDGER="${LEDGER:-mortgage}"
ASSET="${ASSET:-USD/2}"
SERVER="${SERVER:-127.0.0.1:8888}"
# Default to the ledgerctl built in the sibling ledger checkout (../../../ledger).
LEDGERCTL="${LEDGERCTL:-$(cd "$DIR/../../.." 2>/dev/null && pwd)/ledger/build/ledgerctl}"

if [[ ! -x "$LEDGERCTL" ]]; then
  echo "✗ ledgerctl not found at: $LEDGERCTL" >&2
  echo "  Build it (cd <ledger repo> && go build -o build/ledgerctl ./cmd/ledgerctl)" >&2
  echo "  or set LEDGERCTL=/path/to/ledgerctl" >&2
  exit 1
fi

lc() { "$LEDGERCTL" --insecure --server "$SERVER" "$@"; }

# tx <posting> <reference> — create a transaction, tolerating a duplicate
# reference (an already-seeded run) so the script is idempotent.
tx() {
  local out
  if out=$(lc transactions create --ledger "$LEDGER" --reference "$2" --posting "$1" 2>&1); then
    echo "  + $1"
  elif printf '%s' "$out" | grep -qiE "reference|already|conflict|exists"; then
    echo "  · already seeded ($2)"
  else
    echo "  ✗ $1" >&2; printf '    %s\n' "$out" >&2; return 1
  fi
}

echo "Seeding data ledger \"$LEDGER\" (asset \"$ASSET\") on $SERVER"
lc ledgers create --name "$LEDGER" >/dev/null 2>&1 || true

# Loan 201 book — balances chosen to make each demo rule land on a known verdict:
tx "world,loan:201:principal,25000000,$ASSET"        "seed:loan-201:principal"    # >20M cap => exposure FAIL; >=0 => floor PASS
tx "world,loan:201:company:tranche-a,3000000,$ASSET" "seed:loan-201:company"      # <5M floor => company-floor FAIL
tx "world,loan:201:investor:fund-x,22000000,$ASSET"  "seed:loan-201:investor"     # != company 3M => parity FAIL
tx "world,loan:201:repaid:2026-q1,5000000,$ASSET"    "seed:loan-201:repaid"       # != principal 25M (tol 100) => FAIL
tx "world,loan:201:interest:accrued,150000,$ASSET"   "seed:loan-201:int-accrue"   # interest accrues...
tx "loan:201:interest:accrued,world,150000,$ASSET"   "seed:loan-201:int-clear"    # ...then clears => interest:* nets to 0 => PASS

echo
echo "✓ Data ledger seeded. Now create the rules:"
echo "    RECON_API_URL=http://localhost:8081 LEDGER=$LEDGER ASSET=$ASSET node $DIR/seed-demo.mjs"
