# Design — `stale_holds`: time-based monitoring of trapped funds

> ✅ **Implemented** — [stale_holds.go](../../internal/templates/stale_holds.go), V2 only. Two
> questions still need the client (§5, Q1/Q2); the template is built on a stated assumption whose
> alternatives the spec already absorbs. Client ask: a card programme whose holds are placed by its
> **issuer-processor**.
> **Last updated:** 2026-09-08
>
> This repository is public, so the parties here are deliberately unnamed: **the client** runs the
> card programme and writes the holds into the ledger; **the processor** is its issuer-processor,
> which supplies each hold's expiry and owns the hold-writing path. Keep it that way — the design
> and its open questions do not depend on who they are.

## 1. The ask

| Requirement | Detail |
|---|---|
| Primary goal | Alert on "stale" holds — funds reserved in an account longer than expected. |
| Trigger | The **age** of a held balance, not its size. |
| Threshold | The **expiry the processor communicated** for that hold, with a **48h fallback**. |
| Business impact | Funds stop being trapped indefinitely; liquidity stays aligned with the issuer's constraints. |
| Emphasis | Being warned **before** a hold goes stale is the real value — this is a liquidity-management tool, not a post-hoc flag. |

Everything else in this doc follows from one question: **where does "age" come from**, given a
ledger that stores balances and postings, not holds-with-a-TTL.

---

## 2. The constraint that kills the obvious approach

The tempting design is "compare the balance now against the balance 48h ago" — a trailing-window
minimum balance, which approximates *funds that have sat continuously*.

**That is not available.** Ledger V3 has no arbitrary point-in-time read. The V2 `moves`-diff PIT
was dropped, and the query-checkpoint replacement was itself removed for reconciliation:
[ADR-003](../prd/adr-003-checkpoint-anchor-and-crosscheck.md) — *all ledger sources read live*.
`EvalInput.PIT` still exists, but it is a **nominal clock** (it derives the period id), not a read
anchor: `AggregateBalance` / `ListAccounts` carry no `at=`/`checkpoint` argument
([internal/ledger/resolver.go](../../internal/ledger/resolver.go)).

So the age signal must come from **state readable at a single live instant**, not from history.
Three such signals exist. Only these three.

---

## 3. What the ledger actually gives us

| Signal | Where it lives | Readable? | **Filterable in a query?** | Per-hold? |
|---|---|---|---|---|
| **A. Processor expiry** (or created-at) in account metadata | `metadata[<key>]` on the hold account | Yes | **Yes** — `$lt`/`$lte`/`$gt`/`$gte` | Yes |
| **B. Ledger-native account timestamps** — `first_usage`, `insertion_date`, `updated_at` | on the account row itself | Yes, but currently **dropped** on projection | **No** | Yes |
| **C. Trailing-window minimum balance** | historical reads | **No** (§2) | — | No |

Verified details that shape the design:

