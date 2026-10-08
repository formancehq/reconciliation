# ADR-006: Templates evaluate in Go, CEL leaves reconciliation v3

**Status:** Accepted, to be implemented in S5 ([EN-2755](https://formance-team.atlassian.net/browse/EN-2755)) of the re-implementation ([EN-2746](https://formance-team.atlassian.net/browse/EN-2746)). Supersedes [ADR-001](./adr-001-cel-kernel.md).

**Date:** 2026-10-08 (decided 2026-10-07, review point 12b)

**Decision owners:** Reconciliation maintainers

**Related:** [ADR-003](./adr-003-checkpoint-anchor-and-crosscheck.md), [ADR-004](./adr-004-multi-source-comparisons.md), RFC 0022 "Reconciliation v3 on Ledger v3" ([formancehq/internal-rfcs#37](https://github.com/formancehq/internal-rfcs/pull/37), in review)

## Context

ADR-001 chose CEL over a typed object model as the evaluation core of the rule engine. Templates were to compile to CEL, and the kernel was to evaluate that CEL. A raw CEL "power mode" was to follow after GA.

The kernel never became the evaluator:

- ADR-003 made direct Go math authoritative for built-in templates and narrowed ADR-001 §11. CEL kept three jobs: validation at rule creation (`eng.Compile`), the rendered formula in `compiledCEL`, and a cross-check test.
- Commit `13b03574` removed the cross-check, and commit `cc5f7d6b` removed its last tests.
- On `feat/reconciliation-ledger-v3`, `Engine.Evaluate` has no caller outside `internal/engine`. The service calls `engine.Compile` on create and on patch (`internal/api/service/rule.go:145` and `:237`). Templates take the engine only to read `MaxAccountsScanned()`.
- The released `v2.5.0` behaves the same way: `Compile` on the explanation string only (`internal/api/service/rule.go:198` and `:293` at `v2.5.0`), and the OpenAPI field `explanationCEL` is documented as "not the runtime program".

The review of 2026-10-07 (finding M10) found that this leftover CEL does harm:

- **It rejects valid rules.** A bound of `1e20` fails `Compile` and returns `400`, although the Go check handles it.
- **It explains the wrong check.** `stale_holds` renders `balance == 0`, which is not what it tests. With a wildcard asset, the rendered formula reads the asset `"*"` and always passes.
- **It costs code and dependencies.** About 800 to 1,300 lines of production code and 600 lines of tests, plus `github.com/google/cel-go` and its indirect dependencies `github.com/antlr4-go/antlr/v4` and `golang.org/x/exp`.

Reconciliation v3 is a re-implementation in slices with a public API break (RFC 0022). It is the cheapest moment to remove what does not serve the product.

## Decision

Built-in templates are typed Go: a spec, a validator, an evaluator and an explainer per template. Reconciliation v3 has no CEL.

- The `internal/engine` package loses `Compile`, `Evaluate` and the CEL builtins. Its evaluation limits, such as `MaxAccountsScanned`, move to the template framework.
- Rules no longer store `compiledCEL`, and captures no longer carry it in their evidence. The API drops the field.
- Each template validates its spec in Go at `POST /rules` and `PATCH /rules/{id}`. A spec that the evaluator accepts is never rejected by a second parser.
- The UI shows a formula that the Console renders from the template spec. The formula is display text. Nothing parses, stores or evaluates it.
- Raw CEL rules ("power mode") are deferred past GA. They come back only through a new ADR, which must ship:
  - CEL evaluation on the production path, not only compilation.
  - A correspondence test per template, proving that the template and its CEL form give the same verdict on the same inputs.
  - A cost budget per evaluation.
  - A builtin namespace reviewed against the Ledger v3 sources.

ADR-001 stays the record of why CEL was the best language for that future mode (§4 to §6). This ADR replaces its decision that CEL is the kernel of reconciliation.

## Rejected alternatives

- **Keep CEL as a validator and explainer, and fix it (option B of the review).** Fix the two divergences and add a property test per template. Rejected: the product would maintain two descriptions of every check, and CEL would gain nothing while it is not the evaluator.
- **Make CEL the evaluator again, as ADR-001 intended.** Rejected for the same reason ADR-003 gave: two evaluators to keep equivalent, and a CEL runtime cost on every evaluation, for templates that typed Go already expresses.
- **Keep `compiledCEL` as stored text without `Compile`.** Rejected: a stored formula that nothing checks drifts from the code that runs, as `stale_holds` already shows.

## Consequences

- **API.** `compiledCEL` disappears from rules and from capture evidence. This is part of the public API break of RFC 0022. Evidence also gets smaller, which helps keep captures under the 16 KiB Ledger v3 metadata limit.
- **Dependencies.** `github.com/google/cel-go` leaves `go.mod`. Its indirect dependencies go with it once nothing else imports them.
- **Console.** The Console needs a formula renderer per template, fed by the spec it already receives.
- **Product.** The PRD rule "no raw-CEL public API at GA" still holds. Power mode moves from "behind a feature flag after GA" to "after GA, with a new ADR".
- **ADR-003.** Its statements that `compiledCEL` stays in evidence and that `eng.Compile` runs at rule creation (§4 and §9, item 2) no longer apply to v3. Its read model is unchanged.
- **Docs.** `docs/technical/architecture.md` and `docs/technical/templates.md` describe the CEL kernel. They are rewritten with S5, when the code changes.
