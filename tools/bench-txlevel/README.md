# bench-txlevel

This tool reproduces the measurements behind [ADR-005](../../docs/prd/adr-005-transaction-level-reconciliation.md).
The results are recorded in
[transaction-level-reconciliation.md §7](../../docs/technical/transaction-level-reconciliation.md#7-measurements).

It is a benchmark, not part of the service. It speaks gRPC to Ledger v3 through recon's vendored
`internal/ledgerpb`, so it only builds from inside this module. It writes millions of transactions
and creates query checkpoints.

**Never point it at a ledger you care about.**

## 1. Start an isolated ledger

Use its own ports and a throwaway data directory, never the daily-driver ledger on `:8888`. Build
it from the ledger checkout you want to measure:

```bash
cd <your ledger checkout> && go build -o /tmp/bench-ledger/ledger-server . && go build -o /tmp/bench-ledger/ledgerctl ./cmd/ledgerctl
```

```bash
/tmp/bench-ledger/ledger-server run --node-id 1 --cluster-id bench --bootstrap --bind-addr 127.0.0.1:17777 --grpc-port 18888 --http-port 19000 --wal-dir /tmp/bench-ledger/wal --data-dir /tmp/bench-ledger/data > /tmp/bench-ledger/server.log 2>&1 &
```

The tool dials `127.0.0.1:18888` by default. Override it with `LEDGER_ADDR`.

## 2. Commands

| Command | What it does |
|---|---|
| `load -ledger L -prefix P -n N [-start S] [-batch 200] [-workers 16] [-drift]` | One `world → {P}{id(i)}` USD/2 transaction per `i`. Ids are pseudo-random 16-hex strings. With `-drift`, each block of 1,000 ids has one missing, one at +1 and one funded twice. |
| `agg -ledger L -prefix P [-cp ID]` | `AggregateVolumes` over the prefix, live or at a checkpoint |
| `scan -ledger L -prefix P [-cp ID] [-page 1000]` | Full `ListAccounts` walk over the cursor. Reports accounts/s and whether rows arrive in key order. |
| `diff -a LA -pa PA -b LB -pb PB [-cp ID] [-seq] [-out f.ndjson.gz]` | Scans both scopes (in parallel, or one after the other with `-seq`), merge-joins them on the key and writes the breaks as gzipped NDJSON |
| `cp-create` / `cp-delete -id ID` | Creates or deletes a query checkpoint through `Apply` |
| `logs -ledger L -prefix P [-from X] [-to Y]` | Streams `ListLogs` over the log-id window `(X, Y]` and folds each account's last `post_commit_volumes`. Run several at once on disjoint ranges to measure parallel reads. |
| `load-mixed [-ledger mixed] [-n 1000000] [-every 10]` | Mixed traffic: one payment (`kind=payment`, `payment_ref`) every N transactions, the rest `kind=internal`. `kind` is declared and indexed |
| `load-lettering [-ledger lett] [-prefix psp:hold:] [-persistence NORMAL] [-n 1000000] [-noise 0]` | Books each payment as lettering: `world → {prefix}{id}`, then `{prefix}{id} → psp:main`, both with `payment_ref = id`. Creates the `payment_ref` metadata index and the address index. NORMAL keeps lettered holds listed at zero, as if purged holds were reachable by address prefix (as EN-2036's head `20a5595d6` makes them) |
| `txs -ledger L -from X -to Y [-kind K] [-exists F [-index]] [-prefix P] [-ranges 8]` | `ListTransactions` over the transaction-id window `(X, Y]`, optionally filtered server-side on `kind == K`, on metadata `F` being present (`-index` creates `F`'s index first), or on a posting address under prefix `P`, split into parallel ranges |
| `retag -ledger L -tx ID -kind K` | Rewrites one transaction's `kind`, to show that a filtered re-read of a past window changes (transaction metadata is mutable; logs are not) |
| `lastlog L` | The ledger's head log id (`GetLedgerStats.log_count`) |
| `rewind [-ledger psp] [-prefix psp:tx:] [-writers 8]` | Proves the rewind: takes an oracle checkpoint at `S`, lists the scope live while writers mutate it, rewinds the listing with the logs `(S, head]`, then compares every row with the checkpoint |
| `probe` | Shows what a lettered (purged) EPHEMERAL hold still exposes: account listing, and transaction lookup by address and by reference |
| `cutprobe` | Resolves a cut `S` from a date, using the per-ledger log-date index |
| `cut-cost [-load] [-ledger cut] [-n 10000000] [-reps 3]` | With `-load`, books `-n` light transactions on a fresh ledger, then creates the log-date and `inserted_at` indexes on that history and times how long each takes to serve. Resolves `S` and `T` for cut-offs with 1k to `-n` entries after them, with an open `date > cut-off` and with a bounded range widened while empty, and checks every answer |
| `rewind-sources [-ledger psp] [-prefix psp:tx:] [-writers 8] [-ranges 8]` | The rewind proof with two window sources side by side: the logs `(S, head]` and the unfiltered transactions `(T, head_tx]`, each read on 1 and on `-ranges` streams. The writers also revert transactions older and newer than the cut and write metadata only. Every rewound row is compared with a checkpoint taken at the cut |
| `fold [-ledger psp] [-prefix psp:tx:] [-source txs\|logs] [-from X] [-to Y] [-ranges 8] [-compare]` | Folds the balance-moving transactions of `(X, Y]` (default: the whole history), from the logs or from the unfiltered transactions. `-compare` checks the forward fold against the live listing (run it with no writes) |
| `crossover [-ledger psp] [-prefix psp:tx:] [-ranges 8] [-sizes …] [-days 100000,1000000] [-since 0.083] [-reps 2]` | Times the two ways to get a day's stock: live listings of growing hex sub-prefixes (the open book), folds of the last `X` transactions, and the decode and merge of a stored stock of `N` rows (gzipped NDJSON, in memory). Prints where forward from the stored stock overtakes list and rewind. Run it with no writes; `-reps 1` right after starting the server gives the cold-cache first pass |
| `load-product [-ledger product] [-n 50000] [-noise 1]` | Books a product ledger as the design doc §2 recommends: invoices opened on `EPHEMERAL` holds with `invoice_no`, 9 in 10 applied with `payment_ref` and `invoice_no` (plus an unkeyed revenue recognition), 1 in 25 refunded with `refund_no`, and `-noise` unkeyed transactions per invoice. Declares and indexes the three keys |
| `product-or [-ledger product] [-from X] [-ranges 8] [-reps 3]` | Reads the window `(X, head]` with each key alone, with the product `Or` of the three written id range first, membership first and as an `Or` of `And`s, and with the three reads merged client-side. Checks that every `Or` returns exactly the union of its terms |
| `silent [-n 100000] [-silent 100] [-keyless-drain 10] [-keyed-other] …` | Books two days of PSP activity on a fresh ledger, including finals with no pending and no payment reference. Prints what the hold continuity and the payment-account book (`psp:main`) each see at the cut, checked against a checkpoint. `-keyed-other` gives payouts and fees a declared `movement_ref` and reads the flow with `Or(payment_ref, movement_ref)` |

## 3. Reproduce the ADR-005 figures

Every figure below comes from `docs/technical/transaction-level-reconciliation.md` §7.

**Keyed scopes, live and at a checkpoint** (§7.1):

```bash
go build -o /tmp/bench-ledger/bench ./tools/bench-txlevel
```

```bash
B=/tmp/bench-ledger/bench; $B load -ledger psp -prefix psp:tx: -n 1000000 -batch 500 && $B load -ledger bank -prefix bank:tx: -n 1000500 -batch 500 -drift
```

```bash
B=/tmp/bench-ledger/bench; $B agg -ledger psp -prefix psp:tx: && $B scan -ledger psp -prefix psp:tx: && $B scan -ledger psp -prefix psp:tx: -page 200 && $B diff -a psp -pa psp:tx: -b bank -pb bank:tx:
```

`cp-create` prints the checkpoint id. Substitute it for `ID`:

```bash
B=/tmp/bench-ledger/bench; $B cp-create && $B agg -ledger psp -prefix psp:tx: -cp ID && $B diff -a psp -pa psp:tx: -b bank -pb bank:tx: -cp ID
```

Add `-seq` to that `diff` on a ledger older than EN-2108 (`7492e7304`): two concurrent reads of one
checkpoint fail there. That is how the "10 min 25 s, sequential" figure was produced.

**Log reads and the rewind proof** (§7.2, §7.4). `logs` reads up to the head when `-to` is omitted.
For parallel ranges, start several `logs` processes on disjoint `-from/-to` windows:

```bash
B=/tmp/bench-ledger/bench; $B logs -ledger psp -prefix psp:tx: && $B rewind -ledger psp -prefix psp:tx:
```

**Logs versus transactions, and metadata mutability** (§7.2). `retag` picks a real payment by
default:

```bash
B=/tmp/bench-ledger/bench; $B load-mixed -n 1000000 -every 10 && $B logs -ledger mixed -prefix psp:payment: && $B txs -from 0 -to 1000000 -ranges 8 && $B txs -from 0 -to 1000000 -kind payment -ranges 8 && $B txs -from 0 -to 1000000 -exists payment_ref -index -ranges 8
```

```bash
B=/tmp/bench-ledger/bench; $B retag && $B txs -from 0 -to 1000000 -kind payment -ranges 8 && $B txs -from 0 -to 1000000 -exists payment_ref -ranges 8
```

**Concurrent readers: choosing K** (§7.7). On a fresh `load-mixed` ledger, sweep the number of
ranges, then measure what the readers cost the writes:

```bash
B=/tmp/bench-ledger/bench; for K in 1 2 4 8 12 16 24 32 64; do for r in 1 2 3; do $B txs -from 0 -to 1000000 -exists payment_ref -ranges $K; $B txs -from 0 -to 1000000 -ranges $K; done; done
```

```bash
B=/tmp/bench-ledger/bench; for K in 0 1 8 16 32 64; do if [ $K -gt 0 ]; then (while [ ! -f /tmp/bench-ledger/stop ]; do $B txs -from 0 -to 1000000 -ranges $K >/dev/null; done) & fi; sleep 1; $B load-mixed -ledger w$K -n 300000; touch /tmp/bench-ledger/stop; wait; rm -f /tmp/bench-ledger/stop; done
```

**Key source: metadata against the hold address** (§7.6). The last command takes about 8 minutes:

```bash
B=/tmp/bench-ledger/bench; $B load-lettering -ledger h100k -n 100000 && $B load-lettering -ledger h1m -n 1000000 && $B txs -ledger h100k -from 198000 -to 200000 -exists payment_ref && $B txs -ledger h100k -from 198000 -to 200000 -prefix psp:hold: && $B txs -ledger h1m -from 1998000 -to 2000000 -exists payment_ref && $B txs -ledger h1m -from 1998000 -to 2000000 -prefix psp:hold:
```

```bash
B=/tmp/bench-ledger/bench; $B txs -ledger h100k -from 180000 -to 200000 -exists payment_ref && $B txs -ledger h100k -from 180000 -to 200000 -prefix psp:hold: && $B txs -ledger h1m -from 1980000 -to 2000000 -exists payment_ref -ranges 8 && $B txs -ledger h1m -from 1980000 -to 2000000 -prefix psp:hold:
```

**Purged holds and the cut** (ADR-005 §2.2, §5):

```bash
B=/tmp/bench-ledger/bench; $B probe && $B cutprobe
```

**Window source for balances, logs or transactions** (§7.8). On the 1M-account `psp` scope loaded
above:

```bash
B=/tmp/bench-ledger/bench; $B rewind-sources -ledger psp -prefix psp:tx: && for K in 1 8; do $B fold -ledger psp -source logs -ranges $K; $B fold -ledger psp -source txs -ranges $K; done && $B fold -ledger psp -source txs -compare && $B fold -ledger psp -source logs -compare
```

**A final with no pending and no key** (§7.9). Each run creates its own ledger:

```bash
B=/tmp/bench-ledger/bench; $B silent -ledger silent-d1 -keyless-drain 0 && $B silent -ledger silent-d2 -keyless-drain 0 -keyed-other && $B silent -ledger silent-d3 -keyless-drain 10
```

**Resolving the cut: bounded or open date filter** (§7.12). About 3 min of load on a fresh ledger:

```bash
B=/tmp/bench-ledger/bench; $B cut-cost -load -ledger cut10m -n 10000000
```

**Daily stock: list and rewind, or forward** (§7.10). On the `psp` scope, the first pass right after
starting the server, then a warm pass:

```bash
B=/tmp/bench-ledger/bench; $B crossover -ledger psp -prefix psp:tx: -reps 1 && $B crossover -ledger psp -prefix psp:tx:
```

**The product-side Or, and the order of an And's terms** (§7.11). Each load creates its own
ledger:

```bash
B=/tmp/bench-ledger/bench; $B load-product -ledger product-n1 -noise 1 && $B load-product -ledger product-n9 -noise 9 && $B product-or -ledger product-n1 && $B product-or -ledger product-n9 && $B product-or -ledger product-n9 -from 534600
```

Stop the server and delete `/tmp/bench-ledger` when you are done. At 1M accounts per scope the
store takes about 3 GB. The server log also grows by about 1 GB, because the ledger writes one INFO
line per listed account (ADR-005 ask L2).
