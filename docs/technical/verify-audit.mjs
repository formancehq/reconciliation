#!/usr/bin/env node
// Reference verifier for reconciliation's control-ledger audit chain (EN-1930).
//
// Proves, from the published public key alone, that every served audit entry was
// authored by reconciliation and has not been altered — no ledger credentials, no
// involvement from Formance. Zero dependencies: Node >= 16 built-in crypto only.
// It is a *reference*: the point is that you can reproduce it in any language.
//
//   RECON_URL=https://<recon-host> [TOKEN=<bearer>] node verify-audit.mjs [limit]
//
// What it proves:   authorship + integrity of each entry (Ed25519 over its payload).
// What it does NOT: completeness. The served `sequence` is the ledger's bucket-wide
//   audit sequence, shared with other ledgers, so a "no gaps in this filtered list"
//   check is meaningless — that guarantee is anchored in the ledger, not this list.
//   See audit-chain-v3.md §9.
import { createPublicKey, verify } from "node:crypto"

const BASE = (process.env.RECON_URL || "http://localhost:8081").replace(/\/+$/, "")
const LIMIT = Number(process.argv[2] || 500)
const headers = process.env.TOKEN ? { Authorization: `Bearer ${process.env.TOKEN}` } : {}

// A raw 32-byte Ed25519 public key wrapped as DER SPKI (the fixed 12-byte prefix
// + the key), so Node's createPublicKey accepts it. Other languages take the raw
// key directly (e.g. Go ed25519.PublicKey, Python Ed25519PublicKey.from_public_bytes).
const SPKI_PREFIX = Buffer.from("302a300506032b6570032100", "hex")
const ed25519PublicKey = (rawBase64) =>
  createPublicKey({
    key: Buffer.concat([SPKI_PREFIX, Buffer.from(rawBase64, "base64")]),
    format: "der",
    type: "spki",
  })

async function getJSON(path) {
  const res = await fetch(BASE + path, { headers })
  if (!res.ok) throw new Error(`GET ${path} -> HTTP ${res.status}`)
  return res.json()
}

// 1. Fetch the published public key(s), indexed by key id.
const keys = new Map()
for (const k of (await getJSON("/audit/signing-keys")).data.keys ?? []) {
  keys.set(k.keyId, ed25519PublicKey(k.publicKey))
}
if (keys.size === 0) {
  console.error("no signing keys published — is signing enabled on this deployment?")
  process.exit(2)
}

// 2. Read the served audit entries (newest first, up to `limit`).
const entries = (await getJSON(`/audit/entries?limit=${LIMIT}`)).data.entries ?? []

// 3. Verify each signed entry: ed25519.verify(publicKey, payload, signature).
let ok = 0
let bad = 0
let unsigned = 0
for (const e of entries) {
  if (!e.signed || !e.signature || !e.payload) {
    // Expected only for the bootstrap key registration (sent while the ledger's
    // keystore was still empty); everything after the key is active is signed.
    unsigned++
    continue
  }
  const pub = keys.get(e.keyId)
  if (!pub) {
    bad++
    console.error(`#${e.sequence}: signed with an unpublished key id ${e.keyId}`)
    continue
  }
  const valid = verify(
    null,
    Buffer.from(e.payload, "base64"),
    pub,
    Buffer.from(e.signature, "base64")
  )
  if (valid) ok++
  else {
    bad++
    console.error(`#${e.sequence}: SIGNATURE INVALID`)
  }
}

console.log(
  `verified ${ok}/${entries.length} entries · invalid ${bad} · unsigned ${unsigned} (bootstrap)`
)
process.exit(bad > 0 ? 1 : 0)
