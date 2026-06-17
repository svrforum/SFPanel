package compose

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/docker"
)

// ClusterNodeStacks is one node's compose stacks in the cluster-wide view.
type ClusterNodeStacks struct {
	NodeID   string                            `json:"node_id"`
	NodeName string                            `json:"node_name"`
	Local    bool                              `json:"local"`
	Status   string                            `json:"status"` // node health (online/suspect/offline/…) for the UI dot
	Stacks   []docker.ComposeProjectWithStatus `json:"stacks"`
	// Error, when set, is a STABLE machine code (e.g. "unreachable", "list_failed")
	// the frontend maps to translated copy — never a raw stderr string.
	Error string `json:"error,omitempty"`
}

// ClusterStacks (GET /docker/compose/cluster-stacks) aggregates compose stacks
// across every cluster node — the local node listed directly, remote nodes via
// the cluster proxy. An offline or unreachable node contributes an error marker
// with an empty list, never a 500, so one bad node can't break the whole view.
// Nodes are queried concurrently. Called without ?node= so it runs locally and
// fans out; the per-node results carry node attribution for the UI.
func (h *Handler) ClusterStacks(c echo.Context) error {
	mgr := h.clusterManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInternalError, "cluster is not enabled")
	}
	ctx := c.Request().Context()
	username, _ := c.Get("username").(string)
	localID := mgr.LocalNodeID()
	nodes := mgr.GetNodes()

	out := make([]ClusterNodeStacks, len(nodes))
	var wg sync.WaitGroup
	for i := range nodes {
		i, node := i, nodes[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			ns := ClusterNodeStacks{
				NodeID: node.ID, NodeName: node.Name, Local: node.ID == localID,
				Status: string(node.Status),
				Stacks: []docker.ComposeProjectWithStatus{},
			}
			if node.ID == localID {
				if projects, err := h.Compose.ListProjectsWithStatus(ctx); err != nil {
					ns.Error = "list_failed"
				} else if projects != nil {
					ns.Stacks = projects
				}
			} else {
				// Always TRY the proxy rather than trusting the local heartbeat
				// status (a follower's local view of a peer can be stale even while
				// the proxy works). A genuinely-down node fails fast on the bounded
				// timeout and is marked unreachable — never a 500.
				nctx, cancel := context.WithTimeout(ctx, 15*time.Second)
				status, body, err := mgr.ProxyToNode(nctx, node.ID, http.MethodGet, "/api/v1/docker/compose", nil, username)
				cancel()
				switch {
				case err != nil:
					ns.Error = "unreachable"
				case status == http.StatusNotFound:
					// Node is reachable but Docker isn't enabled there, so its compose
					// routes aren't registered (socket-gating). An empty list with no
					// error is the right answer, not "unreachable".
				case status != http.StatusOK:
					ns.Error = "unreachable"
				default:
					var wrap struct {
						Data []docker.ComposeProjectWithStatus `json:"data"`
					}
					if json.Unmarshal(body, &wrap) == nil && wrap.Data != nil {
						ns.Stacks = wrap.Data
					}
				}
			}
			out[i] = ns
		}()
	}
	wg.Wait()
	return response.OK(c, out)
}
