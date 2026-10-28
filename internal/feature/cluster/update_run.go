package featurecluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/paneltls"
	"github.com/svrforum/SFPanel/internal/release"
)

// updateEnv is what a cluster update needs from the cluster, so its order and
// its decisions can be tested without one.
type updateEnv interface {
	IsLeader() bool
	Trigger(ctx context.Context, id string, body []byte) (status int, resp []byte, err error)
	Version(ctx context.Context, id string) (string, error)
	TransferTo(id string) error
	SelfUpdate(body []byte) (status int, resp []byte, err error)
	Sleep(d time.Duration)
}

const (
	// updateBackTimeout is how long a node may take, once its download is
	// done, to answer on the new version: the restart, the rollback
	// watchdog's ninety seconds, and a boot.
	updateBackTimeout = 4 * time.Minute
	updateBackPoll    = 5 * time.Second
)

// firstSSEError returns the message of the first SSE event in body whose step
// is "error", or "". A node's update answers 200 before it streams, so a
// checksum, signature or download failure is only in the stream.
func firstSSEError(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		data, ok := strings.CutPrefix(strings.TrimSpace(line), "data:")
		if !ok {
			continue
		}
		var ev struct{ Step, Message string }
		if json.Unmarshal([]byte(strings.TrimSpace(data)), &ev) == nil && ev.Step == "error" {
			if ev.Message == "" {
				return "the update failed"
			}
			return ev.Message
		}
	}
	return ""
}

// updateFailure says why an answer to an update request is a failure, or ""
// when it is not.
func updateFailure(status int, body []byte, err error) string {
	switch {
	case err != nil:
		return "request failed: " + err.Error()
	case status >= 400:
		msg := fmt.Sprintf("HTTP %d", status)
		if detail := response.SanitizeOutput(strings.TrimSpace(string(body))); detail != "" {
			msg += ": " + detail
		}
		// The leader sends no credential; the target signs its own loopback
		// hop, and one too old to do that answers 401/403.
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			msg += " — the target node is likely too old to authenticate the update relay; update it locally (SSH + install.sh) for large version jumps, then it can re-join cluster updates"
		}
		return msg
	}
	return firstSSEError(body)
}

func upToDate(body []byte) bool { return bytes.Contains(body, []byte(`"up_to_date"`)) }

