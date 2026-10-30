package cluster

import (
	"fmt"
	"sync"
	"testing"

	"google.golang.org/grpc/connectivity"
)

// A page that opens on a node sends several proxied requests at once, and when
// the pool holds no live connection to their target each of them dials one.
// Each used to pool its own and close the one it found — the connection another
// of them had just been handed — so all but the last failed with "grpc: the
// client connection is closing", and the phone's node picker, which asks once,
// stayed empty. They now share the first one pooled.
func TestConnPoolConcurrentGetsShareOneOpenConnection(t *testing.T) {
	_, tlsMgr := setupCertDir(t)
	pool := NewConnPool(tlsMgr)
	defer pool.Close()

	const callers = 8
	for round := 0; round < 20; round++ {
		// Never dialed for real: grpc.NewClient connects on the first call.
		addr := fmt.Sprintf("127.0.0.1:%d", 40000+round)
		start := make(chan struct{})
		got := make([]*GRPCClient, callers)
		var wg sync.WaitGroup
		for i := range got {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				c, err := pool.Get(addr)
				if err != nil {
					t.Errorf("Get: %v", err)
					return
				}
				got[i] = c
			}(i)
		}
		close(start)
		wg.Wait()

		for i, c := range got {
			if c == nil {
				t.FailNow()
			}
			if c.conn.GetState() == connectivity.Shutdown {
				t.Fatalf("round %d: caller %d was handed a connection another caller closed", round, i)
			}
			if c != got[0] {
				t.Fatalf("round %d: callers %d and 0 hold different connections to one address", round, i)
			}
		}
	}
}

// Requests that failed on the same pooled connection each retry on a fresh
// one. The retry used to drop whatever the pool held for the address, which by
// then was the connection the first of them had just re-dialed, so that one's
// retry failed with "grpc: the client connection is closing" too. A retry now
// drops only the connection it failed on.
func TestConnPoolRedialLeavesAnotherRetrysConnectionOpen(t *testing.T) {
	_, tlsMgr := setupCertDir(t)
	pool := NewConnPool(tlsMgr)
	defer pool.Close()

	const addr = "127.0.0.1:40100"
	shared, err := pool.Get(addr) // both requests were handed this one
	if err != nil {
		t.Fatal(err)
	}
	first, err := pool.Redial(addr, shared)
	if err != nil {
		t.Fatal(err)
	}
	second, err := pool.Redial(addr, shared)
	if err != nil {
		t.Fatal(err)
	}

	if first.conn.GetState() == connectivity.Shutdown {
		t.Fatal("the second retry closed the connection the first retry was using")
	}
	if second != first {
		t.Fatal("the second retry dialed again instead of using the first retry's connection")
	}
	if shared.conn.GetState() != connectivity.Shutdown {
		t.Fatal("the connection the requests failed on stayed open")
	}
}
