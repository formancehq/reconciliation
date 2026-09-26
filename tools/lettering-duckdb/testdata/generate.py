#!/usr/bin/env python3
"""Generates the test data of tools/lettering-duckdb. Python standard library only.

    python3 tools/lettering-duckdb/testdata/generate.py

Three rules come out:

- rule=psp-vs-billing: the worked example of docs/technical/transaction-level-results.md §10.
  Its NDJSON lines are copied from the doc, byte for byte; the manifest is the doc's, with real
  SHA-256 values. The run of 23 September is partial: only what check-chain reads (its manifest,
  carried, stock and breaks files).
- rule=qa-scenarios: seven days, two assets, one scripted story per class and edge case.
- rule=qa-verdicts: a week that walks through every verdict, an empty day, an incomplete run
  followed by a two-day window, a retry, and the week's period.json.

The two qa rules are produced by a small reference engine that follows the results doc, and that
asserts the doc's identities (books, bridge, residual, open items) on every day it writes. Next to
their files, expected/ holds the CSV each query of the pack must return, computed from the
engine's own state without DuckDB. The tests therefore check the SQL against the engine; the
engine answers to the doc through its assertions and through check.sql.

The output is deterministic: running this twice gives the same bytes.
"""
import csv
import datetime as dt
import gzip
import hashlib
import io
import json
import os
import re
import shutil
from dataclasses import dataclass, field

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.abspath(os.path.join(HERE, '..', '..', '..'))
DOC = os.path.join(REPO, 'docs', 'technical', 'transaction-level-results.md')


def sha(data):
    return hashlib.sha256(data).hexdigest()


def gz(lines):
    return gzip.compress(''.join(line + '\n' for line in lines).encode(), compresslevel=9, mtime=0)


def dump(obj):
    return json.dumps(obj, separators=(',', ':'), ensure_ascii=False)


def write(path, data):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'wb') as f:
        f.write(data)


# --- The worked example of the results doc -------------------------------------------------

def worked_example(out):
    doc = open(DOC).read()
    ex = doc.split('## 10. Worked example')[1]
    blocks = dict((n, b) for n, _, b in re.findall(
        r"\*\*`([a-z.]+)`\*\*[^\n]*\n(?:(?!```)[^\n]*\n)*?```(json|text)\n(.*?)```", ex, re.S))
    base = os.path.join(out, 'rule=psp-vs-billing')
    day24 = os.path.join(base, 'day=2026-09-24', 'run=r-20260925T000004Z')
    day23 = os.path.join(base, 'day=2026-09-23', 'run=r-20260924T000003Z')

    m = json.loads(blocks['manifest.json'])
    rule = dict(m['rule'])
    rule.pop('sha256')
    m['rule']['sha256'] = sha(dump(rule).encode())

    pay39 = [l for l in blocks['carried.ndjson.gz'].strip().splitlines() if '"PAY-39"' in l][0]
    pay40 = ('{"ref":"PAY-40","asset":"EUR/2","class":"unapplied_payment","outcome":"pending",'
             '"pspAmount":"70000","productAmount":"0","drift":"70000","firstSeen":"2026-09-23",'
             '"firstSide":"psp","breakOn":"2026-09-24","psp":[{"tx":1160874,"state":"payin.succeeded",'
             '"amount":"70000","insertedAt":"2026-09-23T15:03:12Z"}],"product":[]}')
    carried23 = gz([pay39, pay40])
    write(os.path.join(day23, 'carried.ndjson.gz'), carried23)
    # The previous day's stock and breaks, as the doc's 24 September implies them.
    def hold(hid, balance, klass, opened, age, bucket):
        return dump({"side": "product", "hold": f"main:hold:invoice:{hid}", "asset": "EUR/2",
                     "prefix": "main:hold:invoice:", "holdId": hid, "openSign": "negative", "balance": str(balance),
                     "class": klass, "outcome": "break" if klass != "open" else "ok",
                     "lifecycle": "persisting", "openedAt": opened, "ageDays": age, "bucket": bucket})
    stock23 = [hold('INV-11', -120000, 'open', '2026-09-04T08:30:00Z', 19, '8-30d'),
               hold('INV-12', -80000, 'open', '2026-09-19T12:05:00Z', 4, '2-7d'),
               hold('INV-14', 10000, 'wrong_sign', '2026-09-18T16:20:00Z', 5, '2-7d'),
               hold('INV-3', -120000, 'stuck', '2026-08-14T10:02:00Z', 40, '>30d'),
               hold('INV-5', -70000, 'stuck', '2026-08-21T09:40:00Z', 33, '>30d'),
               hold('INV-9', -50000, 'open', '2026-09-12T07:45:00Z', 11, '8-30d')]
    stock23_gz = gz(stock23)
    write(os.path.join(day23, 'stock.ndjson.gz'), stock23_gz)
    breaks24 = {json.loads(l)['breakId']: json.loads(l) for l in blocks['breaks.ndjson.gz'].strip().splitlines()}
    def was(bid, **row):
        b = dict(breaks24[bid])
        b.update({"lifecycle": "persisting", "outcome": "break"}, **row)
        b.pop('resolvedOn', None)
        for k in ('previousBalance', 'clearedAt', 'clearedBy'):
            b.pop(k, None)
        return b
    breaks23 = [was('1c7f3a90d2e84b55'),
                was('d4f8a1c3e5b70926', ageDays=40),
                was('3a6e9d0b2c8f4171', balance="-70000", ageDays=33),
                was('7b24e1f09c3d6a12', ageDays=5)]
    breaks23_gz = gz([dump(b) for b in breaks23])
    write(os.path.join(day23, 'breaks.ndjson.gz'), breaks23_gz)
    m23 = {
        "schemaVersion": "lettering/1", "engine": m['engine'], "rule": m['rule'],
        "runId": "r-20260924T000003Z",
        "period": {"type": "daily", "day": "2026-09-23", "cutoff": "2026-09-23T23:59:59+02:00", "tz": "Europe/Paris"},
        "verdict": "breaks",
        "statement": {"EUR/2": {"suspense": {"open": "120000", "count": 2}}},
        "books": [
            {"side": "psp", "prefix": "fpay:stripe:payment:hold:pending:", "asset": "EUR/2", "openSign": "positive", "open": "0", "count": 0},
            {"side": "product", "prefix": "main:hold:invoice:", "asset": "EUR/2", "openSign": "negative", "open": "430000", "count": 6},
            {"side": "product", "prefix": "main:hold:refund:", "asset": "EUR/2", "openSign": "positive", "open": "0", "count": 0},
        ],
        "files": [{"name": "carried.ndjson.gz", "rows": 2, "sha256": sha(carried23), "expiresAt": "2026-12-22"},
                  {"name": "stock.ndjson.gz", "rows": 6, "sha256": sha(stock23_gz), "expiresAt": "2026-12-22"},
                  {"name": "breaks.ndjson.gz", "rows": 4, "sha256": sha(breaks23_gz), "expiresAt": "2026-12-22"}],
        "anchor": False, "expiresAt": "2026-12-22",
    }
    b23 = (dump(m23) + '\n').encode()
    write(os.path.join(day23, 'manifest.json'), b23)

    m['previousRun']['manifestSha256'] = sha(b23)
    for f in m['files']:
        lines = blocks[f['name']].strip().splitlines()
        data = gz(lines)
        assert len(lines) == f['rows'], f['name']
        write(os.path.join(day24, f['name']), data)
        f['sha256'] = sha(data)
    write(os.path.join(day24, 'manifest.json'), (dump(m) + '\n').encode())


# --- A reference engine for the qa rules ----------------------------------------------------

UTC = dt.timezone.utc
TZ_OFFSET = dt.timedelta(hours=2)  # Europe/Paris in October 2026, before the change of 25 Oct.
PSP_HOLD = 'fpay:stripe:payment:hold:pending:'
INVOICE = 'main:hold:invoice:'
REFUND = 'main:hold:refund:'
OPEN_SIGN = {PSP_HOLD: 1, INVOICE: -1, REFUND: 1}
SIGN_NAME = {1: 'positive', -1: 'negative'}
PSP_STATES = {'pending': 'payin.pending', 'final': 'payin.succeeded', 'failed': 'payin.compensate'}
FLOW_CLASSES = ['matched', 'under_applied', 'over_applied', 'unapplied_payment', 'applied_before_final',
                'orphan_application', 'reversed_after_application', 'in_progress', 'failed']
STOCK_CLASSES = ['open', 'wrong_sign', 'stuck', 'cleared']
PRIORITY = {'orphan_application': 1, 'reversed_after_application': 1, 'under_applied': 2,
            'over_applied': 2, 'unapplied_payment': 3, 'stuck': 4, 'wrong_sign': 4}


def at(day, hhmm):
    """An instant in UTC: `day` is a date string, `hhmm` a UTC time."""
    h, m = hhmm.split(':')
    return dt.datetime.fromisoformat(day).replace(hour=int(h), minute=int(m), tzinfo=UTC)


