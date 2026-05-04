# Compose Diff Preview + Git Import — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship Mode 1 (semantic diff before apply) and Mode 2 (one-shot Git import) for Docker Compose stacks. No DB schema changes; no PAT persistence; UI lives inside existing stack drawer.

**Architecture:** Two new pure-function-flavored backend pieces (`diff.go` for YAML semantic diff, `git_import.go` for shallow clone + read + create stack via existing `Compose.CreateProject`). Two new endpoints under existing `/api/v1/compose/...` group. Five new React components rendered inside existing `DockerStacks.tsx` page (Right Sheet for diff, single-form dialog tab for import). Cluster `?node=` proxy works automatically — both endpoints are unary JSON.

**Tech Stack:** Go 1.25 (`gopkg.in/yaml.v3`, `github.com/go-git/go-git/v5`), React 19 + TypeScript, `@monaco-editor/react` (already a dep — gives us `DiffEditor` for the raw text tab), shadcn/ui (`Sheet`, `Accordion`, `Tabs`, `Dialog`, `Button`, `Input`, `Label`, `Skeleton`), lucide-react icons.

**Spec reference:** `docs/superpowers/specs/2026-05-04-compose-diff-and-git-import-design.md`

---

## File structure

| File | Responsibility |
|---|---|
| `internal/feature/compose/diff.go` (new) | Pure function `ComputeDiff(deployedYAML, proposedYAML string) (*DiffResult, error)` over 6 categories |
| `internal/feature/compose/diff_test.go` (new) | Table tests covering each category + service add/remove + malformed YAML |
| `internal/feature/compose/git_import.go` (new) | `ImportFromGit(req ImportRequest)` shallow-clone + read compose + create stack |
| `internal/feature/compose/git_import_test.go` (new) | go-git in-memory bare repo fixture for happy path + error paths |
| `internal/feature/compose/handler.go` (mod) | Add `DiffStack` + `ImportFromGit` echo handlers |
| `internal/feature/compose/handler_test.go` (new) | Handler tests with httptest |
| `internal/api/router.go` (mod) | Register 2 new compose routes |
| `internal/api/response/errors.go` (mod) | Add `ErrGitAuthFailed`, `ErrGitRepoNotFound`, `ErrGitPathNotFound`, `ErrInvalidYAML`, `ErrStackAlreadyExists` |
| `go.mod` / `go.sum` (mod) | Add `github.com/go-git/go-git/v5` |
| `web/src/types/api.ts` (mod) | `DiffResult`, `DiffSummary`, `DiffCategoryChange`, `ImportRequest` types |
| `web/src/lib/api.ts` (mod) | `diffStack(name, yaml)`, `importFromGit(req)` |
| `web/src/components/compose/DiffSummaryHeader.tsx` (new) | 추가/변경/삭제 counter row |
| `web/src/components/compose/DiffServiceRow.tsx` (new) | One-line per-service change render |
| `web/src/components/compose/DiffCategoryList.tsx` (new) | Accordion list of 6 categories |
| `web/src/components/compose/DiffSheet.tsx` (new) | shadcn `Sheet` + Tabs (카테고리/원본 텍스트) |
| `web/src/components/compose/GitImportForm.tsx` (new) | Single-form import tab content |
| `web/src/pages/docker/DockerStacks.tsx` (mod) | "변경사항 미리보기" button + DiffSheet trigger; "git에서 가져오기" tab in 새 스택 dialog |

---

## Task 1: Diff types + first failing test (image category)

**Files:**
- Create: `internal/feature/compose/diff.go`
- Create: `internal/feature/compose/diff_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/feature/compose/diff_test.go`:

```go
package compose

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComputeDiff_ImageChange(t *testing.T) {
	deployed := `services:
  web:
    image: nginx:1.24
    ports:
      - "8080:80"
`
	proposed := `services:
  web:
    image: nginx:1.25
    ports:
      - "8080:80"
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)

	require.Equal(t, 0, got.Summary.Added)
	require.Equal(t, 1, got.Summary.Modified)
	require.Equal(t, 0, got.Summary.Removed)

	images, ok := got.ByCategory["image"].([]ImageChange)
	require.True(t, ok, "ByCategory[image] should be []ImageChange")
	require.Len(t, images, 1)
	require.Equal(t, "web", images[0].Service)
	require.Equal(t, "nginx:1.24", images[0].From)
	require.Equal(t, "nginx:1.25", images[0].To)
}
```

- [ ] **Step 2: Run test, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestComputeDiff -count=1`
Expected: FAIL — `undefined: ComputeDiff`, `undefined: ImageChange`.

- [ ] **Step 3: Create skeleton diff.go**

Create `internal/feature/compose/diff.go`:

```go
package compose

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// DiffSummary counts service-level changes.
type DiffSummary struct {
	Added    int `json:"added"`
	Modified int `json:"modified"`
	Removed  int `json:"removed"`
}

// ImageChange describes an image:tag change for one service.
type ImageChange struct {
	Service string `json:"service"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// PortChange / VolumeChange / EnvChange share the added/removed shape.
type SetChange struct {
	Service string   `json:"service"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
}

// ScalarChange covers restart policy and similar string fields.
type ScalarChange struct {
	Service string `json:"service"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// HealthcheckChange flags any difference in the healthcheck block.
// The actual block content is rendered raw (the frontend pretty-prints).
type HealthcheckChange struct {
	Service string `json:"service"`
	From    string `json:"from"` // "" if absent
	To      string `json:"to"`   // "" if absent
}

// DiffResult is the full payload returned to the frontend.
type DiffResult struct {
	Summary    DiffSummary    `json:"summary"`
	ByCategory map[string]any `json:"by_category"`
	RawDiff    string         `json:"raw_diff"`
}

type composeDoc struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Image       string         `yaml:"image"`
	Ports       []any          `yaml:"ports"`
	Volumes     []any          `yaml:"volumes"`
	Environment any            `yaml:"environment"` // map or list
	Restart     string         `yaml:"restart"`
	Healthcheck map[string]any `yaml:"healthcheck"`
}

// ComputeDiff returns a categorized diff between two compose YAML strings.
func ComputeDiff(deployedYAML, proposedYAML string) (*DiffResult, error) {
	var deployed, proposed composeDoc
	if err := yaml.Unmarshal([]byte(deployedYAML), &deployed); err != nil {
		return nil, fmt.Errorf("parse deployed yaml: %w", err)
	}
	if err := yaml.Unmarshal([]byte(proposedYAML), &proposed); err != nil {
		return nil, fmt.Errorf("parse proposed yaml: %w", err)
	}

	res := &DiffResult{
		ByCategory: map[string]any{
			"image":       []ImageChange{},
			"ports":       []SetChange{},
			"volumes":     []SetChange{},
			"env":         []SetChange{},
			"restart":     []ScalarChange{},
			"healthcheck": []HealthcheckChange{},
		},
	}

	// Image diff
	imgs := []ImageChange{}
	for name, p := range proposed.Services {
		d, existed := deployed.Services[name]
		if existed && d.Image != p.Image {
			imgs = append(imgs, ImageChange{Service: name, From: d.Image, To: p.Image})
		}
	}
	if len(imgs) > 0 {
		res.ByCategory["image"] = imgs
	}

	// Service-level summary (only counts services that exist in both with at least one change)
	for name, p := range proposed.Services {
		d, existed := deployed.Services[name]
		if !existed {
			continue
		}
		if d.Image != p.Image {
			res.Summary.Modified++
		}
	}

	return res, nil
}
```

- [ ] **Step 4: Run test, expect PASS**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestComputeDiff -count=1 -v`
Expected: 1 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/feature/compose/diff.go internal/feature/compose/diff_test.go
git commit -m "compose: ComputeDiff scaffold with image category"
```

---

## Task 2: Ports / Volumes / Env set-diff

**Files:**
- Modify: `internal/feature/compose/diff.go`
- Modify: `internal/feature/compose/diff_test.go`

- [ ] **Step 1: Append failing tests**

Append to `internal/feature/compose/diff_test.go`:

```go
func TestComputeDiff_PortsAddedAndRemoved(t *testing.T) {
	deployed := `services:
  api:
    image: api:1
    ports:
      - "8080:80"
      - "8443:443"
`
	proposed := `services:
  api:
    image: api:1
    ports:
      - "8081:80"
      - "8443:443"
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)
	ports, ok := got.ByCategory["ports"].([]SetChange)
	require.True(t, ok)
	require.Len(t, ports, 1)
	require.Equal(t, "api", ports[0].Service)
	require.Equal(t, []string{"8081:80"}, ports[0].Added)
	require.Equal(t, []string{"8080:80"}, ports[0].Removed)
}

func TestComputeDiff_VolumesUnchanged_NotInOutput(t *testing.T) {
	deployed := `services:
  db:
    image: pg:16
    volumes:
      - db-data:/var/lib/postgresql/data
`
	proposed := `services:
  db:
    image: pg:16
    volumes:
      - db-data:/var/lib/postgresql/data
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)
	require.Empty(t, got.ByCategory["volumes"])
}

