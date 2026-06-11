# SFPanel e2e suite (Playwright)

API + UI smoke tests against a running panel. CI runs the single-node specs
in the `e2e-smoke` job (`.github/workflows/ci.yml`); the 2-node cluster specs
self-skip unless their env vars point at a live cluster.

## Running locally

Never point the suite at a production panel — the disruptive lifecycle test
exits the process and the password-replication test rotates the admin
password (it reverts, but only if nothing crashes mid-test). Boot a throwaway
panel instead:

```bash
# 1. Build (web/dist must exist — `make build` produces both)
make build

# 2. Throwaway panel on an unused port with a temp DB
TMP=$(mktemp -d)
cat > "$TMP/config.yaml" <<EOF
server:
  port: 13628
database:
  path: $TMP/sfpanel.db
auth:
  jwt_secret: $(openssl rand -hex 32)
EOF
# sudo: the binary refuses to start as non-root
sudo ./sfpanel "$TMP/config.yaml" &

# 3. Seed the admin account (CSRF-exempt bootstrap endpoint)
curl -fsS -X POST http://localhost:13628/api/v1/auth/setup \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"TestPass123!"}'

# 4. Run the single-node specs
cd e2e && npm ci && npx playwright install chromium
PLAYWRIGHT_BASE_URL=http://localhost:13628 npx playwright test \
  tests/connect-port.spec.ts tests/sidebar-banner.spec.ts \
  tests/tuning-categories.spec.ts tests/cluster-lifecycle.spec.ts
```

## Environment variables

| Variable | Default | Used by |
|---|---|---|
| `PLAYWRIGHT_BASE_URL` | `http://localhost:3628` | config `baseURL` — all relative-URL specs |
| `PW_USER` / `PW_PASS` | `admin` / `TestPass123!` | every spec that logs in |
| `PW_BASE_URL` | — | 2-node specs (leader URL, absolute) |
| `PW_FOLLOWER_URL` | — | cluster-password-replication (direct follower URL) |
| `PW_REMOTE_NODE_ID` / `PW_REMOTE_HOST` | — | cluster-remote-node |
| `PLAYWRIGHT_CLUSTER_DISRUPTIVE` | unset | opt-in for the init→disband test (process self-exits) |

## Constraints

- **The test account must have NO TOTP enrolled.** Every spec authenticates
  with username + password only (API login or the login form); a 2FA
  challenge fails the run. Seed CI/throwaway accounts via `/api/v1/auth/setup`
  and never enable 2FA on them.
- Non-GET API calls must echo the `sfpanel_csrf` cookie via `X-CSRF-Token`
  (double-submit CSRF, enforced since 2026-05-15). Use `login()` from
  `tests/helpers.ts` and pass its `headers` — it captures the cookie from the
  request context's jar.
- Specs needing a 2-node cluster gate on their env vars and skip when absent,
  so a plain `npx playwright test` on a standalone panel stays green.
