package agentinstall

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
)

const loginsPath = "/api/v1/cluster/agent-installs/logins"

// loginAnswer is a saved login route's answer, its data raw.
type loginAnswer struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Details map[string]string `json:"details"`
	} `json:"error"`
}

// loginCall sends a request to the saved logins' routes, path after
// …/logins, and reads its answer.
func (f *apiFixture) loginCall(method, path, body string) (int, loginAnswer) {
	f.t.Helper()
	f.do(method, loginsPath+path, body)
	f.mu.Lock()
	a := f.answers[len(f.answers)-1]
	f.mu.Unlock()
	var ans loginAnswer
	if err := json.Unmarshal([]byte(a.body), &ans); err != nil {
		f.t.Fatalf("%s %s answered %d %q, no JSON: %v", method, path, a.status, a.body, err)
	}
	return a.status, ans
}

// saveLogin saves body as a new login and returns its view.
func (f *apiFixture) saveLogin(body string) LoginView {
	f.t.Helper()
	st, ans := f.loginCall(http.MethodPost, "", body)
	var d struct {
		Login LoginView `json:"login"`
	}
	if st != http.StatusCreated || json.Unmarshal(ans.Data, &d) != nil || d.Login.ID == "" {
		f.t.Fatalf("saving %s = %d %s %+v, want 201 with the login", body, st, ans.Data, ans.Error)
	}
	return d.Login
}

// listLogins is GET …/logins' list, which is never null.
func (f *apiFixture) listLogins() []LoginView {
	f.t.Helper()
	st, ans := f.loginCall(http.MethodGet, "", "")
	var d struct {
		Logins []LoginView `json:"logins"`
	}
	if st != http.StatusOK || json.Unmarshal(ans.Data, &d) != nil || d.Logins == nil {
		f.t.Fatalf("GET logins = %d %s, want 200 with a list", st, ans.Data)
	}
	return d.Logins
}

// startWith starts an install of s with saved login id.
func (f *apiFixture) startWith(id string, s *fakeServer) string {
	f.t.Helper()
	st, ans := f.start(installBody(fmt.Sprintf(`{"login":%q}`, id), s))
	if st != http.StatusAccepted || ans.Data.RunID == "" {
		f.t.Fatalf("a start with login %s = %d %+v, want 202", id, st, ans.Error)
	}
	return ans.Data.RunID
}

// serverAt is passing server n at ip.
func serverAt(t *testing.T, env *fakeEnv, n int, ip string) *fakeServer {
	s := passingServer(t, env, n)
	s.ip = net.ParseIP(ip).To4()
	s.facts.Addrs = []net.IP{s.ip}
	return s
}

// wantRefusal fails t unless the answer is status and code, with field when
// one is given.
func wantRefusal(t *testing.T, what string, st int, ans loginAnswer, status int, code, field string) {
	t.Helper()
	if st != status || ans.Error.Code != code || (field != "" && ans.Error.Details["field"] != field) {
		t.Fatalf("%s = %d %+v, want %d %s {field: %s}", what, st, ans.Error, status, code, field)
	}
}

