#!/usr/bin/env node
/**
 * Demo seed for the standalone Reconciliation UI.
 *
 * Creates a set of reconciliation rules over the `mortgage` data ledger (which
 * the mortgage module populates on the same local Ledger v3 cluster), evaluates
 * them to produce a live mix of PASS / FAIL receipts, and drives the alert
 * lifecycle (open → ack → resolve → accept) so every screen has real content:
 *   • Rules list + templates
 *   • Rule activity (evaluation receipts / captures)
 *   • Alerts inbox with OPEN / ACKNOWLEDGED / RESOLVED / ACCEPTED breaks
 *   • Overview KPIs + Insights
 *
 * Talks to the recon HTTP API directly (RECON_API_URL, default :8081).
 * Idempotent-ish: it deletes rules it previously created (matched by the
 * `demo=ledger-clarity` label) before re-seeding.
 *
 * PREREQUISITE: the data ledger must exist and hold the loan:201:* book these
 * rules read. On a fresh local cluster (no mortgage module running), seed it
 * first with `./scripts/seed-data.sh` — otherwise evaluations produce no breaks.
 *
 * Usage:  ./scripts/seed-data.sh && node scripts/seed-demo.mjs
 *         RECON_API_URL=http://localhost:8081 LEDGER=mortgage node scripts/seed-demo.mjs
 */

