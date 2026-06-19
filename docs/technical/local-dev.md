# Local development

The repo ships an **isolated** local stack at [`local/`](../../local/). It runs alongside (not on top of) the daily-driver `stack/` setup — different ports, different volumes, different Compose project name. Bringing it up does not touch anything in `stack/`.

> Discipline: when adding new dev tooling, default to project-local files. A daily-driver `stack/` setup outside this repo is off-limits unless explicitly authorized.

---

## Bring it up

```bash
cd <path-to-reconciliation-checkout>
just local-up        # start in background
just local-ps        # see what's running
just local-smoke     # connectivity check through the gateway
just local-logs      # tail all services (just local-logs reconciliation-api for one)
just local-down      # stop (keeps the volume)
just local-reset     # stop + wipe the postgres volume (destroys data)
```

First boot pulls ~10 images. Postgres init is idempotent across `local-down/up` and creates three databases (`ledger`, `payments`, `reconciliation`) — see [`local/postgres-init/01-databases.sql`](../../local/postgres-init/01-databases.sql).

---

## Services + ports

| Service                | Port (host)        | Notes                                                                 |
|------------------------|--------------------|-----------------------------------------------------------------------|
| Postgres               | **5433**           | Three DBs: `ledger`, `payments`, `reconciliation`                     |
| Temporal               | **7233** (gRPC), **8233** (Web UI) | `temporalio/temporal:1.7.2`, embedded dev server, in-memory |
| Ledger v2              | **3168**           | `ghcr.io/formancehq/ledger:v2.4.10`                                   |
| Payments API           | **8190**           | `payments serve` (v3 — `api server` was renamed to `serve`)           |
| Payments Connectors    | **8191**           | `payments run-worker` (v3 — `connectors server` was renamed)          |
| Reconciliation API     | **8290**           | Published image until our local build replaces it                     |
| **Gateway (Caddy)**    | **8180** / **8543** TLS | Routes `/api/ledger`, `/api/payments`, `/api/reconciliation`     |
| **Console**            | **3100**           | UI at http://localhost:3100; talks via gateway                         |

All container/volume names are prefixed `recon-v1-*`. Stack volumes (`postgres_data`, `pgadmin_data`, `clickhouse_data`) are untouched.

Override any of these via [`local/.env.example`](../../local/.env.example) → copy to `local/.env`.

---

## Auth (the Caddy fake-token snippet)

The Formance SDK does OAuth2 client_credentials against `${STACK_URL}/api/auth/oauth/token` before every outbound call. We have no auth issuer in this compose, so [`local/gateway/Caddyfile`](../../local/gateway/Caddyfile) ships a snippet that hands back a static bearer token:

```caddyfile
(fake_oauth_token) {
    handle /api/auth/oauth/token {
        header Content-Type application/json
        respond `{"access_token":"local-dev-token","token_type":"Bearer","expires_in":3600}` 200
    }
}
```

Downstream services (ledger, payments) don't enforce auth in local mode (no `--auth-issuer` set), so the token value is irrelevant — we just need the SDK's OAuth handshake to succeed.

**This is local-only.** Production OAuth wiring is unchanged.

---

## Smoke test

`bash local/smoke.sh` (or `just local-smoke`) hits each service through the gateway:

```text
Smoke-testing http://localhost:8180

  gateway /versions                200 OK
  ledger _info                     200 OK
  payments _info                   200 OK
  reconciliation _info             200 OK
```

If `reconciliation _info` fails, check `docker compose -f local/docker-compose.yml logs reconciliation-api`. Most likely the published image's auth middleware demands an issuer — flip the Caddy snippet to verify.

---

## End-to-end baseline against the generic-connector mock

The user maintains a hosted generic-connector mock at https://formance-generic-connector-mock.fly.dev/admin.html. Install it pointing at the user's `apiKey` and let it poll:

```bash
GW=http://localhost:8180
curl -X POST "$GW/api/payments/v3/connectors/install/generic" \
  -H "Content-Type: application/json" -d '{
  "name":          "mock-connector",
  "apiKey":        "<your-mock-apikey>",
  "endpoint":      "https://formance-generic-connector-mock.fly.dev",
  "pollingPeriod": "5s"
}'
```

The compose pre-sets `CONNECTOR_POLLING_PERIOD_MINIMUM=1s` / `_DEFAULT=5s` on both payments services so iteration is fast (default minimum is 20m).

Mock events are all in **EUR/2** by default. To reconcile against `reco-ledger`, post EUR/2 transactions and set matching account metadata.

---

## Reconciliation baseline (legacy `/policies` path)

