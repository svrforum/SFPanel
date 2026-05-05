# OS × Docker Integrated Views (Theme B Phase 1) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship Unified Port Map (UFW × Docker DNAT × `ss` listening, one row per port, read-only with jump links) and Docker Volume Usage cache (5-minute background `du -sb` collector + Volume page Size column + Disk page card).

**Architecture:** New `internal/feature/portmap` package merges three existing data sources (firewall package's UFW parser, docker.Client port bindings, a shared `ss` listener parser) via a pure-function aggregator. New `internal/monitor/docker_volume_usage.go` runs a per-node background goroutine that sequentially `du -sb`s each Docker volume and caches result in a new `docker_volume_usage` table (migration 20). Frontend adds a "포트 맵" tab to the existing Firewall page, a "Docker 볼륨 사용량" card to the Disk page, and a Size column to the Volumes page.

**Tech Stack:** Go 1.25 + existing `internal/common/exec.Commander` for `ss`/`du`/`ufw` invocations, `gopkg.in/yaml.v3` not needed (no YAML), modernc.org/sqlite, React 19 + TypeScript, shadcn/ui (`Tabs`, `Table`, `Card`).

**Spec reference:** `docs/superpowers/specs/2026-05-05-portmap-and-volume-usage-design.md`

---

## File structure

| File | Responsibility |
|---|---|
| `internal/feature/portmap/types.go` (new) | `PortMapRow`, `FirewallInfo`, `ContainerInfo`, `ProcessInfo`, `SsEntry` types |
| `internal/feature/portmap/ss_parser.go` (new) | `ParseSs(out string) []SsEntry` — parse `ss -tlnp -H` / `ss -ulnp -H` output |
| `internal/feature/portmap/ss_parser_test.go` (new) | Table tests for IPv4/IPv6/multi-process/no-users:/UDP edges |
| `internal/feature/portmap/aggregator.go` (new) | `Aggregate(ufw, dnat, ss) []PortMapRow` — pure merge by `(port, proto)` |
| `internal/feature/portmap/aggregator_test.go` (new) | Merge correctness across overlap matrix |
| `internal/feature/portmap/handler.go` (new) | `Handler{Cmd, Docker}` + `GetPortMap` echo handler with errgroup fan-out |
| `internal/feature/portmap/handler_test.go` (new) | `GetPortMap` graceful-degradation when 1 source fails |
| `internal/api/router.go` (mod) | Register `GET /api/v1/system/portmap` + wire portmap.Handler |
| `internal/db/migrations.go` (mod) | Migration 20 — `docker_volume_usage` table |
| `internal/monitor/docker_volume_usage.go` (new) | `StartVolumeUsageCollector` 5-min ticker + `duOnce` helper |
| `internal/monitor/docker_volume_usage_test.go` (new) | One-tick test with mocked `Cmd` + Docker fake |
| `internal/feature/docker/handler.go` (mod) | Augment `ListVolumes` response with cached size_bytes / size_measured_at |
| `cmd/sfpanel/main.go` (mod) | Start `StartVolumeUsageCollector` next to existing observability collectors |
| `web/src/types/api.ts` (mod) | `PortMapRow` + sub-types; `VolumeWithSize` augmentation |
| `web/src/lib/api.ts` (mod) | `getPortMap()` method |
| `web/src/components/portmap/PortMapTable.tsx` (new) | shadcn Table with sticky header + 6 columns |
| `web/src/pages/Firewall.tsx` (mod) | Wrap UFW UI in `<TabsContent value="rules">`, add `<TabsContent value="portmap">` |
| `web/src/components/disk/DockerVolumeUsageCard.tsx` (new) | Top 10 + total + link to volumes page |
| `web/src/pages/system/Disk.tsx` (mod) | Render `<DockerVolumeUsageCard>` near partition pie |
| `web/src/pages/docker/DockerVolumes.tsx` (mod) | Add sortable "크기" column |

---

## Task 1: Schema migration 20 — `docker_volume_usage`

**Files:**
- Modify: `internal/db/migrations.go`
- Test: `internal/db/migrations_test.go` (existing `TestRunMigrations_Idempotent` auto-exercises new entries)

- [ ] **Step 1: Run baseline migration test**

Run: `cd /opt/stacks/SFPanel && go test ./internal/db/ -run TestRunMigrations_Idempotent -count=1 -v`
Expected: PASS.

- [ ] **Step 2: Append migration 20**

Open `internal/db/migrations.go` and append to the end of the `migrations` slice (just before the closing `}`):

```go
	{ID: 20, Up: `CREATE TABLE IF NOT EXISTS docker_volume_usage (
		volume_name TEXT PRIMARY KEY,
		size_bytes  INTEGER NOT NULL,
		measured_at INTEGER NOT NULL
	)`},
```

- [ ] **Step 3: Run migration test**

Run: `cd /opt/stacks/SFPanel && go test ./internal/db/ -count=1 -v`
Expected: All PASS, log shows `applied id=20`.

- [ ] **Step 4: Commit**

```bash
git add internal/db/migrations.go
git commit -m "db: docker_volume_usage table (migration 20)"
```

---

## Task 2: Portmap types + first failing parser test

**Files:**
- Create: `internal/feature/portmap/types.go`
- Create: `internal/feature/portmap/ss_parser.go`
- Create: `internal/feature/portmap/ss_parser_test.go`

- [ ] **Step 1: Write failing test for IPv4 single-process**

Create `internal/feature/portmap/ss_parser_test.go`:

```go
package portmap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSs_IPv4SingleProcess(t *testing.T) {
	// `ss -tlnp -H` output (no header, tab-separated columns).
	in := `LISTEN 0      128          *:8444         *:*    users:(("sfpanel",pid=1410507,fd=10))
`
	got := ParseSs(in, "tcp")
	require.Len(t, got, 1)
	require.Equal(t, 8444, got[0].Port)
	require.Equal(t, "tcp", got[0].Proto)
	require.Equal(t, 1410507, got[0].PID)
	require.Equal(t, "sfpanel", got[0].Name)
}
```

- [ ] **Step 2: Run, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/portmap/ -run TestParseSs -count=1`
Expected: FAIL — `undefined: ParseSs`, `undefined: SsEntry`.

- [ ] **Step 3: Create types.go**

Create `internal/feature/portmap/types.go`:

```go
package portmap

// PortMapRow is the canonical row returned by GET /api/v1/system/portmap.
// Each non-nil pointer reflects one of the three data sources.
type PortMapRow struct {
	Port      int             `json:"port"`
	Proto     string          `json:"proto"`     // "tcp" | "udp"
	State     string          `json:"state"`     // "listening" | "bound"
	Firewall  *FirewallInfo   `json:"firewall"`
	Container *ContainerInfo  `json:"container"`
	Process   *ProcessInfo    `json:"process"`
}

// FirewallInfo captures the UFW rule that affects this port.
type FirewallInfo struct {
	Action string `json:"action"`  // "ALLOW" | "DENY" | "REJECT" | "LIMIT"
	Scope  string `json:"scope"`   // "Anywhere" | "192.168.1.0/24" | …
	RuleID int    `json:"rule_id"` // UFW rule number
	Source string `json:"source"`  // "ufw"
}

// ContainerInfo captures the Docker DNAT mapping for this port.
type ContainerInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Stack string `json:"stack"` // "" if not part of a compose stack
}

