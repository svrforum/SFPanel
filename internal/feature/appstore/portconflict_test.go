package appstore

import (
	"sort"
	"strings"
	"testing"

	"github.com/svrforum/SFPanel/internal/common/exec"
)

// TestCheckPortConflicts_EnvOverridesDefault reproduces the install bug where
// changing the external port to dodge a busy default still failed: meta.Ports
// duplicates the PORT env default, and the static default was checked even
// after the user moved the port via the env value.
func TestCheckPortConflicts_EnvOverridesDefault(t *testing.T) {
	mock := exec.NewMockCommander()
	// `ss -tlnH`-style output: 8080 is already listening.
	mock.SetOutput("ss", "LISTEN 0 511 0.0.0.0:8080 0.0.0.0:*\n", nil)
	h := &Handler{Cmd: mock}

	meta := &AppStoreMeta{
		Ports: []int{8080},
		Env:   []AppStoreEnvDef{{Key: "PORT", Type: "port", Default: "8080"}},
	}

	// User moved PORT to a free 8081 → the busy default 8080 must NOT be flagged.
	if c := h.checkPortConflicts(meta, map[string]string{"PORT": "8081"}); len(c) != 0 {
		t.Errorf("PORT=8081 should have no conflict, got %v", c)
	}
	// User kept the busy default 8080 → conflict on 8080.
	if c := h.checkPortConflicts(meta, map[string]string{"PORT": "8080"}); len(c) != 1 || c[0] != "8080" {
		t.Errorf("PORT=8080 should conflict on 8080, got %v", c)
	}
	// No env supplied → falls back to default 8080 (busy) → conflict.
	if c := h.checkPortConflicts(meta, nil); len(c) != 1 || c[0] != "8080" {
		t.Errorf("no env should conflict on default 8080, got %v", c)
	}
}

// TestCheckPortConflicts_FixedPortStillChecked ensures a fixed port (one not
// tied to a PORT-type env default) is still checked even when the overridable
// port is moved to a free value.
func TestCheckPortConflicts_FixedPortStillChecked(t *testing.T) {
	mock := exec.NewMockCommander()
	mock.SetOutput("ss", "LISTEN 0 511 0.0.0.0:9000 0.0.0.0:*\n", nil)
	h := &Handler{Cmd: mock}

	meta := &AppStoreMeta{
		Ports: []int{8080, 9000},
		Env:   []AppStoreEnvDef{{Key: "PORT", Type: "port", Default: "8080"}},
	}
	c := h.checkPortConflicts(meta, map[string]string{"PORT": "8081"})
	sort.Strings(c)
	if strings.Join(c, ",") != "9000" {
		t.Errorf("fixed 9000 should conflict, overridable 8080 should not; got %v", c)
	}
}
