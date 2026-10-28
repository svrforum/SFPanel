package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/common/safe"
	"github.com/svrforum/SFPanel/internal/config"
)

// AgentStateFile is the file in the cluster data directory where an agent
// keeps its AgentState.
const AgentStateFile = "agent-state.json"

// AgentState is what an agent keeps between runs.
type AgentState struct {
	// Managers are whom the agent heartbeats to and takes requests from:
	// cluster.managers until a pong from the leader names them.
	Managers []config.ManagerRef `json:"managers"`
	Leader   string              `json:"leader,omitempty"` // gRPC address
	// Removed is set once the leader has said the agent is not a member. It
	// stays set until `sfpanel agent reset`: nothing here undoes it.
	Removed bool `json:"removed,omitempty"`
}

// LoadAgentState reads the state at path. Before the first save there is no
// file, and the agent starts from seed, the managers it registered with.
func LoadAgentState(path string, seed []config.ManagerRef) (*AgentState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &AgentState{Managers: slices.Clone(seed)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read agent state: %w", err)
	}
	var s AgentState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse agent state %s: %w", path, err)
	}
	return &s, nil
}

// Save writes the state to path, 0600.
func (s *AgentState) Save(path string) error {
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode agent state: %w", err)
	}
	return replaceFile(path, data)
}

// replaceFile replaces path with data, 0600, through a temporary file of its
// own in the same directory — not config.AtomicWriteFile, whose fixed temp
// name two writers can share. The data is synced before the rename: the
// agent's state and key files are its only copy of what they hold (a
// removal, the key its managers sign with), and a crash must leave the old
// file or the new one, not an empty one the agent cannot boot from.
func replaceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp") // 0600
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op once the rename consumes it
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// AgentLinkConfig is what an AgentLink is built from. It is separate so the
// caller can pass it by value: AgentLink holds mutexes, which go vet will not
// let be copied.
type AgentLinkConfig struct {
	NodeID    string
	TLS       *TLSManager
	StatePath string
	Version   string
	Interval  time.Duration                                   // between pings, and before retrying; 30s in production
	Collect   func() (cpu, mem, disk float64, containers int) // nil: pings carry no metrics
	PanelCA   func() ([]byte, error)                          // nil: nothing to report
}

// AgentLink is an agent's tie to its managers. It heartbeats to the leader,
// follows the leader when it moves, keeps the manager list current from the
// leader's pongs, and stops for good once the leader says the agent was
// removed.
type AgentLink struct {
	AgentLinkConfig

	// saveMu orders the writes of the state file. mu is not held across a
	// write: the manager list is read on every request the agent's gRPC
	// server and panel take, and those must not wait on the disk.
	saveMu    sync.Mutex
	mu        sync.Mutex // guards state and onRemoved
	state     *AgentState
	onRemoved func()
}

// NewAgentLink loads the agent's state, from seed if none is saved yet.
func NewAgentLink(cfg AgentLinkConfig, seed []config.ManagerRef) (*AgentLink, error) {
	st, err := LoadAgentState(cfg.StatePath, seed)
	if err != nil {
		return nil, err
	}
	return &AgentLink{AgentLinkConfig: cfg, state: st}, nil
}

// SetOnRemoved sets what runs once the leader has removed the agent, after
// the removal is saved and before Run returns. Set it before Run.
func (l *AgentLink) SetOnRemoved(f func()) {
	l.mu.Lock()
	l.onRemoved = f
	l.mu.Unlock()
}

// ManagerIDs are the node IDs of the managers the agent takes requests from.
func (l *AgentLink) ManagerIDs() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	ids := make([]string, 0, len(l.state.Managers))
	for _, m := range l.state.Managers {
		ids = append(ids, m.ID)
	}
	return ids
}

// ManagerHosts are the hosts of the managers' gRPC addresses, each once.
func (l *AgentLink) ManagerHosts() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var hosts []string
	for _, m := range l.state.Managers {
		host, _, err := net.SplitHostPort(m.GRPCAddress)
		if err != nil || host == "" || slices.Contains(hosts, host) {
			continue
		}
		hosts = append(hosts, host)
	}
	return hosts
}

// Removed reports whether the leader has removed the agent.
func (l *AgentLink) Removed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state.Removed
}