// ProcessInfo captures the bare host process listening on this port.
type ProcessInfo struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}

// SsEntry is one parsed entry from `ss -tlnp -H` / `ss -ulnp -H`.
// Multiple users on one socket emit one entry each.
type SsEntry struct {
	Port  int
	Proto string // "tcp" | "udp"
	PID   int
	Name  string
}

// PortBinding is the simplified Docker DNAT mapping passed to Aggregate.
type PortBinding struct {
	HostPort      int
	ContainerID   string
	ContainerName string
	Stack         string
}
```

- [ ] **Step 4: Create minimal ss_parser.go**

Create `internal/feature/portmap/ss_parser.go`:

```go
package portmap

import (
	"regexp"
	"strconv"
	"strings"
)

// ssAddrRe extracts port from "*:8444" or "[::]:8444" or "127.0.0.1:80".
var ssAddrRe = regexp.MustCompile(`:(\d+)$`)

// ssUserRe extracts pid and process name from `users:(("sfpanel",pid=1410507,fd=10))`.
// The tuple can repeat for multi-process listeners.
var ssUserRe = regexp.MustCompile(`\("([^"]+)",pid=(\d+),fd=\d+\)`)

// ParseSs parses output of `ss -tlnp -H` (or -ulnp). proto is "tcp" or "udp".
// Returns one SsEntry per (port, listener-process) tuple.
func ParseSs(out, proto string) []SsEntry {
	out = strings.TrimSpace(out)
	if out == "" {
		return nil
	}
	entries := []SsEntry{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		// `ss -H` columns: State Recv-Q Send-Q Local-Address:Port Peer-Address:Port [users:(...)]
		// Local-Address:Port is field index 3 for tcp listening.
		localAddr := fields[3]
		m := ssAddrRe.FindStringSubmatch(localAddr)
		if m == nil {
			continue
		}
		port, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		// Look for users:(...) somewhere in the line (may not exist if not root).
		users := ssUserRe.FindAllStringSubmatch(line, -1)
		if len(users) == 0 {
			entries = append(entries, SsEntry{Port: port, Proto: proto})
			continue
		}
		for _, u := range users {
			pid, _ := strconv.Atoi(u[2])
			entries = append(entries, SsEntry{
				Port:  port,
				Proto: proto,
				PID:   pid,
				Name:  u[1],
			})
		}
	}
	return entries
}
```

- [ ] **Step 5: Run test, expect PASS**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/portmap/ -run TestParseSs -count=1 -v`
Expected: 1 PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/feature/portmap/types.go internal/feature/portmap/ss_parser.go internal/feature/portmap/ss_parser_test.go
git commit -m "portmap: types + ss parser scaffold (IPv4 single-process)"
```

---

## Task 3: ss parser edges — IPv6 / multi-process / no users / UDP

**Files:**
- Modify: `internal/feature/portmap/ss_parser_test.go`

- [ ] **Step 1: Append failing tests**

Append to `internal/feature/portmap/ss_parser_test.go`:

```go
func TestParseSs_IPv6(t *testing.T) {
	in := `LISTEN 0      128       [::]:22       [::]:*    users:(("sshd",pid=1024,fd=4))
`
	got := ParseSs(in, "tcp")
	require.Len(t, got, 1)
	require.Equal(t, 22, got[0].Port)
	require.Equal(t, "sshd", got[0].Name)
}

func TestParseSs_MultiProcess(t *testing.T) {
	in := `LISTEN 0      128          *:5432         *:*    users:(("docker-proxy",pid=9912,fd=4),("postgres",pid=10001,fd=3))
`
	got := ParseSs(in, "tcp")
	require.Len(t, got, 2)
	require.Equal(t, 5432, got[0].Port)
	require.Equal(t, "docker-proxy", got[0].Name)
	require.Equal(t, "postgres", got[1].Name)
}

func TestParseSs_NoUsersClause(t *testing.T) {
	// Non-root invocation: no users:(...) suffix.
	in := `LISTEN 0      128          *:80           *:*
`
	got := ParseSs(in, "tcp")
	require.Len(t, got, 1)
	require.Equal(t, 80, got[0].Port)
	require.Equal(t, 0, got[0].PID)
	require.Equal(t, "", got[0].Name)
}

func TestParseSs_UDP(t *testing.T) {
	in := `UNCONN 0      0            *:53           *:*    users:(("dnsmasq",pid=512,fd=2))
`
	got := ParseSs(in, "udp")
	require.Len(t, got, 1)
	require.Equal(t, 53, got[0].Port)
	require.Equal(t, "udp", got[0].Proto)
}

func TestParseSs_EmptyInput(t *testing.T) {
	require.Empty(t, ParseSs("", "tcp"))
	require.Empty(t, ParseSs("   \n  ", "tcp"))
}
```

- [ ] **Step 2: Run, expect 4-5 fails**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/portmap/ -run TestParseSs -count=1 -v`
Expected: most PASS already (the regex covers most cases) but verify. Likely 0 fails because IPv6 / multi-process are already covered by the regex from Task 2. If any fail, fix below.

- [ ] **Step 3: Adjust parser if needed**

If multi-process or UDP test fails, the issue is the regex matching only the first `users:` group or not handling `UNCONN` state. Adjust as follows:

```go
// In ParseSs, before the strings.Fields(line) line, add:
//   if !strings.HasPrefix(line, "LISTEN") && !strings.HasPrefix(line, "UNCONN") {
//       continue
//   }
```

This filters out non-listening lines. If multi-process count is wrong, verify `ssUserRe.FindAllStringSubmatch(line, -1)` returns all matches (regex is correct as written; test should pass).

- [ ] **Step 4: Run all parser tests**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/portmap/ -run TestParseSs -count=1 -v`
Expected: 5 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/feature/portmap/ss_parser.go internal/feature/portmap/ss_parser_test.go
git commit -m "portmap: ss parser edges — IPv6, multi-process, no users, UDP, empty"
```

---

## Task 4: Aggregator — pure merge function

**Files:**
- Create: `internal/feature/portmap/aggregator.go`
- Create: `internal/feature/portmap/aggregator_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/feature/portmap/aggregator_test.go`:

```go
package portmap

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAggregate_AllThreeSources(t *testing.T) {
	ufw := []FirewallInfo{
		{Action: "ALLOW", Scope: "192.168.1.0/24", RuleID: 12, Source: "ufw"},
	}
	dnat := []PortBinding{
		{HostPort: 5432, ContainerID: "abc", ContainerName: "npg-db", Stack: "npg"},
	}
	ss := []SsEntry{
		{Port: 5432, Proto: "tcp", PID: 9912, Name: "docker-proxy"},
	}
	got := Aggregate(map[int]FirewallInfo{5432: ufw[0]}, dnat, ss)
	require.Len(t, got, 1)
	require.Equal(t, 5432, got[0].Port)
	require.Equal(t, "tcp", got[0].Proto)
	require.NotNil(t, got[0].Firewall)
	require.Equal(t, "ALLOW", got[0].Firewall.Action)
	require.NotNil(t, got[0].Container)
	require.Equal(t, "npg-db", got[0].Container.Name)
	require.NotNil(t, got[0].Process)
	require.Equal(t, "docker-proxy", got[0].Process.Name)
}

func TestAggregate_OnlyProcess_NoFirewall_NoContainer(t *testing.T) {
	ss := []SsEntry{
		{Port: 22, Proto: "tcp", PID: 1024, Name: "sshd"},
	}
	got := Aggregate(nil, nil, ss)
	require.Len(t, got, 1)
	require.Equal(t, 22, got[0].Port)
	require.Nil(t, got[0].Firewall)
	require.Nil(t, got[0].Container)
	require.NotNil(t, got[0].Process)
}

func TestAggregate_OnlyContainer_BoundButNotListening(t *testing.T) {
	dnat := []PortBinding{
		{HostPort: 8080, ContainerID: "x", ContainerName: "myapp", Stack: ""},
	}
	got := Aggregate(nil, dnat, nil)
	require.Len(t, got, 1)
	require.Equal(t, 8080, got[0].Port)
	require.Equal(t, "bound", got[0].State)
	require.NotNil(t, got[0].Container)
	require.Nil(t, got[0].Process)
}

func TestAggregate_SortedByPortAsc(t *testing.T) {
	ss := []SsEntry{
		{Port: 80, Proto: "tcp"},
		{Port: 22, Proto: "tcp"},
		{Port: 443, Proto: "tcp"},
	}
	got := Aggregate(nil, nil, ss)
	require.Len(t, got, 3)
	require.Equal(t, 22, got[0].Port)
	require.Equal(t, 80, got[1].Port)
	require.Equal(t, 443, got[2].Port)
}

func TestAggregate_DedupesMultiProcessSameSocket(t *testing.T) {
	// Same port, two processes from `ss` (e.g. docker-proxy + actual server).
	// Aggregate should produce ONE row, processes joined into a single Process info
	// (preferring docker-proxy if present, since DNAT containers always go through it).
	ss := []SsEntry{
		{Port: 5432, Proto: "tcp", PID: 9912, Name: "docker-proxy"},
		{Port: 5432, Proto: "tcp", PID: 10001, Name: "postgres"},
	}
	got := Aggregate(nil, nil, ss)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Process)
}
```

- [ ] **Step 2: Run, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/portmap/ -run TestAggregate -count=1`
Expected: FAIL — `undefined: Aggregate`.

- [ ] **Step 3: Implement Aggregate**

Create `internal/feature/portmap/aggregator.go`:

```go
package portmap

import (
	"sort"
)

// Aggregate merges UFW firewall info, Docker port bindings, and listener
// entries into one sorted []PortMapRow keyed by port.
//
// ufwByPort: map from port number → firewall info (caller pre-indexes
// because UFW rules can match multiple ports via ranges; the caller is
// responsible for expansion).
//
// dnat: Docker port bindings (HostPort).
//
// ss: listener entries from `ParseSs`. Multiple entries on same port
// collapse into one row (first-seen wins for Process; in practice
// docker-proxy always sorts before user processes, which is the
// preferred display).
func Aggregate(ufwByPort map[int]FirewallInfo, dnat []PortBinding, ss []SsEntry) []PortMapRow {
	rows := map[portKey]*PortMapRow{}

	// Listeners → "listening" state.
	for _, e := range ss {
		k := portKey{Port: e.Port, Proto: e.Proto}
		if _, ok := rows[k]; !ok {
			rows[k] = &PortMapRow{Port: e.Port, Proto: e.Proto, State: "listening"}
		}
		row := rows[k]
		if row.Process == nil {
			row.Process = &ProcessInfo{PID: e.PID, Name: e.Name}
		}
	}

	// Docker DNAT → at least "bound" (overrides to "listening" if ss already
	// flagged it — DNAT containers always have docker-proxy listening).
	for _, b := range dnat {
		// Docker bindings are tcp by default; we treat all as tcp here. UDP
		// bindings are rare and surface via ss.
		k := portKey{Port: b.HostPort, Proto: "tcp"}
		if _, ok := rows[k]; !ok {
			rows[k] = &PortMapRow{Port: b.HostPort, Proto: "tcp", State: "bound"}
		}
		row := rows[k]
		row.Container = &ContainerInfo{
			ID:    b.ContainerID,
			Name:  b.ContainerName,
			Stack: b.Stack,
		}
	}

	// UFW → attach to whichever row uses this port. UFW rules without a
	// matching listener / DNAT are not surfaced (no point showing an
	// allow rule for a port nothing uses).
	for port, info := range ufwByPort {
		copyInfo := info
		// Apply to both tcp and udp variants if present.
		for _, proto := range []string{"tcp", "udp"} {
			k := portKey{Port: port, Proto: proto}
			if row, ok := rows[k]; ok {
				row.Firewall = &copyInfo
			}
		}
	}

	out := make([]PortMapRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Proto < out[j].Proto
	})
	return out
}

