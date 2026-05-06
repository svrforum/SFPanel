# Healthcheck Composer Implementation Plan (Theme D Phase 2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A ❤️ icon on each service row in the stack drawer opens a dialog that writes a `healthcheck:` block into the compose YAML — backed by yaml.v3 Node-API round-trip preservation, backup-before-write, pre-flight re-parse, and concurrent-edit detection.

**Architecture:** Pure-function `ApplyHealthcheck` / `ParseHealthcheck` in `internal/feature/compose/healthcheck.go` operate on yaml.v3 `*yaml.Node` trees so anchors, comments, and key order survive. A thin REST handler `PUT /api/v1/compose/:project/healthcheck/:service` reads the on-disk YAML, validates a `base_yaml_sha256` precondition, calls the pure function, re-parses the result for safety, writes a timestamped backup, and writes the new YAML. The composer never auto-deploys — it returns the new YAML to the editor where the existing diff/save flow takes over.

**Tech Stack:** Go 1.25 + gopkg.in/yaml.v3 Node API + crypto/sha256; React 19 + shadcn (Dialog, RadioGroup-style buttons, Input, Label) + lucide HeartPulse.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/feature/compose/healthcheck.go` | `HealthcheckSpec` type, `ApplyHealthcheck`, `ParseHealthcheck`, duration validation. Pure: no I/O. |
| `internal/feature/compose/healthcheck_test.go` | Table tests for both pure functions + round-trip property test. |
| `internal/feature/compose/handler.go` (modify) | New method `ApplyHealthcheck(c echo.Context)`. |
| `internal/feature/compose/handler_test.go` (modify or create) | Handler validation + sha256 precondition tests. |
| `internal/api/router.go` (modify) | Register `PUT /compose/:project/healthcheck/:service`. |
| `web/src/types/api.ts` (modify) | `HealthcheckSpec` + `HealthcheckTestType` types. |
| `web/src/lib/api.ts` (modify) | `applyHealthcheck` method. |
| `web/src/components/compose/HealthcheckComposerDialog.tsx` | Dialog component. |
| `web/src/pages/docker/DockerStacks.tsx` (modify) | Add HeartPulse icon to services-row actions, mount dialog. |

---

## Task 1: Pure function — `ApplyHealthcheck` (basic add path)

**Files:**
- Create: `internal/feature/compose/healthcheck.go`
- Create: `internal/feature/compose/healthcheck_test.go`

- [ ] **Step 1: Write failing test**

Create `internal/feature/compose/healthcheck_test.go`:

```go
package compose

import (
	"strings"
	"testing"
)

const sampleCompose = `services:
  jellyfin:
    image: jellyfin/jellyfin:latest
    container_name: jellyfin
    ports:
      - '8096:8096/tcp'
    restart: unless-stopped
`

func TestApplyHealthcheck_AddCMDSHELL(t *testing.T) {
	spec := HealthcheckSpec{
		TestType:    "CMD-SHELL",
		TestValue:   "curl -f http://localhost:8096/health || exit 1",
		Interval:    "30s",
		Timeout:     "10s",
		Retries:     3,
		StartPeriod: "30s",
	}
	got, err := ApplyHealthcheck(sampleCompose, "jellyfin", spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "healthcheck:") {
		t.Fatalf("missing healthcheck block:\n%s", got)
	}
	if !strings.Contains(got, "CMD-SHELL") {
		t.Errorf("missing CMD-SHELL marker:\n%s", got)
	}
	if !strings.Contains(got, "interval: 30s") {
		t.Errorf("missing interval:\n%s", got)
	}
	if !strings.Contains(got, "retries: 3") {
		t.Errorf("missing retries:\n%s", got)
	}
}
```

- [ ] **Step 2: Run, expect compile failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestApplyHealthcheck_AddCMDSHELL -count=1`
Expected: FAIL — `HealthcheckSpec`/`ApplyHealthcheck` undefined.

- [ ] **Step 3: Implement `healthcheck.go`**

Create `internal/feature/compose/healthcheck.go`:

```go
package compose

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// HealthcheckSpec is the input shape from the composer dialog.
type HealthcheckSpec struct {
	TestType    string `json:"test_type"`    // "CMD-SHELL" | "CMD" | "NONE"
	TestValue   string `json:"test_value"`   // command for CMD-SHELL; pipe-separated argv for CMD; ignored for NONE
	Interval    string `json:"interval"`     // Go duration: "30s", "1m30s"
	Timeout     string `json:"timeout"`
	Retries     int    `json:"retries"`
	StartPeriod string `json:"start_period"`
}

// ErrServiceNotFound is returned when the named service doesn't exist.
var ErrServiceNotFound = errors.New("compose: service not found")

// ErrHealthcheckExists is returned when a healthcheck is already
// present and replace=false. The composer surfaces this so the
// operator can opt in to overwriting.
var ErrHealthcheckExists = errors.New("compose: healthcheck already present (set replace=true to overwrite)")

// ApplyHealthcheck inserts or replaces the healthcheck block on the
// named service. yaml.v3 Node API is used so anchors, comments, and
// key ordering survive untouched.
func ApplyHealthcheck(yamlContent string, service string, spec HealthcheckSpec, replace bool) (string, error) {
	if err := spec.validate(); err != nil {
		return "", err
	}

	var root yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &root); err != nil {
		return "", fmt.Errorf("parse compose: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return "", errors.New("empty compose document")
	}

	svcNode, err := findServiceNode(&root, service)
	if err != nil {
		return "", err
	}

	hcKeyNode, hcValNode := findChild(svcNode, "healthcheck")
	if hcKeyNode != nil && !replace {
		return "", ErrHealthcheckExists
	}

	newHC := buildHealthcheckNode(spec)
	if hcKeyNode == nil {
		// Append healthcheck at end of service map.
		svcNode.Content = append(svcNode.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "healthcheck"},
			newHC,
		)
	} else {
		// Replace the existing value node, preserving key node (and any
		// comments attached to it).
		_ = hcValNode
		// Find the val index to swap in place.
		for i := 0; i+1 < len(svcNode.Content); i += 2 {
			if svcNode.Content[i] == hcKeyNode {
				svcNode.Content[i+1] = newHC
				break
			}
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return "", fmt.Errorf("encode compose: %w", err)
	}
	enc.Close()
	return buf.String(), nil
}

// validate rejects malformed input before it touches the YAML tree.
func (s HealthcheckSpec) validate() error {
	switch s.TestType {
	case "CMD-SHELL", "CMD", "NONE":
	default:
		return fmt.Errorf("invalid test_type %q (want CMD-SHELL|CMD|NONE)", s.TestType)
	}
	if s.TestType == "NONE" {
		return nil // other fields ignored
	}
	if strings.TrimSpace(s.TestValue) == "" {
		return errors.New("test_value required for CMD-SHELL and CMD")
	}
	for _, d := range []struct{ name, val string }{
		{"interval", s.Interval}, {"timeout", s.Timeout}, {"start_period", s.StartPeriod},
	} {
		if d.val == "" {
			return fmt.Errorf("%s required", d.name)
		}
		if _, err := time.ParseDuration(d.val); err != nil {
			return fmt.Errorf("%s must be a Go duration (e.g. 30s, 1m30s): %w", d.name, err)
		}
	}
	if s.Retries <= 0 {
		return errors.New("retries must be positive")
	}
	return nil
}

// findServiceNode returns the *yaml.Node for `services.<name>` (a
// MappingNode), or ErrServiceNotFound.
func findServiceNode(root *yaml.Node, service string) (*yaml.Node, error) {
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, errors.New("compose root is not a mapping")
	}
	_, services := findChild(doc, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil, errors.New("services block missing or malformed")
	}
	_, svc := findChild(services, service)
	if svc == nil {
		return nil, fmt.Errorf("%w: %s", ErrServiceNotFound, service)
	}
	if svc.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("service %s is not a mapping", service)
	}
	return svc, nil
}

// findChild looks up a key in a MappingNode and returns (keyNode, valueNode)
// or (nil, nil) if absent. Mapping nodes interleave keys and values in
// Content: [k1, v1, k2, v2, ...].
func findChild(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return k, m.Content[i+1]
		}
	}
	return nil, nil
}

// buildHealthcheckNode constructs the yaml.Node for the healthcheck
// MappingNode value. The shape:
//
//	healthcheck:
//	  test: ["CMD-SHELL", "<value>"]    # or ["CMD", arg1, arg2, ...] or ["NONE"]
//	  interval: 30s
//	  timeout: 10s
//	  retries: 3
//	  start_period: 30s
func buildHealthcheckNode(s HealthcheckSpec) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	addKV := func(k string, v *yaml.Node) {
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
			v,
		)
	}
	addKV("test", buildTestNode(s))
	if s.TestType == "NONE" {
		return m
	}
	addKV("interval", scalar(s.Interval))
	addKV("timeout", scalar(s.Timeout))
	addKV("retries", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", s.Retries)})
	addKV("start_period", scalar(s.StartPeriod))
	return m
}

func buildTestNode(s HealthcheckSpec) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	switch s.TestType {
	case "NONE":
		seq.Content = []*yaml.Node{scalar("NONE")}
	case "CMD-SHELL":
		seq.Content = []*yaml.Node{scalar("CMD-SHELL"), scalar(s.TestValue)}
	case "CMD":
		argv := strings.Split(s.TestValue, "|")
		seq.Content = append(seq.Content, scalar("CMD"))
		for _, a := range argv {
			seq.Content = append(seq.Content, scalar(a))
		}
	}
	return seq
}

func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}
```

- [ ] **Step 4: Run, expect tests pass**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestApplyHealthcheck_AddCMDSHELL -count=1 -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/feature/compose/healthcheck.go internal/feature/compose/healthcheck_test.go
git commit -m "compose: HealthcheckSpec + ApplyHealthcheck (basic add)"
```

---

## Task 2: ApplyHealthcheck — replace + missing service + NONE + CMD

**Files:**
- Modify: `internal/feature/compose/healthcheck_test.go` (append tests)

- [ ] **Step 1: Append failing tests**

Append to `internal/feature/compose/healthcheck_test.go`:

```go
const composeWithHealthcheck = `services:
  jellyfin:
    image: jellyfin/jellyfin:latest
    healthcheck:
      test: ["CMD-SHELL", "echo old"]
      interval: 60s
      timeout: 5s
      retries: 5
      start_period: 10s
    restart: unless-stopped
`

func TestApplyHealthcheck_RejectsExistingWithoutReplace(t *testing.T) {
	spec := HealthcheckSpec{
		TestType: "CMD-SHELL", TestValue: "echo new",
		Interval: "30s", Timeout: "10s", Retries: 3, StartPeriod: "30s",
	}
	_, err := ApplyHealthcheck(composeWithHealthcheck, "jellyfin", spec, false)
	if !errors.Is(err, ErrHealthcheckExists) {
		t.Fatalf("got %v want ErrHealthcheckExists", err)
	}
}

func TestApplyHealthcheck_ReplaceExisting(t *testing.T) {
	spec := HealthcheckSpec{
		TestType: "CMD-SHELL", TestValue: "echo new",
		Interval: "30s", Timeout: "10s", Retries: 3, StartPeriod: "30s",
	}
	got, err := ApplyHealthcheck(composeWithHealthcheck, "jellyfin", spec, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "echo old") {
		t.Errorf("old test value still present:\n%s", got)
	}
	if !strings.Contains(got, "echo new") {
		t.Errorf("new test value missing:\n%s", got)
	}
}

