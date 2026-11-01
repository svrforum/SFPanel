package agentinstall

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/svrforum/SFPanel/internal/cluster"
)

// sshTarget is an SSH server on 127.0.0.1 standing in for a target: it
// logs in one password, records every exec's command line and stdin, and
// runs each command under bash in a directory of the test's, with sudo,
// systemd-run and systemctl stubbed on its PATH, so nothing of this host's
// services is touched. The login check and the probe, which read the host,
// are answered from the facts given instead.
type sshTarget struct {
	addr   string
	root   string
	signer ssh.Signer
	facts  Facts

	mu    sync.Mutex
	execs []fakeExec
}

func startSSHTarget(t *testing.T, password string, facts Facts) *sshTarget {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	st := &sshTarget{root: t.TempDir(), signer: signer, facts: facts}
	for _, d := range []string{"bin", "tmp", "record"} {
		if err := os.Mkdir(filepath.Join(st.root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	stubs := map[string]string{
		// sudo takes its options off, reads the password line when -S asks
		// for one, keeps it for the test to compare, and runs the rest.
		"sudo": `#!/bin/bash
pw=0
while [ $# -gt 0 ]; do
  case "$1" in
    -k|-n) shift ;;
    -S) pw=1; shift ;;
    -p) shift 2 ;;
    *) break ;;
  esac
done
if [ "$pw" = 1 ]; then IFS= read -r line; printf '%s' "$line" > "$SFPANEL_TEST_RECORD/sudo_password"; fi
exec "$@"
`,
		// systemd-run runs the unit's command in the foreground.
		"systemd-run": `#!/bin/bash
while [ $# -gt 0 ] && [ "${1#--}" != "$1" ]; do echo "$1" >> "$SFPANEL_TEST_RECORD/systemd_run"; shift; done
exec "$@"
`,
		"systemctl": "#!/bin/bash\nexit 0\n",
	}
	for name, body := range stubs {
		if err := os.WriteFile(filepath.Join(st.root, "bin", name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &ssh.ServerConfig{
		PasswordCallback: func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) == password {
				return nil, nil
			}
			return nil, errors.New("refused")
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	st.addr = ln.Addr().String()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go st.serve(conn, cfg)
		}
	}()
	return st
}

func (st *sshTarget) serve(conn net.Conn, cfg *ssh.ServerConfig) {
	defer conn.Close()
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go st.session(ch, chReqs)
	}
}

func (st *sshTarget) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" || len(req.Payload) < 4 {
			_ = req.Reply(false, nil)
			continue
		}
		n := binary.BigEndian.Uint32(req.Payload)
		cmd := string(req.Payload[4 : 4+n])
		_ = req.Reply(true, nil)
		stdin, _ := io.ReadAll(ch)
		st.mu.Lock()
		st.execs = append(st.execs, fakeExec{cmd: cmd, stdin: stdin})
		st.mu.Unlock()

		stdout, stderr, exit := st.exec(cmd, stdin)
		_, _ = ch.Write(stdout)
		_, _ = ch.Stderr().Write(stderr)
		status := make([]byte, 4)
		binary.BigEndian.PutUint32(status, uint32(exit))
		_, _ = ch.SendRequest("exit-status", false, status)
		return
	}
}

func (st *sshTarget) exec(cmd string, stdin []byte) ([]byte, []byte, int) {
	switch {
	case cmd == "sh -s" && string(stdin) == LoginCheckScript():
		return []byte(loginText(st.facts.Login)), nil, 0
	case strings.HasSuffix(cmd, "bash -c 'bash -s'"):
		return []byte(probeText(st.facts)), nil, 0
	}
	c := exec.Command("bash", "-c", cmd)
	c.Dir = st.root
	c.Env = []string{"PATH=" + filepath.Join(st.root, "bin") + ":/usr/bin:/bin", "HOME=" + st.root,
		"LANG=C", "SFPANEL_TEST_RECORD=" + filepath.Join(st.root, "record")}
	c.Stdin = bytes.NewReader(stdin)
	var out, errOut bytes.Buffer
	c.Stdout, c.Stderr = &out, &errOut
	err := c.Run()
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr):
		return out.Bytes(), errOut.Bytes(), exitErr.ExitCode()
	case err != nil:
		return out.Bytes(), []byte(err.Error()), 127
	}
	return out.Bytes(), errOut.Bytes(), 0
}

func (st *sshTarget) record(name string) string {
	b, _ := os.ReadFile(filepath.Join(st.root, "record", name))
	return string(b)
}

// fakeInstallScript stands in for install.sh: it records its arguments and
// the token file it was given, with the file's mode, and installs nothing.
const fakeInstallScript = `#!/bin/bash
rec="$SFPANEL_TEST_RECORD"
printf '%s\n' "$@" > "$rec/args"
tf=""
while [ $# -gt 0 ]; do
  case "$1" in
    --token-file) tf="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [ -f "$tf" ]; then cat "$tf" > "$rec/token"; stat -c %a "$tf" > "$rec/token_mode"; fi
echo "[INFO] installing the agent"
exit 0
`