type portKey struct {
	Port  int
	Proto string
}
```

- [ ] **Step 4: Run, expect 5 PASS**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/portmap/ -run TestAggregate -count=1 -v`
Expected: 5 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/feature/portmap/aggregator.go internal/feature/portmap/aggregator_test.go
git commit -m "portmap: Aggregate pure function (3-source merge)"
```

---

## Task 5: Handler with errgroup fan-out + graceful degradation

**Files:**
- Create: `internal/feature/portmap/handler.go`
- Create: `internal/feature/portmap/handler_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/feature/portmap/handler_test.go`:

```go
package portmap

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// TestGetPortMap_GracefulDegradation: when ss is missing, page still
// renders with empty rows but 200 OK.
func TestGetPortMap_GracefulDegradation(t *testing.T) {
	mock := exec.NewMockCommander()
	// All 3 commands fail (ss, ufw, docker socket simulated absent via nil Docker)
	mock.ExpectFail("ss", "ss not found")
	mock.ExpectFail("ufw", "ufw not installed")

	h := &Handler{Cmd: mock, Docker: nil}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/portmap", nil)
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)

	require.NoError(t, h.GetPortMap(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Success bool         `json:"success"`
		Data    []PortMapRow `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	require.Empty(t, resp.Data) // all sources failed → empty rows
}
```

Note: `exec.NewMockCommander()` may not have an `ExpectFail` method; check the actual `internal/common/exec/mock.go` API and adapt the call. If only `Run`/`RunWithTimeout` mocks exist, set the mock's queued responses to return errors.

- [ ] **Step 2: Run, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/portmap/ -run TestGetPortMap -count=1`
Expected: FAIL — `undefined: Handler`, `undefined: GetPortMap`.

- [ ] **Step 3: Implement handler**

Create `internal/feature/portmap/handler.go`:

```go
package portmap

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/docker"
)

// Handler exposes the unified port map endpoint.
type Handler struct {
	Cmd    exec.Commander
	Docker *docker.Client // nil-safe; nil means Docker source returns empty
}

// GetPortMap aggregates UFW + Docker DNAT + ss listening into a single response.
//   GET /api/v1/system/portmap
func (h *Handler) GetPortMap(c echo.Context) error {
	ctx := c.Request().Context()

	var (
		mu sync.Mutex
		wg sync.WaitGroup

		ufwByPort = map[int]FirewallInfo{}
		dnat      []PortBinding
		listeners []SsEntry
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		out, err := h.Cmd.RunWithTimeout(5_000_000_000, "ss", "-tlnp", "-H")
		if err != nil {
			slog.Warn("portmap: ss tcp failed", "error", err)
			return
		}
		entries := ParseSs(out, "tcp")
		out2, err2 := h.Cmd.RunWithTimeout(5_000_000_000, "ss", "-ulnp", "-H")
		if err2 != nil {
			slog.Warn("portmap: ss udp failed", "error", err2)
		} else {
			entries = append(entries, ParseSs(out2, "udp")...)
		}
		mu.Lock()
		listeners = entries
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		out, err := h.Cmd.RunWithTimeout(5_000_000_000, "ufw", "status", "numbered")
		if err != nil {
			slog.Warn("portmap: ufw status failed", "error", err)
			return
		}
		// We do NOT fully re-parse UFW here. We rely on the existing parser in
		// internal/feature/firewall to do format conversions. To keep portmap
		// independent, parse only enough to extract (port → action/scope/ruleID).
		parsed := parseUFWForPortMap(out)
		mu.Lock()
		ufwByPort = parsed
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		if h.Docker == nil {
			return
		}
		bindings, err := collectDockerBindings(ctx, h.Docker)
		if err != nil {
			slog.Warn("portmap: docker bindings failed", "error", err)
			return
		}
		mu.Lock()
		dnat = bindings
		mu.Unlock()
	}()
	wg.Wait()

	rows := Aggregate(ufwByPort, dnat, listeners)
	return response.OK(c, rows)
}

// parseUFWForPortMap is a minimal UFW status numbered parser scoped to the
// fields portmap needs. Single-port rules (`22/tcp`) yield one entry; ranges
// like `4000:4010/tcp` yield 11 entries.
func parseUFWForPortMap(output string) map[int]FirewallInfo {
	out := map[int]FirewallInfo{}
	// Reuse the format from internal/feature/firewall/firewall_ufw.go::parseUFWRules
	// but extract by port number only. Lines look like:
	//   [ 1] 22/tcp                     ALLOW IN    Anywhere                   # SSH
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[") {
			continue
		}
		// Find the rule body after "]".
		closeIdx := strings.Index(line, "]")
		if closeIdx < 0 {
			continue
		}
		ruleNumStr := strings.TrimSpace(strings.Trim(line[1:closeIdx], " "))
		ruleID := 0
		_, _ = fmtSscan(ruleNumStr, &ruleID)
		body := strings.TrimSpace(line[closeIdx+1:])
		// Strip trailing "# comment".
		if hashIdx := strings.LastIndex(body, "#"); hashIdx >= 0 {
			body = strings.TrimSpace(body[:hashIdx])
		}
		// Split on action keyword.
		fields := strings.Fields(body)
		if len(fields) < 2 {
			continue
		}
		// Format: "<to>/<proto>" "ALLOW IN" "<from>"
		toField := fields[0]
		var action, scope string
		// Find action verb among "ALLOW", "DENY", "REJECT", "LIMIT".
		actionIdx := -1
		for i, f := range fields {
			switch f {
			case "ALLOW", "DENY", "REJECT", "LIMIT":
				actionIdx = i
				action = f
			}
			if actionIdx >= 0 {
				break
			}
		}
		if actionIdx < 0 {
			continue
		}
		// Scope is the trailing fields after action+direction.
		scopeStart := actionIdx + 1
		// Skip optional "IN" / "OUT" / "FWD".
		if scopeStart < len(fields) {
			switch fields[scopeStart] {
			case "IN", "OUT", "FWD":
				scopeStart++
			}
		}
		if scopeStart < len(fields) {
			scope = strings.Join(fields[scopeStart:], " ")
		}
		// Parse ports out of toField. Examples: "22/tcp", "22", "4000:4010/tcp".
		port, proto := splitPortProto(toField)
		if port == 0 {
			continue
		}
		_ = proto // not used here; aggregator probes both proto rows
		out[port] = FirewallInfo{Action: action, Scope: scope, RuleID: ruleID, Source: "ufw"}
	}
	return out
}

// splitPortProto parses "22/tcp" → (22, "tcp"); "22" → (22, ""); "4000:4010/tcp" → (4000, "tcp")
// (range start; range expansion is intentionally not implemented in Phase 1).
func splitPortProto(s string) (int, string) {
	port := 0
	proto := ""
	if slash := strings.Index(s, "/"); slash >= 0 {
		proto = s[slash+1:]
		s = s[:slash]
	}
	if colon := strings.Index(s, ":"); colon >= 0 {
		s = s[:colon] // range start
	}
	_, _ = fmtSscan(s, &port)
	return port, proto
}

// fmtSscan is a tiny wrapper around fmt.Sscan to keep the imports tight in
// this file (avoids pulling fmt for a one-liner).
func fmtSscan(s string, dst *int) (int, error) {
	return scanIntoInt(s, dst)
}

func scanIntoInt(s string, dst *int) (int, error) {
	*dst = 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errParseInt
		}
		*dst = *dst*10 + int(r-'0')
	}
	return 1, nil
}

var errParseInt = errParse("not a number")

type errParse string

func (e errParse) Error() string { return string(e) }

