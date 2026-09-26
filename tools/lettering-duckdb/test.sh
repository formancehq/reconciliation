#!/bin/sh
# Tests the checks and the query pack on testdata/. Needs the DuckDB CLI, gzip, sed, sort and comm.
#
# 1. Every run of the test data passes `check`, and every run chains onto the run its manifest names.
# 2. Every query returns the results testdata/generate.py computed without DuckDB.
# 3. Every rule of check.sql and check-chain.sql fires on at least one corrupted copy (a rule
#    name always contains an underscore, which tells it from the other literals of the SQL).
# 4. Legitimate variants pass, and awkward inputs (paths, quotes, unknown fields) are handled.
set -eu

here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
lettering=$here/lettering
data=$here/testdata
worked=$data/rule=psp-vs-billing
day23=$worked/day=2026-09-23/run=r-20260924T000003Z
day24=$worked/day=2026-09-24/run=r-20260925T000004Z
qa=$data/rule=qa-scenarios
qa1=$qa/day=2026-10-01/run=r-20261002T000004Z
qa2=$qa/day=2026-10-02/run=r-20261003T000004Z
qa3=$qa/day=2026-10-03/run=r-20261004T000004Z
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
failures=0
: > "$work/fired"

pass() { printf 'ok    %s\n' "$1"; }
fail() {
    printf 'FAIL  %s\n' "$1"
    sed 's/^/      /' "$work/out" | head -40
    failures=$((failures + 1))
}

# expect_ok NAME COMMAND...: the command succeeds.
expect_ok() {
    name=$1
    shift
    if "$@" > "$work/out" 2>&1; then pass "$name"; else fail "$name"; fi
}

# expect_fail NAME COMMAND...: the command fails.
expect_fail() {
    name=$1
    shift
    if "$@" > "$work/out" 2>&1; then fail "$name (passed)"; else pass "$name"; fi
}

