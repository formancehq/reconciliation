# Vendored Ledger v3 protos

These `.proto` files are copied verbatim from the ledger repo's `misc/proto/`,
with only `option go_package` rewritten to point at `internal/ledgerpb/`. Do not
hand-edit them: any local change is silently overwritten by the next sync.

| | |
|---|---|
| Source | `github.com/formancehq/ledger`, branch `release/v3.0` |
| Synced at | `8ee5f797e` (2026-09-17) |
| Protocol revision | `10` — see [internal/ledgerpb/grpcprotocol](../../internal/ledgerpb/grpcprotocol/protocol.go) |

## Re-syncing

```bash
just sync-ledger-proto /path/to/ledger
```

That copies the protos, rewrites `go_package`, prints the ledger SHA and both
protocol revisions for comparison, and regenerates the bindings.

## Why the revision matters

Ledger v3 is unreleased and **renumbers proto field tags between revisions**.
Removing the chapters subsystem, for instance, freed tags 9–15 on the
`Request.type` oneof and every later variant was compacted down — so a client
built on the old contract sends `set_metadata_field_type` on tag 16, which the
current server reads as `create_index`. Both sides decode successfully and mean
different things.

`ledger-protocol-version` (EN-1851) is the guard: every business RPC must
declare the revision it was built against, and the server rejects a mismatch
with `FailedPrecondition` before any handler runs. That turns a silent
misdecode into a loud startup failure — but only if the vendored revision is
bumped in the same change as the protos. Update both, or neither.

Also note: the hot read path (`Transaction`, `Account`, `Volumes`,
`QueryFilter`, `ListOptions`, `AggregateResult`, `MetadataValue`) and
`Request` tags 1–8 — which carry `apply`, the variant behind every transaction
and metadata write — have been tag-stable so far. Drift therefore tends to hide
in provisioning and control-plane paths while day-to-day traffic keeps working.
Do not read "it works locally" as "the contract matches".
