package agentinstall

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/common/exec"
)

// tailnetFixture is `tailscale status --json` on a leader (mgr-01) whose
// tailnet holds: web-01, Linux, online, with Tailscale SSH host keys;
// db-01, Linux, offline; agent-01, Linux, online, an agent of the cluster;
// laptop-01, Windows; v6-only, Linux with no IPv4 address.
const tailnetFixture = `{
  "Version": "1.76.6-t1a2b3c4d5-g6e7f8a9b0",
  "TUN": true,
  "BackendState": "Running",
  "HaveNodeKey": true,
  "AuthURL": "",
  "TailscaleIPs": ["100.64.0.1", "fd7a:115c:a1e0::1"],
  "Self": {
    "ID": "nSELF11CNTRL",
    "PublicKey": "nodekey:0000000000000000000000000000000000000000000000000000000000000001",
    "HostName": "mgr-01",
    "DNSName": "mgr-01.example.ts.net.",
    "OS": "linux",
    "UserID": 1001,
    "TailscaleIPs": ["100.64.0.1", "fd7a:115c:a1e0::1"],
    "AllowedIPs": ["100.64.0.1/32", "fd7a:115c:a1e0::1/128"],
    "Addrs": ["192.0.2.1:41641"],
    "CurAddr": "",
    "Relay": "fra",
    "RxBytes": 0,
    "TxBytes": 0,
    "Created": "2026-01-10T08:00:00Z",
    "LastWrite": "0001-01-01T00:00:00Z",
    "LastSeen": "0001-01-01T00:00:00Z",
    "LastHandshake": "0001-01-01T00:00:00Z",
    "Online": true,
    "ExitNode": false,
    "ExitNodeOption": false,
    "Active": false,
    "PeerAPIURL": ["http://100.64.0.1:51234", "http://[fd7a:115c:a1e0::1]:51234"],
    "Capabilities": ["https://tailscale.com/cap/ssh"],
    "InNetworkMap": true,
    "InMagicSock": false,
    "InEngine": false
  },
  "Health": [],
  "MagicDNSSuffix": "example.ts.net",
  "CurrentTailnet": {"Name": "example.org", "MagicDNSSuffix": "example.ts.net", "MagicDNSEnabled": true},
  "CertDomains": ["mgr-01.example.ts.net"],
  "Peer": {
    "nodekey:0000000000000000000000000000000000000000000000000000000000000031": {
      "ID": "nWEB0111CNTRL",
      "PublicKey": "nodekey:0000000000000000000000000000000000000000000000000000000000000031",
      "HostName": "web-01",
      "DNSName": "web-01.example.ts.net.",
      "OS": "linux",
      "UserID": 1001,
      "TailscaleIPs": ["100.64.0.31", "fd7a:115c:a1e0::31"],
      "AllowedIPs": ["100.64.0.31/32", "fd7a:115c:a1e0::31/128"],
      "Addrs": ["192.0.2.31:41641"],
      "CurAddr": "192.0.2.31:41641",
      "Relay": "fra",
      "RxBytes": 18244,
      "TxBytes": 20516,
      "Created": "2026-02-01T10:00:00Z",
      "LastWrite": "2026-11-02T09:00:00Z",
      "LastSeen": "2026-11-02T09:00:00Z",
      "LastHandshake": "2026-11-02T08:59:30Z",
      "Online": true,
      "ExitNode": false,
      "ExitNodeOption": false,
      "Active": true,
      "PeerAPIURL": ["http://100.64.0.31:51234"],
      "sshHostKeys": [
        "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBOKhfqbAx9DX2gEKA5jC2subMzGXPBFCMDXyJMXA4yLtoCiqVR9fHNzLwyiCneWuz+OZjSZMHEwSDcA/hoF1cUI=",
        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAzKC5/VsMA/wolZLfViECnOLNNzXatTExHWMbQ8JYue"
      ],
      "InNetworkMap": true,
      "InMagicSock": true,
      "InEngine": true
    },
    "nodekey:0000000000000000000000000000000000000000000000000000000000000032": {
      "ID": "nDB0111CNTRL",
      "PublicKey": "nodekey:0000000000000000000000000000000000000000000000000000000000000032",
      "HostName": "db-01",
      "DNSName": "db-01.example.ts.net.",
      "OS": "linux",
      "UserID": 1001,
      "TailscaleIPs": ["100.64.0.32", "fd7a:115c:a1e0::32"],
      "Addrs": null,
      "CurAddr": "",
      "Relay": "fra",
      "Created": "2026-02-01T10:05:00Z",
      "LastSeen": "2026-10-30T22:14:00Z",
      "Online": false,
      "Active": false,
      "InNetworkMap": true,
      "InMagicSock": true,
      "InEngine": false
    },
    "nodekey:0000000000000000000000000000000000000000000000000000000000000033": {
      "ID": "nAGENT0111CNTRL",
      "PublicKey": "nodekey:0000000000000000000000000000000000000000000000000000000000000033",
      "HostName": "agent-01",
      "DNSName": "agent-01.example.ts.net.",
      "OS": "linux",
      "UserID": 1001,
      "TailscaleIPs": ["100.64.0.33", "fd7a:115c:a1e0::33"],
      "CurAddr": "192.0.2.33:41641",
      "Relay": "fra",
      "Created": "2026-03-01T10:00:00Z",
      "LastSeen": "2026-11-02T09:00:00Z",
      "Online": true,
      "Active": true,
      "InNetworkMap": true,
      "InMagicSock": true,
      "InEngine": true
    },
    "nodekey:0000000000000000000000000000000000000000000000000000000000000040": {
      "ID": "nLAPTOP0111CNTRL",
      "PublicKey": "nodekey:0000000000000000000000000000000000000000000000000000000000000040",
      "HostName": "laptop-01",
      "DNSName": "laptop-01.example.ts.net.",
      "OS": "windows",
      "UserID": 1001,
      "TailscaleIPs": ["100.64.0.40", "fd7a:115c:a1e0::40"],
      "Relay": "fra",
      "Online": true,
      "Active": false,
      "InNetworkMap": true,
      "InMagicSock": true,
      "InEngine": false
    },
    "nodekey:0000000000000000000000000000000000000000000000000000000000000050": {
      "ID": "nV6ONLY11CNTRL",
      "PublicKey": "nodekey:0000000000000000000000000000000000000000000000000000000000000050",
      "HostName": "v6-only",
      "DNSName": "v6-only.example.ts.net.",
      "OS": "linux",
      "UserID": 1001,
      "TailscaleIPs": ["fd7a:115c:a1e0::50"],
      "Relay": "fra",
      "Online": true,
      "Active": false,
      "InNetworkMap": true,
      "InMagicSock": true,
      "InEngine": false
    }
  },
  "User": {"1001": {"ID": 1001, "LoginName": "ops@example.org", "DisplayName": "ops", "ProfilePicURL": ""}},
  "ClientVersion": null
}
`

