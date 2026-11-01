package sshexec

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func dialTest(t *testing.T, s *testServer, a Auth, onNotice func(string)) *Client {
	t.Helper()
	c, err := Dial(testCtx(t), s.addr, a, s.hostKey(), onNotice)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// The first connection only learns the host key: it is closed inside the
// key check, so the server sees no authentication attempt at all, let alone
// a command.
func TestNothingRunsBeforeTheHostKeyIsApproved(t *testing.T) {
	s := startTestServer(t, serverOpts{password: "pw", allowNone: true})

	hk, err := FetchHostKey(testCtx(t), s.addr, Auth{User: "ops", Method: "password", Password: []byte("pw")}, 10*time.Second)
	if err != nil {
		t.Fatalf("FetchHostKey: %v", err)
	}
	want := s.signer.PublicKey()
	if hk.Fingerprint != ssh.FingerprintSHA256(want) || !strings.HasPrefix(hk.Fingerprint, "SHA256:") {
		t.Fatalf("fingerprint = %q, want %q", hk.Fingerprint, ssh.FingerprintSHA256(want))
	}
	if hk.Type != ssh.KeyAlgoED25519 {
		t.Fatalf("type = %q, want %q", hk.Type, ssh.KeyAlgoED25519)
	}
	if hk.Key == nil || !bytes.Equal(hk.Key.Marshal(), want.Marshal()) {
		t.Fatal("key is not the server's host key")
	}

	// The server's auth log runs on its own goroutine; give it a moment to
	// record anything that did reach it.
	time.Sleep(100 * time.Millisecond)
	if got := s.recordedAttempts(); len(got) != 0 {
		t.Fatalf("authentication attempts before approval = %v, want none", got)
	}
	if got := s.recordedExecs(); len(got) != 0 {
		t.Fatalf("execs before approval = %v, want none", got)
	}
}

func TestAChangedHostKeyIsRefused(t *testing.T) {
	s := startTestServer(t, serverOpts{password: "pw", allowNone: true})
	approved := newHostSigner(t).PublicKey()
	hk := HostKey{Type: approved.Type(), Fingerprint: ssh.FingerprintSHA256(approved), Key: approved}

	c, err := Dial(testCtx(t), s.addr, Auth{User: "ops", Method: "none"}, hk, nil)
	if c != nil {
		_ = c.Close()
	}
	if !errors.Is(err, ErrHostKeyChanged) {
		t.Fatalf("Dial with another key: err = %v, want ErrHostKeyChanged", err)
	}
	// The error carries the key the server presented, not the approved one.
	var changed *HostKeyChangedError
	if !errors.As(err, &changed) {
		t.Fatalf("Dial with another key: err = %v, want a *HostKeyChangedError", err)
	}
	presented := s.signer.PublicKey()
	if p := changed.Presented; p.Key == nil || !bytes.Equal(p.Key.Marshal(), presented.Marshal()) ||
		p.Fingerprint != ssh.FingerprintSHA256(presented) || p.Type != presented.Type() {
		t.Fatalf("the error's presented key = %+v, want the server's %s", changed.Presented, ssh.FingerprintSHA256(presented))
	}
	time.Sleep(100 * time.Millisecond)
	if got := s.recordedAttempts(); len(got) != 0 {
		t.Fatalf("authentication attempts on a changed key = %v, want none", got)
	}
	if got := s.recordedExecs(); len(got) != 0 {
		t.Fatalf("execs on a changed key = %v, want none", got)
	}
}

func encryptedKey(t *testing.T, passphrase string) (pemBytes []byte, pub ssh.PublicKey) {
	t.Helper()
	pubRaw, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	if err != nil {
		t.Fatal(err)
	}
	pub, err = ssh.NewPublicKey(pubRaw)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(block), pub
}

func TestEachAuthMethodLogsIn(t *testing.T) {
	keyPEM, keyPub := encryptedKey(t, "open sesame")

	cases := []struct {
		name string
		opts serverOpts
		auth Auth
		// method is the one the server must have accepted last.
		method string
	}{
		{"password", serverOpts{password: "s3cret"}, Auth{User: "ops", Method: "password", Password: []byte("s3cret")}, "password"},
		{"password answered through keyboard-interactive", serverOpts{password: "s3cret", kbdPassword: true}, Auth{User: "ops", Method: "password", Password: []byte("s3cret")}, "keyboard-interactive"},
		{"key with a passphrase", serverOpts{authorizedKey: keyPub}, Auth{User: "ops", Method: "key", Key: keyPEM, Passphrase: []byte("open sesame")}, "publickey"},
		{"none (Tailscale SSH)", serverOpts{allowNone: true}, Auth{User: "ops", Method: "none"}, "none"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := startTestServer(t, tc.opts)
			c := dialTest(t, s, tc.auth, nil)
			if _, _, exit, err := c.Run(testCtx(t), "true", nil); err != nil || exit != 0 {
				t.Fatalf("Run after login: exit %d, err %v", exit, err)
			}
			attempts := s.recordedAttempts()
			if len(attempts) == 0 || attempts[len(attempts)-1] != tc.method {
				t.Fatalf("auth attempts = %v, want the last to be %q", attempts, tc.method)
			}
		})
	}

	refused := []struct {
		name string
		opts serverOpts
		auth Auth
	}{
		{"wrong password", serverOpts{password: "s3cret"}, Auth{User: "ops", Method: "password", Password: []byte("guess")}},
		{"wrong password through keyboard-interactive", serverOpts{password: "s3cret", kbdPassword: true}, Auth{User: "ops", Method: "password", Password: []byte("guess")}},
		{"wrong passphrase", serverOpts{authorizedKey: keyPub}, Auth{User: "ops", Method: "key", Key: keyPEM, Passphrase: []byte("guess")}},
		{"none where the server wants a password", serverOpts{password: "s3cret"}, Auth{User: "ops", Method: "none"}},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			s := startTestServer(t, tc.opts)
			c, err := Dial(testCtx(t), s.addr, tc.auth, s.hostKey(), nil)
			if c != nil {
				_ = c.Close()
			}
			if !errors.Is(err, ErrAuthFailed) {
				t.Fatalf("err = %v, want ErrAuthFailed", err)
			}
			if got := s.recordedExecs(); len(got) != 0 {
				t.Fatalf("execs after a refused login = %v", got)
			}
		})
	}
}

