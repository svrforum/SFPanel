package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/monitor"
)

// overviewBroadcastInterval is how often the shared sampler rebuilds the cluster
// snapshot. A var so tests can shorten it. 5s gives a near-live dashboard
// without hammering the FSM.
var overviewBroadcastInterval = 5 * time.Second

// leaderOverviewTimeout bounds a follower's ask to the leader within one tick.
const leaderOverviewTimeout = 3 * time.Second

// leaderOverview is the leader's GET /cluster/overview, asked the way the HTTP
// handler asks for it on a follower. Node health is what the leader hears in
// heartbeats, which a follower never receives: its own view shows every other
// node offline with no metrics. A var so tests can stand in for the leader.
var leaderOverview = func(mgr *cluster.Manager) (map[string]interface{}, error) {
	local := mgr.GetOverview()
	if local == nil || local.LeaderID == "" {
		return nil, errors.New("no leader known")
	}
	ctx, cancel := context.WithTimeout(context.Background(), leaderOverviewTimeout)
	defer cancel()
	status, body, err := mgr.ProxyToNode(ctx, local.LeaderID, http.MethodGet, "/api/v1/cluster/overview", nil, "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("leader answered %d", status)
	}
	var resp struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if !resp.Success || resp.Data == nil {
		return nil, errors.New("leader sent no overview")
	}
	return resp.Data, nil
}

// latestRelease is the release the leader's update checker last saw. A var so
// tests can set it.
var latestRelease = monitor.CachedLatest

// buildClusterSnapshot assembles the combined status+overview+events payload the
// dashboard needs. The leader builds it from its FSM and in-memory event bus. A
// follower takes the overview from the leader (leaderOverview), once per tick
// for every dashboard open on it, and serves its own view flagged stale=true
// only when the leader does not answer, so the UI can show its "may be out of
// date" banner. This is what lets one WS push replace the old 15s triple-poll
// (status + overview + events) without a per-tab leader round-trip.
func buildClusterSnapshot(mgr *cluster.Manager) map[string]interface{} {
	if mgr == nil {
		return map[string]interface{}{"enabled": false}
	}
	isLeader := mgr.IsLeader()
	events := mgr.GetEvents().Recent(20)
	if events == nil {
		events = []cluster.ClusterEvent{}
	}
	snap := map[string]interface{}{
		"enabled":   true,
		"local_id":  mgr.LocalNodeID(),
		"is_leader": isLeader,
		"stale":     !isLeader,
		"events":    events,
	}
	if !isLeader {
		if lo, err := leaderOverview(mgr); err == nil {
			snap["stale"] = false
			for _, k := range []string{"name", "node_count", "leader_id", "nodes", "metrics", "stale", "latest_version"} {
				if v, ok := lo[k]; ok {
					snap[k] = v
				}
			}
			return snap
		}
	}
	if overview := mgr.GetOverview(); overview != nil {
		snap["name"] = overview.Name
		snap["node_count"] = overview.NodeCount
		snap["leader_id"] = overview.LeaderID
		snap["nodes"] = overview.Nodes
		snap["metrics"] = overview.Metrics
	}
	// The page takes each push for its whole state, so the leader's carries
	// what its GET /cluster/overview does. A follower's own stale view has
	// none: it does not check, and a cache from when it led could be old.
	if isLeader {
		if v := latestRelease(); v != "" {
			snap["latest_version"] = v
		}
	}
	return snap
}

// overviewBroadcasterT collapses every open cluster dashboard into a single
// sampler: one goroutine rebuilds the snapshot per interval and fans it out to
// all subscribers on this node, instead of each browser tab polling status +
// overview + events on its own 15s timer. Mirrors monitor's metrics broadcaster.
type overviewBroadcasterT struct {
	mu         sync.Mutex
	subs       map[chan map[string]interface{}]struct{}
	latest     map[string]interface{}
	running    bool
	getManager func() *cluster.Manager
}

var clusterOverviewBroadcaster = &overviewBroadcasterT{subs: make(map[chan map[string]interface{}]struct{})}

// subscribeOverview registers a subscriber. getManager is captured so the
// sampler can resolve the (possibly runtime-activated) manager each tick. The
// channel is buffered by 1; a slow client drops a tick rather than stalling the
// shared sampler. The cached latest snapshot is delivered immediately so a
// freshly opened dashboard paints without waiting a full interval.
func subscribeOverview(getManager func() *cluster.Manager) (<-chan map[string]interface{}, func()) {
	b := clusterOverviewBroadcaster
	b.mu.Lock()
	defer b.mu.Unlock()

	b.getManager = getManager
	ch := make(chan map[string]interface{}, 1)
	b.subs[ch] = struct{}{}
	if b.latest != nil {
		ch <- b.latest
	}
	if !b.running {
		b.running = true
		go b.run()
	}

	var once sync.Once
	return ch, func() { once.Do(func() { b.unsubscribe(ch) }) }
}

func (b *overviewBroadcasterT) unsubscribe(ch chan map[string]interface{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
}

func (b *overviewBroadcasterT) run() {
	b.mu.Lock()
	interval := overviewBroadcastInterval
	getManager := b.getManager
	b.mu.Unlock()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		b.mu.Lock()
		if len(b.subs) == 0 {
			b.running = false
			b.mu.Unlock()
			return
		}
		b.mu.Unlock()

		var mgr *cluster.Manager
		if getManager != nil {
			mgr = getManager()
		}
		snap := buildClusterSnapshot(mgr)

		b.mu.Lock()
		b.latest = snap
		for ch := range b.subs {
			select {
			case ch <- snap:
			default:
			}
		}
		b.mu.Unlock()
	}
}

// ClusterOverviewWS streams the combined cluster snapshot (status + overview +
// recent events) to the dashboard over a WebSocket, replacing the old 15s
// HTTP triple-poll. All connections on a node share one sampler.
func ClusterOverviewWS(getManager func() *cluster.Manager, jwtSecret func() string) echo.HandlerFunc {
	return func(c echo.Context) error {
		if _, err := AuthenticateWS(c, jwtSecret()); err != nil {
			return err
		}
		ws, err := Upgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}
		defer ws.Close()

		ctx, cancel := context.WithCancel(c.Request().Context())
		defer cancel()

		writer := &safeWSWriter{conn: ws}
		startWSKeepalive(ctx, ws, writer)

		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				if _, _, err := ws.ReadMessage(); err != nil {
					return
				}
			}
		}()

		snapshots, unsubscribe := subscribeOverview(getManager)
		defer unsubscribe()

		for {
			select {
			case <-done:
				return nil
			case <-ctx.Done():
				return nil
			case snap, ok := <-snapshots:
				if !ok {
					return nil
				}
				if err := writer.WriteJSON(snap); err != nil {
					return nil
				}
			}
		}
	}
}
