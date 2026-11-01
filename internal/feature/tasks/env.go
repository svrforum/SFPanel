package featuretasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/paneltls"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// runStore is the part of the task store a run reads and writes: the
// cluster's replicated state, on the leader.
type runStore interface {
	TaskRevision(sha string) (string, error)
	GetRun(id string) (cluster.RunRecord, map[string]cluster.RunNodeRecord, error)
	ListRuns() ([]cluster.RunRecord, error)
	PutRun(r cluster.RunRecord) error
	PutRunNode(runID, nodeID string, rec cluster.RunNodeRecord) error
	PruneRuns(keep int) error
}

// taskStore is the task store as the API reads and writes it: the run
// store, the definitions and revisions, and whether every manager can keep
// them. Writes are the leader's; reads answer from this manager's own FSM.
type taskStore interface {
	runStore
	PutTask(t cluster.StoredTask) error
	GetTask(id string) (cluster.StoredTask, error)
	ListTasks() ([]cluster.StoredTask, error)
	DeleteTask(id string) error
	PutTaskRevision(yaml string) (string, error)
	// TasksReady names the managers a run waits for, each with why.
	TasksReady() []string
}

// runEnv is what planning and running a task need from the cluster, so the
// order and every decision can be tested without one.
type runEnv interface {
	IsLeader() bool
	// CaughtUp reports whether this node leads with an FSM that holds every
	// committed entry: only then are the runs it reads all the runs.
	CaughtUp() bool
	// Done is closed when the cluster manager stops.
	Done() <-chan struct{}
	LocalID() string
	Nodes() []cluster.Node
	// Health is LiveHealth: what an operator starts goes by it.
	Health() map[string]cluster.NodeStatus
	// Capable reports whether node id runs task jobs (CapTasks).
	Capable(id string) bool
	// Unreachable reports whether this leader's fresh probe found agent id
	// out of reach.
	Unreachable(id string) bool
	// StartJob, GetJob, CancelJob and Check call a node's task-job routes.
	// A node's refusal is a *jobRefusal; any other error is the call's own.
	// None sets a time limit: the caller's context does.
	StartJob(ctx context.Context, nodeID string, spec tasks.JobSpec, username string) (tasks.JobView, error)
	GetJob(ctx context.Context, nodeID, jobID string, skip int64) (tasks.JobView, error)
	CancelJob(ctx context.Context, nodeID, jobID string, force bool) error
	Check(ctx context.Context, nodeID string, specs []tasks.JobSpec) ([]tasks.CheckVerdict, error)
	Store() runStore
	// Tasks is the whole task store, for the API.
	Tasks() taskStore
	// HandOver hands the leadership to another manager voter and names it,
	// or names none and does nothing when there is none to take it.
	HandOver() (target string, err error)
	Now() time.Time
	Sleep(d time.Duration)
}

// jobRefusal is a node's answer refusing a task-job request: the response
// envelope's error, with the HTTP status.
type jobRefusal struct {
	Status  int
	Code    string
	Message string
	Details map[string]string
}

func (e *jobRefusal) Error() string { return e.Code + ": " + e.Message }

// refusalCode is the code of the node's refusal err carries, or "".
func refusalCode(err error) string {
	var ref *jobRefusal
	if errors.As(err, &ref) {
		return ref.Code
	}
	return ""
}

// The node's task-job routes (internal/feature/taskjob).
const (
	taskJobsPath     = "/api/v1/task-jobs"
	taskJobCheckPath = "/api/v1/task-jobs/check"
)

// clusterRunEnv runs a task against the real cluster, as its leader.
type clusterRunEnv struct {
	mgr  *cluster.Manager
	self paneltls.Self
}

func (e clusterRunEnv) IsLeader() bool  { return e.mgr.IsLeader() }
func (e clusterRunEnv) LocalID() string { return e.mgr.LocalNodeID() }

// caughtUpWait bounds the wait to enqueue the barrier a new leader's first
// look takes (caughtUpLeader), as the other leader-side loops do.
const caughtUpWait = 2 * time.Second

func (e clusterRunEnv) CaughtUp() bool { return e.mgr.CaughtUpLeader(caughtUpWait) }

// Done is the heartbeat's: the manager stops it first as it shuts down.
func (e clusterRunEnv) Done() <-chan struct{} {
	if hb := e.mgr.GetHeartbeat(); hb != nil {
		return hb.Done()
	}
	return nil
}

func (e clusterRunEnv) Nodes() []cluster.Node {
	nodes := e.mgr.GetNodes()
	out := make([]cluster.Node, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, *n)
	}
	return out
}

func (e clusterRunEnv) Health() map[string]cluster.NodeStatus {
	return e.mgr.GetHeartbeat().LiveHealth()
}