def local_day(t):
    return (t + TZ_OFFSET).date()


def iso(t):
    return t.strftime('%Y-%m-%dT%H:%M:%SZ')


def day_str(d):
    return d.isoformat()


@dataclass
class Psp:
    """A PSP event. `amount` is the net posting on the payment account, in absolute value;
    `hold` the signed movement on the payment's pending hold."""
    t: dt.datetime
    ref: str
    asset: str
    kind: str  # pending, final, failed, or an unclassified state value
    amount: int = 0
    hold: int = 0
    merchant: str = None
    tx: int = 0


@dataclass
class Prod:
    """A product transaction: its signed movements per hold, and its PSP reference if any."""
    t: dt.datetime
    kind: str  # open, apply, other (credit note, adjustment), unclassified
    moves: list  # [(prefix, holdId, asset, signed delta)]
    ref: str = None
    tx: int = 0


@dataclass
class Rule:
    id: str
    psp_grace: int
    product_grace: int
    psp_max_age: int
    product_max_age: int
    backfill_from: dt.date
    period_type: str = 'daily'
    buckets: tuple = (1, 7, 30)

    def as_json(self):
        return {
            "id": self.id, "version": 1, "sha256": None,
            "buckets": [f"{b}d" for b in self.buckets], "retention": "90d", "anchorRetention": "13mo",
            "backfillFrom": day_str(self.backfill_from),
            "psp": {"ledger": "psp", "key": "payments.formance.com/payment-id",
                    "state": {"field": "formance.com/observation.event-type", "pending": ["payin.pending"],
                              "final": ["payin.succeeded"], "failed": ["payin.compensate"]},
                    "grace": f"{self.psp_grace}d", "maxAge": f"{self.psp_max_age}d",
                    "paymentAccount": "fpay:stripe:account:*:main", "merchantRef": "merchant_ref",
                    "holds": [{"prefix": PSP_HOLD, "openSign": "positive"}]},
            "product": {"ledger": "main", "key": "psp_payment_ref",
                        "state": {"field": "transition_kind", "final": ["to_final"]},
                        "grace": f"{self.product_grace}d", "maxAge": f"{self.product_max_age}d",
                        "holds": [{"prefix": INVOICE, "openSign": "negative", "businessId": "invoice_no"},
                                  {"prefix": REFUND, "openSign": "positive", "businessId": "refund_no"}]},
        }


class Book:
    """Everything booked on both ledgers for one rule, with the transaction ids assigned."""

    def __init__(self, psp, prod):
        self.psp = sorted(psp, key=lambda e: e.t)
        self.prod = sorted(prod, key=lambda e: e.t)
        for i, e in enumerate(self.psp):
            e.tx = 1001 + i
        for i, e in enumerate(self.prod):
            e.tx = 501 + i
        self.psp_by_ref, self.apply_by_ref = {}, {}
        for e in self.psp:
            if e.kind in PSP_STATES:
                self.psp_by_ref.setdefault(e.ref, []).append(e)
        for e in self.prod:
            if e.kind == 'apply':
                self.apply_by_ref.setdefault(e.ref, []).append(e)

    @staticmethod
    def last_tx(events, until):
        ids = [e.tx for e in events if e.t <= until]
        return max(ids) if ids else 0


def cutoff(day):
    return dt.datetime.combine(day, dt.time(23, 59, 59), tzinfo=UTC) - TZ_OFFSET


def bucket(age, bounds):
    if age <= bounds[0]:
        return f"0-{bounds[0]}d"
    for lo, hi in zip(bounds, bounds[1:]):
        if age <= hi:
            return f"{lo + 1}-{hi}d"
    return f">{bounds[-1]}d"


