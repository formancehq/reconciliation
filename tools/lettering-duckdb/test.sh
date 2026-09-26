#!/bin/sh
# Tests the checks and the query pack on testdata/, the worked example of
# docs/technical/transaction-level-results.md §10. Needs the DuckDB CLI, gzip and sed.
set -eu

here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
lettering=$here/lettering
data=$here/testdata/rule=psp-vs-billing
day23=$data/day=2026-09-23/run=r-20260924T000003Z
day24=$data/day=2026-09-24/run=r-20260925T000004Z
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0

pass() { printf 'ok    %s\n' "$1"; }
fail() {
    printf 'FAIL  %s\n' "$1"
    sed 's/^/      /' "$work/out"
    failures=$((failures + 1))
}

# expect_ok NAME COMMAND...: the command succeeds.
expect_ok() {
    name=$1
    shift
    if "$@" > "$work/out" 2>&1; then pass "$name"; else fail "$name"; fi
}

# expect_output NAME EXPECTED COMMAND...: the command succeeds and prints EXPECTED.
expect_output() {
    name=$1
    expected=$2
    shift 2
    if "$@" > "$work/out" 2>&1 && [ "$(cat "$work/out")" = "$expected" ]; then pass "$name"; else fail "$name"; fi
}

# expect_violation NAME RULE COMMAND...: the command fails and reports RULE.
expect_violation() {
    name=$1
    rule=$2
    shift 2
    if LETTERING_MODE=csv "$@" > "$work/out" 2>&1; then
        fail "$name (passed)"
    elif grep -q "^$rule," "$work/out"; then
        pass "$name"
    else
        fail "$name ($rule not reported)"
    fi
}

