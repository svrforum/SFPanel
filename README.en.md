<p align="center">
  <img src="banner.png" alt="SFPanel" width="518" />
</p>

# SFPanel

<p align="center"><a href="README.md">한국어</a> · <strong>English</strong></p>

**An all-in-one server management web panel for homelabs, VPSes and NAS boxes.** One single Go binary manages Docker, firewall, disk, network, terminal and an app store — ready the moment it's installed, with zero runtime dependencies.

- 🪶 **Single binary** — Go backend + embedded React SPA. SQLite built in (CGO-free), zero external dependencies. One `curl | sudo bash` and you're done.
- 🐳 **Docker optional** — if the socket is reachable it manages containers and Compose stacks; if not, only those menus disappear and everything else keeps working.
- 🔒 **Security built in** — JWT + TOTP 2FA + one-time recovery codes, login rate-limiting, type-to-confirm on destructive actions.
- 📱 **Desktop to mobile** — Korean/English, responsive web + PWA, plus native Windows/macOS/Linux apps (Tauri).
- 🧩 **Cluster optional** — single-node by default. Scale out to a Raft multi-node setup (live overview + transparent `?node=` proxy) when you need it.

## Features

| Area | What it does |
|------|------|
| **Dashboard** | Real-time CPU/memory/disk/network monitoring (WebSocket), 24-hour history charts, Docker summary, quick-action shortcuts |
| **Docker** | Containers, images, volumes, networks; Compose stacks (per-service detail, current→target digest updates, rollback); Hub search; resource pruning |
| **App Store** | One-click install of **90+** curated self-hosted apps (\*arr, Nextcloud, Vaultwarden, Immich, AdGuard, Authentik, Forgejo and more). Recommended badges, sorting & search, "update available" badge, keep-data uninstall, post-install health check |
| **File manager** | In-browser file explorer + Monaco editor, recursive search, copy/multi-select delete, upload/download |
| **Terminal** | xterm.js multi-tab web terminal (PTY), session persistence & reconnect, 10,000-line scrollback, mobile touch scrolling |
| **Process · Service · Cron** | Process tree, renice, signals (TERM/KILL/STOP/CONT, …); systemd service control + unit view; crontab GUI + run-now with output capture + **system cron run logs** |
| **Logs** | Real-time streaming of system/custom logs (SSE), structured parsing (auth, UFW, Fail2ban, sfpanel), search, level coloring, download |
| **Network / VPN** | Interfaces (DHCP/Static), DNS, routing, bonding; **WireGuard** (peer management, key generation, client QR, boot autostart); **Tailscale** |
| **Disk** | Partitions, filesystems, LVM, RAID, swap; usage explorer; **S.M.A.R.T.** self-test runner + logs |
| **Firewall** | UFW rules (lockout guard), Fail2ban jails, Docker firewall (DOCKER-USER chain), unified port map (firewall × container × process) |
| **Packages** | APT search/install/upgrade + one-click install of Docker, Node.js, Claude, Codex, Gemini (live SSE streaming) |
| **Security · Audit** | JWT + TOTP 2FA + one-time recovery codes, bcrypt, login rate-limiting (5 tries → 5 min), audit log (user, IP, path, status, node) |
| **Backup · Update** | Config backup/restore, scheduled backups (retention count), web self-update (SSE + cosign/SHA-256 verification, automatic .bak snapshot, watchdog rollback) |
| **Alerts** | Condition-based rules + channels (Discord, Telegram, Webhook = Slack/Mattermost compatible) + alert history |
| **Cluster** (optional) | Raft multi-node (automatic leader election, mTLS, join tokens), live overview WS, transparent `?node=` proxy, rolling updates |
| **More** | Korean/English (auto-detect) · responsive + PWA · system tuning profiles · desktop app (Tauri, Win/macOS/Linux) |

## Architecture

