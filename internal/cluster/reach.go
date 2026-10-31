package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/svrforum/SFPanel/internal/common/safe"
)

// Which agents each manager reaches, and the leader role handed to a manager
// that reaches more of them. A manager relays to an agent only over its own
// networks — its LAN, its Tailscale — and an agent on a Tailscale address is
// out of reach of a manager with none. The operator should not have to know
// which manager leads for every agent to be one hop away.

// reachInterval is how often a manager probes the agents. A variable so a
// test can shorten it.
var reachInterval = 60 * time.Second

const (
	// reachFresh is how long a probe counts: three missed rounds and what a
	// manager knows of its reach is unknown again.
	reachFresh = 3 * time.Minute
	// reachDialTimeout bounds one dial to an agent, and reachParallel how many
	// run at once: an agent that drops the SYN costs a round three seconds,
	// not the whole probe.
	reachDialTimeout = 3 * time.Second
	reachParallel    = 8

	// A leader hands over only once it has led preferAfterElection, and at
	// most once per preferCooldown: one election must not set off the next.
	preferAfterElection = 30 * time.Second
	preferCooldown      = 10 * time.Minute
)

// reachResult is one probe: for every agent in the FSM, whether this manager
// connected to it, and when the probe ended.
type reachResult struct {
	Agents map[string]bool
	At     time.Time
}

// startReachProbes probes the agents now and every reachInterval, in a
// goroutine of its own — a dial that hangs must not hold up the heartbeat —
// until the manager stops; shutdown waits for it.
func (m *Manager) startReachProbes() {
	ctx, cancel := context.WithCancel(context.Background())
	done := m.heartbeat.Done()
	interval := reachInterval
	m.reachWG.Add(1)
	safe.Go("cluster-agent-reach", func() {
		defer m.reachWG.Done()
		defer cancel()
		// The stop ends a probe under way, not only the wait for the next.
		safe.Go("cluster-agent-reach-stop", func() {
			select {
			case <-done:
			case <-ctx.Done():
			}
			cancel()
		})
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			m.probeAgents(ctx)
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	})
}

// probeAgents dials every agent in the FSM, online or not — an agent only
// another manager reaches looks offline to a leader that does not — at its
// gRPC address and, failing that, at its other addresses on the same port,
// reachParallel at once. A bare TCP connect and close, no TLS: whether the
// network gets there is the question, and grpc-go logs a connection closed
// before its handshake below the severity it prints, so the agent's log
// stays quiet. The result is kept for reachableAgents unless ctx ended
// first, which is no verdict on anyone.
func (m *Manager) probeAgents(ctx context.Context) reachResult {
	var agents []*Node
	if m.raft != nil {
		for _, n := range m.raft.GetFSM().GetState().Nodes {
			if n.Kind == KindAgent {
				agents = append(agents, n)
			}
		}
	}
	dial := m.reachDial
	if dial == nil {
		dial = (&net.Dialer{Timeout: reachDialTimeout}).DialContext
	}

	res := reachResult{Agents: make(map[string]bool, len(agents))}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, reachParallel)
	for _, n := range agents {
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			ok := reachAgent(ctx, dial, n)
			mu.Lock()
			res.Agents[n.ID] = ok
			mu.Unlock()
		}()
	}
	wg.Wait()
	res.At = time.Now()
	if ctx.Err() == nil {
		m.reach.Store(&res)
	}
	return res
}