// updateOne updates one node and waits for it to answer on the target
// version. It reports in emit and returns the reason it failed, or nil.
func updateOne(env updateEnv, n updateNode, body []byte, target string, emit func(map[string]interface{})) error {
	say := func(step, msg string) {
		emit(map[string]interface{}{"node_id": n.ID, "node_name": n.Name, "step": step, "message": msg})
	}
	say("updating", "Starting update...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	status, resp, err := env.Trigger(ctx, n.ID, body)
	cancel()
	if msg := updateFailure(status, resp, err); msg != "" {
		say("error", "Update failed: "+msg)
		return errors.New(msg)
	}
	if upToDate(resp) {
		// A node from before pinned releases looks the release up itself,
		// and right after one is published that can still name the version
		// it runs: the answer is believed only from a node on the target.
		if !sameVersion(n.Version, target) {
			vctx, vcancel := context.WithTimeout(context.Background(), 10*time.Second)
			v, verr := env.Version(vctx, n.ID)
			vcancel()
			if verr != nil || !sameVersion(v, target) {
				msg := fmt.Sprintf("answered up to date but runs v%s: it does not take the release this update pins; update it locally (SSH + install.sh)", strings.TrimPrefix(v, "v"))
				if verr != nil {
					msg = "answered up to date, and its version could not be read: " + verr.Error()
				}
				say("error", "Update failed: "+msg)
				return errors.New(msg)
			}
		}
		say("online", "Already up to date")
		return nil
	}
	say("waiting", "Waiting for the node to restart on v"+strings.TrimPrefix(target, "v")+"...")
	// last is what the latest poll heard: the old process still answers for
	// a moment after its update returns, and a node that then stays down
	// does not answer on that version.
	last := ""
	for waited := time.Duration(0); waited < updateBackTimeout; waited += updateBackPoll {
		env.Sleep(updateBackPoll)
		vctx, vcancel := context.WithTimeout(context.Background(), 10*time.Second)
		v, verr := env.Version(vctx, n.ID)
		vcancel()
		if verr != nil {
			last = ""
			continue // restarting
		}
		last = v
		if sameVersion(v, target) {
			say("online", "Back on v"+strings.TrimPrefix(v, "v"))
			return nil
		}
	}
	msg := fmt.Sprintf("did not answer on v%s within %v", strings.TrimPrefix(target, "v"), updateBackTimeout)
	if last != "" {
		msg += fmt.Sprintf("; it answers on v%s: the update was rolled back, or the node never restarted", strings.TrimPrefix(last, "v"))
	}
	say("error", "Update failed: "+msg)
	return errors.New(msg)
}

const leadershipMoved = "Leadership moved during the update: run it again on the new leader to finish"

// runUpdatePlan runs a cluster update in plan's order and reports through
// emit. Agents go wave by wave and a wave with a failure ends the run, as
// does any manager's failure, before anything else restarts. The leader goes
// last: it hands leadership to another manager first, and with one there to
// take it does not update itself until that worked — restarting as leader
// would leave the managers without one for the whole restart.
func runUpdatePlan(env updateEnv, plan updatePlan, rel *release.Release, mode string, emit func(map[string]interface{})) (updated, failed int) {
	body, _ := json.Marshal(map[string]interface{}{"release": rel})
	target := rel.TagName
	total := len(plan.Managers)
	for _, w := range plan.Waves {
		total += len(w)
	}
	if plan.Leader != nil {
		total++
	}
	emit(map[string]interface{}{"overall": "started", "mode": mode, "total_nodes": total, "waves": len(plan.Waves), "target": target})
	for _, n := range plan.Skipped {
		emit(map[string]interface{}{"node_id": n.ID, "node_name": n.Name, "step": "skipped", "message": "Node is offline"})
	}
	for _, n := range plan.Current {
		emit(map[string]interface{}{"node_id": n.ID, "node_name": n.Name, "step": "online", "message": "Already on " + target})
	}
	stop := func(msg string) (int, int) {
		emit(map[string]interface{}{"overall": "error", "message": msg, "updated": updated, "failed": failed})
		return updated, failed
	}

	for i, wave := range plan.Waves {
		if !env.IsLeader() {
			return stop(leadershipMoved)
		}
		names := make([]string, len(wave))
		for j, n := range wave {
			names[j] = n.Name
		}
		emit(map[string]interface{}{"wave": i + 1, "waves": len(plan.Waves), "nodes": names})
		errs := make([]error, len(wave))
		var wg sync.WaitGroup
		for j, n := range wave {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[j] = updateOne(env, n, body, target, emit)
			}()
		}
		wg.Wait()
		var bad []string
		for j, err := range errs {
			if err != nil {
				failed++
				bad = append(bad, wave[j].Name)
			} else {
				updated++
			}
		}
		if len(bad) > 0 {
			return stop(fmt.Sprintf("Wave %d of %d failed (%s): the update stopped, and no manager was updated", i+1, len(plan.Waves), strings.Join(bad, ", ")))
		}
	}
	for _, n := range plan.Managers {
		if !env.IsLeader() {
			return stop(leadershipMoved)
		}
		if err := updateOne(env, n, body, target, emit); err != nil {
			failed++
			return stop("Rolling update stopped: " + n.Name + " failed")
		}
		updated++
	}

	if plan.Leader == nil {
		emit(map[string]interface{}{"overall": "complete", "updated": updated, "failed": failed, "leader_restarting": false})
		return updated, failed
	}
	leader := *plan.Leader
	if !env.IsLeader() {
		return stop(leadershipMoved)
	}
	// Any other manager voter can take over: one this run updated, and one
	// on the target already — after a run that stopped because the handover
	// failed, that is every one of them. The ones just updated go first:
	// they have just answered.
	var voters []updateNode
	for _, m := range append(append([]updateNode(nil), plan.Managers...), plan.Current...) {
		if m.Manager && m.Voter && m.ID != leader.ID {
			voters = append(voters, m)
		}
	}
	if len(voters) > 0 {
		var reasons []string
		handed := false
		for _, m := range voters {
			emit(map[string]interface{}{"node_id": leader.ID, "node_name": leader.Name, "step": "transfer", "message": "Transferring leadership to " + m.Name})
			if err := env.TransferTo(m.ID); err != nil {
				reasons = append(reasons, m.Name+": "+err.Error())
				continue
			}
			handed = true
			break
		}
		if !handed {
			return stop("Leadership could not be handed over (" + strings.Join(reasons, "; ") + "): the leader was not updated")
		}
		env.Sleep(2 * time.Second)
	}
	emit(map[string]interface{}{"node_id": leader.ID, "node_name": leader.Name, "step": "updating", "message": "Updating this node..."})
	status, resp, err := env.SelfUpdate(body)
	if msg := updateFailure(status, resp, err); msg != "" {
		failed++
		emit(map[string]interface{}{"node_id": leader.ID, "node_name": leader.Name, "step": "error", "message": "Update failed: " + msg})
		return stop("The leader's own update failed: " + msg)
	}
	updated++
	restarting := !upToDate(resp)
	emit(map[string]interface{}{"overall": "complete", "updated": updated, "failed": failed, "leader_restarting": restarting})
	return updated, failed
}