// Tailscale SSH in check mode holds the login and sends an https URL for the
// operator to open; only https URLs are passed on, whether they come in a
// banner or a keyboard-interactive instruction.
func TestTheCheckNoticeIsReported(t *testing.T) {
	notice := "# Tailscale SSH requires an additional check.\n" +
		"# To authenticate, visit: https://login.example/a/xyz\n" +
		"# not these: http://login.example/plain javascript:alert(1) ftp://login.example/f\n"

	cases := []struct {
		name string
		opts serverOpts
	}{
		{"banner", serverOpts{allowNone: true, banner: notice}},
		{"keyboard-interactive instruction", serverOpts{kbdInstruction: notice}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := startTestServer(t, tc.opts)
			var mu sync.Mutex
			var got []string
			dialTest(t, s, Auth{User: "ops", Method: "none"}, func(url string) {
				mu.Lock()
				got = append(got, url)
				mu.Unlock()
			})
			mu.Lock()
			defer mu.Unlock()
			if len(got) != 1 || got[0] != "https://login.example/a/xyz" {
				t.Fatalf("notices = %q, want exactly [https://login.example/a/xyz]", got)
			}
		})
	}
}

// The sudo password and the join token reach the target only on stdin: the
// command line the server records (what /proc/*/cmdline and the sudo log
// would show) holds neither.
func TestNothingSecretInArgv(t *testing.T) {
	const sudoPW = "sudo-pw-6d1f9a"
	const token = "join-token-0c4e72b8"
	s := startTestServer(t, serverOpts{password: "login-pw"})
	c := dialTest(t, s, Auth{User: "ops", Method: "password", Password: []byte("login-pw")}, nil)
	ctx := testCtx(t)

	writeToken := "umask 077 && cat > /tmp/sfpanel-install.X/token"
	cmd, head := SudoCommand(writeToken, false, []byte(sudoPW))
	// -k: sudo asks for the password whatever it has cached, so it reads the
	// first line every time rather than leave it on the command's stdin.
	if want := "env LC_ALL=C sudo -k -S -p '' bash -c 'umask 077 && cat > /tmp/sfpanel-install.X/token'"; cmd != want {
		t.Fatalf("sudo command = %q, want %q", cmd, want)
	}
	if _, _, exit, err := c.Run(ctx, cmd, append(head, token...)); err != nil || exit != 0 {
		t.Fatalf("Run: exit %d, err %v", exit, err)
	}
	if err := c.Upload(ctx, "/tmp/sfpanel-install.X/token2", 0o600, []byte(token)); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	execs := s.recordedExecs()
	if len(execs) != 2 {
		t.Fatalf("execs = %d, want 2", len(execs))
	}
	for _, e := range execs {
		if strings.Contains(e.Cmd, sudoPW) || strings.Contains(e.Cmd, token) {
			t.Fatalf("a secret reached the command line: %q", e.Cmd)
		}
	}
	if want := sudoPW + "\n" + token; string(execs[0].Stdin) != want {
		t.Fatalf("sudo stdin = %q, want %q", execs[0].Stdin, want)
	}
	if want := "umask 077 && cat > '/tmp/sfpanel-install.X/token2' && chmod 0600 '/tmp/sfpanel-install.X/token2'"; execs[1].Cmd != want {
		t.Fatalf("upload command = %q, want %q", execs[1].Cmd, want)
	}
	if string(execs[1].Stdin) != token {
		t.Fatalf("upload stdin = %q, want the file's bytes", execs[1].Stdin)
	}
}