# fresh_run: a copy of the worked day's run in $work/run.
fresh_run() {
    rm -rf "$work/run"
    mkdir -p "$work/run"
    cp "$day24"/* "$work/run/"
}

# sha FILE: the file's SHA-256, computed by DuckDB so that no other tool is needed.
sha() {
    printf "SELECT sha256(content) FROM read_blob('%s');\n" "$1" | "${DUCKDB:-duckdb}" -list -noheader
}

# edit FILE SED-SCRIPT: edits a file in place, gzipped or not.
edit() {
    case $1 in
    *.gz) gzip -dc "$1" | sed "$2" | gzip -n > "$1.tmp" ;;
    *) sed "$2" "$1" > "$1.tmp" ;;
    esac
    mv "$1.tmp" "$1"
}

echo "check"
expect_ok "the worked day is sound" "$lettering" check "$day24"
expect_ok "it chains onto the previous day" "$lettering" check-chain "$day23" "$day24"

fresh_run
edit "$work/run/carried.ndjson.gz" '/"ref":"PAY-39"/d'
expect_violation "a carried row removed" carried_vs_flow "$lettering" check "$work/run"

fresh_run
edit "$work/run/manifest.json" 's/"earlierDay":false,"amount":"80000"/"earlierDay":false,"amount":"80001"/'
expect_violation "a bridge line altered" bridge_line "$lettering" check "$work/run"

fresh_run
edit "$work/run/breaks.ndjson.gz" 's/"amount":"120000","side":"product","hold":"main:hold:invoice:INV-3"/"amount":"-120000","side":"product","hold":"main:hold:invoice:INV-3"/'
expect_violation "a stock break with the wrong sign" break_amount "$lettering" check "$work/run"

fresh_run
edit "$work/run/flow.ndjson.gz" '/"ref":"PAY-42"/p'
expect_violation "a duplicate key" unique_key "$lettering" check "$work/run"

fresh_run
edit "$work/run/manifest.json" 's/"matched":3/"matched":4/'
expect_violation "a manifest count off by one" counts_flow "$lettering" check "$work/run"

fresh_run
edit "$work/run/stock.ndjson.gz" '1{h;d;};2{G;}'
expect_violation "two stock rows out of order" row_order "$lettering" check "$work/run"

fresh_run
edit "$work/run/breaks.ndjson.gz" '/"ref":"PAY-44"/d'
expect_violation "an open break missing from the breaks file" breaks_vs_rows "$lettering" check "$work/run"

fresh_run
edit "$work/run/flow.ndjson.gz" '/"ref":"PAY-42"/s/"productAmount":"100000"/"productAmount":"90000"/'
expect_violation "a product amount that its applications contradict" row_amounts "$lettering" check "$work/run"

fresh_run
edit "$work/run/stock.ndjson.gz" '/INV-14/s/"class":"wrong_sign","outcome":"break"/"class":"open","outcome":"ok"/'
expect_violation "a wrong-sign hold classed open" row_outcome "$lettering" check "$work/run"

fresh_run
edit "$work/run/manifest.json" 's/"txTo":902750/"txTo":899000/'
expect_violation "an application outside the window counted in the bridge" bridge_residual "$lettering" check "$work/run"

fresh_run
edit "$work/run/manifest.json" 's/"manifestSha256":"[0-9a-f]*"/"manifestSha256":"00"/'
expect_violation "a broken chain" previous_run "$lettering" check-chain "$day23" "$work/run"

# Legitimate runs the checks must accept.
fresh_run
gzip -dc "$work/run/flow.ndjson.gz" | sed -n '1,4p' | gzip -n > "$work/run/flow-00000.ndjson.gz"
gzip -dc "$work/run/flow.ndjson.gz" | sed -n '5,$p' | gzip -n > "$work/run/flow-00001.ndjson.gz"
rm "$work/run/flow.ndjson.gz"
part0=$(sha "$work/run/flow-00000.ndjson.gz")
part1=$(sha "$work/run/flow-00001.ndjson.gz")
edit "$work/run/manifest.json" "s/{\"name\":\"flow.ndjson.gz\",\"rows\":8,\"sha256\":\"[0-9a-f]*\"/{\"name\":\"flow-00000.ndjson.gz\",\"part\":0,\"rows\":4,\"sha256\":\"$part0\",\"expiresAt\":\"2026-12-23\"},{\"name\":\"flow-00001.ndjson.gz\",\"part\":1,\"rows\":4,\"sha256\":\"$part1\"/"
expect_ok "a flow file in two parts" "$lettering" check "$work/run"

fresh_run
printf '%s\n' '{"schemaVersion":"lettering/1","days":[]}' > "$work/run/period.json"
period=$(sha "$work/run/period.json")
edit "$work/run/manifest.json" "s/}\],\"anchor\"/},{\"name\":\"period.json\",\"rows\":0,\"sha256\":\"$period\",\"expiresAt\":\"2027-10-24\"}],\"anchor\"/"
expect_ok "a period's last run, with its period.json" "$lettering" check "$work/run"

rm -rf "$work/run" && mkdir -p "$work/run"
printf '%s\n' '{"schemaVersion":"lettering/1","runId":"r-20260925T060000Z","period":{"type":"daily","day":"2026-09-24"},"verdict":"incomplete","incomplete":{"reason":"short_log_range","detail":"test"}}' > "$work/run/manifest.json"
expect_ok "an incomplete run with its manifest only" "$lettering" check "$work/run"
cp "$day24/flow.ndjson.gz" "$work/run/"
if "$lettering" check "$work/run" > "$work/out" 2>&1; then fail "an incomplete run with a data file (passed)"; else pass "an incomplete run with a data file"; fi

echo "queries"
bridge='asset,class,outcome,earlier_day,amount,payments
EUR/2,unapplied_payment,pending,false,80000,1
EUR/2,under_applied,break,false,5000,1
EUR/2,applied_before_final,pending,false,-30000,1
EUR/2,matched,ok,true,-70000,1'
expect_output "bridge adds up to the net of -15000" "$bridge" env LETTERING_MODE=csv "$lettering" query bridge "$data"
expect_output "open items close at 105000 over 4 rows" "2026-09-24,EUR/2,breaks,120000,-15000,0,105000,4,55000" \
    sh -c "LETTERING_MODE=csv '$lettering' query open-items '$data' | grep '^2026-09-24,'"
expect_output "psp-events lists only the day's 10 events" 11 \
    sh -c "LETTERING_MODE=csv '$lettering' query psp-events '$data' | wc -l | tr -d ' '"
expect_output "applications lists only the day's 6 applications" 7 \
    sh -c "LETTERING_MODE=csv '$lettering' query applications '$data' | wc -l | tr -d ' '"
for query in "$here"/queries/*.sql; do
    name=$(basename "$query" .sql)
    expect_ok "query $name runs" "$lettering" query "$name" "$data" id=INV-12
done

# A replay's earlier run and a later incomplete retry of the same day: neither counts.
cp -R "$data" "$work/rule"
earlier=$work/rule/day=2026-09-24/run=r-20260925T000001Z
mkdir -p "$earlier" "$work/rule/day=2026-09-24/run=r-20260925T060000Z"
cp "$day24"/* "$earlier/"
edit "$earlier/flow.ndjson.gz" '/"ref":"PAY-45"/d'
cp "$work/run/manifest.json" "$work/rule/day=2026-09-24/run=r-20260925T060000Z/manifest.json"
expect_output "the current run is the latest complete one" "$bridge" env LETTERING_MODE=csv "$lettering" query bridge "$work/rule"

echo
if [ "$failures" -ne 0 ]; then
    echo "$failures test(s) failed"
    exit 1
fi
echo "all tests passed"