// A saved login is answered as its view, which is all of what any route
// answers of it: its keys are the view's every key. Logins are listed by
// name without regard to case, changed and deleted.
func TestASavedLoginIsSavedListedChangedAndDeleted(t *testing.T) {
	f := newAPIFixture(t)
	st, ans := f.loginCall(http.MethodPost, "", `{"name":"  기본 계정  ","user":"ubuntu","method":"password","password":"pw","sudo_same":true}`)
	if st != http.StatusCreated || !ans.Success {
		t.Fatalf("POST = %d %+v, want 201", st, ans)
	}
	var raw map[string]map[string]any
	if err := json.Unmarshal(ans.Data, &raw); err != nil {
		t.Fatal(err)
	}
	want := []string{"created_at", "has_key", "has_passphrase", "has_password", "has_sudo_password", "id", "method", "name", "sudo_same", "updated_at", "updated_by", "user"}
	if keys := slices.Sorted(maps.Keys(raw["login"])); !slices.Equal(keys, want) {
		t.Fatalf("the view's keys %v, want %v", keys, want)
	}
	var d struct {
		Login LoginView `json:"login"`
	}
	if err := json.Unmarshal(ans.Data, &d); err != nil {
		t.Fatal(err)
	}
	pw := d.Login
	if pw.Name != "기본 계정" || pw.User != "ubuntu" || pw.Method != "password" || !pw.HasPassword || pw.HasKey || pw.HasPassphrase ||
		!pw.SudoSame || pw.HasSudoPassword || pw.UpdatedBy != "operator" || !pw.CreatedAt.Equal(f.env.now) || !pw.UpdatedAt.Equal(f.env.now) {
		t.Fatalf("created %+v, want the trimmed name, a password login sharing its password with sudo, by the operator", pw)
	}

	key := f.saveLogin(`{"name":"web","user":"ops","method":"key","key":"k","passphrase":"pp","sudo_password":"s"}`)
	if key.Method != "key" || !key.HasKey || !key.HasPassphrase || !key.HasSudoPassword || key.HasPassword || key.SudoSame {
		t.Fatalf("created %+v, want a key login with its passphrase and a sudo password", key)
	}
	ts := f.saveLogin(`{"name":"Tailnet","user":"ops","method":"tailscale"}`)
	if ts.Method != "tailscale" || ts.HasPassword || ts.HasKey || ts.HasPassphrase || ts.HasSudoPassword {
		t.Fatalf("created %+v, want Tailscale SSH by the API's name, with no secret", ts)
	}
	if f.env.storedLogin(ts.ID).Method != "none" {
		t.Fatalf("stored %+v, want the engine's name for Tailscale SSH", f.env.storedLogin(ts.ID))
	}
	var names []string
	for _, l := range f.listLogins() {
		names = append(names, l.Name)
	}
	if want := []string{"Tailnet", "web", "기본 계정"}; !slices.Equal(names, want) {
		t.Fatalf("listed %v, want %v", names, want)
	}

	f.env.advance(time.Minute)
	st, ans = f.loginCall(http.MethodPut, "/"+pw.ID, `{"name":"기본","user":"root","method":"password","sudo_same":true}`)
	d.Login = LoginView{}
	if st != http.StatusOK || json.Unmarshal(ans.Data, &d) != nil {
		t.Fatalf("PUT = %d %+v, want 200", st, ans)
	}
	if c := d.Login; c.ID != pw.ID || c.Name != "기본" || c.User != "root" || !c.HasPassword || !c.CreatedAt.Equal(pw.CreatedAt) || !c.UpdatedAt.Equal(f.env.now) {
		t.Fatalf("changed %+v, want the new name and user, the password kept, created when it was", c)
	}

	st, ans = f.loginCall(http.MethodDelete, "/"+key.ID, "")
	if st != http.StatusOK {
		t.Fatalf("DELETE = %d %+v, want 200", st, ans)
	}
	sameJSON(t, ans.Data, `{"id": "`+key.ID+`"}`)
	for _, id := range []string{pw.ID, ts.ID} {
		if st, ans := f.loginCall(http.MethodDelete, "/"+id, ""); st != http.StatusOK {
			t.Fatalf("DELETE %s = %d %+v", id, st, ans)
		}
	}
	if list := f.listLogins(); len(list) != 0 {
		t.Fatalf("listed %+v after every delete, want an empty list", list)
	}
}

