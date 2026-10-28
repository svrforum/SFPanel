package system

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
)

// promoteToManager runs this agent's promotion once BecomeManager has
// answered; tests replace it.
var promoteToManager = cluster.PromoteToManager

// exitAfterPromotion ends the process once a promotion has run, so that
// systemd starts it again in its new role. Not exitProcess, the exit of an
// update or a restore without systemd, which this package's tests count. A
// test replaces it.
var exitAfterPromotion = os.Exit

// becomeManagerRequest is what the leader sends with a promotion
// (internal/feature/cluster's PromoteNode): a token it made for this node
// alone, the cluster CA's fingerprint and its own gRPC address.
type becomeManagerRequest struct {
	Token   string `json:"token"`
	CAPin   string `json:"ca_pin"`
	Manager string `json:"manager"`
}

// BecomeManagerHandler serves an agent's half of its promotion to a manager.
// The router makes it on an agent only, with the agent's config, the file it
// was read from, and its database, where the join puts the cluster's admin
// account.
type BecomeManagerHandler struct {
	Config     *config.Config
	ConfigPath string
	DB         *sql.DB

	// becoming is the token of this node's promotion once it has begun,
	// guarded by mu; never cleared, for the process exits after it.
	mu       sync.Mutex
	becoming string
}

// BecomeManager (POST /api/v1/system/become-manager) is an agent's half of
// its promotion, which the leader asks for once its gates have passed
// (PromoteNode). Every request an agent takes is one its managers signed —
// the router's InternalProxyOnly — and what admits the join is the token the
// leader bound to this node. While a refusal still changes nothing it checks
// what the manager it becomes will need, then answers 202, and only then
// joins as a manager and restarts (cluster.PromoteToManager).
func (h *BecomeManagerHandler) BecomeManager(c echo.Context) error {
	if !h.Config.Cluster.IsAgent() {
		return response.Fail(c, http.StatusConflict, response.ErrClusterPromoteRefused, "This node is not an agent: only an agent is promoted to a manager")
	}
	if h.ConfigPath == "" {
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "Config path not available")
	}
	var body becomeManagerRequest
	if err := c.Bind(&body); err != nil || body.Token == "" || body.Manager == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "token, ca_pin and manager are required")
	}
	pin, err := cluster.NormalizeCAPin(body.CAPin)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, err.Error())
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.becoming == body.Token:
		// The leader's call is retried once on any error, and the first may
		// have arrived: it is under way.
		return response.Accepted(c, map[string]string{"message": "This node's promotion to a manager is under way"})
	case h.becoming != "":
		return response.Fail(c, http.StatusConflict, response.ErrClusterPromoteRefused, "Another promotion of this node to a manager is under way")
	}

	// The manager binds its Raft port, the gRPC port + 1, on the address this
	// node advertises when it starts. An agent uses no such port; held by
	// anything else, the node would come up without its cluster while the
	// leader lists it as a manager. It stays held until the promotion has run:
	// the leader dials it at the join, before it makes this node a manager,
	// and refuses a node it cannot reach — behind a firewall that closes the
	// port, this node's Raft would never hear from it.
	next := *h.Config
	advertise := next.Cluster.AdvertiseAddress
	grpcPort := next.Cluster.GRPCPort
	if grpcPort == 0 {
		grpcPort = next.Server.Port + 1 // as the join reads it
	}
	raftPort := grpcPort + 1
	ln, err := net.Listen("tcp", net.JoinHostPort(advertise, strconv.Itoa(raftPort)))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, fmt.Sprintf(
			"The Raft port %d (the cluster gRPC port + 1) cannot be listened on at %s, where the manager this node becomes binds it: %s", raftPort, advertise, response.SanitizeOutput(err.Error())))
	}

	h.becoming = body.Token
	req := cluster.ConvertRequest{Token: body.Token, Pin: pin, Manager: body.Manager, Advertise: advertise}
	exit, promote := exitAfterPromotion, promoteToManager
	go func() {
		err := promote(&next, h.ConfigPath, h.DB, req)
		_ = ln.Close() // the manager binds it after the restart
		if err != nil {
			slog.Error("the promotion to a manager failed", "component", "agent", "node_id", next.Cluster.NodeID, "error", err)
		}
		// Restart to come up in the new role (CLAUDE.md: Restart=always
		// starts it again): the router serves one role per boot. After a
		// failure too — an agent whose join was refused comes back as the
		// agent it was.
		exit(1)
	}()
	return response.Accepted(c, map[string]string{"message": "This node is joining the cluster as a manager"})
}