func TestApplyHealthcheck_ServiceMissing(t *testing.T) {
	spec := HealthcheckSpec{
		TestType: "CMD-SHELL", TestValue: "x",
		Interval: "30s", Timeout: "10s", Retries: 3, StartPeriod: "30s",
	}
	_, err := ApplyHealthcheck(sampleCompose, "nonexistent", spec, false)
	if !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("got %v want ErrServiceNotFound", err)
	}
}

func TestApplyHealthcheck_NONE(t *testing.T) {
	spec := HealthcheckSpec{TestType: "NONE"}
	got, err := ApplyHealthcheck(sampleCompose, "jellyfin", spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "test:") || !strings.Contains(got, "NONE") {
		t.Errorf("missing test: [NONE]:\n%s", got)
	}
	// NONE block must NOT include interval/timeout/retries/start_period.
	if strings.Contains(got, "interval:") {
		t.Errorf("NONE block should not include interval:\n%s", got)
	}
}

func TestApplyHealthcheck_CMDArgv(t *testing.T) {
	spec := HealthcheckSpec{
		TestType: "CMD", TestValue: "curl|-f|http://localhost:8096/health",
		Interval: "30s", Timeout: "10s", Retries: 3, StartPeriod: "30s",
	}
	got, err := ApplyHealthcheck(sampleCompose, "jellyfin", spec, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "CMD") || !strings.Contains(got, "curl") || !strings.Contains(got, "/health") {
		t.Errorf("CMD argv not flattened correctly:\n%s", got)
	}
}
```

Add `"errors"` to the test file's imports.

- [ ] **Step 2: Run, expect tests pass**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestApplyHealthcheck -count=1 -v`
Expected: 5 sub-tests PASS (the 4 new + Task 1).

- [ ] **Step 3: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/feature/compose/healthcheck_test.go
git commit -m "compose: ApplyHealthcheck tests for replace/missing/NONE/CMD argv"
```

---

## Task 3: ParseHealthcheck + round-trip preservation property test

**Files:**
- Modify: `internal/feature/compose/healthcheck.go` (append `ParseHealthcheck`)
- Modify: `internal/feature/compose/healthcheck_test.go` (append tests)

- [ ] **Step 1: Append failing tests**

```go
func TestParseHealthcheck_PresentCMDSHELL(t *testing.T) {
	got, ok, err := ParseHealthcheck(composeWithHealthcheck, "jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("expected healthcheck found")
	}
	if got.TestType != "CMD-SHELL" {
		t.Errorf("test_type: got %q want CMD-SHELL", got.TestType)
	}
	if got.TestValue != "echo old" {
		t.Errorf("test_value: got %q want 'echo old'", got.TestValue)
	}
	if got.Interval != "60s" || got.Timeout != "5s" || got.Retries != 5 || got.StartPeriod != "10s" {
		t.Errorf("durations/retries mismatched: %+v", got)
	}
}

func TestParseHealthcheck_Absent(t *testing.T) {
	_, ok, err := ParseHealthcheck(sampleCompose, "jellyfin")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("expected absent")
	}
}

// TestApplyHealthcheck_RoundTripPreservesComments — apply with
// replace=true using the SAME spec parsed back should produce a
// document where everything except the healthcheck block is
// byte-identical to the original.
func TestApplyHealthcheck_RoundTripPreservesComments(t *testing.T) {
	yamlWithComments := `# top comment
services:
  jellyfin:                # service line comment
    image: jellyfin/jellyfin:latest
    container_name: jellyfin
    ports:
      - '8096:8096/tcp'    # port comment
    restart: unless-stopped
`
	spec := HealthcheckSpec{
		TestType: "CMD-SHELL", TestValue: "echo ok",
		Interval: "30s", Timeout: "10s", Retries: 3, StartPeriod: "30s",
	}
	got, err := ApplyHealthcheck(yamlWithComments, "jellyfin", spec, false)
	if err != nil {
		t.Fatal(err)
	}
	// Comments must survive.
	if !strings.Contains(got, "# top comment") {
		t.Errorf("top comment lost:\n%s", got)
	}
	if !strings.Contains(got, "service line comment") {
		t.Errorf("service line comment lost:\n%s", got)
	}
	if !strings.Contains(got, "port comment") {
		t.Errorf("port comment lost:\n%s", got)
	}
	// Existing keys must still be present.
	if !strings.Contains(got, "container_name: jellyfin") {
		t.Errorf("container_name lost:\n%s", got)
	}
}
```

- [ ] **Step 2: Run, expect compile/parse failure**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run "TestParseHealthcheck|TestApplyHealthcheck_RoundTrip" -count=1`
Expected: FAIL — `ParseHealthcheck` undefined.

- [ ] **Step 3: Append `ParseHealthcheck` to `healthcheck.go`**

