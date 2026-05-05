# AppStore Template Forks (Theme E Phase 1) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship template forks — operators save a running stack as a personal template (cluster-replicated), then reinstall it from a new "내 Templates" tab in AppStore. No DB migration; storage is the cluster Raft FSM.

**Architecture:** Three new `CommandType` constants on the existing FSM (`CmdForkCreate`, `CmdForkUpdate`, `CmdForkDelete`) plus a `Forks` map field on `ClusterState`. New `internal/feature/appstore/fork*.go` files for extract logic + handlers. Existing `GetApp` falls back to fork lookup when an `id` starts with `fork-` so the install path is reused without modification. Frontend adds a 3-tab AppStore page (Marketplace / 내 Templates / 설치됨) and a "Template으로 저장" button to the stack detail drawer.

**Tech Stack:** Go 1.25 + `gopkg.in/yaml.v3` + `hashicorp/raft`, React 19 + TypeScript, shadcn/ui (`Tabs`, `Dialog`, `Card`, `DropdownMenu`).

**Spec reference:** `docs/superpowers/specs/2026-05-05-template-forks-design.md`

---

## File structure

| File | Responsibility |
|---|---|
| `internal/cluster/types.go` (mod) | Add `Forks map[string]*ForkRecord` to `ClusterState` |
| `internal/cluster/raft_fsm.go` (mod) | 3 new `CommandType` consts + Apply branches + snapshot/restore (already JSON serializes whole state, no extra work) |
| `internal/cluster/raft_fsm_test.go` (mod) | Apply tests for fork commands + snapshot round-trip |
| `internal/cluster/manager.go` (mod) | `CreateFork / UpdateFork / DeleteFork` helpers wrapping `raft.Apply` |
| `internal/feature/appstore/fork_types.go` (new) | `ForkRecord`, `UserForkInput` types |
| `internal/feature/appstore/fork_extract.go` (new) | `ExtractForkMeta` pure function (compose YAML + env values → AppStoreMeta + Compose) |
| `internal/feature/appstore/fork_extract_test.go` (new) | Table tests for env shape variants (map / list / both / empty) |
| `internal/feature/appstore/fork_handler.go` (new) | 5 CRUD handlers (`ListForks`, `GetFork`, `CreateFork`, `UpdateFork`, `DeleteFork`) |
| `internal/feature/appstore/fork_handler_test.go` (new) | Handler validation tests (bad name / bad stack_name) |
| `internal/feature/appstore/handler.go` (mod) | `GetApp` falls back to fork store for `fork-` IDs |
| `internal/api/router.go` (mod) | Register 5 new routes |
| `web/src/types/api.ts` (mod) | `Fork`, `ForkInput` types |
| `web/src/lib/api.ts` (mod) | 5 new methods |
| `web/src/pages/AppStore.tsx` (mod) | Wrap content in 3 Tabs |
| `web/src/components/appstore/ForkList.tsx` (new) | "내 Templates" tab card grid |
| `web/src/components/appstore/InstalledList.tsx` (new) | "설치됨" tab list |
| `web/src/components/appstore/ForkCreateDialog.tsx` (new) | Dialog from stack detail "Template으로 저장" button |
| `web/src/pages/docker/DockerStacks.tsx` (mod) | Wire button + dialog |
| `web/src/pages/AppStoreForkDetail.tsx` (new) | Edit metadata page (route: `/appstore/forks/:id`) |
| `web/src/App.tsx` (mod) | Lazy-import + register `/appstore/forks/:id` route |

---

## Task 1: FSM types — `Forks` map + 3 command constants

**Files:**
- Modify: `internal/cluster/types.go`
- Modify: `internal/cluster/raft_fsm.go`

- [ ] **Step 1: Add `Forks` map to `ClusterState`**

In `internal/cluster/types.go`, modify the `ClusterState` struct (line 37) — add the new `Forks` field:

```go
type ClusterState struct {
	Name     string                   `json:"name"`
	Nodes    map[string]*Node         `json:"nodes"`
	Config   map[string]string        `json:"config"`
	Accounts map[string]*AdminAccount `json:"accounts,omitempty"`
	Forks    map[string]*ForkRecord   `json:"forks,omitempty"`
}

// ForkRecord is a user-saved AppStore template, replicated via the FSM.
// Stored as a JSON blob — the appstore package owns the schema; this is
// just opaque storage from the cluster layer's perspective.
type ForkRecord struct {
	ID          string          `json:"id"`           // "fork-<short-uuid>"
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Category    string          `json:"category"`
	Compose     string          `json:"compose"`      // verbatim YAML
	Meta        json.RawMessage `json:"meta"`         // serialized AppStoreMeta
	CreatedAt   int64           `json:"created_at"`   // unix millis
	CreatedBy   string          `json:"created_by"`
}
```

(Add `"encoding/json"` to imports of types.go if not present.)

- [ ] **Step 2: Add 3 command constants in raft_fsm.go**

In `internal/cluster/raft_fsm.go`, extend the const block at line 16:

```go
const (
	CmdAddNode       CommandType = iota + 1
	CmdRemoveNode
	CmdUpdateNode
	CmdSetConfig
	CmdDeleteConfig
	CmdSetAccount
	CmdDeleteAccount
	CmdDisband
	CmdForkCreate
	CmdForkUpdate
	CmdForkDelete
)
```

- [ ] **Step 3: Initialize `Forks` map in `NewFSM`**

Modify `NewFSM` (line 57):

```go
func NewFSM() *FSM {
	return &FSM{
		state: ClusterState{
			Nodes:    make(map[string]*Node),
			Config:   make(map[string]string),
			Accounts: make(map[string]*AdminAccount),
			Forks:    make(map[string]*ForkRecord),
		},
	}
}
```

- [ ] **Step 4: Build to verify compile**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/cluster/types.go internal/cluster/raft_fsm.go
git commit -m "cluster: ForkRecord type + 3 command constants"
```

---

## Task 2: FSM Apply branches + tests

**Files:**
- Modify: `internal/cluster/raft_fsm.go`
- Modify: `internal/cluster/raft_fsm_test.go`

- [ ] **Step 1: Write failing test**

Append to `internal/cluster/raft_fsm_test.go`:

```go
func TestFSMApply_ForkCreate(t *testing.T) {
	fsm := NewFSM()
	rec := &ForkRecord{
		ID:        "fork-abc",
		Name:      "My Stack",
		Compose:   "services:\n  web:\n    image: nginx:1\n",
		CreatedAt: 1714742400000,
	}
	val, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	cmd := Command{Type: CmdForkCreate, Value: val}
	data, _ := json.Marshal(cmd)
	if applyErr := fsm.Apply(&raft.Log{Data: data}); applyErr != nil {
		t.Fatalf("apply: %v", applyErr)
	}
	got := fsm.GetState().Forks["fork-abc"]
	if got == nil {
		t.Fatal("expected fork in state")
	}
	if got.Name != "My Stack" {
		t.Errorf("name: got %q want %q", got.Name, "My Stack")
	}
}

