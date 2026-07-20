# V1 GA Template Catalog

Templates are the **entire public V1 GA surface** — raw CEL is internal-only (see [ADR-001](../prd/adr-001-cel-kernel.md)). Each template is a typed spec, a validator, an explainer (for the persisted `compiled_cel`), and an end-to-end evaluator that produces one `Outcome` per fingerprint axis (per-asset for V1 GA).

> Status: all four templates are ✅ shipped in [internal/templates/](../../internal/templates/), including `account_threshold` per-account mode.

---

## How templates work

```mermaid
flowchart LR
    Spec[Typed Spec] --> Validate
    Spec --> Explain[Explain → representative CEL]
    Explain --> Persist[Saved to rule.compiled_cel]
    Spec --> Evaluate
    Evaluate --> Scout[Scout via SDK resolvers]
    Scout --> Universe[Determine fingerprint axis\n(asset universe)]
    Universe --> Loop[For each axis value]
    Loop --> SnapshotCEL[Render CEL over scouted snapshot values]
    Loop --> RenderCEL[Render per-axis CEL → evidence.compiledCEL]
    SnapshotCEL --> Kernel[Engine.EvaluateBatch\nshared CEL cost budget]
    Kernel --> Outcome[Outcome: fingerprint + passed + evidence]
    RenderCEL --> Outcome
    Loop --> Outcomes[List of Outcome]
```

Every template:

1. **Validates** the spec at rule-create time. Failures return `ErrInvalidSpec` (→ HTTP 400).
2. **Explains** itself — produces a representative CEL string for `rule.compiled_cel`. Not executed at runtime.
3. **Scouts** the asset universe at evaluation time (queries resolvers).
4. **Evaluates** per asset through CEL over the already-scouted snapshot values. All fingerprints share one wall-clock deadline and CEL-cost budget; no Ledger or Payments read is repeated.
5. **Returns `EvaluationResult`** — outcomes plus the PIT actually used per source and cumulative CEL runtime cost.

The source-shaped CEL saved in `evidence.compiledCEL` remains the explainable invariant. At runtime the template substitutes the balances it just read into an equivalent snapshot expression and executes that through `Engine.EvaluateBatch`. This makes CEL authoritative and enforces its cost limit without the TOCTOU and remote-call cost of resolving every source again. `TestKernelParity_Aggregate` verifies that the source-shaped expression and snapshot verdict remain equivalent.

Balance reads are centralised in a shared **Source** primitive ([source.go](../../internal/templates/source.go)): a `ledger` or `payments_pool` descriptor that knows how to resolve to per-asset balances and render its `balance(ledgerSet…|pool…)` CEL term. `source_parity` and `ledger_vs_pool_drift` both compose sources through it, so there is one code path for "read a balance source".

**Scope.** A ledger source can be read in one of two scopes, a native capability of the Source primitive:
- **aggregate** (default): the matched account set is summed into one balance per asset. A query matching a single account is the degenerate single-account case — so "single account" and "set of accounts" are both aggregate, differing only in the query.
- **per_account**: the source fans out — each matched account is evaluated individually, producing one Outcome per (account, asset) with the account address as the fingerprint axis. Available only where every source involved is a ledger source (a payments pool has no per-account breakdown, so it stays aggregate-only). The account address is the alignment key when two ledger sources are compared per account. Fan-out is bounded by the engine's `MaxAccountsScanned` budget.

---

## Catalog

### 1. `ledger_vs_pool_drift` (✅ shipped)

Port of today's legacy reconciliation. Compares a dynamic ledger account set against a dynamic payments pool, per asset.

**Spec**

```jsonc
{
  "ledger":         "buildr",
  "ledgerQuery":    { "$match": { "metadata[trust]": "true" } },
  "paymentsPoolID": "0eb4a31f-751e-42d4-8d5b-2129e6d4cf4c",
  "ledgerSign":     -1,                              // optional; +1 (default) or -1
  "tolerance":      { "USD/2": 0, "EUR/2": 50 }      // optional; defaults to 0 per asset
}
```

**Validation**