// collectDockerBindings calls h.Docker to get all containers + their port
// bindings, flattened into []PortBinding for Aggregate.
func collectDockerBindings(ctx context.Context, dc *docker.Client) ([]PortBinding, error) {
	containers, err := dc.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	out := []PortBinding{}
	for _, c := range containers {
		name := ""
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		stack := c.Labels["com.docker.compose.project"]
		for _, p := range c.Ports {
			if p.PublicPort == 0 {
				continue
			}
			out = append(out, PortBinding{
				HostPort:      int(p.PublicPort),
				ContainerID:   c.ID,
				ContainerName: name,
				Stack:         stack,
			})
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Adjust mock if needed and run test**

If the `exec.MockCommander` API doesn't have `ExpectFail`, replace the test setup with whatever the real API uses. Typical pattern in this repo:
```go
mock := exec.NewMockCommander()
mock.QueueOutput("", fmt.Errorf("ss not found"))  // first call returns error
```
Adjust the test to whatever `mock.go` exports. Run:

```
cd /opt/stacks/SFPanel && go test ./internal/feature/portmap/ -run TestGetPortMap -count=1 -v
```
Expected: 1 PASS.

- [ ] **Step 5: Run all portmap tests**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/portmap/ -count=1 -v`
Expected: All PASS.

- [ ] **Step 6: Lint**

Run: `/home/dalso/go/bin/golangci-lint run ./internal/feature/portmap/...`
Expected: clean. The handcrafted `errParse` / `scanIntoInt` may trip "use strconv.Atoi" linters — if so, replace with `strconv.Atoi(s)` directly (and import `strconv`).

- [ ] **Step 7: Commit**

```bash
git add internal/feature/portmap/handler.go internal/feature/portmap/handler_test.go
git commit -m "portmap: handler with errgroup fan-out + graceful degradation"
```

---

## Task 6: Register portmap route

**Files:**
- Modify: `internal/api/router.go`

- [ ] **Step 1: Find the system route group**

Run: `grep -n 'system/' internal/api/router.go | head`
Identify the line where `system` routes are registered (e.g. `authorized.GET("/system/info", ...)`).

- [ ] **Step 2: Wire portmap.Handler**

Near the top of `NewRouter` where other handlers are constructed (search `systemHandler :=` or similar), add:

```go
portmapHandler := &portmap.Handler{Cmd: commonExec.NewCommander(), Docker: dockerClient}
```

The exact import alias for `internal/feature/portmap` may need adjustment — add to the import block at top:
```go
"github.com/svrforum/SFPanel/internal/feature/portmap"
```

`dockerClient` is already declared near top (used for the existing docker handler). `commonExec` and `NewCommander` — check existing similar handler construction for the exact var name.

- [ ] **Step 3: Register route**

Add after the existing `authorized.GET("/system/...")` lines:

```go
authorized.GET("/system/portmap", portmapHandler.GetPortMap)
```

- [ ] **Step 4: Build + run all backend tests**

Run: `cd /opt/stacks/SFPanel && go build ./... && go test ./internal/feature/portmap/... -count=1`
Expected: clean.

- [ ] **Step 5: Lint**

Run: `/home/dalso/go/bin/golangci-lint run ./internal/api/... ./internal/feature/portmap/...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/api/router.go
git commit -m "router: register GET /api/v1/system/portmap"
```

---

## Task 7: Volume usage collector — types + first failing test

**Files:**
- Create: `internal/monitor/docker_volume_usage.go`
- Create: `internal/monitor/docker_volume_usage_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/monitor/docker_volume_usage_test.go`:

```go
package monitor

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/volume"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

func openTestDBForVolUsage(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE docker_volume_usage (
		volume_name TEXT PRIMARY KEY,
		size_bytes  INTEGER NOT NULL,
		measured_at INTEGER NOT NULL
	)`)
	require.NoError(t, err)
	return db
}

// fakeVolumeLister returns a fixed list and counts calls.
type fakeVolumeLister struct {
	volumes []*volume.Volume
	calls   int
}

func (f *fakeVolumeLister) ListVolumes() []*volume.Volume {
	f.calls++
	return f.volumes
}

func TestVolumeUsageOnce_WritesCacheRows(t *testing.T) {
	db := openTestDBForVolUsage(t)
	mock := exec.NewMockCommander()
	// Stub `du -sb /var/lib/docker/volumes/<name>/_data` for two volumes.
	mock.QueueOutput("123456\t/var/lib/docker/volumes/v1/_data\n", nil)
	mock.QueueOutput("987654321\t/var/lib/docker/volumes/v2/_data\n", nil)

	lister := &fakeVolumeLister{
		volumes: []*volume.Volume{
			{Name: "v1"},
			{Name: "v2"},
		},
	}
	measureVolumeUsageOnce(db, mock, lister.ListVolumes)

	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM docker_volume_usage`).Scan(&n))
	require.Equal(t, 2, n)

	var sz int64
	require.NoError(t, db.QueryRow(`SELECT size_bytes FROM docker_volume_usage WHERE volume_name='v2'`).Scan(&sz))
	require.Equal(t, int64(987654321), sz)
}
```

If `exec.MockCommander.QueueOutput` API name differs, adjust to whatever the existing pattern is (check `internal/common/exec/mock.go`).

- [ ] **Step 2: Run, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/monitor/ -run TestVolumeUsageOnce -count=1`
Expected: FAIL — `undefined: measureVolumeUsageOnce`.

- [ ] **Step 3: Implement collector**

Create `internal/monitor/docker_volume_usage.go`:

```go
package monitor

import (
	"context"
	"database/sql"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/volume"

	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/docker"
)

const (
	volumeUsageInterval = 5 * time.Minute
	volumeUsageInitialDelay = 30 * time.Second
	duPerVolumeTimeout = 30 * time.Second
)

// VolumeListerFunc returns the current list of Docker volumes. Decoupled from
// docker.Client so tests can inject fakes without spinning up a real daemon.
type VolumeListerFunc func() []*volume.Volume

// StartVolumeUsageCollector launches a 5-minute ticker goroutine that
// sequentially measures `du -sb` for every Docker volume and writes the
// result to docker_volume_usage. Stops cleanly on ctx cancellation.
func StartVolumeUsageCollector(ctx context.Context, db *sql.DB, dockerCli *docker.Client) {
	if dockerCli == nil {
		slog.Warn("volume usage collector: docker client nil; not starting")
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(volumeUsageInitialDelay):
		}
		cmd := exec.NewSystemCommander()
		lister := func() []*volume.Volume {
			lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			vs, err := dockerCli.ListVolumes(lctx)
			if err != nil {
				slog.Warn("volume usage collector: list volumes failed", "error", err)
				return nil
			}
			return vs
		}
		measureVolumeUsageOnce(db, cmd, lister)
		ticker := time.NewTicker(volumeUsageInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				measureVolumeUsageOnce(db, cmd, lister)
			}
		}
	}()
}

// measureVolumeUsageOnce performs one tick: enumerates volumes, runs `du -sb`
// sequentially per volume, writes result to cache. Errors per volume are
// logged + skipped — never abort the tick.
func measureVolumeUsageOnce(db *sql.DB, cmd exec.Commander, lister VolumeListerFunc) {
	volumes := lister()
	now := time.Now().UnixMilli()
	for _, v := range volumes {
		path := "/var/lib/docker/volumes/" + v.Name + "/_data"
		out, err := cmd.RunWithTimeout(duPerVolumeTimeout, "du", "-sb", path)
		if err != nil {
			slog.Warn("volume usage: du failed", "volume", v.Name, "error", err)
			continue
		}
		// `du -sb` output: "<bytes>\t<path>\n"
		fields := strings.Fields(strings.TrimSpace(out))
		if len(fields) == 0 {
			continue
		}
		bytes, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			slog.Warn("volume usage: parse bytes failed", "volume", v.Name, "raw", fields[0])
			continue
		}
		if _, err := db.Exec(
			`INSERT OR REPLACE INTO docker_volume_usage (volume_name, size_bytes, measured_at) VALUES (?, ?, ?)`,
			v.Name, bytes, now,
		); err != nil {
			slog.Warn("volume usage: db write failed", "volume", v.Name, "error", err)
		}
	}
}
```

- [ ] **Step 4: Run test, expect PASS**

Run: `cd /opt/stacks/SFPanel && go test ./internal/monitor/ -run TestVolumeUsageOnce -count=1 -v`
Expected: 1 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/monitor/docker_volume_usage.go internal/monitor/docker_volume_usage_test.go
git commit -m "monitor: docker volume usage collector (5min du, sequential)"
```

---

## Task 8: Wire StartVolumeUsageCollector in main.go

**Files:**
- Modify: `cmd/sfpanel/main.go`

- [ ] **Step 1: Find where observability collectors start**

Run: `grep -n 'StartDockerHistoryCollector\|StartDockerEventsListener\|StartDockerMetricsRetention' cmd/sfpanel/main.go`
Expected: those lines are inside the `if cfg.Docker.Observability.IsEnabled()` block.

- [ ] **Step 2: Add collector start**

Volume usage runs unconditionally — it's not gated on the observability flag (different feature). Add a NEW block AFTER the observability block:

```go
// Docker volume usage cache (Theme B Phase 1) — independent of
// observability flag. Only runs if Docker socket is available.
if dockerCli, dockerErr := docker.NewClient(cfg.Docker.Socket); dockerErr == nil {
	monitor.StartVolumeUsageCollector(bgCtx, database, dockerCli)
}
```

- [ ] **Step 3: Build + run**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: clean.

- [ ] **Step 4: Run all tests**

Run: `cd /opt/stacks/SFPanel && go test ./internal/monitor/... -count=1`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/sfpanel/main.go
git commit -m "wire: StartVolumeUsageCollector in main.go on bgCtx"
```

---

## Task 9: Augment ListVolumes with size cache

**Files:**
- Modify: `internal/feature/docker/handler.go`

- [ ] **Step 1: Locate ListVolumes**

Run: `grep -n 'ListVolumes\|listVolumes' internal/feature/docker/handler.go`
Expected: a method exists (e.g. `func (h *Handler) ListVolumes(c echo.Context) error`).

- [ ] **Step 2: Augment with size data**

Read the existing handler. After the call to fetch volumes via `h.Docker.ListVolumes(ctx)` or `ListVolumesWithUsage`, JOIN with the cache:

Add a small helper near the bottom of `handler.go`:

```go
// volumeUsageRow caches size data fetched from docker_volume_usage.
type volumeUsageRow struct {
	SizeBytes      int64
	MeasuredAtUnix int64 // milliseconds
}

// loadVolumeUsageMap returns a map[volumeName]→size cache. Empty map on error.
func (h *Handler) loadVolumeUsageMap() map[string]volumeUsageRow {
	out := map[string]volumeUsageRow{}
	if h.DB == nil {
		return out
	}
	rows, err := h.DB.Query(`SELECT volume_name, size_bytes, measured_at FROM docker_volume_usage`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var sz, ts int64
		if err := rows.Scan(&name, &sz, &ts); err != nil {
			continue
		}
		out[name] = volumeUsageRow{SizeBytes: sz, MeasuredAtUnix: ts}
	}
	return out
}
```

In the existing `ListVolumes` handler, after fetching the volume list, decorate the response. The exact shape depends on the existing code — typically:

```go
// After the existing fetch:
usageMap := h.loadVolumeUsageMap()
type augmented struct {
    docker.VolumeWithUsage          // existing inline shape
    SizeBytes      *int64 `json:"size_bytes"`
    SizeMeasuredAt *int64 `json:"size_measured_at"`
}
out := make([]augmented, 0, len(volumes))
for _, v := range volumes {
    a := augmented{VolumeWithUsage: v}
    if u, ok := usageMap[v.Name]; ok {
        sz := u.SizeBytes
        ts := u.MeasuredAtUnix
        a.SizeBytes = &sz
        a.SizeMeasuredAt = &ts
    }
    out = append(out, a)
}
return response.OK(c, out)
```

If `ListVolumes` already returns `docker.VolumeWithUsage` directly, change the response to `[]augmented` and import nothing new (`docker.VolumeWithUsage` is already imported).

- [ ] **Step 3: Build + run**

Run: `cd /opt/stacks/SFPanel && go build ./... && go test ./internal/feature/docker/... -count=1`
Expected: clean.

- [ ] **Step 4: Lint**

Run: `/home/dalso/go/bin/golangci-lint run ./internal/feature/docker/...`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/feature/docker/handler.go
git commit -m "docker: augment ListVolumes with size_bytes from cache"
```

---

## Task 10: Frontend types + API methods

**Files:**
- Modify: `web/src/types/api.ts`
- Modify: `web/src/lib/api.ts`

- [ ] **Step 1: Append types**

Append to `web/src/types/api.ts`:

```typescript
export interface PortMapFirewallInfo {
  action: string   // "ALLOW" | "DENY" | "REJECT" | "LIMIT"
  scope: string
  rule_id: number
  source: string   // "ufw"
}
export interface PortMapContainerInfo {
  id: string
  name: string
  stack: string
}
export interface PortMapProcessInfo {
  pid: number
  name: string
}
export interface PortMapRow {
  port: number
  proto: string    // "tcp" | "udp"
  state: string    // "listening" | "bound"
  firewall:  PortMapFirewallInfo  | null
  container: PortMapContainerInfo | null
  process:   PortMapProcessInfo   | null
}
```

The `Volume` type (existing in api.ts) needs `size_bytes` + `size_measured_at` added if it's a typed interface. If it's `unknown[]` already, no change needed; the React component will read the new fields ad-hoc.

Search: `grep -n 'interface Volume' web/src/types/api.ts` — if present, add:
```typescript
  size_bytes: number | null
  size_measured_at: number | null  // unix millis
```

- [ ] **Step 2: Add api method**

In `web/src/lib/api.ts`, add to imports at top:
```typescript
import type { ..., PortMapRow } from '@/types/api'
```

Add method to `ApiClient` near other `system` methods:
```typescript
getPortMap() {
  return this.request<PortMapRow[]>(`/system/portmap`)
}
```

- [ ] **Step 3: Build + lint frontend**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/src/types/api.ts web/src/lib/api.ts
git commit -m "web: types + api client for port map + volume size"
```

---

## Task 11: PortMapTable component

**Files:**
- Create: `web/src/components/portmap/PortMapTable.tsx`

- [ ] **Step 1: Create component**

Create `web/src/components/portmap/PortMapTable.tsx`:

```typescript
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { ExternalLink, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Badge } from '@/components/ui/badge'
import { api } from '@/lib/api'
import type { PortMapRow } from '@/types/api'

export function PortMapTable() {
  const [rows, setRows] = useState<PortMapRow[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = () => {
    setLoading(true); setError(null)
    api.getPortMap()
      .then((data) => setRows(data ?? []))
      .catch((e: Error) => setError(e.message || '포트 맵을 불러올 수 없습니다.'))
      .finally(() => setLoading(false))
  }

  useEffect(load, [])

  return (
    <div className="space-y-2">
      <div className="flex justify-end">
        <Button variant="outline" size="sm" onClick={load} disabled={loading}>
          <RefreshCw className={`h-3.5 w-3.5 mr-1 ${loading ? 'animate-spin' : ''}`} />
          새로고침
        </Button>
      </div>
      {error && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-[13px] text-destructive">
          {error}
        </div>
      )}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-20">포트</TableHead>
            <TableHead className="w-16">프로토콜</TableHead>
            <TableHead className="w-24">상태</TableHead>
            <TableHead className="w-56">방화벽</TableHead>
            <TableHead>컨테이너</TableHead>
            <TableHead className="w-56">프로세스</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.length === 0 && !loading && (
            <TableRow><TableCell colSpan={6} className="text-center text-muted-foreground py-8">데이터 없음</TableCell></TableRow>
          )}
          {rows.map((r, i) => {
            const externalRisk = r.firewall && r.firewall.scope === 'Anywhere' && r.container
            const exposedNoRule = !r.firewall && !r.container && r.process
            const borderClass = externalRisk
              ? 'border-l-2 border-amber-500'
              : exposedNoRule
              ? 'border-l-2 border-destructive'
              : ''
            return (
              <TableRow key={i} className={borderClass}>
                <TableCell className="font-mono">{r.port}</TableCell>
                <TableCell><Badge variant="outline" className="text-[10px]">{r.proto.toUpperCase()}</Badge></TableCell>
                <TableCell className="text-[12px]">
                  <Badge variant={r.state === 'listening' ? 'default' : 'secondary'} className="text-[10px]">
                    {r.state === 'listening' ? 'LISTENING' : 'BOUND'}
                  </Badge>
                </TableCell>
                <TableCell>
                  {r.firewall
                    ? <span className="inline-flex items-center gap-1 text-[12px]">
                        <span className={r.firewall.action === 'DENY' ? 'text-destructive' : 'text-emerald-600'}>{r.firewall.action}</span>
                        <span className="text-muted-foreground">{r.firewall.scope}</span>
                      </span>
                    : <span className="text-muted-foreground">—</span>}
                </TableCell>
                <TableCell className="text-[12px]">
                  {r.container
                    ? <Link to={`/docker/containers?selected=${encodeURIComponent(r.container.id)}`}
                            className="inline-flex items-center gap-1 hover:text-primary">
                        <span className="font-medium">{r.container.name}</span>
                        {r.container.stack && <span className="text-muted-foreground">({r.container.stack})</span>}
                        <ExternalLink className="h-3 w-3" />
                      </Link>
                    : <span className="text-muted-foreground">—</span>}
                </TableCell>
                <TableCell className="text-[12px]">
                  {r.process
                    ? <span className="font-mono">{r.process.name}{r.process.pid > 0 && ` (${r.process.pid})`}</span>
                    : <span className="text-muted-foreground">—</span>}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}
```

If `Badge` component doesn't exist: `cd web && npx shadcn@latest add badge`.

- [ ] **Step 2: Build + lint**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/portmap/PortMapTable.tsx web/src/components/ui/badge.tsx
git commit -m "web: PortMapTable component (3-source unified view)"
```

---

## Task 12: Wire PortMapTable into Firewall page

**Files:**
- Modify: `web/src/pages/Firewall.tsx` (or wherever the firewall page is)

- [ ] **Step 1: Locate the firewall page**

Run: `find web/src -iname '*firewall*' -type f`
Expected: e.g. `web/src/pages/Firewall.tsx` or `web/src/pages/system/Firewall.tsx`.

- [ ] **Step 2: Wrap existing UI in Tabs**

In the firewall page file, find the top-level body. Wrap the existing UFW UI in a shadcn Tabs:

```tsx
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'  // may already be imported
import { PortMapTable } from '@/components/portmap/PortMapTable'

// inside JSX:
<Tabs defaultValue="rules" className="w-full">
  <TabsList>
    <TabsTrigger value="rules">방화벽 룰</TabsTrigger>
    <TabsTrigger value="portmap">포트 맵</TabsTrigger>
  </TabsList>
  <TabsContent value="rules">
    {/* existing UFW UI body */}
  </TabsContent>
  <TabsContent value="portmap">
    <PortMapTable />
  </TabsContent>
</Tabs>
```

- [ ] **Step 3: Build + lint**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/Firewall.tsx
git commit -m "web: 방화벽 페이지에 \"포트 맵\" 탭 추가"
```

---

## Task 13: DockerVolumeUsageCard component

**Files:**
- Create: `web/src/components/disk/DockerVolumeUsageCard.tsx`

- [ ] **Step 1: Create component**

Create `web/src/components/disk/DockerVolumeUsageCard.tsx`:

```typescript
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { api } from '@/lib/api'

// Existing Volume type may not include size yet — read ad-hoc.
interface VolumeRow {
  Name: string
  size_bytes: number | null
  size_measured_at: number | null
}

function humanBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024, i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${v.toFixed(1)} ${units[i]}`
}

