# fctl Reconciliation plugin — preparation tranche

This directory is the Reconciliation-owned preparation for the fctl
Reconciliation plugin (fctl-v2 programme Tasks 10A and 10B). It currently
contains **no plugin**: no runtime, no component entry point, no HTTP client, no
generated SDK, no bindings, no ABI. It contains the one thing that can be
delivered and defended before the fctl-v2 plugin runtime is frozen — a
reproducible, versioned inventory of the Reconciliation operation surface and
its mapping onto the legacy fctl command baseline.

## Why the inventory comes first

The fctl-v2 programme gates every product plugin implementation behind its MVP4
contract freeze (portable component lifecycle, host-owned access and
capabilities, exact per-operation authorisation scopes), and it sequences the
Reconciliation SDK generation after that freeze. Writing a catalogue, an
adapter, or a generated client against an unfrozen ABI would produce work that
has to be thrown away. Establishing *which operations exist, which ones the
legacy CLI covered, what each one requires, and what is genuinely blocked* does
not depend on that freeze, and it is the input the later implementation needs.

## Contents

| Path | Role |
|---|---|
| `docs/command-inventory.md` | Source of truth: method, reasoning, evidence, risks, blockers, gates. Hand-written. |
| `docs/operations.generated.md` | Generated tables: totals, per-family operations, baseline mapping. |
| `audit/spec.go` | Reads `openapi.yaml` into typed operations and document facts. No inference. |
| `audit/baseline.go` | The pinned legacy fctl `reconciliation` command baseline and its mapping. |
| `audit/classify.go` | Frozen family table and per-operation risk derivation. |
| `audit/blockers.go` | Recorded admission blockers, SDK-generation blockers, and spec-versus-server divergences. |
| `audit/report.go` | Assembles the report and derives every quoted count. |
| `audit/testdata/report.json` | Golden report; the determinism gate. |
| `cmd/specaudit` | Regenerates the two committed artefacts. |

## Commands

Run from the repository root inside `nix develop`:

```sh
just fctl-audit         # regenerate the committed inventory artefacts
just fctl-audit-check   # fail if they no longer match openapi.yaml
```

Or from this directory:

```sh
go test ./...
```

`just tests`, `just lint`, `just tidy` and `just pre-commit` at the repository
root include this module.

## Headline facts

24 operations, all under one tag. 7 legacy fctl commands, all 7 mapped, none
excluded, none deprecated. The first tranche (policies + reconciliations) is
exactly the 7 operations the legacy CLI reached. 5 operations are blocked by a
GET-with-request-body hazard; 3 recorded blockers stand between here and the
Task 10A generated SDK; 7 spec-versus-server divergences are recorded, of which
the document's undefined `Authorization` security scheme is the most
consequential.

## What must not be inferred from this directory

- No operation is accepted into a plugin catalogue. The inventory records proven
  facts and separately records blockers.
- No authorisation mechanism is invented. Every operation references a security
  scheme the document never defines; the scope strings are quoted, the scheme is
  recorded as missing.
- No product version or supported major is asserted — the document's version is
  an unsubstituted build placeholder.
- No transport, component build, OCI installation or dual-host behaviour is
  claimed, prepared or gated here.
- Nothing here imports fctl-v2. The module's only dependency is
  `gopkg.in/yaml.v3`.