1. **Metadata comparison operators exist and are pushed down to the ledger.** `dataLedgerLeaf`
   ([internal/ledger/resolver.go:148](../../internal/ledger/resolver.go#L148)) translates
   `metadata[k]` with `$match`/`$gt`/`$gte`/`$lt`/`$lte`/`$exists`, and `address` with exact or
   trailing-`*` prefix `$match`, combined by `$and`/`$or`/`$not`. **This is the whole design.** The
   time predicate can be evaluated *by the ledger*, against its index, instead of by us over a
   fetched set.
2. **A `datetime` metadata key is stored as int64 epoch micros and compares against int bounds**
   ([internal/ledgerschema/filters.go:68](../../internal/ledgerschema/filters.go#L68),
   [internal/ledgerstore/metadata.go:25](../../internal/ledgerstore/metadata.go#L25)). So a
   properly-typed `datetime` expiry key is directly comparable — no epoch-int requirement on the
   client, *provided we send micros*.
3. **But `metadataInt()` cannot read a `datetime` key.** `MetadataToMap` renders a datetime value as
   an **RFC3339Nano string**
   ([metadata_helpers.go:61](../../internal/ledgerpb/commonpb/metadata_helpers.go#L61)), and
   `SumAccountMetadataInt` parses base-10 integers only. A datetime-typed expiry read through
   `metadataInt` **errors**. Reading per-hold deadlines therefore needs a small instant parser in the
   template (accepting RFC3339 *and* integer epochs in a declared unit), not `metadataInt`.
4. **Account timestamps are returned but discarded.** The proto carries `first_usage`,
   `insertion_date`, `updated_at` ([proto/ledger/common.proto:148](../../proto/ledger/common.proto#L148));
   `accountFromProto` ([internal/ledger/resolver.go:131](../../internal/ledger/resolver.go#L131))
   keeps only address/metadata/balances. Plumbing them through is ~10 lines — and gives a
   **metadata-free fallback**. They are *not* filterable: `FieldRef` has a `metadata` field and
   nothing else ([common.pb.go:11173](../../internal/ledgerpb/commonpb/common.pb.go#L11173)), so
   signal B always means a full scan of the hold universe.
5. **Sparse fan-out is already supported by the alert machinery.** `planAlertTransitions`
   ([internal/api/service/evaluation.go:209](../../internal/api/service/evaluation.go#L209)) opens an
   alert per failing outcome and **auto-resolves any active fingerprint the evaluation did not
   emit**. A template may therefore emit outcomes *only for the holds that are stale* — the released
   ones simply disappear and their alerts close.

---

## 4. Proposed design

**Approach A (metadata), with the time predicate pushed into the ledger query as a literal cutoff
computed from the evaluation clock.** Signal B is the documented fallback when the client has no
metadata; signal C is not implementable.

### 4.1 Shape

```jsonc
{
  "ledger": "holds",
  "query":  { "$match": { "address": "holds:*" } },          // the hold universe
  "asset":  "USD/2",
  "deadline": {
    "expiryKey":  "hold_expires_at",   // the expiry recorded on the hold; wins when present
    "createdKey": "hold_created_at",   // fallback basis
    "encoding":   "datetime",          // datetime | epoch_seconds | epoch_millis | epoch_micros
    "maxAge":     "48h"                // fallback: createdKey + maxAge
  },
  "warnWithin": "6h",                  // optional — band mode (§4.4)
  "maxHoldsScanned": 500               // optional per-rule read cap (§4.6)
}
```

### 4.2 Evaluation

1. `now := in.PIT` (defaults to `time.Now().UTC()`,
   [evaluation.go:60](../../internal/api/service/evaluation.go#L60)).
2. Compute the **cutoff instant** and encode it in the key's declared unit → an integer literal.
3. Build the effective query: `spec.query AND { "$lte": { "metadata[expiryKey]": <cutoff> } }`
   (plus a `$gt` lower bound in `approaching` mode).
   The ledger returns **only** the holds past (or approaching) their deadline.
4. `ListAccounts` on that query — budgeted by `MaxAccountsScanned`
   (50 000 default, [budget.go:33](../../internal/engine/budget.go#L33)).
5. Drop accounts whose balance for `asset` is **zero** — a released hold leaves a zeroed volume but
   keeps its account row and metadata (an `EPHEMERAL` volume eviction does not delete the account),
   so it still matches an expiry-based filter. Balance is *not* filterable in the query, so this
   post-filter is unavoidable.
6. Classify each survivor against its own deadline (expiry if present, else created + `maxAge`) and
   emit one outcome per stale hold.

### 4.3 We do **not** need a `now()` CEL builtin

The brief anticipated exposing `evalCtx.pit` to CEL. The pushdown design makes that unnecessary —
and the absence is better:

- The clock enters as a **materialized integer literal** at render time, so the persisted
  `compiledCEL` is an exact, re-runnable record of the predicate *that evaluation* applied. A
  `now()` builtin would silently re-clock on replay: the same stored expression would mean something
  different tomorrow, which is precisely what an audit trail must not do.
- The kernel stays **pure and deterministic** — the property ADR-001 sells. No impure builtin, no
  new declaration, no new cross-check surface.
- The rendered CEL is honest and executable with today's vocabulary:

  ```
  balance(ledgerSet("holds",
    "{\"$and\":[{\"$match\":{\"address\":\"holds:*\"}},
                {\"$lte\":{\"metadata[hold_expires_at]\":1756809600000000}}]}"), "USD/2") == 0
  ```

  *"No funds sit in a hold whose deadline has passed."* This is also what evidence carries as
  `effectiveQuery`, so the set behind an alert is recoverable from the alert. **Net engine work: zero new builtins**, plus the ~10-line
  account-timestamp plumbing only if the signal-B fallback is in scope.

### 4.4 Advance warning — a band, not a second threshold

Warning and stale are the **same predicate at two cutoffs**. Given rule-level severity
([evaluation.go:281](../../internal/api/service/evaluation.go#L281) — every alert an evaluation opens
takes `rule.Severity`), an "early warning at `low`, breach at `high`" story needs **two rules**:

| Rule | Cutoff | Severity |
|---|---|---|
| *Holds approaching expiry* | `now < deadline <= now + warnWithin` (a **band**) | `low` / `medium` |
| *Stale holds* | `deadline <= now` | `high` |

Because the warning rule is a band, a hold crossing into stale **disappears** from the warning
rule's outcomes → its warning alert auto-resolves (§3.5) as the stale alert opens. Clean escalation,
no duplicate live alerts, no change to the alert contract.

> ⚠️ **`warnWithin` must exceed the rule's evaluation interval**, or a hold can jump the band between
> ticks and never warn. The template's `Validate` only sees `templateSpec`, so this check belongs in
> the service layer (where `schedule` is visible) or in the guide.

### 4.5 Outcomes

**One outcome per asset**, fingerprint `asset:<asset>`. Evidence is the scan, not the holds:
`holdsMatched`, `holdsBudget`, `holdsReleased`, `holdsRejected`, `holdsFlagged`, `amountFlagged`,
`oldestDeadline`, `evaluatedAt`, `deadlineOnOrBefore` (and `deadlineAfter` for a band), `ledger`,
`asset`, `compiledCEL`, and `effectiveQuery`.

The counts partition the matched set — `matched = released + rejected + flagged` — so they add up
rather than leaving a reader to wonder where rows went. `holdsRejected` counts holds the ledger
returned and the authoritative in-Go check then declined. It is normally zero, because the pushdown
and the direct evaluation are equivalent by construction; it was previously a silent `continue`,
which meant a disagreement between them cost the identity with nothing to show for it.

> **Revised 2026-09-08 — `per_hold` is gone.** This section previously offered a per-hold scope
> alongside the aggregate, and made it the default: it answered "*which* hold is stuck, for how
> much", auto-resolved per hold, and was bounded by the stale count rather than the account set. The
> bound was real. The shape was still wrong — an alert per stuck hold pages an operator once per
> problem, which is worst precisely when a release feed breaks and every live hold goes stale at
> once. `scope` and `identityKeys` are removed; see the ADR-004 amendment.
>
> What replaces it is the rule's own selector. A rule watches a subset it declares — an address
> prefix plus a metadata match narrowing to one desk or book — so the aggregate over that subset is
> the signal, and separate sets are separate rules with separate severities.
>
> The set behind a number is still recoverable: `effectiveQuery` is the query the evaluation ran,
> deadline cutoff included as an integer literal, in this module's own dialect — so it drops back
> into a rule's `source.query`. It is not byte-compatible with the ledger's HTTP `?filter=`, which
> spells existence `{"$exists":{"metadata":"k"}}` against this module's
> `{"$exists":{"metadata[k]":true}}`. That gap is closed on this side instead:
> `GET /ledgers/{ledger}/accounts?filter=<effectiveQuery>` runs a stored query through this module,
> which already speaks the dialect. Verified against a live ledger — an alert reporting
> `holdsMatched: 3` returns exactly those three accounts, the released one among them with a zero
> balance, which is what `holdsReleased: 1` counted. Re-running it does not reconstruct the evaluation
> — no point-in-time read (ADR-003) means the deadline half is frozen while balances stay live, so
> the answer is *the holds still past that cutoff and still funded*. A list captured at evaluation
> time would be stale by the time anyone opened it anyway.

`periodType: continuous` is the right default: a trapped hold is a live condition, not a
period-close fact.

### 4.6 Bounding the blast radius

A rule reads every hold the deadline filter matches, bounded only by the engine's accounts budget
(50 000). The outcome is one aggregate per asset, so the exposure is the *read* — and while Q2b (the
release marker) is unresolved, that read grows with every hold ever placed.

`maxHoldsScanned` lets a rule cap its own read below the engine's. What it is, precisely:

- **A read cap, and only that.** It bounds the accounts the deadline filter returns — stale holds
  *plus* any released hold still carrying an expired deadline. While Q2b is unresolved it doubles as
  an early warning on accumulated dead holds.
- **Fails, never truncates.** Over the cap, `ListAccounts` aborts, the evaluation is recorded as
  `ERROR` with an engine-health alert, and **the alert transition plan never runs** — existing alerts
  are untouched ([evaluation.go](../../internal/api/service/evaluation.go)). Truncating would be
  worse than failing in a different way now: a partial read understates `holdsFlagged` and
  `amountFlagged`, reporting a smaller problem than the one that exists.
- **Never raises the engine's limit.** `MaxAccountsScanned` protects the ledger from any single
  evaluation, so the effective budget is `min(rule, engine)`, recorded in evidence as `holdsBudget`.
  Absent a rule-level cap, a rule reads under the engine's budget.

### The guard is still in the wrong place

Be clear about what this is: a **read** cap standing in for an **alert** cap. It bounds alerts only
because `flagged <= matched`, so `maxHoldsScanned: 500` does guarantee at most 500 new alerts from
that rule per run — but it is an over-approximation (dead holds count against it) and it lives in one
template's spec, while the exposure is the module's.

Every fan-out rule has it — `account_threshold` mode `per_account` and `source_parity` scope
`per_account` are bounded by the engine's 50 000 and nothing else. The guard that actually fits a
rules-and-alerts module is **a cap on new alerts per evaluation, applied at the service layer**,
where `planAlertTransitions` already knows both the outcomes and the currently-active fingerprints:
if a plan would open more than *N* new alerts, withhold the **whole** plan — never a subset — record
the evaluation and its full evidence as usual, and raise one meta-alert naming the rule and the
count. Withholding everything is what keeps it safe: no partial application means no disappearance
sweep, so no alert resolves because there were too many problems.

✅ **Built** — see [workflows.md §6b](./workflows.md). The service counts the alerts a plan would
*open*, and above `DefaultMaxNewAlertsPerEvaluation` (200) applies none of it and raises one
`alert.cap` meta-alert. Only opens count, so a rule steadily failing on an already-alerted set keeps
updating and resolving normally; the evaluation and capture are recorded either way.

The two caps are independent layers, and both still apply: `maxHoldsScanned` bounds **one rule's
read**, while the service cap bounds the **module's alert output** across every fan-out template.

**What deliberately does not exist is "open at most N alerts" inside a template.** Every version of
it breaks the fingerprint contract: capping emitted outcomes auto-resolves the remainder, and
switching an over-budget rule to a summary outcome changes its fingerprints, which resolves the
entire open set as a side effect. Since 2026-09-08 `stale_holds` emits one aggregate per asset
unconditionally, so the question no longer arises for this template — but the reasoning is why no
template gets a runtime fallback that changes its own outcome shape.

---

## 5. Decisions

Q3–Q6 are settled and implemented. Q1 and Q2 need the client: the template is built on a stated
assumption, and the `deadline` spec absorbs a different answer without a rewrite.

### Q1 — How are the processor's holds modelled? ✅ *confirmed by the client (2026-09-07)*
**The client already writes one account per authorisation, keyed by Authorization ID** — the model this
template was built for. The processor's per-hold expiry is therefore usable directly, and **the watermark
fallback below is moot**: it existed only for the aggregated
model, and maintaining one on top of per-authorisation accounts would be redundant work for a
strictly worse signal. It is kept here as the answer for a *future* client who cannot split accounts.

The reasoning is worth keeping, because it is what makes the per-authorisation model the right one
rather than merely the convenient one.

**A deadline is a property of a *lot* of money, not of an account.** Account metadata is one scalar
per key, so an account holding N authorisations can carry at most one deadline — and the ledger
offers no per-lot dimension *inside* an account to hang the other N−1 on:

- **Colors** segregate volumes within an account, but they are constrained to `^[A-Z]*$`
  ([common.proto](../../proto/ledger/common.proto)) — they cannot encode an authorisation id or a
  date — and they carry no metadata of their own.
- **Volume rows** carry no timestamps; `Account.first_usage` / `updated_at` describe the whole
  account, and neither is filterable (§3.4).

So per-hold ageing requires per-hold addressing: **the address space is the only lot dimension there
is.** Splitting costs nothing, because aggregation is just a prefix query — `holds:issuer:card123:*`
still sums to the card's total reserve whenever someone wants that number.

| Model | What the deadline metadata can mean | What the rule can say |
|---|---|---|
| **One account per authorisation** ✅ *(what the client does)* | exactly this hold's deadline, written once at creation, never updated | *"authorisation 8801 has been stuck for 6 hours, for $250"* — and the processor's own per-hold expiry is usable |
| **One account per card, many authorisations** — with a `funds_held_since` **watermark**: set when the balance goes 0 → non-zero, cleared when it returns to zero, **never touched in between** | "funds have sat continuously in this account since T" | *"this card has had money held for over 48h"* — no hold identity, no amount attribution, and it over-reports on an account that simply never empties |
| **One account per card, timestamp updated per transaction** | nothing usable | ❌ **Actively harmful.** Each new authorisation resets the clock, so the oldest trapped funds are the ones the rule can never see. The alert silently never fires — worse than having no alert. |

The middle row is a real fallback and needs **no code change**: it is `createdKey: funds_held_since` +
`maxAge: 48h` + `scope: aggregate`. But the watermark has to be maintained by whoever writes the
holds, with exactly that 0 → non-zero / → 0 discipline — and it is the client, not us, who can do
that, because the ledger can no longer answer "what was the balance 48h ago" (§2).

If holds are aggregated per card **and** no watermark is available, the honest answer is *"this needs
a ledger-modelling change first"*, not a template. That case does not arise here.

### Q2 — Expiry / created-at metadata: key, type, encoding? ⏳ *assumed, needs confirmation*
**Assumed:** `hold_expires_at` and `hold_created_at`, both declared **`datetime`** on the ledger and
indexed. The spec's `deadline.encoding` covers the alternatives (`epoch_seconds`, `epoch_millis`,
`epoch_micros`) if the keys turn out to be integers, and `expiryKey`/`createdKey` are free-form, so a
different answer is a spec change, not a code change. What is **not** negotiable: the key must be
queryable — a declared `datetime`/integer type **and** a ready accounts index. A plain RFC3339
*string* key cannot be filtered, and would force the metadata-free fallback (§3, signal B).

**Q2b — how is a released hold represented?** ⏳ **The scaling question.** A released hold keeps its
account row and its deadline metadata, so it keeps matching an expiry filter forever. The template
drops it on its zero balance — balances are not filterable — but the *matched* set still grows with
every hold ever placed, and eventually trips the accounts budget. For a card programme doing
thousands of authorisations a day, that is a matter of weeks.

**`EPHEMERAL` hold accounts are the client side's proposed answer to this**, and they are the right
call for the reason given — the client's current jobs poll balances over HTTP and permanent rows for
every short-lived hold bloat the store. But they do **not** close this particular gap. Two mechanisms
look like they should solve it for free; **neither does** (both checked on a live cluster, §6):

- **`EPHEMERAL` hold accounts.** Purging on zero empties the account's `volumes` list, but the
  account row **and its metadata survive** — so the hold still matches a deadline filter. Ephemeral
  retires the *volume* rows, not the *account* rows: it is a real answer to store bloat and a
  non-answer to query selectivity.
- **The ledger's has-asset account filter** (`AccountHasAssetCondition`). Its semantics are
  *"has **ever** held a volume cell for this asset"*, not "holds it now" — a released, purged hold
  still matches. (Verified both ways: a filter on an asset nobody ever held returns empty, so the
  filter is genuinely applied.) It is also not in reconciliation's query DSL today, and adding it
  would not help.

So the liveness signal has to be an **explicit write at release** — one write, on the client's side
of the boundary:

1. **Clear the deadline key** (`hold_expires_at`) when the hold is released. Cheapest: no new key,
   and an absent key is naturally excluded by the range filter, so released holds leave the matched
   set with no change to the rule.
2. **Flip a status marker** (`metadata[hold_status] = released`) and fold `= active` into the rule's
   own query. More explicit, and it keeps the historical expiry readable.

Ask the client which is achievable in the hold-writing path.

### Q3 — Fallback semantics ✅ *decided*
**Expiry wins; else `created_at` + `maxAge` (48h); else nothing** — a hold matched by the query but
carrying no readable deadline is an error, not a silent pass. Both keys may be declared together: the
pushdown expresses "expiry if present, else creation" as an `$or` over `$exists`, so the ledger still
does the filtering. Explicitly **not** "last movement": `updated_at` is bumped by metadata writes as
well as postings, so a bookkeeping touch would silently reset the clock.

### Q4 — Advance warning ✅ *decided: two rules*
Severity is declared per rule, so "warn early, page late" is a warning rule at a low severity and a
stale rule at a high one. The warning rule matches a **band** (`now < deadline <= now + warnWithin`),
so a hold crossing into stale leaves it and its warning alert auto-resolves as the stale alert opens.
Zero change to the alert contract. Per-outcome severity (`Outcome.Severity` → `OpenAlertInput`) stays
available as a later, separate piece of work if one rule ever has to carry both.

### Q5 — Contract version ✅ *decided: V2*
V1's three-kind catalog is the frozen GA surface; V2 is the additive lane. Note that V2's other four
templates are [ADR-004](../prd/adr-004-multi-source-comparisons.md) named-source *arithmetic* — a
single-source *time* control is a new family, so V2 is the additive lane generally, not the
multi-source lane specifically. The spec still uses `V2NamedSource` for its one source, so IDs,
labels and asset declaration stay consistent with the rest of V2.

### Q6 — Per-hold granularity ✅ *decided: it is available here — but the branches disagree*
Per-account fan-out is **live on this branch** and **parked on `main`**, including the released
`v2.4.0` / `v2.4.1`, where `account_threshold` mode `per_account` and `source_parity` scope
`per_account` are rejected at rule-create (`perAccountParkedMsg`) to keep evaluation evidence bounded
to the asset count.

This is not a deliberate un-parking. `feat/reconciliation-ledger-v3` forked from main at `5868ce01`
(2026-06-24), a month before the parking landed (`1bd99ecd`, 2026-07-21, squashed into main as #83 on
2026-07-23), so this lineage never received it.

Two consequences worth carrying:

- **`per_account` is a V1-template property, not a V2 one.** It lives on `account_threshold.mode` and
  `source_parity.scope`. The V2 templates are aggregate-only by construction — a `V2NamedSource` has
  no scope and resolves to exactly one balance for one declared asset. `stale_holds` is the first V2
  template with per-account granularity, and it gets there through its own `scope` field rather than
  by reusing the V1 `Scope` type.
- **Resolved 2026-09-08.** The question was whether `stale_holds`'s `per_hold` fell under the same
  parking rationale. It was argued that it did not — the parking was about unbounded PASS evidence,
  and per-hold emitted outcomes only for *failing* holds with `maxHoldsScanned` bounding each rule.
  That argument was sound and beside the point: an alert per stuck hold is the wrong shape for an
  inbox regardless of how well bounded the read is. `per_hold` is removed (§4.5), so **no template
  fans out per account.**

---

## 6. Verified against a live Ledger V3

The design rests on one claim that no unit test can settle — that a `datetime` metadata key really is
comparable against an integer epoch-micros bound. It was checked against a local Ledger V3 cluster,
and the check is now a repeatable integration test
([stale_holds_it_test.go](../../internal/ledgerresolver/stale_holds_it_test.go),
`go test -tags it -run TestIntegration_StaleHolds ./internal/ledgerresolver/...`):

| Claim | Result |
|---|---|
| A `datetime` key compares against an epoch-micros `$lte` / `$gt` bound | ✅ — and it reads back as RFC3339, so `metadataInt` genuinely cannot read it |
| A metadata field must be **declared** (`set-metadata-type`) before it can be indexed | ✅ — `CreateIndex` fails with *"metadata field not declared in schema"* otherwise |
| The `$or` over `$exists` picks expiry when present, creation otherwise | ✅ |
| A released hold (zero balance, expiry retained) still matches the query | ✅ — dropped by the template on its balance, as designed |
| `Queries()`'s augmented query passes the create-time `ValidateQuery` guard | ✅ |
| The warning band excludes an already-stale hold | ✅ |
| An `EPHEMERAL` account purged at zero keeps its **row and metadata** (only `volumes` empties) | ✅ — re-checked in the real-world order (mint → write metadata → release) and again after a delay, in case a background reaper ran: the released hold still matches the deadline filter both times |
| The has-asset filter means *"ever held"*, not *"holds now"* | ✅ — a released, purged hold still matches; an asset nobody ever held returns empty, so the filter is genuinely applied |
| An account **without** the deadline key is excluded by the range filter | ✅ — which is what makes "clear the key on release" a working liveness marker |

The full vertical slice was then run on an isolated instance (its own port and control ledger): both
rules created through `POST /rules`, evaluated `FAIL`, and opened exactly three alerts — the
expired hold (`basis: expiry`, ~6h overdue), the hold with no expiry (`basis: created_at`, via the
48h fallback), and one warning (`dueInSeconds` ≈ 3h) — while the healthy hold and the released one
were correctly ignored.

---

## 7. What shipped

| Piece | Where |
|---|---|
| Template kind | `models.TemplateStaleHolds` ([rule.go](../../internal/models/rule.go)) |
| Evaluator | [internal/templates/stale_holds.go](../../internal/templates/stale_holds.go) — validate, augmented query, deadline resolution, one aggregate outcome per asset |
| Contract gating | V2 only ([contract_version.go](../../internal/api/service/contract_version.go)) |
| Tests | [stale_holds_test.go](../../internal/templates/stale_holds_test.go) (49 cases) + the live-ledger IT above |
| Public API | `StaleHoldsSpec` + `stale_holds` in `TemplateKind` ([openapi.yaml](../../openapi.yaml)) |
| Docs | [templates.md §6](./templates.md), the in-app guide, this design |
| Web UI | `StaleHoldsEditor` in [CreateRuleDialogV2.tsx](../../frontend/components/reconcile/panels/CreateRuleDialogV2.tsx), `StaleHoldsEvidence` in [V2Evidence.tsx](../../frontend/components/reconcile/V2Evidence.tsx) |
| Demo | `holds:*` book in `seed-data.sh`, the warn/stale rule pair in `seed-demo.mjs` |

**No new CEL builtin was added** (§4.3), and no PIT-read capability was assumed anywhere.

### Revised after the client's answer (2026-09-07)

Confirming one account per authorisation (§5, Q1) changed what an alert should *say*, not how the
check works — the deadline pushdown was already per-account. Two things were added: `identityKeys`,
to name the hold in an operator's own terms, and `maxHoldsScanned`, to let a rule cap its own read
below the engine's accounts budget.

**Superseded 2026-09-08 (§4.5).** `identityKeys` is removed with `per_hold`: with one aggregate per
asset there is no single hold for a label to name. `maxHoldsScanned` stays, now bounding the read
alone rather than standing in for an alert cap.

### Not done

- **The signal-B fallback** (age from the ledger's own `first_usage` / `insertion_date`, for a client
  with no deadline metadata) is designed (§3) but not built. It needs those fields plumbed through
  `engine.Account`, costs a full scan of the hold universe, and cannot render its age predicate in
  CEL — so it should only be built if Q2 comes back negative.
- **`warnWithin` vs the evaluation interval** is documented, not enforced. `Validate` sees only
  `templateSpec`; the check belongs in the service layer, where `schedule` is visible.

---

## 8. Still open with the client

Q1 came back confirmed (§5): **one account per authorisation, keyed by Authorization ID**. That
settles the structural question — it is why a per-hold *deadline* is usable at all. What is left:

1. **The deadline field — key name, declared type, unit. 🔴 The remaining blocker.**
   We know the processor supplies a per-payment expiration date; we do not know how it lands in the ledger.
   Needed: the **key name**, whether it is declared `datetime` or an integer (and in which unit), and
   — non-negotiable — that the key is **declared on the ledger *and* indexed**. `CreateIndex` refuses
   an undeclared field (*"metadata field not declared in schema"*), and an undeclared or unindexed key
   makes the rule unbuildable rather than merely slow. A plain RFC3339 **string** key cannot be
   filtered at all.
2. **What happens in the ledger when a hold is captured, voided, or expires?**
   Funds posting out (balance → 0) is assumed. The open part is whether the writer can *also* clear
   `hold_expires_at` — or set a status marker — in the same step. `EPHEMERAL` does **not** cover this
   (§5, Q2b): it retires the volume row, not the account row, so without an explicit write every hold
   ever placed keeps matching the rule's query. See *The teardown write* below for what to ask for.
3. **Re-authorisation and partial capture.**
   Does the processor ever revise an expiry (extension / re-auth)? Updating the account's deadline in place
   is fine — it is the hold's deadline, not a movement timestamp. Can a hold be *partially* captured,
   leaving a residual balance, and does the original expiry still govern that residual?
4. **Volume.** Authorisations per day, and the typical and worst-case number of *live* holds. The
   outcome shape no longer depends on the answer (§4.5), but the *read* does: it tells us how much
   headroom there is under the accounts budget, and therefore how urgent the teardown write is.

### The teardown write — what to ask for, and where it belongs

Q2b settled *that* an explicit write at release is required, and offered two shapes. Three things
about it were established later, and they change what to ask for.

**It is the posting that brings the balance to zero, not any posting that leaves the account.** A
partial capture also debits the hold account while leaving a residual — the hold is still live, its
deadline still governs, and clearing the key there would stop the rule from ever looking at it
again. That is the worst failure available here: funds still parked, deadline passing, monitoring
switched off without a trace. Item 3 above therefore gates this one — until we know whether partial
captures happen, the condition has to be written as *balance reached zero*, never *money moved out*.

**It costs one write, not two.** `DeleteAccountMetadataAction` is a standalone request the ledger
accepts in the same idempotent batch as the transaction, so the release posting carries the key
deletion atomically — no extra round-trip, and no window in which the hold is released but still
matching. (`ApplyMetadata` is the metadata-only shape, for a sweep with no posting; it needs no
numscript.) One sharp edge: the ledger **rejects deleting an absent key** and fails the whole batch,
so the writer must clear only keys it knows are present — harmless on a release path, a real hazard
for a retrying sweeper.

**Reconciliation must not do it itself.** The resolver handed to templates is read-only by
construction (`AggregateBalance` + `ListAccounts`), and every write in the module targets the control
ledger. Beyond the architectural point — the module observes, it does not mutate what it observes —
the race is disqualifying: reads are live, so between a zero balance being read and the delete
landing, the account can be re-funded, and reconciliation would strip the expiry off a hold that is
live again. A monitoring tool whose failure mode is quietly disabling its own monitoring is not an
acceptable trade for a smaller result set.

**So the first question is narrower than "can you clear the key".** It is: *what does the release
transaction already write?* If it stamps a status, a released-at, or a capture reference, the rule
filters on that today and nobody's code changes. Only if the answer is "nothing" does this become a
change request against the hold-writing path — which is the processor's, not the client's.

**Ledger-side follow-up: [EN-1972](https://formance-team.atlassian.net/browse/EN-1972)** (Ledger
v3.1 epic, EN-1336) asks for a live-volume predicate on account queries, so a zero-balance account
can be excluded with no client write at all. It is worth having — it removes the requirement that
every writer remembers, and makes a book written before the convention repairable without a
backfill — but it is a robustness feature, not the fix. If the release path clears the key, it stops
mattering here.

**Meanwhile, watch `holdsReleased`.** Every run already reports how many matched holds were dropped
on a zero balance, and nobody is looking at it. Alerting when that count passes a fraction of
`maxHoldsScanned` turns a silent budget exhaustion into a warning weeks before the rule stops
evaluating, and needs no write to customer data.

### Not needed after all

- **The `funds_held_since` watermark.** It was the fallback for holds aggregated on one account per
  card. With an account per authorisation, the real per-hold deadline is available and strictly
  better; maintaining a watermark alongside it would be redundant work for a weaker signal.
- **`ledger_invariant` for this check.** It asserts that a signed sum of balances nets to zero within
  tolerance, per asset — it has no notion of time and cannot express "this balance has not returned
  to zero past its deadline". Pointed at a holds book it would simply report that live holds exist,
  which is true of every healthy hold too. "Flag any account whose balance has not returned to zero
  past the expected timestamp" is exactly what `stale_holds` does, and is why it is a separate
  template rather than a configuration of an existing one.