func TestComputeDiff_EnvMapAndListForms(t *testing.T) {
	// docker-compose accepts both list and map env. Treat both as map.
	deployed := `services:
  app:
    image: app:1
    environment:
      LOG_LEVEL: info
      DEBUG: "false"
`
	proposed := `services:
  app:
    image: app:1
    environment:
      - LOG_LEVEL=debug
      - DEBUG=false
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)
	env, ok := got.ByCategory["env"].([]SetChange)
	require.True(t, ok)
	require.Len(t, env, 1)
	require.Equal(t, "app", env[0].Service)
	require.Equal(t, []string{"LOG_LEVEL=debug"}, env[0].Added)
	require.Equal(t, []string{"LOG_LEVEL=info"}, env[0].Removed)
}
```

- [ ] **Step 2: Run, expect 3 fails on the new tests**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestComputeDiff -count=1`
Expected: 3 FAIL.

- [ ] **Step 3: Add helpers + fill the 3 categories**

Append to `internal/feature/compose/diff.go` (replace the existing `// Image diff` and summary blocks with the full body below):

```go
// Replace the body of ComputeDiff between the unmarshal step and `return res, nil`
// with the following expanded body:

	// Track per-service modification flag — increments Summary.Modified once
	// per service regardless of how many categories changed inside it.
	modified := map[string]bool{}
	addedSvcs := map[string]bool{}
	removedSvcs := map[string]bool{}
	for name := range proposed.Services {
		if _, ok := deployed.Services[name]; !ok {
			addedSvcs[name] = true
		}
	}
	for name := range deployed.Services {
		if _, ok := proposed.Services[name]; !ok {
			removedSvcs[name] = true
		}
	}

	// Image
	imgs := []ImageChange{}
	for name, p := range proposed.Services {
		d, existed := deployed.Services[name]
		if existed && d.Image != p.Image {
			imgs = append(imgs, ImageChange{Service: name, From: d.Image, To: p.Image})
			modified[name] = true
		}
	}
	if len(imgs) > 0 {
		res.ByCategory["image"] = imgs
	}

	// Ports / Volumes — both are []any of strings; canonicalize to []string.
	ports := setDiff(deployed.Services, proposed.Services, func(s composeService) []string {
		return toStringSlice(s.Ports)
	}, modified)
	if len(ports) > 0 {
		res.ByCategory["ports"] = ports
	}
	volumes := setDiff(deployed.Services, proposed.Services, func(s composeService) []string {
		return toStringSlice(s.Volumes)
	}, modified)
	if len(volumes) > 0 {
		res.ByCategory["volumes"] = volumes
	}

	// Environment — accept map or list, normalize to KEY=VAL form.
	env := setDiff(deployed.Services, proposed.Services, func(s composeService) []string {
		return normalizeEnv(s.Environment)
	}, modified)
	if len(env) > 0 {
		res.ByCategory["env"] = env
	}

	res.Summary.Added = len(addedSvcs)
	res.Summary.Removed = len(removedSvcs)
	res.Summary.Modified = len(modified)
```

Append helpers at the end of `diff.go`:

```go
// setDiff returns one SetChange per service whose extract() differs.
func setDiff(deployed, proposed map[string]composeService, extract func(composeService) []string, modified map[string]bool) []SetChange {
	out := []SetChange{}
	for name, p := range proposed {
		d, existed := deployed[name]
		if !existed {
			continue
		}
		ds := stringSet(extract(d))
		ps := stringSet(extract(p))
		added, removed := []string{}, []string{}
		for v := range ps {
			if !ds[v] {
				added = append(added, v)
			}
		}
		for v := range ds {
			if !ps[v] {
				removed = append(removed, v)
			}
		}
		if len(added) == 0 && len(removed) == 0 {
			continue
		}
		out = append(out, SetChange{Service: name, Added: sorted(added), Removed: sorted(removed)})
		modified[name] = true
	}
	return out
}

func toStringSlice(v []any) []string {
	out := make([]string, 0, len(v))
	for _, item := range v {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func normalizeEnv(env any) []string {
	out := []string{}
	switch e := env.(type) {
	case map[string]any:
		for k, v := range e {
			out = append(out, fmt.Sprintf("%s=%v", k, v))
		}
	case []any:
		for _, item := range e {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func stringSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

func sorted(items []string) []string {
	out := make([]string, len(items))
	copy(out, items)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[i] > out[j] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
```

- [ ] **Step 4: Run, expect 4 PASS**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestComputeDiff -count=1 -v`
Expected: 4 PASS (image + ports + volumes + env).

- [ ] **Step 5: Commit**

```bash
git add internal/feature/compose/diff.go internal/feature/compose/diff_test.go
git commit -m "compose: diff ports/volumes/env with set-diff helper"
```

---

## Task 3: Restart / healthcheck / service add+remove

**Files:**
- Modify: `internal/feature/compose/diff.go`
- Modify: `internal/feature/compose/diff_test.go`

- [ ] **Step 1: Append failing tests**

Append to `internal/feature/compose/diff_test.go`:

```go
func TestComputeDiff_RestartPolicy(t *testing.T) {
	deployed := `services:
  web:
    image: nginx:1
`
	proposed := `services:
  web:
    image: nginx:1
    restart: unless-stopped
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)
	rs, ok := got.ByCategory["restart"].([]ScalarChange)
	require.True(t, ok)
	require.Len(t, rs, 1)
	require.Equal(t, "web", rs[0].Service)
	require.Equal(t, "", rs[0].From)
	require.Equal(t, "unless-stopped", rs[0].To)
}

func TestComputeDiff_HealthcheckAdded(t *testing.T) {
	deployed := `services:
  web:
    image: nginx:1
`
	proposed := `services:
  web:
    image: nginx:1
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost"]
      interval: 30s
      retries: 3
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)
	hcs, ok := got.ByCategory["healthcheck"].([]HealthcheckChange)
	require.True(t, ok)
	require.Len(t, hcs, 1)
	require.Equal(t, "web", hcs[0].Service)
	require.Equal(t, "", hcs[0].From)
	require.NotEmpty(t, hcs[0].To)
}

func TestComputeDiff_ServiceAddedAndRemoved(t *testing.T) {
	deployed := `services:
  web:
    image: nginx:1
  legacy:
    image: legacy:1
`
	proposed := `services:
  web:
    image: nginx:1
  cache:
    image: redis:7
`
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)
	require.Equal(t, 1, got.Summary.Added)
	require.Equal(t, 1, got.Summary.Removed)
	require.Equal(t, 0, got.Summary.Modified)
}
```

- [ ] **Step 2: Run, expect 3 fails**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestComputeDiff -count=1`
Expected: 3 FAIL.

- [ ] **Step 3: Implement restart + healthcheck**

In `internal/feature/compose/diff.go`, before `res.Summary.Added = ...` line, add:

```go
	// Restart
	rs := []ScalarChange{}
	for name, p := range proposed.Services {
		d, existed := deployed.Services[name]
		if existed && d.Restart != p.Restart {
			rs = append(rs, ScalarChange{Service: name, From: d.Restart, To: p.Restart})
			modified[name] = true
		}
	}
	if len(rs) > 0 {
		res.ByCategory["restart"] = rs
	}

	// Healthcheck — compare via marshaled form (cheap deep-equal).
	hcs := []HealthcheckChange{}
	for name, p := range proposed.Services {
		d, existed := deployed.Services[name]
		if !existed {
			continue
		}
		dStr := marshalBlock(d.Healthcheck)
		pStr := marshalBlock(p.Healthcheck)
		if dStr != pStr {
			hcs = append(hcs, HealthcheckChange{Service: name, From: dStr, To: pStr})
			modified[name] = true
		}
	}
	if len(hcs) > 0 {
		res.ByCategory["healthcheck"] = hcs
	}
```