// A saved login's secrets are written once and never answered: not by the
// routes that save, change and list it, nor a refusal of them, nor the run
// record of an install that used it, its audit rows, the log or a phase's
// output. The secrets did reach the store, the login and sudo: the test
// looks where they would show.
func TestNoSavedSecretIsEverAnswered(t *testing.T) {
	const (
		loginPw    = "canary-login-pw-41d7"
		newLoginPw = "canary-login-pw-8c03"
		sudoPw     = "canary-sudo-pw-b29e"
		keyBody    = "canary-key-body-77f1"
		passphrase = "canary-phrase-0e5a"
		keySudoPw  = "canary-key-sudo-3a6c"
	)
	secrets := []string{loginPw, newLoginPw, sudoPw, keyBody, passphrase, keySudoPw}
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\n" + keyBody + "\n-----END OPENSSH PRIVATE KEY-----\n"

	logs := &lockedBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	env := newFakeEnv()
	s1, s2 := serverAt(t, env, 1, "192.168.1.11"), serverAt(t, env, 2, "10.0.0.12")
	s1.sudoPw, s2.sudoPw = sudoPw, keySudoPw
	f := newAPIFixture(t, s1, s2)
	d := f.eng.dialer.(*fakeDialer)

	pw := f.saveLogin(fmt.Sprintf(`{"name":"pw","user":"ops","method":"password","password":%q,"sudo_password":%q}`, loginPw, sudoPw))
	kl := f.saveLogin(fmt.Sprintf(`{"name":"key","user":"ops","method":"key","key":%q,"passphrase":%q,"sudo_password":%q}`, key, passphrase, keySudoPw))
	if st, ans := f.loginCall(http.MethodPut, "/"+pw.ID, fmt.Sprintf(`{"name":"pw","user":"ops","method":"password","password":%q}`, newLoginPw)); st != http.StatusOK {
		t.Fatalf("a new password = %d %+v", st, ans.Error)
	}
	if st, ans := f.loginCall(http.MethodPut, "/"+kl.ID, `{"name":"key","user":"ops","method":"key"}`); st != http.StatusOK {
		t.Fatalf("a change keeping every secret = %d %+v", st, ans.Error)
	}
	f.listLogins()
	if st, _ := f.loginCall(http.MethodPost, "", fmt.Sprintf(`{"name":"","user":"ops","method":"password","password":%q,"sudo_password":%q}`, loginPw, sudoPw)); st != http.StatusBadRequest {
		t.Fatalf("a refused save = %d, want 400", st)
	}
	if p, k := env.storedLogin(pw.ID), env.storedLogin(kl.ID); p.Password != newLoginPw || p.SudoPassword != sudoPw ||
		k.Key != key || k.Passphrase != passphrase || k.SudoPassword != keySudoPw {
		t.Fatalf("stored %+v and %+v, want every secret saved", p, k)
	}

	check := func(t *testing.T, runID string) {
		t.Helper()
		var sent []string
		f.mu.Lock()
		for _, a := range f.answers {
			sent = append(sent, a.what+" → "+a.body)
		}
		f.mu.Unlock()
		env.mu.Lock()
		snapshot, _ := json.Marshal(map[string]any{"type": "snapshot", "run": env.runs[runID], "nodes": env.rows[runID]})
		sent = append(sent, "GET and the stream → "+string(snapshot), "every record written → "+strings.Join(env.writes, "\n"),
			"audit → "+strings.Join(env.audits, "\n"))
		env.mu.Unlock()
		for step := range phases {
			if v, err := f.eng.StepOutput(runID, "s1", step, 0); err == nil {
				b, _ := json.Marshal(v)
				sent = append(sent, fmt.Sprintf("output %d → %s", step, b))
			}
		}
		sent = append(sent, "log → "+logs.String())
		for _, s := range sent {
			for _, secret := range secrets {
				if strings.Contains(s, secret) {
					t.Fatalf("a saved secret reached what is sent: %s", s)
				}
			}
		}
	}
	for _, run := range []struct {
		login LoginView
		srv   *fakeServer
	}{{pw, s1}, {kl, s2}} {
		id := f.startWith(run.login.ID, run.srv)
		f.approve(id, "s1")
		if h := env.waitEnded(t, id); h.Status != cluster.RunSucceeded {
			t.Fatalf("the install with %s = %s %+v, want succeeded", run.login.Name, h.Status, env.row(id, "s1"))
		}
		check(t, id)
	}
	logins := d.loginsGiven()
	if len(logins) == 0 || string(logins[0].Password) != newLoginPw || string(logins[len(logins)-1].Key) != key {
		t.Fatalf("the logins given %d, want the saved password first and the saved key last", len(logins))
	}
	if !strings.Contains(string(s1.execList()[1].stdin), sudoPw) || !strings.Contains(string(s2.execList()[1].stdin), keySudoPw) {
		t.Fatal("sudo was not given the saved sudo passwords")
	}
}