class Engine:
    """Runs a rule day by day, as the results doc specifies, and writes every run's files."""

    def __init__(self, rule, book, out, acceptances=None, anomalies=None):
        self.rule = rule
        self.book = book
        self.out = os.path.join(out, f'rule={rule.id}')
        self.acceptances = acceptances or {}  # break key -> day accepted
        self.anomalies = anomalies or {}      # day -> [(side, tx)]
        self.prev = None                      # the last complete run's state
        self.accepted_state = {}              # break key -> (class, amount) when accepted
        self.runs = []                        # every run, for the expected query results
        self.business_ids = []                # the ids expected/ holds a business-id result for
        rule_json = rule.as_json()
        body = dict(rule_json)
        body.pop('sha256')
        rule_json['sha256'] = sha(dump(body).encode())
        self.rule_json = rule_json

    # -- one run -----------------------------------------------------------------------------

    def run(self, day, run_id=None, incomplete=None):
        day = dt.date.fromisoformat(day)
        run_id = run_id or f"r-{(day + dt.timedelta(days=1)).strftime('%Y%m%d')}T000004Z"
        run_dir = os.path.join(self.out, f'day={day}', f'run={run_id}')
        if incomplete:
            m = {"schemaVersion": "lettering/1", "engine": "reconciliation v1.4.0", "rule": self.rule_json,
                 "runId": run_id, "period": self.period(day), "verdict": "incomplete",
                 "incomplete": {"reason": incomplete, "detail": "a log range came back short"}}
            write(os.path.join(run_dir, 'manifest.json'), (dump(m) + '\n').encode())
            self.runs.append({'day': day, 'run': run_id, 'verdict': 'incomplete', 'reason': incomplete,
                              'complete': False})
            return
        state = self.compute(day, run_id)
        self.write_run(run_dir, day, run_id, state)
        self.runs.append(state)
        self.prev = state

    def period(self, day):
        return {"type": self.rule.period_type, "day": day_str(day),
                "cutoff": f"{day}T23:59:59+02:00", "tz": "Europe/Paris"}

    def compute(self, day, run_id):
        rule, book, prev = self.rule, self.book, self.prev
        cut = cutoff(day)
        t_psp = Book.last_tx(book.psp, cut)
        t_prod = Book.last_tx(book.prod, cut)
        if prev:
            f_psp, f_prod = prev['cuts']['psp'][1], prev['cuts']['product'][1]
        else:
            start = cutoff(rule.backfill_from - dt.timedelta(days=1))
            f_psp = Book.last_tx(book.psp, start)
            f_prod = Book.last_tx(book.prod, cutoff(rule.backfill_from - dt.timedelta(days=rule.psp_grace + 1)))
        win_psp = [e for e in book.psp if f_psp < e.tx <= t_psp]
        win_prod = [e for e in book.prod if f_prod < e.tx <= t_prod]
        classified = [e for e in win_psp if e.kind in PSP_STATES]
        applies = [e for e in win_prod if e.kind == 'apply']

        carried_in = {r['ref']: r for r in (prev['carried'] if prev else [])}
        w_psp_by_ref, w_app_by_ref = {}, {}
        for e in classified:
            w_psp_by_ref.setdefault(e.ref, []).append(e)
        for e in applies:
            w_app_by_ref.setdefault(e.ref, []).append(e)
        refs = set(carried_in) | set(w_psp_by_ref) | set(w_app_by_ref)
        lookups = {'psp': 0, 'product': 0}
        rows = []
        for ref in sorted(refs):
            w_psp = w_psp_by_ref.get(ref, [])
            w_app = w_app_by_ref.get(ref, [])
            if ref in carried_in:
                psp_items = carried_in[ref]['psp_ev'] + w_psp
                app_items = carried_in[ref]['app_ev'] + w_app
            else:
                psp_items, app_items = list(w_psp), list(w_app)
                if w_app and not w_psp:
                    lookups['psp'] += 1
                    psp_items = [e for e in book.psp_by_ref.get(ref, []) if e.tx <= t_psp]
                    if any(e.kind == 'final' for e in psp_items):
                        app_items = [e for e in book.apply_by_ref.get(ref, []) if e.tx <= t_prod]
                elif any(e.kind == 'failed' for e in w_psp):
                    # a failure on a reference not carried in: its whole history, on both ledgers
                    lookups['product'] += 1
                    app_items = [e for e in book.apply_by_ref.get(ref, []) if e.tx <= t_prod]
                    if app_items:
                        lookups['psp'] += 1
                        psp_items = [e for e in book.psp_by_ref.get(ref, []) if e.tx <= t_psp]
            rows.append(self.flow_row(day, ref, psp_items, app_items, f_psp, f_prod, t_prod, ref in carried_in))

        stock, holds_now = self.stock(day, t_psp, t_prod, rows)
        open_holds = {s['hold'] for s in stock if s['class'] != 'cleared'}
        paired = {}
        for r in rows:  # pairing: an unapplied payment whose merchant reference names an open hold
            if r['class'] == 'unapplied_payment' and r['merchant'] and INVOICE + r['merchant'] in open_holds:
                r['pairedHold'] = INVOICE + r['merchant']
                paired[r['pairedHold']] = r['ref']
        for s in stock:
            s['pairedRef'] = paired.get(s['hold'])
        books = self.books(day, win_psp, win_prod, stock, f_psp, f_prod)
        breaks = self.breaks(day, rows, stock)
        unclassified = self.unclassified(win_psp, win_prod)
        carried = [r for r in rows if r['drift'] != 0]
        anomalies = self.anomalies.get(day_str(day), [])
        if any(b['open'] for b in breaks):
            verdict = 'breaks'
        elif unclassified or anomalies:
            verdict = 'reconciled_with_warnings'
        elif any(r['outcome'] == 'pending' for r in rows):
            verdict = 'reconciled_with_pending'
        else:
            verdict = 'reconciled'
        return {'verdict': verdict, 'day': day, 'run': run_id, 'complete': True, 'rows': rows, 'carried': carried,
                'stock': stock, 'books': books, 'breaks': breaks, 'unclassified': unclassified,
                'cuts': {'psp': (f_psp, t_psp), 'product': (f_prod, t_prod)}, 'lookups': lookups,
                'win_psp': win_psp, 'win_prod': win_prod, 'anomalies': anomalies}

    def flow_row(self, day, ref, psp_items, app_items, f_psp, f_prod, t_prod, carried):
        asset = (psp_items or app_items)[0].asset if psp_items else self.app_asset(app_items)
        items = [(e, mv) for e in app_items for mv in e.moves]

        def amounts(psp_ev, app_ev):
            finals = [e for e in psp_ev if e.kind == 'final']
            first_final = min((e.tx for e in finals), default=None)
            failed = first_final is not None and any(e.kind == 'failed' and e.tx > first_final for e in psp_ev)
            psp_amount = 0 if failed else sum(e.amount for e in finals)
            product_amount = sum(-OPEN_SIGN[p] * d for e in app_ev for (p, _, _, d) in e.moves)
            return psp_amount, product_amount

        psp_amount, product_amount = amounts(psp_items, app_items)
        before = amounts([e for e in psp_items if e.tx <= f_psp], [e for e in app_items if e.tx <= f_prod])
        drift = psp_amount - product_amount
        impact = drift - (before[0] - before[1])
        finals = [e for e in psp_items if e.kind == 'final']
        fails = [e for e in psp_items if e.kind == 'failed']
        t_app = min((e.t for e in app_items), default=None)
        t_final = min((e.t for e in finals), default=None)
        t_fail = min((e.t for e in fails), default=None)
        t_terminal = min([t for t in (t_final, t_fail) if t], default=None)

        if fails:
            if app_items:
                klass = 'reversed_after_application' if t_fail > t_app else 'orphan_application'
            else:
                klass = 'failed'
        elif finals:
            if not app_items:
                klass = 'unapplied_payment'
            elif product_amount == psp_amount:
                klass = 'matched'
            else:
                klass = 'under_applied' if product_amount < psp_amount else 'over_applied'
        else:
            klass = 'applied_before_final' if app_items else 'in_progress'

        seen = [t for t in (t_final, t_app) if t]
        first_seen = local_day(min(seen)) if seen else None
        break_on = None
        outcome = 'ok'
        if klass == 'unapplied_payment':
            break_on = first_seen + dt.timedelta(days=self.rule.product_grace)
            outcome = 'pending' if day < break_on else 'break'
        elif klass == 'applied_before_final':
            break_on = local_day(t_app) + dt.timedelta(days=self.rule.psp_grace)
            if day < break_on:
                outcome = 'pending'
            else:
                klass, outcome = 'orphan_application', 'break'
        elif klass in ('under_applied', 'over_applied', 'orphan_application', 'reversed_after_application'):
            outcome = 'break'
        if klass == 'in_progress':
            first_side = None
        elif t_terminal and (t_app is None or t_terminal < t_app):
            first_side = 'psp'
        else:
            first_side = 'product'
        merchant = next((e.merchant for e in finals if e.merchant), None)
        return {'ref': ref, 'asset': asset, 'class': klass, 'outcome': outcome, 'pspAmount': psp_amount,
                'productAmount': product_amount, 'drift': drift, 'impact': impact, 'firstSeen': first_seen,
                'firstSide': first_side, 'breakOn': break_on, 'merchant': merchant, 'pairedHold': None,
                'psp_ev': psp_items, 'app_ev': app_items, 'carried_in': carried,
                'drift_before': before[0] - before[1], 'psp_before': before[0]}

    @staticmethod
    def app_asset(app_items):
        return app_items[0].moves[0][2]

    def stock(self, day, t_psp, t_prod, rows):
        """Open holds at the cut, plus the holds open at the previous cut and lettered since."""
        holds = {}  # (side, prefix, holdId, asset) -> dict

        def move(side, prefix, hold_id, asset, delta, e):
            key = (side, prefix, hold_id, asset)
            h = holds.setdefault(key, {'balance': 0, 'opened': None, 'last': None, 'last_ref': None})
            if h['balance'] == 0 and delta != 0:
                h['opened'] = e.t
            h['balance'] += delta
            h['last'] = e.t
            h['last_ref'] = e.ref if e.kind in ('apply', 'unclassified') or side == 'psp' else None

        for e in self.book.psp:
            if e.tx <= t_psp and e.hold:
                move('psp', PSP_HOLD, e.ref, e.asset, e.hold, e)
        for e in self.book.prod:
            if e.tx <= t_prod:
                for (p, hid, asset, d) in e.moves:
                    move('product', p, hid, asset, d, e)

        prev_stock = {(s['side'], s['hold'], s['asset']): s for s in (self.prev['stock'] if self.prev else [])}
        out = []
        for (side, prefix, hid, asset), h in sorted(holds.items()):
            address = prefix + hid
            key = (side, address, asset)
            was = prev_stock.get(key)
            was_open = was is not None and was['class'] != 'cleared'
            max_age = self.rule.psp_max_age if side == 'psp' else self.rule.product_max_age
            sign = OPEN_SIGN[prefix]
            if h['balance'] != 0:
                age = (day - local_day(h['opened'])).days
                od = sign * h['balance']
                klass = 'wrong_sign' if od < 0 else ('stuck' if age > max_age else 'open')
                s = {'side': side, 'hold': address, 'asset': asset, 'prefix': prefix, 'holdId': hid,
                     'openSign': SIGN_NAME[sign], 'balance': h['balance'], 'class': klass,
                     'outcome': 'break' if klass in ('wrong_sign', 'stuck') else 'ok',
                     'lifecycle': 'persisting' if was_open else 'new', 'openedAt': h['opened'],
                     'ageDays': age, 'bucket': bucket(age, self.rule.buckets)}
                out.append(s)
            elif was_open:
                age = (day - local_day(h['opened'])).days
                out.append({'side': side, 'hold': address, 'asset': asset, 'prefix': prefix, 'holdId': hid,
                            'openSign': SIGN_NAME[sign], 'balance': 0, 'class': 'cleared', 'outcome': 'ok',
                            'lifecycle': 'cleared', 'openedAt': h['opened'], 'ageDays': age,
                            'bucket': bucket(age, self.rule.buckets), 'previousBalance': was['balance'],
                            'clearedAt': h['last'], 'clearedBy': h['last_ref']})
        out.sort(key=lambda s: (s['side'], s['hold'], s['asset']))
        return out, holds

    def books(self, day, win_psp, win_prod, stock, f_psp, f_prod):
        """Per side, prefix and asset, in the open direction. On the product side, a transaction
        that takes part in matching always counts in `lettered`, with its signed amount, so that
        lettered - letteredOther is the window's applications (B)."""
        keys = {}

        def entry(side, prefix, asset):
            return keys.setdefault((side, prefix, asset), {'opened': 0, 'lettered': 0, 'other': 0})

        for e in win_psp:
            if e.hold:
                b = entry('psp', PSP_HOLD, e.asset)
                od = OPEN_SIGN[PSP_HOLD] * e.hold
                if od > 0:
                    b['opened'] += od
                else:
                    b['lettered'] += -od
                    if e.kind not in PSP_STATES:
                        b['other'] += -od
        for e in win_prod:
            for (p, _, asset, d) in e.moves:
                b = entry('product', p, asset)
                od = OPEN_SIGN[p] * d
                if e.kind == 'apply':
                    b['lettered'] += -od
                elif od > 0:
                    b['opened'] += od
                else:
                    b['lettered'] += -od
                    b['other'] += -od
        prev_open = {}
        if self.prev:
            prev_open = {(b['side'], b['prefix'], b['asset']): b['open'] for b in self.prev['books']}
        else:  # a first run rewinds to the start of its window
            for e in self.book.psp:
                if e.tx <= f_psp and e.hold:
                    k = ('psp', PSP_HOLD, e.asset)
                    prev_open[k] = prev_open.get(k, 0) + OPEN_SIGN[PSP_HOLD] * e.hold
            for e in self.book.prod:
                if e.tx <= f_prod:
                    for (p, _, asset, d) in e.moves:
                        k = ('product', p, asset)
                        prev_open[k] = prev_open.get(k, 0) + OPEN_SIGN[p] * d
        for s in stock:
            entry(s['side'], s['prefix'], s['asset'])
        for k in prev_open:
            entry(*k)
        out = []
        by_book = {}
        for s in stock:
            if s['class'] != 'cleared':
                by_book.setdefault((s['side'], s['prefix'], s['asset']), []).append(s)
        for (side, prefix, asset), b in sorted(keys.items()):
            rows = by_book.get((side, prefix, asset), [])
            open_ = sum(OPEN_SIGN[prefix] * s['balance'] for s in rows)
            open_prev = prev_open.get((side, prefix, asset), 0)
            assert open_ == open_prev + b['opened'] - b['lettered'], (day, side, prefix, asset)
            buckets = {bucket(a, self.rule.buckets): 0 for a in (0, 2, 8, 31)}
            for s in rows:
                buckets[s['bucket']] += 1
            out.append({'side': side, 'prefix': prefix, 'asset': asset, 'openSign': SIGN_NAME[OPEN_SIGN[prefix]],
                        'open_prev': open_prev, 'opened': b['opened'], 'lettered': b['lettered'],
                        'other': b['other'], 'open': open_, 'count': len(rows), 'buckets': buckets})
        return out

    def breaks(self, day, rows, stock):
        prev_open = {b['breakId']: b for b in (self.prev['breaks'] if self.prev else []) if b['open']}
        out = []
        seen = set()

        def break_id(leg, key, asset):
            return sha(f"{self.rule.id}|{leg}|{key}|{asset}".encode())[:16]

        candidates = [('flow', r['ref'], r['asset'], r) for r in rows if r['outcome'] == 'break']
        candidates += [('stock', f"{s['side']}/{s['hold']}", s['asset'], s) for s in stock if s['outcome'] == 'break']
        for leg, key, asset, r in candidates:
            bid = break_id(leg, key, asset)
            seen.add(bid)
            was = prev_open.get(bid)
            amount = r['drift'] if leg == 'flow' else OPEN_SIGN[r['prefix']] * r['balance']
            accepted = None
            acc_day = self.acceptances.get(key)
            if acc_day and dt.date.fromisoformat(acc_day) <= day:
                # an acceptance records the class and amount of its day, and lapses when either changes
                at_accept = self.accepted_state.setdefault(key, (r['class'], amount))
                if at_accept == (r['class'], amount):
                    accepted = dt.date.fromisoformat(acc_day)
            out.append({'breakId': bid, 'leg': leg, 'key': key, 'asset': asset, 'row': r,
                        'priority': PRIORITY[r['class']], 'lifecycle': 'persisting' if was else 'new',
                        'openedOn': was['openedOn'] if was else day, 'resolvedOn': None, 'amount': amount,
                        'class': r['class'],
                        'previousClass': was['class'] if was and was['class'] != r['class'] else None,
                        'acceptedOn': accepted, 'open': True})
        for bid, was in prev_open.items():
            if bid in seen:
                continue
            if was['leg'] == 'flow':
                row = next((r for r in rows if r['ref'] == was['key'] and r['asset'] == was['asset']), None)
            else:
                row = next((s for s in stock if f"{s['side']}/{s['hold']}" == was['key'] and s['asset'] == was['asset']), None)
            assert row is not None, ('a resolved break has no row today', was['key'])
            out.append({'breakId': bid, 'leg': was['leg'], 'key': was['key'], 'asset': was['asset'], 'row': row,
                        'priority': was['priority'], 'lifecycle': 'resolved', 'openedOn': was['openedOn'],
                        'resolvedOn': day, 'amount': was['amount'], 'class': was['class'],
                        'previousClass': None, 'acceptedOn': None, 'open': False})
        out.sort(key=lambda b: (0 if b['open'] else 1, b['priority'], -abs(b['amount']), b['breakId']))
        return out

    def unclassified(self, win_psp, win_prod):
        out = []
        for e in win_psp:
            if e.kind not in PSP_STATES:
                out.append({'side': 'psp', 'tx': e.tx, 'ref': e.ref, 'asset': e.asset, 'state': e.kind,
                            'amount': abs(e.amount) + abs(e.hold), 't': e.t})
        for e in win_prod:
            if e.kind == 'unclassified':
                for (p, _, asset, d) in e.moves:
                    out.append({'side': 'product', 'tx': e.tx, 'ref': e.ref, 'asset': asset,
                                'state': 'manual_fix', 'amount': abs(d), 't': e.t})
        out.sort(key=lambda u: (u['side'], u['tx'], u['asset']))
        return out

    # -- files -------------------------------------------------------------------------------

    @staticmethod
    def psp_item(e):
        item = {'tx': e.tx, 'state': PSP_STATES[e.kind]}
        if e.kind == 'final':
            item['amount'] = str(e.amount)
        else:
            item['holdAmount'] = str(abs(e.hold))
        item['insertedAt'] = iso(e.t)
        return item

    @staticmethod
    def product_items(events):
        return [{'tx': e.tx, 'businessId': hid, 'holdId': hid, 'amount': str(-OPEN_SIGN[p] * d),
                 'insertedAt': iso(e.t)} for e in events for (p, hid, _, d) in e.moves]

    def flow_json(self, r, with_impact=True):
        o = {'ref': r['ref'], 'asset': r['asset'], 'class': r['class'], 'outcome': r['outcome'],
             'pspAmount': str(r['pspAmount']), 'productAmount': str(r['productAmount']), 'drift': str(r['drift'])}
        if with_impact:
            o['impact'] = str(r['impact'])
        if r['firstSeen']:
            o['firstSeen'] = day_str(r['firstSeen'])
        if r['firstSide']:
            o['firstSide'] = r['firstSide']
        if r['breakOn']:
            o['breakOn'] = day_str(r['breakOn'])
        if r['merchant']:
            o['merchantRef'] = r['merchant']
        if r.get('pairedHold'):
            o['pairedHold'] = r['pairedHold']
        o['psp'] = [self.psp_item(e) for e in r['psp_ev']]
        o['product'] = self.product_items(r['app_ev'])
        return o

    @staticmethod
    def stock_json(s, with_lifecycle=True):
        o = {'side': s['side'], 'hold': s['hold'], 'asset': s['asset'], 'prefix': s['prefix'],
             'holdId': s['holdId'], 'openSign': s['openSign'], 'balance': str(s['balance']),
             'class': s['class'], 'outcome': s['outcome']}
        if with_lifecycle:
            o['lifecycle'] = s['lifecycle']
        o.update({'openedAt': iso(s['openedAt']), 'ageDays': s['ageDays'], 'bucket': s['bucket']})
        if s.get('pairedRef'):
            o['pairedRef'] = s['pairedRef']
        if s['class'] == 'cleared':
            o['previousBalance'] = str(s['previousBalance'])
            o['clearedAt'] = iso(s['clearedAt'])
            if s['clearedBy']:
                o['clearedBy'] = s['clearedBy']
        return o

    def break_json(self, b):
        o = {'breakId': b['breakId'], 'leg': b['leg'], 'priority': b['priority'], 'lifecycle': b['lifecycle'],
             'openedOn': day_str(b['openedOn'])}
        if b['resolvedOn']:
            o['resolvedOn'] = day_str(b['resolvedOn'])
        o['amount'] = str(b['amount'])
        if b['previousClass']:
            o['previousClass'] = b['previousClass']
        if b['acceptedOn']:
            o['acceptedOn'] = day_str(b['acceptedOn'])
        row = self.flow_json(b['row']) if b['leg'] == 'flow' else self.stock_json(b['row'], with_lifecycle=False)
        row['class'] = b['class']
        row['outcome'] = 'break' if b['open'] else 'ok'
        o.update(row)
        return o

    def write_run(self, run_dir, day, run_id, st):
        files = []

        def data_file(name, lines):
            data = gz([dump(l) for l in lines])
            write(os.path.join(run_dir, name), data)
            files.append({'name': name, 'rows': len(lines), 'sha256': sha(data),
                          'expiresAt': day_str(day + dt.timedelta(days=90))})

        data_file('flow.ndjson.gz', [self.flow_json(r) for r in st['rows']])
        data_file('carried.ndjson.gz', [self.flow_json(r, with_impact=False) for r in st['carried']])
        data_file('stock.ndjson.gz', [self.stock_json(s) for s in st['stock']])
        data_file('breaks.ndjson.gz', [self.break_json(b) for b in st['breaks']])
        data_file('unclassified.ndjson.gz', [
            {'side': u['side'], 'tx': u['tx'], 'ref': u['ref'], 'asset': u['asset'], 'outcome': 'warning',
             'state': u['state'], 'amount': str(u['amount']), 'insertedAt': iso(u['t'])} for u in st['unclassified']])
        st['path'] = os.path.relpath(run_dir, self.out)
        m = self.manifest(day, run_id, st, files)
        period_file = self.period_summary(day, st, m)
        if period_file is not None:  # listed in the manifest, which therefore cannot be in it
            data = (dump(period_file) + '\n').encode()
            write(os.path.join(run_dir, 'period.json'), data)
            files.append({'name': 'period.json', 'rows': len(period_file['days']), 'sha256': sha(data),
                          'expiresAt': day_str(day + dt.timedelta(days=395))})
        data = (dump(m) + '\n').encode()
        write(os.path.join(run_dir, 'manifest.json'), data)
        st['manifest'] = m
        st['manifest_sha'] = sha(data)

    def statement(self, day, st):
        out = {}
        assets = sorted({r['asset'] for r in st['rows']} | {b['asset'] for b in st['books']}
                        | {u['asset'] for u in st['unclassified']})
        prev_susp = self.prev['suspense'] if self.prev else {}
        st['suspense'] = {}
        for a in assets:
            rows = [r for r in st['rows'] if r['asset'] == a]
            f_prod, t_prod = st['cuts']['product']
            psp_a = sum(r['pspAmount'] - r['psp_before'] for r in rows)
            psp_n = sum(1 for e in st['win_psp'] if e.kind == 'final' and e.asset == a)
            prod_books = [b for b in st['books'] if b['side'] == 'product' and b['asset'] == a]
            prod_a = sum(b['lettered'] - b['other'] for b in prod_books)
            prod_n = sum(len(e.moves) for e in st['win_prod'] if e.kind == 'apply' and e.moves[0][2] == a)
            net = sum(r['impact'] for r in rows)
            assert net == psp_a - prod_a, (day, a, net, psp_a, prod_a)
            window_b = sum(-OPEN_SIGN[p] * d for r in rows for e in r['app_ev'] if f_prod < e.tx <= t_prod
                           for (p, _, _, d) in e.moves)
            assert window_b == prod_a, ('residual', day, a, window_b, prod_a)
            lines = {}
            for r in rows:
                if r['impact'] != 0:
                    k = (r['class'], r['outcome'], bool(r['firstSeen'] and r['firstSeen'] < day))
                    lines.setdefault(k, []).append(r)
            outside = {}
            for r in rows:
                if r['impact'] == 0 and r['drift'] != 0 and r['firstSeen'] and r['firstSeen'] < day:
                    outside.setdefault((r['class'], r['outcome']), []).append(r)

            def top(rs, by):
                return [r['ref'] for r in sorted(rs, key=lambda r: (-abs(r[by]), r['ref']))[:3]]

            breaks = [r for r in rows if r['outcome'] == 'break']
            carried = [r for r in st['carried'] if r['asset'] == a]
            open_prev, count_prev = prev_susp.get(a, (0, 0))
            from_lookups = sum(r['drift_before'] for r in rows if not r['carried_in'])
            open_ = sum(r['drift'] for r in carried)
            assert open_ == open_prev + net + from_lookups, ('open items', day, a)
            st['suspense'][a] = (open_, len(carried))
            uncl = {}
            for u in st['unclassified']:
                if u['asset'] == a:
                    k = (u['side'], u['state'])
                    s, n = uncl.get(k, (0, 0))
                    uncl[k] = (s + u['amount'], n + 1)
            out[a] = {
                "psp": {"amount": str(psp_a), "count": psp_n},
                "product": {"amount": str(prod_a), "count": prod_n},
                "net": str(net),
                "lines": [{"class": c, "outcome": o, "earlierDay": e, "amount": str(sum(r['impact'] for r in rs)),
                           "count": len(rs), "top": top(rs, 'impact')}
                          for (c, o, e), rs in sorted(lines.items(), key=lambda kv: (-sum(r['impact'] for r in kv[1]), kv[0]))],
                "residual": "0",
                "carriedOutside": [{"class": c, "outcome": o, "amount": str(sum(r['drift'] for r in rs)),
                                    "count": len(rs), "top": top(rs, 'drift')}
                                   for (c, o), rs in sorted(outside.items())],
                "flowGross": str(sum(abs(r['drift']) for r in breaks)),
                "offsetting": any(r['drift'] > 0 for r in breaks) and any(r['drift'] < 0 for r in breaks),
                "suspense": {"openPrev": str(open_prev), "countPrev": count_prev, "fromLookups": str(from_lookups),
                             "open": str(open_), "count": len(carried), "continuityOk": True},
                "unclassified": [{"side": s, "state": t, "amount": str(v[0]), "count": v[1]}
                                 for (s, t), v in sorted(uncl.items())],
            }
        return out

    def manifest(self, day, run_id, st, files):
        rows, stock, breaks = st['rows'], st['stock'], st['breaks']
        open_breaks = [b for b in breaks if b['open']]
        flow_counts = {c: sum(1 for r in rows if r['class'] == c) for c in FLOW_CLASSES}
        outcomes = {o: sum(1 for r in rows if r['outcome'] == o) for o in ('ok', 'pending', 'break')}
        stock_counts = {side: {c: sum(1 for s in stock if s['side'] == side and s['class'] == c) for c in STOCK_CLASSES}
                        for side in ('psp', 'product')}
        breaks_counts = {'new': sum(1 for b in breaks if b['lifecycle'] == 'new'),
                         'persisting': sum(1 for b in breaks if b['lifecycle'] == 'persisting'),
                         'resolved': sum(1 for b in breaks if b['lifecycle'] == 'resolved'),
                         'accepted': sum(1 for b in open_breaks if b['acceptedOn']),
                         'openByLeg': {leg: sum(1 for b in open_breaks if b['leg'] == leg) for leg in ('flow', 'stock')},
                         'openByPriority': {str(p): sum(1 for b in open_breaks if b['priority'] == p) for p in (1, 2, 3, 4)}}
        verdict = st['verdict']
        statement = self.statement(day, st)

        def triage_break(b):
            o = {'breakId': b['breakId'], 'priority': b['priority'], 'class': b['class'], 'lifecycle': b['lifecycle']}
            if b['leg'] == 'flow':
                o['ref'] = b['key']
            else:
                o['side'], o['hold'] = b['row']['side'], b['row']['hold']
            o.update({'asset': b['asset'], 'amount': str(b['amount'])})
            if b['leg'] == 'flow':
                hold_ids = [hid for e in b['row']['app_ev'] for (_, hid, _, _) in e.moves]
                if hold_ids:
                    o['holdIds'] = hold_ids
                elif b['row']['firstSeen']:
                    o['firstSeen'] = day_str(b['row']['firstSeen'])
            else:
                o['ageDays'] = b['row']['ageDays']
            if b['acceptedOn']:
                o['acceptedOn'] = day_str(b['acceptedOn'])
            return o

        pending = sorted([r for r in rows if r['outcome'] == 'pending'], key=lambda r: (r['breakOn'], -abs(r['drift']), r['ref']))
        resolved = [b for b in breaks if not b['open']]
        cuts = []
        for side, ledger in (('psp', 'psp'), ('product', 'main')):
            lo, hi = st['cuts'][side]
            events = self.book.psp if side == 'psp' else self.book.prod
            head = Book.last_tx(events, cutoff(day) + dt.timedelta(hours=2))
            cuts.append({'side': side, 'ledger': ledger, 'logFrom': lo, 'logTo': hi, 'txFrom': lo, 'txTo': hi,
                         'logHead': head, 'logSha256': sha(f'{ledger}:{hi}'.encode())})
        m = {
            "schemaVersion": "lettering/1", "engine": "reconciliation v1.4.0", "rule": self.rule_json,
            "runId": run_id,
        }
        if self.prev:
            m["previousRun"] = {"runId": self.prev['run'], "day": day_str(self.prev['day']),
                                "manifestSha256": self.prev['manifest_sha']}
        start = cutoff(day) + dt.timedelta(hours=2, seconds=5)
        m.update({
            "period": self.period(day),
            "startedAt": iso(start), "finishedAt": iso(start + dt.timedelta(seconds=27)),
            "timingsMs": {"cut": 400, "flow": 1100, "lookup": 30, "stock": 700, "watch": 650, "join": 90, "write": 100},
            "cuts": cuts,
            "execution": {"readRanges": 8, "maxConcurrentReads": 16, "stockFrom": "live",
                          "rewindLogs": {"psp": 0, "product": 0}, "lookups": st['lookups'],
                          "watchLogs": {"psp": st['cuts']['psp'][1] - st['cuts']['psp'][0],
                                        "product": st['cuts']['product'][1] - st['cuts']['product'][0]}},
            "verdict": verdict,
            "counts": {"flow": flow_counts, "flowOutcome": outcomes, "stock": stock_counts, "breaks": breaks_counts,
                       "unclassified": {side: sum(1 for u in st['unclassified'] if u['side'] == side)
                                        for side in ('psp', 'product')}},
            "anomalies": {"key_metadata_mutated": [{"side": s, "tx": t} for (s, t) in st['anomalies']]},
            "statement": statement,
            "books": [{"side": b['side'], "prefix": b['prefix'], "asset": b['asset'], "openSign": b['openSign'],
                       "openPrev": str(b['open_prev']), "opened": str(b['opened']), "lettered": str(b['lettered']),
                       "letteredOther": str(b['other']), "open": str(b['open']), "count": b['count'],
                       "buckets": b['buckets'], "continuityOk": True} for b in st['books']],
            "triage": {"topK": 10, "breaks": [triage_break(b) for b in breaks if b['open']][:10],
                       "pending": [dict({'ref': r['ref'], 'class': r['class'], 'asset': r['asset'],
                                         'amount': str(r['drift']), 'breakOn': day_str(r['breakOn'])},
                                        **({'pairedHold': r['pairedHold']} if r.get('pairedHold') else
                                           ({'holdIds': [hid for e in r['app_ev'] for (_, hid, _, _) in e.moves]}
                                            if r['app_ev'] else {})))
                                   for r in pending],
                       "resolved": [dict({'breakId': b['breakId'], 'class': b['class']},
                                         **({'ref': b['key']} if b['leg'] == 'flow' else
                                            {'side': b['row']['side'], 'hold': b['row']['hold']}),
                                         asset=b['asset'], amount=str(b['amount']),
                                         **({'clearedBy': b['row']['clearedBy']}
                                            if b['leg'] == 'stock' and b['row'].get('clearedBy') else {}))
                                    for b in resolved]},
            "files": files,
            "anchor": False,
            "expiresAt": day_str(day + dt.timedelta(days=90)),
        })
        return m

    def period_summary(self, day, st, current_manifest):
        """The last day of a weekly period writes period.json, built from the period's manifests."""
        if self.rule.period_type != 'weekly' or day.weekday() != 6:
            return None
        start = day - dt.timedelta(days=6)
        days = []
        for i in range(7):
            d = start + dt.timedelta(days=i)
            done = [r for r in self.runs if r['day'] == d and r['complete']]
            if d == day:
                done = [st]
            if not done:
                gap = 'incomplete' if any(r['day'] == d for r in self.runs) else 'no_run'
                days.append({'day': day_str(d), 'gap': gap})
                continue
            r = done[-1]
            m = current_manifest if d == day else r['manifest']
            entry = {'day': day_str(d), 'runId': r['run']}
            if d != day:  # the last day's manifest lists period.json, so its hash cannot be here
                entry['manifestSha256'] = r['manifest_sha']
            entry.update({'verdict': m['verdict'],
                          'counts': {k: m['counts'][k] for k in ('flow', 'flowOutcome', 'stock', 'breaks')},
                          'statement': {a: {'net': s['net'], 'flowGross': s['flowGross'], 'suspense': s['suspense']['open']}
                                        for a, s in m['statement'].items()},
                          'path': r['path'], 'expiresAt': m['expiresAt']})
            days.append(entry)
        return {"schemaVersion": "lettering/1", "rule": {"id": self.rule.id, "version": 1},
                "period": {"type": "weekly", "from": day_str(start), "to": day_str(day), "tz": "Europe/Paris"},
                "days": days}


