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
cd ~/Documents/GitHub/ledger && GOROOT= go build -o /tmp/bench-ledger/ledger-server . && GOROOT= go build -o /tmp/bench-ledger/ledgerctl ./cmd/ledgerctl
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
| `txs -ledger L -from X -to Y [-kind K] [-exists F [-index]] [-ranges 8]` | `ListTransactions` over the transaction-id window `(X, Y]`, optionally filtered server-side on `kind == K` or on metadata `F` being present (`-index` creates `F`'s index first), split into parallel ranges |
| `retag -ledger L -tx ID -kind K` | Rewrites one transaction's `kind`, to show that a filtered re-read of a past window changes (transaction metadata is mutable; logs are not) |
| `lastlog L` | The ledger's head log id (`GetLedgerStats.log_count`) |
| `rewind [-ledger psp] [-prefix psp:tx:] [-writers 8]` | Proves the rewind: takes an oracle checkpoint at `S`, lists the scope live while writers mutate it, rewinds the listing with the logs `(S, head]`, then compares every row with the checkpoint |
| `probe` | Shows what a lettered (purged) EPHEMERAL hold still exposes: account listing, and transaction lookup by address and by reference |
| `cutprobe` | Resolves a cut `S` from a date, using the per-ledger log-date index |

## 3. Reproduce the ADR-005 figures

```bash
go build -o /tmp/bench-ledger/bench ./tools/bench-txlevel
```

```bash
B=/tmp/bench-ledger/bench; $B load -ledger psp -prefix psp:tx: -n 1000000 -batch 500 && $B load -ledger bank -prefix bank:tx: -n 1000500 -batch 500 -drift
```

```bash
B=/tmp/bench-ledger/bench; $B diff -a psp -pa psp:tx: -b bank -pb bank:tx: && $B cp-create
```

Pass the id printed by `cp-create`, then the ledger's head log id:

```bash
B=/tmp/bench-ledger/bench; $B diff -a psp -pa psp:tx: -b bank -pb bank:tx: -cp 1 && $B logs -ledger psp -prefix psp:tx: && $B rewind
```

The log-versus-transaction comparison (ADR-005 §5):

```bash
B=/tmp/bench-ledger/bench; $B load-mixed -n 1000000 -every 10 && $B txs -from 0 -to 1000000 -ranges 8 && $B txs -from 0 -to 1000000 -kind payment -ranges 8 && $B txs -from 0 -to 1000000 -exists payment_ref -index -ranges 8
```

Stop the server and delete `/tmp/bench-ledger` when you are done. At 1M accounts per scope the
store takes about 3 GB.

The server log also grows by about 1 GB, because the ledger writes one INFO line per listed account
(ADR-005 ask L2).
