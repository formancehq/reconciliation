# Verifying the reconciliation audit chain (for an external auditor)

Every write reconciliation makes to its control ledger — provisioning, each rule
evaluation, every alert transition (open / acknowledge / resolve / accept /
snooze) — is signed with reconciliation's Ed25519 key before it is committed. From
the **public half of that key alone** you can prove, with no ledger credentials
and nobody from Formance in the loop:

- **Authorship** — the write came from reconciliation (only it holds the private key).
- **Integrity** — the record has not been altered since it was signed.

This is the whole external guarantee for *"did the control run"* and *"was
anything changed."* It is **not** a completeness guarantee — see the last section.

## The recipe

Three calls, then one signature check per entry. Both endpoints need only a
read-scoped token (`reconciliation:read`); neither needs ledger access.

1. **Get the public key(s).** `GET /audit/signing-keys` →
   `{ data: { keys: [{ keyId, publicKey }] } }`. `publicKey` is the base64 of the
   raw 32-byte Ed25519 public key; `keyId` identifies which key signed an entry.

2. **Read the entries.** `GET /audit/entries?limit=N` →
   `{ data: { entries: [{ sequence, keyId, payload, signature, signed, outcome, … }] } }`,
   newest first. `payload` is the exact signed batch bytes (base64) and
   `signature` is the Ed25519 signature over them (base64). A rejected write is
   still signed and audited (`outcome: "failure"`) — signing proves the *attempt*
   was authentic, independent of whether the ledger accepted it.

3. **Verify each signed entry.** For every entry with `signed: true`, check
   `ed25519.verify(publicKey[keyId], base64decode(payload), base64decode(signature))`.
   Any dropped, reordered, altered, or forged record fails this check, and nobody
   without reconciliation's private key can produce a passing one.

A self-contained reference implementation (Node ≥ 16, zero dependencies) lives
next to this page:

```bash
RECON_URL=https://<recon-host> TOKEN=<bearer> node docs/technical/verify-audit.mjs
# verified 112/112 entries · invalid 0 · unsigned 0 (bootstrap)
```

It is deliberately small and dependency-free because the point is that you can
reproduce it in any language — Go's `ed25519.Verify`, Python's
`Ed25519PublicKey.from_public_bytes(...).verify(...)`, a WebCrypto `verify` — all
take the same raw public key, payload, and signature.

### Notes

- **Unsigned entries.** At most the very first key-registration is unsigned (the
  ledger accepts an unsigned registration only while its keystore is still empty —
  the bootstrap window); every write after the key is active is signed. The
  reference script counts these separately so you can see there is nothing else.
- **Key rotation.** Retired public keys stay published and stay verifiable; an
  entry names the `keyId` that signed it, so historical entries verify against the
  key that was active when they were written.
- **Spot-check from the screen.** In the app, expanding any event on an alert's
  timeline resolves that action to its signed audit entry and verifies the
  signature in-browser — the same check as the script, per action. That path is a
  convenience over the public verification here, not a substitute for it.

## What this does *not* prove: completeness

Authorship and integrity are absolute from the public key. **Completeness — that
no genuine reconciliation write was dropped or hidden from the list — is a
separate guarantee with a different anchor: the ledger, not the public key.**

The `sequence` on each entry is the ledger's **bucket-wide** audit sequence,
shared with every other ledger in the same bucket. Filtering it to reconciliation
yields a monotonic but **sparse** subset — the gaps are the expected footprint of
the other ledgers, not evidence of a drop. So a *"no gaps in this filtered list"*
check proves nothing, and this recipe does not attempt one. A gapless guarantee
requires either an isolated bucket or the ledger's own chain head; see
[audit-chain-v3.md](audit-chain-v3.md) §9 for the resolved model.
