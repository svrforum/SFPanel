package featurecluster

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// Busy is what is under way on this leader as a start is judged: a cluster
// update, a CA rotation, and a conversion from the moment it is admitted
// until it has been answered.
type Busy struct{ UpdateRunning, CARotation, Converting, Promoting bool }

// StartGate runs claim under startMu with what is under way filled in, and
// returns its refusal. A group task run is judged and recorded only here:
// whatever claim records — the run, in replicated state — is what the next
// starter to take startMu (an update, a CA rotation, a conversion, a
// promotion, another run) reads through SetTaskRunsActive's function. Two
// locks would let a run and an update each pass their check before the other
// began. claim must not call back into this handler's starters.
func (h *Handler) StartGate(claim func(b Busy) error) error {
	h.startMu.Lock()
	defer h.startMu.Unlock()
	b := Busy{UpdateRunning: h.updateRunning.Load(), Converting: h.converting.Load(), Promoting: h.promoting.Load()}
	if mgr := h.getManager(); mgr != nil {
		b.CARotation = mgr.CARotationActive()
	}
	return claim(b)
}

// SetTaskRunsActive gives the gate the task runs under way: fn returns the ID
// of one, or "" with none. Until it is set nothing is under way. A cluster
// update, a CA rotation, a conversion and a promotion each ask it under
// startMu as they start, and wait for the run it names.
func (h *Handler) SetTaskRunsActive(fn func() (runID string)) {
	h.startMu.Lock()
	h.taskRunsActive = fn
	h.startMu.Unlock()
}

// SetAgentInstallActive gives the handler the agent installs under way: fn
// reports whether one runs on this leader. Until it is set none does. While
// one runs the leader keeps its role (leadershipHold) and a manual leadership
// transfer is refused.
func (h *Handler) SetAgentInstallActive(fn func() bool) {
	h.agentInstallActive.Store(&fn)
}

// agentInstallRunning reports whether an agent install is under way.
func (h *Handler) agentInstallRunning() bool {
	fn := h.agentInstallActive.Load()
	return fn != nil && *fn != nil && (*fn)()
}

// taskRunActive is the task run under way, or "". The caller holds startMu.
func (h *Handler) taskRunActive() string {
	if h.taskRunsActive == nil {
		return ""
	}
	return h.taskRunsActive()
}

// failTaskRunActive refuses what would restart or re-key nodes under the
// steps of run runID.
func failTaskRunActive(c echo.Context, what, runID string) error {
	return response.FailWith(c, http.StatusConflict, response.ErrTaskRunActive,
		"A group task run is under way: start the "+what+" once run "+runID+" has finished",
		map[string]string{"run_id": runID})
}
