package featurecluster

import (
	"errors"
	"fmt"
	"net/http"

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
