package featurecluster

import (
	"testing"
	"time"
)

func TestUpdateFeedForwardsInOrderUntilFinished(t *testing.T) {
	f := newUpdateFeed()
	go func() {
		for i := 0; i < 3; i++ {
			f.emit(map[string]interface{}{"n": i})
		}
		f.finish()
	}()
	var got []int
	f.forward(make(chan struct{}), func(ev map[string]interface{}) { got = append(got, ev["n"].(int)) })
	if len(got) != 3 || got[0] != 0 || got[2] != 2 {
		t.Fatalf("forwarded %v, want [0 1 2]", got)
	}
}

// The property the feed exists for: once the watcher is gone, the
// orchestration keeps going. Filling the buffer past its size would block
// emit forever if it still waited for a reader.
func TestUpdateFeedEmitNeverBlocksAfterTheWatcherLeaves(t *testing.T) {
	f := newUpdateFeed()
	left := make(chan struct{})
	close(left)
	f.forward(left, func(map[string]interface{}) {})

	finished := make(chan struct{})
	go func() {
		for i := 0; i < cap(f.events)*4; i++ {
			f.emit(map[string]interface{}{"n": i})
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("emit blocked after the watcher left: the update would stall with nobody watching")
	}
}