// Capable is true for this node, whose own code is what runs the task, and
// for a node whose last heartbeat said CapTasks.
func (e clusterRunEnv) Capable(id string) bool {
	if id == e.mgr.LocalNodeID() {
		return true
	}
	met := e.mgr.GetHeartbeat().GetMetrics(id)
	return met != nil && slices.Contains(met.Capabilities, cluster.CapTasks)
}

func (e clusterRunEnv) Unreachable(id string) bool { return e.mgr.Unreachable(id) }

func (e clusterRunEnv) Store() runStore  { return e.mgr }
func (e clusterRunEnv) Tasks() taskStore { return e.mgr }

// HandOver hands the leadership to the manager voter pickTransferTarget
// takes: online, and reaching the most agents.
func (e clusterRunEnv) HandOver() (string, error) {
	target := e.mgr.PickTransferTarget()
	if target == "" {
		return "", nil
	}
	if err := e.mgr.GetRaft().TransferLeadership(target); err != nil {
		return target, err
	}
	return target, nil
}

func (clusterRunEnv) Now() time.Time        { return time.Now() }
func (clusterRunEnv) Sleep(d time.Duration) { time.Sleep(d) }

func (e clusterRunEnv) StartJob(ctx context.Context, nodeID string, spec tasks.JobSpec, username string) (tasks.JobView, error) {
	body, err := json.Marshal(spec)
	if err != nil {
		return tasks.JobView{}, err
	}
	var v tasks.JobView
	err = e.call(ctx, nodeID, http.MethodPost, taskJobsPath, body, username, &v)
	return v, err
}

func (e clusterRunEnv) GetJob(ctx context.Context, nodeID, jobID string, skip int64) (tasks.JobView, error) {
	var v tasks.JobView
	path := taskJobsPath + "/" + jobID + "?skip=" + strconv.FormatInt(skip, 10)
	err := e.call(ctx, nodeID, http.MethodGet, path, nil, "", &v)
	return v, err
}

func (e clusterRunEnv) CancelJob(ctx context.Context, nodeID, jobID string, force bool) error {
	body, err := json.Marshal(map[string]bool{"force": force})
	if err != nil {
		return err
	}
	return e.call(ctx, nodeID, http.MethodPost, taskJobsPath+"/"+jobID+"/cancel", body, "", nil)
}

func (e clusterRunEnv) Check(ctx context.Context, nodeID string, specs []tasks.JobSpec) ([]tasks.CheckVerdict, error) {
	body, err := json.Marshal(specs)
	if err != nil {
		return nil, err
	}
	var out []tasks.CheckVerdict
	err = e.call(ctx, nodeID, http.MethodPost, taskJobCheckPath, body, "", &out)
	return out, err
}

// call sends one task-job request to node nodeID and decodes the envelope's
// data into out (when not nil). Another node is called through ProxyToNode,
// which carries the operator's name; this node through its own panel port,
// signed as the gRPC server signs the loopback hop of a proxied request.
func (e clusterRunEnv) call(ctx context.Context, nodeID, method, path string, body []byte, username string, out any) error {
	var status int
	var resp []byte
	var err error
	if nodeID == e.mgr.LocalNodeID() {
		status, resp, err = e.selfCall(ctx, method, path, body, username)
	} else {
		status, resp, err = e.mgr.ProxyToNode(ctx, nodeID, method, path, body, username)
	}
	if err != nil {
		return err
	}
	return decodeEnvelope(status, resp, out)
}

// decodeEnvelope reads a {success, data} / {success:false, error} answer.
// A refusal is a *jobRefusal; an answer that is not the envelope is an
// error naming the status.
func decodeEnvelope(status int, body []byte, out any) error {
	var env struct {
		Success bool                `json:"success"`
		Data    json.RawMessage     `json:"data"`
		Error   *response.ErrorBody `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("HTTP %d: the node's answer is not readable: %w", status, err)
	}
	if !env.Success || status >= 400 {
		if env.Error == nil {
			return fmt.Errorf("HTTP %d with no reason", status)
		}
		return &jobRefusal{Status: status, Code: env.Error.Code, Message: env.Error.Message, Details: env.Error.Details}
	}
	if out == nil || len(env.Data) == 0 {
		return nil
	}
	return json.Unmarshal(env.Data, out)
}

// selfCall calls this node's own panel over loopback (paneltls.Self), the
// request signed for its method and full URI and carrying the operator's
// name, as the gRPC server's own loopback hop does for a proxied request.
func (e clusterRunEnv) selfCall(ctx context.Context, method, path string, body []byte, username string) (int, []byte, error) {
	client, err := e.self.HTTPClient(0)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, e.self.URL(path), bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	if username != "" {
		req.Header.Set("X-SFPanel-Original-User", username)
	}
	if sig := auth.SignProxyRequestV2(method, path); sig != "" {
		req.Header.Set(auth.InternalProxyHeaderV2, sig)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, out, nil
}