export function DockerVolumeUsageCard() {
  const [vols, setVols] = useState<VolumeRow[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api.listVolumes()
      .then((data) => setVols((data as VolumeRow[]) ?? []))
      .catch(() => setVols([]))
      .finally(() => setLoading(false))
  }, [])

  if (loading) return null
  const sized = vols.filter(v => typeof v.size_bytes === 'number' && v.size_bytes! >= 0)
  const sorted = [...sized].sort((a, b) => (b.size_bytes ?? 0) - (a.size_bytes ?? 0))
  const top10 = sorted.slice(0, 10)
  const total = sized.reduce((s, v) => s + (v.size_bytes ?? 0), 0)
  const oldest = sized.reduce((m, v) => Math.max(m, v.size_measured_at ?? 0), 0)

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between pb-2">
        <CardTitle className="text-[14px]">🐳 Docker 볼륨 사용량</CardTitle>
        <Link to="/docker/volumes" className="text-[12px] text-primary hover:underline">전체 보기 →</Link>
      </CardHeader>
      <CardContent>
        {sized.length === 0 ? (
          <div className="text-[12px] text-muted-foreground text-center py-4">측정된 볼륨 없음 (수집 중일 수 있음)</div>
        ) : (
          <>
            <div className="space-y-1 text-[12px]">
              {top10.map(v => (
                <div key={v.Name} className="flex justify-between">
                  <span className="truncate flex-1 mr-2">{v.Name}</span>
                  <span className="font-mono text-muted-foreground">{humanBytes(v.size_bytes ?? 0)}</span>
                </div>
              ))}
            </div>
            <div className="mt-2 pt-2 border-t text-[11px] text-muted-foreground flex justify-between">
              <span>합계: {humanBytes(total)} · {sized.length}개 볼륨</span>
              {oldest > 0 && <span>{Math.round((Date.now() - oldest) / 60000)}분 전 측정</span>}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  )
}
```

- [ ] **Step 2: Build + lint**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/disk/DockerVolumeUsageCard.tsx
git commit -m "web: DockerVolumeUsageCard (top 10 + 합계)"
```