```go
// ParseHealthcheck reads the existing healthcheck for a service. Returns
// (zero-value, false, nil) if the service has no healthcheck block.
func ParseHealthcheck(yamlContent string, service string) (HealthcheckSpec, bool, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(yamlContent), &root); err != nil {
		return HealthcheckSpec{}, false, fmt.Errorf("parse compose: %w", err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return HealthcheckSpec{}, false, nil
	}
	svc, err := findServiceNode(&root, service)
	if err != nil {
		return HealthcheckSpec{}, false, err
	}
	_, hc := findChild(svc, "healthcheck")
	if hc == nil || hc.Kind != yaml.MappingNode {
		return HealthcheckSpec{}, false, nil
	}

	var spec HealthcheckSpec
	_, testNode := findChild(hc, "test")
	if testNode != nil && testNode.Kind == yaml.SequenceNode && len(testNode.Content) > 0 {
		head := testNode.Content[0].Value
		switch head {
		case "NONE":
			spec.TestType = "NONE"
		case "CMD-SHELL":
			spec.TestType = "CMD-SHELL"
			if len(testNode.Content) >= 2 {
				spec.TestValue = testNode.Content[1].Value
			}
		case "CMD":
			spec.TestType = "CMD"
			parts := make([]string, 0, len(testNode.Content)-1)
			for _, n := range testNode.Content[1:] {
				parts = append(parts, n.Value)
			}
			spec.TestValue = strings.Join(parts, "|")
		}
	}
	if _, n := findChild(hc, "interval"); n != nil {
		spec.Interval = n.Value
	}
	if _, n := findChild(hc, "timeout"); n != nil {
		spec.Timeout = n.Value
	}
	if _, n := findChild(hc, "retries"); n != nil {
		fmt.Sscanf(n.Value, "%d", &spec.Retries)
	}
	if _, n := findChild(hc, "start_period"); n != nil {
		spec.StartPeriod = n.Value
	}
	return spec, true, nil
}
```

- [ ] **Step 4: Run, expect tests pass**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -count=1 -v`
Expected: all healthcheck tests PASS (8 total at this point).

- [ ] **Step 5: Lint**

Run: `/home/dalso/go/bin/golangci-lint run ./internal/feature/compose/...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/feature/compose/healthcheck.go internal/feature/compose/healthcheck_test.go
git commit -m "compose: ParseHealthcheck + round-trip comment preservation test"
```

---

## Task 4: REST handler with stability guarantees

**Files:**
- Modify: `internal/feature/compose/handler.go` (append `ApplyHealthcheck` method)
- Create: `internal/feature/compose/healthcheck_handler_test.go`

- [ ] **Step 1: Add a public path resolver helper**

The handler needs to resolve `<stacksPath>/<project>/docker-compose.yml`. The `ComposeManager.resolveComposeFilePath` method exists but is unexported. Add a public wrapper in `internal/docker/compose.go` just above the existing `resolveComposeFilePath`:

```go
// ResolveComposeFile is the public entry to find the compose YAML file
// for a project. Returns ("", "") if the project directory contains no
// recognizable compose file. Used by feature handlers that need to
// read+write compose YAML directly (Theme D Phase 2 healthcheck composer).
func (m *ComposeManager) ResolveComposeFile(ctx context.Context, name string) (yamlPath string, dir string) {
	return m.resolveComposeFilePath(ctx, name)
}
```

(Add immediately after the existing `resolveComposeFilePath` function.)

- [ ] **Step 2: Write failing handler test**

Create `internal/feature/compose/healthcheck_handler_test.go`:

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

// TestApplyHealthcheckHandler_RejectsBadDuration — validation runs
// before any disk I/O, so we can drive it with a bare Handler{}.
func TestApplyHealthcheckHandler_RejectsBadDuration(t *testing.T) {
	body := bytes.NewBufferString(`{"test_type":"CMD-SHELL","test_value":"x","interval":"30","timeout":"10s","retries":3,"start_period":"30s","replace":false}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/compose/foo/healthcheck/jellyfin", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetParamNames("project", "service")
	c.SetParamValues("foo", "jellyfin")

	h := &Handler{}
	_ = h.ApplyHealthcheck(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, false, resp["success"])
}

