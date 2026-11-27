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

- `TestIntegration_ControlStateContract`: 19 of 19 scenarios pass on Ledger `main`. They include the three reproduced races, the guard without a write, the status move and the bump. They also include the two rule-change scenarios of option B. In the first, a rule change claims the rule's sequence, so an evaluation planned on the old rule conflicts and re-plans. In the second, a rule change that loses the sequence to an evaluation retries with the same rule version, without a `409`.
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
| Second run, after the review fixes: prototype / RFC 0022 | 420 / **565** (+35 %) | 18.1 / 22.0 / 24.4 ms (RFC 0022) | 20,401 / 2,300 | 204 / 23 |

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

## Rule changes: option B

A rule change writes the rule and also claims the next version of the rule's sequence (`eval:rule:{id}`), in one transaction. Evaluations need no guard on the rule, so the hot path keeps one transaction per evaluation. The sequence becomes the rule's ordered, gapless audit trail: every capture sits after the configuration it used, and every change sits between two evaluations, with its actor and diff. The alternative, a TRANSIENT guard on the rule in every evaluation, would have added one transaction to each evaluation. The guard stays available for rare cases. RFC 0022 records the choice.

## API and UI

The spike also brings the API contract and the UI in line with the design. They still run on the prototype store.

| Change | API | UI |
|---|---|---|
| Optimistic concurrency on rules | `GET` and `PATCH /rules/{id}` return an `ETag` header. `PATCH` accepts `If-Match`; a stale value answers `409 CONFLICT` with the current `ETag` and writes nothing. `openapi.yaml` documents it. | Every rule patch sends the revision it read as `If-Match`: from the list toggle, the detail page and the edit dialog. On `409`, the UI explains that the rule changed, reloads its latest version, and the next action succeeds. The Next proxy forwards `If-Match` and `ETag`. |
| Live alert counts on the rules list | Already served (`alerts.open`, `alerts.acknowledged`). | Shown on each rule: "1 open", "2 acknowledged". |
| Rule sequence (option B) | Already served: activity `sequence` and `ruleRevision`. | Each configuration change in the timeline states that evaluations after it, in the rule's sequence, use its revision. |

**CEL removed from the API and the UI (ADR-006).**
- The rule response and `openapi.yaml` no longer carry `compiledCEL`, and the handler tests assert its absence.
- The UI no longer shows "Implementation details" (rule detail and revision snapshot) or "Technical details (compiled CEL)" (evidence), and the guide no longer mentions generated CEL.
- The CEL engine (`cel-go`, `engine.Compile` at rule create and patch) and the `compiledCEL` formula that templates still write into evidence leave with S5 (EN-2755). The UI already ignores that formula.

Checked end to end on Ledger `main` with the spike server (`:18081`) and the UI (`:13003`):
- create two rules and evaluate them, so one fails;
- see "1 open" on the failing rule;
- change that rule through the API behind the UI's back;
- toggle it in the UI and get the conflict message, with the new name reloaded;
- toggle again, which succeeds;
- see the boundary note on every configuration change.

Limits on the prototype store:
- The ETag is the rule's revision (content hash), not the `V` balance of the new layout.
- The `If-Match` check happens before the write, not in the write.
- The server-side retry of option B and an `accepted` count need the new storage behind the service.
- Alert actions (`ack`, `resolve`, `accept`) do not take `If-Match` yet.

## UX pass (P1 and P2 of the UI review)

| Area | Change |
|---|---|
| Schedule | Each rule shows "Runs every 15 minutes (UTC) · next 15:15", or "On demand only" flagged in amber, with a note in the detail page. A new rule gets a 15-minute schedule by default. The alert period reads "One alert per day", no longer only "Daily". |
| Active alerts | The alerts list defaults to Active (open and acknowledged). The overview splits "To handle" (open) from "In progress" (acknowledged and still failing), and adds rule health: failing, never evaluated, on demand only. |
| Amounts | Exact major units everywhere ("8,000.00 USD", "tolerance 10.00 USD"), computed with string arithmetic. Hovering shows the exact ledger integer. |
| Titles and gaps | Alerts are titled by rule and gap ("GL cash matches Stripe balance — Off by 50.00 USD"), with the fingerprint and the period as secondary text. The rules list, the rule detail and the timeline show the latest gap. |
| Compact list | The rules list sorts by "Needs attention" (failing, then never run, then passing), folds the configuration, and shows "Never evaluated". |
| Form | "New rule" and "Create rule", without "advanced" or "V2". Only the three v3.0.0 templates are offered. An empty or `*` account selector is refused. |
| Alert actions | Each action says what it does. Accept asks for an end date (30 days by default, 90 at most), sent as `expiresAt`. |
| Technical details | The timeline folds start, end, read instant ("Balances read at", no longer "PIT") and sequence under "Technical details". Account queries read `gl:cash` instead of raw JSON. |