// A saved login's name is trimmed, 1 to 64 characters and no control
// character, and its account holds together as an install's must; a field
// no login has is refused. Nothing refused is saved, and a change is checked
// as a create is.
func TestASavedLoginIsValidated(t *testing.T) {
	long := strings.Repeat("가", 65)
	cases := []struct{ name, body, field string }{
		{"no name", `{"user":"ops","method":"key","key":"k"}`, "name"},
		{"a blank name", `{"name":"  \t ","user":"ops","method":"key","key":"k"}`, "name"},
		{"a name of 65 characters", `{"name":"` + long + `","user":"ops","method":"key","key":"k"}`, "name"},
		{"a name with a line break", `{"name":"ops\nroot","user":"ops","method":"key","key":"k"}`, "name"},
		{"a name with a control character", `{"name":"ops\u0007","user":"ops","method":"key","key":"k"}`, "name"},
		{"no user", `{"name":"n","method":"key","key":"k"}`, "user"},
		{"no method", `{"name":"n","user":"ops","key":"k"}`, "method"},
		{"the engine's name for Tailscale SSH", `{"name":"n","user":"ops","method":"none"}`, "method"},
		{"a password login without one", `{"name":"n","user":"ops","method":"password"}`, "password"},
		{"a password login with a key", `{"name":"n","user":"ops","method":"password","password":"pw","key":"k"}`, "key"},
		{"a password login with a passphrase", `{"name":"n","user":"ops","method":"password","password":"pw","passphrase":"pp"}`, "passphrase"},
		{"a key login without a key", `{"name":"n","user":"ops","method":"key","passphrase":"pp"}`, "key"},
		{"a key login with a password", `{"name":"n","user":"ops","method":"key","key":"k","password":"pw"}`, "password"},
		{"Tailscale SSH with a password", `{"name":"n","user":"ops","method":"tailscale","password":"pw"}`, "password"},
		{"Tailscale SSH with a key", `{"name":"n","user":"ops","method":"tailscale","key":"k"}`, "key"},
		{"sudo the same as no password", `{"name":"n","user":"ops","method":"key","key":"k","sudo_same":true}`, "sudo_same"},
		{"sudo the same and its own", `{"name":"n","user":"ops","method":"password","password":"pw","sudo_same":true,"sudo_password":"s"}`, "sudo_password"},
		{"a field no login has", `{"name":"n","user":"ops","method":"tailscale","login":"x"}`, "body"},
		{"no JSON", `name`, "body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAPIFixture(t)
			st, ans := f.loginCall(http.MethodPost, "", tc.body)
			wantRefusal(t, "POST "+tc.body, st, ans, http.StatusBadRequest, response.ErrAgentInstallInvalid, tc.field)
			if n := len(f.env.logins); n != 0 {
				t.Fatalf("a refused login was saved: %d stored", n)
			}
		})
	}

	t.Run("64 characters, trimmed", func(t *testing.T) {
		f := newAPIFixture(t)
		name := strings.Repeat("가", 64)
		l := f.saveLogin(`{"name":"  ` + name + `\n","user":"ops","method":"tailscale"}`)
		if l.Name != name || f.env.storedLogin(l.ID).Name != name {
			t.Fatalf("saved %q, want the 64 characters trimmed", l.Name)
		}
	})
	t.Run("a change", func(t *testing.T) {
		f := newAPIFixture(t)
		l := f.saveLogin(`{"name":"ops","user":"ops","method":"password","password":"pw"}`)
		before := f.env.storedLogin(l.ID)
		for _, tc := range []struct{ body, field string }{
			{`{"name":"","user":"ops","method":"password"}`, "name"},
			{`{"name":"ops","user":"ops","method":"none"}`, "method"},
			{`{"name":"ops","method":"password"}`, "user"},
			{`{"name":"ops","user":"ops","method":"password","key":"k"}`, "key"},
			{`{"name":"ops","user":"ops","method":"password","extra":1}`, "body"},
		} {
			st, ans := f.loginCall(http.MethodPut, "/"+l.ID, tc.body)
			wantRefusal(t, "PUT "+tc.body, st, ans, http.StatusBadRequest, response.ErrAgentInstallInvalid, tc.field)
		}
		if after := f.env.storedLogin(l.ID); after != before {
			t.Fatalf("refused changes changed the login: %+v, was %+v", after, before)
		}
	})
}