Append the `marshalBlock` helper at the bottom of `diff.go`:

```go
func marshalBlock(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return ""
	}
	return string(out)
}
```

- [ ] **Step 4: Run, expect all 7 PASS**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestComputeDiff -count=1 -v`
Expected: 7 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/feature/compose/diff.go internal/feature/compose/diff_test.go
git commit -m "compose: diff restart + healthcheck + service add/remove counters"
```

---

## Task 4: Raw text diff + invalid YAML error

**Files:**
- Modify: `internal/feature/compose/diff.go`
- Modify: `internal/feature/compose/diff_test.go`

- [ ] **Step 1: Append failing tests**

Append to `internal/feature/compose/diff_test.go`:

```go
import "strings"

func TestComputeDiff_RawDiffPopulated(t *testing.T) {
	deployed := "services:\n  web:\n    image: nginx:1.24\n"
	proposed := "services:\n  web:\n    image: nginx:1.25\n"
	got, err := ComputeDiff(deployed, proposed)
	require.NoError(t, err)
	require.Contains(t, got.RawDiff, "nginx:1.24")
	require.Contains(t, got.RawDiff, "nginx:1.25")
	require.True(t, strings.HasPrefix(got.RawDiff, "--- deployed") || strings.Contains(got.RawDiff, "@@"))
}

func TestComputeDiff_InvalidProposedYAML(t *testing.T) {
	_, err := ComputeDiff("services: {}", "this is not yaml: [unclosed")
	require.Error(t, err)
}
```

(Make sure `import "strings"` is added to the test file's import block — merge with existing imports.)

- [ ] **Step 2: Run, expect 2 fails**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestComputeDiff -count=1`
Expected: 2 FAIL (raw_diff empty, parse error path already works).

- [ ] **Step 3: Implement raw line-diff**

Append to `internal/feature/compose/diff.go`:

```go
// rawLineDiff returns a tiny line-level diff of the form
//   --- deployed
//   +++ proposed
//   @@ -i,1 +i,1 @@
//   - old line
//   + new line
// It's not a real unified diff (no context), but enough for the
// frontend's "원본 텍스트" tab where Monaco DiffEditor is doing the
// real rendering. We just need *something* the operator can copy.
func rawLineDiff(deployed, proposed string) string {
	if deployed == proposed {
		return ""
	}
	dLines := splitLines(deployed)
	pLines := splitLines(proposed)
	var b strings.Builder
	b.WriteString("--- deployed\n+++ proposed\n")
	max := len(dLines)
	if len(pLines) > max {
		max = len(pLines)
	}
	for i := 0; i < max; i++ {
		var dl, pl string
		if i < len(dLines) {
			dl = dLines[i]
		}
		if i < len(pLines) {
			pl = pLines[i]
		}
		if dl == pl {
			continue
		}
		fmt.Fprintf(&b, "@@ -%d +%d @@\n", i+1, i+1)
		if i < len(dLines) {
			fmt.Fprintf(&b, "-%s\n", dl)
		}
		if i < len(pLines) {
			fmt.Fprintf(&b, "+%s\n", pl)
		}
	}
	return b.String()
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	out := strings.Split(s, "\n")
	// Trim trailing empty caused by terminal newline — keeps diff symmetric.
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}
```

Add `"strings"` to the imports in `diff.go` (merge with existing block — currently has `fmt` and `gopkg.in/yaml.v3`).

In `ComputeDiff`, **before** the final `return res, nil`, add:

```go
	res.RawDiff = rawLineDiff(deployedYAML, proposedYAML)
```

- [ ] **Step 4: Run, expect all 9 PASS**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestComputeDiff -count=1 -v`
Expected: 9 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/feature/compose/diff.go internal/feature/compose/diff_test.go
git commit -m "compose: raw line diff + invalid-YAML error path"
```

---

## Task 5: Error codes for the new endpoints

**Files:**
- Modify: `internal/api/response/errors.go`

- [ ] **Step 1: Read current error codes**

Run: `grep -n "Err.*=" internal/api/response/errors.go | head -30`
Note the existing constants and copy their style exactly.

- [ ] **Step 2: Append new error codes**

Find the section around the existing `ErrInvalidRequest` constant in `internal/api/response/errors.go` and add these constants alongside the existing ones (preserve alphabetical or grouped style if there is one):

```go
	ErrInvalidYAML        = "INVALID_YAML"
	ErrGitAuthFailed      = "GIT_AUTH_FAILED"
	ErrGitRepoNotFound    = "GIT_REPO_NOT_FOUND"
	ErrGitPathNotFound    = "GIT_PATH_NOT_FOUND"
	ErrGitCloneFailed     = "GIT_CLONE_FAILED"
	ErrStackAlreadyExists = "STACK_ALREADY_EXISTS"
```

If `errors.go` has a `StatusCode()` switch, add status mappings (immediately after the existing 400/500 cases):

```go
	case ErrInvalidYAML:
		return http.StatusBadRequest
	case ErrGitAuthFailed:
		return http.StatusUnauthorized
	case ErrGitRepoNotFound, ErrGitPathNotFound:
		return http.StatusNotFound
	case ErrStackAlreadyExists:
		return http.StatusConflict
	case ErrGitCloneFailed:
		return http.StatusInternalServerError
```

- [ ] **Step 3: Build to verify no syntax errors**

Run: `cd /opt/stacks/SFPanel && go build ./internal/api/response/...`
Expected: clean.

- [ ] **Step 4: Run full repo build to confirm no breakage**

Run: `cd /opt/stacks/SFPanel && go build ./...`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/api/response/errors.go
git commit -m "response: error codes for compose diff + git import"
```

---

## Task 6: DiffStack handler + route registration

**Files:**
- Modify: `internal/feature/compose/handler.go`
- Modify: `internal/api/router.go`
- Create: `internal/feature/compose/handler_test.go`

- [ ] **Step 1: Write failing handler test**

Create `internal/feature/compose/handler_test.go`:

```go
package compose

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

// TestDiffStack_PureFlow exercises the handler's body parsing and
// ComputeDiff invocation. It uses a fake project loader (the real
// handler reads from disk via h.Compose) — to keep this test small,
// the handler accepts both deployed and proposed YAML in the body
// IF a special test-only field is set… actually no: we just verify
// that an invalid YAML body returns 400 INVALID_YAML, which doesn't
// require any disk state.
func TestDiffStack_InvalidProposedYAML_Returns400(t *testing.T) {
	body := bytes.NewBufferString(`{"yaml": "this is not yaml: [unclosed"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/compose/myproj/diff", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetParamNames("project")
	c.SetParamValues("myproj")

	h := &Handler{} // no Compose dependency hit on the invalid-yaml branch
	_ = h.DiffStack(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var body2 map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body2))
	require.Equal(t, false, body2["success"])
}
```

- [ ] **Step 2: Run, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestDiffStack -count=1`
Expected: FAIL — `undefined: (*Handler).DiffStack`.

- [ ] **Step 3: Implement DiffStack handler**

Append to `internal/feature/compose/handler.go`:

```go
// DiffStack returns a categorized diff between the deployed compose YAML
// for :project and the YAML supplied in the request body.
// POST /api/v1/compose/:project/diff   {"yaml": "..."}
func (h *Handler) DiffStack(c echo.Context) error {
	name := c.Param("project")
	if name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "project name required")
	}
	var req struct {
		YAML string `json:"yaml"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "invalid request body")
	}
	if req.YAML == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "yaml required")
	}

	ctx := c.Request().Context()
	deployedYAML, _, err := h.Compose.GetProjectYAML(ctx, name)
	if err != nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, response.SanitizeOutput(err.Error()))
	}

	res, err := ComputeDiff(deployedYAML, req.YAML)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidYAML, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, res)
}
```

- [ ] **Step 4: Register route**

In `internal/api/router.go`, find the compose group block (around line 481-499) and add **immediately after** `compose.PUT("/:project", composeHandler.UpdateProject)`:

```go
		compose.POST("/:project/diff", composeHandler.DiffStack)
```

- [ ] **Step 5: Run all compose tests + build**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -count=1 -v && go build ./...`
Expected: all PASS, build clean.

- [ ] **Step 6: Commit**

```bash
git add internal/feature/compose/handler.go internal/feature/compose/handler_test.go internal/api/router.go
git commit -m "compose: DiffStack handler + POST /:project/diff route"
```

---

