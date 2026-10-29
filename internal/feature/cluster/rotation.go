package featurecluster

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/middleware"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/config"
	"gopkg.in/yaml.v3"
)

// applyJWTKey makes this manager run k, the cluster's new JWT key: the
// applier the manager calls (cluster.Manager.SetJWTKeyApplier) when the FSM
// holds a newer generation than this node runs. The first time for a
// generation it ends every session here while the key changes: the refresh
// tokens go first, then the key is saved and swapped in, then the WebSocket
// tickets go (EndSessions). A key swapped alone ends nobody's session — the
// refresh tokens mint new tokens with it. The key runs even when config.yaml
// cannot be written, for the node must stop taking the retired key at once.
// An error makes the manager call again, and a call for a generation whose
// sessions have ended here only saves: ending them again would end the ones
// signed in since.
func (h *Handler) applyJWTKey(k cluster.JWTKey) error {
	h.jwtMu.Lock()
	defer h.jwtMu.Unlock()
	if h.jwtSwapped >= k.Generation {
		return h.saveJWTKey(k)
	}
	var saveErr error
	err := h.EndSessions(func() {
		saveErr = h.saveJWTKey(k)
		auth.SetJWTSecret(k.Secret)
	})
	if err == nil {
		h.jwtSwapped = k.Generation
	}
	return errors.Join(err, saveErr)
}

// saveJWTKey writes k and its generation to config.yaml in one atomic write
// — what a restart and the CLI read — and then to the in-memory config, only
// once the file holds them: a leave or a disband writes the in-memory config
// out whole, and the generation it holds is what this node has saved.
func (h *Handler) saveJWTKey(k cluster.JWTKey) error {
	h.configMu.Lock()
	defer h.configMu.Unlock()
	if h.ConfigPath != "" {
		next := *h.Config
		next.Auth.JWTSecret, next.Auth.JWTGeneration = k.Secret, k.Generation
		data, err := yaml.Marshal(&next)
		if err != nil {
			return fmt.Errorf("encode the config with the new JWT key: %w", err)
		}
		if err := config.AtomicWriteFile(h.ConfigPath, data, 0600); err != nil {
			return fmt.Errorf("save the new JWT key to config.yaml: %w", err)
		}
	}
	h.Config.Auth.JWTSecret, h.Config.Auth.JWTGeneration = k.Secret, k.Generation
	return nil
}

// wireJWTApplier makes m apply the cluster's rotated JWT keys through this
// handler, from the generation config.yaml says this node runs. Without
// EndSessions nothing would end the sessions a rotation must end, so nothing
// is wired, and the manager does not report that it follows a rotation.
func (h *Handler) wireJWTApplier(m *cluster.Manager) {
	if h.EndSessions == nil || h.Config == nil {
		return
	}
	h.jwtMu.Lock()
	h.configMu.Lock()
	running := h.Config.Auth.JWTGeneration
	h.configMu.Unlock()
	h.jwtSwapped = running
	h.jwtMu.Unlock()
	// Unlocked: the manager applies a newer key at once, through applyJWTKey.
	m.SetJWTKeyApplier(h.applyJWTKey, running)
}

// RotateJWT (POST /cluster/rotate/jwt) gives the cluster a new JWT key, which
// every manager applies and with it ends every session it holds — the
// caller's own included: this answer is the last its session gets, and the
// operator signs in again. A follower forwards it to the leader. It is
// refused (409) while a manager cannot follow, nothing having changed.
func (h *Handler) RotateJWT(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	if handled, err := middleware.ProxyToLeader(c, mgr); handled {
		return err
	}
	gen, err := mgr.RotateJWTKey()
	switch {
	case errors.Is(err, cluster.ErrRotationRefused):
		return response.Fail(c, http.StatusConflict, response.ErrClusterRotationRefused, err.Error())
	case err != nil:
		return clusterErrResponse(c, err)
	}
	return response.OK(c, map[string]interface{}{
		"generation": gen,
		"message":    "The cluster has a new JWT key: every session has ended, this one included — sign in again",
	})
}