# --- Scenarios ------------------------------------------------------------------------------

def inv(hid, amount, asset='EUR/2'):
    return [(INVOICE, hid, asset, -amount)]


def scenarios():
    """qa-scenarios: one story per class and edge case, 1–7 October 2026."""
    D = ['2026-09-30'] + [f'2026-10-0{i}' for i in range(1, 8)]
    psp, prod = [], []

    def pay(day, t, ref, amount, asset='EUR/2', pending=True, merchant=None, t_final=None):
        if pending:
            psp.append(Psp(at(day, t), ref, asset, 'pending', 0, amount))
        if t_final is not None:
            psp.append(Psp(at(day, t_final), ref, asset, 'final', amount, -amount if pending else 0, merchant))

    def apply(day, t, ref, moves):
        prod.append(Prod(at(day, t), 'apply', moves, ref))

    def opening(day, t, moves):
        prod.append(Prod(at(day, t), 'open', moves))

    # S12: paid and invoiced before backfillFrom, applied on day 3 (a PSP lookup, fromLookups != 0)
    opening(D[0], '07:00', inv('INV-S12', 5000))
    pay(D[0], '09:00', 'S12', 5000, pending=False, t_final='09:00')
    apply(D[3], '10:00', 'S12', [(INVOICE, 'INV-S12', 'EUR/2', 5000)])
    # S01: matched on its day
    opening(D[1], '07:00', inv('INV-S01', 10000))
    pay(D[1], '08:00', 'S01', 10000, t_final='08:05')
    apply(D[1], '08:10', 'S01', [(INVOICE, 'INV-S01', 'EUR/2', 10000)])
    # S02: one payment split across two invoices by one transaction
    opening(D[1], '07:01', inv('INV-S02A', 6000) + inv('INV-S02B', 4000))
    pay(D[1], '08:20', 'S02', 10000, t_final='08:25')
    apply(D[1], '08:30', 'S02', [(INVOICE, 'INV-S02A', 'EUR/2', 6000), (INVOICE, 'INV-S02B', 'EUR/2', 4000)])
    # S03: pending on day 1 (in_progress), final and applied on day 2
    opening(D[1], '07:02', inv('INV-S03', 7000))
    psp.append(Psp(at(D[1], '20:00'), 'S03', 'EUR/2', 'pending', 0, 7000))
    psp.append(Psp(at(D[2], '09:00'), 'S03', 'EUR/2', 'final', 7000, -7000))
    apply(D[2], '10:00', 'S03', [(INVOICE, 'INV-S03', 'EUR/2', 7000)])
    # S04: unapplied (paired with its invoice), accepted on day 3, applied short on day 4
    # (class changes, the acceptance lapses), completed on day 5 (resolved, earlier day)
    opening(D[1], '07:03', inv('INV-S04', 8000))
    pay(D[1], '09:00', 'S04', 8000, merchant='INV-S04', t_final='09:05')
    apply(D[4], '10:00', 'S04', [(INVOICE, 'INV-S04', 'EUR/2', 5000)])
    apply(D[5], '10:00', 'S04', [(INVOICE, 'INV-S04', 'EUR/2', 3000)])
    # S07: applied at pending on day 1, finalised on day 2 (matched, earlier day, positive impact)
    opening(D[1], '07:04', inv('INV-S07', 3000))
    apply(D[1], '11:00', 'S07', [(INVOICE, 'INV-S07', 'EUR/2', 3000)])
    psp.append(Psp(at(D[1], '12:00'), 'S07', 'EUR/2', 'pending', 0, 3000))
    psp.append(Psp(at(D[2], '12:00'), 'S07', 'EUR/2', 'final', 3000, -3000))
    # S08: applied at pending, never finalised: orphan when psp.grace (3 days) runs out
    opening(D[1], '07:05', inv('INV-S08', 2000))
    apply(D[1], '11:05', 'S08', [(INVOICE, 'INV-S08', 'EUR/2', 2000)])
    psp.append(Psp(at(D[1], '12:05'), 'S08', 'EUR/2', 'pending', 0, 2000))
    # S10: matched on day 1, failed at the PSP on day 3 (a product lookup: reversed after application)
    opening(D[1], '07:06', inv('INV-S10', 9000))
    pay(D[1], '13:00', 'S10', 9000, t_final='13:05')
    apply(D[1], '13:10', 'S10', [(INVOICE, 'INV-S10', 'EUR/2', 9000)])
    psp.append(Psp(at(D[3], '09:00'), 'S10', 'EUR/2', 'failed', 0, 0))
    # S11: failed on day 1 (class failed), applied anyway on day 2 (a PSP lookup: orphan at once)
    psp.append(Psp(at(D[1], '13:20'), 'S11', 'EUR/2', 'pending', 0, 1500))
    psp.append(Psp(at(D[1], '14:00'), 'S11', 'EUR/2', 'failed', 0, -1500))
    opening(D[1], '07:07', inv('INV-S11', 1500))
    apply(D[2], '11:00', 'S11', [(INVOICE, 'INV-S11', 'EUR/2', 1500)])
    # S14: matched on day 1, applied a second time on day 4 (over-applied, and a wrong-sign invoice)
    opening(D[1], '07:08', inv('INV-S14', 4000))
    pay(D[1], '14:00', 'S14', 4000, t_final='14:05')
    apply(D[1], '14:10', 'S14', [(INVOICE, 'INV-S14', 'EUR/2', 4000)])
    apply(D[4], '11:00', 'S14', [(INVOICE, 'INV-S14', 'EUR/2', 4000)])
    # S15: matched on day 1, refunded on day 3 on the original payment id (unclassified)
    opening(D[1], '07:09', inv('INV-S15', 2500))
    pay(D[1], '15:00', 'S15', 2500, t_final='15:05')
    apply(D[1], '15:10', 'S15', [(INVOICE, 'INV-S15', 'EUR/2', 2500)])
    psp.append(Psp(at(D[3], '10:00'), 'S15', 'EUR/2', 'payin.refunded', 2500, 0))
    # S18: an invoice never paid, stuck once older than maxAge (5 days)
    opening(D[1], '07:10', inv('INV-S18', 6000))
    # U01, U02: the second asset. U02 is finalised on day 2 and never applied.
    opening(D[1], '07:11', inv('INV-U01', 12000, 'USD/2'))
    pay(D[1], '16:00', 'U01', 12000, 'USD/2', t_final='16:05')
    apply(D[1], '16:10', 'U01', [(INVOICE, 'INV-U01', 'USD/2', 12000)])
    opening(D[1], '07:12', inv('INV-U02', 5000, 'USD/2'))
    pay(D[2], '16:00', 'U02', 5000, 'USD/2', t_final='16:05')
    # S05: applied short on day 2, completed on day 3
    opening(D[2], '07:00', inv('INV-S05', 10000))
    pay(D[2], '09:30', 'S05', 10000, t_final='09:35')
    apply(D[2], '09:40', 'S05', [(INVOICE, 'INV-S05', 'EUR/2', 9000)])
    apply(D[3], '09:40', 'S05', [(INVOICE, 'INV-S05', 'EUR/2', 1000)])
    # S06: over-applied on day 2 (wrong-sign invoice); an adjustment clears the hold on day 3,
    # which resolves the stock break, while the flow break stays
    opening(D[2], '07:01', inv('INV-S06', 5000))
    pay(D[2], '10:00', 'S06', 5000, t_final='10:05')
    apply(D[2], '10:10', 'S06', [(INVOICE, 'INV-S06', 'EUR/2', 6000)])
    prod.append(Prod(at(D[3], '08:00'), 'other', [(INVOICE, 'INV-S06', 'EUR/2', -1000)]))
    # S06B: the same, but the adjustment overshoots: the stock break resolves on a hold still open
    opening(D[2], '07:02', inv('INV-S06B', 5000))
    pay(D[2], '10:20', 'S06B', 5000, t_final='10:25')
    apply(D[2], '10:30', 'S06B', [(INVOICE, 'INV-S06B', 'EUR/2', 6000)])
    prod.append(Prod(at(D[3], '08:05'), 'other', [(INVOICE, 'INV-S06B', 'EUR/2', -1500)]))
    # S09: pending then failed on day 2, never applied (class failed)
    psp.append(Psp(at(D[2], '11:00'), 'S09', 'EUR/2', 'pending', 0, 2000))
    psp.append(Psp(at(D[2], '11:30'), 'S09', 'EUR/2', 'failed', 0, -2000))
    # S19: pending on day 2 (in_progress), applied on day 3 (a PSP lookup finds it pending:
    # applied_before_final), finalised on day 4 (matched, earlier day)
    opening(D[2], '07:03', inv('INV-S19', 3500))
    psp.append(Psp(at(D[2], '12:00'), 'S19', 'EUR/2', 'pending', 0, 3500))
    apply(D[3], '12:00', 'S19', [(INVOICE, 'INV-S19', 'EUR/2', 3500)])
    psp.append(Psp(at(D[4], '12:00'), 'S19', 'EUR/2', 'final', 3500, -3500))
    # R01: a refund, its own 1-to-1 pair: a refund hold that opens positive
    opening(D[2], '07:04', [(REFUND, 'RF-R01', 'EUR/2', 2000)])
    pay(D[3], '13:00', 'R01', 2000, pending=False, t_final='13:05')  # the payout's amount, in absolute value
    apply(D[3], '13:10', 'R01', [(REFUND, 'RF-R01', 'EUR/2', -2000)])
    # S16: an invoice cleared by a credit note (no PSP reference: letteredOther)
    opening(D[2], '07:05', inv('INV-S16', 3000))
    prod.append(Prod(at(D[4], '09:00'), 'other', [(INVOICE, 'INV-S16', 'EUR/2', 3000)]))
    # PU: applied by a transaction whose state is in no set (unclassified, product side)
    opening(D[2], '07:06', inv('INV-PU', 4500))
    pay(D[2], '14:00', 'PU', 4500, t_final='14:05')
    prod.append(Prod(at(D[2], '14:10'), 'unclassified', [(INVOICE, 'INV-PU', 'EUR/2', 4500)], 'PU'))
    # S13: pending on day 3, never finalised: its PSP hold is stuck past maxAge (3 days)
    psp.append(Psp(at(D[3], '15:00'), 'S13', 'EUR/2', 'pending', 0, 2200))
    # S20: matched on day 4, un-applied with its reference on day 5 (a negative application)
    opening(D[1], '07:13', inv('INV-S20', 3000))
    pay(D[4], '13:00', 'S20', 3000, t_final='13:05')
    apply(D[4], '13:10', 'S20', [(INVOICE, 'INV-S20', 'EUR/2', 3000)])
    apply(D[5], '13:10', 'S20', [(INVOICE, 'INV-S20', 'EUR/2', -3000)])

    rule = Rule('qa-scenarios', psp_grace=3, product_grace=1, psp_max_age=3, product_max_age=5,
                backfill_from=dt.date(2026, 10, 1))
    acceptances = {'S04': '2026-10-03', 'PU': '2026-10-04'}
    return rule, Book(psp, prod), D[1:], acceptances, {}