// A change need not send the secrets it keeps: one left empty stays as
// stored where the login's method uses it. A new key comes with its own
// passphrase or none, sudo_same drops a sudo password of the login's own,
// and the secrets the new method does not use go with the change.
func TestAChangeKeepsTheSecretsLeftEmpty(t *testing.T) {
	f := newAPIFixture(t)
	l := f.saveLogin(`{"name":"ops","user":"ops","method":"password","password":"pw-1","sudo_password":"sudo-1"}`)
	type held struct {
		password, key, passphrase, sudo string
		same                            bool
	}
	steps := []struct {
		what, body string
		want       held
	}{
		{"every secret left empty", `{"name":"ops","user":"ops","method":"password"}`, held{"pw-1", "", "", "sudo-1", false}},
		{"a new password", `{"name":"ops","user":"ops","method":"password","password":"pw-2"}`, held{"pw-2", "", "", "sudo-1", false}},
		{"sudo the same as the login", `{"name":"ops","user":"ops","method":"password","sudo_same":true}`, held{"pw-2", "", "", "", true}},
		{"a key login", `{"name":"ops","user":"ops","method":"key","key":"k-1","passphrase":"pp-1","sudo_password":"sudo-2"}`, held{"", "k-1", "pp-1", "sudo-2", false}},
		{"the key login left as it is", `{"name":"ops","user":"ops","method":"key"}`, held{"", "k-1", "pp-1", "sudo-2", false}},
		{"a passphrase alone", `{"name":"ops","user":"ops","method":"key","passphrase":"pp-2"}`, held{"", "k-1", "pp-2", "sudo-2", false}},
		{"a new key without a passphrase", `{"name":"ops","user":"ops","method":"key","key":"k-2"}`, held{"", "k-2", "", "sudo-2", false}},
		{"Tailscale SSH", `{"name":"ops","user":"ops","method":"tailscale"}`, held{"", "", "", "sudo-2", false}},
	}
	for _, s := range steps {
		st, ans := f.loginCall(http.MethodPut, "/"+l.ID, s.body)
		if st != http.StatusOK {
			t.Fatalf("%s: PUT = %d %+v, want 200", s.what, st, ans.Error)
		}
		got := f.env.storedLogin(l.ID)
		if h := (held{got.Password, got.Key, got.Passphrase, got.SudoPassword, got.SudoSame}); h != s.want {
			t.Fatalf("%s: stored %+v, want %+v", s.what, h, s.want)
		}
	}
	// What the change to a key login dropped is not there to keep.
	st, ans := f.loginCall(http.MethodPut, "/"+l.ID, `{"name":"ops","user":"ops","method":"password"}`)
	wantRefusal(t, "a password login again, its password left empty", st, ans, http.StatusBadRequest, response.ErrAgentInstallInvalid, "password")
	st, ans = f.loginCall(http.MethodPut, "/"+l.ID, `{"name":"ops","user":"ops","method":"key"}`)
	wantRefusal(t, "a key login again, its key left empty", st, ans, http.StatusBadRequest, response.ErrAgentInstallInvalid, "key")
}

// A name another login has, in any case, is 409 AGENT_INSTALL_LOGIN_EXISTS;
// the 33rd login 409 AGENT_INSTALL_LOGIN_LIMIT; a change or a delete of a
// login there is not 404 AGENT_INSTALL_LOGIN_NOT_FOUND.
func TestASavedLoginsRefusals(t *testing.T) {
	f := newAPIFixture(t)
	f.saveLogin(`{"name":"Ops","user":"ops","method":"tailscale"}`)
	web := f.saveLogin(`{"name":"web","user":"ops","method":"tailscale"}`)

	st, ans := f.loginCall(http.MethodPost, "", `{"name":"oPS","user":"ops","method":"tailscale"}`)
	wantRefusal(t, "a second Ops", st, ans, http.StatusConflict, response.ErrAgentInstallLoginExists, "name")
	st, ans = f.loginCall(http.MethodPut, "/"+web.ID, `{"name":"OPS","user":"ops","method":"tailscale"}`)
	wantRefusal(t, "web renamed OPS", st, ans, http.StatusConflict, response.ErrAgentInstallLoginExists, "name")
	if got := f.env.storedLogin(web.ID).Name; got != "web" {
		t.Fatalf("the refused rename stored %q", got)
	}

	st, ans = f.loginCall(http.MethodPut, "/no-such-login", `{"name":"x","user":"ops","method":"tailscale"}`)
	wantRefusal(t, "a change of no login", st, ans, http.StatusNotFound, response.ErrAgentInstallLoginNotFound, "")
	st, ans = f.loginCall(http.MethodDelete, "/no-such-login", "")
	wantRefusal(t, "a delete of no login", st, ans, http.StatusNotFound, response.ErrAgentInstallLoginNotFound, "")

	for i := len(f.env.logins); i < cluster.MaxInstallLogins; i++ {
		f.saveLogin(fmt.Sprintf(`{"name":"login %d","user":"ops","method":"tailscale"}`, i))
	}
	st, ans = f.loginCall(http.MethodPost, "", `{"name":"one too many","user":"ops","method":"tailscale"}`)
	wantRefusal(t, "the 33rd login", st, ans, http.StatusConflict, response.ErrAgentInstallLoginLimit, "")
	if ans.Error.Details["max"] != "32" || len(f.env.logins) != cluster.MaxInstallLogins {
		t.Fatalf("details %v with %d logins, want max 32 and 32 kept", ans.Error.Details, len(f.env.logins))
	}
}