- `ledger`, `ledgerQuery`, `paymentsPoolID` required
- `ledgerSign` must be `+1`, `-1`, or omitted (defaults to `+1`)
- `tolerance` values must be ≥ 0
- `ledgerQuery` must be a non-null, non-empty JSON value

**Asset universe**

Discovered at eval time = `union(ledgerBalances, poolBalances)`. **Every** asset present on either side is checked — including assets absent from `tolerance` (which then default to strict 0).

**Per-asset CEL** (rendered into `evidence.compiledCEL`; not executed at eval time). With `ledgerSign: -1` the ledger term carries a leading minus:

```cel
abs(-balance(ledgerSet("buildr", "<query json>"), "USD/2")
  + balance(pool("0eb4a31f-…"), "USD/2")) <= 0
```

**Fingerprint** — `asset:<asset>` (e.g. `asset:USD/2`)

**Evidence** (per outcome)

```jsonc
{
  "asset":            "USD/2",
  "ledgerBalanceRaw": "350",   // raw, before ledgerSign
  "ledgerBalance":    "-350",  // signed contribution to the sum
  "ledgerSign":       -1,
  "poolBalance":      "350",
  "drift":            "0",
  "signedDrift":      "0",
  "tolerance":        0,
  "compiledCEL":      "abs(-balance(ledgerSet(\"buildr\", \"…\"), \"USD/2\") + balance(pool(\"…\"), \"USD/2\")) <= 0"
}
```

**Sign convention** — the rule checks `abs(ledgerSign·ledger + pool) <= tolerance` per asset.

