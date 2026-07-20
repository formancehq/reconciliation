# ADR-001 — CEL verdict kernel behind typed templates

**Status:** Accepted (implemented in [`internal/engine/`](../../internal/engine/))
**Linked from:** [PRD §5](./README.md), [architecture.md](../technical/architecture.md)
**Last updated:** 2026-07-20

---

## 1. Decision in one sentence

V1 rules are **typed templates** whose evaluators read each source once at its
effective PIT, discover the fingerprint universe, and run bounded Google Common
Expression Language (CEL) verdicts over the resulting immutable snapshot.

---

## 2. Why this is on the table

The legacy implementation hardcodes one rule shape: ledger metadata query vs payments pool, drift == 0, per asset. Generalizing to N customer asks, N source kinds, and N predicates requires deciding **how a rule is represented** before we ship anything.

That decision is hard to revisit. Once customers author rules — especially in EE+ power-mode where they reference builtin names — renaming or restructuring breaks contracts. Pick once, live with it.

---

## 3. Requirements the choice must satisfy

| #  | Requirement                                                                                  | Type   |
| -- | -------------------------------------------------------------------------------------------- | ------ |
| 1  | **Safety** — generated verdicts cannot DOS the engine, access I/O, or fail to terminate       | must   |
| 2  | **Early validation** — bad customer-authored template specs are rejected at rule creation     | must   |
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

Read them side by side. CEL is the only verdict language here that is safe,
typed, compact, and backed by a runtime we do not maintain. V1 nevertheless
keeps typed templates as the authoring interface: the selected design is A at
the public seam and C inside each template's verdict implementation.

---

## 5. Options matrix

| Opt | Safe? | Typed? | YAML-friendly? | Extensible | Mature impl? | Verdict |
| --- | ----- | ------ | -------------- | ---------- | ------------ | ------- |
| **A** Typed Go structs only | ✅ | ✅ | medium | engine PR per new rule type | ✅ (ours) | **Chosen for V1 authoring**, insufficient as the verdict runtime alone |
| **B** JSON-AST (we evaluate) | ✅ | medium | poor (verbose) | ✅ | we'd own it | Bad ergonomics + bus factor |
| **C** CEL (`cel-go`) | ✅ | ✅ static | ✅ | ✅ (custom builtins) | ✅ (Google) | **Chosen for the internal verdict kernel** |
| **D** Rego / OPA | ✅ | medium | poor (Datalog) | ✅ | ✅ (OPA) | Wrong abstraction for numeric predicates |
| **E** Starlark / Lua / Tengo | partial | ❌ | medium | ✅ | ✅ | Loops + unbounded compute = wrong shape |
| **F** Numscript extension | depends | partial | medium | partial | partial | Mutates Numscript's identity |
| **G** JS / WASM sandbox | hard | ❌ | medium | ✅ | ✅ | Huge attack & resource surface |
| **H** Custom DSL | depends | ✅ | ✅ | ✅ | we'd maintain | Years to catch up to CEL on tooling |

---

## 6. Why CEL for verdicts

1. **Safety is built in, not enforced by a library.** No I/O. No loops. Guaranteed termination. cel-go exposes a cost-limit API per evaluation.
2. **Statically typed.** V1 validates the representative expression at rule creation and compiles every generated snapshot expression before execution. A future raw-CEL interface must type-check customer expressions at its own write boundary.
3. **One-line readable in YAML.** Reviewable in a PR.
4. **Custom functions are first-class.** Source-shaped explanations use the same typed vocabulary that a future, separately versioned raw-CEL mode may expose.
5. **Production-grade Go runtime we don't maintain.** cel-go is used by Kubernetes admission controllers, Envoy, GCP IAM, Cerbos. Google won't drop it.
6. **C-family syntax** — engineers parse it on first read. No tutorial.

---

## 7. The typed object model

CEL is only as good as the types it reasons about. In V1, the typed template
specification is the product surface and the source-shaped CEL vocabulary is an
internal explanation format. If raw CEL power mode is exposed after GA, this
object model becomes a customer-facing compatibility contract.

### Candidate raw-CEL types (not a V1 GA interface)

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

### Candidate raw-CEL builtins (not a V1 GA interface)

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

- **No stateful computation across evaluations.** A future rule such as "alert when balance drops 10% WoW" must receive prior state through a template-owned resolver or an explicitly designed builtin; V1 does not provide one implicitly.
- **No iteration beyond bounded comprehensions.** No `while`, no recursion. V1 templates perform bounded fingerprint expansion before entering CEL rather than embedding unbounded discovery in the language.
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

1. **An internal CEL builtin namespace for V1.** It is covered by compatibility tests but is not a customer contract until raw CEL power mode is explicitly shipped. At that point, renames require a versioning and deprecation policy.
2. **An eval budget.** Per-evaluation hard ceilings on CEL cost, accounts scanned, wall-clock. Budget exceeded → evaluation errors, raises a meta-alert.
3. **One V1 API surface.** Typed templates are the only GA authoring interface. Raw CEL remains a future, separately versioned power mode rather than an implicit property of V1 rules.
4. **One executable source of truth.** `template_spec` plus the versioned template implementation define behavior. `explanation_cel` is a deterministic representative expression for humans; it is never loaded as a runtime program. Each evaluation stores the exact source-shaped expression and observed values for every fingerprint in its evidence.

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
  ├─ validate typed template spec
  ├─ Explain(spec) → representative CEL
  ├─ parse + type-check the representative shape
  └─ persist { rule, template_spec, explanation_cel }


API or worker evaluation
  │
  ├─ load rule.template_spec
  ├─ resolve each source once at its effective PIT
  ├─ discover the asset/account fingerprint universe
  ├─ for each fingerprint
  │     ├─ render source-shaped CEL for evidence
  │     └─ render equivalent CEL over immutable snapshot values
  ├─ Engine.EvaluateBatch(snapshot expressions)
  │     └─ enforce one cumulative CEL cost + wall-clock budget
  ├─ persist Evaluation { result, cost, PITs, evidence, duration }
  └─ if FAIL → Alert layer (fingerprint, open/update/notify; append AlertEvent)
```

Remote I/O deliberately stays outside CEL. This prevents repeated resolver calls
and time-of-check/time-of-use drift inside one verdict, while allowing templates
to discover assets and accounts that were not known when the rule was created.
The source-shaped and snapshot expressions are kept equivalent by kernel-parity
tests.

The kernel's Go code in the runtime path remains small; templates own source
resolution and fingerprint expansion behind the `Evaluator` interface. A future
performance optimization may cache compiled snapshot expression shapes, but it
must not make a persisted explanation string executable or move hidden network
I/O back into CEL.

---

## 12. Appendix — V1 catalog as CEL

For reviewers who want to feel the surface:

```cel
# ledger_vs_pool_drift  (port of today)
balance(ledgerSet("buildr","held=*")) + balance(pool("pool_xyz")) == 0

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
