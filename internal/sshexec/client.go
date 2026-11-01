// Package sshexec is the thin SSH client the agent installer uses to reach a
// target server: it learns the server's host key without running anything,
// logs in only against the key the operator approved, runs commands with
// their secrets on stdin, and writes files through the shell.
//
// Secrets (passwords, private keys, passphrases, tokens) never go into a
// command line: whatever the target's /proc/*/cmdline or sudo log would show
// is built from the caller's command alone, and every secret byte travels on
// the session's stdin.
package sshexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

// connectTimeout bounds the TCP connect only. Authentication may wait much
// longer: Tailscale SSH in check mode holds the login until the operator
// approves it in a browser, so Dial lets that wait run as long as its ctx.
const connectTimeout = 15 * time.Second

var (
	// ErrAuthFailed is a login the server refused, or a private key that
	// cannot be read with the passphrase given.
	ErrAuthFailed = errors.New("ssh login refused")
	// ErrHostKeyChanged is a server that presented a key other than the one
	// the operator approved. Nothing is sent to it, not even a login.
	ErrHostKeyChanged = errors.New("ssh host key differs from the approved one")

	// errKeyCaptured ends FetchHostKey's handshake inside the host-key check.
	errKeyCaptured = errors.New("host key captured")
)

// Auth is how to log in. Method is "password", "key" or "none" (Tailscale
// SSH). The secrets are byte slices so Wipe can zero them in place.
type Auth struct {
	User   string
	Method string
	// Password is the login password for "password".
	Password []byte
	// Key is a PEM private key for "key", with Passphrase when it is
	// encrypted.
	Key        []byte
	Passphrase []byte
}

// Wipe zeroes the secrets in place, so every copy of the Auth that shares
// these slices loses them too.
func (a *Auth) Wipe() {
	clear(a.Password)
	clear(a.Key)
	clear(a.Passphrase)
}

// HostKey is a server's host key as the operator sees it: its type
// ("ssh-ed25519"), its fingerprint ("SHA256:…") and the key itself, which
// Dial compares byte for byte.
type HostKey struct {
	Type, Fingerprint string
	Key               ssh.PublicKey
}

func hostKeyOf(key ssh.PublicKey) HostKey {
	return HostKey{Type: key.Type(), Fingerprint: ssh.FingerprintSHA256(key), Key: key}
}

// Client is a logged-in SSH connection.
type Client struct {
	conn *ssh.Client
}

// Close ends the connection.
func (c *Client) Close() error {
	return c.conn.Close()
}

// FetchHostKey connects to addr only to learn its host key. The connection is
// ended inside the host-key check, before any login is attempted, so nothing
// can run on the server until the operator approves the key and Dial is
// called with it. Of a only the user name is sent; timeout bounds the whole
// exchange (15 s when zero).
func FetchHostKey(ctx context.Context, addr string, a Auth, timeout time.Duration) (HostKey, error) {
	if timeout <= 0 {
		timeout = connectTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := dialTCP(ctx, addr)
	if err != nil {
		return HostKey{}, err
	}
	var captured atomic.Pointer[HostKey]
	cfg := &ssh.ClientConfig{
		User: a.User,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			hk := hostKeyOf(key)
			captured.Store(&hk)
			return errKeyCaptured
		},
	}
	client, err := handshake(ctx, conn, addr, cfg)
	if client != nil {
		// Unreachable: the callback refuses every key. Close rather than
		// leave a logged-in connection behind if that ever changes.
		_ = client.Close()
	}
	if hk := captured.Load(); hk != nil {
		return *hk, nil
	}
	return HostKey{}, fmt.Errorf("sshexec: read the host key of %s: %w", addr, err)
}

