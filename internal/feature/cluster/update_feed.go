package featurecluster

import "sync"

// updateFeed carries a cluster update's progress from the orchestration to
// whoever is watching it, without letting the watcher decide whether the
// update finishes.
//
// The watcher can vanish mid-update for an ordinary reason: an update started
// on a follower reaches the leader through that follower's relay, and the
// follower is one of the nodes the update restarts. The orchestration used to
// run on the request goroutine and read a closed request as "the operator
// cancelled", so it stopped right after updating the node that relayed it and
// never reached the leader. It now runs on its own goroutine; events nobody is
// left to read are dropped instead of blocking it.
type updateFeed struct {
	events chan map[string]interface{}
	gone   chan struct{}
	leave  sync.Once
}

func newUpdateFeed() *updateFeed {
	return &updateFeed{events: make(chan map[string]interface{}, 64), gone: make(chan struct{})}
}

// emit hands one event to the watcher, or drops it once the watcher has left.
func (f *updateFeed) emit(ev map[string]interface{}) {
	select {
	case f.events <- ev:
	case <-f.gone:
	}
}

// finish tells the watcher nothing more is coming. Only the orchestration
// calls it, after its last emit.
func (f *updateFeed) finish() { close(f.events) }

// forward writes events to the watcher until the update finishes or done
// closes, whichever comes first. Leaving early never cancels the update.
func (f *updateFeed) forward(done <-chan struct{}, write func(map[string]interface{})) {
	defer f.leave.Do(func() { close(f.gone) })
	for {
		select {
		case ev, ok := <-f.events:
			if !ok {
				return
			}
			write(ev)
		case <-done:
			return
		}
	}
}