## Task 7: go-git dependency + ImportRequest types

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/feature/compose/git_import.go`
- Create: `internal/feature/compose/git_import_test.go`

- [ ] **Step 1: Add go-git dependency**

Run:

```bash
cd /opt/stacks/SFPanel
go get github.com/go-git/go-git/v5@latest
go mod tidy
```

Expected: `go.mod` shows `github.com/go-git/go-git/v5 v5.x.y` (some line). `go.sum` updated.

- [ ] **Step 2: Write failing test for URL validation**

Create `internal/feature/compose/git_import_test.go`:

```go
package compose

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateImportRequest_URLPattern(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		want   bool
		reason string
	}{
		{"happy github https", "https://github.com/foo/bar.git", true, ""},
		{"happy github https no .git", "https://github.com/foo/bar", true, ""},
		{"reject http (insecure)", "http://github.com/foo/bar.git", false, "https only"},
		{"reject ssh form", "git@github.com:foo/bar.git", false, "https only"},
		{"reject non-github", "https://gitlab.com/foo/bar.git", false, "github only"},
		{"reject empty", "", false, "url required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateImportRequest(ImportRequest{URL: tc.url, Name: "stack"})
			if tc.want {
				require.NoError(t, err, tc.reason)
			} else {
				require.Error(t, err, tc.reason)
			}
		})
	}
}

func TestValidateImportRequest_NameRules(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"my-stack", true},
		{"a", true},
		{"123abc", true},
		{"My-Stack", false},     // uppercase rejected
		{"my_stack", false},     // underscore rejected
		{"my stack", false},     // space rejected
		{"", false},             // empty rejected
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateImportRequest(ImportRequest{
				URL:  "https://github.com/foo/bar.git",
				Name: tc.name,
			})
			if tc.want {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
```

- [ ] **Step 3: Run, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestValidateImportRequest -count=1`
Expected: FAIL — `undefined: ImportRequest`, `undefined: validateImportRequest`.

- [ ] **Step 4: Create git_import.go skeleton**

Create `internal/feature/compose/git_import.go`:

```go
package compose

import (
	"fmt"
	"regexp"
	"strings"
)

// ImportRequest is the payload for POST /api/v1/compose/import.
// Token is used once to clone and is never persisted.
type ImportRequest struct {
	URL    string `json:"url"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	Token  string `json:"token"`
	Name   string `json:"name"`
}

var (
	// GitHub HTTPS only: https://github.com/<user>/<repo>(.git)?
	githubURLRe = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(\.git)?$`)
	// SFPanel project naming: lowercase, digits, hyphen.
	stackNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,49}$`)
)

// validateImportRequest enforces format constraints. It does NOT touch
// the network and does NOT validate the token's value (a wrong token
// surfaces as a 401 from the clone step).
func validateImportRequest(req ImportRequest) error {
	if req.URL == "" {
		return fmt.Errorf("url required")
	}
	if !strings.HasPrefix(req.URL, "https://") {
		return fmt.Errorf("only https github URLs are supported")
	}
	if !githubURLRe.MatchString(req.URL) {
		return fmt.Errorf("only github.com URLs are supported")
	}
	if !stackNameRe.MatchString(req.Name) {
		return fmt.Errorf("stack name must be 1-50 chars, lowercase/digits/hyphen, start with letter or digit")
	}
	return nil
}
```

- [ ] **Step 5: Run tests, expect PASS**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestValidateImportRequest -count=1 -v`
Expected: 11 PASS (6 URL + 5 name subtests).

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/feature/compose/git_import.go internal/feature/compose/git_import_test.go
git commit -m "compose: ImportRequest type + URL/name validation"
```

---

## Task 8: ImportFromGit clone + read with in-memory test fixture

**Files:**
- Modify: `internal/feature/compose/git_import.go`
- Modify: `internal/feature/compose/git_import_test.go`

- [ ] **Step 1: Write failing test using in-memory bare repo**

Append to `internal/feature/compose/git_import_test.go`:

```go
import (
	"context"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/stretchr/testify/require"
)

// makeFakeRepo returns a *git.Repository in memory containing the
// given file at the given path on the default branch (HEAD).
func makeFakeRepo(t *testing.T, path, content string) *git.Repository {
	t.Helper()
	fs := memfs.New()
	storer := memory.NewStorage()
	r, err := git.Init(storer, fs)
	require.NoError(t, err)

	w, err := r.Worktree()
	require.NoError(t, err)

	f, err := fs.Create(path)
	require.NoError(t, err)
	_, err = f.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = w.Add(path)
	require.NoError(t, err)

	_, err = w.Commit("seed", &git.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "t@x", When: time.Now()},
	})
	require.NoError(t, err)
	return r
}

func TestReadComposeFromRepo_HappyPath(t *testing.T) {
	yaml := "services:\n  web:\n    image: nginx:1.25\n"
	r := makeFakeRepo(t, "docker-compose.yml", yaml)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := readComposeFromRepo(ctx, r, "main", "docker-compose.yml")
	require.NoError(t, err)
	require.Equal(t, yaml, got)
}