// A follower sends every saved login request on to the leader, its body as
// it came, and reads and writes no login itself: the leader answers from
// its own state, so a page reads its own write.
func TestAFollowerForwardsTheSavedLogins(t *testing.T) {
	f := newAPIFixture(t)
	f.at = place{inCluster: true}
	body := `{"name":"ops","user":"ops","method":"password","password":"pw","sudo_same":true}`
	f.do(http.MethodGet, loginsPath, "")
	f.do(http.MethodPost, loginsPath, body)
	f.do(http.MethodPut, loginsPath+"/login01", body)
	f.do(http.MethodDelete, loginsPath+"/login01", "")
	// An install started with a saved login: the follower never reads it.
	f.start(`{"servers":[{"host":"192.168.1.11"}],"account":{"login":"login01"}}`)
	want := []string{
		"GET " + loginsPath + " ",
		"POST " + loginsPath + " " + body,
		"PUT " + loginsPath + "/login01 " + body,
		"DELETE " + loginsPath + "/login01 ",
		`POST /api/v1/cluster/agent-installs {"servers":[{"host":"192.168.1.11"}],"account":{"login":"login01"}}`,
	}
	if !slices.Equal(f.forwarded, want) {
		t.Fatalf("forwarded\n%s\nwant\n%s", strings.Join(f.forwarded, "\n"), strings.Join(want, "\n"))
	}
	if n := f.env.loginReadCount(); n != 0 || len(f.env.logins) != 0 {
		t.Fatalf("the follower read the logins %d times and stored %d", n, len(f.env.logins))
	}

	f.at = place{inCluster: true, leader: true}
	for _, r := range []struct{ method, path, body string }{
		{http.MethodGet, "", ""}, {http.MethodPost, "", body}, {http.MethodPut, "/login01", body}, {http.MethodDelete, "/login01", ""},
	} {
		st, ans := f.loginCall(r.method, r.path, r.body)
		wantRefusal(t, "a leader catching up: "+r.method, st, ans, http.StatusServiceUnavailable, response.ErrInternalError, "")
	}
	f.at = place{}
	st, ans := f.loginCall(http.MethodGet, "", "")
	wantRefusal(t, "outside a cluster", st, ans, http.StatusBadRequest, response.ErrInvalidRequest, "")
}

// An install started with a saved login logs in with the stored user and
// password, read on the leader into the install's own copy: deleting the
// login while the install waits changes nothing for it, and the install's
// wipe at its end leaves the store's alone, so the next install with it
// logs in too. The run names the login it used, and nothing more of it.
func TestAnInstallUsesASavedLogin(t *testing.T) {
	env := newFakeEnv()
	s1, s2 := serverAt(t, env, 1, "192.168.1.11"), serverAt(t, env, 2, "192.168.1.12")
	s1.sudoPw, s2.sudoPw = "saved-pw", "saved-pw"
	f := newAPIFixture(t, s1, s2)
	d := f.eng.dialer.(*fakeDialer)
	l := f.saveLogin(`{"name":"기본 계정","user":"ubuntu","method":"password","password":"saved-pw","sudo_same":true}`)

	first := f.startWith(l.ID, s1)
	f.eng.mu.Lock()
	held := f.eng.cur.auth.Password
	f.eng.mu.Unlock()
	f.approve(first, "s1")
	h := env.waitEnded(t, first)
	if h.Status != cluster.RunSucceeded {
		t.Fatalf("the first install = %s %+v, want succeeded", h.Status, env.row(first, "s1"))
	}
	if !isZero(held) || string(held) == "" {
		t.Fatalf("the install's copy of the password is %q after its end, want it zeroed", held)
	}
	if got := env.storedLogin(l.ID).Password; got != "saved-pw" {
		t.Fatalf("the store holds %q after the install's wipe, want the password", got)
	}
	inputs := slices.Sorted(maps.Keys(h.Inputs))
	if !slices.Equal(inputs, []string{"login", "mode", "review", "version"}) || h.Inputs["login"] != "기본 계정" {
		t.Fatalf("the run's inputs %v, want the login's name beside mode, review and version", h.Inputs)
	}

	second := f.startWith(l.ID, s2)
	env.waitRow(t, second, "s1", func(r cluster.RunNodeRecord) bool { return r.Awaiting == awaitHostKey }, "to wait for its host key")
	if st, ans := f.loginCall(http.MethodDelete, "/"+l.ID, ""); st != http.StatusOK {
		t.Fatalf("deleting the login under way = %d %+v", st, ans.Error)
	}
	f.approve(second, "s1")
	if h := env.waitEnded(t, second); h.Status != cluster.RunSucceeded || h.Inputs["login"] != "기본 계정" {
		t.Fatalf("the second install = %s %v %+v, want succeeded with the login named", h.Status, h.Inputs, env.row(second, "s1"))
	}
	logins := d.loginsGiven()
	if len(logins) < 2 {
		t.Fatalf("%d logins given, want at least one per install", len(logins))
	}
	for i, a := range logins {
		if a.User != "ubuntu" || a.Method != "password" || string(a.Password) != "saved-pw" {
			t.Fatalf("login %d given %s/%s/%q, want the saved user and password", i, a.User, a.Method, a.Password)
		}
	}
}

