package featureserveragents

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/serveragents"
)

// fakeSettings is the cluster's store of the connection values.
type fakeSettings struct {
	saved  *serveragents.Settings
	puts   int
	putErr error
}

func (s *fakeSettings) ServerAgentSettings() (*serveragents.Settings, error) { return s.saved, nil }

func (s *fakeSettings) PutServerAgentSettings(v serveragents.Settings) error {
	s.puts++
	if s.putErr != nil {
		return s.putErr
	}
	s.saved = &v
	return nil
}

// apiFixture serves the agents API from a handler whose store, place and
// leader a test sets. Outside a cluster the store is nil, as NewHandler's.
type apiFixture struct {
	t     *testing.T
	e     *echo.Echo
	host  *hostFixture
	store *fakeSettings
	place place
	// sent are the bodies handed to the leader, as it reads them.
	sent []string
}

var (
	leaderPlace   = place{inCluster: true, leader: true, caughtUp: true}
	followerPlace = place{inCluster: true}
)

func newAPIFixture(t *testing.T, p place) *apiFixture {
	t.Helper()
	f := &apiFixture{t: t, e: echo.New(), store: &fakeSettings{}, place: p}
	var host *Host
	f.host, host = newHostFixture(t)
	h := &Handler{Host: host, now: func() time.Time { return fixtureNow }}
	h.store = func() (settingsStore, place) {
		if !f.place.inCluster {
			return nil, f.place
		}
		return f.store, f.place
	}
	h.toLeader = func(c echo.Context) (bool, error) {
		b, err := io.ReadAll(c.Request().Body)
		if err != nil {
			t.Fatalf("the leader could not read the body: %v", err)
		}
		f.sent = append(f.sent, string(b))
		return true, response.OK(c, "the leader's answer")
	}
	f.e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set("username", "admin")
			return next(c)
		}
	})
	f.e.GET("/api/v1/agents/status", h.Status)
	f.e.GET("/api/v1/agents/settings", h.GetSettings)
	f.e.PUT("/api/v1/agents/settings", h.PutSettings)
	return f
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Details map[string]string `json:"details"`
	} `json:"error"`
}

func (f *apiFixture) do(method, body string) (int, envelope) {
	f.t.Helper()
	return f.doPath(method, "/api/v1/agents/settings", body)
}

func (f *apiFixture) doPath(method, path, body string) (int, envelope) {
	f.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	f.e.ServeHTTP(rec, r)
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		f.t.Fatalf("%s %s: %d %q is not the API's envelope", method, path, rec.Code, rec.Body)
	}
	return rec.Code, env
}

func (e envelope) view(t *testing.T) SettingsView {
	t.Helper()
	var v SettingsView
	if err := json.Unmarshal(e.Data, &v); err != nil {
		t.Fatalf("data %s is not a SettingsView: %v", e.Data, err)
	}
	return v
}

// validBody sets both agents.
const validBody = `{"zabbix":{"server":"192.0.2.10","server_active":"192.0.2.10","host_metadata":"linux-fleet-auto"},"wazuh":{"manager":"192.0.2.10","groups":"","version":"4.14.10"}}`

func TestSettingsBeforeAnyAreSavedReadAsDefaults(t *testing.T) {
	f := newAPIFixture(t, leaderPlace)

	code, env := f.do(http.MethodGet, "")

	if code != http.StatusOK || !env.Success {
		t.Fatalf("GET: %d %+v, want 200", code, env)
	}
	v := env.view(t)
	if v.Saved || v.Wazuh.Version != "4.14.10" || v.Zabbix.Server != "" || v.Wazuh.Manager != "" || !v.UpdatedAt.IsZero() || v.UpdatedBy != "" {
		t.Errorf("unsaved values read %+v, want saved false, the default Wazuh version 4.14.10 and nothing else", v)
	}
}

