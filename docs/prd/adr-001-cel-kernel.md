# ADR-001 — Engine kernel: CEL over a typed object model

**Status:** Accepted (implemented in [`internal/engine/`](../../internal/engine/))
**Linked from:** [PRD §5](./README.md), [architecture.md](../technical/architecture.md)
**Last updated:** 2026-06-17

---

## 1. Decision in one sentence

The reconciliation rule engine's evaluation core is **Google's Common Expression Language (CEL) bound to a typed object model**, with `Source` as a first-class abstraction and resolvers (`LedgerSet`, `PaymentsPool`, `LedgerPostings`) registered as runtime backends.

---

## 2. Why this is on the table

The current implementation hardcodes one rule shape: ledger metadata query vs payments pool, drift == 0, per asset. Generalizing to N customer asks, N source kinds, and N predicates requires deciding **how a rule is represented** before we ship anything.

That decision is hard to revisit. Once customers author rules — especially in EE+ power-mode where they reference builtin names — renaming or restructuring breaks contracts. Pick once, live with it.

---

## 3. Requirements the choice must satisfy

| #  | Requirement                                                                                  | Type   |
| -- | -------------------------------------------------------------------------------------------- | ------ |
| 1  | **Safety** — customer expressions cannot DOS the engine, access I/O, or fail to terminate    | must   |
| 2  | **Static type-check** — bad expressions caught at rule-creation, not at 3 AM                  | must   |
| 3  | **Ergonomic authoring** — GitOps (YAML), one-line readable, diffable in PRs                  | must   |
| 4  | **Extensibility** — new sources & functions land without re-shaping the public API           | must   |
| 5  | **Mature runtime** — we don't maintain a parser/evaluator from scratch                       | must   |
| 6  | **Backwards compat** — existing Policy continues to evaluate transparently                   | must   |
| 7  | **Bounded eval cost** — predictable performance, hard ceilings                              | should |

---

## 4. Same rule, six representations

The Buildr trust-integrity check, expressed in each candidate. This is the most honest comparison artifact.

```yaml
# A — Typed Go structs (status quo extended)
type: ledger_invariant
spec:
  terms:
    - { ledger: buildr, query: { metadata: "trust.held=*" },       sign: +1 }
    - { ledger: buildr, query: { metadata: "trust.obligation=*" }, sign: +1 }
  tolerance: { USD: 0 }
```

```yaml
# B — JSON-AST (JSON Logic-style)
when:
  op: eq
  lhs:
    fn: sum
    args:
      - { fn: balance, args: [{ fn: ledgerSet, args: ["buildr", "trust.held=*"] }] }
      - { fn: balance, args: [{ fn: ledgerSet, args: ["buildr", "trust.obligation=*"] }] }
  rhs: 0
```

```yaml
# C — CEL (chosen)
when: |
  balance(ledgerSet("buildr", "trust.held=*"))
  + balance(ledgerSet("buildr", "trust.obligation=*")) == 0
```

```yaml
# D — Rego / OPA
when: |
  package recon
  default ok = false
  ok {
    sum([b | src := input.sources[_]; b := balance(src)]) == 0
  }
```

```yaml
# E — Starlark (real language, unbounded loops, loses static safety)
when: |
  def check(ctx):
      held = balance(ledger_set("buildr", "trust.held=*"))
      obl  = balance(ledger_set("buildr", "trust.obligation=*"))
      return held + obl == 0
```

```yaml
# F — Numscript extension (hypothetical, not pursued)
# Numscript is a posting language, not a predicate language.
```

Read them side by side. **C is the only option that's safe, typed, one-line, and uses a runtime we don't maintain.**

---

## 5. Options matrix

