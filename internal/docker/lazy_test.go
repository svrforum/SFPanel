package docker

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time { return f.t }

// Docker is absent at first and appears later — the Packages page installed
// it. Attempts are spaced by retry, and the first one after it appears hands
// out a client.
func TestLazy_ConnectsOnceDockerAppears(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	present := false
	var attempts int
	want := &Client{}
	l := newLazy(func() (*Client, error) {
		attempts++
		if !present {
			return nil, errors.New("docker socket not present")
		}
		return want, nil
	}, 5*time.Second, clock.now)

	if l.Client() != nil {
		t.Fatal("got a client with no Docker")
	}
	if l.Err() == nil {
		t.Fatal("a failed attempt left no reason")
	}
	present = true
	if l.Client() != nil || attempts != 1 {
		t.Fatalf("attempts within the retry interval: %d (want the one attempt)", attempts)
	}
	clock.t = clock.t.Add(5 * time.Second)
	if got := l.Client(); got != want {
		t.Fatalf("after Docker appeared: client %p, want %p", got, want)
	}
	if l.Err() != nil {
		t.Fatalf("connected, yet Err() = %v", l.Err())
	}
	clock.t = clock.t.Add(time.Hour)
	l.Client()
	if attempts != 2 {
		t.Fatalf("a connected Lazy kept connecting: %d attempts", attempts)
	}
}

// What waits for Docker runs once, before anyone else is handed the client:
// the router sets the handlers' client in one, and a request must never find
// the client published but the handler still empty.
func TestLazy_OnConnectRunsBeforeTheClientIsPublished(t *testing.T) {
	l := newLazy(func() (*Client, error) { return &Client{}, nil }, time.Second, time.Now)
	var calls int
	l.OnConnect(func(c *Client) {
		calls++
		if l.client.Load() != nil {
			t.Error("the client was published before OnConnect ran")
		}
	})
	if l.Client() == nil {
		t.Fatal("no client")
	}
	l.Client()
	if calls != 1 {
		t.Fatalf("OnConnect ran %d times, want 1", calls)
	}

	// Registered after the connection: runs at once.
	ran := false
	l.OnConnect(func(*Client) { ran = true })
	if !ran {
		t.Fatal("OnConnect on a connected Lazy did not run")
	}
}

// Many requests arriving as Docker appears connect it once.
func TestLazy_ConcurrentCallersConnectOnce(t *testing.T) {
	var attempts atomic.Int32
	l := newLazy(func() (*Client, error) { attempts.Add(1); return &Client{}, nil }, time.Second, time.Now)
	var waiters atomic.Int32
	l.OnConnect(func(*Client) { waiters.Add(1) })
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Client() == nil {
				t.Error("a caller got no client")
			}
		}()
	}
	wg.Wait()
	if attempts.Load() != 1 || waiters.Load() != 1 {
		t.Fatalf("attempts %d, OnConnect runs %d; want 1 and 1", attempts.Load(), waiters.Load())
	}
}

// Watch connects without any request, and stops with its context.
func TestLazy_Watch(t *testing.T) {
	var present atomic.Bool
	l := newLazy(func() (*Client, error) {
		if !present.Load() {
			return nil, errors.New("absent")
		}
		return &Client{}, nil
	}, 0, time.Now)
	connected := make(chan struct{})
	l.OnConnect(func(*Client) { close(connected) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { l.Watch(ctx, 10*time.Millisecond); close(done) }()
	present.Store(true)
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("Watch never connected")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Watch kept running after connecting")
	}

	absent := newLazy(func() (*Client, error) { return nil, errors.New("absent") }, 0, time.Now)
	ctx2, cancel2 := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { absent.Watch(ctx2, 10*time.Millisecond); close(stopped) }()
	cancel2()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Watch ignored its context")
	}
}