```bash
GW=http://localhost:8180

# 1. Create a ledger with ACCOUNT_METADATA_HISTORY: ON (avoids ledger#1416)
curl -X POST "$GW/api/ledger/v2/reco-ledger-2" -d '{
  "features": { "ACCOUNT_METADATA_HISTORY": "SYNC" }
}'

# 2. Post a transaction + set account metadata
curl -X POST "$GW/api/ledger/v2/reco-ledger-2/transactions" \
  -H "Content-Type: application/json" -d '{
  "postings":[{"source":"world","destination":"merchant:m1:held","amount":100,"asset":"USD/2"}]
}'
curl -X POST "$GW/api/ledger/v2/reco-ledger-2/accounts/merchant:m1:held/metadata" \
  -H "Content-Type: application/json" -d '{"trust":"true"}'

# 3. Install connector + wait for accounts → create pool (see above).
# 4. Create policy and trigger reconciliation:
POOL_ID="<from /api/payments/v3/pools>"
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
POLICY_ID=$(curl -s -X POST "$GW/api/reconciliation/policies" \
  -H "Content-Type: application/json" -d "{
  \"name\":\"baseline\",\"ledgerName\":\"reco-ledger-2\",
  \"ledgerQuery\":{\"\$match\":{\"metadata[trust]\":\"true\"}},
  \"paymentsPoolID\":\"$POOL_ID\"
}" | grep -oE '"id":"[^"]+"' | head -1 | cut -d'"' -f4)

curl -X POST "$GW/api/reconciliation/policies/$POLICY_ID/reconciliation" \
  -H "Content-Type: application/json" -d "{
  \"reconciledAtLedger\":   \"$NOW\",
  \"reconciledAtPayments\": \"$NOW\"
}"
```

Expected result: `status: "OK"` with populated `ledgerBalances` (and `paymentsBalances: {"USD/2": 0}` because the legacy SDK call hits the empty PIT route — that's a known issue captured in [v1-vs-legacy.md §5](./v1-vs-legacy.md#5-payments-side-read)).

---

## Driving V1 visually — `poc-reconciliation-demo`

For an interactive end-to-end exploration (the replacement for the planned dockertest harness), use the sibling repo [`poc-reconciliation-demo`](../../../poc-reconciliation-demo):

```bash
cd ../poc-reconciliation-demo
pnpm install
pnpm dev          # http://localhost:3002
```

The UI proxies `/api/*` → the local gateway at `:8180`, so as long as `just local-up` is running you have everything wired. The headline page is `/demo` — one button runs the full V1 lifecycle (create rule → eval pass → break → eval fail → re-eval → fix → auto-resolve → break again → re-open with parent link). Same flow as [`v1_orchestration_test.go`](../../internal/api/service/v1_orchestration_test.go), validated visually.

The right-hand documentation panel mirrors selected sections of this `docs/` tree (templates, workflows, resolution paths).

## Building the reconciliation image locally

The compose pins `RECONCILIATION_TAG=latest` by default — that's the **published** image (currently `v2.0.33`, pre-V1). To run your local V1 code inside the docker stack:

```bash
just local-rebuild   # build local Dockerfile → image :dev → restart recon-api container
```

Under the hood:

1. **`local-build-recon`** runs `docker build -t ghcr.io/formancehq/reconciliation:dev -f local/Dockerfile .` (multi-stage Go build → distroless static image, ~8MB).
2. **`local-rebuild`** also writes `RECONCILIATION_TAG=dev` to `local/.env` if not already set and recreates the `reconciliation-migrate` + `reconciliation-api` containers.

Verify with:

```bash
curl -s http://localhost:8180/api/reconciliation/rules
# {"cursor":{"pageSize":15,"hasMore":false,"data":[…]}}  ← V1 surface live
```

For unit-test iteration (no container, no docker), `just build` writes `./bin/reconciliation` and `just tests` runs the full suite without touching the stack.

### Known wiring quirk — go-libs v3/v5 logger bridge

The repo mixes `github.com/formancehq/go-libs` (v3, used by `service.New`) and `github.com/formancehq/go-libs/v5` (used by `messagingfx` + the V1 SDK paths). v3's `service.New` supplies a v3 `logging.Logger`; v5's `messagingfx` wants a v5 `observe/log.Logger` (same name, different package, different type).

[`internal/api/module.go`](../../internal/api/module.go) supplies a v5 logger explicitly via `fx.Provide`. The published `:latest` image predates the v5 messaging dep so doesn't need this — but the locally built `:dev` image absolutely does.

---

## Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `dependency failed to start: container recon-v1-payments-api is unhealthy` | `wget` not in payments image (only `curl`) | Already handled in compose — verify with `docker compose -f local/docker-compose.yml logs payments-api` |
| `Error: unknown command "api"` in payments logs | Stale payments tag without v3 rename | Set `PAYMENTS_TAG=v3.3.1` or newer in `local/.env` |
| `oauth2: cannot fetch token: 502 Bad Gateway` in reconciliation logs | Gateway missing the `fake_oauth_token` snippet | Restart gateway: `docker compose -f local/docker-compose.yml restart gateway` |
| Aggregate balances empty with metadata filter under `pit=` | `ACCOUNT_METADATA_HISTORY: DISABLED` on the target ledger | Use `reco-ledger-2` (history `SYNC`) or use an address-based query. See [v1-vs-legacy.md §6](./v1-vs-legacy.md#6-ledger-side-feature-flag-gotcha) |
| Pool balances endpoint returns `[]` | Legacy SDK route, empty under payments v3 | V1 resolver uses `/v3/pools/{id}/balances/latest` — legacy `/policies` will keep showing this until task #6's facade routes through the new resolver |