// tailnetEnvelope is the tailnet route's answer: data raw, so the whole of
// it can be compared.
type tailnetEnvelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (f *apiFixture) tailnet() (int, tailnetEnvelope) {
	f.t.Helper()
	f.do(http.MethodGet, "/api/v1/cluster/agent-installs/tailnet", "")
	f.mu.Lock()
	a := f.answers[len(f.answers)-1]
	f.mu.Unlock()
	var env tailnetEnvelope
	if err := json.Unmarshal([]byte(a.body), &env); err != nil {
		f.t.Fatalf("the answer %q is no JSON: %v", a.body, err)
	}
	return a.status, env
}

// sameJSON fails t unless got and want are the same JSON value.
func sameJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("%s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want %s: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("answer\n%s\nwant\n%s", got, want)
	}
}

// The leader's tailnet lists its Linux devices with an IPv4 address,
// online first, then by name; each says whether Tailscale reports SSH host
// keys for it, and the cluster node that holds its address.
func TestTheTailnetListsItsLinuxDevices(t *testing.T) {
	f := newAPIFixture(t)
	f.env.onTailnet(tailnetFixture)
	f.env.nodes["m1"] = cluster.Node{ID: "m1", Name: "mgr-01", Kind: cluster.KindManager, GRPCAddress: "192.0.2.1:3629", Addresses: []string{"192.0.2.1", "100.64.0.1"}}
	f.env.nodes["a1"] = cluster.Node{ID: "a1", Name: "agent-01", Kind: cluster.KindAgent, GRPCAddress: "100.64.0.33:3629"}

	status, env := f.tailnet()
	if status != http.StatusOK || !env.Success {
		t.Fatalf("GET tailnet = %d %+v, want 200", status, env)
	}
	sameJSON(t, env.Data, `{"on_tailscale": true, "reason": "", "peers": [
		{"name": "agent-01", "dns_name": "agent-01.example.ts.net", "ip": "100.64.0.33", "os": "linux", "online": true, "ssh": false, "node": "agent-01"},
		{"name": "web-01", "dns_name": "web-01.example.ts.net", "ip": "100.64.0.31", "os": "linux", "online": true, "ssh": true, "node": ""},
		{"name": "db-01", "dns_name": "db-01.example.ts.net", "ip": "100.64.0.32", "os": "linux", "online": false, "ssh": false, "node": ""}
	]}`)
	want := []exec.MockCall{{Name: "exists:tailscale"}, {Name: "tailscale", Args: []string{"status", "--json"}}}
	if !reflect.DeepEqual(f.env.tailscale.Calls, want) {
		t.Fatalf("commands %+v, want %+v", f.env.tailscale.Calls, want)
	}
}

