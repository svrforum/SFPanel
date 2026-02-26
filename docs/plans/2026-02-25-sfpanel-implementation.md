# SFPanel MVP Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build a lightweight server management panel with system monitoring, Docker management, and website management as a single Go binary with embedded React SPA.

**Architecture:** All-in-one Go binary using Echo v4 for HTTP/WebSocket, SQLite for storage, Docker Go SDK for container management, and React SPA embedded via `go:embed`. Single admin auth with JWT + TOTP 2FA.

**Tech Stack:** Go 1.22+, Echo v4, SQLite (modernc.org/sqlite), Docker Go SDK, React 18, TypeScript, Vite, Tailwind CSS, shadcn/ui, Recharts, xterm.js, Monaco Editor

---

## Task 1: Development Environment Setup

**Files:**
- Create: `go.mod`
- Verify: Go, Node.js installed

**Step 1: Install Go**

```bash
wget https://go.dev/dl/go1.23.6.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.23.6.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc
source ~/.bashrc
rm go1.23.6.linux-amd64.tar.gz
```

Expected: `go version` → `go version go1.23.6 linux/amd64`

**Step 2: Initialize Go module**

```bash
cd /opt/stacks/SFPanel
go mod init github.com/sfpanel/sfpanel
```

**Step 3: Commit**

```bash
git add go.mod .gitignore CLAUDE.md Makefile configs/ docs/
git commit -m "chore: initial project setup with design docs and config"
```

---

## Task 2: Go Backend Skeleton — Config + Echo Router + Main

**Files:**
- Create: `cmd/sfpanel/main.go`
- Create: `internal/config/config.go`
- Create: `internal/api/router.go`
- Create: `internal/api/response.go`

**Step 1: Create config loader**

Create `internal/config/config.go`:

```go
package config

import (
	"os"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Auth     AuthConfig     `yaml:"auth"`
	Nginx    NginxConfig    `yaml:"nginx"`
	Docker   DockerConfig   `yaml:"docker"`
	Log      LogConfig      `yaml:"log"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type AuthConfig struct {
	JWTSecret   string `yaml:"jwt_secret"`
	TokenExpiry string `yaml:"token_expiry"`
}

type NginxConfig struct {
	ConfigDir  string `yaml:"config_dir"`
	EnabledDir string `yaml:"enabled_dir"`
	WebRoot    string `yaml:"web_root"`
}

type DockerConfig struct {
	Socket string `yaml:"socket"`
}

type LogConfig struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Server:   ServerConfig{Host: "0.0.0.0", Port: 8443},
		Database: DatabaseConfig{Path: "./sfpanel.db"},
		Auth:     AuthConfig{TokenExpiry: "24h"},
		Docker:   DockerConfig{Socket: "unix:///var/run/docker.sock"},
		Log:      LogConfig{Level: "info"},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, nil // use defaults if no config file
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
```

**Step 2: Create API response helpers**

Create `internal/api/response.go`:

```go
package api

import (
	"net/http"
	"github.com/labstack/echo/v4"
)

type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *ErrorBody  `json:"error,omitempty"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func OK(c echo.Context, data interface{}) error {
	return c.JSON(http.StatusOK, Response{Success: true, Data: data})
}

func Fail(c echo.Context, status int, code, message string) error {
	return c.JSON(status, Response{
		Success: false,
		Error:   &ErrorBody{Code: code, Message: message},
	})
}
```

**Step 3: Create Echo router**

Create `internal/api/router.go`:

```go
package api

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

func NewRouter() *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"http://localhost:5173"},
		AllowMethods: []string{"GET", "POST", "PUT", "DELETE"},
	}))

	// Health check
	v1 := e.Group("/api/v1")
	v1.GET("/health", func(c echo.Context) error {
		return OK(c, map[string]string{"status": "ok"})
	})

	return e
}
```

**Step 4: Create main.go entrypoint**

Create `cmd/sfpanel/main.go`:

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/sfpanel/sfpanel/internal/api"
	"github.com/sfpanel/sfpanel/internal/config"
)