```
Go Binary (Echo v4)
├── REST API (270+ endpoints) + WebSocket (7) + SSE (8 streaming)
├── Embedded React SPA (go:embed)
├── SQLite (16+ tables — auth, settings, audit log, metrics history, alerts, container events, volume usage, scheduled backups, …)
├── Docker Go SDK (direct socket; only the Docker routes disable when unavailable)
├── Compose Manager (filesystem-based, docker compose CLI)
├── System Metrics (gopsutil, 60s interval, 24-hour history)
└── Cluster (HashiCorp Raft + gRPC + mTLS)
    ├── Consensus-based config sync (JWT secret, admin account)
    ├── Non-leader API request proxy (gRPC 30s / SSE HTTP relay 5m)
    ├── WebSocket relay (remote-node terminal/logs/metrics)
    ├── Cluster overview WS push (/ws/cluster/overview, 5s sampler)
    └── Heartbeat metric collection (CPU, memory, disk, containers)
```

```
internal/
├── api/
│   ├── router.go           # Route registration
│   ├── middleware/          # JWT, audit log, cluster proxy, request logging
│   └── response/            # Standard responses, error codes (150+), output sanitizing
├── feature/                 # 22 independent feature modules
│   ├── auth/                # JWT, TOTP 2FA + recovery codes, password
│   ├── docker/              # Containers, images, volumes, networks
│   ├── compose/             # Docker Compose stacks (healthcheck composer, backup retention)
│   ├── portmap/             # Unified port map (firewall × container × process)
│   ├── firewall/            # UFW, Fail2ban, Docker firewall
│   ├── disk/                # Partitions, filesystems, LVM, RAID, swap
│   ├── network/             # Interfaces, WireGuard (peers/QR), Tailscale
│   ├── packages/            # APT, Docker, Node.js, Claude, Codex, Gemini
│   ├── cluster/             # Cluster management API
│   ├── alert/               # Alert channels (Discord/Telegram), rules, history
│   ├── system/              # Update, backup/restore, scheduled backups, tuning
│   ├── appstore/            # App store (runtime catalog fetch + catalog.json bundle, 90+ apps)
│   ├── services/            # systemd services
│   ├── files/               # File manager
│   ├── terminal/            # Web terminal (PTY, 10,000-line scrollback, 256KB server buffer for reconnect)
│   ├── websocket/           # WebSocket real-time data
│   ├── monitor/             # Dashboard metrics
│   ├── logs/                # Log viewer + custom sources
│   ├── cron/                # Cron jobs
│   ├── process/             # Process management
│   ├── audit/               # Audit log (50k rolling)
│   └── settings/            # Panel settings
├── cluster/                 # Raft, gRPC, TLS, consensus engine
├── db/                      # SQLite migrations, schema
├── config/                  # YAML config loading
├── docker/                  # Docker SDK client
├── monitor/                 # Metric history collection
└── common/
    ├── exec/                # Commander interface (with test Mock)
    └── logging/             # slog structured logging
```

## Tech stack

| Area | Technology |
|------|------|
| Backend | Go 1.25, Echo v4, SQLite (modernc.org/sqlite, CGO-free) |
| Frontend | React 19, TypeScript 6, Vite 8 (rolldown), Tailwind CSS v4, shadcn/ui |
| UI | uplot (charts), xterm.js v6 (terminal), Monaco Editor (code editor) |
| Auth | JWT (golang-jwt/jwt/v5) + TOTP (pquerna/otp) + bcrypt + refresh token rotation |
| Docker | Docker Go SDK v28 |
| Cluster | HashiCorp Raft v1.7, gRPC v1.79, mTLS (auto-issued CA), peers.json quorum-loss recovery |
| Monitoring | gopsutil v4, gorilla/websocket |
| Desktop | Tauri 2 (Rust, Windows/Linux/macOS) |
| i18n | Korean / English (i18next) |
| E2E Test | Playwright |

## Requirements

- Linux (x86_64 or arm64)
- root privileges (needed to run system management commands)
- Docker (optional — the panel itself works without it)

## Installation

> **SFPanel runs as root.** root is required for system management (Docker, firewall, disk, packages, …). After installing, be sure to **enable 2FA (TOTP)** to harden your account.

```bash
curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh | sudo bash
```

The install script handles the following automatically:

- Installs the binary to `/usr/local/bin/sfpanel` after SHA-256 checksum verification
- Creates `/etc/sfpanel/config.yaml` (32-byte random JWT secret, mode 0600)
- Registers a systemd unit (`Restart=always`, `MemoryHigh=1G`, `TasksMax=4096`, `PrivateTmp=true`)
- Configures logrotate (`/etc/logrotate.d/sfpanel`, 7 daily files)
- Auto-snapshots the DB on upgrade (`sfpanel.db.bak-<ts>`, last 3 kept)

