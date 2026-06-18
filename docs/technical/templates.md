# V1 GA Template Catalog

Templates are the **entire public V1 GA surface** — raw CEL is internal-only (see [ADR-001](../prd/adr-001-cel-kernel.md)). Each template is a typed spec, a validator, an explainer (for the persisted `compiled_cel`), and an end-to-end evaluator that produces one `Outcome` per fingerprint axis (per-asset for V1 GA).

> Status: all three templates are ✅ shipped in [internal/templates/](../../internal/templates/). The `account_threshold` per-account mode is rejected at validate-time with a clear "V1.1" message.

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
    Loop --> RenderCEL[Render per-axis CEL]
    RenderCEL --> Compile[engine.Compile]
    Compile --> Run[engine.Evaluate]
    Run --> Outcome[Outcome: fingerprint + passed + evidence]
    Loop --> Outcomes[List of Outcome]
```

Every template:

1. **Validates** the spec at rule-create time. Failures return `ErrInvalidSpec` (→ HTTP 400).
2. **Explains** itself — produces a representative CEL string for `rule.compiled_cel`. Not executed at runtime.
3. **Scouts** the asset universe at evaluation time (queries resolvers).
4. **Evaluates** per asset by rendering a fresh CEL string, compiling it via the kernel, and running it.
5. **Returns `[]Outcome`** — one per asset, with fingerprint, pass/fail, and evidence.

A **kernel/template consistency guard** in each template double-checks the kernel's verdict against direct big.Int math and errors loudly on divergence. Catches future kernel drift.

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
  "tolerance":      { "USD/2": 0, "EUR/2": 50 }   // optional; defaults to 0 per asset
}
```

**Validation**

- `ledger`, `ledgerQuery`, `paymentsPoolID` required
- `tolerance` values must be ≥ 0
- `ledgerQuery` must be a non-null, non-empty JSON value

**Asset universe**

Discovered at eval time = `union(ledgerBalances, poolBalances)`. **Every** asset present on either side is checked — including assets absent from `tolerance` (which then default to strict 0).

**Per-asset CEL** (the runtime form)

```
abs(balance(ledgerSet("buildr", "<query json>"), "USD/2")
  + balance(pool("0eb4a31f-…"), "USD/2")) <= 0
```

**Fingerprint** — `asset:<asset>` (e.g. `asset:USD/2`)

**Evidence** (per outcome)

```jsonc
{
  "asset":          "USD/2",
  "ledgerBalance":  "350",
  "poolBalance":    "-350",
  "drift":          "0",
  "signedDrift":    "0",
  "tolerance":      0,
  "compiledCEL":    "abs(balance(ledgerSet(\"buildr\", \"…\"), \"USD/2\") + balance(pool(\"…\"), \"USD/2\")) <= 0"
}
```

**Sign convention** — the rule assumes `ledger + payments == 0` per asset; i.e. ledger balances are *negative* (obligations) and payments *positive* (cash held). This is inherited from the legacy semantics and is captured in the persisted `signedDrift` so the operator can see whether the asymmetry is "more cash than expected" or "less."

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

```
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

### 3. `account_threshold` (✅ aggregate mode shipped · ❌ per_account mode is V1.1)

Per-asset min/max bounds on the aggregated balance of a ledger account set.

**Spec**

```jsonc
{
  "ledger": "acme",
  "query":  { "$match": { "address": "treasury:operating:" } },
  "mode":   "aggregate",                          // V1 GA only
  "bounds": {
    "USD/2": { "min": 100000, "max": 5000000 },
    "EUR/2": { "min": 50000 }                     // one-sided: only min
  }
}
```

**Validation**

- `ledger` and `query` required
- `mode` must be `"aggregate"` (V1 GA). `"per_account"` returns `ErrInvalidSpec` with: *"mode 'per_account' is not yet implemented in V1 GA — landing with the accounts() CEL builtin in V1.1"*
- `bounds` must be non-empty; each entry needs at least one of `min` or `max`; if both set, `min <= max`

**Asset universe**

`Bounds` keys define the asset universe.

**Per-asset CEL** (combines whichever bounds are set with `&&`)

```
balance(ledgerSet("acme", "<query>"), "USD/2") >= 100000
  && balance(ledgerSet("acme", "<query>"), "USD/2") <= 5000000
```

If only `min` is set: `balance(...) >= 100000`. If only `max` is set: `balance(...) <= 5000000`.

**Fingerprint** — `asset:<asset>` (aggregate mode)
**Fingerprint** — `asset:<asset>|account:<address>` (per-account mode, V1.1)

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

## V1.1 carve-outs (❌ not in V1 GA)

The following templates are part of the spec roadmap but require kernel work that isn't shipped yet:

| Template | Needed kernel work |
|---|---|
| `account_threshold` per_account | `accounts(source)` CEL builtin + `Account` as a CEL struct |
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

func (*MyTemplate) Evaluate(ctx, spec, eng, resolvers, in) ([]Outcome, error) {
    // 1. Scout asset/account universe via resolvers.
    // 2. For each fingerprint axis value:
    //    a. Render per-axis CEL string.
    //    b. eng.Compile(expr).
    //    c. eng.Evaluate(compiled, in).
    //    d. (Optional) cross-check direct math vs kernel result.
    // 3. Return one Outcome per axis value.
}
```

Register it in `templates.DefaultRegistry()` ([internal/templates/template.go](../../internal/templates/template.go)) and add its `TemplateKind` constant in [internal/models/rule.go](../../internal/models/rule.go).

The test pattern is well-established — see [internal/templates/templates_test.go](../../internal/templates/templates_test.go) for the round-trip shape (fake resolvers, mustJSON helper, fingerprint assertions, validation table tests).
