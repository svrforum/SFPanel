package featureserveragents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/serveragents"
)

// The server agents API. GET /agents/status is every node's own: it reads
// the agents of the node it runs on and changes nothing, so managers and
// agents both serve it and the screen asks each server with ?node=. The
// connection values are the cluster's, in the managers' replicated state:
// GET /agents/settings answers from the manager asked, and PUT
// /agents/settings checks the values there and stores them on the leader, a
// follower sending the write on.

// SettingsView is the connection values as the API answers with them. Saved
// is false until they are first saved; until then the values are empty but
// for the default Wazuh version.
type SettingsView struct {
	serveragents.Settings
	Saved bool `json:"saved"`
}

// settingsStore keeps the connection values: the cluster manager.
type settingsStore interface {
	ServerAgentSettings() (*serveragents.Settings, error)
	PutServerAgentSettings(s serveragents.Settings) error
}

// place is where a manager stands: in a cluster or not, its leader or not,
// and as its leader whether its FSM holds every committed entry.
type place struct{ inCluster, leader, caughtUp bool }

const (
	// statusTimeout bounds a reading of the node's agents: its TCP checks
	// take 3 seconds at most each.
	statusTimeout = 10 * time.Second
	// caughtUpWait bounds the wait for the barrier a new leader's first
	// write takes, as the task API's does.
	caughtUpWait = 2 * time.Second
)

// Handler serves the server agents API.
type Handler struct {
	Host *Host
	// store is the connection values' store and where this node stands,
	// read per request: a manager is activated without a restart. Outside a
	// cluster the store is nil.
	store func() (settingsStore, place)
	// toLeader sends a request on to the leader (middleware.ProxyToLeader).
	toLeader func(c echo.Context) (handled bool, err error)
	now      func() time.Time
}

// NewHandler returns the API over host's agents and the connection values
// of the cluster manager getMgr returns (nil outside a cluster).
func NewHandler(host *Host, getMgr func() *cluster.Manager) *Handler {
	h := &Handler{Host: host, now: time.Now}
	h.store = func() (settingsStore, place) {
		m := getMgr()
		switch {
		case m == nil:
			return nil, place{}
		case !m.IsLeader():
			return m, place{inCluster: true}
		}
		return m, place{inCluster: true, leader: true, caughtUp: m.CaughtUpLeader(caughtUpWait)}
	}
	h.toLeader = func(c echo.Context) (bool, error) { return middleware.ProxyToLeader(c, getMgr()) }
	return h
}

// Status answers GET /agents/status: how this node's Zabbix and Wazuh
// agents are installed, what their config files say, and whether each
// reaches its server.
func (h *Handler) Status(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), statusTimeout)
	defer cancel()
	return response.OK(c, h.Host.Status(ctx))
}

// GetSettings answers GET /agents/settings from this manager's replicated
// state.
func (h *Handler) GetSettings(c echo.Context) error {
	st, p := h.store()
	if !p.inCluster {
		return notInCluster(c)
	}
	s, err := st.ServerAgentSettings()
	if err != nil {
		logger().Error("reading the server agent settings", "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "The agents' connection values could not be read")
	}
	if s == nil {
		return response.OK(c, SettingsView{Settings: serveragents.Settings{}.Normalize()})
	}
	return response.OK(c, SettingsView{Settings: *s, Saved: true})
}

// settingsBody is PUT /agents/settings' body: both agents' values, which
// replace the saved ones whole. Who saved them and when is the leader's to
// say.
type settingsBody struct {
	Zabbix serveragents.ZabbixSettings `json:"zabbix"`
	Wazuh  serveragents.WazuhSettings  `json:"wazuh"`
}

// PutSettings answers PUT /agents/settings: the values normalized, then
// checked, on whichever manager is asked, then stored by the leader.
func (h *Handler) PutSettings(c echo.Context) error {
	st, p := h.store()
	if !p.inCluster {
		return notInCluster(c)
	}
	raw, err := io.ReadAll(c.Request().Body)
	if err != nil {
		return err // over the body limit: echo's 413
	}
	var body settingsBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	// Validate checks normalized values and does not normalize them itself.
	s := serveragents.Settings{Zabbix: body.Zabbix, Wazuh: body.Wazuh}.Normalize()
	if fe := s.Validate(); fe != nil {
		return response.FailWith(c, http.StatusBadRequest, response.ErrServerAgentSettingsInvalid, fe.Error(), map[string]string{"field": fe.Field})
	}
	switch {
	case !p.leader:
		// The leader reads the body again, as it came.
		c.Request().Body = io.NopCloser(bytes.NewReader(raw))
		if handled, err := h.toLeader(c); handled {
			return err
		}
	case !p.caughtUp:
		return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "The cluster leader is catching up: retry in a few seconds")
	}
	s.UpdatedAt = h.now().UTC()
	s.UpdatedBy, _ = c.Get("username").(string)
	if err := st.PutServerAgentSettings(s); err != nil {
		if errors.Is(err, cluster.ErrNotLeader) {
			return response.Fail(c, http.StatusServiceUnavailable, response.ErrInternalError, "This node is not the cluster leader: retry in a few seconds")
		}
		logger().Error("saving the server agent settings", "error", err)
		return response.Fail(c, http.StatusInternalServerError, response.ErrInternalError, "The agents' connection values could not be saved")
	}
	return response.OK(c, SettingsView{Settings: s, Saved: true})
}

func notInCluster(c echo.Context) error {
	return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
}