P3, done next:
- **Header.** The brand stacks in the sidebar instead of spilling over the tab bar. The tab bar keeps its four tabs whole, and the endpoint and health controls wrap below them.
- **Delete.** In the rules list, Delete moved behind a "…" menu, away from Run now.
- **Back to the ledger.** Each source card in the rule detail has "Show accounts", which lists the accounts the source selects in its ledger, with live balances in major units.
- **Rule detail header.** It reads "One alert per day · On demand only".
- **Alert history.** It says "audit record #…", "evaluation …" and "not notified", no longer "tx", "eval" and "suppressed".
- **Deep links.** A link such as `#reconcile?view=rules&rule=…` no longer breaks hydration: the hash is read after mount.
- **Rule names.** At narrow widths they keep at least 16rem; the controls wrap below.

Still open: "Test on current balances" in the form needs a preview endpoint (ADR-005 decision 31), and the overview's period status comes in phase 4.

## Review fixes

An independent review of the spike commit found the following. The fixes are in the adapter and the port.

| Finding | Fix |
|---|---|
| A projected metadata key that a write dropped stayed on the account, so a list filter could match a stale value. | Every projected key is written on every write, empty when absent. `TestIntegration_ReconChart` checks it. |
| `Load` hid the record asset `CSR`, while the port accepted it in a move. | `CSR` is reserved in the port next to `V`, and `Load` hides nothing. |
| A key outside the reconciliation chart reached the ledger and consumed the idempotency key of the rejected batch. | `Recon.Address` validates the UUID, the period and the hex fingerprint first, and returns `ErrInvalid`. The ledger's STRICT chart stays as the second line. |
| A bump or record replayed after the 24-hour idempotency window would apply twice. | Bump and record transactions carry a reference derived from the unit of work's key. A late replay fails with `ErrConflict` instead. |
| `Validate` accepted two bumps of one key. | Rejected as `ErrInvalid`. |

Checked and fine: byte-equal replay (the ledger hashes the batch deterministically), the replayed rejection reason, the soundness of the guard without a write, and atomicity. Nothing outside `internal/controlstate` imports the port. The full `-tags it` suite of the prototype (ledger client, `ledgerstore`, resolver, API) passes on Ledger `main`.

Left as known risks:
- `Load` has no explicit barrier. Address filters read the main store, so this is low risk.
- The global `guard:a` and `guard:b` accounts are TRANSIENT and never persisted. The ledger applies serially anyway.
- Metadata limits (16 KiB per value, 128 keys per entity per command) are not checked before the commit.

## Migrating to the key-value store

The draft store of formancehq/ledger#2487 offers Get, Put and Delete inside `Apply`, with no compare-and-swap and no listing. A key-value adapter therefore keeps:

- preconditions, `Move` and `Bump` on ledger accounts, because they need the reference, insufficient funds and commutative adds;
- the projected metadata on the ledger, because the store cannot list.

Only `Fields` and `Live` move to `Put`, in the same batch, with `Live` under a key of its own so that a bump never overwrites a concurrent write. Because `Load` would read accounts and values separately, the value must carry its version so that a torn read is detected. The port's interface does not change: the switch is a new adapter that reuses the layout and the contract suite.

## Limits

- One local node, Ledger `main` built from source, with no other load. Absolute numbers will differ on a cluster: compare layouts, not absolutes.
- The benchmark loads entities through the port. It does not exercise the reconciliation service, templates or source reads.
- The prototype benchmark uses the same workload with no status units and no occurrence counter.
