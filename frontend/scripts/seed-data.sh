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


# ── Card holds, for the stale_holds demo rules ──────────────────────────────
# A hold is one account carrying reserved funds plus a deadline in its metadata.
# The deadline keys must be DECLARED datetime and INDEXED before they can be
# filtered on — declaring a type does not by itself make a field queryable, and
# the stale_holds rule is rejected at create time if the index isn't there.
echo
echo "Seeding card holds (stale_holds demo)"
for key in hold_expires_at hold_created_at; do
  lc ledgers set-metadata-type --ledger "$LEDGER" --target account --key "$key" --type datetime >/dev/null 2>&1 \
    && echo "  + declared account.$key as datetime" || echo "  · account.$key already declared"
  lc indexes create --ledger "$LEDGER" --type metadata --target account --key "$key" >/dev/null 2>&1 \
    && echo "  + indexed account.$key" || echo "  · account.$key already indexed"
done

# iso <bsd-offset> <gnu-offset> — an RFC3339 instant relative to now, on either date(1).
iso() { date -u -v"$1" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d "$2" +%Y-%m-%dT%H:%M:%SZ; }

# meta <address> <key=value>... — set account metadata, tolerating a re-run.
meta() {
  local address="$1"; shift
  local args=()
  for kv in "$@"; do args+=(--metadata "$kv"); done
  lc accounts set-metadata "$address" --ledger "$LEDGER" "${args[@]}" >/dev/null 2>&1 && echo "  + $address $*"
}

# Three holds of 250.00 each, dated to produce a known verdict against a rule
# with a 48h fallback and a 24h warning window:
tx "world,holds:enfuce:auth-8801,25000,$ASSET" "seed:hold:auth-8801"   # expired 6h ago  => STALE
tx "world,holds:enfuce:auth-8802,25000,$ASSET" "seed:hold:auth-8802"   # expires in 3h   => APPROACHING
tx "world,holds:enfuce:auth-8803,25000,$ASSET" "seed:hold:auth-8803"   # expires in 5d   => healthy
tx "world,holds:enfuce:auth-8804,25000,$ASSET" "seed:hold:auth-8804"   # no expiry, placed 50h ago => STALE via the 48h fallback
tx "world,holds:enfuce:auth-8805,25000,$ASSET" "seed:hold:auth-8805"   # released below, keeps a long-passed expiry => ignored
tx "holds:enfuce:auth-8805,world,25000,$ASSET" "seed:hold:auth-8805-release"

# enfuce_auth_id / card_id are identity labels: the rule copies them onto each
# alert so it names the authorisation, not just an address. Labels are read off
# the account, never filtered on, so they need no declared type and no index.
meta holds:enfuce:auth-8801 "hold_created_at=$(iso -30H '-30 hours')" "hold_expires_at=$(iso -6H '-6 hours')"  "enfuce_auth_id=AUTH-8801" "card_id=card_42"
meta holds:enfuce:auth-8802 "hold_created_at=$(iso -21H '-21 hours')" "hold_expires_at=$(iso +3H '+3 hours')"  "enfuce_auth_id=AUTH-8802" "card_id=card_42"
meta holds:enfuce:auth-8803 "hold_created_at=$(iso -2H '-2 hours')"   "hold_expires_at=$(iso +5d '+5 days')"   "enfuce_auth_id=AUTH-8803" "card_id=card_17"
meta holds:enfuce:auth-8804 "hold_created_at=$(iso -50H '-50 hours')"                                          "enfuce_auth_id=AUTH-8804" "card_id=card_17"
meta holds:enfuce:auth-8805 "hold_created_at=$(iso -20d '-20 days')"  "hold_expires_at=$(iso -19d '-19 days')" "enfuce_auth_id=AUTH-8805" "card_id=card_42"

echo
echo "✓ Data ledger seeded. Now create the rules:"
echo "    RECON_API_URL=http://localhost:8081 LEDGER=$LEDGER ASSET=$ASSET node $DIR/seed-demo.mjs"