func TestFSMApply_ForkDelete(t *testing.T) {
	fsm := NewFSM()
	fsm.state.Forks["fork-x"] = &ForkRecord{ID: "fork-x", Name: "x"}
	cmd := Command{Type: CmdForkDelete, Key: "fork-x"}
	data, _ := json.Marshal(cmd)
	if applyErr := fsm.Apply(&raft.Log{Data: data}); applyErr != nil {
		t.Fatalf("apply: %v", applyErr)
	}
	if _, ok := fsm.GetState().Forks["fork-x"]; ok {
		t.Fatal("expected fork removed")
	}
}

func TestFSMApply_ForkUpdate_MetadataOnly(t *testing.T) {
	fsm := NewFSM()
	fsm.state.Forks["fork-x"] = &ForkRecord{
		ID:          "fork-x",
		Name:        "old",
		Description: "old desc",
		Category:    "old cat",
		Compose:     "services: {}",
	}
	patch := &ForkRecord{
		ID:          "fork-x",
		Name:        "new",
		Description: "new desc",
		Category:    "new cat",
		// Compose intentionally empty in patch — must NOT overwrite existing.
	}
	val, _ := json.Marshal(patch)
	cmd := Command{Type: CmdForkUpdate, Key: "fork-x", Value: val}
	data, _ := json.Marshal(cmd)
	if err := fsm.Apply(&raft.Log{Data: data}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := fsm.GetState().Forks["fork-x"]
	if got.Name != "new" || got.Description != "new desc" || got.Category != "new cat" {
		t.Errorf("metadata not updated: %+v", got)
	}
	if got.Compose != "services: {}" {
		t.Errorf("compose was overwritten: %q", got.Compose)
	}
}
```

If the test file's existing imports don't include `"encoding/json"` and `"github.com/hashicorp/raft"`, add them (merge into existing block).

- [ ] **Step 2: Run, expect fails**

Run: `cd /opt/stacks/SFPanel && go test ./internal/cluster/ -run TestFSMApply_Fork -count=1`
Expected: FAIL — "unknown command type" or similar.

- [ ] **Step 3: Add Apply branches**

In `internal/cluster/raft_fsm.go`, find the `switch cmd.Type` in `Apply` (around line 85). Add three new cases (after `CmdDeleteAccount`, before `CmdDisband`):

```go
	case CmdForkCreate:
		var rec ForkRecord
		if err := json.Unmarshal(cmd.Value, &rec); err != nil {
			return err
		}
		f.state.Forks[rec.ID] = &rec
		return nil

	case CmdForkUpdate:
		var patch ForkRecord
		if err := json.Unmarshal(cmd.Value, &patch); err != nil {
			return err
		}
		existing, ok := f.state.Forks[cmd.Key]
		if !ok {
			return fmt.Errorf("fork not found: %s", cmd.Key)
		}
		// Metadata-only update: never overwrite Compose / Meta / CreatedAt / CreatedBy / ID.
		if patch.Name != "" {
			existing.Name = patch.Name
		}
		// Description and Category may be intentionally cleared, so apply unconditionally.
		existing.Description = patch.Description
		existing.Category = patch.Category
		return nil

	case CmdForkDelete:
		delete(f.state.Forks, cmd.Key)
		return nil
```

- [ ] **Step 4: Run tests, expect 3 PASS**

Run: `cd /opt/stacks/SFPanel && go test ./internal/cluster/ -run TestFSMApply_Fork -count=1 -v`
Expected: 3 PASS.

- [ ] **Step 5: Run full cluster test package + lint**

Run: `cd /opt/stacks/SFPanel && go test ./internal/cluster/ -count=1 && /home/dalso/go/bin/golangci-lint run ./internal/cluster/...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/cluster/raft_fsm.go internal/cluster/raft_fsm_test.go
git commit -m "cluster: FSM apply for fork create/update/delete"
```

---

## Task 3: FSM snapshot round-trip with forks

**Files:**
- Modify: `internal/cluster/raft_fsm_test.go`

- [ ] **Step 1: Write failing test**

Append to `internal/cluster/raft_fsm_test.go`:

```go
import (
	"bytes"
)

// fakeSink discards Cancel/Close + collects bytes written (raft.SnapshotSink).
type fakeSink struct {
	buf bytes.Buffer
	id  string
}

func (s *fakeSink) Write(p []byte) (int, error) { return s.buf.Write(p) }
func (s *fakeSink) Close() error                { return nil }
func (s *fakeSink) ID() string                  { return s.id }
func (s *fakeSink) Cancel() error               { return nil }

func TestFSM_SnapshotRestore_PreservesForks(t *testing.T) {
	fsm := NewFSM()
	fsm.state.Forks["fork-a"] = &ForkRecord{ID: "fork-a", Name: "A", Compose: "services: a"}
	fsm.state.Forks["fork-b"] = &ForkRecord{ID: "fork-b", Name: "B", Compose: "services: b"}

	snap, err := fsm.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	sink := &fakeSink{}
	if err := snap.Persist(sink); err != nil {
		t.Fatal(err)
	}

	// Restore into a fresh FSM and confirm both forks survive.
	other := NewFSM()
	if err := other.Restore(io.NopCloser(bytes.NewReader(sink.buf.Bytes()))); err != nil {
		t.Fatal(err)
	}
	got := other.GetState()
	if len(got.Forks) != 2 {
		t.Fatalf("forks: got %d want 2", len(got.Forks))
	}
	if got.Forks["fork-a"].Name != "A" || got.Forks["fork-b"].Name != "B" {
		t.Errorf("names mismatched: %+v", got.Forks)
	}
}
```

(Make sure `"io"` and `"bytes"` are in the test file's import block.)

- [ ] **Step 2: Run, expect PASS** (Snapshot/Restore already JSON-encodes the entire state struct, so adding a new field works without code changes — this test confirms that.)

Run: `cd /opt/stacks/SFPanel && go test ./internal/cluster/ -run TestFSM_SnapshotRestore_PreservesForks -count=1 -v`
Expected: PASS.

If the test FAILS, check `Snapshot()` and `Restore()` implementations in `raft_fsm.go` — likely they marshal/unmarshal the full `state` via `json.Marshal(f.state)` already, so the new `Forks` field travels along. If the snapshot format is hand-rolled per-field (unlikely given the simple `state` struct), update both methods to include forks.

- [ ] **Step 3: Commit**

```bash
git add internal/cluster/raft_fsm_test.go
git commit -m "cluster: FSM snapshot test covers forks (regression guard)"
```

---

## Task 4: Manager helpers — `CreateFork / UpdateFork / DeleteFork`

**Files:**
- Modify: `internal/cluster/manager.go`

- [ ] **Step 1: Add 3 helper methods**

Append at the bottom of `internal/cluster/manager.go` (after `Disband` or other config helpers — match existing style):

```go
// CreateFork applies a CmdForkCreate via Raft. Caller serializes the
// ForkRecord and provides it as cmd.Value via this helper.
func (m *Manager) CreateFork(rec *ForkRecord, timeout time.Duration) error {
	if m.raft == nil {
		return fmt.Errorf("raft not initialized")
	}
	val, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal fork: %w", err)
	}
	return m.raft.Apply(Command{Type: CmdForkCreate, Value: val}, timeout)
}

