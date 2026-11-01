package featuretasks

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/cluster"
)

// Variables so tests can shorten them.
var (
	// streamPoll is how often the stream reads the run from this manager's
	// replicated state.
	streamPoll = time.Second
	// streamKeepalive is how long the stream stays quiet before it sends a
	// comment, so a proxy or a browser does not take it for dead.
	streamKeepalive = 15 * time.Second
)

// streamEvent is one event of a run's stream, sent as an SSE "data:" line:
//
//	{"type":"snapshot","run":…,"nodes":{…}}  first, the whole run
//	{"type":"node","node_id":…,"record":…}   a node's record changed
//	{"type":"run","run":…}                   the head changed
//	{"type":"end"}                           the run has ended
type streamEvent struct {
	Type   string                           `json:"type"`
	Run    *cluster.RunRecord               `json:"run,omitempty"`
	Nodes  map[string]cluster.RunNodeRecord `json:"nodes,omitempty"`
	NodeID string                           `json:"node_id,omitempty"`
	Record *cluster.RunNodeRecord           `json:"record,omitempty"`
}

// StreamRun answers GET /cluster/task-runs/:id/stream with the run's
// progress as server-sent events. Any manager serves it from its own
// replicated state, as the records are replicated: a node with no record yet
// is sent as pending (withPending). It is never sent on to the leader, and it follows a run across a change of leader. It reads the
// run every streamPoll and sends what changed, a keepalive after
// streamKeepalive of quiet, and "end" once the run has ended (or been
// pruned). A client that comes back starts again from a snapshot. The path
// is a streaming endpoint (middleware isStreamingEndpoint), so a panel
// shutting down cuts it rather than wait for its client.
func (h *Handler) StreamRun(c echo.Context) error {
	env, err := h.apiEnv(c)
	if env == nil {
		return err
	}
	st := env.Store()
	id := c.Param("id")
	head, nodes, err := st.GetRun(id)
	if err != nil {
		return fail(c, runNotFound(err))
	}

	w := c.Response()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.Writer.(http.Flusher)
	last := env.Now()
	// The quiet is counted from when a write starts: a client that has read
	// what was sent may already be acting on it before the flush returns.
	write := func(s string) {
		last = env.Now()
		_, _ = io.WriteString(w, s)
		if flusher != nil {
			flusher.Flush()
		}
	}
	send := func(ev streamEvent) {
		b, err := json.Marshal(ev)
		if err != nil {
			logger().Warn("encoding a task run event", "run", id, "error", err)
			return
		}
		write("data: " + string(b) + "\n\n")
	}

	nodes = withPending(head, nodes)
	send(streamEvent{Type: "snapshot", Run: &head, Nodes: nodes})
	ticker := time.NewTicker(streamPoll)
	defer ticker.Stop()
	for {
		if !head.Active() {
			send(streamEvent{Type: "end"})
			return nil
		}
		select {
		case <-c.Request().Context().Done():
			return nil
		case <-ticker.C:
		}
		next, nextNodes, err := st.GetRun(id)
		switch {
		case errors.Is(err, cluster.ErrTaskRunNotFound):
			send(streamEvent{Type: "end"}) // pruned: nothing more will come
			return nil
		case err != nil:
			logger().Warn("reading a task run for its stream", "run", id, "error", err)
		default:
			nextNodes = withPending(next, nextNodes)
			ids := make([]string, 0, len(nextNodes))
			for nid := range nextNodes {
				ids = append(ids, nid)
			}
			slices.Sort(ids)
			for _, nid := range ids {
				rec := nextNodes[nid]
				if old, ok := nodes[nid]; !ok || !reflect.DeepEqual(old, rec) {
					send(streamEvent{Type: "node", NodeID: nid, Record: &rec})
				}
			}
			if !reflect.DeepEqual(head, next) {
				send(streamEvent{Type: "run", Run: &next})
			}
			head, nodes = next, nextNodes
		}
		if env.Now().Sub(last) >= streamKeepalive {
			write(": keepalive\n\n")
		}
	}
}