func TestSettingsAreReadFromTheManagerAsked(t *testing.T) {
	f := newAPIFixture(t, followerPlace)
	saved := serveragents.Settings{
		Zabbix:    serveragents.ZabbixSettings{Server: "192.0.2.10", ServerActive: "192.0.2.10"},
		Wazuh:     serveragents.WazuhSettings{Manager: "192.0.2.10", Version: "4.14.10"},
		UpdatedAt: fixtureNow, UpdatedBy: "admin",
	}
	f.store.saved = &saved

	code, env := f.do(http.MethodGet, "")

	if code != http.StatusOK {
		t.Fatalf("GET on a follower: %d %+v, want 200", code, env)
	}
	if v := env.view(t); !v.Saved || v.Zabbix != saved.Zabbix || v.Wazuh != saved.Wazuh || !v.UpdatedAt.Equal(fixtureNow) || v.UpdatedBy != "admin" {
		t.Errorf("GET on a follower = %+v, want the saved values %+v with saved true", v, saved)
	}
	if len(f.sent) != 0 {
		t.Errorf("a read was sent to the leader: %q; a manager answers it from its own replicated state", f.sent)
	}
}

func TestPuttingSettingsChecksThemFirst(t *testing.T) {
	for name, p := range map[string]place{"leader": leaderPlace, "follower": followerPlace} {
		t.Run(name, func(t *testing.T) {
			f := newAPIFixture(t, p)

			code, env := f.do(http.MethodPut, `{"zabbix":{"server":"192.0.2.10","server_active":"192.0.2.10:70000"},"wazuh":{}}`)

			if code != http.StatusBadRequest || env.Error.Code != response.ErrServerAgentSettingsInvalid || env.Error.Details["field"] != "zabbix.server_active" {
				t.Fatalf("PUT with port 70000: %d %+v, want 400 %s naming zabbix.server_active", code, env.Error, response.ErrServerAgentSettingsInvalid)
			}
			if !strings.Contains(env.Error.Message, "70000") {
				t.Errorf("message %q does not say what is wrong", env.Error.Message)
			}
			if f.store.puts != 0 || len(f.sent) != 0 {
				t.Errorf("stored %d times and sent %d bodies to the leader; want neither for values that are refused", f.store.puts, len(f.sent))
			}
		})
	}
}

func TestABodyThatIsNotSettingsIsRefused(t *testing.T) {
	f := newAPIFixture(t, leaderPlace)

	code, env := f.do(http.MethodPut, `{"zabbix":"192.0.2.10"}`)

	if code != http.StatusBadRequest || env.Error.Code != response.ErrInvalidRequest {
		t.Fatalf("PUT with a string for zabbix: %d %+v, want 400 %s", code, env.Error, response.ErrInvalidRequest)
	}
	if f.store.puts != 0 {
		t.Errorf("stored %d times, want none", f.store.puts)
	}
}

func TestPuttingSettingsStoresThemNormalised(t *testing.T) {
	f := newAPIFixture(t, leaderPlace)
	// Who saved them and when are the leader's to say, not the body's.
	body := `{"zabbix":{"server":" 192.0.2.10 ,","server_active":" 192.0.2.10 ","host_metadata":" linux-fleet-auto "},` +
		`"wazuh":{"manager":" 192.0.2.10 ","groups":" default , ","version":""},` +
		`"updated_by":"someone-else","updated_at":"2020-01-01T00:00:00Z","saved":false}`

	code, env := f.do(http.MethodPut, body)

	if code != http.StatusOK {
		t.Fatalf("PUT: %d %+v, want 200", code, env.Error)
	}
	if f.store.puts != 1 || f.store.saved == nil {
		t.Fatalf("stored %d times, want once", f.store.puts)
	}
	want := serveragents.Settings{
		Zabbix:    serveragents.ZabbixSettings{Server: "192.0.2.10", ServerActive: "192.0.2.10", HostMetadata: "linux-fleet-auto"},
		Wazuh:     serveragents.WazuhSettings{Manager: "192.0.2.10", Groups: "default", Version: "4.14.10"},
		UpdatedAt: fixtureNow, UpdatedBy: "admin",
	}
	got := *f.store.saved
	if got.Zabbix != want.Zabbix || got.Wazuh != want.Wazuh || !got.UpdatedAt.Equal(want.UpdatedAt) || got.UpdatedBy != want.UpdatedBy {
		t.Errorf("stored %+v, want %+v", got, want)
	}
	if v := env.view(t); !v.Saved || v.Zabbix != want.Zabbix || v.Wazuh != want.Wazuh || !v.UpdatedAt.Equal(fixtureNow) || v.UpdatedBy != "admin" {
		t.Errorf("answered %+v, want the stored values with saved true", v)
	}
	if len(f.sent) != 0 {
		t.Errorf("the leader sent the write on: %q", f.sent)
	}
}

