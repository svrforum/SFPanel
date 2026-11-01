package agentinstall

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/sshexec"
)

// fakeEnv is the cluster and the release as a test sets them up: it keeps
// the run record, mints and revokes tokens, and registers an agent when a
// fake server's install says so.
type fakeEnv struct {
	mu sync.Mutex

	version   string
	managers  map[string]string
	activeRun string
	archives  string
	// archivesAsked counts reads of SFPANEL_AGENT_ARCHIVES.
	archivesAsked int
	notLeader     bool
	now           time.Time
	script        []byte
	sums          []byte
	sumsAsked     int
	archiveAsked  []string
	ready         func(name string, ip net.IP) error
	joinAddr      string

	nodes   map[string]cluster.Node
	minted  []mintedToken
	revoked []string
	removed []string
	labels  map[string]map[string]string
	// heartbeat, proxy and panel answer the online checks; nil answers as
	// a healthy agent at the leader's version.
	heartbeat    func(id string) (bool, bool, string)
	proxy, panel func(id string) (int, error)
	proxyCalls   int

	runs   map[string]cluster.RunRecord
	rows   map[string]map[string]cluster.RunNodeRecord
	writes []string
	audits []string

	// engine, when set, is asked at each run write whether it owns the run.
	engine      *Engine
	unownedPuts int
	// beforeNodes, when set, runs before the nodes are listed.
	beforeNodes func()
}

type mintedToken struct {
	ip    string
	token string
}

func newFakeEnv() *fakeEnv {
	return &fakeEnv{
		version: "0.81.0", managers: map[string]string{"m2": "0.81.0"},
		now:      time.Unix(1_800_000_000, 0),
		script:   []byte("#!/bin/bash\n# the install script\n"),
		sums:     []byte("abc  sfpanel_0.81.0_linux_amd64.tar.gz\n"),
		joinAddr: "192.0.2.1:3629",
		nodes:    map[string]cluster.Node{}, labels: map[string]map[string]string{},
		runs: map[string]cluster.RunRecord{}, rows: map[string]map[string]cluster.RunNodeRecord{},
	}
}

func (f *fakeEnv) IsLeader() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.notLeader
}

func (f *fakeEnv) Version() string { return f.version }

func (f *fakeEnv) ManagerVersions() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.managers)
}

func (f *fakeEnv) ActiveRun() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.activeRun
}

func (f *fakeEnv) Nodes() []cluster.Node {
	if f.beforeNodes != nil {
		f.beforeNodes()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cluster.Node
	for _, n := range f.nodes {
		n.Labels = maps.Clone(n.Labels)
		out = append(out, n)
	}
	return out
}

func (f *fakeEnv) ReadyForAgentRegistration(name string, ip net.IP) error {
	if f.ready != nil {
		return f.ready(name, ip)
	}
	return nil
}

func (f *fakeEnv) JoinAddressFor(net.IP) (string, error) { return f.joinAddr, nil }

func (f *fakeEnv) CAPin() (string, error) { return "sha256:" + strings.Repeat("ab", 32), nil }

func (f *fakeEnv) CreateAgentToken(ip net.IP) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tok := fmt.Sprintf("%048x.%064x", len(f.minted)+1, 0xfeed)
	f.minted = append(f.minted, mintedToken{ip: ip.String(), token: tok})
	return tok, nil
}

func (f *fakeEnv) RevokeToken(token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, token)
	return nil
}

func (f *fakeEnv) Heartbeat(id string) (bool, bool, string) {
	if f.heartbeat != nil {
		return f.heartbeat(id)
	}
	return true, true, f.version
}

func (f *fakeEnv) ProxyHealth(_ context.Context, id string) (int, error) {
	f.mu.Lock()
	f.proxyCalls++
	f.mu.Unlock()
	if f.proxy != nil {
		return f.proxy(id)
	}
	return 200, nil
}

func (f *fakeEnv) PanelHealth(_ context.Context, id string) (int, error) {
	if f.panel != nil {
		return f.panel(id)
	}
	return 200, nil
}

func (f *fakeEnv) RemoveNode(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	delete(f.nodes, id)
	return nil
}

func (f *fakeEnv) UpdateNodeLabels(id string, labels map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.labels[id] = maps.Clone(labels)
	n := f.nodes[id]
	n.Labels = maps.Clone(labels)
	f.nodes[id] = n
	return nil
}