func main() {
	cfgPath := "config.yaml"
	if len(os.Args) > 1 {
		cfgPath = os.Args[1]
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	e := api.NewRouter()

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("SFPanel starting on %s", addr)
	if err := e.Start(addr); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
```

**Step 5: Install Go dependencies and verify**

```bash
go mod tidy
go build ./cmd/sfpanel
```

Expected: Binary `sfpanel` created, no errors.

**Step 6: Commit**

```bash
git add -A
git commit -m "feat: add Go backend skeleton with config, Echo router, and main entrypoint"
```

---

## Task 3: SQLite Database + Migrations

**Files:**
- Create: `internal/db/sqlite.go`
- Create: `internal/db/migrations.go`

**Step 1: Create SQLite connection and migration runner**

Create `internal/db/sqlite.go`:

```go
package db

import (
	"database/sql"
	_ "modernc.org/sqlite"
)

func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if err := RunMigrations(db); err != nil {
		return nil, err
	}
	return db, nil
}
```

Create `internal/db/migrations.go`:

```go
package db

import "database/sql"

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS admin (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		username   TEXT NOT NULL UNIQUE,
		password   TEXT NOT NULL,
		totp_secret TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS sites (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		domain      TEXT NOT NULL UNIQUE,
		doc_root    TEXT NOT NULL,
		php_enabled BOOLEAN DEFAULT 0,
		ssl_enabled BOOLEAN DEFAULT 0,
		ssl_expiry  DATETIME,
		status      TEXT DEFAULT 'active',
		created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS compose_projects (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		name       TEXT NOT NULL UNIQUE,
		yaml_path  TEXT NOT NULL,
		status     TEXT DEFAULT 'stopped',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`,
	`CREATE TABLE IF NOT EXISTS sessions (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		token_hash TEXT NOT NULL UNIQUE,
		expires_at DATETIME NOT NULL
	)`,
}

func RunMigrations(db *sql.DB) error {
	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			return err
		}
	}
	return nil
}
```

**Step 2: Wire DB into main.go**

Update `cmd/sfpanel/main.go` to open DB before starting server.

**Step 3: Verify build**

```bash
go mod tidy && go build ./cmd/sfpanel
```

**Step 4: Commit**

```bash
git add -A
git commit -m "feat: add SQLite database with schema migrations"
```

---

## Task 4: Authentication — JWT + TOTP + Login Handler

**Files:**
- Create: `internal/auth/jwt.go`
- Create: `internal/auth/totp.go`
- Create: `internal/auth/hash.go`
- Create: `internal/api/handlers/auth.go`
- Create: `internal/api/middleware/auth.go`

**Step 1: Create password hashing helpers**

`internal/auth/hash.go` — bcrypt hash/compare.

**Step 2: Create JWT token issuer/validator**

`internal/auth/jwt.go` — Sign and parse JWT tokens with `golang-jwt/jwt/v5`.

**Step 3: Create TOTP helpers**

`internal/auth/totp.go` — Generate secret, validate code using `pquerna/otp`.

**Step 4: Create auth handlers**

`internal/api/handlers/auth.go`:
- `POST /api/v1/auth/login` — validate credentials, return JWT
- `POST /api/v1/auth/2fa/setup` — generate TOTP secret + QR URL
- `POST /api/v1/auth/2fa/verify` — validate TOTP code

**Step 5: Create auth middleware**

`internal/api/middleware/auth.go` — Extract JWT from Authorization header, validate, inject user into context.

**Step 6: Create initial admin setup**

On first startup, if no admin exists, auto-create one with default credentials and log them. User must change password on first login.

**Step 7: Wire auth routes into router**

Update `internal/api/router.go` to register auth handlers and protect `/api/v1/*` routes with auth middleware (except `/auth/login`).

**Step 8: Verify build**

```bash
go mod tidy && go build ./cmd/sfpanel
```

**Step 9: Commit**

```bash
git add -A
git commit -m "feat: add JWT + TOTP authentication with login handler and middleware"
```

---

## Task 5: System Monitor — Metrics Collector + WebSocket

**Files:**
- Create: `internal/monitor/collector.go`
- Create: `internal/api/handlers/dashboard.go`
- Create: `internal/api/handlers/ws.go`

**Step 1: Create system metrics collector**

`internal/monitor/collector.go` — Use `shirou/gopsutil/v4` to collect:
- CPU usage percentage
- Memory usage (total, used, percent)
- Disk usage (total, used, percent)
- Network I/O (bytes sent/received per second)
- Host info (hostname, OS, kernel, uptime)

Expose `GetMetrics() Metrics` and `GetHostInfo() HostInfo`.

**Step 2: Create dashboard handler**

`internal/api/handlers/dashboard.go`:
- `GET /api/v1/system/info` — returns host info + current metrics snapshot

**Step 3: Create WebSocket metrics handler**

`internal/api/handlers/ws.go`:
- `WS /ws/metrics` — push metrics every 2 seconds via WebSocket using `gorilla/websocket`.

**Step 4: Wire into router**

Register dashboard and ws routes.

**Step 5: Verify build and test WebSocket**

```bash
go mod tidy && go build ./cmd/sfpanel
```

**Step 6: Commit**

```bash
git add -A
git commit -m "feat: add system monitoring with real-time WebSocket metrics"
```

---

## Task 6: Docker Management — SDK Client + Container Handlers

**Files:**
- Create: `internal/docker/client.go`
- Create: `internal/api/handlers/docker.go`

**Step 1: Create Docker SDK wrapper**

`internal/docker/client.go` — Initialize Docker client from socket path. Methods:
- `ListContainers()` — list all containers (running + stopped)
- `StartContainer(id)`, `StopContainer(id)`, `RestartContainer(id)`, `RemoveContainer(id)`
- `ListImages()`, `PullImage(ref)`, `RemoveImage(id)`
- `ListVolumes()`, `CreateVolume(name)`, `RemoveVolume(name)`
- `ListNetworks()`, `CreateNetwork(name, driver)`, `RemoveNetwork(id)`

**Step 2: Create Docker REST handlers**

`internal/api/handlers/docker.go` — Map each API endpoint to Docker client calls.

**Step 3: Create container logs WebSocket**

`WS /ws/docker/containers/{id}/logs` — Stream container logs in real-time.

**Step 4: Create container exec WebSocket**

`WS /ws/docker/containers/{id}/exec` — Interactive shell via xterm.js. Attach stdin/stdout via WebSocket.

**Step 5: Wire Docker routes into router**

**Step 6: Verify build**

```bash
go mod tidy && go build ./cmd/sfpanel
```

**Step 7: Commit**

```bash
git add -A
git commit -m "feat: add Docker management with container, image, volume, network handlers"
```

---

## Task 7: Docker Compose Management

**Files:**
- Create: `internal/docker/compose.go`
- Create: `internal/api/handlers/compose.go`

**Step 1: Create Compose manager**

`internal/docker/compose.go` — Manage compose projects:
- Store YAML files in `/var/lib/sfpanel/compose/`
- `ListProjects()` — read from DB
- `CreateProject(name, yaml)` — save YAML, store in DB
- `DeleteProject(name)` — remove YAML and DB entry
- `Up(name)` / `Down(name)` — exec `docker compose -f <path> up -d` / `down`

Uses `os/exec` to call `docker compose` CLI (more reliable than SDK for compose).

**Step 2: Create Compose REST handlers**

`internal/api/handlers/compose.go`:
- `GET /api/v1/docker/compose` — list projects
- `POST /api/v1/docker/compose` — create project
- `DELETE /api/v1/docker/compose/{project}` — delete project
- `POST /api/v1/docker/compose/{project}/up` — deploy
- `POST /api/v1/docker/compose/{project}/down` — stop

**Step 3: Wire routes, verify build**

**Step 4: Commit**

```bash
git add -A
git commit -m "feat: add Docker Compose project management"
```

---

## Task 8: Website Management — Nginx + SSL

**Files:**
- Create: `internal/nginx/manager.go`
- Create: `internal/nginx/templates/vhost.conf.tmpl`
- Create: `internal/ssl/certbot.go`
- Create: `internal/api/handlers/sites.go`

**Step 1: Create Nginx vhost template**

`internal/nginx/templates/vhost.conf.tmpl` — Go template for Nginx server block:
- HTTP → HTTPS redirect (when SSL enabled)
- PHP-FPM upstream (when PHP enabled)
- Static file serving

**Step 2: Create Nginx manager**

`internal/nginx/manager.go`:
- `CreateSite(domain, docRoot, phpEnabled)` — render template, write to sites-available, symlink to sites-enabled, create doc root dir
- `DeleteSite(domain)` — remove config, symlink
- `ReloadNginx()` — exec `nginx -t && systemctl reload nginx`
- `GetConfig(domain)` / `UpdateConfig(domain, content)` — read/write raw config

**Step 3: Create SSL manager**

`internal/ssl/certbot.go`:
- `IssueCert(domain)` — exec `certbot certonly --nginx -d <domain> --non-interactive --agree-tos`
- `GetCertExpiry(domain)` — parse cert file to get expiry date

**Step 4: Create site REST handlers**

`internal/api/handlers/sites.go`:
- `GET /api/v1/sites` — list from DB
- `POST /api/v1/sites` — create site (Nginx config + DB record)
- `DELETE /api/v1/sites/{id}` — delete site
- `POST /api/v1/sites/{id}/ssl` — issue SSL
- `GET/PUT /api/v1/sites/{id}/config` — raw Nginx config

**Step 5: Wire routes, verify build**

**Step 6: Commit**

```bash
git add -A
git commit -m "feat: add website management with Nginx vhost and SSL support"
```

---

## Task 9: React Frontend — Project Setup + Layout

**Files:**
- Create: `web/` (entire React project via Vite)

**Step 1: Initialize React + TypeScript + Vite project**

```bash
cd /opt/stacks/SFPanel
npm create vite@latest web -- --template react-ts
cd web && npm install
```

**Step 2: Install dependencies**

```bash
npm install tailwindcss @tailwindcss/vite
npm install react-router-dom
npm install recharts
npm install @xterm/xterm @xterm/addon-fit @xterm/addon-web-links
npm install @monaco-editor/react
npm install lucide-react
npm install clsx tailwind-merge class-variance-authority
```

**Step 3: Configure Tailwind CSS**

Update `web/src/index.css` with Tailwind directives. Update `vite.config.ts` with Tailwind plugin and API proxy to `:8443`.

**Step 4: Setup shadcn/ui**

```bash
npx shadcn@latest init
npx shadcn@latest add button card input label table badge dialog dropdown-menu tabs toast
```

**Step 5: Create app layout**

- `web/src/App.tsx` — React Router with sidebar layout
- `web/src/components/Layout.tsx` — Sidebar navigation (Dashboard, Docker, Sites, Settings) + top bar
- `web/src/lib/api.ts` — Fetch wrapper with JWT auth header
- `web/src/hooks/useWebSocket.ts` — WebSocket hook with auto-reconnect
- `web/src/types/api.ts` — TypeScript types matching Go API responses

**Step 6: Create Login page**

`web/src/pages/Login.tsx` — Username + password form, 2FA code input, JWT token storage in localStorage.

**Step 7: Verify dev server**

```bash
cd web && npm run dev
```

Expected: React app loads on :5173 with login page.

**Step 8: Commit**

```bash
git add -A
git commit -m "feat: add React frontend with routing, layout, auth, and shadcn/ui"
```

---

## Task 10: Frontend — Dashboard Page

**Files:**
- Create: `web/src/pages/Dashboard.tsx`
- Create: `web/src/components/MetricsCard.tsx`
- Create: `web/src/components/MetricsChart.tsx`

**Step 1: Create metrics summary cards**

`MetricsCard.tsx` — Reusable card showing label, value, percentage bar (CPU, RAM, Disk).

**Step 2: Create real-time charts**

`MetricsChart.tsx` — Recharts LineChart with rolling 60-second window. Lines for CPU, RAM, Network.

**Step 3: Create Dashboard page**

`Dashboard.tsx` — Connect to `ws://host/ws/metrics`, display:
- Host info section (hostname, OS, kernel, uptime)
- 4 summary cards (CPU, RAM, Disk, Network)
- Real-time line charts

**Step 4: Verify in browser**

**Step 5: Commit**

```bash
git add -A
git commit -m "feat: add dashboard page with real-time system metrics"
```

---

## Task 11: Frontend — Docker Management Pages

**Files:**
- Create: `web/src/pages/Docker.tsx`
- Create: `web/src/pages/DockerContainers.tsx`
- Create: `web/src/pages/DockerImages.tsx`
- Create: `web/src/pages/DockerVolumes.tsx`
- Create: `web/src/pages/DockerNetworks.tsx`
- Create: `web/src/pages/DockerCompose.tsx`
- Create: `web/src/components/ContainerLogs.tsx`
- Create: `web/src/components/ContainerShell.tsx`
- Create: `web/src/components/ComposeEditor.tsx`

**Step 1: Create Docker containers page**

`DockerContainers.tsx`:
- Table: name, image, status, ports, created, actions (start/stop/restart/delete)
- Click row → detail panel with logs (WebSocket stream) and shell (xterm.js)

**Step 2: Create container logs component**

`ContainerLogs.tsx` — xterm.js terminal displaying real-time logs via WebSocket.

**Step 3: Create container shell component**

`ContainerShell.tsx` — xterm.js interactive terminal with bidirectional WebSocket.

**Step 4: Create Docker images page**

`DockerImages.tsx` — Table with pull dialog and delete action.

**Step 5: Create Docker volumes page**

`DockerVolumes.tsx` — Table with create dialog and delete action.

**Step 6: Create Docker networks page**

`DockerNetworks.tsx` — Table with create dialog and delete action.

**Step 7: Create Docker Compose page**

`DockerCompose.tsx`:
- Project list with status, up/down actions
- Create dialog with Monaco YAML editor
- Project detail showing containers

**Step 8: Create Docker parent page with tabs**

`Docker.tsx` — Tab navigation: Containers | Images | Volumes | Networks | Compose

**Step 9: Verify all Docker pages in browser**

**Step 10: Commit**

```bash
git add -A
git commit -m "feat: add Docker management pages with container logs, shell, and compose editor"
```

---

## Task 12: Frontend — Website Management Page

**Files:**
- Create: `web/src/pages/Sites.tsx`
- Create: `web/src/components/SiteConfigEditor.tsx`

**Step 1: Create Sites page**

`Sites.tsx`:
- Table: domain, document root, PHP status, SSL status, SSL expiry, actions
- Create site dialog (domain input, PHP toggle)
- Delete confirmation
- Issue SSL button per site

**Step 2: Create Nginx config editor**

`SiteConfigEditor.tsx` — Monaco Editor showing raw Nginx config with save button.

**Step 3: Verify in browser**

**Step 4: Commit**

```bash
git add -A
git commit -m "feat: add website management page with SSL and config editor"
```

---

## Task 13: Build Pipeline — go:embed + Final Binary

**Files:**
- Modify: `cmd/sfpanel/main.go` (add embed directives)
- Create: `web/embed.go` (embed FS declaration)
- Update: `internal/api/router.go` (serve embedded SPA)
- Update: `Makefile`

**Step 1: Create embed file**

Create `web/embed.go` in appropriate package to declare `//go:embed dist/*`.

**Step 2: Update router to serve SPA**

Add static file handler that serves embedded files and falls back to `index.html` for SPA routing.

**Step 3: Update Makefile**

Ensure `make build` runs frontend build first, then Go build.

**Step 4: Full build test**

```bash
make clean && make build
./sfpanel
```

Expected: Single binary serves both API and React SPA on :8443.

**Step 5: Commit**

```bash
git add -A
git commit -m "feat: embed React SPA into Go binary with go:embed"
```

---

## Task 14: Settings Page + First Run Setup

**Files:**
- Create: `web/src/pages/Settings.tsx`
- Modify: `internal/api/handlers/auth.go` (add password change, setup wizard)

**Step 1: Create Settings page**

- Change password form
- 2FA setup/disable toggle
- Panel port configuration
- System info display

**Step 2: Create first-run setup flow**

On first access (no admin in DB), redirect to setup wizard:
- Set admin username + password
- Optional 2FA setup
- Basic config (port, etc.)

**Step 3: Verify**

**Step 4: Commit**

```bash
git add -A
git commit -m "feat: add settings page and first-run setup wizard"
```

---

## Execution Order Summary

| Task | Description | Depends On |
|------|-------------|------------|
| 1 | Dev environment setup | — |
| 2 | Go backend skeleton | 1 |
| 3 | SQLite + migrations | 2 |
| 4 | Auth (JWT + TOTP) | 3 |
| 5 | System monitor | 2 |
| 6 | Docker management | 2 |
| 7 | Docker Compose | 6 |
| 8 | Website management (Nginx + SSL) | 3 |
| 9 | React frontend setup + layout | 1 |
| 10 | Dashboard page | 5, 9 |
| 11 | Docker pages | 6, 7, 9 |
| 12 | Sites page | 8, 9 |
| 13 | go:embed build pipeline | 9 |
| 14 | Settings + first run | 4, 9 |

**Parallel tracks possible:**
- Track A (Backend): Tasks 1→2→3→4, 5, 6→7, 8 (Tasks 5,6,8 can run in parallel after Task 2)
- Track B (Frontend): Tasks 9→10, 11, 12, 14 (after Task 9, pages can run in parallel)
- Task 13 merges both tracks