func (l *AgentLink) snapshot() AgentState {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := *l.state
	s.Managers = slices.Clone(s.Managers)
	return s
}

// update applies change to a copy of the state. When change reports a
// difference, the copy becomes the state — before it is saved, so a disk
// that refuses the write does not keep the agent on a stale list.
func (l *AgentLink) update(change func(*AgentState) bool) error {
	l.saveMu.Lock()
	defer l.saveMu.Unlock()
	next := l.snapshot()
	if !change(&next) {
		return nil
	}
	l.mu.Lock()
	l.state = &next
	l.mu.Unlock()
	return next.Save(l.StatePath)
}

// nextTargets is where to send heartbeats, in order: the leader the agent
// last heard of, then each manager in list order.
func nextTargets(state AgentState) []string {
	var out []string
	add := func(addr string) {
		if addr != "" && !slices.Contains(out, addr) {
			out = append(out, addr)
		}
	}
	add(state.Leader)
	for _, m := range state.Managers {
		add(m.GRPCAddress)
	}
	return out
}

// removedByLeader reports whether err is the leader saying the agent is not a
// member (notMemberError). Only a leader that has caught up says that; a
// follower that has not seen the agent yet says something else, and must not
// stop an agent for good.
func removedByLeader(err error) bool {
	st, ok := status.FromError(err)
	return ok && st.Code() == codes.PermissionDenied && strings.Contains(st.Message(), NotMemberRemovedMarker)
}

const (
	warnHeartbeat = "heartbeat to a manager failed"
	warnReport    = "could not report the panel CA to the leader"
)

var (
	errNoManagers = errors.New("the agent's state names no manager with a gRPC address")
	// errManagerPredatesAgents is a manager without the agents' heartbeat:
	// its release is older than agents, and every manager must be upgraded.
	errManagerPredatesAgents = errors.New("this manager runs an SFPanel release from before agents; upgrade it")
	// errNoLeaderKnown ends a session with a manager that answers but knows
	// no leader: in an election, cut off from the others, or a leader whose
	// Raft has stopped for its own update.
	errNoLeaderKnown = errors.New("this manager knows no leader right now")
)

// linkRun is what one Run remembers from attempt to attempt.
type linkRun struct {
	reported bool              // the panel CA has reached the leader
	current  string            // the manager the agent last stayed with
	warned   map[string]string // the error last logged, per warning and manager
}

// warn logs err only when it is not what was last logged for the same
// warning and manager: while a manager is down, every attempt fails the same
// way, once an Interval.
func (r *linkRun) warn(what, target string, err error) {
	key := what + "|" + target
	if r.warned[key] == err.Error() {
		return
	}
	r.warned[key] = err.Error()
	slog.Warn(what, "component", "agent", "manager", target, "error", err)
}

// stayedWith records that target answered and the agent stays with it.
func (r *linkRun) stayedWith(target string) {
	delete(r.warned, warnHeartbeat+"|"+target)
	if target != r.current {
		slog.Info("heartbeats go to a new manager", "component", "agent", "manager", target, "previous", r.current)
		r.current = target
	}
}

type endKind int

const (
	endFailed     endKind = iota // the manager could not be reached, or the stream broke
	endRedirected                // a follower named the leader elsewhere
	endRemoved                   // the leader said the agent is not a member
	endStopped                   // ctx ended
)

type attemptEnd struct {
	kind endKind
	err  error
}

func classify(ctx context.Context, err error) attemptEnd {
	switch {
	case ctx.Err() != nil:
		return attemptEnd{kind: endStopped}
	case removedByLeader(err):
		return attemptEnd{endRemoved, err}
	case status.Code(err) == codes.Unimplemented:
		return attemptEnd{endFailed, errManagerPredatesAgents}
	default:
		return attemptEnd{endFailed, err}
	}
}

func (l *AgentLink) interval() time.Duration {
	if l.Interval <= 0 {
		return metricsStreamSendInterval
	}
	return l.Interval
}