- `ledgerSign = +1` (default) preserves legacy semantics: the ledger side is expected to be the *negative* of the pool side, so `ledger + pool == 0`. Customers porting from legacy `/policies` keep this default.
- `ledgerSign = -1` is the symmetric case: both sides naturally positive (e.g. a "held" account on the ledger compared against the pool's cash balance). Lifts the prior limitation that ledger balances had to be negative for reconciliation to balance out.

The signed contribution is captured in `evidence.ledgerBalance`; the pre-sign raw value is preserved in `evidence.ledgerBalanceRaw` so audit consumers can cross-check directly against the ledger UI.

**Code**: [internal/templates/ledger_vs_pool_drift.go](../../internal/templates/ledger_vs_pool_drift.go)

---

### 2. `ledger_invariant` (✅ shipped)

Buildr-style "sum of signed balances must net to zero (within tolerance)" check. Multiple terms over different ledger queries; each carries a `+1` or `-1` sign.

**Spec**

```jsonc
{
  "terms": [
    { "ledger": "buildr", "query": { "$match": { "metadata[trust]": "held" } },       "sign":  1 },
    { "ledger": "buildr", "query": { "$match": { "metadata[trust]": "obligation" } }, "sign": -1 }
  ],
  "tolerance": { "USD/2": 0, "EUR/2": 0 }       // required; asset universe = these keys
}
```

**Validation**

- `terms` must be non-empty; each term needs `ledger`, `query`, and `sign ∈ {+1, -1}`
- `tolerance` must be non-empty; values ≥ 0
- `query` must be non-null, non-empty JSON

**Asset universe**

`Tolerance` keys define the asset universe — assets present on terms but missing from `tolerance` are **not** checked. This is deliberate: opting in by listing an asset in `tolerance` keeps the rule's scope explicit.

**Per-asset CEL**

```cel
abs(balance(ledgerSet("buildr", "<held query>"), "USD/2")
  + -balance(ledgerSet("buildr", "<obligation query>"), "USD/2")) <= 0
```

(Negative-sign terms emit `-balance(...)`; positive terms emit `balance(...)`. Joined with `+`. Wrapped in `abs() <= TOL`.)

**Fingerprint** — `asset:<asset>`

**Evidence**

```jsonc
{
  "asset":      "USD/2",
  "signedSum":  "0",
  "absDrift":   "0",
  "tolerance":  0,
  "termValues": ["350", "-350"],
  "compiledCEL": "abs(balance(…, \"USD/2\") + -balance(…, \"USD/2\")) <= 0"
}
```

**Code**: [internal/templates/ledger_invariant.go](../../internal/templates/ledger_invariant.go)

---

### 3. `account_threshold` (✅ shipped — aggregate + per_account)

Per-asset min/max bounds on a ledger account set, either aggregated or per account.

**Spec**

```jsonc
{
  "ledger": "acme",
  "query":  { "$match": { "address": "treasury:operating:" } },
  "mode":   "aggregate",                          // "aggregate" (default) | "per_account"
  "bounds": {
    "USD/2": { "min": 100000, "max": 5000000 },
    "EUR/2": { "min": 50000 }                     // one-sided: only min
  }
}
```

**Validation**

- `ledger` and `query` required
- `mode` must be `"aggregate"` (default) or `"per_account"`
- `bounds` must be non-empty; each entry needs at least one of `min` or `max`; if both set, `min <= max`

**Scope (`mode`)** — see [the scope model](#how-templates-work):
- `aggregate`: bounds are checked against the summed balance of the matched set (one query matching one account is the degenerate single-account case). One Outcome per asset.
- `per_account`: bounds are checked against **each** matched account individually — one Outcome per (account, asset). Accounts are read via `ListAccounts` (volumes), bounded by the engine's `MaxAccountsScanned` budget (the resolver errors rather than truncating). As on the aggregate path, CEL evaluates the scouted snapshot values and the source-shaped expression is retained in evidence.

**Asset universe**

`Bounds` keys define the asset universe.

**Per-asset CEL** (combines whichever bounds are set with `&&`)

```cel
balance(ledgerSet("acme", "<query>"), "USD/2") >= 100000
  && balance(ledgerSet("acme", "<query>"), "USD/2") <= 5000000
```

If only `min` is set: `balance(...) >= 100000`. If only `max` is set: `balance(...) <= 5000000`. In `per_account` mode the ledgerSet query is narrowed to a single account address.

**Fingerprint** — `asset:<asset>` (aggregate) · `asset:<asset>|account:<address>` (per_account)

**Evidence**

```jsonc
{
  "asset":       "USD/2",
  "balance":     "523500",
  "min":         100000,
  "max":         5000000,
  "compiledCEL": "balance(…, \"USD/2\") >= 100000 && balance(…, \"USD/2\") <= 5000000"
}
```

**Code**: [internal/templates/account_threshold.go](../../internal/templates/account_threshold.go)

---

### 4. `source_parity` (✅ shipped)

"Two independent records of the same money agree, per asset, within tolerance." Each side is a **Source** — a ledger account set *or* a payments pool — so one template expresses ledger↔pool (the drift use case), **ledger↔ledger** (a sub-ledger reconciled against a control account on another ledger), and pool↔pool, without a bespoke template per pairing.

**Spec**

```jsonc
{
  "left":      { "kind": "ledger",        "ledger": "main", "query": { "$match": { "address": "stripe-clearing" } } },
  "right":     { "kind": "payments_pool", "poolID": "0eb4a31f-…" },
  "scope":     "aggregate",                  // "aggregate" (default) | "per_account"
  "tolerance": { "USD/2": 0, "EUR/2": 50 }   // optional; defaults to 0 per asset
}
```

A `SourceSpec` is `{ "kind": "ledger" | "payments_pool", ... }`:
- `ledger` → requires `ledger` + `query` (read point-in-time at the source's PIT)
- `payments_pool` → requires `poolID` (read point-in-time via `/v3/pools/{id}/balances?at=` when an explicit past PIT is requested; latest for the as-of-now default, since a PIT read at ~now hits the empty balance-window tail)

Each source resolves at its own PIT: the evaluation's default `at`, or a per-source override supplied in `sourcePITs` (keyed by the source's stable `"<label>#<idx>"` key). This is the two-independent-timestamps contract — e.g. ledger and pool read a settlement cycle apart — and it applies to every multi-source template. Residual cross-system skew is still absorbed by `tolerance`.

**Scope** — `aggregate` (default) compares the two sources' summed balances. `per_account` compares them **account-by-account, aligned by address**, emitting one Outcome per (account, asset) — e.g. reconcile each merchant's balance on ledger A against ledger B. It requires **both** sides to be ledger sources (a pool is aggregate-only); see [the scope model](#how-templates-work).

**Validation** — each side: known `kind` with its required fields; `tolerance` values ≥ 0; `per_account` scope requires both sides to be ledger sources.

**Asset universe** — `union(leftBalances, rightBalances)`; every asset on either side is checked, missing-side defaults to 0.

**Per-asset CEL** (rendered into `evidence.compiledCEL`; not executed at eval time)

```cel
abs(balance(ledgerSet("main", "<query json>"), "USD/2") - balance(pool("0eb4a31f-…"), "USD/2")) <= 0
```

**Fingerprint** — `asset:<asset>` (aggregate) · `asset:<asset>|account:<address>` (per_account)

**Evidence** — `{ asset, leftSource, leftBalance, rightSource, rightBalance, difference (abs), signedDiff, tolerance, compiledCEL }` (`leftSource`/`rightSource` are labels like `ledger:main` / `pool:…`; the matching `pit_per_source` keys carry the `#idx` suffix, e.g. `ledger:main#0`; per_account also carries `account`).

**Relation to `ledger_vs_pool_drift`** — `source_parity` is the equality primitive (`abs(left − right) ≤ tol`). `ledger_vs_pool_drift` is a sum-to-zero relation with a configurable `ledgerSign`; it now resolves and renders both sides through the same shared `Source` primitive ([source.go](../../internal/templates/source.go)) — one code path for "read a balance source" — and layers only its sign arithmetic on top. (Collapsing drift's signed sum and `ledger_invariant`'s N-term sum into a single signed-combination template is a possible future consolidation.) An external bank/PSP-account source kind is a natural next addition once it has a resolver + kernel builtin.

**Code**: [internal/templates/source_parity.go](../../internal/templates/source_parity.go), [internal/templates/source.go](../../internal/templates/source.go)

---

## V1.1 carve-outs (❌ not in V1 GA)

The following templates are part of the spec roadmap but require kernel work that isn't shipped yet:

| Template | Needed kernel work |
|---|---|
| `account_inactivity`            | `ledgerPostings(...)` source + `lastActivity(source)` builtin + `now()` |
| `posting_rate`                  | `postings(source).count` builtin |
| `metadata_invariant`            | `accounts(source).all(a, has(a.metadata.X))` |
| `cross_account_ratio`           | Per-account iteration + multi-source comparison |

See [PRD §6.2](../prd/README.md#62-v11-fast-follow-catalog) for the V1.1 catalog plan.

---

## Adding a new template

Each template is ~150 LOC + ~80 LOC of tests. The minimal contract:

```go
type MyTemplate struct{}

func (*MyTemplate) Kind() models.TemplateKind { return "my_template" }

func (*MyTemplate) Validate(spec json.RawMessage) error {
    // Parse + check required fields. Return ErrInvalidSpec on failure.
}

func (*MyTemplate) Explain(spec json.RawMessage) (string, error) {
    // Return a representative CEL string for rule.compiled_cel.
}

func (*MyTemplate) Evaluate(ctx, spec, eng, resolvers, in) (*EvaluationResult, error) {
	// 1. Scout the asset/account universe via resolvers.
	// 2. Render one source-shaped expression for evidence and one equivalent
	//    snapshot-value expression for each fingerprint axis.
	// 3. Evaluate the snapshot expressions through Engine.EvaluateBatch.
	// 4. Return outcomes, actual source PITs, and cumulative CEL cost.
}
```

Register it in `templates.DefaultRegistry()` ([internal/templates/template.go](../../internal/templates/template.go)) and add its `TemplateKind` constant in [internal/models/rule.go](../../internal/models/rule.go).

The test pattern is well-established — see [internal/templates/templates_test.go](../../internal/templates/templates_test.go) for the round-trip shape (fake resolvers, mustJSON helper, fingerprint assertions, validation table tests).