// Dial logs in to addr, accepting only the approved host key; any other key
// is ErrHostKeyChanged before a login is attempted. A refused login is
// ErrAuthFailed.
//
// Every https:// URL the server sends in a banner or a keyboard-interactive
// prompt is passed to onNotice (once each): Tailscale SSH in check mode sends
// the approval URL that way and holds the login until the operator opens it,
// so the wait for authentication is bounded by ctx alone.
//
// "password" also answers keyboard-interactive prompts with the password,
// since many servers offer only that. "none" sends no credential: Tailscale
// SSH lets the implicit "none" attempt in; it also answers a
// keyboard-interactive round that asks nothing (a notice), and refuses one
// that asks a question.
func Dial(ctx context.Context, addr string, a Auth, key HostKey, onNotice func(url string)) (*Client, error) {
	if key.Key == nil {
		return nil, fmt.Errorf("sshexec: %s: no approved host key: %w", addr, ErrHostKeyChanged)
	}
	n := &notices{report: onNotice, seen: map[string]bool{}}
	methods, err := authMethods(a, n)
	if err != nil {
		return nil, err
	}

	approved := key.Key.Marshal()
	var mismatch atomic.Bool
	cfg := &ssh.ClientConfig{
		User: a.User,
		Auth: methods,
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			if !bytes.Equal(k.Marshal(), approved) {
				mismatch.Store(true)
				return ErrHostKeyChanged
			}
			return nil
		},
		// Ask only for the approved key's type, so a server with several
		// host keys presents the one the operator saw.
		HostKeyAlgorithms: algorithmsFor(key.Key.Type()),
		BannerCallback: func(message string) error {
			n.scan(message)
			return nil
		},
	}

	conn, err := dialTCP(ctx, addr)
	if err != nil {
		return nil, err
	}
	client, err := handshake(ctx, conn, addr, cfg)
	if err == nil {
		return &Client{conn: client}, nil
	}
	var negotiation *ssh.AlgorithmNegotiationError
	switch {
	case mismatch.Load(), errors.As(err, &negotiation) && negotiation.What == "host key":
		return nil, fmt.Errorf("sshexec: %s: %w", addr, ErrHostKeyChanged)
	case ctx.Err() != nil:
		return nil, err
	case isAuthRefusal(err):
		return nil, fmt.Errorf("sshexec: %s: %w", addr, ErrAuthFailed)
	}
	return nil, fmt.Errorf("sshexec: %s: %w", addr, err)
}

// algorithmsFor lists the host key algorithms that present a key of this
// type; an RSA key signs under three names.
func algorithmsFor(keyType string) []string {
	if keyType == ssh.KeyAlgoRSA {
		return []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}
	}
	return []string{keyType}
}

// isAuthRefusal reports whether a handshake error is the server turning the
// login down, as opposed to the network or the protocol failing. x/crypto
// has no typed error for it, only these messages.
func isAuthRefusal(err error) bool {
	if errors.Is(err, ErrAuthFailed) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unable to authenticate") ||
		strings.Contains(msg, "too many authentication failures")
}