// A leader not on Tailscale answers 200 with why: no tailscale command,
// Tailscale not running, or no IPv4 Tailscale address of its own. Each
// fixture would list the devices but for its one difference.
func TestALeaderOffTailscaleSaysWhy(t *testing.T) {
	for _, c := range []struct {
		name, reason string
		setup        func(*fakeEnv)
	}{
		{"no tailscale command", "not_installed", func(env *fakeEnv) {
			env.tailscale.SetOutput("tailscale", tailnetFixture, nil)
		}},
		{"Tailscale stopped", "not_running", func(env *fakeEnv) {
			env.onTailnet(strings.Replace(tailnetFixture, `"BackendState": "Running"`, `"BackendState": "Stopped"`, 1))
		}},
		{"no IPv4 address of its own", "no_address", func(env *fakeEnv) {
			env.onTailnet(strings.ReplaceAll(tailnetFixture, `"100.64.0.1", `, ``))
		}},
		// The daemon down: the command reaches no tailscaled, and says so in
		// either of the forms tailscale 1.96 prints.
		{"tailscaled not running", "not_running", func(env *fakeEnv) {
			env.tailscale.SetOutput("exists:tailscale", "", nil)
			env.tailscale.SetOutput("tailscale", "failed to connect to local tailscaled; it doesn't appear to be running (sudo systemctl start tailscaled ?)\n", errors.New("exit status 1"))
		}},
		{"tailscaled not answering", "not_running", func(env *fakeEnv) {
			env.tailscale.SetOutput("exists:tailscale", "", nil)
			env.tailscale.SetOutput("tailscale", "failed to connect to local tailscaled (which appears to be running as tailscaled, pid 1325). Got error: Failed to connect to local Tailscale daemon for /localapi/v0/status; not running? Error: dial unix /var/run/tailscale/tailscaled.sock: connect: no such file or directory\n", errors.New("exit status 1"))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newAPIFixture(t)
			c.setup(f.env)
			status, env := f.tailnet()
			if status != http.StatusOK {
				t.Fatalf("GET tailnet = %d %+v, want 200", status, env)
			}
			sameJSON(t, env.Data, `{"on_tailscale": false, "reason": "`+c.reason+`", "peers": []}`)
		})
	}
}