// clusterUpdateEnv runs a cluster update against the real cluster.
type clusterUpdateEnv struct {
	mgr  *cluster.Manager
	self paneltls.Self
}

func (e clusterUpdateEnv) IsLeader() bool { return e.mgr.IsLeader() }

// call proxies one request to node id. The node is read from the live FSM,
// not the plan: an update runs for minutes, and a node may be removed or its
// address changed meanwhile.
func (e clusterUpdateEnv) call(ctx context.Context, id, method, path string, body []byte) (int, []byte, error) {
	node := e.mgr.GetRaft().GetFSM().GetNode(id)
	if node == nil {
		return 0, nil, errors.New("the node is no longer in the cluster")
	}
	client, err := e.mgr.GetConnPool().Get(node.GRPCAddress)
	if err != nil {
		return 0, nil, fmt.Errorf("connect: %w", err)
	}
	// Unsigned: the target's gRPC server drops any internal-proxy header and
	// signs its own loopback hop.
	resp, err := client.ProxyRequest(ctx, &pb.APIRequest{Method: method, Path: path, Body: body})
	if err != nil {
		return 0, nil, err
	}
	return int(resp.StatusCode), resp.Body, nil
}

func (e clusterUpdateEnv) Trigger(ctx context.Context, id string, body []byte) (int, []byte, error) {
	return e.call(ctx, id, http.MethodPost, orchestratedUpdatePath, body)
}

// Version asks the node itself: its heartbeats may go to another manager, or
// none, while it restarts, and the health route answers before them.
func (e clusterUpdateEnv) Version(ctx context.Context, id string) (string, error) {
	status, body, err := e.call(ctx, id, http.MethodGet, "/api/v1/health", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("health answered HTTP %d", status)
	}
	var r struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", err
	}
	return r.Data.Version, nil
}

func (e clusterUpdateEnv) TransferTo(id string) error { return e.mgr.GetRaft().TransferLeadership(id) }

// SelfUpdate asks this node's own panel to update. Raft keeps running through
// the download: the restart that follows a successful update stops it
// through the usual shutdown, and an update that fails leaves the node as it
// was, still in the cluster.
func (e clusterUpdateEnv) SelfUpdate(body []byte) (int, []byte, error) {
	client, err := e.self.HTTPClient(0)
	if err != nil {
		return 0, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.self.URL(orchestratedUpdatePath), bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if sig := auth.SignProxyRequestV2(http.MethodPost, orchestratedUpdatePath); sig != "" {
		req.Header.Set(auth.InternalProxyHeaderV2, sig)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		// The stream ends when the update does, so one that breaks off
		// says nothing of how it ended: the five minutes above running out
		// cancels a slow download partway.
		return resp.StatusCode, out, fmt.Errorf("its stream broke off before the update ended: %w", err)
	}
	return resp.StatusCode, out, nil
}

func (clusterUpdateEnv) Sleep(d time.Duration) { time.Sleep(d) }