After installing:
1. Confirm the service is up: `curl http://localhost:3628/api/v1/health` → `{"success":true,"data":{"status":"ok"}}`
2. Open `http://<server-IP>:3628`
3. Create the admin account in the setup wizard
4. **Settings → Two-Factor Authentication → enable 2FA** (recommended)
5. In production, put a reverse proxy + TLS in front of the panel and restrict port 3628 to LAN/VPN with a firewall
6. To edit the config file: `/etc/sfpanel/config.yaml` (run `systemctl restart sfpanel` after changes)

### Manual installation

When there's no systemd or you'd rather not use the install script:

```bash
# 1. Download the binary from GitHub Releases
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
VERSION=$(curl -fsSL https://api.github.com/repos/svrforum/SFPanel/releases/latest \
  | grep '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/')
curl -fsSL "https://github.com/svrforum/SFPanel/releases/download/v${VERSION}/sfpanel_${VERSION}_linux_${ARCH}.tar.gz" \
  | sudo tar -xzC /usr/local/bin/

# 2. Directories + permissions
sudo install -d -m 700 /etc/sfpanel /var/lib/sfpanel
sudo install -d -m 755 /var/log/sfpanel

# 3. Write a minimal config.yaml (the JWT secret MUST be unique)
sudo tee /etc/sfpanel/config.yaml > /dev/null <<EOF
server:
  host: "0.0.0.0"
  port: 3628
database:
  path: "/var/lib/sfpanel/sfpanel.db"
auth:
  jwt_secret: "$(openssl rand -hex 32)"
  token_expiry: "24h"
EOF
sudo chmod 600 /etc/sfpanel/config.yaml

# 4. Run
sudo /usr/local/bin/sfpanel /etc/sfpanel/config.yaml
```

> In a manual install, handlers like cluster leave/disband and panel update exit the process on purpose, expecting a supervisor to restart it. Write a systemd unit alongside it where possible, or switch to the install script.

## Configuration

`/etc/sfpanel/config.yaml`:

```yaml
server:
  host: "0.0.0.0"
  port: 3628                     # default: 3628 HTTP, 3629 cluster gRPC, 3630 Raft

database:
  path: "/var/lib/sfpanel/sfpanel.db"

auth:
  jwt_secret: "random-secret-key"   # auto-generated by the install script
  token_expiry: "24h"

docker:
  socket: "unix:///var/run/docker.sock"

log:
  level: "info"                  # debug, info, warn, error
  file: "/var/log/sfpanel/sfpanel.log"

cluster:
  enabled: false
  name: ""
  node_id: ""
  node_name: ""
  grpc_port: 3629                # Raft transport is automatically grpc_port + 1 (3630)
  data_dir: "/var/lib/sfpanel/cluster"
  cert_dir: "/etc/sfpanel/cluster"
  advertise_address: ""
  raft_tls: false
```

Environment variable overrides:

| Variable | Description | Default |
|------|------|--------|
| `SFPANEL_PORT` | Server port | 3628 |
| `SFPANEL_JWT_SECRET` | JWT signing secret | config.yaml |
| `SFPANEL_DB_PATH` | SQLite DB path | config.yaml |
| `SFPANEL_LOG_LEVEL` | Log level (`debug`/`info`/`warn`/`error`) | `info` |
| `GOMAXPROCS` | Go concurrent core count | 2 (homeserver default) |
| `GOGC` | Go GC trigger ratio (%) | 50 (memory saving) |

> On an 8-core+ cluster host with heavy cluster fanout, raising `GOMAXPROCS=4` or `GOMAXPROCS=8` helps. Add `Environment="GOMAXPROCS=4"` to the `[Service]` section of the systemd unit, then `systemctl daemon-reload && systemctl restart sfpanel`.

## CLI commands

```bash
sfpanel                           # run with the default config.yaml
sfpanel /path/to/config.yaml      # run with a specified config file
sfpanel version                   # version info
sfpanel update                    # update to the latest version from GitHub
sfpanel reset                     # reset the database (re-run the setup wizard)
sfpanel help                      # help
```