const BASE = (process.env.RECON_API_URL || 'http://localhost:8081').replace(/\/+$/, '');
const LEDGER = process.env.LEDGER || 'mortgage';
const ASSET = process.env.ASSET || 'USD/2';
const DEMO_LABEL = 'ledger-clarity';

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function api(method, path, body) {
  const res = await fetch(`${BASE}${path}`, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : {},
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (res.status === 204) return undefined;
  const text = await res.text();
  const json = text ? JSON.parse(text) : undefined;
  if (!res.ok) {
    const msg = json?.errorMessage || `HTTP ${res.status}`;
    throw new Error(`${method} ${path} → ${res.status}: ${msg}${json?.details ? ` (${json.details})` : ''}`);
  }
  return json;
}

const A = (address) => ({ $match: { address } });
const ledgerSrc = (query) => ({ kind: 'ledger', ledger: LEDGER, query });

// ── Rule catalogue ──────────────────────────────────────────────────────────
// Each rule is deterministic against the seeded mortgage ledger: the PASS rules
// check invariants that hold, the FAIL rules check ones that don't (so they open
// a break we can then work through the lifecycle).
const RULES = [
  {
    key: 'interest-cleared',
    expect: 'PASS',
    body: {
      name: 'Interest accruals cleared to zero',
      templateKind: 'ledger_invariant',
      templateSpec: {
        terms: [{ ledger: LEDGER, query: A('loan:201:interest:*'), sign: 1 }],
        tolerance: { [ASSET]: 0 },
      },
      severity: 'low',
      periodType: 'continuous',
      enabled: true,
    },
  },
  {
    key: 'principal-floor',
    expect: 'PASS',
    body: {
      name: 'Loan 201 principal balance floor',
      templateKind: 'account_threshold',
      templateSpec: {
        ledger: LEDGER,
        query: A('loan:201:principal'),
        mode: 'aggregate',
        bounds: { [ASSET]: { min: 0 } },
      },
      severity: 'info',
      periodType: 'continuous',
      enabled: true,
    },
  },
  {
    key: 'exposure-cap',
    expect: 'FAIL',
    lifecycle: 'ack',
    body: {
      name: 'Loan 201 principal exposure cap',
      templateKind: 'account_threshold',
      templateSpec: {
        ledger: LEDGER,
        query: A('loan:201:principal'),
        mode: 'aggregate',
        bounds: { [ASSET]: { max: 20000000 } },
      },
      severity: 'high',
      periodType: 'continuous',
      enabled: true,
    },
  },
  {
    key: 'company-vs-investor',
    expect: 'FAIL',
    lifecycle: 'resolve',
    body: {
      name: 'Company vs investor sub-ledger parity — loan 201',
      templateKind: 'source_parity',
      templateSpec: {
        left: ledgerSrc(A('loan:201:company:*')),
        right: ledgerSrc(A('loan:201:investor:*')),
        scope: 'aggregate',
        tolerance: { [ASSET]: 0 },
      },
      severity: 'medium',
      periodType: 'continuous',
      enabled: true,
    },
  },
  {
    key: 'repaid-vs-principal',
    expect: 'FAIL',
    lifecycle: 'accept',
    body: {
      name: 'Repaid ledger reconciles to principal — loan 201',
      templateKind: 'source_parity',
      templateSpec: {
        left: ledgerSrc(A('loan:201:repaid:*')),
        right: ledgerSrc(A('loan:201:principal')),
        scope: 'aggregate',
        tolerance: { [ASSET]: 100 },
      },
      severity: 'medium',
      periodType: 'continuous',
      enabled: true,
    },
  },
  {
    key: 'company-floor',
    expect: 'FAIL',
    lifecycle: 'open',
    body: {
      name: 'Loan 201 company allocation floor',
      templateKind: 'account_threshold',
      templateSpec: {
        ledger: LEDGER,
        query: A('loan:201:company:*'),
        mode: 'aggregate',
        bounds: { [ASSET]: { min: 5000000 } },
      },
      severity: 'high',
      periodType: 'continuous',
      enabled: true,
    },
  },
];

// ── V2 rule catalogue ───────────────────────────────────────────────────────
// stale_holds is a V2 template, so these are created on /v2/rules. They read the
// holds:enfuce:* book seeded by seed-data.sh, whose deadline metadata is dated
// relative to seed time — so the verdicts below hold whenever you run this.
//
// The pair is the "warn early, page late" pattern: severity is declared per
// rule, so the advance warning and the breach are two rules over the same holds.
const HOLDS_SOURCE = {
  id: 'enfuce-holds',
  ledger: LEDGER,
  query: A('holds:enfuce:*'),
  asset: ASSET,
};
const HOLD_DEADLINE = {
  expiryKey: 'hold_expires_at',
  createdKey: 'hold_created_at',
  encoding: 'datetime',
  maxAge: '48h',
};
// Labels copied onto each alert so it names the authorisation, not just an
// address. Read off the account, never filtered on — no index required.
const HOLD_IDENTITY = ['enfuce_auth_id', 'card_id'];

const V2_RULES = [
  {
    key: 'holds-stale',
    expect: 'FAIL',
    body: {
      name: 'Card holds past their expiry',
      templateKind: 'stale_holds',
      templateSpec: {
        source: HOLDS_SOURCE,
        deadline: HOLD_DEADLINE,
        mode: 'stale',
        scope: 'per_hold',
        identityKeys: HOLD_IDENTITY,
      },
      severity: 'high',
      periodType: 'continuous',
      enabled: true,
    },
  },
  {
    key: 'holds-approaching',
    expect: 'FAIL',
    body: {
      name: 'Card holds expiring within 24 hours',
      templateKind: 'stale_holds',
      templateSpec: {
        source: HOLDS_SOURCE,
        deadline: { expiryKey: HOLD_DEADLINE.expiryKey, encoding: 'datetime' },
        mode: 'approaching',
        warnWithin: '24h',
        scope: 'per_hold',
        identityKeys: HOLD_IDENTITY,
      },
      severity: 'low',
      periodType: 'continuous',
      enabled: true,
    },
  },
];

async function listRules(prefix = '') {
  const r = await api('GET', `${prefix}/rules`);
  return r?.cursor?.data ?? [];
}

async function listAlerts(prefix = '') {
  const r = await api('GET', `${prefix}/alerts`);
  return r?.cursor?.data ?? [];
}

async function cleanup() {
  let cleared = 0;
  for (const prefix of ['', '/v2']) {
    const mine = (await listRules(prefix)).filter((r) => r?.labels?.demo === DEMO_LABEL);
    for (const r of mine) {
      await api('DELETE', `${prefix}/rules/${encodeURIComponent(r.id)}`).catch(() => {});
    }
    cleared += mine.length;
  }
  if (cleared) console.log(`· cleared ${cleared} previously-seeded rule(s)`);
}

async function main() {
  console.log(`Seeding reconciliation demo → ${BASE} (ledger "${LEDGER}", asset "${ASSET}")`);

  // Health check first — fail fast with a helpful message.
  try {
    await api('GET', '/_healthcheck');
  } catch (e) {
    console.error(`\n✗ Recon backend not reachable at ${BASE}.`);
    console.error(`  Start it:  cd reconciliation && go run . serve --ledger-grpc-address 127.0.0.1:8888 --ledger-insecure --listen :8081\n`);
    process.exit(1);
  }

  await cleanup();

  // 1) Create rules (tagged so re-runs are idempotent).
  const created = [];
  for (const rule of RULES) {
    const body = { ...rule.body, labels: { ...(rule.body.labels || {}), demo: DEMO_LABEL, loan: '201' } };
    const r = await api('POST', '/rules', body);
    created.push({ ...rule, id: r.data.id });
    console.log(`+ rule "${rule.body.name}" [${rule.body.templateKind}] → ${r.data.id}`);
  }

  // 1b) Create the V2 rules on the versioned route. A stale_holds rule is
  // rejected at create if its deadline keys aren't declared + indexed on the
  // ledger, so report that clearly rather than failing the whole seed.
  for (const rule of V2_RULES) {
    const body = { ...rule.body, labels: { ...(rule.body.labels || {}), demo: DEMO_LABEL, program: 'cards' } };
    try {
      const r = await api('POST', '/v2/rules', body);
      created.push({ ...rule, id: r.data.id, prefix: '/v2' });
      console.log(`+ rule "${rule.body.name}" [${rule.body.templateKind}] → ${r.data.id}`);
    } catch (e) {
      console.log(`! skipped "${rule.body.name}": ${e.message}`);
      console.log('  stale_holds needs holds:enfuce:* accounts whose hold_expires_at /');
      console.log('  hold_created_at keys are declared datetime and indexed — re-run ./scripts/seed-data.sh.');
    }
  }

  // 2) Evaluate each rule twice to build a short activity history. Evaluation is
  // best-effort: if the backend can't record captures against this ledger, the
  // rules are still seeded (and the reason is reported) rather than aborting.
  let evalOk = 0;
  let evalErr;
  for (const round of [1, 2]) {
    for (const rule of created) {
      try {
        const r = await api('POST', `${rule.prefix ?? ''}/rules/${encodeURIComponent(rule.id)}/evaluate`, {});
        evalOk += 1;
        if (round === 1) {
          const result = r?.data?.result ?? '?';
          const ok = result === rule.expect;
          console.log(`  ▸ evaluate "${rule.body.name}" → ${result}${ok ? '' : `  (expected ${rule.expect})`}`);
        }
      } catch (e) {
        evalErr = e;
      }
    }
    await sleep(400);
  }
  if (evalOk === 0 && evalErr) {
    console.log(`\n⚠ Evaluations could not be recorded: ${evalErr.message}`);
    console.log('  The rules are seeded, but captures/alerts require the backend to record');
    console.log('  capture transactions on its control ledger. Skipping the alert lifecycle.\n');
    console.log('✓ Rules seeded. Open the UI at http://localhost:3003');
    return;
  }

  // 3) Read-after-write is eventually consistent — poll for the alerts.
  let alerts = [];
  for (let i = 0; i < 6; i++) {
    await sleep(500);
    alerts = await listAlerts();
    if (alerts.length >= created.filter((r) => r.expect === 'FAIL').length) break;
  }
  const byRule = new Map();
  for (const a of alerts) if (!byRule.has(a.ruleID)) byRule.set(a.ruleID, a);
  // V2 alerts live behind the versioned route — a V1 listing never returns them.
  const alertsV2 = await listAlerts('/v2').catch(() => []);
  console.log(`· ${alerts.length} alert(s) opened (+ ${alertsV2.length} on /v2)`);

  // 4) Drive the alert lifecycle per rule intent.
  for (const rule of created) {
    if (rule.expect !== 'FAIL') continue;
    // The stale-hold alerts are left open on purpose: they are the live "funds
    // are trapped right now" content, and they resolve themselves once the hold
    // clears rather than being worked through the lifecycle here.
    if (rule.prefix === '/v2') continue;
    const alert = byRule.get(rule.id);
    if (!alert) {
      console.log(`  ! no alert found for "${rule.body.name}" (skipping ${rule.lifecycle})`);
      continue;
    }
    try {
      if (rule.lifecycle === 'ack') {
        await api('POST', `/alerts/${alert.id}/ack`, { by: 'ops@acme.com', note: 'Investigating the exposure breach.' });
        console.log(`  ✓ acknowledged "${rule.body.name}"`);
      } else if (rule.lifecycle === 'resolve') {
        await api('POST', `/alerts/${alert.id}/resolve`, { by: 'ops@acme.com', note: 'Re-booked the mis-allocated postings.', transactionRefs: ['txn-demo-realloc-201'] });
        console.log(`  ✓ resolved (fixed by booking) "${rule.body.name}"`);
      } else if (rule.lifecycle === 'accept') {
        await api('POST', `/alerts/${alert.id}/accept`, { by: 'cfo@acme.com', note: 'Known timing difference between repayment posting and principal roll-forward; accepted.' });
        console.log(`  ✓ accepted (business) "${rule.body.name}"`);
      } else {
        console.log(`  · left OPEN "${rule.body.name}"`);
      }
    } catch (e) {
      console.log(`  ! lifecycle ${rule.lifecycle} failed for "${rule.body.name}": ${e.message}`);
    }
  }

  console.log('\n✓ Demo seed complete. Open the UI at http://localhost:3003');
}

main().catch((e) => {
  console.error('\n✗ Seed failed:', e.message);
  process.exit(1);
});