def verdicts():
    """qa-verdicts: a week, 5–11 October 2026, that walks through every verdict."""
    psp, prod = [], []

    def matched(day, t, ref, amount, t_apply=None, day_apply=None):
        prod.append(Prod(at(day, t), 'open', inv('INV-' + ref, amount)))
        psp.append(Psp(at(day, t), ref, 'EUR/2', 'pending', 0, amount))
        psp.append(Psp(at(day, t) + dt.timedelta(minutes=5), ref, 'EUR/2', 'final', amount, -amount))
        prod.append(Prod(at(day_apply or day, t_apply or t) + dt.timedelta(minutes=10), 'apply',
                         [(INVOICE, 'INV-' + ref, 'EUR/2', amount)], ref))

    matched('2026-10-05', '09:00', 'V1', 10000)                      # reconciled
    matched('2026-10-06', '21:00', 'V2', 6000, '07:00', '2026-10-07')  # pending, applied next morning
    psp.append(Psp(at('2026-10-07', '10:00'), 'V1', 'EUR/2', 'payin.refunded', 10000, 0))  # a warning
    # 8 October: nothing at all. 9 October: the run is incomplete.
    matched('2026-10-09', '09:00', 'V5', 7000)
    matched('2026-10-10', '21:00', 'V6', 4000, '07:00', '2026-10-11')
    rule = Rule('qa-verdicts', psp_grace=3, product_grace=1, psp_max_age=10, product_max_age=30,
                backfill_from=dt.date(2026, 10, 5), period_type='weekly')
    return rule, Book(psp, prod)