// Run heartbeats until ctx ends or the leader removes the agent. For an agent
// already removed it returns at once.
//
// Each attempt goes to one manager, the leader first. A follower that names
// the leader elsewhere is left at once for it; any other failure moves on to
// the next manager after an Interval.
func (l *AgentLink) Run(ctx context.Context) {
	if l.Removed() {
		return
	}
	r := &linkRun{reported: l.PanelCA == nil, warned: map[string]string{}}
	next := 0
	for ctx.Err() == nil {
		targets := nextTargets(l.snapshot())
		if len(targets) == 0 {
			r.warn(warnHeartbeat, "", errNoManagers)
			if !sleepCtx(ctx, l.interval()) {
				return
			}
			continue
		}
		target := targets[next%len(targets)]
		end := l.attempt(ctx, r, target)
		switch end.kind {
		case endStopped:
			return
		case endRemoved:
			l.markRemoved(target)
			return
		case endRedirected:
			// The pong saved the leader's address; nextTargets puts it first.
			next = 0
		default:
			r.warn(warnHeartbeat, target, end.err)
			next++
			if !sleepCtx(ctx, l.interval()) {
				return
			}
		}
	}
}

// attempt reports the panel CA while that is still to do, then heartbeats to
// target until the stream ends.
func (l *AgentLink) attempt(ctx context.Context, r *linkRun, target string) attemptEnd {
	client, err := DialManager(target, l.TLS, l.ManagerIDs)
	if err != nil {
		return attemptEnd{endFailed, err}
	}
	defer client.Close()

	if !r.reported {
		switch err := l.reportPanelCA(ctx, client); {
		case err == nil:
			r.reported = true
		case ctx.Err() != nil:
			return attemptEnd{kind: endStopped}
		case removedByLeader(err):
			return attemptEnd{endRemoved, err}
		default:
			// The heartbeat goes ahead regardless — it is how the agent
			// learns it was removed — and the report is tried again before
			// the next one.
			r.warn(warnReport, target, err)
		}
	}
	return l.heartbeat(ctx, r, client, target)
}

// reportPanelCA sends the panel's CA to the leader, through client or, when
// client's manager is a follower, to the leader it names.
func (l *AgentLink) reportPanelCA(ctx context.Context, client *GRPCClient) error {
	caPEM, err := l.PanelCA()
	if err != nil {
		return fmt.Errorf("read the panel CA: %w", err)
	}
	if len(caPEM) == 0 {
		return nil
	}
	req := &pb.ReportPanelCARequest{CaCert: caPEM}
	resp, err := client.ReportPanelCA(ctx, req)
	if err == nil && !resp.Success && resp.Error == ErrNotLeader.Error() && resp.LeaderGrpcAddress != "" {
		leader, derr := DialManager(resp.LeaderGrpcAddress, l.TLS, l.ManagerIDs)
		if derr != nil {
			return derr
		}
		defer leader.Close()
		resp, err = leader.ReportPanelCA(ctx, req)
	}
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("refused: %s", resp.Error)
	}
	return nil
}

// heartbeat pings target at once and then every Interval. A second goroutine
// reads the pongs — they must be read, or they fill the stream's flow-control
// window and both ends wedge (the drain in StartLocalMetrics) — and it ends
// the session: on a stream error, or when a follower names the leader
// elsewhere; a manager that names no leader ends it too (errNoLeaderKnown).
// What it saw is how the session ended, because a failed Send says only
// io.EOF.
func (l *AgentLink) heartbeat(ctx context.Context, r *linkRun, client *GRPCClient, target string) attemptEnd {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.OpenAgentHeartbeat(sctx)
	if err != nil {
		return classify(ctx, err)
	}

	stayed := make(chan struct{}) // closed at the first pong that is not a redirect
	ended := make(chan attemptEnd, 1)
	safe.Go("agent-heartbeat-recv", func() {
		end := attemptEnd{endFailed, errors.New("the heartbeat receive loop stopped")}
		defer func() {
			ended <- end
			cancel()
		}()
		first := true
		for {
			pong, err := stream.Recv()
			if err != nil {
				end = classify(ctx, err)
				return
			}
			l.applyPong(pong)
			switch {
			case !pong.FromLeader && pong.LeaderGrpcAddress != "" && pong.LeaderGrpcAddress != target:
				end = attemptEnd{kind: endRedirected}
				return
			case !pong.FromLeader && pong.LeaderGrpcAddress == "":
				// The leader may be another manager; staying here kept the
				// agent off it for as long as this one answered.
				end = attemptEnd{endFailed, errNoLeaderKnown}
				return
			}
			if first {
				first = false
				close(stayed)
			}
		}
	})

	ticker := time.NewTicker(l.interval())
	defer ticker.Stop()
	stayedCh := stayed
	if stream.Send(l.ping()) == nil {
	loop:
		for {
			select {
			case <-sctx.Done():
				break loop
			case <-stayedCh:
				stayedCh = nil
				r.stayedWith(target)
			case <-ticker.C:
				if stream.Send(l.ping()) != nil {
					break loop
				}
			}
		}
	}
	return <-ended
}

