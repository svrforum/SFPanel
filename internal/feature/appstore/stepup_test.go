package appstore

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	featureauth "github.com/svrforum/SFPanel/internal/feature/auth"
)

// forbiddenAdvanced binds /etc/sfpanel: refused after the re-auth, before any
// docker call — so reaching "Refused compose file" means the re-auth passed.
const forbiddenAdvanced = `"compose":"services:\n  thief:\n    image: alpine\n    volumes:\n      - /etc/sfpanel:/loot\n"`

func postInstall(t *testing.T, h *Handler, body string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/appstore/apps/demo/install", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("demo")
	c.Set("username", "admin")
	if err := h.InstallApp(c); err != nil {
		t.Fatal(err)
	}
	return rec
}

// passedReAuth says the install went past the re-auth: its stream started,
// and stopped at the compose gate.
func passedReAuth(rec *httptest.ResponseRecorder) bool {
	return rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), "Refused compose file")
}

// refusedAtReAuth says the re-auth refused the install with code and nothing
// ran after it. A refusal used to be written and the install carried on
// behind it: compose written, images pulled, containers started.
func refusedAtReAuth(rec *httptest.ResponseRecorder, code string) bool {
	body := rec.Body.String()
	return rec.Code == http.StatusForbidden && strings.Contains(body, `"code":"`+code+`"`) && !strings.Contains(body, "data:")
}

// freeGRPCPort returns a port whose successor is free too: a manager's Raft
// binds its gRPC port + 1.
func freeGRPCPort(t *testing.T) int {
	t.Helper()
	for i := 0; i < 50; i++ {
		l1, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			continue
		}
		p := l1.Addr().(*net.TCPAddr).Port
		l2, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p+1))
		_ = l1.Close()
		if err != nil {
			continue
		}
		_ = l2.Close()
		return p
	}
	t.Fatal("no two consecutive free ports")
	return 0
}

// D1: on a clustered manager a password change is written to the cluster's
// account, and the re-auth read the local row — refusing the new password,
// taking the old one.
func TestAdvancedInstallTakesThePasswordAChangeSet(t *testing.T) {
	const oldPW, newPW = "old-password-horse-1", "new-password-staple-2"
	h := newAdvancedHandler(t, oldPW)
	dir := t.TempDir()
	mgr := cluster.NewManager(&config.ClusterConfig{DataDir: filepath.Join(dir, "raft"), CertDir: filepath.Join(dir, "certs"), AdvertiseAddress: "127.0.0.1", GRPCPort: freeGRPCPort(t), APIPort: 1})
	if err := mgr.Init("d1"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Shutdown)
	var oldHash string
	_ = h.DB.QueryRow("SELECT password FROM admin WHERE username='admin'").Scan(&oldHash)
	if err := mgr.SyncAccountFromDB("admin", oldHash, ""); err != nil {
		t.Fatal(err)
	}
	ah := &featureauth.Handler{DB: h.DB, Config: &config.Config{}, ClusterMgr: mgr}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/change-password", strings.NewReader(`{"current_password":"`+oldPW+`","new_password":"`+newPW+`"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.Set("username", "admin")
	if err := ah.ChangePassword(c); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("change password: %v %d %s", err, rec.Code, rec.Body)
	}
	h.StepUp = ah.VerifyStepUp

	if rec := postInstall(t, h, `{"advanced":true,"password":"`+newPW+`",`+forbiddenAdvanced+`}`, nil); !passedReAuth(rec) {
		t.Fatalf("the new password: %d %s", rec.Code, rec.Body)
	}
	if rec := postInstall(t, h, `{"advanced":true,"password":"`+oldPW+`",`+forbiddenAdvanced+`}`, nil); !refusedAtReAuth(rec, "INVALID_PASSWORD") {
		t.Fatalf("the old password: %d %s", rec.Code, rec.Body)
	}
}

// An agent has no accounts. Its manager checks the password and says so,
// bound to the request and its body; without that word an advanced install
// is refused, however it was signed.
func TestAnAgentTakesAManagersWordForThePassword(t *testing.T) {
	t.Cleanup(func() { auth.SetProxySecrets("", nil) })
	auth.SetProxySecrets("agent-key", []string{"agent-key"})
	h := newHandler(t) // no admin row: an agent
	h.AgentMode = true
	h.apps = []AppStoreMeta{{ID: "demo", Name: "Demo", Version: "1.0.0"}}
	h.cachedAt = time.Now()
	body := `{"advanced":true,` + forbiddenAdvanced + `}`
	v2 := auth.SignProxyRequestV2(http.MethodPost, "/api/v1/appstore/apps/demo/install")
	proof := auth.StepUpProofWith("agent-key", v2, []byte(body))

	if rec := postInstall(t, h, body, map[string]string{auth.InternalProxyHeaderV2: v2, auth.StepUpHeader: proof}); !passedReAuth(rec) {
		t.Fatalf("with the manager's word: %d %s", rec.Code, rec.Body)
	}
	for name, hdr := range map[string]map[string]string{
		"no word":            {auth.InternalProxyHeaderV2: v2},
		"word for elsewhere": {auth.InternalProxyHeaderV2: v2, auth.StepUpHeader: auth.StepUpProofWith("agent-key", v2, []byte(`{"advanced":false}`))},
	} {
		if rec := postInstall(t, h, body, hdr); !refusedAtReAuth(rec, "INVALID_CREDENTIALS") {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

// A node with no way to check the password refuses an advanced install; it
// does not install one unchecked.
func TestAnAdvancedInstallWithNoReAuthIsRefused(t *testing.T) {
	h := newAdvancedHandler(t, "correct-horse")
	h.StepUp = nil
	rec := postInstall(t, h, `{"advanced":true,"password":"correct-horse",`+forbiddenAdvanced+`}`, nil)
	if !refusedAtReAuth(rec, "INVALID_CREDENTIALS") || !strings.Contains(rec.Body.String(), "not available") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}