// UpdateFork applies a CmdForkUpdate (metadata-only) via Raft.
func (m *Manager) UpdateFork(id string, patch *ForkRecord, timeout time.Duration) error {
	if m.raft == nil {
		return fmt.Errorf("raft not initialized")
	}
	val, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal fork patch: %w", err)
	}
	return m.raft.Apply(Command{Type: CmdForkUpdate, Key: id, Value: val}, timeout)
}

// DeleteFork applies a CmdForkDelete via Raft.
func (m *Manager) DeleteFork(id string, timeout time.Duration) error {
	if m.raft == nil {
		return fmt.Errorf("raft not initialized")
	}
	return m.raft.Apply(Command{Type: CmdForkDelete, Key: id}, timeout)
}

// GetFork returns a copy of the named fork from FSM state (read-only,
// safe on followers). Returns nil if not found.
func (m *Manager) GetFork(id string) *ForkRecord {
	if m.raft == nil {
		return nil
	}
	state := m.raft.GetFSM().GetState()
	rec, ok := state.Forks[id]
	if !ok {
		return nil
	}
	cp := *rec
	return &cp
}

// ListForks returns a snapshot copy of all forks in FSM state.
func (m *Manager) ListForks() []*ForkRecord {
	if m.raft == nil {
		return nil
	}
	state := m.raft.GetFSM().GetState()
	out := make([]*ForkRecord, 0, len(state.Forks))
	for _, rec := range state.Forks {
		cp := *rec
		out = append(out, &cp)
	}
	return out
}
```

If `"encoding/json"` is not in `manager.go` imports, add it.

- [ ] **Step 2: Build + lint**

Run: `cd /opt/stacks/SFPanel && go build ./... && /home/dalso/go/bin/golangci-lint run ./internal/cluster/...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/cluster/manager.go
git commit -m "cluster: Manager helpers for fork CRUD"
```

---

## Task 5: Fork extraction — `ExtractForkMeta` pure function

**Files:**
- Create: `internal/feature/appstore/fork_types.go`
- Create: `internal/feature/appstore/fork_extract.go`
- Create: `internal/feature/appstore/fork_extract_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/feature/appstore/fork_extract_test.go`:

```go
package appstore

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractForkMeta_EnvFromMapForm(t *testing.T) {
	composeYAML := `services:
  web:
    image: nginx:1.25
    environment:
      LOG_LEVEL: info
      TZ: Asia/Seoul
`
	envValues := map[string]string{
		"LOG_LEVEL": "debug",
		"TZ":        "UTC",
	}
	user := UserForkInput{Name: "My Stack", Description: "test", Category: "테스트"}
	meta, compose := ExtractForkMeta("my-stack", composeYAML, envValues, user)

	require.NotEmpty(t, meta.ID)
	require.Contains(t, meta.ID, "fork-")
	require.Equal(t, "My Stack", meta.Name)
	require.Equal(t, "테스트", meta.Category)
	require.Equal(t, "1.0.0", meta.Version)
	require.Len(t, meta.Env, 2)
	envByKey := map[string]AppStoreEnvDef{}
	for _, e := range meta.Env {
		envByKey[e.Key] = e
	}
	// Env values come from envValues (current runtime values), not the YAML defaults.
	require.Equal(t, "debug", envByKey["LOG_LEVEL"].Default)
	require.Equal(t, "UTC", envByKey["TZ"].Default)
	require.Equal(t, composeYAML, compose)
}

func TestExtractForkMeta_EnvFromListForm(t *testing.T) {
	composeYAML := `services:
  app:
    image: app:1
    environment:
      - LOG_LEVEL=info
      - DEBUG=false
`
	envValues := map[string]string{
		"LOG_LEVEL": "info",
		"DEBUG":     "false",
	}
	meta, _ := ExtractForkMeta("app", composeYAML, envValues, UserForkInput{Name: "App"})
	require.Len(t, meta.Env, 2)
}

func TestExtractForkMeta_DefaultCategory(t *testing.T) {
	meta, _ := ExtractForkMeta("x", "services: {}", nil, UserForkInput{Name: "X"})
	require.Equal(t, "내 Templates", meta.Category)
}

func TestExtractForkMeta_NoEnvSection(t *testing.T) {
	composeYAML := `services:
  web:
    image: nginx
`
	meta, _ := ExtractForkMeta("y", composeYAML, nil, UserForkInput{Name: "Y"})
	require.Empty(t, meta.Env)
}

func TestExtractForkMeta_IDStableShape(t *testing.T) {
	meta, _ := ExtractForkMeta("z", "services: {}", nil, UserForkInput{Name: "Z"})
	require.Regexp(t, `^fork-[a-f0-9]{8}$`, meta.ID)
}
```

- [ ] **Step 2: Run, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/appstore/ -run TestExtractForkMeta -count=1`
Expected: FAIL — `undefined: ExtractForkMeta`, `undefined: UserForkInput`.

- [ ] **Step 3: Create types**

Create `internal/feature/appstore/fork_types.go`:

```go
package appstore

// UserForkInput captures the metadata fields the operator supplies in
// the "Template으로 저장" dialog. Compose YAML and env values are
// extracted by the server, not provided here.
type UserForkInput struct {
	Name        string
	Description string
	Category    string // empty → defaults to "내 Templates"
}
```

- [ ] **Step 4: Implement ExtractForkMeta**

Create `internal/feature/appstore/fork_extract.go`:

```go
package appstore

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const defaultForkCategory = "내 Templates"

// ExtractForkMeta builds an AppStoreMeta + compose YAML pair for a fork
// from a running stack's deployed compose YAML and current env values.
// Pure function — no I/O. Safe for unit tests.
func ExtractForkMeta(stackName, composeYAML string, envValues map[string]string, user UserForkInput) (AppStoreMeta, string) {
	cat := user.Category
	if cat == "" {
		cat = defaultForkCategory
	}
	meta := AppStoreMeta{
		ID:          "fork-" + shortID(),
		Name:        user.Name,
		Description: map[string]string{"ko": user.Description, "en": user.Description},
		Category:    cat,
		Version:     "1.0.0",
		Source:      "fork:" + stackName,
		Env:         extractEnvDefs(composeYAML, envValues),
	}
	return meta, composeYAML
}

// extractEnvDefs walks services.<svc>.environment and produces one
// AppStoreEnvDef per unique env key. Default value comes from the
// runtime envValues map (so the fork captures the current state, not
// the YAML's hardcoded default which may be a placeholder like
// `${VAR:-fallback}`).
func extractEnvDefs(composeYAML string, envValues map[string]string) []AppStoreEnvDef {
	var doc struct {
		Services map[string]struct {
			Environment any `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(composeYAML), &doc); err != nil {
		return nil
	}
	keys := map[string]bool{}
	for _, svc := range doc.Services {
		switch env := svc.Environment.(type) {
		case map[string]any:
			for k := range env {
				keys[k] = true
			}
		case []any:
			for _, item := range env {
				if s, ok := item.(string); ok {
					if eq := strings.Index(s, "="); eq > 0 {
						keys[s[:eq]] = true
					}
				}
			}
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sortedKeys := make([]string, 0, len(keys))
	for k := range keys {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)
	out := make([]AppStoreEnvDef, 0, len(sortedKeys))
	for _, k := range sortedKeys {
		out = append(out, AppStoreEnvDef{
			Key:     k,
			Label:   map[string]string{"ko": k, "en": k},
			Type:    "string",
			Default: envValues[k],
		})
	}
	return out
}

// shortID returns 8 hex characters from crypto/rand.
func shortID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
```

- [ ] **Step 5: Run, expect 5 PASS**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/appstore/ -run TestExtractForkMeta -count=1 -v`
Expected: 5 PASS.

- [ ] **Step 6: Lint**

Run: `/home/dalso/go/bin/golangci-lint run ./internal/feature/appstore/...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/feature/appstore/fork_types.go internal/feature/appstore/fork_extract.go internal/feature/appstore/fork_extract_test.go
git commit -m "appstore: ExtractForkMeta pure function (env def derivation)"
```

---

## Task 6: Fork CRUD handlers

**Files:**
- Create: `internal/feature/appstore/fork_handler.go`
- Create: `internal/feature/appstore/fork_handler_test.go`

- [ ] **Step 1: Write failing handler test**

Create `internal/feature/appstore/fork_handler_test.go`:

```go
package appstore

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

// TestCreateFork_RejectsMissingStackName: validation runs before any
// Cluster / Compose lookup so we can drive it with bare Handler{}.
func TestCreateFork_RejectsMissingStackName(t *testing.T) {
	body := bytes.NewBufferString(`{"name": "x"}`) // stack_name missing
	req := httptest.NewRequest(http.MethodPost, "/api/v1/appstore/forks", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	h := &Handler{}
	_ = h.CreateFork(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, false, resp["success"])
}

func TestCreateFork_RejectsBadName(t *testing.T) {
	body := bytes.NewBufferString(`{"stack_name": "s", "name": ""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/appstore/forks", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)

	h := &Handler{}
	_ = h.CreateFork(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}
```

- [ ] **Step 2: Run, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/appstore/ -run TestCreateFork -count=1`
Expected: FAIL — `undefined: (*Handler).CreateFork`.

- [ ] **Step 3: Inspect existing Handler struct**

Run: `grep -nE 'type Handler struct' internal/feature/appstore/handler.go`
Note the field names — `Handler` already has `DB`, `ClusterMgr` (or similar) fields. Adapt the new methods to use whatever the existing names are. Do NOT add new fields.

- [ ] **Step 4: Implement handlers**

Create `internal/feature/appstore/fork_handler.go`:

```go
package appstore

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
)

const forkApplyTimeout = 5 * time.Second

type createForkRequest struct {
	StackName   string `json:"stack_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
}

type updateForkRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Category    *string `json:"category,omitempty"`
}

// ListForks returns all forks across the cluster. Reads from the local FSM
// (replicated, sub-second lag).
func (h *Handler) ListForks(c echo.Context) error {
	if h.ClusterMgr == nil {
		return response.OK(c, []*cluster.ForkRecord{})
	}
	return response.OK(c, h.ClusterMgr.ListForks())
}

// GetFork returns the fork by id (cluster-wide, read from local FSM).
func (h *Handler) GetFork(c echo.Context) error {
	id := c.Param("id")
	if h.ClusterMgr == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "cluster not initialized")
	}
	rec := h.ClusterMgr.GetFork(id)
	if rec == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "fork not found")
	}
	return response.OK(c, rec)
}

// CreateFork extracts compose + env from a running stack and creates a
// fork record via Raft.
func (h *Handler) CreateFork(c echo.Context) error {
	var req createForkRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "invalid request body")
	}
	req.Name = strings.TrimSpace(req.Name)
	req.StackName = strings.TrimSpace(req.StackName)
	if req.StackName == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "stack_name required")
	}
	if req.Name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "name required")
	}
	if len(req.Name) > 100 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "name too long (max 100)")
	}
	if h.ClusterMgr == nil || h.Compose == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "cluster or compose not configured")
	}

	ctx := c.Request().Context()
	composeYAML, _, err := h.Compose.GetProjectYAML(ctx, req.StackName)
	if err != nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, response.SanitizeOutput(err.Error()))
	}
	envContent, err := h.Compose.GetProjectEnv(ctx, req.StackName)
	if err != nil {
		// Missing .env is fine — fork without runtime values.
		envContent = ""
	}
	envValues := parseEnvFile(envContent)

	meta, compose := ExtractForkMeta(req.StackName, composeYAML, envValues, UserForkInput{
		Name: req.Name, Description: req.Description, Category: req.Category,
	})
	metaJSON, _ := json.Marshal(meta)
	rec := &cluster.ForkRecord{
		ID:          meta.ID,
		Name:        req.Name,
		Description: req.Description,
		Category:    meta.Category,
		Compose:     compose,
		Meta:        metaJSON,
		CreatedAt:   time.Now().UnixMilli(),
		CreatedBy:   getUsernameFromContext(c),
	}
	if err := h.ClusterMgr.CreateFork(rec, forkApplyTimeout); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"id": rec.ID})
}

