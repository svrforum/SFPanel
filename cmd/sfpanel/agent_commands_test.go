package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	"github.com/svrforum/SFPanel/internal/paneltls"
)

func TestParseAgentJoinArgs(t *testing.T) {
	pin := "sha256:" + strings.Repeat("ab", 32)
	a, err := parseAgentJoinArgs([]string{"192.0.2.1:3629", "tok", "--ca", pin, "--advertise", "192.0.2.10", "--no-restart"})
	if err != nil || a.Manager != "192.0.2.1:3629" || a.Token != "tok" || a.Advertise != "192.0.2.10" || !a.NoRestart || a.ConfigPath != defaultCfgPath || a.Name != "" {
		t.Fatalf("%+v %v", a, err)
	}
	// A name of its own, for a server that shares its host name with another.
	if a, err := parseAgentJoinArgs([]string{"192.0.2.1:3629", "tok", "--name", "web-7", "--ca", pin}); err != nil || a.Name != "web-7" {
		t.Fatalf("%+v %v", a, err)
	}
	// Options go anywhere, and the pin is kept in the form the engine compares.
	a, err = parseAgentJoinArgs([]string{"--config", "/srv/sfpanel.yaml", "[2001:db8::1]:3629", "tok", "--ca", " SHA256:" + strings.Repeat("AB", 32)})
	if err != nil || a.ConfigPath != "/srv/sfpanel.yaml" || a.Manager != "[2001:db8::1]:3629" || a.Token != "tok" || a.Pin != pin || a.NoRestart {
		t.Fatalf("%+v %v", a, err)
	}
	for _, tc := range []struct {
		args   []string
		reason string
	}{
		{[]string{"192.0.2.1:3629", "tok"}, "--ca is missing"},
		{[]string{"192.0.2.1:3629", "--ca", "sha256:x"}, "the join token is missing"},
		{[]string{"--ca", pin}, "the manager's address and the join token are missing"},
		// No port is guessed: the command 'cluster token --agent' prints names it.
		{[]string{"192.0.2.1", "tok", "--ca", pin}, "needs a port"},
		{[]string{"192.0.2.1:http", "tok", "--ca", pin}, "needs a port"},
		// A malformed fingerprint is refused before anything is sent.
		{[]string{"192.0.2.1:3629", "tok", "--ca", "sha256:x"}, "the CA fingerprint must be sha256: and 64 hex characters"},
		{[]string{"192.0.2.1:3629", "tok", "--ca"}, "--ca needs a value"},
		{[]string{"192.0.2.1:3629", "tok", "--ca", pin, "--name"}, "--name needs a value"},
		{[]string{"192.0.2.1:3629", "tok", "--ca", pin, "--name", "../x"}, `the node name "../x" is not one`},
		// install.sh passes its arguments through: a misspelt option is not
		// skipped, and so neither is an address meant for --advertise.
		{[]string{"192.0.2.1:3629", "tok", "--ca", pin, "--advertize", "192.0.2.10"}, "unknown option --advertize"},
		{[]string{"192.0.2.1:3629", "tok", "192.0.2.10", "--ca", pin}, `unexpected argument "192.0.2.10"`},
	} {
		if _, err := parseAgentJoinArgs(tc.args); err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Fatalf("%v: %v", tc.args, err)
		}
	}
}

// An agent's panel must serve HTTPS (managers relay to it with its key), so a
// join turns managed TLS on when it is off; operator certificates need the
// CA file managers will trust.
func TestPreparePanelTLSForAgent(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.TLS.Dir = t.TempDir()
	ca, changed, err := preparePanelTLSForAgent(cfg)
	if err != nil || !changed || !cfg.Server.TLS.Enabled || !strings.Contains(string(ca), "BEGIN CERTIFICATE") {
		t.Fatalf("managed TLS: changed=%v err=%v", changed, err)
	}
	if want, err := paneltls.New(cfg.Server.TLS.Dir).CACertPEM(); err != nil || !bytes.Equal(ca, want) {
		t.Fatalf("not the CA behind the panel's certificate (%v)", err)
	}
	op := &config.Config{}
	op.Server.TLS.Enabled = true
	op.Server.TLS.CertFile, op.Server.TLS.KeyFile = "/x.crt", "/x.key"
	if _, _, err := preparePanelTLSForAgent(op); err == nil || !strings.Contains(err.Error(), "ca_file") {
		t.Fatalf("operator certificate without ca_file: %v", err)
	}
}

