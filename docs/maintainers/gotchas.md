# Gotchas

Non-obvious traps. Each is something that cost real debugging time. Verify against the code before relying on a detail here. _Updated 2026-06-04._

## Frontend / mobile

### xterm.js is on v6 — its viewport scrolls only via wheel events
`@xterm/xterm@6.0.0` changed the viewport: `.xterm-viewport` is a `position:absolute` overlay with no natively-scrollable area, so **mouse wheel scrolls but touch-drag does not**. On mobile the scrollback was unreachable. Fix lives in `web/src/pages/Terminal.tsx`: a `touchstart`/`touchmove` (capture-phase) handler that converts a vertical drag into `term.scrollLines()`, plus `touch-action: none` on the terminal container so the browser doesn't preempt the gesture. Don't "simplify" this away thinking native scroll works — it doesn't on touch in v6.

### xterm `fit()` must run after layout settles, not on raw resize events
The mobile soft keyboard fires a burst of `visualViewport` resizes during its open/close animation. Fitting on each one churns the PTY row count and piles up blank rows. `Terminal.tsx` debounces the fit (~140ms), guards with `fitAddon.proposeDimensions()` (skip if rows/cols unchanged), and `scrollToBottom`s after. `Layout.tsx` also re-reads `visualViewport.height` a few times after mount because some browsers report a stale height when a page loads with the keyboard already up. See also [known-issues.md](known-issues.md) — the Samsung-Internet keyboard-toggle blank is still not fully solved.

### PWA service worker serves a stale shell after deploy
`vite-plugin-pwa` is `registerType: 'autoUpdate'` (+ `skipWaiting`/`clientsClaim`). After a deploy, an open tab keeps the old cached chunks until it reloads. `web/src/main.tsx` handles this two ways: `vite:preloadError` → reload (recovers from a stale chunk that now 404s), and a `controllerchange` listener + 60s `reg.update()` poll → auto-reload to the new build. **Symptom to recognize:** "I deployed but the phone still shows the old UI" is almost always this cache, not a code bug — clear site data once or wait for the auto-reload.

### i18n must stay in parity (en.json ↔ ko.json)
Every new `t('...')` key goes in **both** `web/src/i18n/locales/en.json` and `ko.json`. There's no test for it — it's review-enforced. A missing key renders the raw key string.

## Backend

### `response.SanitizeOutput` truncates to 500 chars — don't use it for logs
`internal/api/response/sanitize.go:SanitizeOutput` is for short command output / error strings: it strips ANSI + redacts `password|secret|token|key` assignments **and caps at 500 chars**. For multi-line log views use `SanitizeLog` (same redaction, no cap). The cron-logs endpoint hit this — logs came back as ~4 lines until switched to `SanitizeLog`.

### App Store port-conflict check: the overridable PORT supersedes `meta.Ports`
`internal/feature/appstore/handler.go:checkPortConflicts`. Catalog apps declare both a static `ports:[X]` and a `PORT` env defaulting to `X`. If the user changes `PORT` to dodge a busy default, the static `X` must **not** be checked (it's superseded by the env value). The rule: a `meta.Ports` entry equal to a `port`-type env default is the overridable port; only check the resolved env value for it. ~84/89 catalog apps share this overlap, so getting it wrong broke "change the port when busy" for almost everything.

### App Store catalog is fetched at runtime + bundled
Catalog lives in-repo under `appstore/` but is fetched at runtime from `raw.githubusercontent.com/.../main/appstore/`. `make appstore-catalog` bundles all per-app `metadata.json` into one `appstore/catalog.json` (one fetch, atomic); `catalog_test.go:TestCatalogBundleUpToDate` fails CI if the bundle is stale. Editing an app = edit per-app file → `make appstore-catalog` → commit both. The panel caches 1h (DB-persisted) and `raw.githubusercontent` adds ~5 min CDN propagation; the in-UI Refresh forces a re-fetch (+ cache-bust query). After editing an app you won't see it instantly — that's the cache, not a bug.

## Ops / deploy

### This is a single embedded binary; frontend changes need a rebuild
`go:embed all:web/dist` — a frontend-only change still requires `npm run build` then `go build` (to re-embed) before deploying. Deploy = build → `systemctl stop sfpanel` → `cp` binary to `/usr/local/bin/sfpanel` → `systemctl start sfpanel`, on each node.

### Reaching the peer node: use Tailscale, not the LAN IP
The peer (`peer-node`) is reachable at Tailscale `100.x.x.x` (tailnet name `<tailnet>`). Its LAN IP `192.168.1.x` has an **IP conflict** (a Tuya Wi-Fi device also claims it), so LAN SSH/HTTP is flaky. See [known-issues.md](known-issues.md). Deploy to the peer via `scp` over the tailscale IP.