# --- Expected query results, computed without DuckDB ----------------------------------------

def csv_text(header, rows):
    buf = io.StringIO()
    w = csv.writer(buf, lineterminator='\n')
    w.writerow(header)
    for r in rows:
        w.writerow(['' if v is None else ('true' if v is True else 'false' if v is False else v) for v in r])
    return buf.getvalue()


def ts(t):
    return t.strftime('%Y-%m-%d %H:%M:%S')


def expected(engine, out):
    """One CSV per query and day, in the query's own column order. Row order is not compared."""
    runs = [r for r in engine.runs if r['complete']]
    current = {}
    for r in runs:
        current[r['day']] = r  # runs are appended in time order, so the last one per day wins
    days = sorted(current)
    base = os.path.join(out, 'expected', f'rule={engine.rule.id}')
    res = {}

    res['current-runs'] = csv_text(
        ['day', 'run', 'verdict', 'current', 'reason'],
        [[day_str(r['day']), r['run'], r['verdict'], r['complete'] and current[r['day']] is r, r.get('reason')]
         for r in engine.runs])
    rows = []
    for d in days:
        for a in sorted({x['asset'] for x in current[d]['rows']}):
            for c in FLOW_CLASSES:
                for o in ('ok', 'pending', 'break'):
                    rs = [x for x in current[d]['rows'] if x['asset'] == a and x['class'] == c and x['outcome'] == o]
                    if rs:
                        rows.append([day_str(d), a, c, o, len(rs), sum(x['pspAmount'] for x in rs),
                                     sum(x['productAmount'] for x in rs), sum(x['drift'] for x in rs),
                                     sum(x['impact'] for x in rs)])
    res['daily-flow'] = csv_text(['day', 'asset', 'class', 'outcome', 'payments', 'psp_amount', 'product_amount',
                                  'drift', 'impact'], rows)
    for d in days:
        st = current[d]
        tag = day_str(d)
        lines = {}
        for x in st['rows']:
            if x['impact'] != 0:
                k = (x['asset'], x['class'], x['outcome'], (x['firstSeen'] < d) if x['firstSeen'] else None)
                s, n = lines.get(k, (0, 0))
                lines[k] = (s + x['impact'], n + 1)
        res[f'bridge@{tag}'] = csv_text(['asset', 'class', 'outcome', 'earlier_day', 'amount', 'payments'],
                                        [list(k) + list(v) for k, v in lines.items()])
        ob = []
        for b in st['breaks']:
            if b['open'] and not b['acceptedOn']:
                r = b['row']
                if b['leg'] == 'flow':
                    detail = ', '.join(sorted({hid for e in r['app_ev'] for (_, hid, _, _) in e.moves})) or None
                    ref, hold = r['ref'], None
                else:
                    detail, ref, hold = f"{r['ageDays']} days old", None, r['hold']
                ob.append([b['priority'], b['class'], b['lifecycle'], b['asset'], b['amount'], ref, hold,
                           day_str(b['openedOn']), (d - b['openedOn']).days, detail])
        res[f'open-breaks@{tag}'] = csv_text(['priority', 'class', 'lifecycle', 'asset', 'amount', 'ref', 'hold',
                                              'opened_on', 'days_open', 'detail'], ob)
        pend = []
        for x in st['rows']:
            if x['outcome'] == 'pending':
                applied = sorted({hid for e in x['app_ev'] for (_, hid, _, _) in e.moves})
                pend.append([day_str(x['breakOn']), (x['breakOn'] - d).days, x['class'], x['asset'], x['drift'],
                             x['ref'], x['merchant'], x.get('pairedHold'),
                             '[' + ', '.join(applied) + ']'])
        res[f'pending@{tag}'] = csv_text(['break_on', 'days_left', 'class', 'asset', 'amount', 'ref', 'merchant_ref',
                                          'paired_hold', 'applied_to'], pend)
        f_prod, t_prod = st['cuts']['product']
        f_psp, t_psp = st['cuts']['psp']
        apps = []
        for x in st['rows']:
            for e in x['app_ev']:
                if f_prod < e.tx <= t_prod:
                    for (p, hid, _, dlt) in e.moves:
                        apps.append([tag, x['ref'], x['class'], x['outcome'], e.tx, hid, hid, -OPEN_SIGN[p] * dlt, ts(e.t)])
        res[f'applications@{tag}'] = csv_text(['day', 'ref', 'class', 'outcome', 'tx', 'business_id', 'hold_id',
                                               'amount', 'inserted_at'], apps)
        evs = []
        for x in st['rows']:
            for e in x['psp_ev']:
                if f_psp < e.tx <= t_psp:
                    evs.append([tag, x['ref'], x['class'], x['outcome'], e.tx, PSP_STATES[e.kind],
                                e.amount if e.kind == 'final' else None,
                                abs(e.hold) if e.kind != 'final' else None, ts(e.t)])
        res[f'psp-events@{tag}'] = csv_text(['day', 'ref', 'class', 'outcome', 'tx', 'state', 'amount', 'hold_amount',
                                             'inserted_at'], evs)
    oi = []
    for d in days:
        m = current[d]['manifest']
        for a, s in m['statement'].items():
            oi.append([day_str(d), a, m['verdict'], s['suspense']['openPrev'], s['net'], s['suspense']['fromLookups'],
                       s['suspense']['open'], s['suspense']['count'], s['flowGross']])
    res['open-items'] = csv_text(['day', 'asset', 'verdict', 'open_prev', 'net', 'from_lookups', 'open', 'items',
                                  'open_breaks_gross'], oi)
    res['books'] = csv_text(['day', 'side', 'prefix', 'asset', 'open_prev', 'opened', 'lettered', 'lettered_other',
                             'open', 'holds'],
                            [[day_str(d), b['side'], b['prefix'], b['asset'], b['open_prev'], b['opened'],
                              b['lettered'], b['other'], b['open'], b['count']]
                             for d in days for b in current[d]['books']])
    sa = {}
    for d in days:
        for s in current[d]['stock']:
            if s['class'] == 'cleared':
                continue
            k = (day_str(d), s['side'], s['prefix'], s['asset'], s['bucket'])
            n, amt, stuck, wrong = sa.get(k, (0, 0, 0, 0))
            sa[k] = (n + 1, amt + OPEN_SIGN[s['prefix']] * s['balance'], stuck + (s['class'] == 'stuck'),
                     wrong + (s['class'] == 'wrong_sign'))
    res['stock-ageing'] = csv_text(['day', 'side', 'prefix', 'asset', 'bucket', 'holds', 'amount', 'stuck',
                                    'wrong_sign'], [list(k) + list(v) for k, v in sa.items()])
    lo = []
    for d in days:
        for b in current[d]['books']:
            if b['other'] != 0:
                lo.append([day_str(d), 'book', b['side'], b['prefix'], b['asset'], None, b['other'], None])
        for s in current[d]['stock']:
            if s['class'] == 'cleared' and not s['clearedBy'] and OPEN_SIGN[s['prefix']] * s['previousBalance'] > 0:
                lo.append([day_str(d), 'hold', s['side'], s['prefix'], s['asset'], s['hold'],
                           OPEN_SIGN[s['prefix']] * s['previousBalance'], ts(s['clearedAt'])])
    res['lettered-other'] = csv_text(['day', 'kind', 'side', 'prefix', 'asset', 'hold', 'amount', 'cleared_at'], lo)
    for bid in engine.business_ids:
        res[f'business-id@id={bid}'] = business_id(current, days, bid)
    for name, text in res.items():
        tag = name.replace('@', '_')
        if '@' in name and '=' not in name:
            tag = name.replace('@', '_day=')
        write(os.path.join(base, tag + '.csv'), text.encode())


