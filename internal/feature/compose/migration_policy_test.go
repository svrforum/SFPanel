package compose

import (
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/docker"
)

// fakeDockerScript logs every docker call and answers the few a migration
// makes before it transfers anything.
const fakeDockerScript = `#!/bin/sh
echo "$*" >> "$FAKE_DOCKER_LOG"
case "$*" in
  *"config --format json"*) echo '{"services":{"web":{"image":"nginx:fake"}}}' ;;
  "save nginx:fake") printf 'IMAGEDATA' ;;
  "image inspect --format {{.Size}} nginx:fake") echo 9 ;;
esac
exit 0
`

// withFakeDocker puts that docker on PATH and returns where it logs.
func withFakeDocker(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "docker.log")
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fakeDockerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("FAKE_DOCKER_LOG", logPath)
	return logPath
}

// migrationManager is a one-manager cluster with an agent "a1" whose panel
// is served by panel; withCA and withKey say what the managers know of it.
func migrationManager(t *testing.T, panel *httptest.Server, withCA, withKey bool) *cluster.Manager {
	t.Helper()
	t.Cleanup(func() { auth.SetProxySecrets("", nil) })
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	raftPort := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	dir := t.TempDir()
	mgr := cluster.NewManager(&config.ClusterConfig{
		DataDir: filepath.Join(dir, "raft"), CertDir: filepath.Join(dir, "certs"),
		AdvertiseAddress: "127.0.0.1", GRPCPort: raftPort - 1, APIPort: 1,
	})
	if err := mgr.Init("migration"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Shutdown)
	auth.SetProxySecrets("managers-secret", []string{"managers-secret"})
	v, _ := json.Marshal(cluster.Node{ID: "a1", Name: "a1", Kind: cluster.KindAgent, APIAddress: panel.Listener.Addr().String(), GRPCAddress: "127.0.0.1:1"})
	if err := mgr.GetRaft().Apply(cluster.Command{Type: cluster.CmdAddNode, Value: v}, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if withCA {
		if err := mgr.SetPanelCA("a1", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: panel.Certificate().Raw}))); err != nil {
			t.Fatal(err)
		}
	}
	if withKey {
		if err := mgr.SetConfig(cluster.AgentKeyConfigKey("a1"), "a1-key"); err != nil {
			t.Fatal(err)
		}
	}
	return mgr
}

func demoStack(t *testing.T) string {
	t.Helper()
	stacks := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stacks, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stacks, "demo", "docker-compose.yml"), []byte("services:\n  web:\n    image: nginx:fake\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return stacks
}

func runMigrate(t *testing.T, h *Handler, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/docker/compose/demo/migrate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	c.SetParamNames("project")
	c.SetParamValues("demo")
	c.Set("username", "alice")
	if err := h.Migrate(c); err != nil {
		t.Fatal(err)
	}
	return rec.Body.String()
}

// A migration stopped the source, packaged it, and only then found it could
// not send to the target — no panel CA, no agent key, or no such node — and
// restarted the source: downtime for nothing. The source now stays up. So it
// does for a target the pre-flight could not ask: a1's cluster port is
// closed, so nothing is known of its disk, ports or stacks.
func TestMigrateDoesNotStopTheSourceForATargetItCannotReach(t *testing.T) {
	for _, tc := range []struct {
		name, target   string
		withCA, withKy bool
		want           string
	}{
		{"no panel CA", "a1", false, true, cluster.ErrAgentPanelInsecure.Error()},
		{"no agent key", "a1", true, false, cluster.ErrNoAgentKey.Error()},
		{"no such node", "nope", true, true, "unknown target node"},
		{"target unreachable", "a1", true, true, "could not query the target node"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logPath := withFakeDocker(t)
			panel := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
			defer panel.Close()
			mgr := migrationManager(t, panel, tc.withCA, tc.withKy)
			stacks := demoStack(t)
			h := &Handler{Compose: docker.NewComposeManager(stacks, nil), ComposePath: stacks}
			h.SetClusterMgr(mgr)
			out := runMigrate(t, h, `{"targetNodeId":"`+tc.target+`","disposition":"retain"}`)
			log, _ := os.ReadFile(logPath)
			if strings.Contains(string(log), " stop") {
				t.Fatalf("the source was stopped:\n%s\nSSE:\n%s", log, out)
			}
			if !strings.Contains(out, "pre-flight blocked") || !strings.Contains(out, tc.want) {
				t.Fatalf("SSE does not say why:\n%s", out)
			}
		})
	}
}

// The manager that sends an import says what the operator decided — the
// bundle is the source's word, and a source may be an agent.
func TestPushBundleCarriesThePolicy(t *testing.T) {
	var got http.Header
	panel := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"success":true,"data":{"status":"ok"}}`))
	}))
	defer panel.Close()
	mgr := migrationManager(t, panel, true, true)
	h := &Handler{ComposePath: t.TempDir()}
	h.SetClusterMgr(mgr)
	status, _, err := h.pushBundle(t.Context(), "a1", "alice", strings.NewReader("bundle"), 6, "abc",
		importPolicy{Overwrite: true, AckRisks: false, AgentSource: true}, nil, 0)
	if err != nil || status != http.StatusOK {
		t.Fatalf("push: %d %v", status, err)
	}
	for k, want := range map[string]string{migrationPolicyHeader: "1", migrationOverwriteHeader: "true", migrationAckRisksHeader: "false", migrationSourceKindHeader: "agent"} {
		if got.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, got.Get(k), want)
		}
	}
}

// What the target answers is read with a bound: a hostile target could make
// the sending manager buffer whatever it sends.
func TestPushBundleBoundsTheTargetsAnswer(t *testing.T) {
	panel := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 3<<20)))
	}))
	defer panel.Close()
	mgr := migrationManager(t, panel, true, true)
	h := &Handler{ComposePath: t.TempDir()}
	h.SetClusterMgr(mgr)
	_, body, _ := h.pushBundle(t.Context(), "a1", "alice", strings.NewReader("b"), 1, "abc", importPolicy{}, nil, 0)
	if len(body) > 1<<20 {
		t.Fatalf("read %d bytes of the target's answer", len(body))
	}
}