func (f *fakeEnv) PutRun(r cluster.RunRecord) error {
	if f.engine != nil && !f.engine.Owns(r.ID) {
		f.mu.Lock()
		f.unownedPuts++
		f.mu.Unlock()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notLeader {
		return cluster.ErrNotLeader
	}
	f.runs[r.ID] = r
	b, _ := json.Marshal(r)
	f.writes = append(f.writes, string(b))
	return nil
}

func (f *fakeEnv) PutRunNode(runID, key string, rec cluster.RunNodeRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.notLeader {
		return cluster.ErrNotLeader
	}
	if _, ok := f.runs[runID]; !ok {
		return cluster.ErrTaskRunNotFound
	}
	if f.rows[runID] == nil {
		f.rows[runID] = map[string]cluster.RunNodeRecord{}
	}
	f.rows[runID][key] = rec
	b, _ := json.Marshal(rec)
	f.writes = append(f.writes, string(b))
	return nil
}

func (f *fakeEnv) Audit(user, method, path string, status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.audits = append(f.audits, fmt.Sprintf("%s %s %s %d", user, method, path, status))
}

func (f *fakeEnv) Checksums(context.Context, string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sumsAsked++
	return f.sums, nil
}

func (f *fakeEnv) Archive(_ context.Context, version, arch string, _ []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archiveAsked = append(f.archiveAsked, arch)
	return []byte("archive " + version + " " + arch), nil
}

func (f *fakeEnv) Archives() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.archivesAsked++
	return f.archives
}

func (f *fakeEnv) InstallScript() []byte { return f.script }

func (f *fakeEnv) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeEnv) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// register records an agent at ip, as a registration through install.sh's
// join would.
func (f *fakeEnv) register(ip net.IP, name string, labels map[string]string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := "a-" + name
	f.nodes[id] = cluster.Node{ID: id, Name: name, Kind: cluster.KindAgent,
		GRPCAddress: net.JoinHostPort(ip.String(), "3629"), APIAddress: net.JoinHostPort(ip.String(), "3628"),
		Labels: maps.Clone(labels)}
	return id
}

func (f *fakeEnv) head(id string) cluster.RunRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs[id]
}

func (f *fakeEnv) row(id, key string) cluster.RunNodeRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[id][key]
}

func (f *fakeEnv) mintedFor() []mintedToken {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.minted)
}

func (f *fakeEnv) revokedTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.revoked)
}

func (f *fakeEnv) removedNodes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.removed)
}

func (f *fakeEnv) allWrites() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.writes, "\n")
}

// fakeServer is a server the fake dialer reaches: it answers the login
// check and the probe from facts, keeps the files uploaded to it, and runs
// the install as the test says.
type fakeServer struct {
	env     *fakeEnv
	ip      net.IP
	hostKey sshexec.HostKey
	facts   Facts
	// sudoPw is the sudo password sudo -S accepts.
	sudoPw string
	// installExit is install.sh's exit status; installHang leaves it
	// running; noRegister ends it 0 without an agent.
	installExit int
	installHang bool
	noRegister  bool
	// registerAnyway registers the agent as install.sh starts, whatever
	// installExit says: the join succeeded and a later step failed, or is
	// still running.
	registerAnyway bool
	// dropAfterInstall breaks the connection that started install.sh: its
	// next commands cannot be run. A new connection works.
	dropAfterInstall bool
	// registerOnStop registers the agent as the install unit is stopped:
	// a join admitted before the stop that lands after it.
	registerOnStop bool
	// revokedAtStop says whether the row's token was revoked by the time
	// the install unit was stopped.
	revokedAtStop bool
	labels        map[string]string
	// probeDelay holds the probe, so rows overlap.
	probeDelay time.Duration
	// keyChanged has Dial meet another host key; authFail refuses the login.
	keyChanged, authFail bool

	mu    sync.Mutex
	execs []fakeExec
	// roots are the commands as they ran, sudo taken off, and install.sh's
	// script taken out of systemd-run.
	roots   []string
	uploads map[string]fakeUpload
	files   map[string][]byte
	dir     string
}

type fakeExec struct {
	cmd   string
	stdin []byte
}

type fakeUpload struct {
	mode os.FileMode
	data []byte
}

func newHostKey(t *testing.T) sshexec.HostKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return sshexec.HostKey{Type: k.Type(), Fingerprint: ssh.FingerprintSHA256(k), Key: k}
}