| Opt | Safe? | Typed? | YAML-friendly? | Extensible | Mature impl? | Verdict |
| --- | ----- | ------ | -------------- | ---------- | ------------ | ------- |
| **A** Typed Go structs only | ✅ | ✅ | medium | engine PR per new rule type | ✅ (ours) | Fails (4) — every customer ask = engine PR |
| **B** JSON-AST (we evaluate) | ✅ | medium | poor (verbose) | ✅ | we'd own it | Bad ergonomics + bus factor |
| **C** CEL (`cel-go`) | ✅ | ✅ static | ✅ | ✅ (custom builtins) | ✅ (Google) | **Chosen** |
| **D** Rego / OPA | ✅ | medium | poor (Datalog) | ✅ | ✅ (OPA) | Wrong abstraction for numeric predicates |
| **E** Starlark / Lua / Tengo | partial | ❌ | medium | ✅ | ✅ | Loops + unbounded compute = wrong shape |
| **F** Numscript extension | depends | partial | medium | partial | partial | Mutates Numscript's identity |
| **G** JS / WASM sandbox | hard | ❌ | medium | ✅ | ✅ | Huge attack & resource surface |
| **H** Custom DSL | depends | ✅ | ✅ | ✅ | we'd maintain | Years to catch up to CEL on tooling |

---

## 6. Why CEL specifically

1. **Safety is built in, not enforced by a library.** No I/O. No loops. Guaranteed termination. cel-go exposes a cost-limit API per evaluation.
2. **Statically typed**, with type-checking at *rule-creation time*. `balance("foo") + "bar"` gets a 400 at `POST /rules`, not a stack trace at 3 AM.
3. **One-line readable in YAML.** Reviewable in a PR.
4. **Custom functions are first-class.** Registering `balance(source) → int`, `lastActivity(source) → timestamp`, etc., is the supported extension model — not a hack on top.
5. **Production-grade Go runtime we don't maintain.** cel-go is used by Kubernetes admission controllers, Envoy, GCP IAM, Cerbos. Google won't drop it.
6. **C-family syntax** — engineers parse it on first read. No tutorial.

---

## 7. The typed object model

CEL is only as good as the types it reasons about. The object model **is the actual product surface** — once customers write expressions against these types, renaming is a breaking change.

> **Update (ledger-only, 2026-07-09).** Reconciliation shipped as strictly **ledger↔ledger**: the
> `PaymentsPool` source, the `pool()` builtin, and the Tier-2 payments resolver were removed (the
> only shape they served, ledger-vs-pool drift, is expressed as `source_parity` over two ledger
> sources). The template-facing `SourceSpec` is now just `{ ledger, query }` — no `kind` discriminant.
> The sketch below keeps the original design intent: a future heterogeneous source (`ExternalGL`,
> §11) reintroduces `kind` as an **optional** field defaulting to `"ledger"`, so it stays
> non-breaking. See [ADR-003](./adr-003-checkpoint-anchor-and-crosscheck.md).

### V1 types

```ts
type Source = LedgerSet | PaymentsPool | LedgerPostings
// V2+ adds:  | ExternalGL | ExternalAggregate

type LedgerSet       = { ledger: string,  query: map }
type PaymentsPool    = { id: string }
type LedgerPostings  = { ledger: string,  query: map,  window: duration }

type Balance         = { asset: string,  amount: int }
type Account = {
  address:       string,
  ledger:        string,
  metadata:      map<string, string>,
  balances:      list<Balance>,
  balance:       int,
  lastActivity:  timestamp,
}
type Posting = { txID: string, source: string, destination: string, asset: string, amount: int, at: timestamp }
```

### V1 builtins

```ts
ledgerSet(ledger: string, query: string|map): Source
pool(id: string): Source
postings(source: Source): list<Posting>          // V1.1

balance(source: Source): int                      // single-asset sugar
balance(source: Source, asset: string): int
balances(source: Source): map<string, int>

sum(list<int>): int
abs(int): int
count(list<any>): int                             // V1.1

lastActivity(source: Source): timestamp           // V1.1
now(): timestamp                                  // V1.1
duration(s: string): duration                     // V1.1

accounts.all(a, <predicate>)                      // V1.1
accounts.exists(a, <predicate>)                   // V1.1
```

---

## 8. What CEL is *not* good at (honest limitations)

- **No stateful computation across evaluations.** Want "alert when balance drops 10% WoW"? We provide a `previousBalance(source, "1w")` builtin and the kernel resolves it.
- **No iteration beyond bounded comprehensions.** No `while`, no recursion. If we need that, we extend the object model with the right pre-computed shape — not the language.
- **Limited string manipulation.** Adequate for our domain.
- **No multi-rule correlation.** Each rule evaluates in isolation; cross-rule patterns live in the alert layer.