// TestApplyHealthcheckHandler_RejectsMissingTestType
func TestApplyHealthcheckHandler_RejectsMissingTestType(t *testing.T) {
	body := bytes.NewBufferString(`{"test_value":"x","interval":"30s","timeout":"10s","retries":3,"start_period":"30s"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/compose/foo/healthcheck/svc", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetParamNames("project", "service")
	c.SetParamValues("foo", "svc")

	h := &Handler{}
	_ = h.ApplyHealthcheck(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}
```

- [ ] **Step 3: Run, expect compile fail**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -run TestApplyHealthcheckHandler -count=1`
Expected: FAIL — `(*Handler).ApplyHealthcheck` undefined.

- [ ] **Step 4: Implement handler**

In `internal/feature/compose/handler.go`, append a new method (and add `crypto/sha256`, `encoding/hex`, `errors`, `os`, `path/filepath`, `strconv`, `time` to imports if missing — many are likely already imported):

```go
// ApplyHealthcheck inserts or replaces the healthcheck block on a
// service of the named compose project. Implements the five stability
// guarantees per docs/superpowers/specs/2026-05-06-healthcheck-composer-design.md:
//
//  1. yaml.v3 Node-API round-trip preservation (in ApplyHealthcheck pure func)
//  2. Backup-before-write to docker-compose.yml.bak.healthcheck.<unix-ms>
//  3. Pre-flight re-parse of the new YAML before writing to disk
//  4. base_yaml_sha256 concurrent-edit precondition
//  5. No automatic deploy — returns the new YAML and lets the editor flow
//     ship it on the operator's explicit Save & Deploy.
func (h *Handler) ApplyHealthcheck(c echo.Context) error {
	project := c.Param("project")
	service := c.Param("service")
	if project == "" || service == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "project and service required")
	}

	var req struct {
		HealthcheckSpec
		Replace        bool   `json:"replace"`
		BaseYAMLSHA256 string `json:"base_yaml_sha256"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	// Validate spec early — never touch disk on bad input.
	if err := req.HealthcheckSpec.validate(); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, err.Error())
	}

	if h.Compose == nil {
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "compose manager not configured")
	}

	yamlPath, _ := h.Compose.ResolveComposeFile(c.Request().Context(), project)
	if yamlPath == "" {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "compose file not found for project")
	}
	original, err := os.ReadFile(yamlPath)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrReadError, response.SanitizeOutput(err.Error()))
	}

	// Stability #4: concurrent-edit detection.
	if req.BaseYAMLSHA256 != "" {
		sum := sha256.Sum256(original)
		if hex.EncodeToString(sum[:]) != req.BaseYAMLSHA256 {
			return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists,
				"compose file changed externally — reload before applying healthcheck")
		}
	}

	// Pure-function transform.
	newYAML, err := ApplyHealthcheck(string(original), service, req.HealthcheckSpec, req.Replace)
	switch {
	case errors.Is(err, ErrServiceNotFound):
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, err.Error())
	case errors.Is(err, ErrHealthcheckExists):
		return response.Fail(c, http.StatusConflict, response.ErrAlreadyExists, err.Error())
	case err != nil:
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, response.SanitizeOutput(err.Error()))
	}

	// Stability #3: pre-flight re-parse.
	var sanity yaml.Node
	if err := yaml.Unmarshal([]byte(newYAML), &sanity); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError,
			"healthcheck transform produced unparseable YAML: "+response.SanitizeOutput(err.Error()))
	}

	// Stability #2: backup before write.
	backupPath := yamlPath + ".bak.healthcheck." + strconv.FormatInt(time.Now().UnixMilli(), 10)
	if err := os.WriteFile(backupPath, original, 0o644); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrWriteError,
			"backup failed: "+response.SanitizeOutput(err.Error()))
	}

	// Atomic write via temp + rename.
	tmp := yamlPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(newYAML), 0o644); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrWriteError,
			response.SanitizeOutput(err.Error()))
	}
	if err := os.Rename(tmp, yamlPath); err != nil {
		_ = os.Remove(tmp)
		return response.Fail(c, http.StatusInternalServerError, response.ErrWriteError,
			response.SanitizeOutput(err.Error()))
	}

	_ = filepath.Base // keep import alive if not used elsewhere

	return response.OK(c, map[string]any{
		"yaml":        newYAML,
		"backup_path": backupPath,
	})
}
```

Add the missing imports to the top of `handler.go` — required new imports: `crypto/sha256`, `encoding/hex`, `errors`, `os`, `strconv`, `time`, and `gopkg.in/yaml.v3` (alias as `yaml` if not already). `filepath` is used only for the dead-code reference; remove that line if your linter complains.

- [ ] **Step 5: Run handler tests**

Run: `cd /opt/stacks/SFPanel && go test ./internal/feature/compose/ -count=1 -v`
Expected: all PASS (≥ 10 tests in this package).

- [ ] **Step 6: Lint**

Run: `/home/dalso/go/bin/golangci-lint run ./internal/feature/compose/... ./internal/docker/...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/feature/compose/handler.go internal/feature/compose/healthcheck_handler_test.go internal/docker/compose.go
git commit -m "compose: PUT /healthcheck/:service handler (5 stability guarantees)"
```

---

## Task 5: Register route

**Files:**
- Modify: `internal/api/router.go`

- [ ] **Step 1: Add route alongside other compose routes**

In `internal/api/router.go`, find the `compose := authorized.Group("/compose")` block (around line 525-540 in the existing `compose.PUT("/:project", composeHandler.UpdateProject)` neighborhood). Right after `compose.POST("/:project/diff", composeHandler.DiffStack)`, add:

```go
		compose.PUT("/:project/healthcheck/:service", composeHandler.ApplyHealthcheck)
```

- [ ] **Step 2: Build + tests**

Run: `cd /opt/stacks/SFPanel && go build ./... && go test ./internal/api/... ./internal/feature/compose/... -count=1`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add internal/api/router.go
git commit -m "router: PUT /compose/:project/healthcheck/:service"
```

---

## Task 6: Frontend types + API method

**Files:**
- Modify: `web/src/types/api.ts`
- Modify: `web/src/lib/api.ts`

- [ ] **Step 1: Append types**

In `web/src/types/api.ts`, append after the last interface:

```typescript
export type HealthcheckTestType = 'CMD-SHELL' | 'CMD' | 'NONE'

export interface HealthcheckSpec {
  test_type: HealthcheckTestType
  test_value: string
  interval: string
  timeout: string
  retries: number
  start_period: string
}
```

- [ ] **Step 2: Add API method**

In `web/src/lib/api.ts`, add to imports near other compose types:
```typescript
import type {
  // ... existing ...
  HealthcheckSpec,
} from '@/types/api'
```

Inside the `ApiClient` class, near other compose methods (search for `composeUpStream` or similar):

```typescript
  applyHealthcheck(
    project: string,
    service: string,
    spec: HealthcheckSpec,
    replace: boolean,
    baseYamlSha256: string,
  ) {
    return this.request<{ yaml: string; backup_path: string }>(
      `/compose/${encodeURIComponent(project)}/healthcheck/${encodeURIComponent(service)}`,
      {
        method: 'PUT',
        body: JSON.stringify({ ...spec, replace, base_yaml_sha256: baseYamlSha256 }),
      },
    )
  }
```

- [ ] **Step 3: Build + lint frontend**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add web/src/types/api.ts web/src/lib/api.ts
git commit -m "web: types + api client for healthcheck composer"
```

---

## Task 7: HealthcheckComposerDialog

**Files:**
- Create: `web/src/components/compose/HealthcheckComposerDialog.tsx`

- [ ] **Step 1: Create the dialog**

```tsx
import { useEffect, useState } from 'react'
import { toast } from 'sonner'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'
import type { HealthcheckSpec, HealthcheckTestType } from '@/types/api'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  project: string
  service: string
  baseYaml: string
  onApplied: (newYaml: string) => void
}

const DEFAULTS: HealthcheckSpec = {
  test_type: 'CMD-SHELL',
  test_value: '',
  interval: '30s',
  timeout: '10s',
  retries: 3,
  start_period: '30s',
}

const DURATION_RE = /^\d+(\.\d+)?(ns|us|µs|ms|s|m|h)([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))*$/

async function sha256Hex(s: string): Promise<string> {
  const buf = new TextEncoder().encode(s)
  const hash = await crypto.subtle.digest('SHA-256', buf)
  return Array.from(new Uint8Array(hash))
    .map((b) => b.toString(16).padStart(2, '0'))
    .join('')
}

export function HealthcheckComposerDialog({
  open,
  onOpenChange,
  project,
  service,
  baseYaml,
  onApplied,
}: Props) {
  const [spec, setSpec] = useState<HealthcheckSpec>(DEFAULTS)
  const [hasExisting, setHasExisting] = useState(false)
  const [replace, setReplace] = useState(false)
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (!open) return
    queueMicrotask(() => {
      setSpec(DEFAULTS)
      setHasExisting(false)
      setReplace(false)
      // Cheap client-side detection: look for `healthcheck:` under the service.
      // Backend ParseHealthcheck is the source of truth, but populating the
      // form pre-submit avoids an extra round trip.
      const lines = baseYaml.split('\n')
      let inService = false
      let svcIndent = -1
      let inHealth = false
      const next: Partial<HealthcheckSpec> = {}
      for (const line of lines) {
        const indent = line.match(/^( *)/)?.[1].length ?? 0
        const trimmed = line.trim()
        if (!inService && trimmed === `${service}:`) {
          inService = true
          svcIndent = indent
          continue
        }
        if (inService && indent <= svcIndent && trimmed !== '') break
        if (!inService) continue
        if (trimmed.startsWith('healthcheck:')) {
          inHealth = true
          setHasExisting(true)
          continue
        }
        if (inHealth) {
          if (indent <= svcIndent + 2 && trimmed !== '') {
            inHealth = false
            continue
          }
          if (trimmed.startsWith('test:')) {
            // Cheap parse: ['CMD-SHELL', '...'] or ['NONE'] etc.
            const m = trimmed.match(/test:\s*\[(.*)\]/)
            if (m) {
              const parts = m[1]
                .split(',')
                .map((p) => p.trim().replace(/^['"]|['"]$/g, ''))
              if (parts[0] === 'NONE') {
                next.test_type = 'NONE'
              } else if (parts[0] === 'CMD-SHELL') {
                next.test_type = 'CMD-SHELL'
                next.test_value = parts[1] ?? ''
              } else if (parts[0] === 'CMD') {
                next.test_type = 'CMD'
                next.test_value = parts.slice(1).join('|')
              }
            }
          } else if (trimmed.startsWith('interval:')) {
            next.interval = trimmed.slice(9).trim()
          } else if (trimmed.startsWith('timeout:')) {
            next.timeout = trimmed.slice(8).trim()
          } else if (trimmed.startsWith('retries:')) {
            next.retries = parseInt(trimmed.slice(8).trim(), 10) || 3
          } else if (trimmed.startsWith('start_period:')) {
            next.start_period = trimmed.slice(13).trim()
          }
        }
      }
      if (Object.keys(next).length > 0) {
        setSpec({ ...DEFAULTS, ...next })
      }
    })
  }, [open, baseYaml, service])

  const validDurations =
    spec.test_type === 'NONE' ||
    (DURATION_RE.test(spec.interval) &&
      DURATION_RE.test(spec.timeout) &&
      DURATION_RE.test(spec.start_period))
  const validTest =
    spec.test_type === 'NONE' || spec.test_value.trim() !== ''
  const validReplace = !hasExisting || replace
  const canSubmit = validDurations && validTest && validReplace && spec.retries > 0

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!canSubmit) return
    setSubmitting(true)
    try {
      const baseHash = await sha256Hex(baseYaml)
      const res = await api.applyHealthcheck(project, service, spec, replace || hasExisting, baseHash)
      toast.success('Healthcheck inserted — review and Save & Deploy')
      onApplied(res.yaml)
      onOpenChange(false)
    } catch (err) {
      toast.error((err as Error).message || 'Healthcheck 적용 실패')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Healthcheck — {service}</DialogTitle>
          <DialogDescription>
            compose YAML의 services.{service}.healthcheck 블록을 추가/수정합니다. 자동
            배포되지 않습니다 — 미리보기 후 Save & Deploy 하세요.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={onSubmit} className="space-y-3">
          <div className="space-y-1.5">
            <Label>Test 명령어</Label>
            {(['CMD-SHELL', 'CMD', 'NONE'] as HealthcheckTestType[]).map((t) => (
              <label key={t} className="flex items-start gap-2 cursor-pointer">
                <input
                  type="radio"
                  name="test_type"
                  className="mt-1"
                  checked={spec.test_type === t}
                  onChange={() => setSpec((s) => ({ ...s, test_type: t }))}
                />
                <span className="text-[13px]">
                  <strong>{t}</strong>
                  {t === 'CMD-SHELL' && ' — 셸에서 한 줄 실행'}
                  {t === 'CMD' && ' — 인자 배열 (| 로 구분)'}
                  {t === 'NONE' && ' — 이미지의 baked-in healthcheck 비활성'}
                </span>
              </label>
            ))}
          </div>
          {spec.test_type !== 'NONE' && (
            <div className="space-y-1.5">
              <Label htmlFor="hc-test-value">
                {spec.test_type === 'CMD-SHELL' ? '셸 명령어' : '인자 (| 구분)'}
              </Label>
              <Input
                id="hc-test-value"
                value={spec.test_value}
                onChange={(e) => setSpec((s) => ({ ...s, test_value: e.target.value }))}
                placeholder={
                  spec.test_type === 'CMD-SHELL'
                    ? 'curl -f http://localhost:8096/health || exit 1'
                    : 'curl|-f|http://localhost:8096/health'
                }
                required
              />
            </div>
          )}
          {spec.test_type !== 'NONE' && (
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="hc-interval">주기 (interval)</Label>
                <Input
                  id="hc-interval"
                  value={spec.interval}
                  onChange={(e) => setSpec((s) => ({ ...s, interval: e.target.value }))}
                  placeholder="30s"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="hc-timeout">타임아웃</Label>
                <Input
                  id="hc-timeout"
                  value={spec.timeout}
                  onChange={(e) => setSpec((s) => ({ ...s, timeout: e.target.value }))}
                  placeholder="10s"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="hc-retries">재시도</Label>
                <Input
                  id="hc-retries"
                  type="number"
                  min={1}
                  value={spec.retries}
                  onChange={(e) => setSpec((s) => ({ ...s, retries: parseInt(e.target.value, 10) || 0 }))}
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="hc-start-period">Grace period</Label>
                <Input
                  id="hc-start-period"
                  value={spec.start_period}
                  onChange={(e) => setSpec((s) => ({ ...s, start_period: e.target.value }))}
                  placeholder="30s"
                />
              </div>
            </div>
          )}
          {hasExisting && (
            <label className="flex items-start gap-2 text-[12px] text-muted-foreground">
              <input
                type="checkbox"
                className="mt-0.5"
                checked={replace}
                onChange={(e) => setReplace(e.target.checked)}
              />
              이 service에 이미 healthcheck가 있습니다 — 덮어쓰기
            </label>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
              취소
            </Button>
            <Button type="submit" disabled={submitting || !canSubmit}>
              {submitting ? '적용 중…' : 'Compose YAML에 적용'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
```

- [ ] **Step 2: Build + lint**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add web/src/components/compose/HealthcheckComposerDialog.tsx
git commit -m "web: HealthcheckComposerDialog component"
```

---

## Task 8: DockerStacks integration

**Files:**
- Modify: `web/src/pages/docker/DockerStacks.tsx`

- [ ] **Step 1: Add icon import**

In the lucide imports block at top:

```tsx
import {
  Plus, Play, Square, RotateCw, ArrowUp, RefreshCw,
  Trash2, Terminal, ScrollText, FileText, FileCode, Save, Loader2,
  CheckCircle2, XCircle, Download, Undo2, Search, ChevronLeft, Eye,
  BookmarkPlus, HeartPulse,
} from 'lucide-react'
import { ForkCreateDialog } from '@/components/appstore/ForkCreateDialog'
import { HealthcheckComposerDialog } from '@/components/compose/HealthcheckComposerDialog'
```

- [ ] **Step 2: Add dialog state**

In the `useState` block (search for `forkOpen`):

```tsx
  const [forkOpen, setForkOpen] = useState(false)
  const [healthcheckTarget, setHealthcheckTarget] = useState<ComposeService | null>(null)
```

- [ ] **Step 3: Add icon to services table row (desktop)**

Find the desktop table row block (around line 819-857). Just before the existing `Logs` icon button (search for `setLogService(svc)`), insert:

```tsx
                              <Button variant="ghost" size="icon-xs" title="Healthcheck"
                                onClick={() => setHealthcheckTarget(svc)}>
                                <HeartPulse className="h-3.5 w-3.5" />
                              </Button>
```

- [ ] **Step 4: Add same icon to mobile service card**

Find the mobile service card block (around line 872-902). Same insertion pattern — add the HeartPulse button next to the existing action icons in the mobile card footer.

```tsx
                        <Button variant="ghost" size="icon-xs" title="Healthcheck"
                          onClick={() => setHealthcheckTarget(svc)}>
                          <HeartPulse className="h-3.5 w-3.5" />
                        </Button>
```

- [ ] **Step 5: Mount the dialog near other dialogs**

Find the closing `</div>` of the page body (search for `Diff preview sheet` near the end of the file). Just before the `</div>` that closes the outer page container (after the existing `ForkCreateDialog` mount), add:

```tsx
      {/* Healthcheck composer */}
      {selectedName && healthcheckTarget && (
        <HealthcheckComposerDialog
          open={!!healthcheckTarget}
          onOpenChange={(open) => !open && setHealthcheckTarget(null)}
          project={selectedName}
          service={healthcheckTarget.name}
          baseYaml={editYaml}
          onApplied={(newYaml) => {
            setEditYaml(newYaml)
            setEditorTab('compose')
            setHealthcheckTarget(null)
          }}
        />
      )}
```

- [ ] **Step 6: Build + lint**

Run: `cd /opt/stacks/SFPanel/web && npm run build && npm run lint`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
GIT_AUTHOR_NAME=svrforum GIT_AUTHOR_EMAIL=svrforum.com@gmail.com \
GIT_COMMITTER_NAME=svrforum GIT_COMMITTER_EMAIL=svrforum.com@gmail.com \
git add web/src/pages/docker/DockerStacks.tsx
git commit -m "web: HeartPulse icon on services rows + dialog mount"
```

---

## Task 9: Manual smoke test

- [ ] **Step 1: Build + deploy**

```bash
cd /opt/stacks/SFPanel
make build
sudo cp /usr/local/bin/sfpanel /usr/local/bin/sfpanel.bak.before-d-phase2
sudo systemctl stop sfpanel
sudo cp ./sfpanel /usr/local/bin/sfpanel
sudo systemctl start sfpanel
sleep 4
systemctl is-active sfpanel
/usr/local/bin/sfpanel version
scp ./sfpanel root@192.168.1.118:/tmp/sfpanel.new
ssh root@192.168.1.118 'systemctl stop sfpanel && cp /tmp/sfpanel.new /usr/local/bin/sfpanel && systemctl start sfpanel && sleep 4 && systemctl is-active sfpanel'
```

Expected: both nodes `active`.

- [ ] **Step 2: API smoke (good path)**

Pick a stack without an existing healthcheck (e.g. jellyfin):

```bash
TOKEN=$(sudo /tmp/minttoken | head -1)
ORIG=$(cat /opt/stacks/jellyfin/docker-compose.yml)
HASH=$(printf '%s' "$ORIG" | sha256sum | awk '{print $1}')
curl -s -X PUT -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d "{\"test_type\":\"CMD-SHELL\",\"test_value\":\"curl -f http://localhost:8096/health || exit 1\",\"interval\":\"30s\",\"timeout\":\"10s\",\"retries\":3,\"start_period\":\"30s\",\"replace\":false,\"base_yaml_sha256\":\"$HASH\"}" \
     "http://127.0.0.1:9443/api/v1/compose/jellyfin/healthcheck/jellyfin" | python3 -m json.tool | head -20
```

Expected: `success: true`, response includes `yaml` (with the new healthcheck block) and `backup_path` (a `.bak.healthcheck.<ts>` file in the stack dir).

- [ ] **Step 3: Verify backup file exists**

```bash
ls -la /opt/stacks/jellyfin/docker-compose.yml.bak.healthcheck.* | head -3
```

Expected: at least one `.bak.healthcheck.<ts>` file.

- [ ] **Step 4: API smoke (sha256 mismatch → 409)**

```bash
curl -i -s -X PUT -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d '{"test_type":"CMD-SHELL","test_value":"x","interval":"30s","timeout":"10s","retries":3,"start_period":"30s","base_yaml_sha256":"deadbeef"}' \
     "http://127.0.0.1:9443/api/v1/compose/jellyfin/healthcheck/jellyfin" | head -10
```

Expected: HTTP 409, `code=ALREADY_EXISTS`, message about external edit.

- [ ] **Step 5: API smoke (replace=false on existing → 409)**

```bash
HASH2=$(sha256sum /opt/stacks/jellyfin/docker-compose.yml | awk '{print $1}')
curl -i -s -X PUT -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
     -d "{\"test_type\":\"CMD-SHELL\",\"test_value\":\"x\",\"interval\":\"30s\",\"timeout\":\"10s\",\"retries\":3,\"start_period\":\"30s\",\"replace\":false,\"base_yaml_sha256\":\"$HASH2\"}" \
     "http://127.0.0.1:9443/api/v1/compose/jellyfin/healthcheck/jellyfin" | head -10
```

Expected: HTTP 409, `code=ALREADY_EXISTS`.

- [ ] **Step 6: UI smoke**

Navigate to `http://192.168.1.203:9443/docker/stacks/jellyfin`:

- 서비스 테이블에 ❤️ HeartPulse 아이콘 보임
- 클릭 → Healthcheck 다이얼로그 열림. 기존 healthcheck가 있으면 자동 populate + 덮어쓰기 체크박스 노출.
- CMD-SHELL 라디오 + 명령어 입력 + 4 duration 필드 → "Compose YAML에 적용" 클릭
- toast "Healthcheck inserted — review and Save & Deploy"
- Editor 탭으로 자동 전환 + 새 YAML이 보임
- "변경사항 미리보기" (Phase 1 diff modal) → 정확히 healthcheck 블록만 추가됨 확인
- "Save & Deploy" → stack 재배포

- [ ] **Step 7: Cleanup test backup files**

```bash
sudo rm /opt/stacks/jellyfin/docker-compose.yml.bak.healthcheck.* 2>/dev/null || true
```

(Operator review: keep one if you want to demo rollback.)

- [ ] **Step 8: Push**

```bash
git push origin main
```

---

## Self-Review

### Spec coverage
- ✅ Pure-function `ApplyHealthcheck` + `ParseHealthcheck` → Tasks 1, 2, 3
- ✅ yaml.v3 Node API round-trip preservation → Task 1 implementation, Task 3 round-trip test
- ✅ HealthcheckSpec validation (test_type, durations, retries) → Task 1 (`validate()`)
- ✅ Stability #1 (round-trip) → Task 3 round-trip test
- ✅ Stability #2 (backup before write) → Task 4 handler (`backupPath`)
- ✅ Stability #3 (pre-flight re-parse) → Task 4 handler (`yaml.Unmarshal` after transform)
- ✅ Stability #4 (sha256 precondition) → Task 4 handler + Task 9 step 4 smoke
- ✅ Stability #5 (no auto-deploy) → Task 4 returns yaml only; Task 8 onApplied just sets editor state
- ✅ HealthcheckComposerDialog 5 fields → Task 7
- ✅ ❤️ icon on services row (desktop + mobile) → Task 8
- ✅ Cluster: `?node=` works via existing middleware → no extra task; smoke covers via standard call
- ✅ Defaults (CMD-SHELL, 30s/10s/3/30s) → Task 7 `DEFAULTS`

### Placeholder scan
모든 task에 실제 코드 / 명령 / 기대 출력. Task 8 step 4 (mobile card)에서 "Same insertion pattern" 메모는 같은 코드 블록을 명시했으니 placeholder 아님.

### Type consistency
- Go `HealthcheckSpec` JSON tags (`test_type`, `test_value`, `interval`, `timeout`, `retries`, `start_period`) match TypeScript `HealthcheckSpec` exactly.
- `HealthcheckTestType` literal union (`'CMD-SHELL' | 'CMD' | 'NONE'`) matches Go `validate()` switch.
- `applyHealthcheck` API method signature matches handler endpoint shape.
- `ResolveComposeFile` (new public method on ComposeManager) used only in Task 4; defined in Task 4 step 1.

---

## Execution

Plan complete and saved to `docs/superpowers/plans/2026-05-06-healthcheck-composer-plan.md`.

Two execution options:

**1. Subagent-Driven (recommended)** — Fresh subagent per task + 2-stage review.

**2. Inline Execution** — All tasks in this session via `superpowers:executing-plans`.

Which approach?
