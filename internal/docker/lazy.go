package docker

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Lazy is the node's Docker client, connected whenever there is a Docker to
// connect to. The client used to be made once at boot, so a panel that started
// on a host without Docker stayed without it until restarted: Packages
// installed Docker, and every Docker menu kept answering "not available".
// Everything that needs the client now asks Lazy, which connects on the first
// request after the socket appears and then runs what was waiting for Docker
// (the handlers' client, the collectors).
type Lazy struct {
	connect func() (*Client, error)
	retry   time.Duration
	now     func() time.Time

	client atomic.Pointer[Client]

	mu      sync.Mutex
	lastTry time.Time
	lastErr error
	waiters []func(*Client)
}

// lazyRetry spaces the attempts while Docker is missing. An attempt is one
// stat of the socket, and a Docker page asks several times a second.
const lazyRetry = 5 * time.Second

// NewLazy returns a Lazy for the daemon at host (unix:///var/run/docker.sock
// and the like). Nothing is attempted until something asks for the client.
func NewLazy(host string) *Lazy {
	return newLazy(func() (*Client, error) { return NewClient(host) }, lazyRetry, time.Now)
}

func newLazy(connect func() (*Client, error), retry time.Duration, now func() time.Time) *Lazy {
	return &Lazy{connect: connect, retry: retry, now: now}
}

// Client returns the client, or nil while Docker cannot be reached; a miss
// tries again once the last attempt is retry old. The attempt that succeeds
// runs everything registered with OnConnect before it publishes the client,
// so whoever gets a client also sees what those callbacks set up.
func (l *Lazy) Client() *Client {
	if c := l.client.Load(); c != nil {
		return c
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if c := l.client.Load(); c != nil {
		return c
	}
	if !l.lastTry.IsZero() && l.now().Sub(l.lastTry) < l.retry {
		return nil
	}
	missed := !l.lastTry.IsZero()
	l.lastTry = l.now()
	c, err := l.connect()
	l.lastErr = err
	if err != nil {
		return nil
	}
	for _, fn := range l.waiters {
		fn(c)
	}
	l.waiters = nil
	l.client.Store(c)
	if missed {
		slog.Info("docker became available", "component", "docker")
	}
	return c
}

// Err is why the last attempt failed: nil once connected, and before any
// attempt.
func (l *Lazy) Err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client.Load() != nil {
		return nil
	}
	return l.lastErr
}

// OnConnect runs fn with the client once Docker is reached, at once if it
// already is. fn runs while Lazy holds its lock: it must not call back into
// Lazy, and anything that takes time belongs in a goroutine it starts.
func (l *Lazy) OnConnect(fn func(*Client)) {
	l.mu.Lock()
	if c := l.client.Load(); c != nil {
		l.mu.Unlock()
		fn(c)
		return
	}
	l.waiters = append(l.waiters, fn)
	l.mu.Unlock()
}

// Watch tries every interval until Docker is reached or ctx ends, so what
// waits on OnConnect starts even when no request asks for Docker.
func (l *Lazy) Watch(ctx context.Context, every time.Duration) {
	if l.Client() != nil {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if l.Client() != nil {
				return
			}
		}
	}
}