func TestEndToEndOverSSH(t *testing.T) {
	const password = "e2e-login-pw-71c3"
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	env := newFakeEnv()
	env.script = []byte(fakeInstallScript)
	facts := passingFacts()
	facts.Login = Login{UID: 1000, Sudo: SudoPassword, Bash: true}
	facts.Hostname = "web-e2e"
	facts.Addrs = []net.IP{net.ParseIP("127.0.0.1")}
	facts.Reach = []bool{true}
	// /opt/stacks was there: no clean-up of this host's would touch it.
	facts.OptStacks = true
	facts.Now = env.now
	st := startSSHTarget(t, password, facts)
	host, port, _ := net.SplitHostPort(st.addr)
	portN, _ := strconv.Atoi(port)

	// install.sh's join registers the agent, as the fake records it ran.
	env.beforeNodes = func() {
		if st.record("args") != "" {
			env.mu.Lock()
			_, done := env.nodes["a-web-e2e"]
			env.mu.Unlock()
			if !done {
				env.register(net.ParseIP("127.0.0.1"), "web-e2e", nil)
			}
		}
	}

	e := newTestEngine(env, sshDialer{})
	e.t.tmpBase = filepath.Join(st.root, "tmp")
	req := Request{Servers: []Server{{Host: host, Port: portN, Group: "web"}},
		Account: Account{User: "ops", Method: "password", Password: []byte(password), SudoSame: true}, StartedBy: "admin"}
	pw := req.Account.Password
	id, err := e.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	rec := env.waitRow(t, id, "s1", func(r cluster.RunNodeRecord) bool { return r.Awaiting == awaitHostKey }, "to wait for its host key")
	if want := "ED25519 " + ssh.FingerprintSHA256(st.signer.PublicKey()); rec.HostKey != want {
		t.Fatalf("host key = %q, want %q", rec.HostKey, want)
	}
	if err := e.ApproveHostKey(id, "s1", rec.HostKey, true); err != nil {
		t.Fatal(err)
	}
	h := env.waitEnded(t, id)
	row := env.row(id, "s1")
	if h.Status != cluster.RunSucceeded || row.Status != cluster.RunNodeSucceeded || row.NodeID != "a-web-e2e" {
		t.Fatalf("run %s, row %+v; want both succeeded as a-web-e2e", h.Status, row)
	}

	minted := env.mintedFor()
	if len(minted) != 1 || minted[0].ip != "127.0.0.1" {
		t.Fatalf("tokens = %+v, want one bound to 127.0.0.1", minted)
	}
	token := minted[0].token
	if st.record("token") != token || strings.TrimSpace(st.record("token_mode")) != "600" {
		t.Fatalf("install.sh's token file held %q at mode %q, want the token at 600", st.record("token"), st.record("token_mode"))
	}
	if got := st.record("sudo_password"); got != password {
		t.Fatalf("sudo read %q from its stdin, want the password", got)
	}
	args := strings.Split(strings.TrimSpace(st.record("args")), "\n")
	if len(args) < 4 {
		t.Fatalf("install.sh's arguments = %q", args)
	}
	dir := filepath.Dir(args[3])
	want := []string{"--agent", "192.0.2.1:3629", "--token-file", dir + "/token", "--ca", "sha256:" + strings.Repeat("ab", 32),
		"--advertise", "127.0.0.1", "--name", "web-e2e", "--version", "0.81.0", "--checksums", dir + "/checksums.txt"}
	if strings.Join(args, "\n") != strings.Join(want, "\n") {
		t.Fatalf("install.sh's arguments =\n%q\nwant\n%q", args, want)
	}
	if !strings.Contains(st.record("systemd_run"), "--unit=sfpanel-agent-install-"+id+"-s1") {
		t.Fatalf("systemd-run got %q", st.record("systemd_run"))
	}
	if left, _ := os.ReadDir(filepath.Join(st.root, "tmp")); len(left) != 0 {
		t.Fatalf("the row's directory was left: %v", left)
	}
	if got, want := env.labels["a-web-e2e"], map[string]string{"group": "web"}; got["group"] != want["group"] {
		t.Fatalf("labels = %v", got)
	}

	var outputs strings.Builder
	for p := 0; p < phases; p++ {
		v, err := e.StepOutput(id, "s1", p, 0)
		if err != nil {
			t.Fatal(err)
		}
		outputs.WriteString(v.Output)
	}
	if !strings.Contains(outputs.String(), "[INFO] installing the agent") {
		t.Fatalf("the phase output lacks install.sh's: %q", outputs.String())
	}
	st.mu.Lock()
	execs := append([]fakeExec(nil), st.execs...)
	st.mu.Unlock()
	for _, x := range execs {
		if strings.Contains(x.cmd, password) || strings.Contains(x.cmd, token) {
			t.Fatalf("a secret reached a command line: %q", x.cmd)
		}
	}
	for what, text := range map[string]string{"the phase output": outputs.String(), "the log": logs.String(), "the run record": env.allWrites()} {
		if strings.Contains(text, password) || strings.Contains(text, token) {
			t.Fatalf("a secret reached %s", what)
		}
	}
	if !isZero(pw) {
		t.Fatal("the password outlived the install")
	}
}