func TestSudoCommandForms(t *testing.T) {
	cmd, head := SudoCommand("id -u", true, []byte("pw"))
	if cmd != "id -u" || head != nil {
		t.Fatalf("root: %q, %q; want the command as is and no stdin", cmd, head)
	}
	cmd, head = SudoCommand("echo 'hi'", false, nil)
	if want := `env LC_ALL=C sudo -n bash -c 'echo '\''hi'\'''`; cmd != want || head != nil {
		t.Fatalf("no sudo password: %q, %q; want %q and no stdin", cmd, head, want)
	}
	pw := []byte("pw")
	_, head = SudoCommand("true", false, pw)
	head[0] = 'X'
	if string(pw) != "pw" {
		t.Fatal("the stdin head shares memory with the caller's password")
	}
}

// ShellQuote's output, read back by a real shell, is the original string.
func TestShellQuoteRoundTrips(t *testing.T) {
	for _, s := range []string{"", "plain", "it's", "a b\tc", `$(id) ; "x" \ '' $HOME`, "new\nline"} {
		out, err := exec.Command("sh", "-c", "printf '%s' "+ShellQuote(s)).Output()
		if err != nil {
			t.Fatalf("sh: %v", err)
		}
		if string(out) != s {
			t.Fatalf("round trip of %q = %q", s, out)
		}
	}
}

