# Color of money — what reconciliation sees

> Ledger v3 segregates balances by `(account, asset, color)`. Every number this module
> reads is the **sum across those buckets**, by construction. This records what that
> costs, what it demonstrably does not cost, and what a bucket-aware source would take —
> because the answer to the last one is mostly not an upstream change.

Verified against the ledger at `0b4676d97` (color landed in `59f7ce86c`,
`feat!: color of money — segregated balances per (account, asset, color)`) and the
vendored contract in [common.proto](../../proto/ledger/common.proto) /
[bucket.proto](../../proto/ledger/bucket.proto). The upstream reference is
`docs/technical/architecture/data-model/color-of-money.md` in `formancehq/ledger`.

## 1. What color is, upstream

Color is a **per-posting segregation key**. Two postings on the same `(account, asset)`
with different colors operate on strictly isolated balances. The parts that matter here:

| Property | Consequence for a control |
|---|---|
| Balance is tracked per `(account, asset, color)` | "the balance of `USD/2`" is a vector, not a scalar |
| `Color == ""` is the **uncolored bucket**, not an absence | an empty color is a first-class bucket that segregates like any other |
| Double-entry holds **per bucket** — for every `(asset, color)` the sum of all balances is zero | each bucket is its own conservation universe, and none of them is observable on its own today |
| Color is immutable; there is no re-coloring primitive | a conversion is two transactions — `X` → clearing account, then mint `Y` from `@world` |
| Charset `^[A-Z]*$`, no metadata of its own | it cannot carry an id, a date, or any lot identity (see [stale-holds.md §5 Q1](./stale-holds.md)) |
| Never derived from account metadata | a client's `kind: safeguarded` metadata convention and the ledger's color are unrelated axes |
| **No color filter on list queries** | `QueryFilter` has no color arm — the oneof is field / address / and / or / not / reference / builtin_uint / ledger |

The last row is the only genuine upstream gap for this module, and it is narrower than it
looks — see §5.

## 2. What reconciliation does with it today

It removes the dimension at the wire, deliberately:

| Read path | Where | How the dimension goes away |
|---|---|---|
| `AggregateVolumes` | [client.go](../../internal/ledger/client.go) | sends `CollapseColors: true`; the result loop then keys by `GetAsset()` and adds, so buckets fold twice over |
| `GetAccount` | [client.go](../../internal/ledger/client.go) | sends `CollapseColors: true` — a point read returns one entry per asset with `color = ""` |
| `ListAccounts` (streamed) | [resolver.go](../../internal/ledger/resolver.go), [ledger_meta.go](../../internal/api/ledger_meta.go) | **no collapse flag exists** on `ListAccountsRequest`, so rows arrive per `(asset, color)` and are summed client-side by `commonpb.BalancesByAsset` |
| `_recon` occurrence counter | [alert_metadata.go](../../internal/ledgerstore/alert_metadata.go) | same client-side sum, applied defensively to this module's own (uncolored) marker volumes |

The two server flags are locked by
[client_color_test.go](../../internal/ledger/client_color_test.go), which feeds an uncolored
and a `RESERVED` row for the same asset and asserts the single summed total. The client-side
fold is deliberately centralised in one function,
`commonpb.BalancesByAsset` ([account_volumes.go](../../internal/ledgerpb/commonpb/account_volumes.go)) —
its caller in [alert_metadata.go](../../internal/ledgerstore/alert_metadata.go) says why: so that
color handling lives in one place. That is the seam §5 needs, and it already exists.

Above the client, nothing carries a bucket either:

- **The rule query DSL** admits exactly two leaves — `address` and `metadata[…]`
  (`dataLedgerLeaf` in [resolver.go](../../internal/ledger/resolver.go)). There is no third.
- **A source** is `(id, kind, ledger, query, asset[, metadataKey])`
  ([v2_source.go](../../internal/templates/v2_source.go)). One source, one declared asset amount.
- **A fingerprint** carries `asset` on five of the six templates, and
  `baseSource`/`quoteSource` on `exchange_rate_bounds`
  ([helpers.go](../../internal/templates/helpers.go)). No bucket axis exists.
- **The `asset: "*"` fan-out** discovers its universe from the asset keys of the aggregate
  result, so it enumerates currencies, never buckets.

Collapse is opt-in upstream precisely so that clients cannot aggregate across buckets by
accident. This module opts in everywhere.

## 3. What that costs

**The safeguarding control cannot be written.** Segregating client money is the flagship
use for color and the flagship use for a reconciliation product, and the two do not meet:
*"the `SAFEGUARDED` bucket on the trust account covers customer liabilities"* has no
expressible form. The nearest rule asserts the **total**, which passes when the safeguarded
bucket is short and the operating bucket is long by the same amount.