### Cluster CLI

```bash
sfpanel cluster init [--name NAME] [--advertise IP]   # initialize a cluster
sfpanel cluster token [--ttl DURATION]                 # generate a join token
sfpanel cluster join ADDR:PORT TOKEN [--advertise IP]  # join a cluster
sfpanel cluster status                                 # check cluster status
sfpanel cluster remove NODE_ID                         # remove a node
sfpanel cluster leave                                  # leave the cluster
```

All cluster commands support the `--config PATH` option (default: `/etc/sfpanel/config.yaml`).

## Service management

```bash
sudo systemctl status sfpanel    # check status
sudo systemctl restart sfpanel   # restart
sudo systemctl stop sfpanel      # stop
sudo journalctl -u sfpanel -f    # live logs
```

## Upgrading

```bash
# Option 1: web UI
# Settings → Panel Update → Check for updates → Install update

# Option 2: CLI
sudo sfpanel update

# Option 3: re-run the install script (auto-detects an upgrade)
curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh | sudo bash
```

### Integrity verification

Each release runs two verification stages:

1. **Sigstore cosign keyless OIDC** — verifies `checksums.txt.sig` + `checksums.txt.pem` against the GitHub Actions OIDC identity (v0.13.0+ releases)
2. **SHA-256 checksum** — matches the binary archive hash against the verified `checksums.txt`

A downgrade attempt to an old release without cosign material is rejected (`release.IsForwardUpdate`), falling back to SHA-256 only.

### Automatic DB snapshot

Right before an upgrade (after the service stops for the install script, just before the binary swap for the CLI), a snapshot is auto-created as `sfpanel.db.bak-<YYYYmmdd-HHMMSS>`. The last 3 are kept and older ones removed. To roll back when a migration regression keeps the panel from booting:

```bash
sudo systemctl stop sfpanel
sudo cp /var/lib/sfpanel/sfpanel.db.bak-<timestamp> /var/lib/sfpanel/sfpanel.db
sudo systemctl start sfpanel
```

### Rolling upgrade in a cluster

Restarting multiple nodes at once can cause a **15–20 second leader re-election delay** due to the mTLS handshake + Raft pre-vote flow. For zero-downtime upgrades, do one node at a time with at least 10 seconds between them. Or use the web UI's **Cluster → Update → rolling mode** (SSE progress streaming), which processes nodes one at a time automatically.

## Backup / restore

Backup/restore via the web UI at Settings → Config Backup.

What the backup includes:
- `sfpanel.db` — admin account (+ TOTP), settings, Compose project metadata, custom log sources, audit log, metrics history, alert rules/channels/history
- `config.yaml` — server port, JWT secret, DB path, Docker socket, cluster config
- `compose/*` — Docker Compose project files (docker-compose.yml, .env)

> Docker data (volumes, images, containers) is not included in the backup.

## Desktop app (Tauri)

Separate from the server binary, **Windows / macOS / Linux desktop apps** ship in the same release. The app is a simple WebView wrapper — it doesn't run a server of its own; you enter the address of an already-running SFPanel instance (`http://<server-IP>:3628`) and it opens that panel in a desktop window. There's no feature difference from a browser tab; it's handy when you want a separate window, or OS-native notifications/shortcuts.