func (l *AgentLink) ping() *pb.HeartbeatPing {
	p := &pb.HeartbeatPing{NodeId: l.NodeID, Version: l.Version, Timestamp: time.Now().Unix()}
	if l.Collect != nil {
		cpu, mem, disk, containers := l.Collect()
		p.CpuPercent, p.MemoryPercent, p.DiskPercent, p.ContainerCount = cpu, mem, disk, int32(containers)
	}
	return p
}

// applyPong takes where the leader is from any pong — a follower's word is
// how the agent finds it — but the manager list from the leader's only: a
// follower's list may lag the leader's, and one partitioned away can still
// name a manager since removed, whom the agent would then accept. A manager
// older than these fields sends neither, and an empty list is never taken:
// it would leave the agent no manager to accept or to heartbeat to, and the
// saved state would keep it so.
//
// The one entry taken from a follower is the leader it names, when the
// agent has no manager at that address: a leader elected while the agent was
// not listening is reachable only once the agent accepts it, and only its
// own pong carries the list, which then replaces the agent's.
func (l *AgentLink) applyPong(p *pb.HeartbeatPong) {
	err := l.update(func(s *AgentState) bool {
		changed := false
		if p.LeaderGrpcAddress != "" && p.LeaderGrpcAddress != s.Leader {
			s.Leader = p.LeaderGrpcAddress
			changed = true
		}
		if p.FromLeader {
			if refs := managerRefs(p.Managers); len(refs) > 0 && !slices.Equal(refs, s.Managers) {
				s.Managers = refs
				changed = true
			}
		} else if leader, ok := namedLeader(p); ok && !slices.ContainsFunc(s.Managers, func(m config.ManagerRef) bool { return m.GRPCAddress == leader.GRPCAddress }) {
			managers := slices.DeleteFunc(slices.Clone(s.Managers), func(m config.ManagerRef) bool { return m.ID == leader.ID })
			s.Managers = append(managers, leader)
			changed = true
		}
		return changed
	})
	if err != nil {
		slog.Warn("could not save the agent state", "component", "agent", "path", l.StatePath, "error", err)
	}
}

// namedLeader is the entry in p's manager list for the leader p names.
func namedLeader(p *pb.HeartbeatPong) (config.ManagerRef, bool) {
	if p.LeaderGrpcAddress == "" {
		return config.ManagerRef{}, false
	}
	for _, ref := range managerRefs(p.Managers) {
		if ref.GRPCAddress == p.LeaderGrpcAddress {
			return ref, true
		}
	}
	return config.ManagerRef{}, false
}

func managerRefs(infos []*pb.NodeInfo) []config.ManagerRef {
	var refs []config.ManagerRef
	for _, n := range infos {
		if n.GetId() != "" {
			refs = append(refs, config.ManagerRef{ID: n.GetId(), GRPCAddress: n.GetGrpcAddress()})
		}
	}
	return refs
}

// markRemoved saves the removal and runs the hook main set, which closes the
// agent's cluster-facing listeners.
func (l *AgentLink) markRemoved(by string) {
	if err := l.update(func(s *AgentState) bool { s.Removed = true; return true }); err != nil {
		// Run stops all the same. After a restart the leader says it again.
		slog.Warn("could not save the agent's removal", "component", "agent", "path", l.StatePath, "error", err)
	}
	slog.Info("the leader removed this agent from the cluster; run 'sfpanel agent reset' to use this server standalone again",
		"component", "agent", "node_id", l.NodeID, "manager", by)
	l.mu.Lock()
	f := l.onRemoved
	l.mu.Unlock()
	if f != nil {
		f()
	}
}

// sleepCtx waits d, or until ctx ends; it reports whether ctx is still live.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