func TestAFollowerSendsTheWriteToTheLeader(t *testing.T) {
	f := newAPIFixture(t, followerPlace)

	code, env := f.do(http.MethodPut, validBody)

	if len(f.sent) != 1 || f.sent[0] != validBody {
		t.Fatalf("sent %q to the leader, want the body once, as it came", f.sent)
	}
	if f.store.puts != 0 {
		t.Errorf("a follower stored the values %d times, want none", f.store.puts)
	}
	if code != http.StatusOK || string(env.Data) != `"the leader's answer"` {
		t.Errorf("answered %d %s, want the leader's answer", code, env.Data)
	}
}

func TestALeaderThatHasNotCaughtUpAnswers503(t *testing.T) {
	f := newAPIFixture(t, place{inCluster: true, leader: true})

	code, env := f.do(http.MethodPut, validBody)

	if code != http.StatusServiceUnavailable || env.Error.Code != response.ErrInternalError || !strings.Contains(env.Error.Message, "catching up") {
		t.Fatalf("PUT on a leader behind: %d %+v, want 503 saying it is catching up", code, env.Error)
	}
	if f.store.puts != 0 || len(f.sent) != 0 {
		t.Errorf("stored %d times and sent %d bodies on, want neither", f.store.puts, len(f.sent))
	}

	t.Run("a leader that lost its place under the write", func(t *testing.T) {
		f := newAPIFixture(t, leaderPlace)
		f.store.putErr = cluster.ErrNotLeader

		code, env := f.do(http.MethodPut, validBody)

		if code != http.StatusServiceUnavailable || env.Error.Code != response.ErrInternalError || !strings.Contains(env.Error.Message, "not the cluster leader") {
			t.Fatalf("PUT refused as not the leader: %d %+v, want 503 saying so", code, env.Error)
		}
	})
}

func TestSettingsOutsideAClusterAre400(t *testing.T) {
	f := newAPIFixture(t, place{})

	for _, method := range []string{http.MethodGet, http.MethodPut} {
		code, env := f.do(method, validBody)
		if code != http.StatusBadRequest || env.Error.Code != response.ErrInvalidRequest || env.Error.Message != "Cluster not configured" {
			t.Errorf("%s outside a cluster: %d %+v, want 400 %s Cluster not configured", method, code, env.Error, response.ErrInvalidRequest)
		}
	}
	if len(f.sent) != 0 {
		t.Errorf("sent %q on, want nothing", f.sent)
	}
}

func TestStatusAnswersTheHostsReading(t *testing.T) {
	f := newAPIFixture(t, place{})
	f.host.system(osNoble)
	f.host.noPkg("zabbix-agent2", "zabbix-agent", "wazuh-agent")
	f.host.unit("not-found")

	code, env := f.doPath(http.MethodGet, "/api/v1/agents/status", "")

	if code != http.StatusOK || !env.Success {
		t.Fatalf("GET /agents/status: %d %+v, want 200", code, env.Error)
	}
	var st serveragents.Status
	if err := json.Unmarshal(env.Data, &st); err != nil {
		t.Fatalf("data %s is not a Status: %v", env.Data, err)
	}
	if st.Zabbix.State != serveragents.StateNone || st.Wazuh.State != serveragents.StateNone || st.OS.Codename != "noble" || !st.CheckedAt.Equal(fixtureNow) {
		t.Errorf("status = %+v, want this host's reading: noble, no agents, checked at %v", st, fixtureNow)
	}
}