// UpdateFork patches metadata (name/description/category). YAML immutable.
func (h *Handler) UpdateFork(c echo.Context) error {
	id := c.Param("id")
	var req updateForkRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "invalid request body")
	}
	if h.ClusterMgr == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "cluster not initialized")
	}
	existing := h.ClusterMgr.GetFork(id)
	if existing == nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "fork not found")
	}
	patch := *existing
	if req.Name != nil {
		patch.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		patch.Description = *req.Description
	}
	if req.Category != nil {
		patch.Category = *req.Category
	}
	if err := h.ClusterMgr.UpdateFork(id, &patch, forkApplyTimeout); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"id": id})
}

// DeleteFork removes a fork.
func (h *Handler) DeleteFork(c echo.Context) error {
	id := c.Param("id")
	if h.ClusterMgr == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "cluster not initialized")
	}
	if err := h.ClusterMgr.DeleteFork(id, forkApplyTimeout); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"id": id})
}

// parseEnvFile parses KEY=VALUE lines into a map. Comment lines (#) and
// blank lines skipped. Used to lift current runtime env values into fork
// metadata at create time.
func parseEnvFile(content string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		out[strings.TrimSpace(line[:eq])] = strings.TrimSpace(line[eq+1:])
	}
	return out
}

// getUsernameFromContext extracts the authenticated user from the JWT
// middleware. Returns "" if no claims present (e.g. tests).
func getUsernameFromContext(c echo.Context) string {
	claims := c.Get("claims")
	if claims == nil {
		return ""
	}
	if m, ok := claims.(map[string]any); ok {
		if u, ok := m["username"].(string); ok {
			return u
		}
	}
	return ""
}
```

- [ ] **Step 5: Verify Handler struct has needed fields**

Run: `grep -nE 'type Handler struct' -A 10 internal/feature/appstore/handler.go`. Confirm `ClusterMgr` and `Compose` fields exist (or equivalent names). If they don't exist:
- `ClusterMgr *cluster.Manager` — add to Handler struct.
- `Compose *docker.ComposeManager` — add.

The router (Task 7) wires these.

- [ ] **Step 6: Run handler tests + build**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/appstore/ -run TestCreateFork -count=1 -v && go build ./...`
Expected: 2 PASS, build clean.

- [ ] **Step 7: Lint**

Run: `/home/dalso/go/bin/golangci-lint run ./internal/feature/appstore/...`
Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add internal/feature/appstore/fork_handler.go internal/feature/appstore/fork_handler_test.go internal/feature/appstore/handler.go
git commit -m "appstore: fork CRUD handlers (List/Get/Create/Update/Delete)"
```

---

## Task 7: Register fork routes + GetApp fallback to fork store

**Files:**
- Modify: `internal/api/router.go`
- Modify: `internal/feature/appstore/handler.go`

- [ ] **Step 1: Wire ClusterMgr + Compose into appStoreHandler**

In `internal/api/router.go`, find where `appStoreHandler` is constructed (search `appStoreHandler :=`). Add the missing fields:

```go
appStoreHandler := &featureAppstore.Handler{
    // ... existing fields like DB, Cmd ...
    ClusterMgr: clusterMgr,
    Compose:    composeManager,  // whatever the existing var name is
}
```

(If `composeManager` doesn't exist as a named var, search for where the docker compose handler gets its `Compose` field — reuse the same expression.)

- [ ] **Step 2: Register 5 fork routes**

In `internal/api/router.go`, find the existing appstore route block (line 186-192) and add immediately after `appStore.POST("/refresh", ...)`:

```go
appStore.GET("/forks", appStoreHandler.ListForks)
appStore.GET("/forks/:id", appStoreHandler.GetFork)
appStore.POST("/forks", appStoreHandler.CreateFork)
appStore.PATCH("/forks/:id", appStoreHandler.UpdateFork)
appStore.DELETE("/forks/:id", appStoreHandler.DeleteFork)
```

- [ ] **Step 3: GetApp fallback for `fork-` IDs**

In `internal/feature/appstore/handler.go`, find the `GetApp` handler (around line 377). Inside, where the official cache lookup fails, add the fork fallback BEFORE returning 404:

```go
// (existing) cache lookup
meta, ok := h.cache.apps[id]
if !ok {
    // Phase E: try fork store before giving up.
    if strings.HasPrefix(id, "fork-") && h.ClusterMgr != nil {
        rec := h.ClusterMgr.GetFork(id)
        if rec != nil {
            var forkMeta AppStoreMeta
            if err := json.Unmarshal(rec.Meta, &forkMeta); err == nil {
                meta = forkMeta
                ok = true
                // The detail handler also returns the compose YAML; substitute
                // the fork's stored compose. Look up where the detail response
                // builds Compose (probably from the official cache); add a
                // branch to use rec.Compose for fork-IDs.
            }
        }
    }
}
if !ok {
    return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "app not found")
}
```

The exact integration depends on the existing `GetApp` body — read it carefully. The goal is: for fork-IDs, the response object's `App` field is the fork's `AppStoreMeta` and `Compose` is the fork's stored YAML. The rest of the response (readme, port_status) can be empty for forks (no upstream readme; port checks still run since they use only meta.Ports).

If the existing handler is structured tightly around the official cache, the cleanest approach is a small wrapper that builds an `appStoreAppDetail` from a `ForkRecord` and returns it directly when the ID has the `fork-` prefix:

```go
if strings.HasPrefix(id, "fork-") {
    if rec := h.ClusterMgr.GetFork(id); rec != nil {
        var meta AppStoreMeta
        if err := json.Unmarshal(rec.Meta, &meta); err == nil {
            return response.OK(c, appStoreAppDetail{
                App:        meta,
                Compose:    rec.Compose,
                Readme:     "",
                Installed:  h.isInstalled(id),
                PortStatus: h.computePortStatus(meta.Ports), // existing helper if any
            })
        }
    }
    return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "fork not found")
}
```

If `computePortStatus` doesn't exist as a named helper, omit `PortStatus` (empty slice is fine) — it's a polish concern, not core.

Add `"encoding/json"` and `"strings"` imports if missing.

- [ ] **Step 4: Build + tests**

Run: `cd /opt/stacks/SFPanel && go build ./... && go test ./internal/feature/appstore/... ./internal/api/... -count=1`
Expected: clean.

- [ ] **Step 5: Lint**

Run: `/home/dalso/go/bin/golangci-lint run ./internal/feature/appstore/... ./internal/api/...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/api/router.go internal/feature/appstore/handler.go
git commit -m "appstore: register fork routes + GetApp fallback for fork- IDs"
```

---

## Task 8: Frontend types + API methods

**Files:**
- Modify: `web/src/types/api.ts`
- Modify: `web/src/lib/api.ts`

- [ ] **Step 1: Append Fork types**

Append to `web/src/types/api.ts`:

```typescript
export interface Fork {
  id: string
  name: string
  description: string
  category: string
  compose: string
  meta: unknown            // serialized AppStoreMeta — opaque to TS
  created_at: number       // unix millis
  created_by: string
}