// A saved login is the whole account: any other account field beside it is
// 400 field login, and an ID no login has is 404. Nothing starts.
func TestASavedLoginIsTheWholeAccount(t *testing.T) {
	for _, extra := range []string{`"user":"ops"`, `"method":"password"`, `"password":"pw"`, `"key":"k"`, `"passphrase":"pp"`, `"sudo_password":"s"`, `"sudo_same":true`} {
		t.Run(extra, func(t *testing.T) {
			f := newAPIFixture(t)
			l := f.saveLogin(`{"name":"ops","user":"ops","method":"password","password":"pw","sudo_same":true}`)
			st, ans := f.start(`{"servers":[{"host":"192.168.1.11"}],"account":{"login":"` + l.ID + `",` + extra + `}}`)
			if st != http.StatusBadRequest || ans.Error.Code != response.ErrAgentInstallInvalid || ans.Error.Details["field"] != "login" {
				t.Fatalf("a login with %s = %d %+v, want 400 %s {field: login}", extra, st, ans.Error, response.ErrAgentInstallInvalid)
			}
			if f.runs() != 0 || f.eng.Active() {
				t.Fatal("a refused start started an install")
			}
		})
	}
	t.Run("an unknown login", func(t *testing.T) {
		f := newAPIFixture(t)
		st, ans := f.start(`{"servers":[{"host":"192.168.1.11"}],"account":{"login":"no-such-login"}}`)
		if st != http.StatusNotFound || ans.Error.Code != response.ErrAgentInstallLoginNotFound {
			t.Fatalf("an unknown login = %d %+v, want 404 %s", st, ans.Error, response.ErrAgentInstallLoginNotFound)
		}
		if f.runs() != 0 || f.eng.Active() {
			t.Fatal("a refused start started an install")
		}
	})
}

