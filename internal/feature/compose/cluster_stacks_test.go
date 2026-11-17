package compose

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// A follower's heartbeat view of a peer can say "offline" while the proxy
// reaches it. The stacks view drew such a node with a red dot beside the
// stacks it had just returned.
func TestApplyRemoteAnswer_NodeThatAnsweredIsOnline(t *testing.T) {
	ns := ClusterNodeStacks{Status: "offline"}
	applyRemoteAnswer(&ns, http.StatusOK, []byte(`{"success":true,"data":[{"name":"web","real_status":"running"}]}`), nil)
	require.Equal(t, "online", ns.Status)
	require.Empty(t, ns.Error)
	require.Len(t, ns.Stacks, 1)
	require.Equal(t, "web", ns.Stacks[0].Name)
}

// 404 is a reachable node without Docker: online, no stacks, no error.
func TestApplyRemoteAnswer_NodeWithoutDockerIsOnline(t *testing.T) {
	ns := ClusterNodeStacks{Status: "suspect"}
	applyRemoteAnswer(&ns, http.StatusNotFound, nil, nil)
	require.Equal(t, "online", ns.Status)
	require.Empty(t, ns.Error)
	require.Empty(t, ns.Stacks)
}

// A node that did not answer keeps the heartbeat's word and is marked.
func TestApplyRemoteAnswer_UnreachableKeepsHeartbeatStatus(t *testing.T) {
	ns := ClusterNodeStacks{Status: "offline"}
	applyRemoteAnswer(&ns, 0, nil, errors.New("dial tcp: connection refused"))
	require.Equal(t, "offline", ns.Status)
	require.Equal(t, "unreachable", ns.Error)

	ns = ClusterNodeStacks{Status: "suspect"}
	applyRemoteAnswer(&ns, http.StatusBadGateway, nil, nil)
	require.Equal(t, "suspect", ns.Status)
	require.Equal(t, "unreachable", ns.Error)
}
