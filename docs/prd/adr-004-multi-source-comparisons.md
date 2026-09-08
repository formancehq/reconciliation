# ADR-004 — Versioned multi-source comparisons

**Status:** Accepted for V2 implementation

**Date:** 2026-07-18

**Decision owners:** Reconciliation maintainers
**Related:** [ADR-001](./adr-001-cel-kernel.md), [ADR-003](./adr-003-checkpoint-anchor-and-crosscheck.md), [template catalog](../technical/templates.md)

## Context

V1 `source_parity` answers one deliberately narrow question:

```text
abs(left - right) <= tolerance
```

Its `templateSpec.left` / `templateSpec.right` fields and
`leftSource` / `leftBalance` / `rightSource` / `rightBalance` evidence are already persisted in
rules, captures, alerts, and accepted evidence snapshots. Renaming or changing their meaning in
place would make old evidence ambiguous and could break consumers that bind those exact keys.

The next set of reconciliation controls requires more than two inputs and more than binary parity:

- prove that two account sets sum to a third account set;
- express a signed balance equation over several ledgers or metadata-backed balances;
- compare balances denominated in different assets against an exchange-rate range;
- preserve the exact values and arithmetic that produced the verdict.

Extending V1 `source_parity` with an optional third side would not define the invariant. “All values
are equal”, “compare each value with the first”, and “a signed sum is zero” are different statements.
The exchange-rate case also needs asset precision and ratio semantics that do not exist in parity.

## Decision

### 1. Add a V2 contract; do not rewrite V1

The unprefixed V1 routes and response bodies remain unchanged. V2 resources use `/v2` routes and an
immutable persisted contract marker:

- a missing marker decodes as V1 (`contractVersion = 1`);
- new V1 resources are persisted with version 1;
- V2 resources are persisted with version 2;
- the route, not the client body, selects the contract version;
- V2 responses expose read-only `contractVersion: 2`;
- list, get, evaluation, capture, and alert actions are isolated by contract version.

There is no automatic conversion, backfill, or evidence rewrite. A V1 rule remains executable as
V1 for its lifetime. Creating the equivalent V2 rule is an explicit operator action.

### 2. Use named aggregate sources

All V2 operations share an ordered `sources[]` collection. Each entry is:

```json
{
  "id": "book",
  "label": "Customer balance",
  "kind": "ledger",
  "ledger": "main",
  "query": { "$match": { "address": "accounts:customer:*" } },
  "asset": "USD/2"
}
```

`id` is the stable machine reference used by terms, fingerprints, and evidence. `label` is optional
operator-facing text. `kind` defaults to `ledger`; `account_metadata` additionally requires
`metadataKey`. Every source declares one `asset`, including ledger sources, so an operation never
guesses how values with different denominations should align.

V2 is aggregate-only. Per-account equations would require an explicit alignment key and missing-row
semantics; per-account exchange rates would additionally require pair alignment. Those decisions are
outside this ADR.

> **Amendment (2026-09-07) — per-account fan-out is not carried forward, and V1 is retired.**
> With `balance_bounds` shipped, every V1 template is expressible in the named-source model:
> `source_parity` and `ledger_invariant` as signed `balance_equation`s, `account_threshold` as
> `balance_bounds`, and multi-asset rules as one rule via the wildcard below. The one capability with
> no equivalent is **per-account fan-out** — `account_threshold` mode `per_account` and
> `source_parity` scope `per_account` — and it is **deliberately dropped**, not overlooked. It was
> already parked out of the V1 GA surface once (commit `1bd99ecd`) for unbounded PASS evidence;
> reviving it in the named-source model needs an explicit alignment key and missing-row semantics
> (§2), which no shipped rule asks for. Recorded here so retirement is a decision rather than a side
> effect of a cleanup.
>
> This does **not** touch `stale_holds`' `per_hold` scope, which is a different thing and stays: it
> emits outcomes only for *failing* holds, so its evidence is proportional to the problem rather than
> to the account set, and it is bounded twice over — by the rule's own `maxHoldsScanned` and by the
> service's new-alert cap.