// install.sh writes tls.enabled before the panel first starts, so a join may
// find TLS on and no certificate yet: the CA is made then, and the config
// needs no change.
func TestPreparePanelTLSForAgentBeforeTheFirstStart(t *testing.T) {
	cfg := &config.Config{}
	cfg.Server.TLS = config.TLSConfig{Enabled: true, Dir: filepath.Join(t.TempDir(), "tls")}
	ca, changed, err := preparePanelTLSForAgent(cfg)
	if err != nil || changed || !strings.Contains(string(ca), "BEGIN CERTIFICATE") {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
}

// An operator's certificate stays the panel's: the CA its managers trust is
// ca_file, and a pair the panel could not start with is refused before the
// join — the agent would register and then fail every boot.
func TestPreparePanelTLSForAgentWithAnOperatorCertificate(t *testing.T) {
	dir := t.TempDir()
	m := paneltls.New(filepath.Join(dir, "operator"))
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	want, err := m.CACertPEM()
	if err != nil {
		t.Fatal(err)
	}
	managedDir := filepath.Join(dir, "managed")
	cfg := &config.Config{}
	cfg.Server.TLS = config.TLSConfig{Dir: managedDir, CertFile: m.ServerCertPath(), KeyFile: m.ServerKeyPath(), CAFile: m.CACertPath()}
	ca, changed, err := preparePanelTLSForAgent(cfg)
	if err != nil || !changed || !cfg.Server.TLS.Enabled || !bytes.Equal(ca, want) {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(managedDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("made a managed certificate beside the operator's")
	}
	cfg.Server.TLS.KeyFile = filepath.Join(dir, "missing.key")
	if _, _, err := preparePanelTLSForAgent(cfg); err == nil || !strings.Contains(err.Error(), "server.tls.key_file") {
		t.Fatalf("a key file that is not there: %v", err)
	}
	other := paneltls.New(filepath.Join(dir, "other"))
	if _, err := other.Ensure(); err != nil {
		t.Fatal(err)
	}
	cfg.Server.TLS.KeyFile = other.ServerKeyPath()
	if _, _, err := preparePanelTLSForAgent(cfg); err == nil || !strings.Contains(err.Error(), "do not load as a pair") {
		t.Fatalf("a key that is not the certificate's: %v", err)
	}
}

// The agent is reached at the advertise IP by its managers and at 127.0.0.1
// by its own gRPC hop, both verifying the panel's certificate.
func TestPanelCertCheckWantsLoopbackAndTheAdvertiseIP(t *testing.T) {
	dir := t.TempDir()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Operator CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	leafFor := func(ips ...string) *config.Config {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "panel"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		for _, ip := range ips {
			tmpl.IPAddresses = append(tmpl.IPAddresses, net.ParseIP(ip))
		}
		der, _ := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
		sub := t.TempDir()
		write := func(name string, typ string, b []byte) string {
			p := filepath.Join(sub, name)
			_ = os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: b}), 0o600)
			return p
		}
		keyDER, _ := x509.MarshalECPrivateKey(key)
		cfg := &config.Config{}
		cfg.Server.TLS = config.TLSConfig{Enabled: true, Dir: dir, CertFile: write("panel.crt", "CERTIFICATE", der), KeyFile: write("panel.key", "EC PRIVATE KEY", keyDER), CAFile: write("ca.crt", "CERTIFICATE", caDER)}
		return cfg
	}
	if err := panelCertCheck(leafFor("192.0.2.10"))("192.0.2.10"); err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("a certificate without 127.0.0.1: %v", err)
	}
	if err := panelCertCheck(leafFor("127.0.0.1"))("192.0.2.10"); err == nil || !strings.Contains(err.Error(), "192.0.2.10") {
		t.Fatalf("a certificate without the advertise IP: %v", err)
	}
	if err := panelCertCheck(leafFor("127.0.0.1", "192.0.2.10"))("192.0.2.10"); err != nil {
		t.Fatalf("a certificate for both: %v", err)
	}
}