// RotateCA (POST /cluster/rotate/ca) starts replacing the cluster CA: the
// leader makes the next CA, and every node moves to it a step at a time,
// each step waiting for every node — GET /cluster/rotation follows it. The
// body may say how long the trust and reissue phases may each take before
// the rotation goes back ({"phase_timeout":"30m"}; none: 30 minutes). A
// follower forwards it to the leader. Refused (409), nothing having changed,
// while a node cannot follow, a rotation is under way, the proxy secret is
// switching or a cluster update runs.
func (h *Handler) RotateCA(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	if handled, err := middleware.ProxyToLeader(c, mgr); handled {
		return err
	}
	var body struct {
		PhaseTimeout string `json:"phase_timeout"`
	}
	if err := c.Bind(&body); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Invalid request body")
	}
	var phaseTimeout time.Duration
	if body.PhaseTimeout != "" {
		d, err := time.ParseDuration(body.PhaseTimeout)
		if err != nil {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "phase_timeout must be a duration, such as 30m")
		}
		phaseTimeout = d
	}
	// Not during a cluster update: it restarts nodes, and a node restarted
	// mid-rotation comes back on whatever its disk held. The check and the
	// start are one step under startMu, as an update's check and claim are:
	// an update and a rotation arriving together would each pass their check
	// before the other began.
	h.startMu.Lock()
	defer h.startMu.Unlock()
	if h.updateRunning.Load() {
		return response.Fail(c, http.StatusConflict, response.ErrUpdateInProgress, "A cluster update is running: start the CA rotation once it has finished")
	}
	rot, err := mgr.StartCARotation(phaseTimeout)
	switch {
	case errors.Is(err, cluster.ErrRotationRefused), errors.Is(err, cluster.ErrCARotationInProgress), errors.Is(err, cluster.ErrProxyRotationPending):
		return response.Fail(c, http.StatusConflict, response.ErrClusterRotationRefused, err.Error())
	case err != nil:
		return clusterErrResponse(c, err)
	}
	return response.Accepted(c, map[string]interface{}{
		"rotation": rot,
		"message":  "The cluster CA is being replaced: follow it with GET /api/v1/cluster/rotation",
	})
}

// AbortCARotation (POST /cluster/rotate/ca/abort) stops the CA rotation and
// keeps the current CA: every node goes back to it a step at a time, as it
// came, and GET /cluster/rotation follows that too. A follower forwards it to
// the leader. Refused (409) once the next CA is the cluster's — a node may
// have dropped the old one, and another rotation is the way back — with no
// rotation to stop, and on one going back already.
func (h *Handler) AbortCARotation(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	if handled, err := middleware.ProxyToLeader(c, mgr); handled {
		return err
	}
	rot, err := mgr.AbortCARotation()
	switch {
	case errors.Is(err, cluster.ErrCARotationPastReturn), errors.Is(err, cluster.ErrNoCARotation), errors.Is(err, cluster.ErrCARotationInProgress):
		return response.Fail(c, http.StatusConflict, response.ErrClusterRotationRefused, err.Error())
	case err != nil:
		return clusterErrResponse(c, err)
	}
	return response.Accepted(c, map[string]interface{}{
		"rotation": rot,
		"message":  "The CA rotation goes back to the current CA: follow it with GET /api/v1/cluster/rotation",
	})
}

// GetRotation (GET /cluster/rotation) is where the cluster stands in its
// secret rotations, node by node — generations and fingerprints, never a
// key. A follower asks the leader, which alone hears every node.
func (h *Handler) GetRotation(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	if handled, err := middleware.ProxyToLeader(c, mgr); handled {
		return err
	}
	st, err := mgr.RotationStatus()
	if err != nil {
		return clusterErrResponse(c, err)
	}
	return response.OK(c, st)
}

// RotateProxy (POST /cluster/rotate/proxy) starts replacing the managers'
// internal proxy secret and answers at once (202): every manager takes the
// new secret before any signs with it, then all switch, each step on the
// leader's watch — GET /cluster/rotation follows it. A follower forwards it
// to the leader. Refused (409), nothing having changed, while a manager
// cannot follow, while plan A's first switch has not settled, and while the
// previous secret is still accepted after a switch.
func (h *Handler) RotateProxy(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	if handled, err := middleware.ProxyToLeader(c, mgr); handled {
		return err
	}
	gen, err := mgr.StartProxyRotation()
	switch {
	case errors.Is(err, cluster.ErrRotationRefused):
		return response.Fail(c, http.StatusConflict, response.ErrClusterRotationRefused, err.Error())
	case err != nil:
		return clusterErrResponse(c, err)
	}
	return response.Accepted(c, map[string]interface{}{
		"generation": gen,
		"message":    "Every manager takes the new internal proxy secret, then all switch to it: follow it with GET /api/v1/cluster/rotation",
	})
}

// RotateAgentKeys (POST /cluster/rotate/agent-keys) starts giving every agent
// a new internal-proxy key and answers at once (202): each agent takes its
// key at its next heartbeat, an offline one once it is back, and the managers
// sign with it only once the agent says it holds it — GET /cluster/rotation
// follows it. A follower forwards it to the leader. Refused (409), nothing
// having changed, while a manager or an online agent cannot follow.
func (h *Handler) RotateAgentKeys(c echo.Context) error {
	mgr := h.getManager()
	if mgr == nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "Cluster not configured")
	}
	if handled, err := middleware.ProxyToLeader(c, mgr); handled {
		return err
	}
	gen, err := mgr.StartAgentKeyRotation()
	switch {
	case errors.Is(err, cluster.ErrRotationRefused):
		return response.Fail(c, http.StatusConflict, response.ErrClusterRotationRefused, err.Error())
	case err != nil:
		return clusterErrResponse(c, err)
	}
	return response.Accepted(c, map[string]interface{}{
		"generation": gen,
		"message":    "Every agent moves to a new key at its next heartbeat, an offline one once it is back: follow it with GET /api/v1/cluster/rotation",
	})
}
