package agentinstall

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
)

// online is one round of the three checks that an agent is online: the
// leader hears its heartbeat at the version installed, the leader relays to
// it over its cluster port, and the leader reaches its panel port over
// HTTPS. Only the third proves the panel port and its allow-list.
type online struct {
	heard, live bool
	version     string
	relay       int
	relayErr    error
	panel       int
	panelErr    error
}

func (o online) heartbeatOK(version string) bool {
	return o.heard && o.live && strings.TrimPrefix(o.version, "v") == version
}

func (o online) ok(version string) bool {
	return o.heartbeatOK(version) && o.relay == http.StatusOK && o.panel == http.StatusOK
}

func (o online) String() string {
	hb := "no heartbeat"
	switch {
	case o.heard && o.live:
		hb = "heartbeat online at " + o.version
	case o.heard:
		hb = "heartbeat offline"
	}
	return fmt.Sprintf("%s; relay %s; panel %s", hb, callResult(o.relay, o.relayErr), callResult(o.panel, o.panelErr))
}

func callResult(status int, err error) string {
	if err != nil {
		return "failed: " + err.Error()
	}
	return fmt.Sprintf("%d", status)
}

// check runs one round of the checks against node id.
func (r *row) check(ctx context.Context, id string) online {
	env, call := r.s.env, r.s.e.t.call
	var o online
	o.heard, o.live, o.version = env.Heartbeat(id)
	cctx, cancel := context.WithTimeout(ctx, call)
	o.relay, o.relayErr = env.ProxyHealth(cctx, id)
	cancel()
	cctx, cancel = context.WithTimeout(ctx, call)
	o.panel, o.panelErr = env.PanelHealth(cctx, id)
	cancel()
	return o
}

// verify is phase 4: the three checks every verifyEvery until they all
// pass or verifyLimit passes, then the group label merged into the node's
// labels. A timeout names the likelier way the network is cut.
func (r *row) verify(ctx context.Context) error {
	s, log := r.s, r.logs[phaseVerify]
	s.mu.Lock()
	id := r.rec.NodeID
	s.mu.Unlock()
	t := s.e.t
	deadline := time.Now().Add(t.verifyLimit)
	last := ""
	for {
		o := r.check(ctx, id)
		if line := o.String(); line != last {
			log.printf("%s", line)
			last = line
		}
		if o.ok(s.version) {
			return r.label(id)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return r.offline(id, o)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(t.verifyEvery):
		}
	}
}

// offline is the reason for an agent that registered and did not come
// online: no heartbeat says it cannot reach the managers' cluster port; a
// heartbeat at another version says the install is not the one it ran; a
// heartbeat with a relay failing says the managers cannot reach its
// cluster or panel port.
func (r *row) offline(id string, o online) error {
	switch {
	case !o.heard || !o.live:
		_, port, _ := net.SplitHostPort(r.joinAddr)
		return fail(response.ErrAgentInstallOffline, "the agent's heartbeat never reached the leader",
			"cause", "agent_to_managers", "port", port)
	case !o.heartbeatOK(r.s.version):
		return fail(response.ErrAgentInstallOffline, "the agent runs another version than the one installed",
			"cause", "version", "version", o.version)
	}
	var ports []string
	for _, n := range r.s.env.Nodes() {
		if n.ID != id {
			continue
		}
		for _, a := range []string{n.GRPCAddress, n.APIAddress} {
			if _, p, err := net.SplitHostPort(a); err == nil {
				ports = append(ports, p)
			}
		}
	}
	failed := []string{}
	if o.relay != http.StatusOK {
		failed = append(failed, "relay")
	}
	if o.panel != http.StatusOK {
		failed = append(failed, "panel")
	}
	return fail(response.ErrAgentInstallOffline, "the leader's calls to the agent fail: "+o.String(),
		"cause", "managers_to_agent", "ports", strings.Join(ports, ","), "failed", strings.Join(failed, ","))
}

// label merges the row's group into the node's labels: UpdateNodeLabels
// replaces them all.
func (r *row) label(id string) error {
	if r.srv.Group == "" {
		return nil
	}
	labels := map[string]string{}
	for _, n := range r.s.env.Nodes() {
		if n.ID == id {
			labels = maps.Clone(n.Labels)
			if labels == nil {
				labels = map[string]string{}
			}
		}
	}
	labels[groupLabel] = r.srv.Group
	if err := r.s.env.UpdateNodeLabels(id, labels); err != nil {
		return fail(response.ErrAgentInstallFailed, err.Error(), "step", "labels")
	}
	r.logs[phaseVerify].printf("labelled group=%s", r.srv.Group)
	return nil
}