// A saved login that sends the server a secret, a password login's or a
// sudo password, goes only to a private address or a Tailscale address the
// leader's tailnet lists on a peer, and is refused before anything reaches
// the server, its host key included. A tailnet that cannot be read refuses.
// A saved key login without a sudo password sends no secret, and a password
// typed in the form is not limited.
func TestASavedSecretGoesOnlyToPrivateAndTailnetAddresses(t *testing.T) {
	const (
		passwordLogin = `{"name":"pw","user":"ops","method":"password","password":"sudo-pw","sudo_same":true}`
		keyLogin      = `{"name":"key","user":"ops","method":"key","key":"k"}`
		keySudoLogin  = `{"name":"key and sudo","user":"ops","method":"key","key":"k","sudo_password":"sudo-pw"}`
		typed         = `{"user":"ops","method":"password","password":"sudo-pw","sudo_same":true}`
	)
	listing := func(ip string) func(*fakeEnv) {
		return func(env *fakeEnv) { env.onTailnet(statusWith(map[string][]string{ip: nil})) }
	}
	cases := []struct {
		name, ip, login, typed string
		tailnet                func(*fakeEnv)
		// passwordless is a server whose sudo asks no password.
		passwordless, refused bool
		// log is what the connect phase's output says of the tailnet.
		log string
	}{
		{name: "a saved password to a public address", ip: "192.0.2.11", login: passwordLogin, refused: true},
		{name: "a saved password to 192.168/16", ip: "192.168.1.11", login: passwordLogin},
		{name: "a saved password to 10/8", ip: "10.0.0.11", login: passwordLogin},
		{name: "a saved password to a Tailscale address the tailnet lists", ip: "100.64.0.31", login: passwordLogin, tailnet: listing("100.64.0.31")},
		{name: "a saved password to a Tailscale address the tailnet does not list", ip: "100.64.0.31", login: passwordLogin, tailnet: listing("100.64.0.32"), refused: true},
		{name: "a saved password to a Tailscale address, the tailnet unreadable", ip: "100.64.0.31", login: passwordLogin, refused: true,
			tailnet: func(env *fakeEnv) {
				env.tailscale.SetOutput("exists:tailscale", "", nil)
				env.tailscale.SetOutput("tailscale", "failed to connect to local tailscaled\n", errors.New("exit status 1"))
			}, log: "the tailnet could not be read (tailscaled is not running)"},
		{name: "a saved password to a Tailscale address, no tailscale on the leader", ip: "100.64.0.31", login: passwordLogin, refused: true,
			log: "the tailnet could not be read (tailscale is not installed)"},
		{name: "a saved password to a Tailscale address, Tailscale stopped", ip: "100.64.0.31", login: passwordLogin, refused: true,
			tailnet: func(env *fakeEnv) {
				env.onTailnet(strings.Replace(statusWith(map[string][]string{"100.64.0.31": nil}), `"BackendState":"Running"`, `"BackendState":"Stopped"`, 1))
			}, log: "Tailscale on this leader is Stopped"},
		{name: "a saved key login without a sudo password to a public address", ip: "192.0.2.11", login: keyLogin, passwordless: true},
		{name: "a saved key login with a sudo password to a public address", ip: "192.0.2.11", login: keySudoLogin, refused: true},
		{name: "a typed password to a public address", ip: "192.0.2.11", typed: typed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newFakeEnv()
			s := serverAt(t, env, 1, tc.ip)
			if tc.passwordless {
				s.facts.Sudo = SudoNoPassword
			}
			if tc.tailnet != nil {
				tc.tailnet(env)
			}
			f := newAPIFixture(t, s)
			d := f.eng.dialer.(*fakeDialer)
			account := tc.typed
			if tc.login != "" {
				account = fmt.Sprintf(`{"login":%q}`, f.saveLogin(tc.login).ID)
			}
			st, ans := f.start(installBody(account, s))
			if st != http.StatusAccepted {
				t.Fatalf("start = %d %+v, want 202", st, ans.Error)
			}
			id := ans.Data.RunID
			r := env.waitRow(t, id, "s1", func(r cluster.RunNodeRecord) bool {
				return r.Awaiting == awaitHostKey || r.Status == cluster.RunNodeFailed
			}, "to ask for its host key or fail")
			if !tc.refused {
				if r.Status == cluster.RunNodeFailed {
					t.Fatalf("s1 failed %s %v, want it to ask for its host key", r.Reason, r.ReasonDetails)
				}
				f.approve(id, "s1")
				if h := env.waitEnded(t, id); h.Status != cluster.RunSucceeded {
					t.Fatalf("run %s, s1 %+v; want succeeded", h.Status, env.row(id, "s1"))
				}
				return
			}
			if r.Reason != response.ErrAgentInstallLoginOffNetwork || !maps.Equal(r.ReasonDetails, map[string]string{"address": tc.ip}) {
				t.Fatalf("s1 awaits %q, failed %q %v; want failed %s {address: %s}", r.Awaiting, r.Reason, r.ReasonDetails, response.ErrAgentInstallLoginOffNetwork, tc.ip)
			}
			if h := env.waitEnded(t, id); h.Status != cluster.RunFailed {
				t.Fatalf("run %s, want failed", h.Status)
			}
			if fetches, logins := d.reached(); fetches != 0 || logins != 0 || len(s.commands()) != 0 {
				t.Fatalf("%d host key fetches, %d logins and commands %q reached the server, want none", fetches, logins, s.commands())
			}
			v, err := f.eng.StepOutput(id, "s1", phaseConnect, 0)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(v.Output, "a password typed in the pane is not limited") || !strings.Contains(v.Output, tc.log) {
				t.Fatalf("the connect phase's output %q, want the refusal and %q", v.Output, tc.log)
			}
		})
	}
}