// A `tailscale status` that fails is 500 AGENT_INSTALL_TAILNET_FAILED with
// its output sanitized, and so is one that answers with no status; a
// warning printed before the status is read past.
func TestATailnetThatCannotBeReadIsAFailure(t *testing.T) {
	t.Run("the command fails", func(t *testing.T) {
		f := newAPIFixture(t)
		f.env.tailscale.SetOutput("exists:tailscale", "", nil)
		f.env.tailscale.SetOutput("tailscale", "\x1b[31mpermission denied: the local API needs root\x1b[0m\n", errors.New("exit status 1"))
		status, env := f.tailnet()
		if status != http.StatusInternalServerError || env.Error.Code != response.ErrAgentInstallTailnetFailed {
			t.Fatalf("GET tailnet = %d %+v, want 500 %s", status, env, response.ErrAgentInstallTailnetFailed)
		}
		if want := "tailscale status: permission denied: the local API needs root"; env.Error.Message != want {
			t.Fatalf("message %q, want %q", env.Error.Message, want)
		}
	})
	t.Run("no status in its output", func(t *testing.T) {
		f := newAPIFixture(t)
		f.env.onTailnet("not json\n")
		status, env := f.tailnet()
		if status != http.StatusInternalServerError || env.Error.Code != response.ErrAgentInstallTailnetFailed {
			t.Fatalf("GET tailnet = %d %+v, want 500 %s", status, env, response.ErrAgentInstallTailnetFailed)
		}
	})
	t.Run("a warning before the status", func(t *testing.T) {
		f := newAPIFixture(t)
		f.env.onTailnet("Warning: client version \"1.78.1\" != tailscaled server version \"1.76.6\"\n" + tailnetFixture)
		status, env := f.tailnet()
		if status != http.StatusOK {
			t.Fatalf("GET tailnet = %d %+v, want 200", status, env)
		}
		var ans TailnetAnswer
		if err := json.Unmarshal(env.Data, &ans); err != nil || !ans.OnTailscale || len(ans.Peers) != 3 {
			t.Fatalf("answer %s, want the three Linux devices", env.Data)
		}
	})
}

// A follower sends the request on to the leader, the node that reaches the
// servers, and reads nothing itself.
func TestAFollowerSendsTheTailnetOn(t *testing.T) {
	f := newAPIFixture(t)
	f.env.onTailnet(tailnetFixture)
	f.at = place{inCluster: true}
	f.do(http.MethodGet, "/api/v1/cluster/agent-installs/tailnet", "")
	if want := []string{"GET /api/v1/cluster/agent-installs/tailnet "}; !reflect.DeepEqual(f.forwarded, want) {
		t.Fatalf("forwarded %q, want %q", f.forwarded, want)
	}
	if n := f.env.tailnetReadCount(); n != 0 {
		t.Fatalf("the follower read its tailnet %d times", n)
	}
}

// statusWith is a running leader's `tailscale status --json` whose tailnet
// holds one Linux device per IP of keys, with those SSH host keys.
func statusWith(keys map[string][]string) string {
	peers := map[string]any{}
	i := 0
	for ip, ks := range keys {
		i++
		name := "dev-" + strings.ReplaceAll(ip, ".", "-")
		peers["nodekey:"+strings.Repeat("0", 62)+string(rune('a'+i))+"0"] = map[string]any{
			"HostName": name, "DNSName": name + ".example.ts.net.", "OS": "linux",
			"TailscaleIPs": []string{ip, "fd7a:115c:a1e0::" + string(rune('a'+i))},
			"Online":       true, "sshHostKeys": ks,
		}
	}
	b, _ := json.Marshal(map[string]any{
		"BackendState": "Running",
		"Self":         map[string]any{"HostName": "mgr-01", "OS": "linux", "TailscaleIPs": []string{"100.64.0.1", "fd7a:115c:a1e0::1"}},
		"Peer":         peers,
	})
	return string(b)
}