export interface ForkCreateInput {
  stack_name: string
  name: string
  description?: string
  category?: string
}

export interface ForkUpdateInput {
  name?: string
  description?: string
  category?: string
}
```

- [ ] **Step 2: Add API methods**

In `web/src/lib/api.ts`, add to imports:
```typescript
import type {
  // ... existing ...
  Fork, ForkCreateInput, ForkUpdateInput,
} from '@/types/api'
```

Add methods inside the `ApiClient` class near other appstore methods:

```typescript
listForks() {
  return this.request<Fork[]>(`/appstore/forks`)
}
getFork(id: string) {
  return this.request<Fork>(`/appstore/forks/${encodeURIComponent(id)}`)
}
createFork(input: ForkCreateInput) {
  return this.request<{ id: string }>(`/appstore/forks`, {
    method: 'POST',
    body: JSON.stringify(input),
  })
}
updateFork(id: string, input: ForkUpdateInput) {
  return this.request<{ id: string }>(`/appstore/forks/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: JSON.stringify(input),
  })
}
deleteFork(id: string) {
  return this.request<{ id: string }>(`/appstore/forks/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  })
}
```

- [ ] **Step 3: Build + lint frontend**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/src/types/api.ts web/src/lib/api.ts
git commit -m "web: types + api client for AppStore forks"
```

---

## Task 9: ForkCreateDialog + wire into stack drawer

**Files:**
- Create: `web/src/components/appstore/ForkCreateDialog.tsx`
- Modify: `web/src/pages/docker/DockerStacks.tsx`

- [ ] **Step 1: Create dialog component**

Create `web/src/components/appstore/ForkCreateDialog.tsx`:

```typescript
import { useState } from 'react'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter, DialogDescription } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { api } from '@/lib/api'
import { toast } from 'sonner'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  stackName: string
  onSuccess?: (forkId: string) => void
}

export function ForkCreateDialog({ open, onOpenChange, stackName, onSuccess }: Props) {
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [category, setCategory] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!name.trim()) return
    setSubmitting(true)
    try {
      const res = await api.createFork({
        stack_name: stackName,
        name: name.trim(),
        description: description.trim(),
        category: category.trim(),
      })
      toast.success(`'${name}' 템플릿이 저장되었습니다`)
      onOpenChange(false)
      setName(''); setDescription(''); setCategory('')
      onSuccess?.(res.id)
    } catch (err) {
      toast.error((err as Error).message || '저장 실패')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Template으로 저장</DialogTitle>
          <DialogDescription>현재 stack의 compose YAML과 환경 변수가 자동으로 포함됩니다.</DialogDescription>
        </DialogHeader>
        <form onSubmit={onSubmit} className="space-y-3">
          <div>
            <Label htmlFor="fork-name">이름 *</Label>
            <Input id="fork-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="my-template" maxLength={100} required />
          </div>
          <div>
            <Label htmlFor="fork-desc">설명</Label>
            <Textarea id="fork-desc" value={description} onChange={(e) => setDescription(e.target.value)} rows={2} placeholder="짧은 한 줄 설명…" />
          </div>
          <div>
            <Label htmlFor="fork-cat">카테고리</Label>
            <Input id="fork-cat" value={category} onChange={(e) => setCategory(e.target.value)} placeholder="내 Templates" />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>취소</Button>
            <Button type="submit" disabled={submitting || !name.trim()}>{submitting ? '저장 중…' : '저장'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
```

If `Textarea` from shadcn doesn't exist: `cd web && npx shadcn@latest add textarea`.

- [ ] **Step 2: Wire button into stack drawer**

In `web/src/pages/docker/DockerStacks.tsx`, find where the existing stack action buttons render (search for the existing 시작/중지 buttons or the stack drawer footer). Add the new button + dialog state.

Add to imports near top:
```typescript
import { Save } from 'lucide-react'
import { ForkCreateDialog } from '@/components/appstore/ForkCreateDialog'
```

Add state (find existing useState block):
```typescript
const [forkOpen, setForkOpen] = useState(false)
```

Add button next to existing action buttons (the exact spot depends on the current layout; place it near "재시작" / "중지" if they exist). Use this pattern:
```tsx
<Button variant="outline" size="sm" onClick={() => setForkOpen(true)}>
  <Save className="h-3.5 w-3.5 mr-1" />
  Template으로 저장
</Button>
```

Add the dialog at the bottom of the JSX (sibling of other dialogs):
```tsx
{selectedName && (
  <ForkCreateDialog
    open={forkOpen}
    onOpenChange={setForkOpen}
    stackName={selectedName}
  />
)}
```

(`selectedName` is the existing useParams-derived stack name; confirm with grep.)

- [ ] **Step 3: Build + lint**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/appstore/ForkCreateDialog.tsx web/src/pages/docker/DockerStacks.tsx web/src/components/ui/textarea.tsx
git commit -m "web: ForkCreateDialog + Template으로 저장 button on stack drawer"
```

(Adjust git add list to whatever shadcn install actually creates.)

---

## Task 10: ForkList component (내 Templates tab body)

**Files:**
- Create: `web/src/components/appstore/ForkList.tsx`

- [ ] **Step 1: Create component**

Create `web/src/components/appstore/ForkList.tsx`:

```typescript
import { useEffect, useState, useCallback } from 'react'
import { Link } from 'react-router-dom'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { MoreVertical, Trash2, Pencil, Download } from 'lucide-react'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import type { Fork } from '@/types/api'

interface Props {
  search: string
  category: string
}

