package cluster

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"maps"
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
	// CASeq is the seq of the last CA rotation hint taken; a lower one is
	// older (an older release ignores the field).
	CASeq uint64 `json:"ca_seq,omitempty"`
	// Reached is, per manager ID, the host:port the last session with that
	// manager went through: dialled first next time (targetsFor), whatever
	// network it is on, so an agent that found its way once does not
	// search again.
	Reached map[string]string `json:"reached,omitempty"`
}

// LoadAgentState reads the state at path. Before the first save there is no
// file, and the agent starts from seed, the managers it registered with.
func LoadAgentState(path string, seed []config.ManagerRef) (*AgentState, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &AgentState{Managers: cloneManagerRefs(seed)}, nil
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

// cloneManagerRefs copies refs down to their addresses, which the copy must
// not share with the config it came from.
func cloneManagerRefs(refs []config.ManagerRef) []config.ManagerRef {
	out := slices.Clone(refs)
	for i := range out {
		out[i].Addresses = slices.Clone(out[i].Addresses)
	}
	return out
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
	// Advertise is the IP the agent registered at, which a renewed
	// certificate must name: managers dial it. It also decides which of a
	// manager's addresses the agent dials first (rankAddresses).
	Advertise string
	// Keys are the agent's internal-proxy keys; nil: the link takes no part
	// in a key rotation and does not say it can.
	Keys *AgentKeys
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

	// renew hands the sender the CA a pong's hint wants the agent's
	// certificate from: the receive goroutine makes no call. caWarned is the
	// last reason a hint was not taken, which only receive goroutines read
	// and write, one session after another.
	renew    chan string
	caWarned string
	// rekey hands the sender the leader's word on an agent key rotation, for
	// the same reason.
	rekey chan *pb.AgentKeyHint

	// dial connects to the manager at a host:port; nil is DialManager. Tests
	// stand loopback listeners in for the documentation addresses they
	// list, which no test host has.
	dial func(target string) (*GRPCClient, error)
}

// NewAgentLink loads the agent's state, from seed if none is saved yet.
func NewAgentLink(cfg AgentLinkConfig, seed []config.ManagerRef) (*AgentLink, error) {
	st, err := LoadAgentState(cfg.StatePath, seed)
	if err != nil {
		return nil, err
	}
	return &AgentLink{AgentLinkConfig: cfg, state: st, renew: make(chan string, 1), rekey: make(chan *pb.AgentKeyHint, 1)}, nil
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

// ManagerHosts are the hosts the managers can be reached at, each once: the
// host of each one's gRPC address and every address it reported — a manager
// on a tailnet reaches the agent from its Tailscale address as well.
func (l *AgentLink) ManagerHosts() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var hosts []string
	add := func(host string) {
		if host != "" && !slices.Contains(hosts, host) {
			hosts = append(hosts, host)
		}
	}
	for _, m := range l.state.Managers {
		if host, _, err := net.SplitHostPort(m.GRPCAddress); err == nil {
			add(host)
		}
		for _, a := range m.Addresses {
			add(a)
		}
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
	s.Reached = maps.Clone(s.Reached)
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
// last heard of, then each manager in list order — each at all its
// addresses, in the order targetsFor gives for an agent at own.
func nextTargets(state AgentState, own string) []string {
	var out []string
	add := func(addrs ...string) {
		for _, a := range addrs {
			if a != "" && !slices.Contains(out, a) {
				out = append(out, a)
			}
		}
	}
	if i := slices.IndexFunc(state.Managers, func(m config.ManagerRef) bool { return m.GRPCAddress == state.Leader }); i >= 0 {
		add(state.targetsFor(state.Managers[i], own)...)
	} else {
		add(state.Leader)
	}
	for _, m := range state.Managers {
		add(state.targetsFor(m, own)...)
	}
	return out
}

// isTailscaleAddr reports whether a, an IP with or without a port, is in
// Tailscale's range.
func isTailscaleAddr(a string) bool {
	if host, _, err := net.SplitHostPort(a); err == nil {
		a = host
	}
	ip := net.ParseIP(a)
	return ip != nil && IsTailscaleIP(ip)
}

// rankAddresses orders ips, with or without ports, for an agent at own: those
// on own's network first — Tailscale addresses when own is one, else those
// that are not — then the rest, each group in the order given. The agent
// knows no interface names, only the range: a manager's LAN may be one the
// agent cannot route to, its tailnet one it can, or the other way round.
func rankAddresses(own string, ips []string) []string {
	ts := isTailscaleAddr(own)
	out := make([]string, 0, len(ips))
	for _, a := range ips {
		if isTailscaleAddr(a) == ts {
			out = append(out, a)
		}
	}
	for _, a := range ips {
		if isTailscaleAddr(a) != ts {
			out = append(out, a)
		}
	}
	return out
}

// targetsFor is every host:port the agent at own can dial ref at: the one a
// session last went through first, then ref's addresses as rankAddresses
// orders them, each at the port of ref's gRPC address, then that address
// itself when none of those is it (a manager registered by name). A ref
// without addresses — from a manager older than them — is its gRPC address
// alone.
func (s AgentState) targetsFor(ref config.ManagerRef, own string) []string {
	if ref.GRPCAddress == "" {
		return nil
	}
	_, port, err := net.SplitHostPort(ref.GRPCAddress)
	if err != nil || len(ref.Addresses) == 0 {
		return []string{ref.GRPCAddress}
	}
	out := make([]string, 0, len(ref.Addresses)+1)
	for _, a := range rankAddresses(own, ref.Addresses) {
		if t := net.JoinHostPort(a, port); !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	if !slices.Contains(out, ref.GRPCAddress) {
		out = append(out, ref.GRPCAddress)
	}
	// Only an address the manager still lists: one it has moved off may be
	// another server's by now.
	if r := s.Reached[ref.ID]; r != "" {
		if i := slices.Index(out, r); i > 0 {
			out = append([]string{r}, slices.Delete(out, i, i+1)...)
		}
	}
	return out
}

// leaderTarget is where the agent at own dials the leader, the manager
// leaderID: its first target. A leader the agent does not list, or one
// named by no ID, is dialled at fallback, the address the answer gave.
func (s AgentState) leaderTarget(leaderID, fallback, own string) string {
	if leaderID != "" {
		if i := slices.IndexFunc(s.Managers, func(m config.ManagerRef) bool { return m.ID == leaderID }); i >= 0 {
			if t := s.targetsFor(s.Managers[i], own); len(t) > 0 {
				return t[0]
			}
		}
	}
	return fallback
}

// managerIDAt is the ID of the manager registered at grpcAddr, or "".
func (s AgentState) managerIDAt(grpcAddr string) string {
	if i := slices.IndexFunc(s.Managers, func(m config.ManagerRef) bool { return m.GRPCAddress == grpcAddr }); i >= 0 {
		return s.Managers[i].ID
	}
	return ""
}

// managerReachedAt is the ID of the manager one of whose targets is target,
// or "".
func (s AgentState) managerReachedAt(target string) string {
	for _, m := range s.Managers {
		if slices.Contains(s.targetsFor(m, ""), target) {
			return m.ID
		}
	}
	return ""
}

// leaderAt is where to dial the leader an answer names by its gRPC address
// alone, as the answers to ReportPanelCA, RotateAgentKey and
// RenewCertificate do: on the agent's own network when the agent lists the
// manager registered there.
func (l *AgentLink) leaderAt(grpcAddr string) string {
	s := l.snapshot()
	return s.leaderTarget(s.managerIDAt(grpcAddr), grpcAddr, l.Advertise)
}

// redirectTarget is where to dial the leader a follower's pong names: by its
// ID, which a pong carries, or else by its gRPC address.
func (l *AgentLink) redirectTarget(p *pb.HeartbeatPong) string {
	s := l.snapshot()
	id := p.LeaderId
	if id == "" {
		id = s.managerIDAt(p.LeaderGrpcAddress)
	}
	return s.leaderTarget(id, p.LeaderGrpcAddress, l.Advertise)
}

func (l *AgentLink) dialManager(target string) (*GRPCClient, error) {
	if l.dial != nil {
		return l.dial(target)
	}
	return DialManager(target, l.TLS, l.ManagerIDs)
}

// noteReached saves target as the address the manager there was last
// reached at — only when that is news: a session to the same address as the
// last does not write the state file.
func (l *AgentLink) noteReached(target string) {
	err := l.update(func(s *AgentState) bool {
		id := s.managerReachedAt(target)
		if id == "" || s.Reached[id] == target {
			return false
		}
		if s.Reached == nil {
			s.Reached = map[string]string{}
		}
		s.Reached[id] = target
		return true
	})
	if err != nil {
		slog.Warn("could not save the agent state", "component", "agent", "path", l.StatePath, "error", err)
	}
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
	warnRenew     = "could not renew the certificate for a CA rotation"
	warnRekey     = "could not move to the agent's new key"
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
	// errCAHintOutlivesConnection ends a session on which the leader's CA
	// hint leaves out the CA the connection verified the leader by. A
	// connection keeps the certificate the leader showed when it was made,
	// and the leader may have moved its own since: refused on this
	// connection for as long as it lasts, the hint would leave a rotation
	// waiting on this agent for good. A new connection shows the leader's
	// certificate as it is now.
	errCAHintOutlivesConnection = errors.New("the leader's CA hint leaves out the CA this connection verified it by")
	// errAgentKeyNotHeld is a leader saying its managers sign for this agent
	// with a key the agent does not hold: nothing they relay to it gets in
	// until it joins again.
	errAgentKeyNotHeld = errors.New("the managers sign for this agent with a key it does not hold; run 'sfpanel agent reset' and join again")
)

// linkRun is what one Run remembers from attempt to attempt.
type linkRun struct {
	reported  bool              // the panel CA has reached the leader
	current   string            // the manager the agent last stayed with
	warned    map[string]string // the error last logged, per warning and manager
	lastRenew time.Time         // the last renewal asked for, whatever came of it
	lastRekey time.Time         // the last key rotation step asked for, whatever came of it
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
	endRenewed                   // the certificate was renewed: a new connection shows it
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
// the leader elsewhere is left at once for it, and so is a session that
// renewed the certificate, for a new connection that presents it; any other
// failure moves on to the next manager after an Interval.
func (l *AgentLink) Run(ctx context.Context) {
	if l.Removed() {
		return
	}
	r := &linkRun{reported: l.PanelCA == nil, warned: map[string]string{}}
	next := 0
	for ctx.Err() == nil {
		targets := nextTargets(l.snapshot(), l.Advertise)
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
		case endRedirected, endRenewed:
			// The pong saved the leader's address; nextTargets puts it
			// first, at the address redirectTarget chose.
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
	client, err := l.dialManager(target)
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
		leader, derr := l.dialManager(l.leaderAt(resp.LeaderGrpcAddress))
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
// elsewhere; a manager that names no leader ends it too (errNoLeaderKnown),
// and so does a leader's CA hint that leaves out the CA this connection
// verified it by (errCAHintOutlivesConnection). What it saw is how the
// session ended, because a failed Send says only io.EOF. The sender renews
// the agent's certificate when the leader's CA hint asks for it, at most
// once per renewalInterval, and then ends the session itself (endRenewed):
// the leader goes by the certificate a connection presented, so the new one
// is shown on a new connection. It also moves the agent to a new key when the
// leader's key hint names a newer generation, at most once per
// renewalInterval; the session stays, as the key has nothing to do with the
// connection.
func (l *AgentLink) heartbeat(ctx context.Context, r *linkRun, client *GRPCClient, target string) attemptEnd {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.OpenAgentHeartbeat(sctx)
	if err != nil {
		return classify(ctx, err)
	}
	// The CA this connection verified the manager's certificate by, which a
	// CA hint that came on it must keep (applyCAHint).
	root := peerRootPin(stream.Context())

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
			if err := l.applyPong(pong, root); err != nil {
				end = attemptEnd{endFailed, err}
				return
			}
			switch {
			case !pong.FromLeader && pong.LeaderGrpcAddress != "" && l.redirectTarget(pong) != target:
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
	renewed := false
	if stream.Send(l.ping()) == nil {
	loop:
		for {
			select {
			case <-sctx.Done():
				break loop
			case <-stayedCh:
				stayedCh = nil
				r.stayedWith(target)
				l.noteReached(target)
			case <-ticker.C:
				if stream.Send(l.ping()) != nil {
					break loop
				}
			case pin := <-l.renew:
				if time.Since(r.lastRenew) < renewalInterval {
					continue
				}
				r.lastRenew = time.Now()
				if err := l.renewCertificate(sctx, client, pin); err != nil {
					r.warn(warnRenew, target, err)
					continue
				}
				delete(r.warned, warnRenew+"|"+target)
				renewed = true
				break loop
			case h := <-l.rekey:
				if !l.Keys.Due(h.Generation) || time.Since(r.lastRekey) < renewalInterval {
					continue
				}
				r.lastRekey = time.Now()
				if err := l.rotateKey(sctx, client, h); err != nil {
					r.warn(warnRekey, target, err)
					continue
				}
				delete(r.warned, warnRekey+"|"+target)
			}
		}
	}
	// A renewal leaves the stream up: cancel ends it, and its receive
	// goroutine with it.
	cancel()
	end := <-ended
	if renewed {
		return attemptEnd{kind: endRenewed}
	}
	return end
}

// ping says, besides the metrics, that the agent follows a CA rotation and
// which CAs it trusts: what the rotation's steps wait for. With its keys it
// says it follows an agent key rotation too, which the leader's start waits
// for.
func (l *AgentLink) ping() *pb.HeartbeatPing {
	caps := []string{CapCARotation}
	if l.Keys != nil {
		caps = append(caps, CapAgentKeyRotation)
	}
	p := &pb.HeartbeatPing{NodeId: l.NodeID, Version: l.Version, Timestamp: time.Now().Unix(),
		Capabilities: caps, CaTrustPins: l.TLS.TrustPins()}
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
//
// A CA rotation's hint is the leader's alone as well (applyCAHint): a
// follower's view of the rotation may lag, and one partitioned away could
// bring back a CA the rotation dropped, or drop the one it keeps. root is
// the CA the connection the pong came on verified the manager by; an error
// ends the session (errCAHintOutlivesConnection). So is an agent key
// rotation's: a follower may not know the rotation, or the agent's switch.
func (l *AgentLink) applyPong(p *pb.HeartbeatPong, root string) error {
	err := l.update(func(s *AgentState) bool {
		changed := false
		if p.LeaderGrpcAddress != "" && p.LeaderGrpcAddress != s.Leader {
			s.Leader = p.LeaderGrpcAddress
			changed = true
		}
		if p.FromLeader {
			if refs := managerRefs(p.Managers); len(refs) > 0 && !slices.EqualFunc(refs, s.Managers, sameManagerRef) {
				s.Managers = refs
				// The address a manager no longer listed was reached at
				// goes with it.
				maps.DeleteFunc(s.Reached, func(id, _ string) bool {
					return !slices.ContainsFunc(refs, func(m config.ManagerRef) bool { return m.ID == id })
				})
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
	if p.FromLeader && p.AgentKey != nil && l.Keys != nil {
		l.offerKeyHint(p.AgentKey)
	}
	if p.FromLeader && p.Ca != nil {
		return l.applyCAHint(p.Ca, root)
	}
	return nil
}

// offerKeyHint hands the sender the leader's word on a key rotation. The
// receive goroutine makes no call (the heartbeat wedge, cluster CLAUDE.md);
// a hint still waiting is replaced, as only the newest matters.
func (l *AgentLink) offerKeyHint(h *pb.AgentKeyHint) {
	select {
	case <-l.rekey:
	default:
	}
	select {
	case l.rekey <- h:
	default:
	}
}

// rotateKey takes the agent through one agent key rotation, by h, the
// leader's hint, asking the leader through client. G is the generation of
// agent.key, N' that of the key the agent took and has not switched to:
//
//   - h's generation N ≤ G: nothing to do.
//   - N' = N and the managers sign with it (h.Current): the agent lost the
//     answer to its confirm, and switches.
//   - N' = N, not current: it confirms with the new key, then switches.
//   - N' ≠ N, not current: it fetches its key with the one it holds, takes
//     it, confirms with it and switches. A fetch refused for its proof, with
//     N' < N, is asked again with the key of N': the leader took the
//     agent's confirm of N' and the answer never came — or it stopped
//     between moving the managers to that key and recording it — so the
//     managers sign with that key, and the agent switches to it first.
//   - N' ≠ N and current: the managers sign with a key the agent never took
//     (errAgentKeyNotHeld).
//
// A fetch that answers no key, only that the managers switched, is that last
// case too.
func (l *AgentLink) rotateKey(ctx context.Context, client *GRPCClient, h *pb.AgentKeyHint) error {
	current, held := l.Keys.Current()
	next, pending := l.Keys.Pending()
	n := h.Generation
	switch {
	case n <= held:
		return nil
	case next != "" && pending == n && h.Current:
		return l.switchKey(n)
	case next == "" || pending != n:
		if h.Current {
			return errAgentKeyNotHeld
		}
		resp, err := l.callRotateAgentKey(ctx, client, &pb.RotateAgentKeyRequest{Generation: n, Proof: agentKeyProof(current, "fetch", l.NodeID, n)})
		if errors.Is(err, ErrAgentKeyProof) && next != "" && pending < n {
			resp, err = l.callRotateAgentKey(ctx, client, &pb.RotateAgentKeyRequest{Generation: n, Proof: agentKeyProof(next, "fetch", l.NodeID, n)})
			if err == nil {
				if err := l.switchKey(pending); err != nil {
					return err
				}
			}
		}
		if err != nil {
			return err
		}
		if resp.AgentKey == "" {
			return errAgentKeyNotHeld
		}
		if err := l.Keys.Take(resp.AgentKey, n); err != nil {
			return err
		}
		slog.Info("the agent took its next key", "component", "agent", "generation", n)
		next = resp.AgentKey
	}
	resp, err := l.callRotateAgentKey(ctx, client, &pb.RotateAgentKeyRequest{Generation: n, Proof: agentKeyProof(next, "confirm", l.NodeID, n), Confirm: true})
	if err != nil {
		return err
	}
	if !resp.Switched {
		return fmt.Errorf("the leader took the confirm of generation %d but did not move the managers to the key", n)
	}
	return l.switchKey(n)
}

func (l *AgentLink) switchKey(generation int64) error {
	if err := l.Keys.Switch(generation, time.Now()); err != nil {
		return err
	}
	slog.Info("the agent moved to its new key", "component", "agent", "generation", generation)
	return nil
}

// callRotateAgentKey asks for req through client or, when client's manager is
// a follower, from the leader it names. A manager that answers Unimplemented
// runs a release from before agent key rotations, not from before agents:
// the key stays as it is, and the agent asks again later.
func (l *AgentLink) callRotateAgentKey(ctx context.Context, client *GRPCClient, req *pb.RotateAgentKeyRequest) (*pb.RotateAgentKeyResponse, error) {
	resp, err := client.RotateAgentKey(ctx, req)
	if err == nil && !resp.Success && resp.Error == ErrNotLeader.Error() && resp.LeaderGrpcAddress != "" {
		leader, derr := l.dialManager(l.leaderAt(resp.LeaderGrpcAddress))
		if derr != nil {
			return nil, derr
		}
		defer leader.Close()
		resp, err = leader.RotateAgentKey(ctx, req)
	}
	if status.Code(err) == codes.Unimplemented {
		return nil, errors.New("this manager cannot rotate agent keys; the key stays as it is")
	}
	if err != nil {
		return nil, err
	}
	if !resp.Success && resp.Error == ErrAgentKeyProof.Error() {
		// rotateKey asks again with the key the managers may sign with.
		return nil, fmt.Errorf("refused: %w", ErrAgentKeyProof)
	}
	if !resp.Success {
		return nil, fmt.Errorf("refused: %s", resp.Error)
	}
	return resp, nil
}

// applyCAHint takes the leader's word on the cluster CAs: the CAs to trust,
// the one ca.crt starts with first, and the CA the agent's certificate must
// come from. A hint is taken once, when its seq is above the one taken last
// — a lagging view shows an older step — and only whole: certificates and
// nothing else, every one a CA, within maxCABundleBytes, and with root, the
// CA this very connection verified the manager by. Written without it, the
// bundle would cut the agent off from that manager at its next dial; it is
// asked for again on a new connection instead (errCAHintOutlivesConnection,
// which this returns). ca.crt is written before its CAs are trusted and the
// seq saved, so a restart in any step comes back trusting what the agent
// trusted.
//
// Every hint of the leader's step then hands the sender a renewal when the
// CA it names is trusted and is not the one the agent's certificate comes
// from; the sender asks at most once per renewalInterval, so a renewal that
// failed is asked for again. It runs on the receive goroutine, which makes no
// call: pongs left unread meanwhile would wedge the stream (heartbeat); a
// hint not taken for any other reason is logged when the reason changes.
func (l *AgentLink) applyCAHint(h *pb.ClusterCA, root string) error {
	l.mu.Lock()
	taken := l.state.CASeq
	l.mu.Unlock()
	switch {
	case h.Seq < taken:
		l.noteCAHint(h, fmt.Errorf("its step %d is older than the step %d taken", h.Seq, taken))
		return nil
	case h.Seq > taken:
		if err := l.takeCAHint(h, root); errors.Is(err, errCAHintOutlivesConnection) {
			return err
		} else if err != nil {
			l.noteCAHint(h, err)
			return nil
		}
		l.caWarned = ""
	}
	pin := h.SignPin
	if pin == "" || pin == l.TLS.ServedCertCAPin() || !slices.Contains(l.TLS.TrustPins(), pin) {
		return nil
	}
	// Never blocks the receive goroutine; a pin still waiting is replaced,
	// as only the newest matters.
	select {
	case <-l.renew:
	default:
	}
	select {
	case l.renew <- pin:
	default:
	}
	return nil
}

// takeCAHint checks the bundle of h and makes it this agent's trust: ca.crt,
// then the CAs trusted, then the seq taken.
func (l *AgentLink) takeCAHint(h *pb.ClusterCA, root string) error {
	if len(h.Bundle) > maxCABundleBytes {
		return fmt.Errorf("its bundle is larger than %d KiB", maxCABundleBytes>>10)
	}
	certs, err := certificatesIn(h.Bundle)
	if err != nil {
		return err
	}
	var trust []byte
	pins := make([]string, 0, len(certs))
	for _, cert := range certs {
		if !cert.IsCA {
			return fmt.Errorf("its bundle holds %q, which is not a CA", cert.Subject.CommonName)
		}
		pins = append(pins, CAPin(cert.RawSubjectPublicKeyInfo))
		// Written as parsed: what pem.Decode skips around a block is not
		// trust and does not go to ca.crt.
		trust = append(trust, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})...)
	}
	if root == "" || !slices.Contains(pins, root) {
		return fmt.Errorf("%w (%s); trying a new connection", errCAHintOutlivesConnection, root)
	}
	if onDisk, err := l.TLS.LoadCACert(); err != nil || !bytes.Equal(onDisk, trust) {
		if err := l.TLS.SaveCACert(trust); err != nil {
			return fmt.Errorf("write ca.crt: %w", err)
		}
	}
	if !slices.Equal(l.TLS.TrustPins(), pins) {
		if err := l.TLS.ReloadCAPool(); err != nil {
			return fmt.Errorf("trust ca.crt: %w", err)
		}
	}
	if err := l.update(func(s *AgentState) bool {
		if s.CASeq >= h.Seq {
			return false
		}
		s.CASeq = h.Seq
		return true
	}); err != nil {
		// Taken all the same: the seq is in memory, and after a restart the
		// same hint is taken again, as nothing differs.
		slog.Warn("could not save the agent state", "component", "agent", "path", l.StatePath, "error", err)
	}
	slog.Info("the agent trusts the cluster CAs the leader named", "component", "agent", "seq", h.Seq, "cas", len(certs))
	return nil
}

// noteCAHint warns why a hint of the leader's was not taken, once while the
// reason stays the same: every pong of a step carries the hint. Receive
// goroutine only.
func (l *AgentLink) noteCAHint(h *pb.ClusterCA, err error) {
	if err.Error() == l.caWarned {
		return
	}
	l.caWarned = err.Error()
	slog.Warn("did not take the leader's CA hint", "component", "agent", "seq", h.Seq, "error", err)
}

// renewCertificate asks the leader, through client, for this agent's
// certificate from pin, and installs it. The certificate the leader signs
// must come from pin, the CA the hint named, and the agent trusts it
// already: one from another CA means the rotation moved on since the hint,
// and the next hint says where. A follower names the leader, which is asked
// once more there. A manager that answers Unimplemented runs a release from
// before CA rotations, not from before agents: the certificate stays as it
// is, and the agent asks again later.
func (l *AgentLink) renewCertificate(ctx context.Context, client *GRPCClient, pin string) error {
	key, err := l.TLS.nodeKey()
	if err != nil {
		return err
	}
	// For the key the agent has: only node.crt changes (installRenewedCert).
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		return fmt.Errorf("make the certificate request: %w", err)
	}
	agentKey, err := l.TLS.LoadAgentKey()
	if err != nil {
		return err
	}
	req := &pb.RenewCertificateRequest{Csr: csr, Proof: renewalProof(agentKey, l.NodeID, csr)}
	resp, err := client.RenewCertificate(ctx, req)
	if err == nil && !resp.Success && resp.Error == ErrNotLeader.Error() && resp.LeaderGrpcAddress != "" {
		leader, derr := l.dialManager(l.leaderAt(resp.LeaderGrpcAddress))
		if derr != nil {
			return derr
		}
		defer leader.Close()
		resp, err = leader.RenewCertificate(ctx, req)
	}
	if status.Code(err) == codes.Unimplemented {
		return errors.New("this manager cannot renew certificates; the certificate stays as it is")
	}
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("refused: %s", resp.Error)
	}
	leaf, err := parseCertificatePEM(resp.NodeCert)
	if err != nil {
		return err
	}
	if l.TLS.trustedIssuerPin(leaf) != pin {
		return fmt.Errorf("the leader's certificate is not from %s, the CA its hint named", pin)
	}
	if err := l.TLS.installRenewedCert(resp.NodeCert, l.NodeID, l.Advertise); err != nil {
		return err
	}
	slog.Info("the agent's certificate moved to the CA a CA rotation signs with", "component", "agent", "ca", pin)
	return nil
}

// namedLeader is the entry in p's manager list for the leader p names: by
// its ID, or else by its gRPC address.
func namedLeader(p *pb.HeartbeatPong) (config.ManagerRef, bool) {
	if p.LeaderGrpcAddress == "" {
		return config.ManagerRef{}, false
	}
	refs := managerRefs(p.Managers)
	if p.LeaderId != "" {
		if i := slices.IndexFunc(refs, func(m config.ManagerRef) bool { return m.ID == p.LeaderId }); i >= 0 {
			return refs[i], true
		}
	}
	if i := slices.IndexFunc(refs, func(m config.ManagerRef) bool { return m.GRPCAddress == p.LeaderGrpcAddress }); i >= 0 {
		return refs[i], true
	}
	return config.ManagerRef{}, false
}

// sameManagerRef reports whether a and b name a manager alike, addresses and
// all.
func sameManagerRef(a, b config.ManagerRef) bool {
	return a.ID == b.ID && a.GRPCAddress == b.GRPCAddress && slices.Equal(a.Addresses, b.Addresses)
}

func managerRefs(infos []*pb.NodeInfo) []config.ManagerRef {
	var refs []config.ManagerRef
	for _, n := range infos {
		if n.GetId() != "" {
			refs = append(refs, config.ManagerRef{ID: n.GetId(), GRPCAddress: n.GetGrpcAddress(), Addresses: slices.Clone(n.GetAddresses())})
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
