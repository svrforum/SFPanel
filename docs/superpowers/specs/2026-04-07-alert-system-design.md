# Alert System Design (Discord + Telegram)

## Overview

Server monitoring alert system that detects resource thresholds, container/service state changes, security events, and sends notifications via Discord webhooks and Telegram bot API.

## Alert Types

1. **System Resources**: CPU > X%, Memory > X%, Disk > X% (configurable thresholds, evaluation over duration)
2. **Docker Containers**: Container state change (running → exited/dead)
3. **System Services**: systemd service down detection
4. **Security**: Login failure count threshold
5. **Packages**: Available updates (daily check)

## Architecture

### Backend

**AlertManager** (`internal/feature/alert/manager.go`):
- Runs periodic checks (30s interval)
- Evaluates rules against current system state
- Sends alerts through configured channels
- Cooldown per rule to prevent spam (default 5 min)
- Stores alert history in DB

**Channels** (`internal/feature/alert/channels/`):
- `discord.go`: POST to webhook URL with embed message
- `telegram.go`: POST to Bot API sendMessage endpoint

**Handler** (`internal/feature/alert/handler.go`):
- CRUD for channels and rules
- Test send endpoint
- Alert history query

### Database Tables

```sql
CREATE TABLE alert_channels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT NOT NULL,       -- 'discord' or 'telegram'
    name TEXT NOT NULL,
    config TEXT NOT NULL,     -- JSON: {webhook_url} or {bot_token, chat_id}
    enabled INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE alert_rules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    type TEXT NOT NULL,       -- 'cpu', 'memory', 'disk', 'container', 'service', 'login', 'package'
    condition TEXT NOT NULL,  -- JSON: {threshold: 90, duration: "5m", ...}
    channel_ids TEXT NOT NULL,-- JSON array: [1, 2]
    severity TEXT DEFAULT 'warning', -- 'info', 'warning', 'critical'
    cooldown INTEGER DEFAULT 300,    -- seconds
    enabled INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE alert_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    rule_id INTEGER,
    rule_name TEXT,
    type TEXT,
    severity TEXT,
    message TEXT,
    sent_channels TEXT,      -- JSON: [{channel_id, success, error}]
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### API Endpoints

```
GET    /api/v1/alerts/channels          — List channels
POST   /api/v1/alerts/channels          — Create channel
PUT    /api/v1/alerts/channels/:id      — Update channel
DELETE /api/v1/alerts/channels/:id      — Delete channel
POST   /api/v1/alerts/channels/:id/test — Test send

GET    /api/v1/alerts/rules             — List rules
POST   /api/v1/alerts/rules             — Create rule
PUT    /api/v1/alerts/rules/:id         — Update rule
DELETE /api/v1/alerts/rules/:id         — Delete rule

GET    /api/v1/alerts/history           — List history (paginated)
DELETE /api/v1/alerts/history           — Clear history
```

### Frontend

Settings page: new "알림" tab with three sections:
1. **채널 관리**: Add/edit/delete Discord webhooks and Telegram bots, test button
2. **규칙 관리**: Add/edit rules with type-specific forms (threshold, duration, severity)
3. **알림 히스토리**: Paginated table of past alerts with status

## Files to Create/Modify

- Create: `internal/feature/alert/handler.go` — HTTP handlers
- Create: `internal/feature/alert/manager.go` — Alert evaluation engine
- Create: `internal/feature/alert/channels/discord.go` — Discord webhook sender
- Create: `internal/feature/alert/channels/telegram.go` — Telegram bot sender
- Modify: `internal/db/migrations.go` — Add 3 new tables
- Modify: `internal/api/router.go` — Register alert routes
- Modify: `cmd/sfpanel/main.go` — Start AlertManager goroutine
- Create: `web/src/pages/settings/AlertSettings.tsx` — Alert settings UI
- Modify: `web/src/pages/Settings.tsx` — Add alert tab