// authorizedKey is k as Tailscale lists it: "ssh-ed25519 AAAA…".
func authorizedKey(k ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

// otherTypeKeys are an ECDSA and an RSA host key, none of a fake server's
// ED25519 type.
var otherTypeKeys = []string{
	"ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBOKhfqbAx9DX2gEKA5jC2subMzGXPBFCMDXyJMXA4yLtoCiqVR9fHNzLwyiCneWuz+OZjSZMHEwSDcA/hoF1cUI=",
	"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAAgQDYiCqf3eD63JbyDBFw1+hnIRT1cV5UcCdLFSpWWQigL+rykdxJiC1YfX0AuMHcsotcjJGsNu3mlABH/QQbnCrwvmT8NH41UhH1wkfyUg8O3pEC8BGfvPIJYy0CeG8m7cCVX7uaJOaelAHMZs2aqtoQCVEGhK1/fJ0pEqw7vHu1DQ==",
}

// hostKeyQuestion starts an install of s and returns the row as it was
// first written asking for its host key, and the run's ID. Nothing has
// been sent to the server by then.
func hostKeyQuestion(t *testing.T, env *fakeEnv, s *fakeServer) (cluster.RunNodeRecord, string, *Engine) {
	t.Helper()
	d := newFakeDialer(s)
	e := newTestEngine(env, d)
	id, err := e.Start(context.Background(), request(s))
	if err != nil {
		t.Fatal(err)
	}
	env.waitRow(t, id, "s1", func(r cluster.RunNodeRecord) bool { return r.Awaiting == awaitHostKey }, "to wait for its host key")
	if dials, _, _ := d.counts(); dials != 0 || len(s.commands()) != 0 {
		t.Fatalf("before an approval: %d logins, commands %q; want none", dials, s.commands())
	}
	env.mu.Lock()
	writes := append([]string(nil), env.writes...)
	env.mu.Unlock()
	for _, w := range writes {
		var rec cluster.RunNodeRecord
		if json.Unmarshal([]byte(w), &rec) == nil && rec.Awaiting == awaitHostKey {
			return rec, id, e
		}
	}
	t.Fatal("no write asked for the host key")
	return cluster.RunNodeRecord{}, "", nil
}

// tailscaleServer is a passing server at the Tailscale address 100.64.0.31.
func tailscaleServer(t *testing.T, env *fakeEnv) *fakeServer {
	s := passingServer(t, env, 1)
	s.ip = net.ParseIP("100.64.0.31").To4()
	s.facts.Addrs = []net.IP{s.ip}
	return s
}

// The host key a server at a Tailscale address presents is compared with
// the keys of its type Tailscale reports for that device, and the row asks
// its question with the outcome beside it, in the same write. The answer
// stays the operator's: nothing is sent to the server before it, match or
// not.
func TestTheHostKeyIsComparedWithTailscales(t *testing.T) {
	t.Run("match", func(t *testing.T) {
		env := newFakeEnv()
		s := tailscaleServer(t, env)
		env.onTailnet(statusWith(map[string][]string{
			"100.64.0.31": append(cloned(otherTypeKeys), authorizedKey(s.hostKey.Key)),
		}))
		rec, id, e := hostKeyQuestion(t, env, s)
		if rec.HostKeyTailscale != hostKeyTailscaleMatch || rec.HostKey != "ED25519 "+s.hostKey.Fingerprint {
			t.Fatalf("the question %q / %q, want %q beside it", rec.HostKey, rec.HostKeyTailscale, hostKeyTailscaleMatch)
		}
		approveAll(t, e, env, id, "s1")
		h := env.waitEnded(t, id)
		if r := env.row(id, "s1"); h.Status != cluster.RunSucceeded || r.HostKeyTailscale != hostKeyTailscaleMatch {
			t.Fatalf("s1 = %+v, want succeeded, the match kept", r)
		}
	})
	t.Run("mismatch: another ED25519 key listed", func(t *testing.T) {
		env := newFakeEnv()
		s := tailscaleServer(t, env)
		env.onTailnet(statusWith(map[string][]string{
			"100.64.0.31": append(cloned(otherTypeKeys), authorizedKey(newHostKey(t).Key)),
		}))
		rec, id, e := hostKeyQuestion(t, env, s)
		if rec.HostKeyTailscale != hostKeyTailscaleMismatch {
			t.Fatalf("the question's comparison %q, want %q", rec.HostKeyTailscale, hostKeyTailscaleMismatch)
		}
		// Still the operator's to answer.
		if err := e.ApproveHostKey(id, "s1", rec.HostKey, true); err != nil {
			t.Fatal(err)
		}
		if h := env.waitEnded(t, id); h.Status != cluster.RunSucceeded {
			t.Fatalf("run = %s, want succeeded", h.Status)
		}
	})
	t.Run("only keys of other types listed", func(t *testing.T) {
		env := newFakeEnv()
		s := tailscaleServer(t, env)
		env.onTailnet(statusWith(map[string][]string{"100.64.0.31": otherTypeKeys}))
		rec, _, _ := hostKeyQuestion(t, env, s)
		if rec.HostKeyTailscale != "" || env.tailnetReadCount() != 1 {
			t.Fatalf("the question's comparison %q after %d reads, want none after one", rec.HostKeyTailscale, env.tailnetReadCount())
		}
	})
	t.Run("the device is not in the tailnet", func(t *testing.T) {
		env := newFakeEnv()
		s := tailscaleServer(t, env)
		// Another device lists this very key: only the one at the row's
		// address counts.
		env.onTailnet(statusWith(map[string][]string{"100.64.0.32": {authorizedKey(s.hostKey.Key)}}))
		rec, _, _ := hostKeyQuestion(t, env, s)
		if rec.HostKeyTailscale != "" || env.tailnetReadCount() != 1 {
			t.Fatalf("the question's comparison %q after %d reads, want none after one", rec.HostKeyTailscale, env.tailnetReadCount())
		}
	})
	t.Run("a LAN address reads no tailnet", func(t *testing.T) {
		env := newFakeEnv()
		s := passingServer(t, env, 1)
		env.onTailnet(statusWith(map[string][]string{"100.64.0.31": {authorizedKey(s.hostKey.Key)}}))
		rec, _, _ := hostKeyQuestion(t, env, s)
		if rec.HostKeyTailscale != "" || env.tailnetReadCount() != 0 {
			t.Fatalf("the question's comparison %q after %d reads, want none and no read", rec.HostKeyTailscale, env.tailnetReadCount())
		}
	})
	t.Run("a tailnet that cannot be read is said once and fails nothing", func(t *testing.T) {
		env := newFakeEnv()
		s := tailscaleServer(t, env)
		env.tailscale.SetOutput("exists:tailscale", "", nil)
		env.tailscale.SetOutput("tailscale", "failed to connect to local tailscaled\n", errors.New("exit status 1"))
		rec, id, e := hostKeyQuestion(t, env, s)
		if rec.HostKeyTailscale != "" {
			t.Fatalf("the question's comparison %q, want none", rec.HostKeyTailscale)
		}
		approveAll(t, e, env, id, "s1")
		if h := env.waitEnded(t, id); h.Status != cluster.RunSucceeded {
			t.Fatalf("run = %s, want succeeded", h.Status)
		}
		v, err := e.StepOutput(id, "s1", phaseConnect, 0)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(v.Output, "the tailnet could not be read"); n != 1 || !strings.Contains(v.Output, "tailscaled is not running") {
			t.Fatalf("the connect phase's log says it %d times: %q; want once, with why", n, v.Output)
		}
	})
}

// compareHostKey matches on the key, of the same type only.
func TestCompareHostKey(t *testing.T) {
	k := newHostKey(t)
	other := newHostKey(t)
	for _, c := range []struct {
		name   string
		listed []string
		want   string
	}{
		{"the key", []string{authorizedKey(k.Key)}, hostKeyTailscaleMatch},
		{"the key among others", append(cloned(otherTypeKeys), authorizedKey(other.Key), authorizedKey(k.Key)), hostKeyTailscaleMatch},
		{"another of its type", []string{authorizedKey(other.Key)}, hostKeyTailscaleMismatch},
		{"only other types", otherTypeKeys, ""},
		{"nothing listed", nil, ""},
		{"a line that is no key", []string{"ssh-ed25519 not-base64"}, ""},
	} {
		if got := compareHostKey(k.Key, c.listed); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// cloned copies s, so an append to it leaves s alone.
func cloned(s []string) []string { return append([]string(nil), s...) }
