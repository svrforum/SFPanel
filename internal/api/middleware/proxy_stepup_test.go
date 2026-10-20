package middleware

import (
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
)

// An advanced install aimed at an agent: the manager checks the operator's
// password itself, and the agent gets the request without it — in any case
// of the key — and with the manager's word, bound to the request and body.
func TestAnAdvancedInstallOnAnAgentIsReauthenticatedOnTheManager(t *testing.T) {
	t.Cleanup(func() { auth.SetProxySecrets("", nil) })
	raftPort := freePort(t)
	dir := t.TempDir()
	mgr := cluster.NewManager(&config.ClusterConfig{
		DataDir: filepath.Join(dir, "raft"), CertDir: filepath.Join(dir, "certs"),
		AdvertiseAddress: "127.0.0.1", GRPCPort: raftPort - 1, APIPort: 1,
	})
	if err := mgr.Init("stepup"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Shutdown)
	auth.SetProxySecrets("managers-secret", []string{"managers-secret"})

	var mu sync.Mutex
	var got []byte
	var gotHeader http.Header
	hits := 0
	panel := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits++
		got, gotHeader = b, r.Header.Clone()
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: ok\n\n"))
	}))
	defer panel.Close()
	v, _ := json.Marshal(cluster.Node{ID: "a1", Name: "a1", Kind: cluster.KindAgent, APIAddress: panel.Listener.Addr().String(), GRPCAddress: "127.0.0.1:1"})
	if err := mgr.GetRaft().Apply(cluster.Command{Type: cluster.CmdAddNode, Value: v}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetPanelCA("a1", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: panel.Certificate().Raw}))); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetConfig(cluster.AgentKeyConfigKey("a1"), "a1-key"); err != nil {
		t.Fatal(err)
	}

	stepUp := func(c echo.Context, password string) error {
		if password == "right" {
			return nil
		}
		return &auth.StepUpError{Status: http.StatusForbidden, Code: response.ErrInvalidPassword, Message: "Incorrect password"}
	}
	withStepUp := func(stepUp func(echo.Context, string) error) echo.HandlerFunc {
		return ClusterProxyMiddleware(func() *cluster.Manager { return mgr }, stepUp)(func(c echo.Context) error {
			return c.NoContent(http.StatusTeapot)
		})
	}
	post := func(h echo.HandlerFunc, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/appstore/apps/demo/install?node=a1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.Set("username", "alice")
		if err := h(c); err != nil {
			t.Fatal(err)
		}
		return rec
	}
	h := withStepUp(stepUp)

	if rec := post(h, `{"advanced":true,"password":"wrong","compose":"x"}`); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), response.ErrInvalidPassword) || hits != 0 {
		t.Fatalf("wrong password: %d %s, %d relayed", rec.Code, rec.Body, hits)
	}
	// A manager with nothing to check the password with does not send the
	// install on unchecked.
	if rec := post(withStepUp(nil), `{"advanced":true,"password":"right","compose":"x"}`); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), response.ErrInvalidCredentials) || hits != 0 {
		t.Fatalf("no verifier: %d %s, %d relayed", rec.Code, rec.Body, hits)
	}
	if rec := post(h, `{"advanced":true,"password":"right","Password":"right","compose":"x"}`); rec.Code != http.StatusOK {
		t.Fatalf("right password: %d %s", rec.Code, rec.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(got, &fields); err != nil {
		t.Fatalf("relayed body %q: %v", got, err)
	}
	for k := range fields {
		if strings.EqualFold(k, "password") {
			t.Fatalf("the password went to the agent: %s", got)
		}
	}
	// Only the password: the agent installs what the operator sent.
	if string(fields["advanced"]) != "true" || string(fields["compose"]) != `"x"` {
		t.Fatalf("the relayed install lost more than the password: %s", got)
	}
	if want := auth.StepUpProofWith("a1-key", gotHeader.Get(auth.InternalProxyHeaderV2), got); gotHeader.Get(auth.StepUpHeader) != want {
		t.Fatalf("step-up proof %q, want %q", gotHeader.Get(auth.StepUpHeader), want)
	}
}
