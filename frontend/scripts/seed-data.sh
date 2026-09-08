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
# Idempotent by convergence: balances are moved TO a target rather than minted
# as a fixed delta, so re-running settles the same book whatever the ledger held
# before. That matters because the demo verdicts are absolute amounts — a book
# minted twice put the company tranche at 6M against its 5M floor, turning an
# expected FAIL into a legitimate PASS. Metadata writes are naturally idempotent.
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

# balance <address> — the account's current balance for $ASSET, 0 when the
# account or the asset is absent. Parsed from ledgerctl's --json with awk so the
# script keeps its only dependencies as ledgerctl and a POSIX shell. The `|| true`
# matters under `set -euo pipefail`: an absent account makes ledgerctl exit
# non-zero, and that must read as "holds nothing", not abort the seed.
balance() {
  local out
  out=$( { lc accounts show "$1" --ledger "$LEDGER" --json 2>/dev/null || true; } | awk -v want="$ASSET" '
    /"asset"/   { a = $0; sub(/.*"asset": "/, "", a); sub(/".*/, "", a) }
    /"balance"/ { if (a == want) { b = $0; sub(/.*"balance": "/, "", b); sub(/".*/, "", b); print b; exit } }
  ' || true)
  printf '%s' "${out:-0}"
}

# at <address> <target> — move the account to exactly <target>, minting the
# shortfall from world or burning the excess back to it. Converging rather than
# minting a delta is what makes a re-run safe on a ledger that has already been
# seeded (see the header).
at() {
  local address="$1" target="$2" cur delta posting out
  cur=$(balance "$address")
  delta=$(( target - cur ))
  if (( delta == 0 )); then
    echo "  · $address already at $target"
    return
  fi
  if (( delta > 0 )); then
    posting="world,$address,$delta,$ASSET"
  else
    posting="$address,world,$(( -delta )),$ASSET"
  fi
  if out=$(lc transactions create --ledger "$LEDGER" --posting "$posting" 2>&1); then
    echo "  + $address $cur → $target"
  else
    echo "  ✗ $address → $target" >&2; printf '    %s\n' "$out" >&2; return 1
  fi
}

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
at loan:201:principal         25000000   # >20M cap => exposure FAIL; >=0 => floor PASS
at loan:201:company:tranche-a  3000000   # <5M floor => company-floor FAIL
at loan:201:investor:fund-x   22000000   # != company 3M => parity FAIL
at loan:201:repaid:2026-q1     5000000   # != principal 25M (tol 100) => FAIL
at loan:201:interest:accrued          0  # accrued then cleared => interest:* nets to 0 => PASS


# ── Holds, for the stale_holds demo rules ───────────────────────────────────
# A hold is one account carrying reserved funds plus a deadline in its metadata.
# The deadline keys must be DECLARED datetime and INDEXED before they can be
# filtered on — declaring a type does not by itself make a field queryable, and
# the stale_holds rule is rejected at create time if the index isn't there.
echo
echo "Seeding holds (stale_holds demo)"
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

# Five holds of 250.00 each (one released below), dated to produce a known
# verdict against a rule
# with a 48h fallback and a 24h warning window:
tx "world,holds:h-8801,25000,$ASSET" "seed:hold:h-8801"   # expired 6h ago  => STALE
tx "world,holds:h-8802,25000,$ASSET" "seed:hold:h-8802"   # expires in 3h   => APPROACHING
tx "world,holds:h-8803,25000,$ASSET" "seed:hold:h-8803"   # expires in 5d   => healthy
tx "world,holds:h-8804,25000,$ASSET" "seed:hold:h-8804"   # no expiry, placed 50h ago => STALE via the 48h fallback
tx "world,holds:h-8805,25000,$ASSET" "seed:hold:h-8805"   # released below, keeps a long-passed expiry => ignored
tx "holds:h-8805,world,25000,$ASSET" "seed:hold:h-8805-release"

# hold_reference / customer_id are ordinary account metadata: the rule does not
# read them, and they need no declared type or index. They are here because real
# hold accounts carry business identifiers, and because narrowing a rule to a
# subset (one desk, one book) is done with a metadata match on keys like these.
meta holds:h-8801 "hold_created_at=$(iso -30H '-30 hours')" "hold_expires_at=$(iso -6H '-6 hours')"  "hold_reference=H-8801" "customer_id=cust_42"
meta holds:h-8802 "hold_created_at=$(iso -21H '-21 hours')" "hold_expires_at=$(iso +3H '+3 hours')"  "hold_reference=H-8802" "customer_id=cust_42"
meta holds:h-8803 "hold_created_at=$(iso -2H '-2 hours')"   "hold_expires_at=$(iso +5d '+5 days')"   "hold_reference=H-8803" "customer_id=cust_17"
meta holds:h-8804 "hold_created_at=$(iso -50H '-50 hours')"                                          "hold_reference=H-8804" "customer_id=cust_17"
meta holds:h-8805 "hold_created_at=$(iso -20d '-20 days')"  "hold_expires_at=$(iso -19d '-19 days')" "hold_reference=H-8805" "customer_id=cust_42"

echo
echo "✓ Data ledger seeded. Now create the rules:"
echo "    RECON_API_URL=http://localhost:8081 LEDGER=$LEDGER ASSET=$ASSET node $DIR/seed-demo.mjs"