// stubSystemctl stands in for systemctl for one test: it records each call
// and answers is-active as a running service or a stopped one.
func stubSystemctl(t *testing.T, active bool) *[][]string {
	t.Helper()
	orig := runSystemctl
	t.Cleanup(func() { runSystemctl = orig })
	calls := &[][]string{}
	runSystemctl = func(args ...string) error {
		*calls = append(*calls, args)
		if len(args) > 0 && args[0] == "is-active" && !active {
			return errors.New("inactive")
		}
		return nil
	}
	return calls
}

// agentResetTarget is an agent's config and files as a join leaves them, and
// the path its config is saved to.
func agentResetTarget(t *testing.T) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	certDir := filepath.Join(dir, "certs")
	if err := os.MkdirAll(certDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ca.crt", "node.crt", "node.key", "agent.key"} {
		if err := os.WriteFile(filepath.Join(certDir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{}
	cfg.Server.Port = 3628
	cfg.Database.Path = filepath.Join(dir, "db")
	cfg.Auth.JWTSecret = strings.Repeat("a", 64)
	cfg.Cluster = config.ClusterConfig{Enabled: true, Role: config.ClusterRoleAgent, NodeID: "a1", AdvertiseAddress: "192.0.2.10", CertDir: certDir, DataDir: filepath.Join(dir, "data"),
		Managers: []config.ManagerRef{{ID: "m1", GRPCAddress: "192.0.2.1:3629"}}}
	if err := (&cluster.AgentState{Managers: cfg.Cluster.Managers, Removed: true}).Save(agentStatePath(cfg)); err != nil {
		t.Fatal(err)
	}
	return cfg, filepath.Join(dir, "config.yaml")
}

// agentFilesLeft are the agent's files still on disk.
func agentFilesLeft(cfg *config.Config) []string {
	var left []string
	for _, p := range agentResetFiles(cfg) {
		if _, err := os.Stat(p); err == nil {
			left = append(left, filepath.Base(p))
		}
	}
	return left
}

func TestAgentReset(t *testing.T) {
	cfg, path := agentResetTarget(t)
	if left := agentFilesLeft(cfg); len(left) != 5 {
		t.Fatalf("the fixture has %v", left)
	}
	calls := stubSystemctl(t, true)
	if err := agentReset(cfg, path); err != nil {
		t.Fatal(err)
	}
	if left := agentFilesLeft(cfg); len(left) != 0 {
		t.Fatalf("kept %v", left)
	}
	saved, err := config.Load(path)
	if err != nil || saved.Cluster.Enabled || saved.Cluster.IsAgent() || saved.Cluster.NodeID != "" || len(saved.Cluster.Managers) != 0 || saved.Auth.JWTSecret == strings.Repeat("a", 64) {
		t.Fatalf("config after reset: %+v %v", saved.Cluster, err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("config file: %v %v", st, err)
	}
	if want := [][]string{{"is-active", "--quiet", "sfpanel"}, {"restart", "sfpanel"}}; !slices.EqualFunc(*calls, want, slices.Equal) {
		t.Fatalf("systemctl calls %v, want %v", *calls, want)
	}
}

// A service that is not running is not started: the operator starts it.
func TestAgentResetLeavesAStoppedServiceStopped(t *testing.T) {
	cfg, path := agentResetTarget(t)
	calls := stubSystemctl(t, false)
	if err := agentReset(cfg, path); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"is-active", "--quiet", "sfpanel"}}; !slices.EqualFunc(*calls, want, slices.Equal) {
		t.Fatalf("systemctl calls %v, want %v", *calls, want)
	}
}

// A manager leaves through Raft ('cluster leave'): clearing its cluster block
// here would strand its record and its Raft data. Refused, with nothing
// touched.
func TestAgentResetRefusesWhatIsNotAnAgent(t *testing.T) {
	cfg, path := agentResetTarget(t)
	cfg.Cluster.Role = ""
	calls := stubSystemctl(t, true)
	if err := agentReset(cfg, path); err == nil || !strings.Contains(err.Error(), "not an agent but a cluster manager") {
		t.Fatalf("a manager: %v", err)
	}
	if left := agentFilesLeft(cfg); len(left) != 5 {
		t.Fatalf("removed files: %v left", left)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("wrote the config")
	}
	if len(*calls) != 0 {
		t.Fatalf("ran systemctl %v", *calls)
	}
}

// The config goes first. An agent config whose key is gone stops every boot —
// an agent never falls back to a standalone panel — while files left beside a
// standalone config do nothing; so a config that cannot be saved leaves the
// agent as it was.
func TestAgentResetSavesTheConfigFirst(t *testing.T) {
	cfg, _ := agentResetTarget(t)
	calls := stubSystemctl(t, true)
	err := agentReset(cfg, filepath.Join(t.TempDir(), "missing", "config.yaml"))
	if err == nil || !strings.Contains(err.Error(), "save the config") {
		t.Fatalf("got %v", err)
	}
	if left := agentFilesLeft(cfg); len(left) != 5 {
		t.Fatalf("removed files the agent boots from: %v left", left)
	}
	if !cfg.Cluster.IsAgent() || len(*calls) != 0 {
		t.Fatalf("role %q, systemctl %v", cfg.Cluster.Role, *calls)
	}
}

// Without --yes, reset only says what it would do — and that the cluster
// keeps its record of the node, unless the leader removed it already.
func TestAgentResetPlan(t *testing.T) {
	cfg, path := agentResetTarget(t)
	plan := agentResetPlan(cfg, path)
	for _, p := range agentResetFiles(cfg) {
		if !strings.Contains(plan, p) {
			t.Fatalf("the plan does not name %s:\n%s", p, plan)
		}
	}
	if !strings.Contains(plan, path) || strings.Contains(plan, "cluster remove") {
		t.Fatalf("plan for a removed agent:\n%s", plan)
	}
	if left := agentFilesLeft(cfg); len(left) != 5 {
		t.Fatalf("the plan removed files: %v left", left)
	}
	if err := (&cluster.AgentState{Managers: cfg.Cluster.Managers}).Save(agentStatePath(cfg)); err != nil {
		t.Fatal(err)
	}
	if plan := agentResetPlan(cfg, path); !strings.Contains(plan, "sfpanel cluster remove a1") {
		t.Fatalf("plan for an agent still listed:\n%s", plan)
	}
}

// Every cluster command assumes a manager — Raft, the cluster API, a JWT an
// agent's panel does not take — so on an agent they stop and say where to go
// instead. help still answers.
func TestClusterCommandsRefuseOnAnAgent(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, c config.ClusterConfig) string {
		p := filepath.Join(dir, name)
		data, err := yaml.Marshal(&config.Config{Cluster: c})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	agent := write("agent.yaml", config.ClusterConfig{Enabled: true, Role: config.ClusterRoleAgent})
	manager := write("manager.yaml", config.ClusterConfig{Enabled: true})
	for _, args := range [][]string{
		{"status", "--config", agent}, {"leave", "--config", agent}, {"token", "--agent", "--config", agent}, {"bogus", "--config", agent},
	} {
		if got := clusterRefusal(args); got != "this server is an agent; use 'sfpanel agent status' or 'sfpanel agent reset'" {
			t.Fatalf("%v: %q", args, got)
		}
	}
	for _, args := range [][]string{
		{}, {"help"}, {"help", "--config", agent},
		{"status", "--config", manager}, {"init", "--config", filepath.Join(dir, "missing.yaml")},
	} {
		if got := clusterRefusal(args); got != "" {
			t.Fatalf("%v: %q", args, got)
		}
	}
}

// A server in a cluster does not join as an agent — and an agent config boots
// as an agent whatever cluster.enabled says. A state file an earlier
// membership left is removed first: the agent would prefer it to the managers
// it registers with, and it may say the agent was removed.
func TestReadyForAgentJoin(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	standalone := &config.Config{}
	standalone.Cluster.DataDir = dataDir
	state := agentStatePath(standalone)
	if err := (&cluster.AgentState{Removed: true}).Save(state); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		c      config.ClusterConfig
		reason string
	}{
		{"a manager", config.ClusterConfig{Enabled: true, DataDir: dataDir}, "is a cluster manager"},
		{"an agent", config.ClusterConfig{Enabled: true, Role: config.ClusterRoleAgent, DataDir: dataDir}, "is an agent already"},
		{"an agent with enabled off", config.ClusterConfig{Role: config.ClusterRoleAgent, DataDir: dataDir}, "is an agent already"},
	} {
		if err := readyForAgentJoin(&config.Config{Cluster: tc.c}); err == nil || !strings.Contains(err.Error(), tc.reason) {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if _, err := os.Stat(state); err != nil {
			t.Fatalf("%s: the state file went: %v", tc.name, err)
		}
	}
	if err := readyForAgentJoin(standalone); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a stale state file kept: %v", err)
	}
	if err := readyForAgentJoin(standalone); err != nil {
		t.Fatalf("no state file: %v", err)
	}
}