export function ForkList({ search, category }: Props) {
  const [forks, setForks] = useState<Fork[]>([])
  const [loading, setLoading] = useState(true)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const data = await api.listForks()
      setForks(data ?? [])
    } catch {
      setForks([])
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  async function onDelete(id: string, name: string) {
    if (!confirm(`'${name}' 템플릿을 삭제하시겠습니까?`)) return
    try {
      await api.deleteFork(id)
      toast.success(`'${name}' 삭제됨`)
      void load()
    } catch (err) {
      toast.error((err as Error).message || '삭제 실패')
    }
  }

  const filtered = forks.filter((f) => {
    if (category && f.category !== category) return false
    if (search) {
      const q = search.toLowerCase()
      return f.name.toLowerCase().includes(q) || f.description.toLowerCase().includes(q)
    }
    return true
  })

  if (loading) return <div className="text-muted-foreground text-[13px] py-8 text-center">불러오는 중…</div>
  if (forks.length === 0) {
    return <div className="text-muted-foreground text-[13px] py-12 text-center">아직 저장된 Template이 없습니다. Docker Stack 상세 페이지에서 "Template으로 저장"으로 만들어보세요.</div>
  }

  return (
    <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-3">
      {filtered.map((f) => (
        <Card key={f.id} className="flex flex-col">
          <CardHeader className="pb-2">
            <div className="flex items-start justify-between gap-2">
              <CardTitle className="text-[14px] truncate flex-1">📦 {f.name}</CardTitle>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="ghost" size="icon-xs" aria-label="more"><MoreVertical className="h-4 w-4" /></Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem asChild>
                    <Link to={`/appstore/forks/${f.id}`}><Pencil className="h-3.5 w-3.5 mr-1.5" />편집</Link>
                  </DropdownMenuItem>
                  <DropdownMenuItem onClick={() => onDelete(f.id, f.name)} className="text-destructive">
                    <Trash2 className="h-3.5 w-3.5 mr-1.5" />삭제
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
            <div className="text-[11px] text-muted-foreground">{f.category}</div>
          </CardHeader>
          <CardContent className="flex-1 flex flex-col">
            <p className="text-[12px] text-muted-foreground line-clamp-2 mb-3 min-h-[32px]">{f.description || ' '}</p>
            <Button asChild size="sm" className="mt-auto">
              <Link to={`/appstore/${f.id}`}>
                <Download className="h-3.5 w-3.5 mr-1" />설치
              </Link>
            </Button>
          </CardContent>
        </Card>
      ))}
    </div>
  )
}
```

If `DropdownMenu` from shadcn doesn't exist: `cd web && npx shadcn@latest add dropdown-menu`.

- [ ] **Step 2: Build + lint**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add web/src/components/appstore/ForkList.tsx web/src/components/ui/dropdown-menu.tsx
git commit -m "web: ForkList component (내 Templates 탭 본문)"
```

---

## Task 11: AppStore 3-tab restructure

**Files:**
- Modify: `web/src/pages/AppStore.tsx`

- [ ] **Step 1: Wrap content in 3 Tabs**

In `web/src/pages/AppStore.tsx`, find the main content render (the existing app grid). Wrap the existing search box + category filter + grid in a `Tabs` structure:

```tsx
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ForkList } from '@/components/appstore/ForkList'
```

Inside the page body:
```tsx
<Tabs defaultValue="marketplace">
  <TabsList>
    <TabsTrigger value="marketplace">Marketplace</TabsTrigger>
    <TabsTrigger value="forks">내 Templates</TabsTrigger>
  </TabsList>

  {/* Search + category bar (shared) */}
  <div className="my-3 flex items-center gap-2">
    <Input value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t('appStore.searchPlaceholder')} />
    {/* existing category buttons */}
  </div>

  <TabsContent value="marketplace">
    {/* Existing app grid markup */}
  </TabsContent>
  <TabsContent value="forks">
    <ForkList search={search} category={selectedCategory} />
  </TabsContent>
</Tabs>
```

Adapt the variable names (`search`, `selectedCategory`) to whatever the existing page uses. The point is:
1. Top-level Tabs with two values.
2. Search/filter row stays shared (or moves into each TabsContent — pick whichever the existing layout flows better with).
3. Marketplace content unchanged.
4. Forks tab renders `<ForkList>` with the same search/category filters.

(Spec says 3 tabs including "설치됨" — defer the "설치됨" tab unless the existing AppStore page already has it. If it has a dedicated "installed" view elsewhere, leave it for Phase 2 polish. Two tabs are the minimum to ship the core feature.)

- [ ] **Step 2: Build + lint**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/AppStore.tsx
git commit -m "web: AppStore 페이지에 \"내 Templates\" 탭 추가"
```

---

## Task 12: ForkDetail page (edit metadata)

**Files:**
- Create: `web/src/pages/AppStoreForkDetail.tsx`
- Modify: `web/src/App.tsx`

- [ ] **Step 1: Create page**

Create `web/src/pages/AppStoreForkDetail.tsx`:

```typescript
import { useEffect, useState } from 'react'
import { useParams, useNavigate, Link } from 'react-router-dom'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { ArrowLeft, Save, Trash2, Download } from 'lucide-react'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import type { Fork } from '@/types/api'

export default function AppStoreForkDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [fork, setFork] = useState<Fork | null>(null)
  const [loading, setLoading] = useState(true)
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [category, setCategory] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (!id) return
    let cancelled = false
    api.getFork(id)
      .then((f) => {
        if (cancelled) return
        setFork(f); setName(f.name); setDescription(f.description); setCategory(f.category)
      })
      .catch(() => {
        if (!cancelled) toast.error('Template을 불러올 수 없습니다.')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => { cancelled = true }
  }, [id])

  async function onSave() {
    if (!id) return
    setSaving(true)
    try {
      await api.updateFork(id, { name: name.trim(), description, category })
      toast.success('수정됨')
      const refreshed = await api.getFork(id)
      setFork(refreshed)
    } catch (err) {
      toast.error((err as Error).message || '저장 실패')
    } finally {
      setSaving(false)
    }
  }

  async function onDelete() {
    if (!id || !fork) return
    if (!confirm(`'${fork.name}' 템플릿을 삭제하시겠습니까?`)) return
    try {
      await api.deleteFork(id)
      toast.success('삭제됨')
      navigate('/appstore')
    } catch (err) {
      toast.error((err as Error).message || '삭제 실패')
    }
  }

  if (loading) return <div className="p-6 text-muted-foreground text-[13px]">불러오는 중…</div>
  if (!fork) return <div className="p-6 text-muted-foreground text-[13px]">Template을 찾을 수 없습니다.</div>

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <Button variant="ghost" size="sm" onClick={() => navigate('/appstore')}>
          <ArrowLeft className="h-3.5 w-3.5 mr-1" />뒤로
        </Button>
        <h1 className="text-[20px] font-bold tracking-tight">{fork.name}</h1>
        <span className="text-[12px] text-muted-foreground">{fork.id}</span>
      </div>

      <div className="grid gap-4 max-w-xl">
        <div>
          <Label htmlFor="f-name">이름</Label>
          <Input id="f-name" value={name} onChange={(e) => setName(e.target.value)} maxLength={100} />
        </div>
        <div>
          <Label htmlFor="f-desc">설명</Label>
          <Textarea id="f-desc" value={description} onChange={(e) => setDescription(e.target.value)} rows={3} />
        </div>
        <div>
          <Label htmlFor="f-cat">카테고리</Label>
          <Input id="f-cat" value={category} onChange={(e) => setCategory(e.target.value)} />
        </div>
      </div>

      <div className="flex items-center gap-2">
        <Button onClick={onSave} disabled={saving || !name.trim()}>
          <Save className="h-3.5 w-3.5 mr-1" />{saving ? '저장 중…' : '저장'}
        </Button>
        <Button asChild variant="outline">
          <Link to={`/appstore/${fork.id}`}><Download className="h-3.5 w-3.5 mr-1" />설치</Link>
        </Button>
        <Button onClick={onDelete} variant="destructive">
          <Trash2 className="h-3.5 w-3.5 mr-1" />삭제
        </Button>
      </div>

      <div>
        <Label>compose YAML (immutable)</Label>
        <pre className="mt-1 p-3 bg-secondary/50 rounded-md text-[11px] font-mono overflow-auto max-h-[400px]">{fork.compose}</pre>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Register route in App.tsx**