// reachAgent reports whether a TCP connection to n opens at its gRPC
// address or at another of its addresses with the same port, each dial
// bounded by reachDialTimeout.
func reachAgent(ctx context.Context, dial func(ctx context.Context, network, addr string) (net.Conn, error), n *Node) bool {
	host, port, err := net.SplitHostPort(n.GRPCAddress)
	if err != nil {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	addrs := []string{n.GRPCAddress}
	for _, a := range n.AllAddresses() {
		if a != host {
			addrs = append(addrs, net.JoinHostPort(a, port))
		}
	}
	for _, addr := range addrs {
		if ctx.Err() != nil {
			return false
		}
		dctx, cancel := context.WithTimeout(ctx, reachDialTimeout)
		c, err := dial(dctx, "tcp", addr)
		cancel()
		if err == nil {
			_ = c.Close()
			return true
		}
	}
	return false
}

// reachableAgents is the IDs of the agents this manager's last probe
// reached, sorted, while it is fresh (reachFresh); nil when there is none,
// and an empty list — not nil — when a fresh one reached none.
func (m *Manager) reachableAgents() []string {
	return reachedBy(m.reach.Load(), time.Now())
}

func reachedBy(r *reachResult, now time.Time) []string {
	if r == nil || now.Sub(r.At) > reachFresh {
		return nil
	}
	out := []string{}
	for id, ok := range r.Agents {
		if ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// preferInput is what a leader decides a hand-over on (choosePreferredLeader).
// Reach maps a manager to the agents it reaches; nil is unknown. Candidates
// are the other manager voters online by LiveHealth, and Names every node's
// name.
type preferInput struct {
	Self                           string
	Now, LeaderSince, LastTransfer time.Time
	Hold                           string
	Agents                         []string
	Reach                          map[string][]string
	Candidates                     []string
	Names                          map[string]string
}

// choosePreferredLeader is the manager the leader in should hand its role to,
// or "" and why not. It hands over to a candidate that reaches strictly more
// agents than the leader — as many is no reason, or two managers that reach
// different agents would trade the role back and forth — the most of them,
// and of those the first by name.
func choosePreferredLeader(in preferInput) (target string, reason string) {
	switch {
	case in.Hold != "":
		return "", "held: " + in.Hold
	case in.Now.Sub(in.LeaderSince) < preferAfterElection:
		return "", "just elected"
	case !in.LastTransfer.IsZero() && in.Now.Sub(in.LastTransfer) < preferCooldown:
		return "", "transferred recently"
	case in.Reach[in.Self] == nil:
		return "", "own reach unknown"
	}
	// Only agents in the cluster count, each once, whatever a report holds.
	count := func(id string) int {
		n := 0
		for _, a := range in.Agents {
			if slices.Contains(in.Reach[id], a) {
				n++
			}
		}
		return n
	}
	own := count(in.Self)
	if len(in.Agents) == 0 || own == len(in.Agents) {
		return "", "reaches every agent"
	}
	nameOf := func(id string) string {
		if name := in.Names[id]; name != "" {
			return name
		}
		return id
	}
	best, bestN := "", 0
	for _, id := range in.Candidates {
		if id == in.Self || in.Reach[id] == nil {
			continue
		}
		n := count(id)
		if n <= own {
			continue
		}
		if best == "" || n > bestN || n == bestN && (nameOf(id) < nameOf(best) || nameOf(id) == nameOf(best) && id < best) {
			best, bestN = id, n
		}
	}
	if best == "" {
		return "", "no manager reaches more"
	}
	return best, fmt.Sprintf("%s reaches %d of %d agents, this leader %d", nameOf(best), bestN, len(in.Agents), own)
}

// SetLeadershipHold sets what keeps the leader role where it is: fn returns
// why, or "" when nothing does. The feature handler holds it while a
// cluster update runs, which stops if the leader changes.
func (m *Manager) SetLeadershipHold(fn func() string) {
	m.preferMu.Lock()
	defer m.preferMu.Unlock()
	m.leadershipHold = fn
}

// MaybePreferLeader hands the leader role to a manager that reaches more
// agents than this leader does (choosePreferredLeader), on the leader's tick.
func (m *Manager) MaybePreferLeader() {
	m.preferLeader()
}

// preferLeader is MaybePreferLeader, returning the manager it handed over
// to, or tried to, and the reason.
func (m *Manager) preferLeader() (target string, reason string) {
	// Only a leader whose FSM holds every committed entry knows the agents
	// and the CA rotation record.
	if m.raft == nil || !m.raft.caughtUpLeader(2*time.Second) {
		return "", "not the leader"
	}
	now := time.Now()
	m.preferMu.Lock()
	if m.leaderSince.IsZero() {
		m.leaderSince = now
	}
	in := preferInput{Self: m.nodeID, Now: now, LeaderSince: m.leaderSince, LastTransfer: m.lastTransfer}
	holdFn := m.leadershipHold
	m.preferMu.Unlock()

	// A CA rotation stops if the leader changes, as a cluster update does.
	if holdFn != nil {
		in.Hold = holdFn()
	}
	if in.Hold == "" && m.CARotationActive() {
		in.Hold = "cluster CA rotation running"
	}

	state := m.raft.GetFSM().GetState()
	in.Names = make(map[string]string, len(state.Nodes))
	for id, n := range state.Nodes {
		in.Names[id] = n.Name
		if n.Kind == KindAgent {
			in.Agents = append(in.Agents, id)
		}
	}
	sort.Strings(in.Agents)

	// An agent the last probe has no verdict on — one registered since —
	// leaves this leader's own reach unknown until the next probe: a manager
	// that probed after the registration would reach one more, and every
	// new agent would move the leader.
	r := m.reach.Load()
	own := reachedBy(r, now)
	if own != nil {
		for _, a := range in.Agents {
			if _, judged := r.Agents[a]; !judged {
				own = nil
				break
			}
		}
	}
	in.Reach = map[string][]string{m.nodeID: own}

	health := m.heartbeat.LiveHealth()
	for id, n := range state.Nodes {
		if id == m.nodeID || !n.IsManager() || n.Role != RoleVoter || health[id] != StatusOnline {
			continue
		}
		in.Candidates = append(in.Candidates, id)
		if met := m.heartbeat.GetMetrics(id); met != nil {
			in.Reach[id] = met.ReachableAgents
		}
	}
	sort.Strings(in.Candidates)

	target, reason = choosePreferredLeader(in)
	if target == "" {
		return "", reason
	}
	// Recorded whether it works or not: a hand-over that fails is tried
	// again after the cooldown, not on every tick.
	m.preferMu.Lock()
	m.lastTransfer = now
	m.preferMu.Unlock()
	if err := m.TransferLeadership(target); err != nil {
		slog.Warn("could not hand leadership to a manager that reaches more agents",
			"component", "cluster", "target", target, "reason", reason, "error", err)
		return target, reason
	}
	slog.Info("leadership handed to a manager that reaches more agents",
		"component", "cluster", "target", target, "reason", reason)
	return target, reason
}