All four are **features**, not bugs — they're how CEL keeps its safety properties.

---

## 9. Implications & consequences

### V1 GA does not expose CEL as a public API

The kernel uses CEL internally; templates are the entire customer-facing surface. Raw CEL access is design-partner-gated and lands *after* GA, behind a feature flag.

Rationale: once customer expressions exist in production, the builtin namespace (`balance`, `ledgerSet`, `pool`, …) is a binding API. We need at least one GA cycle of internal use to shake out the builtin naming and the object model before promising stability.

Scope discipline for V1: CEL stays internal-only, templates are the public API, Webhooks deliver events, no vendor integrations.

### Other commitments

1. **A stable CEL builtin namespace.** Renames are breaking changes for EE+ customers. Versioning policy, deprecation cycle, naming review for additions — same rigor as a REST API.
2. **An eval budget.** Per-evaluation hard ceilings on CEL cost, accounts scanned, wall-clock. Budget exceeded → evaluation errors, raises a meta-alert.
3. **Two API surfaces.** Templates (typed structs in OpenAPI) compile down to CEL. Power-mode raw CEL for the 20%.
4. **A compiler from template specs to CEL strings.** Each catalog entry has a deterministic `compile(spec) → cel_expression`. The compiled string is **stored on the rule** for debuggability.

---

## 10. Conditions under which we'd revisit

1. **Customers consistently need iteration/state CEL can't express** → reconsider Starlark.
2. **cel-go is abandoned or carries a serious unfixed CVE** (low probability — Google production-critical).
3. **Performance ceiling we can't fix with budgets or resolver caching.**
4. **A clear successor emerges** from the same ecosystem.

We would **not** revisit because (a) a customer wants a feature CEL can't express in one line (usually a template, not a language gap); or (b) we want to add a new source (a resolver, not a kernel change).

---

## 11. Engine sketch

```text
POST /rules { template: "ledger_invariant", spec: {…} }
  │
  ├─ template compiler:   spec  →  CEL string
  ├─ CEL parser:          CEL   →  AST
  ├─ CEL type-checker:    AST against typed object model
  │                       → reject at create time on type mismatch
  └─ persist { rule, compiled_cel, ast_hash, compiled_program (cached) }


Scheduler tick
  │
  ├─ load rule + cached compiled program
  ├─ build eval context: bind now(), bind source resolvers
  ├─ cel.Eval(program, ctx) under cost budget
  │     ├─ CEL hits balance(ledgerSet(…))
  │     ├─ kernel asks LedgerSet resolver → returns Balance
  │     │      (memoized within this eval context)
  │     └─ CEL evaluates arithmetic / comparison → bool
  ├─ persist Evaluation { result, cost, evidence, duration }
  └─ if FAIL → Alert layer (fingerprint, open/update/notify; append AlertEvent)
```

The kernel's Go code in the runtime path is ~200 lines. Everything else is template compilers, resolvers, scheduler, alert layer. **Small kernel, large composable periphery** — that's the bet.

---

## 12. Appendix — V1 catalog as CEL

For reviewers who want to feel the surface:

```cel
# ledger_vs_pool_drift  (RETIRED — pool source removed; express as source_parity over two ledgers)
# balance(ledgerSet("buildr","held=*")) + balance(pool("pool_xyz")) == 0

# ledger_invariant (Buildr)
balance(ledgerSet("buildr","trust.held=*")) + balance(ledgerSet("buildr","trust.obligation=*")) == 0

# account_threshold (aggregate)
balance(ledgerSet("acme","role=operating"), "USD") >= 100000 &&
balance(ledgerSet("acme","role=operating"), "USD") <= 5000000

# account_threshold (per-account) — V1.1
accounts(ledgerSet("acme","role=operating")).all(a,
  a.balance >= 100000 && a.balance <= 5000000)

# account_inactivity — V1.1
accounts(ledgerSet("acme","role=settlement")).all(a,
  now() - a.lastActivity <= duration("24h"))

# GL trial-balance — V2/V3 horizon
balance(ledgerPostings("acme","gl_account=4000", lastMonth())) ==
balance(externalGL("netsuite","4000", lastMonth()))
```

Six predicates. Six different shapes. One engine. **That's what we're buying.**