# expect_violation NAME RULE COMMAND...: the command fails and reports RULE.
expect_violation() {
    name=$1
    rule=$2
    shift 2
    if LETTERING_MODE=csv "$@" > "$work/out" 2>&1; then
        fail "$name (passed)"
    elif grep -q "^$rule,\|^violation: $rule:" "$work/out"; then
        echo "$rule" >> "$work/fired"
        pass "$name: $rule"
    else
        fail "$name ($rule not reported)"
    fi
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

# fresh RUN-DIR: a copy of a run in $work/run.
fresh() {
    rm -rf "$work/run"
    mkdir -p "$work/run"
    cp "$1"/* "$work/run/"
}

# reseal FILE: writes a data file's new SHA-256 into its manifest, after a legitimate change.
reseal() {
    name=$(basename "$1")
    edit "$(dirname "$1")/manifest.json" "s/\"name\":\"$name\",\"rows\":\([0-9]*\),\"sha256\":\"[0-9a-f]*\"/\"name\":\"$name\",\"rows\":\1,\"sha256\":\"$(sha "$1")\"/"
}

echo "every run is sound and chains"
for manifest in $(find "$data" -name manifest.json | sort); do
    run=$(dirname "$manifest")
    [ "$run" = "$day23" ] && continue # partial on purpose: only what check-chain reads
    expect_ok "check ${run#"$data"/}" "$lettering" check "$run"
    previous=$(sed -n 's/.*"previousRun":{"runId":"\([^"]*\)","day":"\([^"]*\)".*/day=\2\/run=\1/p' "$manifest")
    if [ -n "$previous" ]; then
        expect_ok "check-chain ${run#"$data"/}" "$lettering" check-chain "${run%/day=*}/$previous" "$run"
    fi
done

echo "every query returns what generate.py computed"
for expected in "$data"/expected/*/*.csv; do
    rule=$(basename "$(dirname "$expected")")
    base=$(basename "$expected" .csv)
    name=${base%%_*}
    set -- # a file named <query>_<variable>=<value>.csv is that query run with that variable
    if [ "$name" != "$base" ]; then set -- "${base#*_}"; fi
    if LETTERING_MODE=csv "$lettering" query "$name" "$data/$rule" "$@" > "$work/actual" 2> "$work/out" \
        && [ "$(head -1 "$work/actual")" = "$(head -1 "$expected")" ] \
        && [ "$(sed 1d "$work/actual" | sort)" = "$(sed 1d "$expected" | sort)" ]; then
        pass "$rule $base"
    else
        { cat "$work/out"; echo "--- actual"; cat "$work/actual"; echo "--- expected"; cat "$expected"; } > "$work/diff"
        mv "$work/diff" "$work/out"
        fail "$rule $base"
    fi
done
worked_bridge='asset,class,outcome,earlier_day,amount,payments
EUR/2,applied_before_final,pending,false,-30000,1
EUR/2,matched,ok,true,-70000,1
EUR/2,under_applied,break,false,5000,1
EUR/2,unapplied_payment,pending,false,80000,1'
if [ "$(LETTERING_MODE=csv "$lettering" query bridge "$worked" | sort)" = "$(printf '%s\n' "$worked_bridge" | sort)" ]; then
    pass "the worked day's bridge adds up to -15000"
else
    : > "$work/out"
    fail "the worked day's bridge adds up to -15000"
fi
for query in "$here"/queries/*.sql; do
    expect_ok "query $(basename "$query" .sql) runs on every rule" \
        sh -c "for r in '$data'/rule=*; do '$lettering' query $(basename "$query" .sql) \"\$r\" \$([ $(basename "$query" .sql) = business-id ] && echo id=INV-S04) > /dev/null || exit 1; done"
done

echo "every rule fires on a corrupted copy"
m=manifest.json
fresh "$day24"; edit "$work/run/$m" 's/"schemaVersion":"lettering\/1","engine"/"schemaVersion":"lettering\/2","engine"/'
expect_violation "schema version" schema_version "$lettering" check "$work/run"
fresh "$day24"; rm "$work/run/stock.ndjson.gz"
expect_violation "a data file missing" file_missing "$lettering" check "$work/run"
fresh "$day24"; mv "$work/run/flow.ndjson.gz" "$work/run/flow-00000.ndjson.gz"
expect_violation "a data file under another name" file_unlisted "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"name":"breaks.ndjson.gz","rows":5,"sha256":"[0-9a-f]*"/"name":"breaks.ndjson.gz","rows":5,"sha256":"00"/'
expect_violation "a file's SHA-256" file_sha256 "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"name":"stock.ndjson.gz","rows":9/"name":"stock.ndjson.gz","rows":8/'
expect_violation "a file's row count" file_rows "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"matched":3/"matched":4/'
expect_violation "a flow count" counts_flow "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"flowOutcome":{"ok":4/"flowOutcome":{"ok":5/'
expect_violation "a flow outcome count" counts_flow_outcome "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"product":{"open":4,"wrong_sign"/"product":{"open":5,"wrong_sign"/'
expect_violation "a stock count" counts_stock "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"resolved":1,"accepted":0/"resolved":1,"accepted":1/'
expect_violation "a break count" counts_breaks "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"unclassified":{"psp":1,"product":0}/"unclassified":{"psp":2,"product":0}/'
expect_violation "an unclassified count" counts_unclassified "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"net":"-15000"/"net":"-15001"/'
expect_violation "the net" bridge_net "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"psp":{"amount":"400000"/"psp":{"amount":"400001"/'
expect_violation "the PSP total" bridge_totals "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"txTo":902750/"txTo":899000/'
expect_violation "an application outside the window" bridge_residual "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"letteredOther":"0","open":"245000"/"letteredOther":"1","open":"245000"/'
expect_violation "the product total against the books" bridge_product_vs_books "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"earlierDay":false,"amount":"80000"/"earlierDay":false,"amount":"80001"/'
expect_violation "a bridge line" bridge_line "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"carriedOutside":\[{"class":"unapplied_payment","outcome":"break","amount":"50000"/"carriedOutside":[{"class":"unapplied_payment","outcome":"break","amount":"50001"/'
expect_violation "a carried line" bridge_carried_outside "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"flowGross":"55000"/"flowGross":"55001"/'
expect_violation "the gross" bridge_gross "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"state":"payin.refunded","amount":"20000","count":1/"state":"payin.refunded","amount":"20001","count":1/'
expect_violation "the statement's unclassified total" statement_unclassified "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/carried.ndjson.gz" '/"ref":"PAY-39"/d'
expect_violation "a carried row removed" carried_vs_flow "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"open":"105000","count":4/"open":"105001","count":4/'
expect_violation "the open items" suspense_open "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"fromLookups":"0"/"fromLookups":"5"/'
expect_violation "the open items' identity" suspense_identity "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"opened":"230000"/"opened":"230001"/'
expect_violation "a book's continuity" books_continuity "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"open":"245000","count":5/"open":"245000","count":6/'
expect_violation "a book against the stock" books_vs_stock "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/breaks.ndjson.gz" 's/"amount":"120000","side":"product","hold":"main:hold:invoice:INV-3"/"amount":"-120000","side":"product","hold":"main:hold:invoice:INV-3"/'
expect_violation "a stock break with the wrong sign" break_amount "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/breaks.ndjson.gz" '/"lifecycle":"resolved"/s/"outcome":"ok"/"outcome":"break"/'
expect_violation "a resolved break still marked break" break_outcome "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/breaks.ndjson.gz" 's/"priority":2,"lifecycle":"new"/"priority":1,"lifecycle":"new"/'
expect_violation "a break's priority" break_priority "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"ref":"PAY-44","asset":"EUR\/2","amount":"5000"/"ref":"PAY-44","asset":"EUR\/2","amount":"5001"/'
expect_violation "a triage break" triage_break "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"amount":"80000","breakOn":"2026-09-25"/"amount":"80000","breakOn":"2026-09-26"/'
expect_violation "a triage pending item" triage_pending "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/breaks.ndjson.gz" '/"ref":"PAY-44"/d'
expect_violation "an open break missing from the breaks file" breaks_vs_rows "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/flow.ndjson.gz" '/"ref":"PAY-42"/s/"productAmount":"100000"/"productAmount":"90000"/'
expect_violation "a product amount its applications contradict" row_amounts "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/carried.ndjson.gz" '/"ref":"PAY-39"/s/"drift":"50000","firstSeen"/"drift":"50000","impact":"0","firstSeen"/'
expect_violation "a carried row with an impact" row_impact "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/stock.ndjson.gz" '/INV-14/s/"class":"wrong_sign","outcome":"break"/"class":"open","outcome":"ok"/'
expect_violation "a wrong-sign hold classed open" row_outcome "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/flow.ndjson.gz" '/"ref":"PAY-42"/p'
expect_violation "a duplicate key" unique_key "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/stock.ndjson.gz" '1{h;d;};2{G;}'
expect_violation "two rows out of order" row_order "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"verdict":"breaks"/"verdict":"reconciled"/'
expect_violation "the verdict" verdict_mismatch "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/flow.ndjson.gz" '/"ref":"PAY-42"/s/"drift":"0"/"drift":"1"/'
expect_violation "a matched row with a drift" row_drift "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/flow.ndjson.gz" '/"ref":"PAY-45"/s/"breakOn":"2026-09-25"/"breakOn":"2026-09-26"/'
expect_violation "a breakOn that is not firstSeen plus grace" row_break_on "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/stock.ndjson.gz" '/INV-9/s/"ageDays":12/"ageDays":13/'
expect_violation "a hold's age" stock_age "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"buckets":{"0-1d":0,"2-7d":2,"8-30d":2,">30d":1}/"buckets":{"0-1d":0,"2-7d":2,"8-30d":3,">30d":1}/'
expect_violation "a book's age buckets" books_buckets "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/breaks.ndjson.gz" '/"ref":"PAY-44"/s/"class":"under_applied"/"class":"over_applied"/'
expect_violation "a break that is not its row" break_vs_row "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"topK":10/"topK":3/'
expect_violation "a triage that lists more than topK" triage_count "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/,{"ref":"PAY-99","class":"applied_before_final"[^}]*}//'
expect_violation "a triage that drops a pending row under topK" triage_count "$lettering" check "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"topK":10/"topK":1/; s/,{"breakId":"[0-9a-f]*","priority":[34][^}]*}//g; s/,{"ref":"PAY-99","class":"applied_before_final"[^}]*}//'
expect_ok "a triage cut at topK, with more breaks and pending rows in the files" "$lettering" check "$work/run"
fresh "$qa2"; edit "$work/run/$m" 's/"txFrom":\([0-9]*\),"txTo"/"txFrom":1,"txTo"/'
expect_violation "a window that does not start at the previous cut" window_start "$lettering" check-chain "$qa1" "$work/run"
fresh "$qa2"; edit "$work/run/flow.ndjson.gz" '/"ref":"S04"/s/"impact":"0"/"impact":"1"/'
expect_violation "a carried drift that moves without impact" carried_drift "$lettering" check-chain "$qa1" "$work/run"
fresh "$qa3"; edit "$work/run/$m" 's/"fromLookups":"5000"/"fromLookups":"0"/'
expect_violation "fromLookups" from_lookups "$lettering" check-chain "$qa2" "$work/run"
fresh "$qa3"; edit "$work/run/breaks.ndjson.gz" '/"ref":"S11"/s/"lifecycle":"persisting"/"lifecycle":"new"/'
expect_violation "a persisting break called new" break_lifecycle "$lettering" check-chain "$qa2" "$work/run"
fresh "$qa2"; edit "$work/run/stock.ndjson.gz" '/INV-S04/s/"lifecycle":"persisting"/"lifecycle":"new"/'
expect_violation "a persisting hold called new" stock_lifecycle "$lettering" check-chain "$qa1" "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"manifestSha256":"[0-9a-f]*"/"manifestSha256":"00"/'
expect_violation "a broken chain" previous_run "$lettering" check-chain "$day23" "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"openPrev":"120000"/"openPrev":"120001"/'
expect_violation "open items that do not pick up" suspense_open_prev "$lettering" check-chain "$day23" "$work/run"
fresh "$day24"; edit "$work/run/$m" 's/"openPrev":"430000"/"openPrev":"430001"/'
expect_violation "a book that does not pick up" books_open_prev "$lettering" check-chain "$day23" "$work/run"
rm -rf "$work/prev" && mkdir -p "$work/prev" && cp "$day23"/* "$work/prev/"
edit "$work/prev/carried.ndjson.gz" '$p;$s/"PAY-40"/"PAY-77"/'
expect_violation "a carried item that vanished" carried_in "$lettering" check-chain "$work/prev" "$day24"
expect_violation "a chain onto an incomplete run" previous_run \
    "$lettering" check-chain "$data/rule=qa-verdicts/day=2026-10-09/run=r-20261010T000004Z" "$data/rule=qa-verdicts/day=2026-10-10/run=r-20261011T000004Z"

sed -n "s/.*SELECT '\([a-z0-9]*_[a-z0-9_]*\)', .*/\1/p" "$here/check.sql" "$here/check-chain.sql" | sort -u > "$work/rules"
sort -u "$work/fired" > "$work/fired.sorted"
if [ -s "$work/rules" ] && comm -23 "$work/rules" "$work/fired.sorted" > "$work/out" && [ ! -s "$work/out" ]; then
    pass "every rule of check.sql and check-chain.sql is exercised ($(wc -l < "$work/rules" | tr -d ' ') rules)"
else
    fail "rules never exercised by a corruption"
fi

echo "legitimate variants and awkward inputs"
fresh "$day24"
gzip -dc "$work/run/flow.ndjson.gz" | sed -n '1,4p' | gzip -n > "$work/run/flow-00000.ndjson.gz"
gzip -dc "$work/run/flow.ndjson.gz" | sed -n '5,$p' | gzip -n > "$work/run/flow-00001.ndjson.gz"
rm "$work/run/flow.ndjson.gz"
edit "$work/run/$m" "s/{\"name\":\"flow.ndjson.gz\",\"rows\":8,\"sha256\":\"[0-9a-f]*\"/{\"name\":\"flow-00000.ndjson.gz\",\"part\":0,\"rows\":4,\"sha256\":\"$(sha "$work/run/flow-00000.ndjson.gz")\",\"expiresAt\":\"2026-12-23\"},{\"name\":\"flow-00001.ndjson.gz\",\"part\":1,\"rows\":4,\"sha256\":\"$(sha "$work/run/flow-00001.ndjson.gz")\"/"
expect_ok "a flow file in two parts" "$lettering" check "$work/run"
fresh "$day24"
edit "$work/run/flow.ndjson.gz" '/"ref":"PAY-42"/s/"drift":"0",/"drift":"0","newField":{"a":1},/'
reseal "$work/run/flow.ndjson.gz"
expect_ok "a row with a field unknown to lettering/1" "$lettering" check "$work/run"
rm -rf "$work/run" && mkdir -p "$work/run"
printf '%s\n' '{"schemaVersion":"lettering/1","runId":"r-20260925T060000Z","period":{"type":"daily","day":"2026-09-24"},"verdict":"incomplete","incomplete":{"reason":"short_log_range","detail":"test"}}' > "$work/incomplete.json"
cp "$work/incomplete.json" "$work/run/manifest.json"
expect_ok "an incomplete run with its manifest only" "$lettering" check "$work/run"
cp "$day24/flow.ndjson.gz" "$work/run/"
expect_fail "an incomplete run with a data file" "$lettering" check "$work/run"

odd="$work/it's a [dir]*?"
mkdir -p "$odd"
cp -R "$worked" "$odd/"
expect_ok "a path with a space, a quote and glob characters" "$lettering" check "$odd/rule=psp-vs-billing/day=2026-09-24/run=r-20260925T000004Z"
expect_ok "a query on that path" "$lettering" query open-breaks "$odd/rule=psp-vs-billing"
expect_ok "a variable value with a quote" "$lettering" query business-id "$data/rule=qa-scenarios" "id=O'Brien"
expect_fail "a variable name that is not an identifier" "$lettering" query bridge "$data/rule=qa-scenarios" "day;DROP=1"
expect_fail "an unknown query" "$lettering" query no-such-query "$data/rule=qa-scenarios"
expect_fail "a query given a run's directory instead of a rule's" "$lettering" query bridge "$day24"
grep -q "pass a rule's directory" "$work/out" && pass "  and it says which directory to pass" || fail "  and it says which directory to pass"
expect_fail "check given a rule's directory instead of a run's" "$lettering" check "$worked"
grep -q "pass a run's directory" "$work/out" && pass "  and it says which directory to pass" || fail "  and it says which directory to pass"
expect_fail "a day that is not a date" "$lettering" query bridge "$data/rule=qa-scenarios" day=2026-13-01
expect_fail "a day with no complete run" "$lettering" query open-breaks "$data/rule=qa-verdicts" day=2026-10-09
grep -q "no complete run for day 2026-10-09" "$work/out" && pass "  and it says so" || fail "  and it says so"
expect_fail "a variable the query does not have" "$lettering" query bridge "$qa" dya=2026-10-02
grep -q "has no variable dya" "$work/out" && pass "  and it names it" || fail "  and it names it"
expect_fail "a required variable left out" "$lettering" query business-id "$qa"
grep -q "needs id=" "$work/out" && pass "  and it names it" || fail "  and it names it"
fresh "$day24"; printf 'not gzip' > "$work/run/stock.ndjson.gz"
status=0; "$lettering" check "$work/run" > "$work/out" 2>&1 || status=$?
[ "$status" = 3 ] && pass "an unreadable file exits 3, not 1" || fail "an unreadable file exits 3, not 1 (exit $status)"
status=0; "$lettering" check "$worked" > "$work/out" 2>&1 || status=$?
[ "$status" = 2 ] && pass "a wrong directory exits 2" || fail "a wrong directory exits 2 (exit $status)"
printf '%s\n' "SELECT true AS Success;" > "$work/noisy.sql"
expect_ok "an init file that prints a row, like CREATE SECRET" env LETTERING_INIT="$work/noisy.sql" "$lettering" check "$day24"
if [ "$(LETTERING_INIT="$work/noisy.sql" "$lettering" check "$data/rule=qa-verdicts/day=2026-10-09/run=r-20261010T000004Z")" \
    = "incomplete run (short_log_range): it writes its manifest only, so there is nothing else to check" ]; then
    pass "  and its row reaches neither the parsing nor the output"
else
    : > "$work/out"
    fail "  and its row reaches neither the parsing nor the output"
fi
printf '%s\n' "SET VARIABLE day = '2026-10-03';" > "$work/init.sql"
if [ "$(LETTERING_INIT="$work/init.sql" LETTERING_MODE=csv "$lettering" query bridge "$data/rule=qa-scenarios" | sort)" \
    = "$(sort "$data/expected/rule=qa-scenarios/bridge_day=2026-10-03.csv")" ]; then
    pass "LETTERING_INIT runs first"
else
    : > "$work/out"
    fail "LETTERING_INIT runs first"
fi
if [ "$(LETTERING_MODE=csv "$lettering" query bridge "$data/rule=qa-verdicts" day=2026-10-08)" = "asset,class,outcome,earlier_day,amount,payments" ]; then
    pass "an empty result still has its CSV header"
else
    : > "$work/out"
    fail "an empty result still has its CSV header"
fi

# A replay's earlier run and a later incomplete retry of the same day: neither counts.
cp -R "$worked" "$work/rule"
earlier=$work/rule/day=2026-09-24/run=r-20260925T000001Z
mkdir -p "$earlier" "$work/rule/day=2026-09-24/run=r-20260925T060000Z"
cp "$day24"/* "$earlier/"
edit "$earlier/flow.ndjson.gz" '/"ref":"PAY-45"/d'
cp "$work/incomplete.json" "$work/rule/day=2026-09-24/run=r-20260925T060000Z/manifest.json"
if [ "$(LETTERING_MODE=csv "$lettering" query bridge "$work/rule" | sort)" = "$(printf '%s\n' "$worked_bridge" | sort)" ]; then
    pass "the current run is the latest complete one"
else
    : > "$work/out"
    fail "the current run is the latest complete one"
fi

echo
if [ "$failures" -ne 0 ]; then
    echo "$failures test(s) failed"
    exit 1
fi
echo "all tests passed"