func authMethods(a Auth, n *notices) ([]ssh.AuthMethod, error) {
	switch a.Method {
	case "password":
		pw := a.Password
		return []ssh.AuthMethod{
			ssh.PasswordCallback(func() (string, error) { return string(pw), nil }),
			ssh.KeyboardInteractive(func(_, instruction string, questions []string, _ []bool) ([]string, error) {
				n.scan(instruction)
				answers := make([]string, len(questions))
				for i, q := range questions {
					n.scan(q)
					answers[i] = string(pw)
				}
				return answers, nil
			}),
		}, nil
	case "key":
		var signer ssh.Signer
		var err error
		if len(a.Passphrase) > 0 {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(a.Key, a.Passphrase)
		} else {
			signer, err = ssh.ParsePrivateKey(a.Key)
		}
		if err != nil {
			// The parser's message names the failure (no passphrase, wrong
			// passphrase, unknown format), never the key's bytes.
			return nil, fmt.Errorf("%w: the private key cannot be read: %v", ErrAuthFailed, err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil
	case "none":
		return []ssh.AuthMethod{
			ssh.KeyboardInteractive(func(_, instruction string, questions []string, _ []bool) ([]string, error) {
				n.scan(instruction)
				for _, q := range questions {
					n.scan(q)
				}
				if len(questions) > 0 {
					return nil, ErrAuthFailed
				}
				return nil, nil
			}),
		}, nil
	}
	return nil, fmt.Errorf("sshexec: unknown auth method %q", a.Method)
}

// httpsURL finds the https URLs in server text. It is the scheme guard: the
// page links what is reported, so http, javascript and the rest never match.
var httpsURL = regexp.MustCompile(`https://[^\s"'<>]+`)

// notices passes each https URL the server sends to report, once.
type notices struct {
	report func(string)
	mu     sync.Mutex
	seen   map[string]bool
}

func (n *notices) scan(text string) {
	if n.report == nil {
		return
	}
	for _, m := range httpsURL.FindAllString(text, -1) {
		m = strings.TrimRight(m, ".,;:!?)]}")
		u, err := url.Parse(m)
		if err != nil || u.Host == "" {
			continue
		}
		n.mu.Lock()
		dup := n.seen[m]
		n.seen[m] = true
		n.mu.Unlock()
		if !dup {
			n.report(m)
		}
	}
}

func dialTCP(ctx context.Context, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: connectTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("sshexec: connect %s: %w", addr, err)
	}
	return conn, nil
}

// handshake runs the SSH handshake on conn for as long as ctx allows: ending
// ctx closes the connection, which ends the handshake wherever it waits.
func handshake(ctx context.Context, conn net.Conn, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error) {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if !stop() {
		if err == nil {
			_ = sc.Close()
		}
		return nil, fmt.Errorf("sshexec: %s: %w", addr, ctx.Err())
	}
	if err != nil {
		return nil, err
	}
	return ssh.NewClient(sc, chans, reqs), nil
}

// Run runs cmd on the server with stdin as its standard input (none when
// nil) and returns its output and exit status. A command that ran and exited
// non-zero is not an error; err is for a command that could not be run or
// whose end was not seen (exit is then -1). Ending ctx kills the command.
func (c *Client) Run(ctx context.Context, cmd string, stdin []byte) (stdout, stderr []byte, exit int, err error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, nil, -1, fmt.Errorf("sshexec: open a session: %w", err)
	}
	defer sess.Close()

	var out, errOut bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &errOut
	if stdin != nil {
		sess.Stdin = bytes.NewReader(stdin)
	}
	if err := sess.Start(cmd); err != nil {
		return nil, nil, -1, fmt.Errorf("sshexec: start a command: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Wait() }()

	select {
	case err = <-done:
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		// The output buffers may still be written to until Wait returns,
		// so none of it is returned.
		return nil, nil, -1, fmt.Errorf("sshexec: %w", ctx.Err())
	}
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		return out.Bytes(), errOut.Bytes(), exitErr.ExitStatus(), nil
	}
	if err != nil {
		return out.Bytes(), errOut.Bytes(), -1, fmt.Errorf("sshexec: run a command: %w", err)
	}
	return out.Bytes(), errOut.Bytes(), 0, nil
}

// Upload writes data to path on the server through the shell and sets its
// mode. The file is created under umask 077, so it is never readable by
// others before the chmod, and its bytes travel on stdin, never in the
// command line. It writes as the login user; there is no SFTP.
func (c *Client) Upload(ctx context.Context, path string, mode os.FileMode, data []byte) error {
	q := ShellQuote(path)
	cmd := fmt.Sprintf("umask 077 && cat > %s && chmod %04o %s", q, mode.Perm(), q)
	if data == nil {
		data = []byte{}
	}
	_, stderr, exit, err := c.Run(ctx, cmd, data)
	if err != nil {
		return fmt.Errorf("sshexec: upload %s: %w", path, err)
	}
	if exit != 0 {
		return fmt.Errorf("sshexec: upload %s: exit %d: %s", path, exit, strings.TrimSpace(string(stderr)))
	}
	return nil
}

// SudoCommand wraps cmd to run as root and returns the command line and the
// bytes that must lead its stdin. Logged in as root, cmd is returned as is.
// Otherwise it runs under bash through sudo: with a sudo password, through
// sudo -S with an empty prompt and the password as stdin's first line;
// without one, through sudo -n, which fails rather than waits for a password.
// Either line starts with "env LC_ALL=C ", so sudo answers in English on a
// server set to another language: its refusal of a password is read from
// its stderr. cmd itself runs in that locale too.
//
// The head is a new slice holding the password; the caller appends the
// command's own stdin after it and wipes both when done. With -S, sudo reads
// the first line only when it asks for a password, so -k has it ask whatever
// it has cached; on an account with NOPASSWD sudo it never asks and the line
// would reach cmd's stdin instead, so check "sudo -n true" first and pass no
// password when it succeeds.
func SudoCommand(cmd string, root bool, sudoPassword []byte) (string, []byte) {
	if root {
		return cmd, nil
	}
	if len(sudoPassword) == 0 {
		return "env LC_ALL=C sudo -n bash -c " + ShellQuote(cmd), nil
	}
	head := make([]byte, 0, len(sudoPassword)+1)
	head = append(head, sudoPassword...)
	head = append(head, '\n')
	return "env LC_ALL=C sudo -k -S -p '' bash -c " + ShellQuote(cmd), head
}

// ShellQuote quotes s as one POSIX shell word.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
