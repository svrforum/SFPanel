package safe

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestGo_RecoversFromPanic(t *testing.T) {
	done := make(chan struct{})
	Go("test", func() {
		defer close(done)
		panic("expected")
	})

	select {
	case <-done:
		// pass — closure ran, panic recovered.
	case <-time.After(time.Second):
		t.Fatal("goroutine did not run / closed channel")
	}
}

func TestGo_RunsClosure(t *testing.T) {
	var n int32
	done := make(chan struct{})
	Go("test", func() {
		atomic.StoreInt32(&n, 42)
		close(done)
	})
	<-done
	if atomic.LoadInt32(&n) != 42 {
		t.Errorf("closure did not execute: n=%d", atomic.LoadInt32(&n))
	}
}