func TestReadComposeFromRepo_PathNotFound(t *testing.T) {
	r := makeFakeRepo(t, "docker-compose.yml", "services: {}\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := readComposeFromRepo(ctx, r, "main", "missing.yml")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPathNotFound)
}
```

- [ ] **Step 2: Run, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestReadComposeFromRepo -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement readComposeFromRepo**

Append to `internal/feature/compose/git_import.go`:

```go
import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// ErrPathNotFound is returned when the repo cloned but the requested
// compose file path does not exist in the resolved tree.
var ErrPathNotFound = errors.New("compose path not found in repo")

// readComposeFromRepo extracts the file at `path` on `branch` from an
// already-opened git.Repository. Branch defaults to HEAD if empty.
func readComposeFromRepo(ctx context.Context, r *git.Repository, branch, path string) (string, error) {
	if path == "" {
		path = "docker-compose.yml"
	}
	var ref *plumbing.Reference
	var err error
	if branch == "" {
		ref, err = r.Head()
	} else {
		// Try local branch first; fall back to remote branch (for cloned repos).
		ref, err = r.Reference(plumbing.NewBranchReferenceName(branch), true)
		if err != nil {
			ref, err = r.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
		}
	}
	if err != nil {
		return "", fmt.Errorf("resolve branch %q: %w", branch, err)
	}
	commit, err := r.CommitObject(ref.Hash())
	if err != nil {
		return "", fmt.Errorf("commit object: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return "", fmt.Errorf("commit tree: %w", err)
	}
	file, err := tree.File(path)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			return "", ErrPathNotFound
		}
		return "", fmt.Errorf("tree.File: %w", err)
	}
	rdr, err := file.Reader()
	if err != nil {
		return "", fmt.Errorf("file reader: %w", err)
	}
	defer rdr.Close()
	body, err := io.ReadAll(rdr)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	_ = ctx // ctx not used by go-git tree walk; reserved for future cancel
	return string(body), nil
}
```

You'll also need to add `"github.com/go-git/go-git/v5/plumbing/object"` to the imports for `object.ErrFileNotFound`. Merge with the existing import block.

- [ ] **Step 4: Run, expect 2 PASS**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestReadComposeFromRepo -count=1 -v`
Expected: 2 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/feature/compose/git_import.go internal/feature/compose/git_import_test.go
git commit -m "compose: readComposeFromRepo with in-memory git fixture"
```

---

## Task 9: ImportFromGit handler + clone wrapper + endpoint

**Files:**
- Modify: `internal/feature/compose/git_import.go`
- Modify: `internal/feature/compose/handler.go`
- Modify: `internal/api/router.go`
- Modify: `internal/feature/compose/handler_test.go`

- [ ] **Step 1: Write failing handler test**

Append to `internal/feature/compose/handler_test.go`:

```go
func TestImportFromGit_RejectsBadURL(t *testing.T) {
	body := bytes.NewBufferString(`{"url":"http://example.com/foo.git","name":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/compose/import", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	h := &Handler{}
	_ = h.ImportFromGit(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}
```

- [ ] **Step 2: Run, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestImportFromGit -count=1`
Expected: FAIL.

- [ ] **Step 3: Implement clone wrapper + handler**

Append to `internal/feature/compose/git_import.go`:

```go
import (
	"strings"
	"time"

	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// ErrAuthFailed / ErrRepoNotFound are typed errors returned by
// cloneShallow so the handler can map them to specific HTTP codes
// without parsing string contents.
var (
	ErrAuthFailed   = errors.New("git auth failed")
	ErrRepoNotFound = errors.New("git repo not found")
)

// cloneShallow clones the given URL (depth=1) into an in-memory
// repository. Returns one of ErrAuthFailed / ErrRepoNotFound for
// the two cases the handler maps to specific HTTP statuses.
func cloneShallow(ctx context.Context, url, branch, token string) (*git.Repository, error) {
	opts := &git.CloneOptions{
		URL:           url,
		Depth:         1,
		SingleBranch:  true,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
	}
	if token != "" {
		opts.Auth = &githttp.BasicAuth{Username: "x-access-token", Password: token}
	}
	r, err := git.CloneContext(ctx, memory.NewStorage(), memfs.New(), opts)
	if err != nil {
		// go-git surfaces auth and not-found errors with text bodies.
		// Inspect to map cleanly.
		msg := strings.ToLower(err.Error())
		switch {
		case strings.Contains(msg, "authentication required"),
			strings.Contains(msg, "authorization failed"),
			strings.Contains(msg, "401"):
			return nil, ErrAuthFailed
		case strings.Contains(msg, "repository not found"),
			strings.Contains(msg, "not found"),
			strings.Contains(msg, "404"):
			return nil, ErrRepoNotFound
		}
		return nil, fmt.Errorf("clone: %w", err)
	}
	return r, nil
}

// You'll also need this constant somewhere visible to the handler.
const importCloneTimeout = 30 * time.Second
```

Add the new imports `"github.com/go-git/go-billy/v5/memfs"` (already imported in test, now needed in main file) and `"github.com/go-git/go-git/v5/storage/memory"` to the main file's import block.

Append the handler to `internal/feature/compose/handler.go`:

```go
// ImportFromGit clones a GitHub repo (one-shot, no persistent link),
// reads the compose YAML at the requested path, and creates a stack.
// POST /api/v1/compose/import
func (h *Handler) ImportFromGit(c echo.Context) error {
	var req ImportRequest
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "invalid request body")
	}
	if req.Branch == "" {
		req.Branch = "main"
	}
	if req.Path == "" {
		req.Path = "docker-compose.yml"
	}
	if err := validateImportRequest(req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, err.Error())
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), importCloneTimeout)
	defer cancel()

	repo, err := cloneShallow(ctx, req.URL, req.Branch, req.Token)
	if err != nil {
		switch {
		case errors.Is(err, ErrAuthFailed):
			return response.Fail(c, http.StatusUnauthorized, response.ErrGitAuthFailed,
				"인증 실패. PAT가 필요한 private 저장소입니다.")
		case errors.Is(err, ErrRepoNotFound):
			return response.Fail(c, http.StatusNotFound, response.ErrGitRepoNotFound,
				"저장소를 찾을 수 없습니다.")
		default:
			return response.Fail(c, http.StatusInternalServerError, response.ErrGitCloneFailed,
				response.SanitizeOutput(err.Error()))
		}
	}

	yamlBody, err := readComposeFromRepo(ctx, repo, req.Branch, req.Path)
	if err != nil {
		if errors.Is(err, ErrPathNotFound) {
			return response.Fail(c, http.StatusNotFound, response.ErrGitPathNotFound,
				"해당 경로의 파일이 없습니다.")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrGitCloneFailed,
			response.SanitizeOutput(err.Error()))
	}

	if err := composex.ValidateAdvancedCompose(yamlBody); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidYAML, err.Error())
	}

	project, err := h.Compose.CreateProject(ctx, req.Name, yamlBody)
	if err != nil {
		// CreateProject's error path includes "already exists" for collisions.
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return response.Fail(c, http.StatusConflict, response.ErrStackAlreadyExists,
				"이미 존재하는 스택 이름입니다.")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrComposeError,
			response.SanitizeOutput(err.Error()))
	}

	return response.OK(c, map[string]string{"project_name": project.Name})
}
```

Add the necessary imports at the top of `handler.go`: `"context"`, `"errors"`, `"strings"` — merge with existing.

- [ ] **Step 4: Register route**

In `internal/api/router.go`, immediately after the new `compose.POST("/:project/diff", ...)` line from Task 6, add:

```go
		compose.POST("/import", composeHandler.ImportFromGit)
```

Note: this needs to be **outside** the `:project` parameter prefix. Verify by reading the surrounding code — the `compose` group is `dk.Group("/compose")`, so `compose.POST("/import", ...)` resolves to `POST /api/v1/docker/compose/import`. **Wait** — re-check the spec: it says `POST /api/v1/compose/import` (no `/docker/` prefix). Run `grep -n 'dk := ' internal/api/router.go` and adjust accordingly. If the compose group sits under `dk`, the actual path is `/docker/compose/import`. Update the spec reference here to match the actual route — operators don't care about the exact prefix as long as it's consistent. Use whatever the surrounding compose routes use.

- [ ] **Step 5: Run all tests + build**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -count=1 && go build ./...`
Expected: all tests PASS, build clean.

- [ ] **Step 6: Run lint**

Run: `cd /opt/stacks/SFPanel && golangci-lint run ./internal/feature/compose/...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/feature/compose/git_import.go internal/feature/compose/handler.go internal/feature/compose/handler_test.go internal/api/router.go
git commit -m "compose: ImportFromGit handler — shallow clone, error mapping, route"
```

---

## Task 10: Frontend types + API client methods

**Files:**
- Modify: `web/src/types/api.ts`
- Modify: `web/src/lib/api.ts`

- [ ] **Step 1: Append types**

Append to `web/src/types/api.ts`:

```typescript
export interface DiffSummary {
  added: number
  modified: number
  removed: number
}

export interface DiffImageChange { service: string; from: string; to: string }
export interface DiffSetChange   { service: string; added: string[]; removed: string[] }
export interface DiffScalarChange { service: string; from: string; to: string }
export interface DiffHealthcheckChange { service: string; from: string; to: string }

export interface DiffByCategory {
  image:       DiffImageChange[]
  ports:       DiffSetChange[]
  volumes:     DiffSetChange[]
  env:         DiffSetChange[]
  restart:     DiffScalarChange[]
  healthcheck: DiffHealthcheckChange[]
}

export interface DiffResult {
  summary: DiffSummary
  by_category: DiffByCategory
  raw_diff: string
}

export interface ImportRequest {
  url: string
  branch?: string
  path?: string
  token?: string
  name: string
}
```

- [ ] **Step 2: Add API methods**

In `web/src/lib/api.ts`, add the new types to the import block at the top (merge with existing `import type {...}`):

```typescript
import type {
  // ... existing imports ...
  DiffResult,
  ImportRequest,
} from '@/types/api'
```

Then add inside the `ApiClient` class, near the other `compose*` methods:

```typescript
diffStack(name: string, yaml: string) {
  return this.request<DiffResult>(`/compose/${encodeURIComponent(name)}/diff`, {
    method: 'POST',
    body: JSON.stringify({ yaml }),
  })
}

importFromGit(req: ImportRequest) {
  return this.request<{ project_name: string }>(`/compose/import`, {
    method: 'POST',
    body: JSON.stringify(req),
  })
}
```

(If the existing compose paths use a different prefix — e.g. `/docker/compose/...` — update both URLs to match. Search `web/src/lib/api.ts` for `'/compose'` to find the convention.)

- [ ] **Step 3: Build frontend**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/src/types/api.ts web/src/lib/api.ts
git commit -m "web: types + api client for compose diff + git import"
```

---

## Task 11: DiffSummaryHeader + DiffServiceRow components

**Files:**
- Create: `web/src/components/compose/DiffSummaryHeader.tsx`
- Create: `web/src/components/compose/DiffServiceRow.tsx`

- [ ] **Step 1: Create DiffSummaryHeader**

Create `web/src/components/compose/DiffSummaryHeader.tsx`:

```typescript
import type { DiffSummary } from '@/types/api'

interface Props {
  summary: DiffSummary
}