In `web/src/App.tsx`, add the lazy import (near other AppStore imports):
```typescript
const AppStoreForkDetail = lazy(() => import('@/pages/AppStoreForkDetail'))
```

Add the route inside the existing routes block (alongside `/appstore/:id`):
```tsx
<Route path="appstore/forks/:id" element={<AppStoreForkDetail />} />
```

(Place it BEFORE `/appstore/:id` so that `/appstore/forks/abc` doesn't match the more general route by accident.)

- [ ] **Step 3: Build + lint**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/AppStoreForkDetail.tsx web/src/App.tsx
git commit -m "web: AppStoreForkDetail 페이지 (메타데이터 편집/삭제)"
```

---

## Task 13: Manual smoke test on the live panel

- [ ] **Step 1: Build + deploy**

```bash
cd /opt/stacks/SFPanel
make build
sudo systemctl stop sfpanel
sudo cp /usr/local/bin/sfpanel /usr/local/bin/sfpanel.bak.before-e-phase1
sudo cp ./sfpanel /usr/local/bin/sfpanel
sudo systemctl start sfpanel
sleep 5
sudo systemctl is-active sfpanel
/usr/local/bin/sfpanel version
```

Expected: `active`, version reflects new commit.

- [ ] **Step 2: API smoke — list forks (empty)**

```bash
TOKEN=$(sudo /tmp/minttoken)
curl -s -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:9443/api/v1/appstore/forks" | python3 -m json.tool
```
Expected: `{"success": true, "data": []}` (or null → matches empty list semantics).

- [ ] **Step 3: API smoke — create fork from existing stack**

Pick an existing stack (e.g., `nginxproxyguard`):
```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d '{"stack_name":"nginxproxyguard","name":"Smoke Test Fork","description":"phase 1 verify","category":"테스트"}' \
     "http://127.0.0.1:9443/api/v1/appstore/forks" | python3 -m json.tool
```
Expected: `success: true, data: {id: "fork-..."}`. Capture the ID.

- [ ] **Step 4: Verify FSM replication**

```bash
curl -s -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:9443/api/v1/appstore/forks" | python3 -m json.tool | head -20
```
Expected: list contains the new fork.

If you have access to the second cluster node (`192.168.1.118`):
```bash
RTOK=$(curl -sk --max-time 3 -X POST -H 'Content-Type: application/json' -d '{"username":"admin-sv","password":"<PWD>"}' http://192.168.1.118:8444/api/v1/auth/login | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['token'])")
curl -s -H "Authorization: Bearer $RTOK" "http://192.168.1.118:8444/api/v1/appstore/forks" | python3 -m json.tool
```
Expected: same fork visible on the other node (FSM replication confirmed).

- [ ] **Step 5: UI smoke (Playwright or browser)**

Navigate to `http://192.168.1.203:9443/appstore`:
- 두 탭 표시: "Marketplace" / "내 Templates"
- "내 Templates" 클릭 → 카드 1개 (Smoke Test Fork) 표시
- 카드 ... 메뉴 → "편집" 클릭 → `/appstore/forks/fork-xxx` 페이지 진입
- 이름/설명/카테고리 편집 후 "저장" → toast "수정됨"
- "삭제" 버튼 → confirm → 목록으로 돌아감, 카드 사라짐

`/docker/stacks/<name>` 진입 → 에디터 탭 footer에 "Template으로 저장" 버튼 → 클릭 → 다이얼로그 → 입력 후 저장 → toast.

- [ ] **Step 6: Cleanup test fork via API**

```bash
curl -s -X DELETE -H "Authorization: Bearer $TOKEN" "http://127.0.0.1:9443/api/v1/appstore/forks/fork-<id>"
```

- [ ] **Step 7: Push**

```bash
git push origin main
```

---

## Self-Review

### Spec coverage
- ✅ Fork creation via stack drawer button → Tasks 5, 6, 9
- ✅ Cluster Raft FSM storage → Tasks 1, 2, 3, 4
- ✅ env_defs auto-extraction (map / list forms) → Task 5
- ✅ AppStoreMeta reuse for install path → Tasks 5, 7
- ✅ 5 CRUD endpoints → Tasks 6, 7
- ✅ Metadata-only edit (YAML immutable) → Tasks 2 (apply), 6 (handler), 12 (UI)
- ✅ 2-tab AppStore (Marketplace / 내 Templates) → Task 11. ("설치됨" 3rd tab deferred — spec mentioned "or repurposed"; not core)
- ✅ Fork detail edit page → Task 12
- ✅ Manual smoke → Task 13
- ✅ Cluster proxy via existing middleware → Tasks 6, 7

### Placeholder scan
모든 step에 실제 코드 / 명령 / 기대 출력. Task 7's GetApp fallback note acknowledges the existing handler may need a small adapter — that's a structural note, not a placeholder.

### Type consistency
- Backend `cluster.ForkRecord{ID, Name, Description, Category, Compose, Meta, CreatedAt, CreatedBy}` ↔ Frontend `Fork{id, name, description, category, compose, meta, created_at, created_by}`: JSON tags lowercase + snake_case; matches.
- `UserForkInput{Name, Description, Category}` ↔ `ForkCreateInput{name, description?, category?, stack_name}` (frontend adds stack_name to wrap the request).
- `CmdForkCreate / Update / Delete` constants used consistently in Tasks 1-4.

---

## Execution

Plan complete and saved to `docs/superpowers/plans/2026-05-05-template-forks-plan.md`.

Two execution options:

**1. Subagent-Driven (recommended)** — Fresh subagent per task + 2-stage review.

**2. Inline Execution** — All tasks in this session via `superpowers:executing-plans`.

Which approach?
