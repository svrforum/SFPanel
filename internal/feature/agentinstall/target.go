package agentinstall

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/sshexec"
)

// Target is a logged-in connection to a server being installed: what the
// engine does there, which tests replace without SSH.
type Target interface {
	// Run runs cmd with stdin as its standard input; a command that ran and
	// exited non-zero is not an error (sshexec.Client.Run). A Target that
	// can be spent by a Run, as sshexec.Client is, says so with a Spent()
	// bool method, and the row dials again for its clean-up.
	Run(ctx context.Context, cmd string, stdin []byte) (stdout, stderr []byte, exit int, err error)
	// Upload writes data to path as the login, its bytes on stdin only.
	Upload(ctx context.Context, path string, mode os.FileMode, data []byte) error
	Close() error
}

// Dialer opens Targets: it learns a server's host key without logging in,
// and logs in against the key the operator approved.
type Dialer interface {
	FetchHostKey(ctx context.Context, addr, user string) (sshexec.HostKey, error)
	Dial(ctx context.Context, addr string, a sshexec.Auth, key sshexec.HostKey, onNotice func(url string)) (Target, error)
}

// hostKeyTimeout bounds learning a server's host key.
const hostKeyTimeout = 15 * time.Second

// sshDialer is Dialer over internal/sshexec.
type sshDialer struct{}

func (sshDialer) FetchHostKey(ctx context.Context, addr, user string) (sshexec.HostKey, error) {
	return sshexec.FetchHostKey(ctx, addr, sshexec.Auth{User: user}, hostKeyTimeout)
}

func (sshDialer) Dial(ctx context.Context, addr string, a sshexec.Auth, key sshexec.HostKey, onNotice func(string)) (Target, error) {
	c, err := sshexec.Dial(ctx, addr, a, key, onNotice)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// hostKeyText is a host key as the operator compares it with what the
// server's console prints: "ED25519 SHA256:…", the algorithm's short name
// as ssh-keygen -l shows it, then the fingerprint.
func hostKeyText(k sshexec.HostKey) string {
	name := k.Type
	switch {
	case name == "ssh-ed25519":
		name = "ED25519"
	case name == "ssh-rsa":
		name = "RSA"
	case strings.HasPrefix(name, "ecdsa-sha2-"):
		name = "ECDSA"
	case strings.HasPrefix(name, "sk-ssh-ed25519"):
		name = "ED25519-SK"
	case strings.HasPrefix(name, "sk-ecdsa-"):
		name = "ECDSA-SK"
	default:
		name = strings.ToUpper(strings.TrimPrefix(name, "ssh-"))
	}
	return name + " " + k.Fingerprint
}