// passingServer is a server every check passes, at 192.0.2.<10+n>, whose
// login asks sudo for "sudo-pw".
func passingServer(t *testing.T, env *fakeEnv, n int) *fakeServer {
	ip := net.IPv4(192, 0, 2, byte(10+n)).To4()
	f := passingFacts()
	f.Login = Login{UID: 1000, Sudo: SudoPassword, Bash: true}
	f.Hostname = "web-" + strconv.Itoa(n)
	f.Addrs = []net.IP{ip}
	f.Reach = []bool{true}
	f.Now = env.now
	return &fakeServer{env: env, ip: ip, hostKey: newHostKey(t), facts: f, sudoPw: "sudo-pw",
		uploads: map[string]fakeUpload{}, files: map[string][]byte{}}
}

func (s *fakeServer) addr() string { return net.JoinHostPort(s.ip.String(), "22") }

// unquote reads back one sshexec.ShellQuote word.
func unquote(w string) string {
	w = strings.TrimPrefix(strings.TrimSuffix(w, "'"), "'")
	return strings.ReplaceAll(w, `'\''`, `'`)
}

func loginText(l Login) string {
	b := "0"
	if l.Bash {
		b = "1"
	}
	return fmt.Sprintf("uid=%d\nbash=%s\nsudo=%s\n", l.UID, b, l.Sudo)
}