**Offsetting buckets satisfy a zero-tolerance equation.** `balance_equation` with
`tolerance: "0"` is the natural way to write a control total. Because each `(asset, color)`
is its own conservation universe, a colored surplus nets against an uncolored deficit and
the control reports green. Note this compounds with the cross-ledger skew documented for
[EN-1480](https://formance-team.atlassian.net/browse/EN-1480): tolerance absorbs timing,
collapse absorbs misallocation, and a rule that reads as exact is neither.

Both are **silent false passes**, which is the failure mode worth caring about — unlike the
accounts-budget and new-alert caps, nothing goes `ERROR`.

What is *not* affected, and should not be over-claimed:

- **Totals are never wrong, only coarse.** Per-bucket conservation is a ledger invariant, so
  a collapsed sum is the arithmetically correct total of correct buckets.
- **The clearing account in a color conversion is already watchable.** "This account should
  be flat between the two legs" is a `balance_bounds` rule today — the account is selectable
  by address. What is not checkable is that the second leg minted the right amount into the
  right color.
- **`account_metadata` sources are bucket-less by nature.** The value is one integer under one
  key, standing for one declared asset. Color has no counterpart there, so any ledger↔metadata
  control is structurally asymmetric and the client must say which bucket the mirrored figure
  is supposed to represent.

## 4. What it does not cost — this module's own chart

Reconciliation's control-ledger writes are unaffected, and this is worth recording so it does
not get re-investigated:

- Every posting reconciliation emits on `_recon` is **uncolored**. Alert markers, item
  accounts, captures and cursors all live in the `""` bucket.
- The alert compare-and-swap reads only the **failure code**; the ledger failure is surfaced
  as `failure.GetReason().String()` for audit display ([client.go](../../internal/ledger/client.go),
  [audit.go](../../internal/api/audit.go)). The upstream `ColorKnown` asymmetry — where a
  Numscript insufficient-funds error omits the `color` key because it cannot resolve the
  bucket, while a direct-posting failure always carries it — never reaches this module's logic.
- The streamed-account path already handles uncollapsed rows correctly: `BalancesByAsset`
  **accumulates** per asset rather than assigning, so a multi-bucket account cannot have its
  balance overwritten by whichever row sorts last. Rows arrive sorted by `(asset, color)`
  ascending, which is exactly the shape that punishes an assigning fold.

## 5. What a bucket-aware source would take

**Mostly not an upstream change.** `AggregateVolumes` returns **one row per `(asset, color)`**
by default; `AggregatedVolume.color` is populated on every entry. Per-bucket totals are
therefore reachable today, against a shipped ledger, with no new RPC:

1. Stop collapsing on the two aggregate/point calls, and give `BalancesByAsset` a bucket-aware
   sibling, so both paths key balances by `(asset, color)`.
2. Add an optional `color` to the source spec, and thread it into `resolvedV2Source`.
3. Append a `color:` axis to the fingerprint of the templates that fan out.
4. Carry the bucket in the evidence, so the operator sees which universe broke.
5. Generalise the `asset: "*"` fan-out from per-asset to per-bucket.

The genuine upstream gap is **selection**: there is no way to ask for "every account holding
`GRANTS`", because `QueryFilter` has no color arm and color is not an account property. In
practice that is the lesser half — a source selects an account set by address or metadata,
and the bucket is a dimension of the *result*, not of the selection.

Two blast-radius notes for whoever picks this up: a per-bucket fan-out multiplies outcomes per
rule, which spends the accounts budget (`MaxAccountsScanned`) no faster but does spend the
new-alert cap (`DefaultMaxNewAlertsPerEvaluation`, 200) faster; and step 5 interacts with the wildcard union logic in
`evaluatePerAsset`, not just the source resolver.

## 6. The trap: unset is not `""`

This is the one decision to get right **before** any of §5 is written, because it is a
persisted public contract.

| `color` on a source | Must mean |
|---|---|
| **absent** | every bucket, collapsed — today's meaning, and the only safe default for rules written before the field existed |
| `""` | the **uncolored bucket specifically** — a real, narrower control |
| `"GRANTS"` | that bucket |

Taking the zero value of a Go string as "uncolored" would silently convert every existing
rule into a different control. The ledger hit this exact ambiguity on its error path and
solved it with an explicit `ColorKnown` flag rather than a sentinel; this module needs the
same distinction — an optional/pointer field, or a separate `colorScope` discriminator — not
a zero-value default.

The rest of the migration is clean: appending a `color:` axis only for rules that declare one
leaves every existing fingerprint byte-identical, so adding buckets later causes **no alert
churn** on upgrade.

## 7. Follow-ups

- **Say it in the catalogue.** [templates.md §Named source](./templates.md#named-source) now
  states that a source balance is the sum across buckets. That is the release-relevant half:
  a rule author must not build a safeguarding control on a number that does not mean what the
  field name suggests.
- **A ticket for color-scoped sources**, scoped as §5 + §6 — recon-side work, with the
  account-selection gap noted as upstream and probably not needed.
- **Revisit if a design partner adopts color.** Nothing here is release-blocking while no
  book in production uses buckets; all of it is the day one does.
