# Container Log Viewer Enhancement Design

## Overview

Enhance the existing container log viewer with configurable options (tail lines, stream filter, timestamps, time range) and log level highlighting.

## Current State

- Backend: `docker/client.go` ContainerLogs() — hardcoded Tail="100", stdout+stderr, no timestamps
- Backend: `websocket/handler.go` ContainerLogsWS() — WebSocket streaming, no options
- Frontend: `components/ContainerLogs.tsx` — xterm.js terminal with search, download, auto-scroll

## Changes

### Backend

**1. Add LogOptions struct to docker/client.go**

```go
type LogOptions struct {
    Tail       string // "100", "500", "1000", "all"
    Timestamps bool
    Stream     string // "all", "stdout", "stderr"
    Since      string // "", "1h", "6h", "24h"
}
```

Update `ContainerLogs(ctx, id)` to `ContainerLogs(ctx, id, opts LogOptions)`:
- Map `opts.Tail` to Docker API `Tail` option
- Map `opts.Timestamps` to Docker API `Timestamps` option
- Map `opts.Stream` to `ShowStdout`/`ShowStderr` booleans
- Map `opts.Since` to Docker API `Since` option (convert "1h" to RFC3339 time)

**2. Parse query parameters in ContainerLogsWS**

In `websocket/handler.go` ContainerLogsWS(), parse WebSocket URL query params:
- `?tail=500&timestamps=true&stream=stderr&since=1h`
- Validate and pass to `ContainerLogs(ctx, id, opts)`

**3. Same for ComposeLogsWS**

Apply same query parameter parsing to compose log streaming.

### Frontend

**4. Extend ContainerLogs.tsx toolbar**

Add controls before the existing buttons:

- **Tail lines** dropdown: 100 / 500 / 1000 / All (default: 100)
- **Stream** dropdown: All / stdout / stderr (default: All)
- **Timestamps** toggle button (default: off)
- **Since** dropdown: All / 1h / 6h / 24h (default: All)

Use shadcn/ui Select components, consistent with existing SFPanel design.

When any option changes: close current WebSocket, reconnect with new query params.

**5. Log level highlighting**

Before writing to xterm, scan each log line:
- If line contains `ERROR` or `FATAL` (case-insensitive): wrap in red ANSI
- If line contains `WARN` or `WARNING`: wrap in yellow ANSI
- If line contains `DEBUG` or `TRACE`: wrap in dim ANSI
- JSON log detection: if line starts with `{` and contains `"level"`, parse level field

Keep highlighting lightweight — regex match on keywords, no full JSON parse unless line starts with `{`.

## Files to Modify

- `internal/docker/client.go` — Add LogOptions, update ContainerLogs signature
- `internal/feature/websocket/handler.go` — Parse query params, pass LogOptions
- `web/src/components/ContainerLogs.tsx` — Toolbar controls, reconnect logic, highlighting

## Testing

- Backend: Unit test for LogOptions to Docker API options mapping
- E2E: Verify log viewer works with different options via Playwright