def business_id(current, days, bid):
    """Mirrors queries/business-id.sql."""
    out = []
    for d in days:
        st = current[d]
        tag = day_str(d)
        for x in st['rows']:
            holds = {hid for e in x['app_ev'] for (_, hid, _, _) in e.moves}
            if bid == x['ref'] or x['merchant'] == bid or bid in holds:
                out.append([tag, 'flow', x['class'], x['outcome'], x['ref'], x.get('pairedHold'), x['drift'],
                            f"psp {x['pspAmount']}, product {x['productAmount']}"])
        for s in st['stock']:
            if bid in (s['holdId'], s.get('clearedBy'), s.get('pairedRef')):
                out.append([tag, 'stock', s['class'], s['outcome'], s.get('clearedBy') or s.get('pairedRef'), s['hold'],
                            OPEN_SIGN[s['prefix']] * s['balance'], f"{s['lifecycle']}, {s['ageDays']} days old"])
        for b in st['breaks']:
            r = b['row']
            if b['leg'] == 'flow':
                holds = {hid for e in r['app_ev'] for (_, hid, _, _) in e.moves}
                hit = bid == r['ref'] or r['merchant'] == bid or bid in holds
                ref, hold = r['ref'], None
            else:
                hit = bid == r['holdId']
                ref, hold = None, r['hold']
            if hit:
                detail = f"P{b['priority']}" + (f" resolved on {b['resolvedOn']}" if not b['open']
                                                else f" {b['lifecycle']} since {b['openedOn']}")
                if b['acceptedOn']:
                    detail += f", accepted on {b['acceptedOn']}"
                out.append([tag, 'breaks', b['class'], 'break' if b['open'] else 'ok', ref, hold, b['amount'], detail])
        for u in st['unclassified']:
            if u['ref'] == bid:
                out.append([tag, 'unclassified', u['state'], 'warning', u['ref'], None, u['amount'], f"tx {u['tx']}"])
    return csv_text(['day', 'file', 'class', 'outcome', 'ref', 'hold', 'amount', 'detail'], out)


# --- Main -------------------------------------------------------------------------------------

def main():
    out = HERE
    for name in os.listdir(out):
        if name.startswith('rule=') or name == 'expected':
            shutil.rmtree(os.path.join(out, name))
    worked_example(out)

    rule, book, days, acceptances, anomalies = scenarios()
    engine = Engine(rule, book, out, acceptances, anomalies)
    engine.business_ids = ['INV-S04', 'S14', 'PU', 'S15', 'INV-S06B']
    for d in days:
        engine.run(d)
    expected(engine, out)

    rule, book = verdicts()
    engine = Engine(rule, book, out, anomalies={'2026-10-10': [('product', 504)]})
    engine.run('2026-10-05')
    engine.run('2026-10-06', run_id='r-20261007T000003Z', incomplete='short_log_range')
    engine.run('2026-10-06', run_id='r-20261007T001503Z')
    engine.run('2026-10-07')
    engine.run('2026-10-08')
    engine.run('2026-10-09', incomplete='short_log_range')
    engine.run('2026-10-10')
    engine.run('2026-10-11')
    expected(engine, out)


if __name__ == '__main__':
    main()
