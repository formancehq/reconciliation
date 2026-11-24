# Lettering: fees, refunds and chargebacks, open items between runs, and a review of the result files

**Status:** Draft, for the owner. **Part 1 is decided** as ADR-005 decision 27 (2026-11-24), and
the ADR, the design doc and the results reference now carry it; they win where this draft differs.
Two changes on the way: PSP holds are *recommended* rather than optional, after the Connectivity
team's review (a per-payment EPHEMERAL account says from the data what is still open and why), and
each product hold kind declares `settles` (`in` or `out`), without which a refund hold's lettering
counted the wrong way (§1.3). Parts 2 and 3 are not decided. It develops the open questions of
[ADR-005 §10](../prd/adr-005-transaction-level-reconciliation.md#10-decisions-taken-and-what-remains-open)
raised by the design partner's feedback (2026-11-17) and by the Fragment comparison (2026-11-24).
**Date:** 2026-11-24
**Connector facts checked at:** `formancehq/connectivity` @ `d39579f7` (Stripe plugin last changed
`3a178ac0c`), and `formancehq/connectivity-plugins-poc` @ `34be50e91` for the profiles under
`plugins/connectors/` and @ `cdc2a78a1` for `docs/architecture/source-modeling.md`. The
`formancepayments` facts are ADR-005's.

The design partner wrote:

- "We do exact matching but sometimes we need to accumulate some fees together/partial
  refunds/other edge cases."
- "Sometimes we cannot pass the ids ourselves (e.g. PSP initiated payouts/chargebacks)."
- "As of now RND teams are responsible to go through files through automations to describe
  discrepancies and find solutions."

The document has three parts:

1. Fees, refunds and chargebacks: one method that works for every connector.
2. Open items between two daily runs: a preview of the day so far.
3. A user's review of the result files, and what is missing around them.

---

## 1. Fees, refunds and chargebacks: one method for every connector

### 1.1 What the connectors book today

Seven mappings: the Stripe plugin of `formancehq/connectivity`, and `stripecore`,
`adyenpayments`, `paypal`, `braintree` and `worldpayexpress` in
`formancehq/connectivity-plugins-poc`, read for this draft; `formancepayments` as ADR-005 records
it.

**A payment's gross never lands on one account.** The PSP keeps its fee before the merchant's
balance sees the money, and every mapping books that faithfully:

| Mapping | A captured payment of 100 with a fee of 2 | Net on the merchant's balance |
|---|---|---|
| Stripe, `connectivity` (`plugins/stripe/internal/mapper/mapper.go:88-130`) | `customer:{id}` → `stripe:available` 98 and `customer:{id}` → `stripe:fees` 2, one transaction | 98 |
| `stripecore` (`CHARGE_AVAILABLE`, `:582-650`) | the hold pays 100 into `balance:main`, then `balance:main` → `fees:processing:main` 2, one transaction | 98 |
| `adyenpayments` (`PAYMENT_PAYABLE_CREDIT`, `:295`; `PAYMENT_POSITION_CREDIT`, `:463`) | counterparty → `payable:main` 98 in one transaction, counterparty → `fee:{kind}:main` 2 in another, both with the same `adyen.com/psp-reference` | 98 |
| `paypal` (inbound settled, `:1696`) | the gross into the balance, then the fee paid out, one transaction | 98 |

The product applies the invoice amount, 100. Under ADR-005 as written, the PSP amount is the net on
one payment account (decision 17), so **every payment with a fee is an `over_applied` break**. On
card payments that is almost every payment.

**A single account is not even stable over a payment's life.** `adyenpayments` drains the Payable
register into the settlement batch row by row (`SETTLEMENT_PAYABLE_CREDIT`, `:640`), with the
payment's `psp-reference` on the transaction. Read on `payable:main` alone, a settled payment's
amount falls back to 0.

**Refunds and chargebacks reach the original payment in three different ways.**

| Mapping | How an adjustment names its payment |
|---|---|
| `paypal`, `braintree`, `bankcamt`, `routable`, `circle` | `formance.com/accounting.adjusts-reference`, the parent's id, indexed. This is a Connectivity convention (`docs/architecture/source-modeling.md` §8, `cdc2a78a1`): "Parent-to-adjustment linkage stays in metadata" |
| `adyenpayments`, `formancepayments` | The adjustment carries the parent's own key: `adyen.com/psp-reference` on Adyen's refund and chargeback rows (the report's PSP reference is the original payment's, the modification's own id is in `adyen.com/modification-reference`; to confirm on a real report), `payment-id` on `formancepayments`' `payin.refunded` |
| `worldpayexpress` | A field of its own, `worldpay.com/express.original-transaction-id`, indexed |
| Stripe (`connectivity` and `stripecore`) | **None.** A refund carries `re_…`, a dispute `du_…`; nothing names the charge |

**The event vocabularies differ, but Connectivity already has a shared one.** Each mapping names its
events in its own words (`payin.succeeded`, `balance_transaction.charge.available`, `Chargeback`,
`stripe_type = adjustment`…), while `formance.com/accounting.transition` is a closed set of Connectivity's
conventions: `to_transitional`, `to_final`, `compensate`,
`marker` (source-modeling §2.3, rule 4), written 648 times across the profiles of
`connectivity-plugins-poc`. The Stripe plugin of `connectivity` writes neither.

A method keyed on each connector's event names would need a new rule configuration for every
connector, and could not see Adyen's two phases or Stripe's missing link. **The method below reads
amounts from where the money is, not from what the event is called.**

### 1.2 The method: amounts from account roles, signed, over every transaction of the payment

The PSP side of the rule declares **roles**, as address patterns, instead of one payment account:

```json
"psp": {
  "ledger": "psp",
  "key": "adyen.com/psp-reference",
  "adjusts": "formance.com/accounting.adjusts-reference",
  "funds": ["adyenpayments:*:payable:main", "adyenpayments:*:settlement:hold:*"],
  "fees":  ["adyenpayments:*:fee:*:main"],
  "holds": [{"prefix": "adyenpayments:MyMerchant:payment:", "openSign": "positive"}],
  "grace": "7d"
}
```

- **`funds`** are the merchant's money at the PSP: the balance or payable account, and every account
  the PSP moves it through before paying it out (Adyen's settlement batches). Several patterns are
  allowed.
- **`fees`** are what the PSP, or a platform, takes out of the gross before it reaches `funds`:
  processing fees, FX fees, scheme fees, a marketplace's platform cut (`paypal` books one, `:238`).
- **`funds` ∪ `fees` is the perimeter.**

**The PSP amount of a payment is the net inflow into the perimeter**, per asset, summed over every
transaction of the payment, whatever its event:

- a posting from outside the perimeter into it counts +, one from inside to outside counts −, and a
  posting between two perimeter accounts counts 0. So Adyen's Payable-to-batch drain cancels out,
  and the fee taken inside one transaction, or in a separate one, adds back to the gross;
- `pspNet` is the inflow that lands in `funds`, `pspFees` the inflow that lands in `fees`, and the
  compared amount is their sum, the gross the payer paid less what was returned to them;
- a refund or a chargeback leaves the perimeter: it counts −. A reversed chargeback, a returned fee,
  a refund reversed: + or −, as the postings say. Several partial refunds sum;
- a pending event moves a hold, outside the perimeter: it counts 0. A failure releases a hold: 0.

**The transactions of a payment** are those whose `adjusts` field names it, or, when the field is
absent, those whose `key` names it. That one rule covers the three ways in §1.1: a PayPal
chargeback (`adjusts` = the sale), an Adyen chargeback row (no `adjusts`, `key` = the original
payment) and a `formancepayments` refund (same). `adjusts` defaults to the Connectivity convention,
and a connector with a field of its own (Worldpay) names it. Both fields must be indexed: the flow
read filters on `key EXISTS` and the lookups (below) on equality with either.

**The event is inferred from the postings, so no event vocabulary is needed:**

| What the transaction does | Role |
|---|---|
| Moves value into the perimeter, on a payment's own key | final (the payment is received) |
| Moves value out of the perimeter, or back in, on a transaction that `adjusts` a payment (or carries its key after its final) | reversal (refund, chargeback, chargeback reversed) |
| Touches a PSP hold only | pending, or failed when it brings the hold back to zero with no perimeter movement |
| Touches `fees` only | fee |
| Touches nothing the rule declares | unclassified, as today |

A `state` block stays possible as an override, with `formance.com/accounting.transition` as its
default field (`to_transitional` pending, `to_final` final, `compensate` failed or reversal, told
apart by the postings). It is needed only where the postings cannot tell, for instance a connector
that books a pending payment straight onto `funds`.

**PSP holds become optional** (recommended, in decision 27). The Stripe plugin of `connectivity` books only settled balance
transactions and has no hold; ADR-005 §6 rejects an empty `holds` list on either side. Without PSP
holds the rule still matches every final, refund and chargeback; it loses the PSP stock and the
`in_progress` and `failed` classes, and says so in the manifest.

**The funds book generalises the payment-account book** (decision 23): every posting that crosses
the perimeter carries the payment key, an `adjusts` field, or a declared `movementKeys` field (a
payout, a bulk fee). A crossing with none of them is `unkeyed_payment_movement`, P1, as today.
Payouts out of the batch carry Adyen's `batch-number`; Stripe's standalone `stripe_fee` balance
transactions carry their own id.

**Classes.** One is added: `unapplied_reversal`, the PSP reversed and the product has not booked it
yet; `pending` within `product.grace`, then a P2 break. Everything else keeps its meaning, on signed
lifetime amounts: `matched` means the payment's net on both sides is equal after every refund and
chargeback.

**Late reversals are looked up by key.** A chargeback arrives weeks after its payment. A window
transaction that `adjusts` a reference neither matched in the window nor carried triggers a lookup
of that reference on both ledgers, by `key` and by `adjusts`, grouped by 100 as today (design doc
§5). The ledger keeps every transaction, so the 90-day file retention does not limit it.

### 1.3 The product side

The product side is already generic: its amount is the movement on its business holds. It becomes
signed:

- a movement that brings a hold towards zero counts +, one that moves it away counts −, over every
  product transaction that carries the payment reference, multiplied by the hold kind's `settles`:
  `in` for an invoice or a dispute, `out` for a refund, whose lettering is money paid back. A
  refund hold is opened with its `business_ref` alone, or its opening and lettering cancel out
  (decision 27);
- a refund or a chargeback is booked **with the original payment's reference**, the one the PSP's
  notification names. That is the answer to "we cannot pass the ids ourselves": a PSP-initiated
  chargeback needs no id from the customer, only the one the PSP already sent;
- how the product books it is its choice, as long as a declared hold moves: reopen the invoice hold,
  or open a dispute hold (`main:hold:dispute:{dispute_no}`, its own `holds` entry). A dispute won
  letters it; a dispute lost is written off with `business_ref` and no payment reference, which
  changes no amount.

### 1.4 What a connector must give, whatever the PSP

This replaces rows 6, 7, 11 and 12 of the [connector mapping
checklist](../technical/transaction-level-reconciliation.md#mapping-a-connector-for-reconciliation):

| # | The mapping must… | Why |
|---|---|---|
| G1 | carry the **payment's id the product knows** (a PaymentIntent, a PSP reference) as indexed transaction metadata, on every transaction of the payment | The flow read and the join |
| G2 | link every **adjustment** (refund, chargeback, reversal, fee refund) to its payment: `formance.com/accounting.adjusts-reference`, indexed, or the payment's own key | Refunds and chargebacks land on the payment's row |
| G3 | book **funds and fees on distinct accounts**, each fee as a posting, and keep the merchant's balance on accounts a pattern can name | Gross = funds + fees, read from the postings |
| G4 | key every **other movement across the perimeter** (payout, bulk fee, conversion) with a declared field | The funds book closes at 0 |
| G5 | *optional:* in-flight positions as EPHEMERAL holds, and `formance.com/accounting.transition` | The PSP stock and the `in_progress` and `failed` classes |

Where the seven mappings stand:

| Mapping | G1 | G2 | G3 | G4 | G5 |
|---|---|---|---|---|---|
| Stripe, `connectivity` @ `d39579f7` | `stripe_source` is the charge `ch_…`; the product usually knows the PaymentIntent; no index declared | ✗ | ✓ `stripe:available`, `stripe:fees`; a dispute's fee is dropped (`adjustmentPostings` books `Amount`, not `Net`; to confirm) | ✓ `po_…`, own ids | ✗ no hold, no transition |
| `stripecore` @ `34be50e91` | ✓ `stripe-charge-id`, indexed (`stripe-payment-intent-id` on two rules only) | ✗ refunds and disputes go through `GENERIC_BALANCE_*` without the charge | ✓ `balance:main`, `fees:processing:main`, `fees:fx:main` | ✓ | ✓ |
| `adyenpayments` @ `34be50e91` | ✓ `psp-reference`, indexed | ✓ own key (to confirm on a report) | ✓ `payable:main`, `fee:*:main`; settlement batches must be in `funds` | ✓ `batch-number`, indexed | ✓ |
| `formancepayments` | ✓ `payment-id` | ✓ own key | the `fees` account is declared, not posted (ADR-005 §10) | open (ADR-005 §10) | ✓ |
| `paypal` @ `34be50e91` | ✓ `paypal-transaction-id` | ✓ `adjusts-reference` | ✓ | ✓ | ✓ |
| `braintree` @ `34be50e91` | ✓ `braintree-transaction-id` | ✓ `adjusts-reference` | fees not observable on its surface | ✓ disbursement id | ✓ |
| `worldpayexpress` @ `34be50e91` | ✓ `express.transaction-id` | ✓ `express.original-transaction-id` | settlement and fees not observable on its surface (`:10`) | n/a | ✓ |

### 1.5 Presets: the per-connector part ships with the connector

What changes from one connector to another is five values: `key`, `adjusts`, `funds`, `fees` and
`holds`. They follow the connector's chart of accounts, which Connectivity already describes
(`{conn}:{conn_id}:{position}:main` and `:hold:{id}`, source-modeling §2.2). So:

- each connector's artifact publishes a **lettering preset** next to its profile: those five
  values, with `{conn}` and `{conn_id}` left as parameters;
- a rule names a preset and its connection (`"psp": {"preset": "adyenpayments", "conn": "…"}`), and
  may override any value;
- recon validates the result as today: indexed fields, non-overlapping patterns, `funds` and `fees`
  disjoint from the hold prefixes.

Recon then carries no connector knowledge, and a new connector becomes reconcilable when its
profile meets G1–G4 and publishes its preset.

### 1.6 One payment, end to end

| Event | PSP ledger | Product ledger | Row |
|---|---|---|---|
| Payment 100, fee 2 | +98 into `funds`, +2 into `fees` | application +100 on `invoice:INV-7` | 100 = 100, `matched`; `pspNet` 98, `pspFees` 2 |
| Partial refund 30 | −30 out of `funds`, adjusts the payment | −30 on the invoice hold or a refund hold, same reference | 70 = 70, `matched` |
| Chargeback 70, day 60 | −70 out of `funds` (Adyen `Chargeback`, PayPal `CHARGEBACK`) | nothing yet | looked up; 0 against 70: `unapplied_reversal`, pending one day, then P2 |
| Product books it | | −70 on `dispute:D-9`, same reference | 0 = 0, `matched` |
| Dispute won | +70 back into `funds` | +70 letters `dispute:D-9` | 70 = 70, `matched` |
| Settlement and payout | `payable` → batch (0), batch → bank under `batch-number` | | no change to the row; the funds book closes |

### 1.7 Asks to Connectivity

- **Stripe, both mappings:** write `formance.com/accounting.adjusts-reference` = the charge on
  refund and dispute balance transactions (the refund and the dispute objects name it), indexed;
  carry the PaymentIntent id on charges, since that is the id a product stores.
- **Stripe, `connectivity`:** book a dispute's fee (the balance transaction's `Net`, not its
  `Amount`), declare the key indexes, and write `formance.com/accounting.transition`.
- **Adyen:** confirm that refund and chargeback rows carry the original payment's PSP reference in
  the column the mapping reads.
- **`formancepayments`:** post the fees it declares.

### 1.8 What changes in ADR-005, and the size

- Decision 17 (the PSP amount is the net on `paymentAccount`) becomes the net inflow into
  `funds` ∪ `fees`; §6's "counted in absolute value" becomes signed.
- Decision 23's payment-account book becomes the funds book, over the perimeter.
- Decision 7 (a refund is its own pair) stays valid for a connector that gives each refund its own
  key with no `adjusts`: it is then its own row, as today.
- §6 validation: PSP `holds` become optional; `funds` and `fees` must not overlap each other or a
  hold prefix.
- The `state` sets become optional overrides.

**Size: M.** The perimeter computation and the event inference replace the payment-account match
and most of the state classification; signed amounts on both sides; one class; the lookup by
`adjusts`; validation; the presets of `stripecore`, `adyenpayments`, `formancepayments` and
`paypal` as golden cases (fee in the same transaction, fee apart, Adyen's two phases, partial
refunds, chargeback won and lost).

---

## 2. Open items between two runs

### 2.1 The need

The daily run answers once a day, about yesterday. Fragment lists what is unreconciled at any time;
the design partner's finance team follows incoming payments during the day. What they need before
the cut-off is the flow's open side: payments the PSP finalised that the product has not applied,
and applications with no final payment. The stock and the statement can wait for the day's run.

### 2.2 Options

| | How | Verdict |
|---|---|---|
| Intraday runs | `periodType` below a day: a cut every hour, a full run each time | **No.** The reads would fit (about 20 s a run at 1M payments a day), but each run is a record: a signed capture, a statement and files, 24 times a day, 24 times the storage; the alert model is per day; hourly statements are noise for finance |
| Event-driven matching | Match each transaction as it is written, as Fragment does | **No.** Needs a watch on the logs and per-reference state, both ruled out of V1 (decision 25, EN-1932) |
| **Preview of the day so far** | The flow step alone, on `(T, head]`, joined with the last run's carried items | **Recommended** |

### 2.3 The preview

- **What it reads.** The flow window from the last daily cut `T` to the current head, on both
  ledgers, filtered on the key exactly as a daily run reads it, plus the carried items of the
  latest current run, plus the lookups by key. No stock listing and no rewind: those are what make a
  run cost more than its flow.
- **What it returns.** Flow rows with the daily run's classes, mostly `pending`: unapplied payments,
  applications before the final, unapplied reversals, each with its `firstSeen` and its `breakOn`.
  The rows a daily run would already call a break today (an `orphan_application` past
  `psp.grace`, a `reversed_after_application`) are flagged.
- **What it does not do.** No capture, no signature, no manifest, no alert and no file in the
  bucket. It is labelled provisional in the API and the UI. The daily run is unchanged and remains
  the only record.
- **Where.** An API read on the rule, `GET /v3/rules/{id}/preview`, computed on demand and cached a
  few minutes per rule, and a "Today so far" view next to the Results tab. It shares the
  `--lettering-max-concurrent-reads` slots with the jobs, and waits behind a running job.
- **Cost.** A whole day's flow at 1M payments reads in 10 to 14 s per side, both sides
  concurrently, and the stock adds 1 to 10 s (design doc §7.15). So a preview saves little reading
  time over a run; a preview in mid-afternoon reads about two thirds of a day's flow. What it drops
  is the record: the capture, the files and the alert. Cached for five minutes per rule, it costs
  at most a dozen flow reads an hour, each shorter than the daily run's.
- **Why it fits the process.** The same rule, the same classes and the same keys, so a row the
  preview shows at 15:00 is the row the daily run writes at 00:05, with the same `breakId`. The
  finance team acts on an unapplied payment during the day, and the daily run shows it `resolved`
  instead of opening a break.

**Size: M.** A read path that reuses the flow step and the join, an API route, a UI view, a cache,
and a test that the preview's rows equal the next daily run's flow rows for the same window.

---

## 3. Review of the result files, from the user's side

### 3.1 What the choice gets right

- **Volume.** A day at 1M payments is 80 to 155 MB of gzipped NDJSON, nearly all of it the flow
  file (design doc §7.14). That fits neither an alert nor recon's PostgreSQL, which is being retired.
- **Ownership and proof.** The files sit in the customer's bucket, their hash is signed in the
  capture, and a cut gives byte-identical files. An auditor can check a day without trusting recon.
- **It meets the customer where they are.** Their R&D teams already run automations over files.
  NDJSON with a JSON Schema, a stable `breakId` and self-contained rows is the right input for that,
  and the DuckDB query pack shows it works.
- **No new database** for recon in V1, and retention is one lifecycle rule.

### 3.2 Where a user gets stuck

1. **A break cannot be explained or accepted.** A break stays open until it is booked; only the
   whole period's alert can be accepted (ADR-005 §6). The results reference says that "comments and
   assignments attach to" a `breakId`, but nothing stores them, and the v3 API draft has no route
   for them. A lost chargeback being worked on, a fee the PSP will credit next month: each comes
   back every day as `persisting`, and the alert stays red. Users will keep their own exception
   tracker beside the files, which is the work the customer's R&D teams do today. **This is the
   biggest gap.**
2. **"Is payment X reconciled?" has no direct answer.** The files are laid out by rule, day and run.
   A question about one payment means finding the current run, then the day it was first seen, then
   filtering. The finance team the customer describes wants a status per payment
   (incoming, reconciled, settled), not a day's files.
3. **Two read paths.** The API pages the breaks; everything else (flow, stock, carried items) is
   downloaded and queried, with DuckDB in the browser for the UI. A flow file of 74 MB gzipped is
   fine for DuckDB and heavy for a browser tab.
4. **Setup is infrastructure work.** The bucket is the product ledger's backup destination, recon
   needs its own credentials and the permission to read the lifecycle configuration, and a rule is
   rejected without the lifecycle rule. That couples a reconciliation rule to the ledger's backup
   operations, and a Formance Cloud customer may not have access to that bucket at all; the
   pre-signed URLs only cover reading.
5. **The file format is a public API.** Customer automations will depend on every field. Nested
   arrays (`psp[]`, `product[]`) suit jq and DuckDB, not a spreadsheet or a warehouse load.
6. **90 days is short for an audit.** A year-end audit asks for twelve months or more. Raising the
   retention is allowed, but the default and the size estimate (7 to 14 GB per rule over 90 days)
   should say so; a closed period's alert links to files that expire.
7. **Nothing pushes.** One alert per period is right for people, but an automation that wants each
   new P1 break has to poll the files.

### 3.3 What to add, in order

| # | Addition | Why | Size |
|---|---|---|---|
| R1 | **Break annotations**: assign, comment, explain or accept a break with a reason, stored by `breakId` in recon's control state (the ControlState port of EN-1932). The next run copies the annotation onto the row. An accepted break leaves the alert's trigger and shows as `accepted` in the statement | Makes the alert actionable, and replaces the customer's side tracker | M |
| R2 | **Lookup by reference or business id**: the latest status of a payment across days, from the current run's carried items and breaks, and the day it was first seen. A small per-run index file (`ref → day`) keeps it to one file read | The finance team's question, in one call | M |
| R3 | **Preview of the day so far** (part 2) | Open items before the cut-off | M |
| R4 | **Parquet beside NDJSON**, same rows, flattened events | Warehouse loads and spreadsheets; DuckDB reads it faster | S |
| R5 | **A dedicated destination**, the backup destination staying the default | Decouples recon from the ledger's backup operations; works for Cloud customers | S |
| R6 | **An event per new P1 break**, through Webhooks | Automations stop polling | S |

**Verdict.** Files are the right record for the detail: volume, ownership and proof all point
there, and the customer already works on files. But files are a record, not a product. Without R1
and R2 a lettering user gets the same daily discrepancy dump they build today, better formed and
signed, and still has to build the exception workflow on top. R1 is the one to plan with the
lettering V1.