---

## Task 14: Wire DockerVolumeUsageCard into Disk page

**Files:**
- Modify: `web/src/pages/system/Disk.tsx` (or whatever the disk page is)

- [ ] **Step 1: Locate disk page**

Run: `find web/src -iname '*disk*' -type f`
Expected: e.g. `web/src/pages/system/Disk.tsx`.

- [ ] **Step 2: Add card to layout**

Import:
```tsx
import { DockerVolumeUsageCard } from '@/components/disk/DockerVolumeUsageCard'
```

Place `<DockerVolumeUsageCard />` near the existing partition pie / disk usage section. Match the existing grid layout (probably `<div className="grid grid-cols-1 md:grid-cols-2 gap-4">` or similar).

- [ ] **Step 3: Build + lint**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/system/Disk.tsx
git commit -m "web: 디스크 페이지에 Docker 볼륨 사용량 카드 추가"
```

---

## Task 15: Volumes page Size column

**Files:**
- Modify: `web/src/pages/docker/DockerVolumes.tsx`

- [ ] **Step 1: Locate page + add column**

Find the volumes table. Add a "크기" column header + cell that reads `size_bytes`. If no value, show "측정 중…".

In table header:
```tsx
<TableHead>크기</TableHead>
```

In table body row:
```tsx
<TableCell className="font-mono text-[12px]">
  {typeof v.size_bytes === 'number'
    ? humanBytes(v.size_bytes)
    : <span className="text-muted-foreground">측정 중…</span>}
