package sshexec

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// execRecord is one exec request as the test server received it: the
// command line exactly as it reached the server, and every byte the client
// sent on the session's stdin.
type execRecord struct {
	Cmd   string
	Stdin []byte
}

// serverOpts picks what the test server accepts and sends.
type serverOpts struct {
	// password, when set, is the only password that logs in. With
	// kbdPassword the server asks for it only through keyboard-interactive,
	// as many PAM setups do, and does not offer the password method.
	password    string
	kbdPassword bool
	// authorizedKey, when set, is the only public key that logs in.
	authorizedKey ssh.PublicKey
	// allowNone lets the "none" method log in, as Tailscale SSH does.
	allowNone bool
	// banner is sent before authentication, as Tailscale's check notice is.
	banner string
	// kbdInstruction, when set, makes keyboard-interactive send one
	// challenge with this instruction and no questions, then log in.
	kbdInstruction string
	// handle answers an exec; nil answers every command with exit 0 and no
	// output.
	handle func(cmd string, stdin []byte) (stdout, stderr []byte, exit uint32)
	// silentChannels never answers a channel open, neither accepting nor
	// rejecting it: a server that stopped answering without a reset.
	silentChannels bool
}

// testServer is an in-process SSH server on 127.0.0.1 that records every
// authentication attempt and every exec instead of running anything.
type testServer struct {
	addr   string
	signer ssh.Signer

	mu       sync.Mutex
	attempts []string
	execs    []execRecord
}

func newHostSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func startTestServer(t *testing.T, opts serverOpts) *testServer {
	t.Helper()
	s := &testServer{signer: newHostSigner(t)}

	cfg := &ssh.ServerConfig{
		AuthLogCallback: func(_ ssh.ConnMetadata, method string, _ error) {
			s.mu.Lock()
			s.attempts = append(s.attempts, method)
			s.mu.Unlock()
		},
	}
	if opts.password != "" && !opts.kbdPassword {
		cfg.PasswordCallback = func(_ ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			if string(pw) == opts.password {
				return nil, nil
			}
			return nil, errTestRefused
		}
	}
	if opts.kbdPassword || opts.kbdInstruction != "" {
		cfg.KeyboardInteractiveCallback = func(_ ssh.ConnMetadata, client ssh.KeyboardInteractiveChallenge) (*ssh.Permissions, error) {
			if opts.kbdInstruction != "" {
				if _, err := client("", opts.kbdInstruction, nil, nil); err != nil {
					return nil, err
				}
				return nil, nil
			}
			answers, err := client("", "", []string{"Password: "}, []bool{false})
			if err != nil {
				return nil, err
			}
			if len(answers) == 1 && answers[0] == opts.password {
				return nil, nil
			}
			return nil, errTestRefused
		}
	}
	if opts.authorizedKey != nil {
		want := opts.authorizedKey.Marshal()
		cfg.PublicKeyCallback = func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(want) {
				return nil, nil
			}
			return nil, errTestRefused
		}
	}
	cfg.NoClientAuth = opts.allowNone
	if opts.banner != "" {
		cfg.BannerCallback = func(ssh.ConnMetadata) string { return opts.banner }
	}
	cfg.AddHostKey(s.signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	s.addr = ln.Addr().String()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn, cfg, opts)
		}
	}()
	return s
}

var errTestRefused = errors.New("refused")

func (s *testServer) serve(conn net.Conn, cfg *ssh.ServerConfig, opts serverOpts) {
	defer conn.Close()
	sc, chans, reqs, err := ssh.NewServerConn(conn, cfg)
	if err != nil {
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if opts.silentChannels {
			continue
		}
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.UnknownChannelType, "only sessions")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go s.session(ch, chReqs, opts)
	}
}

func (s *testServer) session(ch ssh.Channel, reqs <-chan *ssh.Request, opts serverOpts) {
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		if len(req.Payload) < 4 {
			_ = req.Reply(false, nil)
			return
		}
		n := binary.BigEndian.Uint32(req.Payload)
		cmd := string(req.Payload[4 : 4+n])
		_ = req.Reply(true, nil)

		stdin, _ := io.ReadAll(ch)
		s.mu.Lock()
		s.execs = append(s.execs, execRecord{Cmd: cmd, Stdin: stdin})
		s.mu.Unlock()

		var stdout, stderr []byte
		var exit uint32
		if opts.handle != nil {
			stdout, stderr, exit = opts.handle(cmd, stdin)
		}
		_, _ = ch.Write(stdout)
		_, _ = ch.Stderr().Write(stderr)
		status := make([]byte, 4)
		binary.BigEndian.PutUint32(status, exit)
		_, _ = ch.SendRequest("exit-status", false, status)
		return
	}
}

// hostKey is the server's host key as FetchHostKey would report it.
func (s *testServer) hostKey() HostKey {
	pub := s.signer.PublicKey()
	return HostKey{Type: pub.Type(), Fingerprint: ssh.FingerprintSHA256(pub), Key: pub}
}

func (s *testServer) recordedAttempts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.attempts...)
}

func (s *testServer) recordedExecs() []execRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]execRecord(nil), s.execs...)
}
