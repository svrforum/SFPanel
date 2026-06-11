package docker

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// NewClient must fail when the unix socket file is absent — the router uses
// that error as the gate that keeps /docker routes off Docker-less hosts.
func TestNewClient_FailsWhenSocketAbsent(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "missing.sock")
	if _, err := NewClient("unix://" + sock); err == nil {
		t.Fatal("expected error for absent socket, got nil")
	}
}

func TestNewClient_SucceedsWhenSocketPresent(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "docker.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("API-Version", "1.43")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	c, err := NewClient("unix://" + sock)
	if err != nil {
		t.Fatalf("NewClient against present socket: %v", err)
	}
	if c == nil {
		t.Fatal("nil client on success")
	}
}

// Regression: a socket that exists but has no daemon answering (dockerd down,
// or starting after the panel) must still yield a client. Gating on a live
// Ping here would leave Docker features dead until a panel restart; the SDK
// redials per request so they recover on their own.
func TestNewClient_SocketPresentButDaemonDownStillReturnsClient(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "docker.sock")
	// A path that exists on disk but has no daemon answering — the
	// transient-down / late-start case. The gate only stats the path, so a
	// plain file is enough to stand in for a socket whose daemon is down.
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatalf("create stub socket file: %v", err)
	}

	c, err := NewClient("unix://" + sock)
	if err != nil {
		t.Fatalf("expected a client for a present-but-dead socket, got error: %v", err)
	}
	if c == nil {
		t.Fatal("nil client when socket present but daemon down")
	}
}