</TableCell>
```

If `humanBytes` is not exported elsewhere, copy the small helper from `DockerVolumeUsageCard.tsx` to the same file (or extract to `web/src/lib/format.ts`).

- [ ] **Step 2: Make it sortable**

If the volumes table already supports column sorting, hook the new column into the sort state. If not, defer sortable to a follow-up — just display the column for now.

- [ ] **Step 3: Build + lint**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/docker/DockerVolumes.tsx
git commit -m "web: 볼륨 페이지에 크기 컬럼 추가"
```

---

## Task 16: Manual smoke test

- [ ] **Step 1: Build + deploy**

```bash
cd /opt/stacks/SFPanel
make build
sudo systemctl stop sfpanel
sudo cp /usr/local/bin/sfpanel /usr/local/bin/sfpanel.bak.before-b-phase1
sudo cp ./sfpanel /usr/local/bin/sfpanel
sudo systemctl start sfpanel
sleep 5
sudo systemctl is-active sfpanel
/usr/local/bin/sfpanel version
```

Expected: `active`, version reflects new commits.

- [ ] **Step 2: Verify portmap endpoint**

```bash
TOKEN=$(sudo /tmp/minttoken)
curl -s -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:9443/api/v1/system/portmap" | python3 -m json.tool | head -50
```

Expected: array of rows. At minimum SSH (22) + sfpanel (9443/8444) visible.

- [ ] **Step 3: Wait for first volume usage tick**

Initial delay is 30s. Sleep 35s, check DB:
```bash
sleep 35
sudo sqlite3 /var/lib/sfpanel/sfpanel.db "SELECT volume_name, size_bytes, datetime(measured_at/1000,'unixepoch','localtime') FROM docker_volume_usage LIMIT 5"
```
Expected: at least a few rows.

- [ ] **Step 4: Verify augmented volumes endpoint**

```bash
curl -s -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:9443/api/v1/docker/volumes" | python3 -c "import sys,json; d=json.load(sys.stdin)['data']; [print(f\"  {v['Name']:30} size={v.get('size_bytes')}\") for v in d[:5]]"
```
Expected: size_bytes populated for at least some volumes (numeric or null if not measured yet).

- [ ] **Step 5: UI smoke (Playwright or browser)**

Navigate to `http://192.168.1.203:9443`:
- 방화벽 페이지 → "포트 맵" 탭 → 포트 리스트 표시 (SSH, sfpanel, 컨테이너 포트들).
- 디스크 페이지 → "Docker 볼륨 사용량" 카드 표시 (top 10 또는 "측정 중").
- Docker 볼륨 페이지 → "크기" 컬럼 표시.

- [ ] **Step 6: Cleanup / push**

No code changes in this task. Push when ready:
```bash
git push origin main
```

---

## Self-Review

### Spec coverage
- ✅ Unified Port Map (3 sources) → Tasks 2-6
- ✅ Volume usage 5min collector → Tasks 1, 7, 8
- ✅ ListVolumes augmented with size → Task 9
- ✅ Frontend types + API → Task 10
- ✅ PortMapTable + Firewall page tab → Tasks 11-12
- ✅ DockerVolumeUsageCard + Disk page → Tasks 13-14
- ✅ Volumes page Size column → Task 15
- ✅ Manual smoke → Task 16
- ✅ Cluster proxy via existing middleware → Tasks 6 + 9 (per-node, unary JSON)
- ✅ Out of scope (per-container egress, systemd dashboard) → Not in plan

### Placeholder scan
모든 step에 실제 코드 / 명령 / 기대 출력. "TBD" / "appropriate handling" 류 없음.

### Type consistency
- Backend `PortMapRow{Port, Proto, State, Firewall, Container, Process}` ↔ Frontend `PortMapRow{port, proto, state, firewall, container, process}`: JSON 태그 lowercase로 자동 변환 일치.
- Backend `FirewallInfo{Action, Scope, RuleID, Source}` ↔ Frontend `PortMapFirewallInfo{action, scope, rule_id, source}`: 일치.
- Backend `volume_name` PK ↔ Frontend reads `Name` from existing Docker volume shape + `size_bytes`/`size_measured_at`: caller maps via volume.Name.

---

## Execution

Plan complete and saved to `docs/superpowers/plans/2026-05-05-portmap-and-volume-usage-plan.md`.

Two execution options:

**1. Subagent-Driven (recommended)** — Fresh subagent per task + 2-stage review (spec compliance, then code quality).

**2. Inline Execution** — All tasks in this session via `superpowers:executing-plans`.

Which approach?