export function DiffSummaryHeader({ summary }: Props) {
  return (
    <div
      className="flex items-center gap-4 text-[13px] bg-secondary/50 rounded-lg px-3 py-2"
      role="status"
      aria-label={`추가 ${summary.added}, 변경 ${summary.modified}, 삭제 ${summary.removed}`}
    >
      <span className="flex items-center gap-1 text-emerald-600">
        <span className="font-mono">+</span>
        <span>추가 {summary.added}</span>
      </span>
      <span className="flex items-center gap-1 text-blue-600">
        <span className="font-mono">~</span>
        <span>변경 {summary.modified}</span>
      </span>
      <span className="flex items-center gap-1 text-destructive">
        <span className="font-mono">−</span>
        <span>삭제 {summary.removed}</span>
      </span>
    </div>
  )
}
```

- [ ] **Step 2: Create DiffServiceRow**

Create `web/src/components/compose/DiffServiceRow.tsx`:

```typescript
import type {
  DiffImageChange,
  DiffSetChange,
  DiffScalarChange,
  DiffHealthcheckChange,
} from '@/types/api'

type RowProps =
  | { kind: 'image'; change: DiffImageChange }
  | { kind: 'set';   change: DiffSetChange }
  | { kind: 'scalar'; change: DiffScalarChange }
  | { kind: 'healthcheck'; change: DiffHealthcheckChange }

