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
| `load-lettering [-ledger lett] [-prefix psp:hold:] [-persistence NORMAL] [-n 1000000] [-noise 0]` | Books each payment as lettering: `world → {prefix}{id}`, then `{prefix}{id} → psp:main`, both with `payment_ref = id`. Creates the `payment_ref` metadata index and the address index. NORMAL keeps lettered holds listed at zero, as if purged holds were reachable by address (EN-2331) |
| `txs -ledger L -from X -to Y [-kind K] [-exists F [-index]] [-prefix P] [-ranges 8]` | `ListTransactions` over the transaction-id window `(X, Y]`, optionally filtered server-side on `kind == K`, on metadata `F` being present (`-index` creates `F`'s index first), or on a posting address under prefix `P`, split into parallel ranges |
| `retag -ledger L -tx ID -kind K` | Rewrites one transaction's `kind`, to show that a filtered re-read of a past window changes (transaction metadata is mutable; logs are not) |
| `lastlog L` | The ledger's head log id (`GetLedgerStats.log_count`) |
| `rewind [-ledger psp] [-prefix psp:tx:] [-writers 8]` | Proves the rewind: takes an oracle checkpoint at `S`, lists the scope live while writers mutate it, rewinds the listing with the logs `(S, head]`, then compares every row with the checkpoint |
| `probe` | Shows what a lettered (purged) EPHEMERAL hold still exposes: account listing, and transaction lookup by address and by reference |
| `cutprobe` | Resolves a cut `S` from a date, using the per-ledger log-date index |

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

Stop the server and delete `/tmp/bench-ledger` when you are done. At 1M accounts per scope the
store takes about 3 GB. The server log also grows by about 1 GB, because the ledger writes one INFO
line per listed account (ADR-005 ask L2).
