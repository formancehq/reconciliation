# fctl Reconciliation plugin

This directory contains the product-owned Reconciliation command plugin for
fctl. It exposes every public v1 product operation except `GET /_info`, whose
service-version probe is owned by the host.

## Command surface

The catalogue contains 23 commands across policies, reconciliations, rules,
evaluations and alerts. Each command declares one exact OpenAPI operation,
either `reconciliation:read` or `reconciliation:write`, stack targeting and
Reconciliation major 1 compatibility.

Request bodies are accepted as JSON through `--body`. The five filtered list
operations accept JSON through `--query` and forward it losslessly through the
OpenAPI `query` query-string parameter; no GET body is emitted. Free-form JSON
objects in requests and responses are decoded with `json.Number` leaves, so
integer tokens larger than JavaScript's safe-integer range survive the adapter
unchanged. Paginated commands accept `--cursor` and `--page-size`. The
host-owned `--all` control follows opaque
cursors with the SDK's canonical limits: 100 pages, 10,000 items and 4 MiB.
After the first page, only the opaque cursor is sent; filters and page size are
not replayed.

## Layout

| Path | Role |
|---|---|
| `core/catalogue.go` | Exact command, scope, compatibility and HTTP policies. |
| `../../pkg/client/` | Speakeasy-generated v1 DTOs, scalars and HTTP client. |
| `core/adapter_v1.go` | Host-bound generated-client adapter and pagination traversal. |
| `component/descriptor.go` | Immutable portable component descriptor. |
| `entrypoints/reconciliation/` | WIT lifecycle implementation. |
| `wit/plugin.wit` | Public portable lifecycle ABI. |
| `scripts/build-component.sh` | Two-build deterministic component gate. |
| `docs/command-inventory.md` | Pinned OpenAPI and historical CLI provenance. |
| `audit/` | Reproducible inventory and drift checks. |

`plugins/fctl` is the hand-written application module. Its test gate covers
the 23-command catalogue, exact read/write scopes, request and response
budgets, pagination limits and failures, generated-client adaptation, and the
portable `describe`/`start`/`resume`/`cancel`/`close` lifecycle. It enforces at
least 80% statement coverage over this module. The generated `pkg/client`
module is deliberately outside that percentage: it is versioned and tested as
a separate generated module, with product-owned client contract tests plus the
byte-identical `generate-client-check` gate. Combining the two profiles would
hide application regressions behind generated statements.

`FCTL_SDK_ROOT` names an explicit fctl source root. The wrapper validates the
SDK module's NAR content hash and canonical WIT hash against
`fctl-sdk.lock.json`. When the source includes Git metadata, it also requires
the locked commit and origin, then projects those exact committed SDK and WIT
paths before validation. Ignored or modified working-tree files therefore
cannot affect the command. It then creates an ephemeral Go workspace for the
Reconciliation plugin module, replaces the unpublished SDK module with the
validated projected source, runs the requested command, and removes the whole
projection on success, failure or signal. No workstation path or Nix store
path is tracked.

## Validation

From the repository root, inside its declared Nix environment:

```sh
export FCTL_SDK_ROOT=/path/to/fctl-v2-poc
just generate-client
just generate-client-check
just fctl-sdk-check
just fctl-audit-check
just fctl-component-test
just tests
```

The plugin-local `just tidy` updates `go.mod` and `go.sum` from an isolated
alternate modfile; `just tidy-check` reports drift without changing them. The
temporary modfile contains the local SDK replacement only while `go mod tidy`
runs, and that replacement is removed before either file is compared or copied
back. The product client replacement remains the tracked relative path
`../../pkg/client`.

Speakeasy 1.761.1 is pinned by `flake.nix`; regeneration must not depend on a
global binary. The repository-owned postprocessor makes Speakeasy's free-form
JSON decoding lossless and is included in the byte-identical regeneration
check. The adapter supplies only the host-bound `producthttp` client.
It does not configure generated authentication or retries, so credentials,
endpoint resolution and retry policy remain owned by fctl.

This checkout contains the generated `gen.yaml` and `gen.lock`, but no
repository-owned Speakeasy workflow definition or workflow lock. Restoring a
hosted Speakeasy workflow is therefore blocked on exporting its authoritative
configuration; a workflow copied from another product would not be valid
provenance. Until then, the pinned local recipe above is the supported
regeneration path.

The repository Nix shell pins the component authoring toolchain to the same
versions and source hashes as fctl-v2 `545521b`: `componentize-go 0.4.1`,
`wasi-virt 0.2.0`, `wasm-tools 1.239.0` and Binaryen/`wasm-opt 124`. The build
fails before compilation if any executable reports a different version:

```sh
nix develop --no-write-lock-file .# --command \
  just fctl-component-build
```

The build produces `dist/reconciliation/reconciliation.wasm`, validates it,
compares two independent builds byte-for-byte, enforces the exact five-import
WASI allowlist and enforces the 16 MiB artifact ceiling. Generated build and
distribution directories are not source files and must not be committed.

Local unit tests and a deterministic build do not replace install, native-host,
browser-host or live-service acceptance receipts.