// The files a cluster manager leaves in its cert and data directories hold
// the cluster's keys — the CA key, every agent's key, the accounts — and an
// agent joined over them kept them all.
func TestAgentJoinRefusesAManagersLeftovers(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.Cluster.CertDir = filepath.Join(dir, "certs")
	cfg.Cluster.DataDir = filepath.Join(dir, "data")
	for _, f := range []string{filepath.Join(cfg.Cluster.CertDir, "ca.key"), filepath.Join(cfg.Cluster.DataDir, "raft-log.db")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	err := readyForAgentJoin(cfg)
	if err == nil || !strings.Contains(err.Error(), "ca.key") || !strings.Contains(err.Error(), "raft-log.db") {
		t.Fatalf("got %v", err)
	}
}

// Status reads the agent's files and its panel's loopback health — with no
// JWT, which an agent's panel does not take — and lists the managers from the
// state file the link keeps, ahead of those the agent registered with.
func TestAgentStatus(t *testing.T) {
	dir := t.TempDir()
	var credential atomic.Bool
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			credential.Store(true)
		}
		if r.URL.Path != "/api/v1/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"status":"ok","version":"9.9.9"}}`))
	}))
	t.Cleanup(panel.Close)
	u, err := url.Parse(panel.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())

	cfg := &config.Config{}
	cfg.Server.Port = port
	cfg.Cluster = config.ClusterConfig{Enabled: true, Role: config.ClusterRoleAgent, Name: "test-cluster", NodeID: "agent-1", NodeName: "host-a",
		AdvertiseAddress: "192.0.2.10", CertDir: filepath.Join(dir, "certs"), DataDir: filepath.Join(dir, "data"),
		Managers: []config.ManagerRef{{ID: "m1", GRPCAddress: "192.0.2.1:3629"}}}
	ca := cluster.NewTLSManager(filepath.Join(dir, "ca"))
	if err := ca.InitCA("test"); err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := ca.IssueNodeCert("agent-1", []string{"192.0.2.10"}, cluster.CertRoleAgent)
	if err != nil {
		t.Fatal(err)
	}
	tm := cluster.NewTLSManager(cfg.Cluster.CertDir)
	if err := os.MkdirAll(cfg.Cluster.CertDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := tm.SaveNodeCert(certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("5c", 32)
	if err := tm.SaveAgentKey(key); err != nil {
		t.Fatal(err)
	}
	leaf, err := tm.NodeCertLeaf()
	if err != nil {
		t.Fatal(err)
	}
	status := func() string {
		var out bytes.Buffer
		writeAgentStatus(&out, cfg)
		if strings.Contains(out.String(), key) {
			t.Fatalf("status printed the agent key:\n%s", out.String())
		}
		return out.String()
	}

	got := status()
	for _, want := range []string{"Cluster: test-cluster", "Node ID: agent-1", "Advertise: 192.0.2.10", "  m1  192.0.2.1:3629", "Removed: no",
		"valid until " + leaf.NotAfter.Format("2006-01-02"), "Agent key: present", "(version 9.9.9)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("no %q in:\n%s", want, got)
		}
	}

	// What the leader last said wins over the registration.
	if err := (&cluster.AgentState{Managers: []config.ManagerRef{{ID: "m2", GRPCAddress: "192.0.2.2:3629"}}, Leader: "192.0.2.2:3629", Removed: true}).Save(agentStatePath(cfg)); err != nil {
		t.Fatal(err)
	}
	got = status()
	if !strings.Contains(got, "  m2  192.0.2.2:3629") || strings.Contains(got, "192.0.2.1:3629") || !strings.Contains(got, "Leader: 192.0.2.2:3629") ||
		!strings.Contains(got, "Removed: yes") || !strings.Contains(got, "sfpanel agent reset --yes") {
		t.Fatalf("after a heartbeat reply:\n%s", got)
	}
	if credential.Load() {
		t.Fatal("status sent a credential to the panel")
	}

	panel.Close()
	if got := status(); !strings.Contains(got, "Panel: not answering") {
		t.Fatalf("a panel that is down:\n%s", got)
	}
}