// Recheck runs row key's online checks again: a row that registered and
// failed them. The install's run is under way again until they end.
func (e *Engine) Recheck(runID, key string) error {
	s, r, err := e.row(runID, key)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.stopped || s.ctx.Err() != nil || r.rec.Status != cluster.RunNodeFailed || r.rec.NodeID == "" || r.reverted || r.verifying {
		s.mu.Unlock()
		return notWaiting("This server has no registered agent to check again")
	}
	r.verifying = true
	s.running++
	s.head.Status, s.head.EndedAt = cluster.RunRunning, time.Time{}
	head := s.head
	s.mu.Unlock()
	s.putHead(head)
	r.update(func(rec *cluster.RunNodeRecord) {
		rec.Status, rec.Reason, rec.ReasonDetails = cluster.RunNodeRunning, "", nil
	})
	go func() {
		defer s.rowDone()
		err := r.phase(s.ctx, phaseVerify, r.verify)
		r.update(func(rec *cluster.RunNodeRecord) {
			var f *rowFail
			switch {
			case err == nil:
				rec.Status = cluster.RunNodeSucceeded
			case errors.As(err, &f):
				rec.Status, rec.Reason, rec.ReasonDetails = cluster.RunNodeFailed, f.code, maps.Clone(f.details)
			default:
				stopRecord(rec)
			}
		})
		s.mu.Lock()
		r.verifying = false
		s.mu.Unlock()
	}()
	return nil
}

// Revert takes back row key's agent: one that registered and then failed,
// or that the operator stopped. The node is removed from the cluster, and
// while the credentials last, a server that had no SFPanel before is purged
// of it in the background (install.sh uninstall --purge, then an
// /opt/stacks it did not have). A server that ran a standalone panel
// before is not touched: its local account went when it booted as an
// agent. Once the credentials are gone (always so after a stop) the node is
// removed all the same and the row says so (AGENT_INSTALL_CREDENTIALS_GONE):
// the screen shows how to clean up by hand.
func (e *Engine) Revert(runID, key, by string) error {
	s, r, err := e.row(runID, key)
	if err != nil {
		return err
	}
	s.mu.Lock()
	ended := r.rec.Status == cluster.RunNodeFailed || r.rec.Status == cluster.RunNodeStopped
	if !ended || r.rec.NodeID == "" || r.reverted || r.verifying {
		s.mu.Unlock()
		return notWaiting("This server has no registered agent to revert")
	}
	r.reverted = true
	held := s.credentialsLocked()
	id := r.rec.NodeID
	head := s.head
	s.mu.Unlock()
	if !held {
		s.putHead(head)
	}

	if err := s.env.RemoveNode(id); err != nil && !errors.Is(err, cluster.ErrNodeNotFound) {
		s.mu.Lock()
		r.reverted = false
		s.mu.Unlock()
		return err
	}
	s.env.Audit(by, http.MethodPost, auditPath(s.id)+"#revert:"+key, http.StatusOK)
	r.logs[phaseVerify].printf("node %s removed from the cluster by %s", id, by)

	switch {
	case !held:
		r.update(func(rec *cluster.RunNodeRecord) {
			rec.Reason = response.ErrAgentInstallCredentialsGone
			rec.ReasonDetails = mergeDetails(rec.ReasonDetails, map[string]string{"reverted": "removed"})
		})
		return nil
	case r.sfpanelBefore:
		r.update(func(rec *cluster.RunNodeRecord) {
			rec.ReasonDetails = mergeDetails(rec.ReasonDetails, map[string]string{"reverted": "removed", "standalone": "1"})
		})
		s.dropUnused()
		return nil
	}
	r.update(func(rec *cluster.RunNodeRecord) {
		rec.ReasonDetails = mergeDetails(rec.ReasonDetails, map[string]string{"reverted": "removing"})
	})
	go r.purgeAfter()
	return nil
}

// purgeAfter purges the server of the agent a revert removed: a new
// connection and a new private directory holding install.sh, which runs
// uninstall --purge.
func (r *row) purgeAfter() {
	ctx, cancel := context.WithTimeout(r.s.ctx, r.s.e.t.cleanup+r.s.e.t.checkWait)
	defer cancel()
	log := r.logs[phaseVerify]
	result := "purged"
	err := func() error {
		if err := r.dial(ctx, log); err != nil {
			return err
		}
		defer r.closeTarget()
		if err := r.mktemp(ctx); err != nil {
			return err
		}
		defer r.removeTemp(ctx)
		if err := r.tgt.Upload(ctx, r.tmp+"/install.sh", 0o600, r.s.env.InstallScript()); err != nil {
			return err
		}
		if !r.purge(ctx, r.tmp, log) {
			return errors.New("uninstall --purge failed")
		}
		return nil
	}()
	if err != nil {
		log.printf("cleaning the server failed: %v", err)
		result = "cleanup_failed"
	}
	r.update(func(rec *cluster.RunNodeRecord) {
		rec.ReasonDetails = mergeDetails(rec.ReasonDetails, map[string]string{"reverted": result})
	})
	r.s.dropUnused()
}