> **Amendment (2026-09-07) — the asset wildcard.** Requiring every source to name one asset made a
> multi-asset V1 rule inexpressible: a `source_parity` rule with `tolerance: {USD/2: 0, EUR/2: 0}` is
> one rule emitting one outcome per asset, and its V2 equivalent was one rule *per asset*. That gap
> was the reason the V1 catalogue could not be migrated, only re-implemented.
>
> A source may now declare `asset: "*"` — every asset the account set holds — and the operation fans
> out to one outcome per asset. This does not reintroduce the guessing §2 rules out: alignment is by
> exact asset code, and a spec must be wholly wildcard or wholly named, because a mixed spec is
> precisely the case where an operation would have to decide how a `USD/2` source lines up with an
> "any asset" one. It is rejected on `account_metadata` sources (the key represents one declared
> asset) and by `exchange_rate_bounds` (a rate is a statement about one named pair).
>
> `TestAssetWildcard_MatchesV1MultiAssetParity` pins the claim: a V1 multi-asset `source_parity` rule
> and the single V2 `balance_equation` that replaces it produce the same outcomes against the same
> ledger.

> **Amendment (2026-09-08) — the `/v2` prefix is removed.** Decision 1 above put the named-source
> contract on `/v2` routes so it could run beside the unprefixed V1 contract. With V1 retired the
> prefix distinguished nothing, and no client depended on it, so the surface is now unversioned:
> `/rules` and `/alerts`. The immutable contract marker is unaffected — it was never derived from
> the path, and it still gates the V2-only wire fields and scopes resource visibility. The routes
> are the only thing that moved.

### 3. Define `balance_equation` as a signed sum

The operation is:

```text
abs(sum(coefficient_i * balance_i)) <= tolerance
```

For `A + B = C`, the coefficients are `+1`, `+1`, and `-1`:

```json
{
  "sources": [
    { "id": "a", "ledger": "main", "query": {}, "asset": "USD/2" },
    { "id": "b", "ledger": "main", "query": {}, "asset": "USD/2" },
    { "id": "c", "ledger": "control", "query": {}, "asset": "USD/2" }
  ],
  "terms": [
    { "source": "a", "coefficient": 1 },
    { "source": "b", "coefficient": 1 },
    { "source": "c", "coefficient": -1 }
  ],
  "tolerance": "0"
}
```

All sources must declare the same asset. Each source is referenced exactly once, and coefficients
are non-zero signed integers. `tolerance` is a non-negative base-10 integer string in the declared
asset's minor units. Strings avoid JSON-number precision loss in clients and persisted evidence.

The outcome fingerprint is `asset:<asset>`.

### 4. Define `exchange_rate_bounds` as an exact signed ratio

The exchange-rate convention is:

```text
observed rate = quote asset major units / base asset major unit
```

For base minor-unit balance `B`, base precision `pB`, quote minor-unit balance `Q`, and quote
precision `pQ`:

```text
observed rate = (Q * 10^pB) / (B * 10^pQ)
```

The implementation compares these integers by cross-multiplication. It does not convert to
`float64`, round a displayed decimal, or take absolute values.

Bounds use exactly one of these modes:

```json
{ "min": "1.075", "max": "1.125" }
```

or:

```json
{ "target": "1.10", "toleranceBps": 25 }
```

`min`, `max`, and `target` are positive plain-decimal strings with at most 18 fractional digits; a
sign or exponent notation is not accepted. `toleranceBps` is an integer from 0 through 10,000.
Bounds are inclusive. Target mode derives exact lower and upper bounds as:

```text
target * (10,000 - toleranceBps) / 10,000
target * (10,000 + toleranceBps) / 10,000
```

The ratio remains signed. Two negative balances yield a positive rate; balances with opposite signs
yield a negative rate and therefore fail positive bounds. A zero quote balance is a defined rate of
zero and is evaluated normally. A zero base balance has no defined rate and produces a failed
outcome with `undefinedReason: "base_balance_zero"`; it is not an engine error.

The outcome fingerprint is `baseSource:<base-id>|quoteSource:<quote-id>`.

### 5. Define `source_consensus` as symmetric agreement

The operation is:

```text
all sources present AND max(balance_i) - min(balance_i) <= tolerance
```

All sources declare the same asset. A missing declared ledger asset remains `balance: "0"` and
`present: false` in evidence but makes the consensus outcome fail. There is no quorum or privileged
reference source: every declared source participates, and the minimum/maximum pair proves the widest
disagreement. The outcome fingerprint is `asset:<asset>`.

### 6. Define `coverage_ratio_bounds` as a ratio of portfolios

The operation is:

```text
minRatio <= sum(numerator coefficient_i * balance_i)
           / sum(denominator coefficient_j * balance_j) <= maxRatio
```

Every source declares the same asset and is assigned exactly once to one portfolio. Both portfolios
are non-empty and accept non-zero signed integer coefficients. Bounds use the same exact explicit or
target-plus-basis-points shapes as `exchange_rate_bounds`. A zero denominator total produces a failed
outcome with `undefinedReason: "denominator_total_zero"`; it is not an engine error. The outcome
fingerprint is `asset:<asset>`.

### 7. Preserve exact, self-describing V2 evidence

Every V2 evidence object carries `schemaVersion: 2` and an `operation` discriminator. Source entries
are keyed by their stable `id` and retain the declared asset, effective kind, observed balance as a
base-10 integer string, and whether that asset was present.

Equation evidence records coefficients, contributions, residual, absolute residual, and tolerance.
Exchange-rate evidence records base and quote snapshots and the exact observed rate. Consensus
evidence records the minimum and maximum source/value pair, full spread, and missing sources.
Coverage evidence records both portfolio totals, every contribution, and the exact reduced ratio.
Each operation retains its canonical `compiledCEL`; undefined ratios omit the observed value and
carry a typed `undefinedReason`.

The V2 CEL financial built-ins are backed by exact `big.Int` / `big.Rat` arithmetic. They do not use
binary floating point. The rule and evidence retain `compiledCEL` as the canonical explanation and
cross-check surface.

V2 evidence never emits the V1 `left*` or `right*` keys.

### 8. Bound and validate the contract

- `sources` contains between 2 and 32 entries; `exchange_rate_bounds` requires exactly two.
- IDs are unique and match `^[A-Za-z][A-Za-z0-9_-]{0,63}$`.
- Every query is validated against its ledger before the rule is persisted.
- A ledger source missing its declared asset resolves to balance `0` with `present: false`.
  `source_consensus` additionally requires every source to be present.
- Missing or non-integer `account_metadata` values remain evaluation errors; they are not silent zero.
- Validation errors use source IDs or labels for people and retain machine paths such as
  `sources[1].asset` for clients.

## Consequences

### Positive

- V1 persisted rules and evidence remain readable without migration.
- Named sources are stable when a control grows beyond two inputs.
- Signed equations state the mathematical invariant instead of inferring it from position.
- Exact integer and rational arithmetic is deterministic across platforms and SDK languages.
- Evidence is sufficient to reproduce a verdict without re-reading mutable ledger state.

### Costs and limitations

- V1 and V2 handlers, schemas, and compatibility tests must coexist.
- V2 sources declare one asset each; a multi-asset control needs separate rules.
- Live reads across sources are sequential rather than a certifiable atomic multi-ledger cut.
  Tolerances or rate bounds must account for acceptable ingestion skew.
- V2 does not provide arbitrary expression trees, dynamic FX feeds, weighted decimal coefficients,
  or per-account alignment.

## Alternatives considered

### Extend `source_parity` with `sources[]`

Rejected. It would overload a stable binary contract and still leave multi-source semantics
ambiguous. It also cannot describe `A + B = C` without adding a second expression language.

### Replace V1 fields in place

Rejected. Persisted rules, captures, alerts, acceptance snapshots, tests, and external consumers may
depend on the exact existing keys.

### Expose raw CEL

Rejected for this surface. Raw CEL is harder to validate, document, meter, and reproduce safely.
The four V2 operations cover distinct reconciliation jobs with typed contracts.

### Use floating-point exchange rates

Rejected. Binary floating point introduces boundary-dependent verdicts and cannot safely preserve
large ledger balances. Cross-multiplied integers provide exact comparisons.

### Make consensus quorum configurable

Rejected for the current contract. A quorum can allow a missing or stale record to disappear from
the invariant precisely when it matters. `source_consensus` therefore requires every declared source
to be present. A future quorum control would need a separate template and explicit evidence for
membership, exclusions, and minimum agreeing weight.

## Migration

No data migration runs when V2 is deployed. Existing records without a contract marker are treated
as V1. V1 APIs continue returning their existing bodies, including legacy left/right evidence.

An operator who wants V2 semantics creates a new V2 rule, validates it in parallel, then disables or
deletes the old rule according to their operational process. Historical V1 captures, alerts, and
accepted evidence snapshots remain attached to the V1 rule and are never rewritten.