Assets on the [Releases page](https://github.com/svrforum/SFPanel/releases/latest):

| OS | Asset | Note |
|----|------|------|
| Windows | `SFPanel_<ver>_x64_en-US.msi` | Standard MSI install |
| Windows | `SFPanel_<ver>_x64-setup.exe` | NSIS install wizard |
| Windows | `SFPanel_portable_windows_amd64.exe` | Portable (no install) |
| macOS | `SFPanel_<ver>_aarch64.dmg` | Apple Silicon (M1/M2/M3) |
| Linux | `SFPanel_<ver>_amd64.deb` | Debian/Ubuntu |
| Linux | `SFPanel-<ver>-1.x86_64.rpm` | RHEL/Fedora |
| Linux | `SFPanel_<ver>_amd64.AppImage` | Distro-agnostic (chmod +x then run) |

The desktop app is released on the **same version line as the server** (v0.13.5+; earlier desktop builds were a separate `0.6.x` line). When a new version lands on the release page, the app detects it automatically and shows an update dialog (ed25519 signature verification, `tauri-plugin-updater`). Update in place — no manual reinstall.

## Cluster

SFPanel supports a multi-node cluster built on the HashiCorp Raft consensus algorithm.

### Features

- **Automatic leader election** — a new leader is elected automatically if the leader fails
- **mTLS communication** — node-to-node gRPC uses auto-issued CA certificates
- **Token-based join** — nodes authenticate with time-limited HMAC-signed tokens
- **Zero-restart lifecycle** — creating/joining a cluster activates immediately, no service restart
- **Config sync** — JWT secret and admin account sync automatically across all nodes
- **API proxy** — API requests on non-leader nodes are relayed to the leader automatically
- **WebSocket relay** — connect to remote-node terminals and logs over a relay
- **Metric sharing** — each node's CPU, memory, disk and container metrics aggregate into the cluster overview
- **Cluster update** — update SFPanel across the whole cluster in rolling/simultaneous mode (SSE progress streaming)

### Setting up a cluster

```bash
# 1. Initialize the cluster on the first node
sudo sfpanel cluster init --name my-cluster

# 2. Generate a join token
sudo sfpanel cluster token

# 3. Join the cluster from another node
sudo sfpanel cluster join 10.0.0.1:3629 <token>
```

Creating/joining a cluster activates immediately **without a service restart**, from both the web UI and the CLI (zero-restart, `JoinEngine` PreFlight → Execute pipeline). Leaving/disbanding does require a service restart and works safely under the `Restart=always` systemd unit installed by `scripts/install.sh` (the leave/disband handlers deliberately `os.Exit` to trigger a supervisor restart).

### TLS certificate lifetimes / rotation

| Item | Lifetime | Rotation method |
|------|------|-----------|
| Cluster CA | 10 years | Coordinated restart (all nodes trust the new CA simultaneously) |
| Node cert | 5 years | `sudo sfpanel cluster reissue-cert` (no restart, takes effect ≤1 min) |

### Quorum-loss recovery (peers.json)

When a hardware failure or permanent node loss drops surviving voters below quorum so a normal membership change is impossible:

```bash
# Write /var/lib/sfpanel/cluster/peers.json on a surviving node:
sudo tee /var/lib/sfpanel/cluster/peers.json > /dev/null <<EOF
[
  {"id":"<node-uuid-1>","address":"<ip-1>:3630","non_voter":false},
  {"id":"<node-uuid-2>","address":"<ip-2>:3630","non_voter":false}
]
EOF
sudo systemctl restart sfpanel
```

On boot, Raft detects the file, rewrites the local configuration via `RecoverCluster()`, and renames it to `peers.info` so it isn't re-applied on the next boot. For normal node removal, use `sudo sfpanel cluster remove <node-id>`.

peers.json is also used — beyond quorum loss — for **changing the Raft transport port on a live cluster** (moving `grpc_port` makes the Raft port follow as `grpc_port + 1`, so the peer addresses baked into the existing membership must be rewritten to the new port). Both can break membership if done wrong, so follow the "Port migration on a live cluster" procedure in [docs/specs/cluster-partition-runbook.md](docs/specs/cluster-partition-runbook.md).

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh | sudo bash -s -- uninstall
```

This removes only the binary and service. Config (`/etc/sfpanel`) and data (`/var/lib/sfpanel`) are preserved; to remove everything, also run:

```bash
sudo rm -rf /etc/sfpanel /var/lib/sfpanel /var/log/sfpanel
```

## Development

### Build

```bash
# Full build (frontend + backend → single binary)
make build

# Or manually:
cd web && npm install && npm run build
cd .. && go build -o sfpanel ./cmd/sfpanel
```

### Dev mode

```bash
# Terminal 1: frontend (hot reload, API proxy → :3628)
cd web && npm run dev

# Terminal 2: backend (must be root)
sudo go run ./cmd/sfpanel
```

> Manual-run caveat: when you launch the binary directly without systemd, pressing **cluster leave/disband** or **panel update** in the web UI makes the backend exit on purpose, and with no supervisor to restart it the panel stays down. Those buttons are only safe under the `Restart=always` systemd unit (installed by `scripts/install.sh`). Creating/joining a cluster activates without a restart, so it's safe even in a manual setup.

### Test

```bash
make test            # Go unit tests
make test-coverage   # coverage report
make lint            # Go + frontend lint
make ci              # full CI pipeline (lint + test + build)

# E2E tests (Playwright)
cd e2e && npm run test          # headless
cd e2e && npm run test:headed   # browser UI
```

## API

All REST responses use a uniform JSON shape:

```json
{"success": true, "data": {...}}
{"success": false, "error": {"code": "ERROR_CODE", "message": "..."}}
```

- Auth: `Authorization: Bearer <JWT>` header
- WebSocket auth: query parameter `?token=<JWT>`
- Cluster remote-node calls: adding `?node=<nodeID>` to any protected route makes `ClusterProxyMiddleware` transparently forward to the target node (gRPC 30s; SSE/WS relay directly over HTTP/WS)
- 8 SSE streaming endpoints (system update, Docker image pull, Compose up/update, package/VPN install, cluster update)

## Documentation

| Document | Contents |
|------|------|
| [docs/specs/tech-features.md](docs/specs/tech-features.md) | Full feature detail + tech stack |
| [docs/specs/api-spec.md](docs/specs/api-spec.md) | Complete REST/SSE endpoints + request/response schemas |
| [docs/specs/websocket-spec.md](docs/specs/websocket-spec.md) | 7 WebSocket + 8 SSE message schemas + cluster relay |
| [docs/specs/db-schema.md](docs/specs/db-schema.md) | SQLite 16+ tables + retention policy + migrations |
| [docs/specs/frontend-spec.md](docs/specs/frontend-spec.md) | Pages/components/routing/state/build |
| [docs/specs/cluster-partition-runbook.md](docs/specs/cluster-partition-runbook.md) | Cluster operator runbook: partition detection/recovery, forced disband, port migration procedure |
| [CHANGELOG.md](CHANGELOG.md) | Release notes |
| [CLAUDE.md](CLAUDE.md) | Contributor guide (code conventions, test scope, cluster awareness) |

## Security notes

SFPanel **runs as root** and is a powerful tool that can manage the entire server. Be sure to apply the security measures below.

### What operators must apply themselves

- **2FA strongly recommended** — enable it with a TOTP app (Google Authenticator, etc.) under Settings → Two-Factor Authentication. If the panel is compromised, the whole server is at risk.
- **Strong password** — at least 12 characters during initial setup
- **Reverse proxy + TLS** — in production, terminate HTTPS with Nginx/Caddy/Cloudflare Tunnel etc. The bundled port 3628 is plain HTTP.
- **Restrict access** — allow 3628/3629/3630 only to trusted IPs/CIDRs via a firewall (UFW)
- **JWT secret on manual install** — set a unique value with `openssl rand -hex 32` (the install script generates it automatically)

### What the panel/script apply automatically

- **Login protection** — 5 failures blocks the IP for 5 minutes (built-in rate limiting)
- **JWT secret** — 32-byte random, auto-generated + config.yaml 0600 permissions
- **Cluster mTLS** — node-to-node gRPC/Raft traffic auto-encrypted (CA auto-issued)
- **Join token** — time-limited HMAC-signed, single use
- **systemd hardening** — `MemoryHigh=1G`, `TasksMax=4096`, `PrivateTmp=true`, `RestrictSUIDSGID=true` (for the unit installed by `scripts/install.sh`)
- **Sensitive directory permissions** — `/etc/sfpanel/` 0700, `config.yaml` 0600, `/etc/sfpanel/cluster/*.key` 0600, `/var/lib/sfpanel/` 0700

## Sponsor / links

If SFPanel is useful to you, support its development with a cup of coffee. The same sponsor / GitHub links are also at the bottom of the panel sidebar.

[![Buy Me A Coffee](https://img.shields.io/badge/Buy%20Me%20A%20Coffee-FFDD00?style=for-the-badge&logo=buy-me-a-coffee&logoColor=black)](https://buymeacoffee.com/svrforum)
[![GitHub](https://img.shields.io/badge/GitHub-svrforum%2FSFPanel-181717?style=for-the-badge&logo=github)](https://github.com/svrforum/SFPanel)

## License

MIT
