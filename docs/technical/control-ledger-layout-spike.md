# Control ledger layout spike

This spike implements the control ledger layout of RFC 0022 (`9959818`) behind the `ControlState` port. It runs the contract suite on a live Ledger, then benchmarks the new layout against the prototype's layout. Branch `spike/control-ledger-layout`, 2026-11-27, Ledger `main` at `60ea68347` (protocol 40), one local node.

## What the spike adds

| Package | Change |
|---|---|
| `internal/controlstate` | Adds `Write.Move` (status units), `Bump` (a counter and live fields, with no version change), and `Entry.Balances` and `Entry.Live`. |
| `internal/controlstate/memstate` | Implements `Move` and `Bump`, as the reference behavior. |
| `internal/controlstate/controlstatetest` | Adds two contract tests: `StatusMoveGuardsTheStatus` and `BumpKeepsTheVersion`. |
| `internal/controlstate/ledgerstate` | The new adapter: one account per entity, `[V 1]` under the reference `<entity>:v<n+1>`, status assets, a gauge per rule, indexed transaction metadata, and `Load` in one `ListAccounts`. `layout.go` holds the generic layout for the contract suite and the reconciliation chart (`Recon`, STRICT). |
| `internal/controlstate/tokenstate` | The prototype adapter (one account per version), kept for comparison. |

How the new adapter maps the port:

- **A write** posts `[V 1]` from the gauge to the entity, under the transaction reference of version n+1. A writer that read an outdated version gets `TRANSACTION_REFERENCE_CONFLICT`, which the adapter returns as `ErrConflict`.
- **A move** posts a status unit between the gauge and the entity. Giving back a unit the entity does not hold fails with `INSUFFICIENT_FUNDS`.
- **A precondition without a write** moves n units of `V` to a TRANSIENT account, moves the rest of the balance to a second TRANSIENT account, then moves the n units back. A later version leaves a balance on the second account, which fails the whole batch with `TRANSIENT_ACCOUNT_NON_ZERO`. This settles the RFC's open point without `balance()`.
- **A bump** posts the counter asset (`OCC`) and sets `live.*` account metadata, without posting `V`.
- **A record** rides on its subject's write transaction. Otherwise it is a transaction of its own that posts `[CSR 1]` from the gauge to itself. Records are listed through the indexed transaction metadata `cs_subject`.
- **Error mapping.** Refusals are classified by the Ledger's `ErrorInfo` reason. Only a reference conflict, insufficient funds or a non-zero transient account count as `ErrConflict`. A chart refusal (`ACCOUNT_NOT_MATCHING_TYPE`) is returned as it is, so callers do not retry it.

Fact checked against this Ledger: an index needs its field declared in the metadata schema first (`SetMetadataFieldType`). Otherwise `CreateIndex` fails with `metadata field not in schema`.

## Tests

- `TestIntegration_ControlStateContract`: 17 of 17 scenarios pass on Ledger `main`, including the three reproduced races, the guard without a write, the status move and the bump.
- `TestIntegration_ReconChart`: one alert's life on the reconciliation chart in STRICT mode. The test checks the gauge balances at each step (`V -4, OPEN -1, OCC -1`, then `V -5, ACK -1, OCC -2`) and the projected `status` metadata. It also checks that an address outside the chart is refused, and that the refusal is not a conflict.

Run them with `RECON_LEDGER_ADDR=<ledger> go test -tags it -run 'TestIntegration_(ControlStateContract|ReconChart)' ./internal/controlstate/ledgerstate/`.

## Benchmark

`TestIntegration_BenchLayouts` runs with `RECON_BENCH=1`, and `RECON_BENCH_RULES`, `RECON_BENCH_CYCLES`, `RECON_BENCH_WORKERS` and `RECON_BENCH_OUT` set the scale and the output file.

The run measured here: 100 rules, 20 cycles of 6 evaluations each, 16 workers. Each cycle runs in a period of its own:

1. pass
2. fail, which opens the alert
3. fail again
4. acknowledge
5. fail again
6. pass, which resolves the alert

That makes 12,100 commits per layout.

### Writes

| Layout | Commits/s | Commit latency p50 / p95 / p99 | Accounts after | Accounts per rule |
|---|---|---|---|---|
| Prototype: one account per version | 439 | 18.4 / 23.7 / 26.4 ms | 20,401 | 204 |
| RFC 0022: one account per entity | **577** (+31 %) | 17.7 / 21.8 / 24.2 ms | **2,300** | **23** |

The new layout keeps one gauge, one rule, one counter and one alert account per (rule, period) that failed. The prototype adds one account per version written, about 10 per evaluation cycle, and the gap grows with every run. Commit latency is set by the Raft commit on both layouts. The throughput gain comes from creating fewer accounts. This matches Ledger ADR 0004, which measured about 55k tx/s when transactions create accounts against about 120k when they reuse them.

### Reads on the new layout (30 repetitions, p50 / p95)

| Read | Call | Latency |
|---|---|---|
| Load a unit of work (rule, counter, alert) | 1 `ListAccounts` on an `or` of 3 exact addresses | 10.5 / 11.4 ms |
| Same | 3 `GetAccount` | 31.4 / 32.3 ms |
| Counts for a page of 100 rules | `ListAccounts` on `pool:rule:` (the gauges) | 10.6 / 17.1 ms |
| Same | `AggregateVolumes` grouped, `status` filter (active alerts only) | 10.5 / 11.9 ms |
| Same | `AggregateVolumes` grouped, address prefix only (visits all 2,000 alerts) | **20.7 / 23.9 ms** |
| Counts for one rule | `GetAccount` on its gauge | 10.3 / 11.1 ms |
| Is the last period green, over all rules? | `AggregateVolumes`, `period` + `status` filter | 10.5 / 11.1 ms |
| Timeline of one rule, first page | `ListTransactions` on `metadata[rule]`, page 100 | 10.5 / 13.9 ms |

Reading the numbers:

- **About 10 ms is the floor of any read on this node.** It is the linearizable read barrier, so a single call costs the same whatever it reads. That is why one `ListAccounts` on 3 addresses is three times faster than 3 `GetAccount`.
- **The aggregation rule is confirmed.** Without a `status` filter, `AggregateVolumes` visits every alert account, resolved history included, and doubles its cost at 2,000 alerts. That cost grows with the history. With the filter, or with the gauges, the cost stays at the floor.
- The gauge read is one point read per rule, and needs neither a scan nor the read index.

## Limits

- One local node, Ledger `main` built from source, with no other load. Absolute numbers will differ on a cluster: compare layouts, not absolutes.
- The benchmark loads entities through the port. It does not exercise the reconciliation service, templates or source reads.
- The prototype benchmark uses the same workload with no status units and no occurrence counter.