func TestRunReportsOutputAndExit(t *testing.T) {
	s := startTestServer(t, serverOpts{password: "pw", handle: func(cmd string, stdin []byte) ([]byte, []byte, uint32) {
		return append([]byte("out:"), stdin...), []byte("err:" + cmd), 3
	}})
	c := dialTest(t, s, Auth{User: "ops", Method: "password", Password: []byte("pw")}, nil)

	stdout, stderr, exit, err := c.Run(testCtx(t), "do-it", []byte("in"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(stdout) != "out:in" || string(stderr) != "err:do-it" || exit != 3 {
		t.Fatalf("Run = %q, %q, %d", stdout, stderr, exit)
	}

	s2 := startTestServer(t, serverOpts{password: "pw", handle: func(string, []byte) ([]byte, []byte, uint32) {
		return nil, []byte("cat: /x: Permission denied"), 1
	}})
	c2 := dialTest(t, s2, Auth{User: "ops", Method: "password", Password: []byte("pw")}, nil)
	if err := c2.Upload(testCtx(t), "/x", 0o644, []byte("data")); err == nil {
		t.Fatal("Upload with a failing command returned no error")
	}
}

func TestWipeZeroesTheSecrets(t *testing.T) {
	pw, key, pass := []byte("password"), []byte("-----BEGIN KEY-----"), []byte("passphrase")
	a := Auth{User: "ops", Method: "key", Password: pw, Key: key, Passphrase: pass}
	a.Wipe()
	for name, b := range map[string][]byte{"password": pw, "key": key, "passphrase": pass} {
		for i, c := range b {
			if c != 0 {
				t.Fatalf("%s byte %d = %d after Wipe, want 0", name, i, c)
			}
		}
	}
}

// A server that completes the login and then never answers a channel open:
// Run gives up when its ctx ends, with ctx's error, and closes the
// connection rather than wait for TCP to give up, leaving no goroutine of
// its own behind.
func TestRunHeedsCtxWhileOpeningASession(t *testing.T) {
	s := startTestServer(t, serverOpts{password: "pw", silentChannels: true})
	before := runtime.NumGoroutine()
	c, err := Dial(testCtx(t), s.addr, Auth{User: "ops", Method: "password", Password: []byte("pw")}, s.hostKey(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	type result struct {
		exit int
		err  error
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		_, _, exit, err := c.Run(ctx, "true", nil)
		done <- result{exit, err}
	}()
	select {
	case r := <-done:
		if !errors.Is(r.err, context.DeadlineExceeded) || r.exit != -1 {
			t.Fatalf("Run = exit %d, %v; want -1 and ctx's error", r.exit, r.err)
		}
		if d := time.Since(start); d > time.Second {
			t.Fatalf("Run returned after %v, want about 200ms", d)
		}
	case <-time.After(3 * time.Second):
		_ = c.Close()
		t.Fatalf("Run with a ctx of 200ms has not returned after %v", time.Since(start))
	}
	// The connection is closed: another command fails at once.
	if _, _, _, err := c.Run(testCtx(t), "true", nil); err == nil {
		t.Fatal("a command ran over the connection Run gave up on")
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before {
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines, %d before the Dial: some were left behind", runtime.NumGoroutine(), before)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A command that writes without end cannot grow the caller's memory without
// end: Run keeps the first maxOutput bytes of each stream and reads the rest
// to the command's end, so the command never blocks on a full channel.
func TestRunKeepsABoundedOutput(t *testing.T) {
	big, bigErr := bytes.Repeat([]byte("o"), 64<<20), bytes.Repeat([]byte("e"), 8<<20)
	s := startTestServer(t, serverOpts{password: "pw", handle: func(string, []byte) ([]byte, []byte, uint32) {
		return big, bigErr, 3
	}})
	c := dialTest(t, s, Auth{User: "ops", Method: "password", Password: []byte("pw")}, nil)
	stdout, stderr, exit, err := c.Run(testCtx(t), "cat /dev/zero", nil)
	if err != nil || exit != 3 {
		t.Fatalf("Run = exit %d, %v; want the command's end, exit 3", exit, err)
	}
	if len(stdout) != maxOutput || len(stderr) != maxOutput {
		t.Fatalf("Run kept %d bytes of stdout and %d of stderr, want %d of each", len(stdout), len(stderr), maxOutput)
	}
}