func flagText(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func probeText(f Facts) string {
	var w strings.Builder
	kv := func(k, v string) { fmt.Fprintf(&w, "%s=%s\n", k, v) }
	kv("os_id", f.OSID)
	kv("os_like", f.OSLike)
	kv("debian", flagText(f.Debian))
	kv("arch", f.Arch)
	kv("systemd", flagText(f.Systemd))
	kv("hostname", f.Hostname)
	var addrs []string
	for _, a := range f.Addrs {
		addrs = append(addrs, a.String())
	}
	kv("addrs", strings.Join(addrs, ","))
	kv("sfpanel_version", f.SFPanelVersion)
	kv("role", f.Role)
	kv("cluster_enabled", flagText(f.ClusterEnabled))
	kv("manager_files", strings.Join(f.ManagerFiles, ","))
	kv("operator_tls_no_ca", flagText(f.OperatorTLSNoCA))
	var ports, busy []string
	for _, p := range f.Ports {
		ports = append(ports, strconv.Itoa(p))
	}
	for _, b := range f.PortsBusy {
		busy = append(busy, fmt.Sprintf("%d:%s", b.Port, b.Owner))
	}
	kv("ports", strings.Join(ports, ","))
	kv("ports_busy", strings.Join(busy, ","))
	kv("tools_missing", strings.Join(f.ToolsMissing, ","))
	kv("disk_kb", strconv.FormatInt(f.DiskKB, 10))
	kv("opt_stacks", flagText(f.OptStacks))
	kv("now", strconv.FormatInt(f.Now.Unix(), 10))
	for i, r := range f.Reach {
		kv("reach_"+strconv.Itoa(i), flagText(r))
	}
	kv("github", flagText(f.GitHub))
	return w.String()
}

const (
	sudoWithPassword = "sudo -k -S -p '' bash -c "
	sudoNoPassword   = "sudo -n bash -c "
)

// run answers one command as the server would.
func (s *fakeServer) run(ctx context.Context, cmd string, stdin []byte) ([]byte, []byte, int, error) {
	s.mu.Lock()
	s.execs = append(s.execs, fakeExec{cmd: cmd, stdin: slices.Clone(stdin)})
	s.mu.Unlock()
	inner := cmd
	switch {
	case strings.HasPrefix(cmd, sudoWithPassword):
		line, rest, _ := bytes.Cut(stdin, []byte("\n"))
		if string(line) != s.sudoPw {
			return nil, []byte("Sorry, try again.\nsudo: 3 incorrect password attempts\n"), 1, nil
		}
		stdin = rest
		inner = unquote(strings.TrimPrefix(cmd, sudoWithPassword))
	case strings.HasPrefix(cmd, sudoNoPassword):
		if s.facts.Sudo != SudoNoPassword {
			return nil, []byte("sudo: a password is required\n"), 1, nil
		}
		inner = unquote(strings.TrimPrefix(cmd, sudoNoPassword))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	root := inner
	if rest, ok := strings.CutPrefix(inner, "systemd-run "); ok {
		if _, script, ok := strings.Cut(rest, " bash -c "); ok {
			root = "systemd-run: " + unquote(script)
		}
	}
	s.roots = append(s.roots, root)
	switch {
	case inner == "sh -s" && string(stdin) == LoginCheckScript():
		return []byte("Welcome!\n" + loginText(s.facts.Login)), nil, 0, nil
	case inner == "bash -s":
		if s.probeDelay > 0 {
			s.mu.Unlock()
			select {
			case <-time.After(s.probeDelay):
			case <-ctx.Done():
			}
			s.mu.Lock()
		}
		return []byte(probeText(s.facts)), nil, 0, nil
	case strings.HasPrefix(inner, "mktemp -d "):
		s.dir = strings.Replace(unquote(strings.TrimPrefix(inner, "mktemp -d ")), "XXXXXXXXXX", "abcDEF0123", 1)
		return []byte(s.dir + "\n"), nil, 0, nil
	case strings.HasPrefix(inner, "systemd-run "):
		if s.registerAnyway {
			s.env.register(s.ip, s.facts.Hostname, s.labels)
		}
		if !s.installHang {
			s.files[s.dir+"/install.log"] = []byte("\x1b[0;32m[INFO]\x1b[0m installing\n")
			s.files[s.dir+"/exit"] = []byte(strconv.Itoa(s.installExit) + "\n")
			if s.installExit == 0 && !s.noRegister && !s.registerAnyway {
				s.env.register(s.ip, s.facts.Hostname, s.labels)
			}
		}
		return nil, nil, 0, nil
	case strings.HasPrefix(inner, "cat "):
		return s.files[unquote(strings.TrimPrefix(inner, "cat "))], nil, 0, nil
	case strings.HasPrefix(inner, "tail -c +"):
		off, path, _ := strings.Cut(strings.TrimPrefix(inner, "tail -c +"), " ")
		n, _ := strconv.Atoi(off)
		b := s.files[unquote(path)]
		if n-1 > len(b) {
			return nil, nil, 0, nil
		}
		return b[n-1:], nil, 0, nil
	case strings.HasPrefix(inner, "systemctl stop "):
		s.revokedAtStop = len(s.env.revokedTokens()) > 0
		if s.registerOnStop {
			s.env.register(s.ip, s.facts.Hostname, s.labels)
		}
		return nil, nil, 0, nil
	case strings.HasSuffix(inner, " uninstall --purge"),
		strings.HasPrefix(inner, "rmdir /opt/stacks"):
		return nil, nil, 0, nil
	case strings.HasPrefix(inner, "rm -rf "):
		dir := unquote(strings.TrimPrefix(inner, "rm -rf "))
		for p := range s.files {
			if strings.HasPrefix(p, dir+"/") {
				delete(s.files, p)
			}
		}
		return nil, nil, 0, nil
	}
	return nil, []byte("unknown command\n"), 127, nil
}

func (s *fakeServer) upload(path string, mode os.FileMode, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploads[path] = fakeUpload{mode: mode, data: slices.Clone(data)}
	s.files[path] = slices.Clone(data)
}

// commands are the command lines the server ran, as root where sudo
// wrapped them.
func (s *fakeServer) commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.execs {
		out = append(out, e.cmd)
	}
	return out
}

func (s *fakeServer) ran(substr string) bool {
	for _, c := range s.commands() {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

// rootRan reports whether a command, as it ran past sudo and systemd-run,
// holds substr.
func (s *fakeServer) rootRan(substr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.roots {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

func (s *fakeServer) execList() []fakeExec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.execs)
}

func (s *fakeServer) uploaded(path string) (fakeUpload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.uploads[path]
	return u, ok
}

// fakeDialer reaches fake servers and counts the connections open at once.
type fakeDialer struct {
	mu      sync.Mutex
	servers map[string]*fakeServer
	dials   int
	open    int
	peak    int
}

func newFakeDialer(servers ...*fakeServer) *fakeDialer {
	d := &fakeDialer{servers: map[string]*fakeServer{}}
	for _, s := range servers {
		d.servers[s.addr()] = s
	}
	return d
}

func (d *fakeDialer) FetchHostKey(_ context.Context, addr, _ string) (sshexec.HostKey, error) {
	s := d.servers[addr]
	if s == nil {
		return sshexec.HostKey{}, errors.New("connection refused")
	}
	return s.hostKey, nil
}

func (d *fakeDialer) Dial(_ context.Context, addr string, a sshexec.Auth, key sshexec.HostKey, _ func(string)) (Target, error) {
	s := d.servers[addr]
	switch {
	case s == nil:
		return nil, errors.New("connection refused")
	case s.keyChanged || key.Key == nil || !bytes.Equal(key.Key.Marshal(), s.hostKey.Key.Marshal()):
		return nil, fmt.Errorf("dial: %w", sshexec.ErrHostKeyChanged)
	case s.authFail || a.User == "":
		return nil, fmt.Errorf("dial: %w", sshexec.ErrAuthFailed)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dials++
	d.open++
	d.peak = max(d.peak, d.open)
	return &fakeTarget{d: d, s: s}, nil
}

func (d *fakeDialer) counts() (dials, open, peak int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dials, d.open, d.peak
}

type fakeTarget struct {
	d      *fakeDialer
	s      *fakeServer
	closed bool
	// broken is a connection that dropped: nothing more runs over it.
	broken bool
}

func (t *fakeTarget) Run(ctx context.Context, cmd string, stdin []byte) ([]byte, []byte, int, error) {
	if t.closed {
		return nil, nil, -1, errors.New("closed")
	}
	if t.broken {
		return nil, nil, -1, errors.New("sshexec: open a session: EOF")
	}
	out, errOut, exit, err := t.s.run(ctx, cmd, stdin)
	if t.s.dropAfterInstall && strings.Contains(cmd, "systemd-run ") {
		t.broken = true
	}
	return out, errOut, exit, err
}

func (t *fakeTarget) Upload(_ context.Context, path string, mode os.FileMode, data []byte) error {
	if t.closed || t.broken {
		return errors.New("closed")
	}
	t.s.upload(path, mode, data)
	return nil
}

func (t *fakeTarget) Close() error {
	t.d.mu.Lock()
	defer t.d.mu.Unlock()
	if !t.closed {
		t.closed = true
		t.d.open--
	}
	return nil
}

// fastTimings are the engine's waits shortened for tests.
func fastTimings() timings {
	return timings{
		poll: 2 * time.Millisecond, installLimit: 2 * time.Second,
		verifyEvery: 2 * time.Millisecond, verifyLimit: 150 * time.Millisecond,
		checkWait: 2 * time.Second, answerWait: 10 * time.Second,
		keep: 10 * time.Minute, sweep: 2 * time.Millisecond,
		cleanup: 2 * time.Second, call: time.Second,
		tmpBase: "/tmp",
	}
}

func newTestEngine(env *fakeEnv, d Dialer) *Engine {
	e := New(env)
	e.dialer = d
	e.resolve = func(_ context.Context, host string) (net.IP, error) {
		if ip := net.ParseIP(host); ip != nil {
			return ip, nil
		}
		return nil, errors.New("no such host")
	}
	e.t = fastTimings()
	env.engine = e
	return e
}

// account is a password login whose sudo password is its own.
func account() Account {
	return Account{User: "ops", Method: "password", Password: []byte("sudo-pw"), SudoSame: true}
}

func request(servers ...*fakeServer) Request {
	r := Request{Account: account(), StartedBy: "admin"}
	for _, s := range servers {
		r.Servers = append(r.Servers, Server{Host: s.ip.String(), Port: 22})
	}
	return r
}

// waitFor polls cond until it holds, or fails the test after 5 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waited 5s for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func (f *fakeEnv) waitRow(t *testing.T, runID, key string, cond func(cluster.RunNodeRecord) bool, what string) cluster.RunNodeRecord {
	t.Helper()
	var rec cluster.RunNodeRecord
	waitFor(t, key+" "+what, func() bool {
		rec = f.row(runID, key)
		return cond(rec)
	})
	return rec
}

func (f *fakeEnv) waitEnded(t *testing.T, runID string) cluster.RunRecord {
	t.Helper()
	var h cluster.RunRecord
	waitFor(t, "the run to end", func() bool {
		h = f.head(runID)
		return h.Status != "" && !h.Active()
	})
	return h
}

// approveAll approves each row's host key as it comes up.
func approveAll(t *testing.T, e *Engine, env *fakeEnv, runID string, keys ...string) {
	t.Helper()
	for _, k := range keys {
		rec := env.waitRow(t, runID, k, func(r cluster.RunNodeRecord) bool { return r.Awaiting == awaitHostKey }, "to wait for its host key")
		if err := e.ApproveHostKey(runID, k, rec.HostKey, true); err != nil {
			t.Fatalf("approve %s: %v", k, err)
		}
	}
}

// refusalCode is the code of a *Refusal, or "".
func refusalCode(err error) (int, string) {
	var r *Refusal
	if errors.As(err, &r) {
		return r.Status, r.Code
	}
	return 0, ""
}

func isZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}