export function DiffServiceRow(props: RowProps) {
  const { change } = props
  return (
    <div className="grid grid-cols-[120px_1fr] gap-2 py-1 text-[12px]">
      <span className="font-medium truncate" title={change.service}>{change.service}</span>
      <div className="font-mono leading-relaxed">
        {props.kind === 'image' && (
          <span>
            <span>{change.from || '(없음)'}</span>
            <span className="text-muted-foreground mx-1">→</span>
            <span>{change.to}</span>
          </span>
        )}
        {props.kind === 'scalar' && (
          <span>
            <span>{change.from || '(없음)'}</span>
            <span className="text-muted-foreground mx-1">→</span>
            <span>{change.to || '(없음)'}</span>
          </span>
        )}
        {props.kind === 'set' && (
          <div className="flex flex-col gap-0.5">
            {change.added.map(v => (
              <div key={`+${v}`} className="text-emerald-600">+ {v}</div>
            ))}
            {change.removed.map(v => (
              <div key={`-${v}`} className="text-destructive">− {v}</div>
            ))}
          </div>
        )}
        {props.kind === 'healthcheck' && (
          <div className="flex flex-col gap-0.5">
            {!change.from && change.to && <div className="text-emerald-600">+ healthcheck 추가</div>}
            {change.from && !change.to && <div className="text-destructive">− healthcheck 제거</div>}
            {change.from && change.to && <div className="text-blue-600">~ healthcheck 변경</div>}
          </div>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 3: Build frontend**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/compose/DiffSummaryHeader.tsx web/src/components/compose/DiffServiceRow.tsx
git commit -m "web: DiffSummaryHeader + DiffServiceRow components"
```

---

## Task 12: DiffCategoryList (Accordion)

**Files:**
- Create: `web/src/components/compose/DiffCategoryList.tsx`

- [ ] **Step 1: Confirm shadcn Accordion exists**

Run: `ls web/src/components/ui/accordion.tsx`. If missing, run `cd web && npx shadcn@latest add accordion` (this is a one-time install — re-check `package.json` doesn't already list `@radix-ui/react-accordion`).

- [ ] **Step 2: Create DiffCategoryList**

Create `web/src/components/compose/DiffCategoryList.tsx`:

```typescript
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion'
import { Container, Network, HardDrive, Settings, RotateCw, Heart } from 'lucide-react'
import type { DiffByCategory } from '@/types/api'
import { DiffServiceRow } from './DiffServiceRow'

interface Props {
  byCategory: DiffByCategory
}

interface CatMeta {
  key: keyof DiffByCategory
  label: string
  icon: React.ComponentType<{ className?: string }>
}

const CATEGORIES: CatMeta[] = [
  { key: 'image',       label: '이미지',     icon: Container },
  { key: 'ports',       label: '포트',       icon: Network },
  { key: 'volumes',     label: '볼륨',       icon: HardDrive },
  { key: 'env',         label: '환경변수',   icon: Settings },
  { key: 'restart',     label: 'restart',   icon: RotateCw },
  { key: 'healthcheck', label: 'healthcheck', icon: Heart },
]

export function DiffCategoryList({ byCategory }: Props) {
  // Categories with ≥1 change are open by default. The shadcn Accordion
  // accepts a `defaultValue` array (multi-mode) to express this.
  const defaultOpen = CATEGORIES
    .filter(c => (byCategory[c.key]?.length ?? 0) > 0)
    .map(c => c.key as string)

  return (
    <Accordion type="multiple" defaultValue={defaultOpen} className="w-full">
      {CATEGORIES.map(({ key, label, icon: Icon }) => {
        const items = byCategory[key] ?? []
        const count = items.length
        const isEmpty = count === 0
        return (
          <AccordionItem key={key} value={key} className={isEmpty ? 'opacity-50' : ''}>
            <AccordionTrigger className="text-[13px]" disabled={isEmpty}>
              <span className="flex items-center gap-2 flex-1">
                <Icon className="h-3.5 w-3.5" />
                <span>{label}</span>
              </span>
              <span className="text-[12px] text-muted-foreground mr-2">
                {isEmpty ? '변경 없음' : `${count} 변경`}
              </span>
            </AccordionTrigger>
            <AccordionContent>
              <div className="px-2">
                {items.map((change, i) => {
                  if (key === 'image') return <DiffServiceRow key={i} kind="image" change={change as never} />
                  if (key === 'restart') return <DiffServiceRow key={i} kind="scalar" change={change as never} />
                  if (key === 'healthcheck') return <DiffServiceRow key={i} kind="healthcheck" change={change as never} />
                  return <DiffServiceRow key={i} kind="set" change={change as never} />
                })}
              </div>
            </AccordionContent>
          </AccordionItem>
        )
      })}
    </Accordion>
  )
}
```

- [ ] **Step 3: Build frontend**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/compose/DiffCategoryList.tsx web/src/components/ui/accordion.tsx
git commit -m "web: DiffCategoryList Accordion (6 카테고리)"
```

---

## Task 13: DiffSheet wrapper + integration

**Files:**
- Create: `web/src/components/compose/DiffSheet.tsx`
- Modify: `web/src/pages/docker/DockerStacks.tsx`

- [ ] **Step 1: Create DiffSheet**

Create `web/src/components/compose/DiffSheet.tsx`:

```typescript
import { useEffect, useState } from 'react'
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetFooter } from '@/components/ui/sheet'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { DiffEditor } from '@monaco-editor/react'
import { api } from '@/lib/api'
import type { DiffResult } from '@/types/api'
import { DiffSummaryHeader } from './DiffSummaryHeader'
import { DiffCategoryList } from './DiffCategoryList'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectName: string
  proposedYaml: string
  deployedYaml: string  // shown in DiffEditor's left side
  onApply: () => void   // close sheet + run existing UpdateStack flow
}

export function DiffSheet({ open, onOpenChange, projectName, proposedYaml, deployedYaml, onApply }: Props) {
  const [data, setData] = useState<DiffResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    if (!open) return
    setLoading(true); setError(null); setData(null)
    api.diffStack(projectName, proposedYaml)
      .then(setData)
      .catch((e: Error) => setError(e.message || '미리보기를 불러올 수 없습니다.'))
      .finally(() => setLoading(false))
  }, [open, projectName, proposedYaml])

  const isEmpty = data && data.summary.added === 0 && data.summary.modified === 0 && data.summary.removed === 0

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="w-full sm:max-w-[640px] flex flex-col">
        <SheetHeader>
          <SheetTitle className="text-[14px]">변경사항 미리보기</SheetTitle>
        </SheetHeader>

        <div className="flex-1 overflow-auto py-2 space-y-3">
          {loading && (
            <>
              <Skeleton className="h-12 w-full" />
              <Skeleton className="h-32 w-full" />
              <Skeleton className="h-32 w-full" />
            </>
          )}

          {error && !loading && (
            <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-[13px] text-destructive">
              <div className="font-medium mb-1">미리보기를 불러올 수 없습니다</div>
              <div className="text-[12px] opacity-80">{error}</div>
            </div>
          )}

          {data && isEmpty && !loading && (
            <div className="text-center py-12 text-muted-foreground text-[13px]">
              🟢 변경 사항이 없습니다
            </div>
          )}

          {data && !isEmpty && !loading && (
            <>
              <DiffSummaryHeader summary={data.summary} />
              <Tabs defaultValue="categories">
                <TabsList>
                  <TabsTrigger value="categories">카테고리</TabsTrigger>
                  <TabsTrigger value="raw">원본 텍스트</TabsTrigger>
                </TabsList>
                <TabsContent value="categories" className="pt-2">
                  <DiffCategoryList byCategory={data.by_category} />
                </TabsContent>
                <TabsContent value="raw" className="pt-2">
                  <div className="border rounded-md overflow-hidden">
                    <DiffEditor
                      height="400px"
                      language="yaml"
                      theme="vs-dark"
                      original={deployedYaml}
                      modified={proposedYaml}
                      options={{
                        readOnly: true,
                        renderSideBySide: true,
                        minimap: { enabled: false },
                        fontSize: 12,
                      }}
                    />
                  </div>
                </TabsContent>
              </Tabs>
            </>
          )}
        </div>

        <SheetFooter className="pt-2 border-t">
          <Button variant="outline" onClick={() => onOpenChange(false)}>닫기</Button>
          <Button
            onClick={onApply}
            disabled={!data || isEmpty || !!error}
          >
            이대로 적용
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
```

- [ ] **Step 2: Confirm shadcn Sheet + Skeleton exist**

Run: `ls web/src/components/ui/sheet.tsx web/src/components/ui/skeleton.tsx`. Install via `npx shadcn@latest add sheet skeleton` if missing.

- [ ] **Step 3: Wire to DockerStacks page**

In `web/src/pages/docker/DockerStacks.tsx`, find the Tabs block where the "에디터" tab content renders `<ComposeEditor>` and add the diff button + sheet. Look for the editor TabsContent (probably `<TabsContent value="editor">`):

Add to imports near top:
```typescript
import { DiffSheet } from '@/components/compose/DiffSheet'
```

Add state near other component state:
```typescript
const [diffOpen, setDiffOpen] = useState(false)
```

In the editor tab content, add a button row above or below the Monaco editor. Find the existing "저장" button and add the new button next to it:

```tsx
<div className="flex justify-end gap-2 mt-2">
  <Button
    variant="outline"
    size="sm"
    disabled={editorYaml === lastSavedYaml}
    onClick={() => setDiffOpen(true)}
    title={editorYaml === lastSavedYaml ? '변경사항이 없습니다' : '변경사항 미리보기'}
  >
    변경사항 미리보기
  </Button>
  <Button size="sm" onClick={handleSave}>저장</Button>
</div>
```

(`editorYaml` and `lastSavedYaml` are the existing state vars in DockerStacks for the Monaco editor — find them with `grep -n editorYaml web/src/pages/docker/DockerStacks.tsx` to confirm exact names.)

Add the DiffSheet near the bottom of the JSX tree (sibling of other dialogs):

```tsx
{selectedStack && (
  <DiffSheet
    open={diffOpen}
    onOpenChange={setDiffOpen}
    projectName={selectedStack.name}
    proposedYaml={editorYaml}
    deployedYaml={lastSavedYaml}
    onApply={() => {
      setDiffOpen(false)
      handleSave()  // existing handler
    }}
  />
)}
```

- [ ] **Step 4: Build frontend**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/compose/DiffSheet.tsx web/src/pages/docker/DockerStacks.tsx
git commit -m "web: DiffSheet (right slide-over) + 변경사항 미리보기 버튼"
```

---

## Task 14: GitImportForm component

**Files:**
- Create: `web/src/components/compose/GitImportForm.tsx`

- [ ] **Step 1: Create GitImportForm**

Create `web/src/components/compose/GitImportForm.tsx`:

```typescript
import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Eye, EyeOff } from 'lucide-react'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import type { ApiError } from '@/types/api'

const GITHUB_URL_RE = /^https:\/\/github\.com\/[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+(\.git)?$/
const STACK_NAME_RE = /^[a-z0-9][a-z0-9-]{0,49}$/

interface Props {
  onSuccess: (projectName: string) => void
  onCancel: () => void
}

export function GitImportForm({ onSuccess, onCancel }: Props) {
  const [url, setUrl] = useState('')
  const [branch, setBranch] = useState('main')
  const [path, setPath] = useState('docker-compose.yml')
  const [token, setToken] = useState('')
  const [name, setName] = useState('')
  const [tokenVisible, setTokenVisible] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [errors, setErrors] = useState<Record<string, string>>({})

  function validate(): boolean {
    const e: Record<string, string> = {}
    if (!url) e.url = 'URL을 입력해주세요'
    else if (!GITHUB_URL_RE.test(url)) e.url = 'GitHub HTTPS URL만 지원합니다'
    if (!name) e.name = '스택 이름을 입력해주세요'
    else if (!STACK_NAME_RE.test(name)) e.name = '소문자/숫자/하이픈만, 1-50자'
    setErrors(e)
    return Object.keys(e).length === 0
  }

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!validate()) return
    setSubmitting(true)
    try {
      const res = await api.importFromGit({ url, branch, path, token: token || undefined, name })
      toast.success(`'${res.project_name}' 스택을 가져왔습니다`)
      onSuccess(res.project_name)
    } catch (err) {
      const apiErr = err as ApiError
      const code = apiErr.code
      const msg = apiErr.message || '가져오기 실패'
      // Map specific codes to specific field errors
      const fieldErrors: Record<string, string> = {}
      if (code === 'GIT_AUTH_FAILED') fieldErrors.token = '인증 실패. PAT가 필요한 private 저장소입니다.'
      else if (code === 'GIT_REPO_NOT_FOUND') fieldErrors.url = '저장소를 찾을 수 없습니다'
      else if (code === 'GIT_PATH_NOT_FOUND') fieldErrors.path = '해당 경로의 파일이 없습니다'
      else if (code === 'INVALID_YAML') fieldErrors.path = 'compose YAML 형식이 올바르지 않습니다'
      else if (code === 'STACK_ALREADY_EXISTS') fieldErrors.name = '이미 존재하는 스택 이름입니다'
      else fieldErrors._form = msg
      setErrors(fieldErrors)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <form onSubmit={onSubmit} className="space-y-3 text-[13px]">
      <div>
        <Label htmlFor="git-url">GitHub repo URL *</Label>
        <Input
          id="git-url"
          value={url}
          onChange={e => setUrl(e.target.value)}
          placeholder="https://github.com/user/repo.git"
          autoComplete="off"
        />
        {errors.url
          ? <p className="text-[12px] text-destructive mt-1">{errors.url}</p>
          : <p className="text-[12px] text-muted-foreground mt-1">GitHub HTTPS URL만 지원</p>}
      </div>

      <div className="grid grid-cols-3 gap-2">
        <div>
          <Label htmlFor="git-branch">branch</Label>
          <Input id="git-branch" value={branch} onChange={e => setBranch(e.target.value)} />
        </div>
        <div className="col-span-2">
          <Label htmlFor="git-path">path</Label>
          <Input id="git-path" value={path} onChange={e => setPath(e.target.value)} />
          {errors.path && <p className="text-[12px] text-destructive mt-1">{errors.path}</p>}
        </div>
      </div>

      <div>
        <Label htmlFor="git-token">Personal Access Token (private repo만)</Label>
        <div className="relative">
          <Input
            id="git-token"
            type={tokenVisible ? 'text' : 'password'}
            value={token}
            onChange={e => setToken(e.target.value)}
            placeholder="ghp_..."
            autoComplete="off"
            className="pr-10"
          />
          <button
            type="button"
            onClick={() => setTokenVisible(v => !v)}
            className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground"
            aria-label={tokenVisible ? '토큰 숨기기' : '토큰 표시'}
          >
            {tokenVisible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
          </button>
        </div>
        {errors.token
          ? <p className="text-[12px] text-destructive mt-1">{errors.token}</p>
          : <p className="text-[12px] text-muted-foreground mt-1">토큰은 한 번만 사용되고 저장되지 않습니다</p>}
      </div>

      <div>
        <Label htmlFor="git-name">stack 이름 *</Label>
        <Input id="git-name" value={name} onChange={e => setName(e.target.value)} placeholder="my-stack" />
        {errors.name
          ? <p className="text-[12px] text-destructive mt-1">{errors.name}</p>
          : <p className="text-[12px] text-muted-foreground mt-1">소문자/숫자/하이픈만, 1-50자</p>}
      </div>

      {errors._form && (
        <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-2 text-[12px] text-destructive">
          {errors._form}
        </div>
      )}

      <div className="flex justify-end gap-2 pt-2">
        <Button type="button" variant="outline" onClick={onCancel} disabled={submitting}>취소</Button>
        <Button type="submit" disabled={submitting}>
          {submitting ? '가져오는 중…' : '가져오기'}
        </Button>
      </div>
    </form>
  )
}
```

- [ ] **Step 2: Confirm `ApiError` shape exists in types**

Run: `grep -n "ApiError" web/src/types/api.ts web/src/lib/api.ts`. If not present, the API client likely throws `Error` with `message` only — adjust the catch block to read `(err as Error).message` and skip the per-code mapping (use only `_form`).

- [ ] **Step 3: Build frontend**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/compose/GitImportForm.tsx
git commit -m "web: GitImportForm — 단일 폼, 실시간 검증, 에러 코드별 필드 매핑"
```

---

## Task 15: Wire GitImportForm into 새 스택 dialog

**Files:**
- Modify: `web/src/pages/docker/DockerStacks.tsx`

- [ ] **Step 1: Add tabs to "새 스택 추가" dialog**

In `web/src/pages/docker/DockerStacks.tsx`, find the existing "새 스택 추가" / "+ 새 스택" Dialog. Wrap its current body in a shadcn `Tabs` with two values: "manual" (existing flow) and "git".

Add to imports near top:
```typescript
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'  // may already be imported
import { GitImportForm } from '@/components/compose/GitImportForm'
```

Find the Dialog body (search the file for `새 스택` or `setShowAddStack`). Wrap the existing form in a Tabs structure:

```tsx
<Tabs defaultValue="manual">
  <TabsList>
    <TabsTrigger value="manual">수동 작성</TabsTrigger>
    <TabsTrigger value="git">git에서 가져오기</TabsTrigger>
  </TabsList>
  <TabsContent value="manual">
    {/* (existing form, unchanged) */}
  </TabsContent>
  <TabsContent value="git">
    <GitImportForm
      onSuccess={(projectName) => {
        setShowAddStack(false)  // existing close handler
        // navigate to imported stack — uses existing pattern in this file
        navigate(`/docker/stacks?selected=${encodeURIComponent(projectName)}`)
        loadStacks()  // refresh list
      }}
      onCancel={() => setShowAddStack(false)}
    />
  </TabsContent>
</Tabs>
```

(`setShowAddStack`, `navigate`, `loadStacks` names: confirm with `grep -n setShowAddStack web/src/pages/docker/DockerStacks.tsx` and adjust to match the actual identifiers.)

- [ ] **Step 2: Build + lint frontend**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 3: Run full Go test suite to confirm no backend regression**

Run: `cd /opt/stacks/SFPanel && go test ./... -count=1`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/docker/DockerStacks.tsx
git commit -m "web: 새 스택 dialog — 수동 작성 / git에서 가져오기 탭"
```

---

## Task 16: Manual smoke test on the live panel

- [ ] **Step 1: Build + deploy**

```bash
cd /opt/stacks/SFPanel
make build
sudo cp /usr/local/bin/sfpanel /usr/local/bin/sfpanel.bak.before-d-phase1
sudo systemctl stop sfpanel
sudo cp ./sfpanel /usr/local/bin/sfpanel
sudo systemctl start sfpanel
sleep 4
sudo systemctl is-active sfpanel
/usr/local/bin/sfpanel version
```

Expected: `active`, version reflects new commit.

- [ ] **Step 2: Verify diff endpoint via curl**

```bash
TOKEN=$(sudo /tmp/minttoken)  # or another method to mint admin JWT
# Pick an existing stack name from the panel; e.g. "nginxproxyguard"
PROJECT=nginxproxyguard
# Tweak any one image tag to introduce a fake change
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d '{"yaml":"services:\n  api:\n    image: nginx:1.25\n"}' \
     "http://127.0.0.1:9443/api/v1/compose/$PROJECT/diff" | python3 -m json.tool | head -30
```

Expected: `summary.modified ≥ 1`, `by_category.image` non-empty.

- [ ] **Step 3: Verify import endpoint via curl (small public test repo)**

```bash
curl -s -X POST -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d '{"url":"https://github.com/dockersamples/example-voting-app.git","branch":"main","path":"docker-compose.yml","name":"voting-test"}' \
     "http://127.0.0.1:9443/api/v1/compose/import" | python3 -m json.tool
```

Expected: `success: true, data.project_name: "voting-test"`. Check `/var/lib/sfpanel/compose/voting-test/docker-compose.yml` was written.

- [ ] **Step 4: UI smoke (Playwright or browser)**

Navigate to `http://192.168.1.203:9443/docker/stacks`, login, click an existing stack:

- 에디터 탭 → 임의로 image 태그 변경 → "변경사항 미리보기" 클릭.
- Sheet가 오른쪽에서 슬라이드 → SummaryHeader (변경 1) → Accordion에서 "이미지" 펼침 → 변경 행 보임.
- "원본 텍스트" 탭 클릭 → Monaco DiffEditor 보임.
- "닫기" 클릭 → Sheet 닫힘, 편집 상태 유지.

"새 스택" 클릭 → "git에서 가져오기" 탭:

- URL: `https://github.com/dockersamples/example-voting-app.git`
- name: `voting-ui-test`
- "가져오기" → toast "voting-ui-test 스택을 가져왔습니다" + drawer 자동 열림.

검증 후 voting-ui-test 스택 삭제 (정리).

- [ ] **Step 5: Cleanup test stack**

```bash
curl -s -X DELETE -H "Authorization: Bearer $TOKEN" \
     "http://127.0.0.1:9443/api/v1/compose/voting-test"
curl -s -X DELETE -H "Authorization: Bearer $TOKEN" \
     "http://127.0.0.1:9443/api/v1/compose/voting-ui-test"
```

- [ ] **Step 6: Commit / push**

No code change in this task — implementation is already on `main` from previous tasks. Push if not already:

```bash
git push origin main
```

---

## Self-Review

### Spec coverage

- ✅ Mode 1 (Diff before apply) → Tasks 1–6, 11–13
- ✅ Mode 2 (One-shot Git import) → Tasks 7–9, 14–15
- ✅ Mode 3 deferred → not in plan (matches spec out-of-scope)
- ✅ DB 변경 없음 → no migrations in plan
- ✅ PAT 저장 안 함 → token only on `ImportRequest` struct, dropped after handler returns
- ✅ DiffPanel = Right Sheet → Task 13
- ✅ Accordion 카테고리 → Task 12 (default-open for changed, `disabled={isEmpty}`)
- ✅ 단일 폼 import → Task 14
- ✅ 색맹 안전 (`+`/`−`/`→` prefix) → Tasks 11–12
- ✅ 6 카테고리 (image/ports/volumes/env/restart/healthcheck) → Tasks 1–4
- ✅ 에러 매핑 (GIT_AUTH_FAILED → 401 등) → Task 5 + Task 9 handler + Task 14 frontend
- ✅ Cluster `?node=` 자동 작동 → routes are unary JSON under `authorized` group
- ✅ Manual smoke → Task 16

### Placeholder scan

검토 결과: 모든 step에 실제 코드 / 명령 / 기대 출력이 있음. "TBD" / "appropriate handling" 류 없음.

### Type consistency

- Backend `ImportRequest{URL, Branch, Path, Token, Name}` ↔ Frontend `ImportRequest{url, branch?, path?, token?, name}`: JSON 태그가 lowercase로 매핑되므로 일치.
- Backend `DiffResult{Summary, ByCategory, RawDiff}` ↔ Frontend `DiffResult{summary, by_category, raw_diff}`: JSON 태그 일치.
- 카테고리 키 일치: Backend map keys (`"image"`, `"ports"`, `"volumes"`, `"env"`, `"restart"`, `"healthcheck"`) === Frontend `DiffByCategory` 필드 이름.
- 에러 코드 일치: Backend constants (`ErrGitAuthFailed = "GIT_AUTH_FAILED"` 등) === Frontend literal strings in `GitImportForm` (`'GIT_AUTH_FAILED'` 등). 관리상 frontend에 enum/유니온 추가하면 더 안전하지만 16-task scope 안에 남.

---

## Execution

Plan complete and saved to `docs/superpowers/plans/2026-05-04-compose-diff-and-git-import-plan.md`.

Two execution options:

**1. Subagent-Driven (recommended)** — Fresh subagent per task, two-stage review (spec + quality), fast iteration. Same process as the observability plan.

**2. Inline Execution** — All tasks in this session via `superpowers:executing-plans`, batch with checkpoints.

Which approach?
