package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/common/safe"
	"github.com/svrforum/SFPanel/internal/config"
)

// agentRuntime is what an agent runs in place of a cluster manager: no Raft,
// no FSM, no accounts. Its link heartbeats to the managers and keeps their
// list; its gRPC server takes the requests they proxy to it.
type agentRuntime struct {
	// link and grpc are nil when the leader removed the agent before this
	// boot: it then serves loopback alone.
	link *cluster.AgentLink
	grpc *cluster.AgentGRPCServer
	// allowed is cluster.agent_allowed_sources; managerHosts are the hosts of
	// the managers' addresses, asked on every request.
	allowed      []*net.IPNet
	managerHosts func() []string
	cancel       context.CancelFunc // ends the link; set by StartLink
}

// startAgent installs the agent's key, loads its link to the managers and
// starts its gRPC server. The link is not started here: StartLink does that
// once the panel's listeners and the removal hook are in place. An error ends
// the boot — an agent never comes up as a standalone panel instead (main).
func startAgent(cfg *config.Config, version string, collect func() (cpu, mem, disk float64, containers int), panelCA func() ([]byte, error)) (*agentRuntime, error) {
	allowed, err := parseAllowedSources(cfg.Cluster.AgentAllowedSources)
	if err != nil {
		return nil, err
	}
	tlsMgr := cluster.NewTLSManager(cfg.Cluster.CertDir)
	key, err := tlsMgr.LoadAgentKey()
	if err != nil {
		return nil, err
	}
	// The one key of this node's internal requests: the managers sign with it
	// what they relay here, and the gRPC server's loopback hop signs with it
	// what they proxy. Nothing signed with any other key gets in.
	auth.SetProxySecrets(key, []string{key})

	link, err := cluster.NewAgentLink(cluster.AgentLinkConfig{
		NodeID:    cfg.Cluster.NodeID,
		TLS:       tlsMgr,
		StatePath: agentStatePath(cfg),
		Version:   version,
		Interval:  30 * time.Second,
		Collect:   collect,
		PanelCA:   panelCA,
	}, cfg.Cluster.Managers)
	if err != nil {
		return nil, err
	}
	a := &agentRuntime{allowed: allowed}
	if link.Removed() {
		slog.Warn("removed from the cluster; run 'sfpanel agent reset' to use this server standalone again",
			"component", "agent", "node_id", cfg.Cluster.NodeID)
		return a, nil
	}

	// The agent listens on the address it registered with, which its
	// certificate names and its managers dial — never on every interface.
	// An empty or unspecified address would bind them all, and the panel's
	// loopback listener would then fail as "address already in use".
	ip := net.ParseIP(cfg.Cluster.AdvertiseAddress)
	if ip == nil || ip.IsUnspecified() {
		return nil, fmt.Errorf("cluster.advertise_address %q is not an address an agent can listen on: it must be the IP address this server registered with", cfg.Cluster.AdvertiseAddress)
	}
	if err := waitForLocalAddress(ip, agentAddressWait, net.InterfaceAddrs); err != nil {
		return nil, err
	}
	srv, err := cluster.NewAgentGRPCServer(tlsMgr, selfPanel(cfg), link.ManagerIDs)
	if err != nil {
		return nil, fmt.Errorf("agent gRPC server: %w", err)
	}
	if err := srv.Start(net.JoinHostPort(cfg.Cluster.AdvertiseAddress, strconv.Itoa(cfg.Cluster.GRPCPort))); err != nil {
		return nil, fmt.Errorf("agent gRPC server: %w", err)
	}
	a.link, a.grpc, a.managerHosts = link, srv, link.ManagerHosts
	return a, nil
}

// agentAddressWait is how long an agent's boot waits for the address it
// registered with to come up. An interface that comes up late — tailscale0, a
// WireGuard link, a slow DHCP lease — made the bind fail and the agent restart
// every five seconds, which the update watchdog takes for a broken release
// after ninety. Waiting rather than binding with IP_FREEBIND also lets the
// panel's certificate, issued after this, name the address.
var agentAddressWait = 60 * time.Second

// waitForLocalAddress waits up to limit for ip to be one of this host's
// addresses. A loopback address stands for its whole prefix: Linux binds all
// of 127.0.0.0/8 on lo, which lists 127.0.0.1/8 alone. Any other counts as
// itself, not its subnet.
func waitForLocalAddress(ip net.IP, limit time.Duration, addrs func() ([]net.Addr, error)) error {
	logged := false
	for deadline := time.Now().Add(limit); ; time.Sleep(time.Second) {
		list, err := addrs()
		if err == nil {
			for _, a := range list {
				if n, ok := a.(*net.IPNet); ok && (n.IP.Equal(ip) || n.IP.IsLoopback() && n.Contains(ip)) {
					return nil
				}
			}
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("the advertise address %s is not on this server after %v: the agent registered with it, and its managers reach it there", ip, limit)
		}
		if !logged {
			slog.Info("waiting for the advertise address to come up", "component", "agent", "address", ip.String())
			logged = true
		}
	}
}

// StartLink starts heartbeating to the managers, until ctx ends or Stop. It
// does nothing for an agent the leader removed before this boot.
func (a *agentRuntime) StartLink(ctx context.Context) {
	if a.link == nil {
		return
	}
	ctx, a.cancel = context.WithCancel(ctx)
	safe.Go("agent-link", func() { a.link.Run(ctx) })
}

// Stop ends the link and stops the gRPC server without waiting for it: the
// stop is graceful, and a request a manager proxied may run for minutes
// (cluster.ProxyTimeoutFor). The manager's server is stopped the same way.
func (a *agentRuntime) Stop() {
	if a.cancel != nil {
		a.cancel()
	}
	if a.grpc != nil {
		go a.grpc.Stop()
	}
}

// AllowsSource reports whether the panel takes a request from ip, besides
// loopback, which AgentSourceCheck always admits: a manager's host, or an
// address in cluster.agent_allowed_sources. The managers are read on every
// call, so the list the leader sends takes effect without a restart. A
// manager recorded by host name never matches: resolving it per request would
// make the check only as good as DNS.
func (a *agentRuntime) AllowsSource(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if a.managerHosts != nil {
		for _, h := range a.managerHosts() {
			if m := net.ParseIP(h); m != nil && m.Equal(ip) {
				return true
			}
		}
	}
	for _, n := range a.allowed {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// parseAllowedSources reads cluster.agent_allowed_sources: CIDRs, and single
// addresses as a /32 or /128.
func parseAllowedSources(entries []string) ([]*net.IPNet, error) {
	nets := make([]*net.IPNet, 0, len(entries))
	for _, s := range entries {
		if ip := net.ParseIP(s); ip != nil {
			if v4 := ip.To4(); v4 != nil {
				nets = append(nets, &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)})
			} else {
				nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)})
			}
			continue
		}
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("cluster.agent_allowed_sources: %q is neither an IP address nor a CIDR", s)
		}
		nets = append(nets, n)
	}
	return nets, nil
}

// agentListenAddrs are where an agent's panel listens, in place of
// server.host: its cluster address, which managers relay streams to, and
// loopback, which this server's own tools use — the update watchdog, the gRPC
// server's hop, install.sh and the CLI. When the cluster address is loopback
// itself, advertise is "": one address cannot be bound twice.
func agentListenAddrs(cfg *config.Config) (advertise, loopback string) {
	port := strconv.Itoa(cfg.Server.Port)
	loopback = net.JoinHostPort("127.0.0.1", port)
	if ip := net.ParseIP(cfg.Cluster.AdvertiseAddress); ip != nil && ip.Equal(net.IPv4(127, 0, 0, 1)) {
		return "", loopback
	}
	return net.JoinHostPort(cfg.Cluster.AdvertiseAddress, port), loopback
}

// servePanel serves srv on the agent's listeners and ties them to removal:
// once the leader removes the agent, the cluster-facing listener and the gRPC
// server close, and loopback stays — the update watchdog and `sfpanel agent`
// still reach the panel. An agent removed before this boot opens loopback
// alone.
func (a *agentRuntime) servePanel(srv *http.Server, cfg *config.Config, certFile, keyFile string) error {
	advertise, loopback := agentListenAddrs(cfg)
	if a.link == nil {
		advertise = ""
	}
	ls, err := serveAgent(srv, advertise, loopback, certFile, keyFile)
	if err != nil {
		return err
	}
	scheme := "http"
	if certFile != "" {
		scheme = "https"
	}
	slog.Info("server listening", "component", "agent", "advertise", advertise, "loopback", loopback, "scheme", scheme)
	if a.link != nil {
		a.link.SetOnRemoved(func() {
			_ = ls.CloseAdvertise()
			a.grpc.Stop()
		})
	}
	return nil
}

// agentListeners is what removal closes of an agent panel's listeners: the
// cluster-facing one, nil when there is none. Loopback is left to srv.
type agentListeners struct {
	advertise *clusterListener
	closeOnce sync.Once
	closeErr  error
}

// serveAgent serves srv on loopback and, unless advertise is "", on the
// cluster address, over TLS when certFile is set. Both run on srv, so its
// Shutdown closes both.
func serveAgent(srv *http.Server, advertise, loopback, certFile, keyFile string) (*agentListeners, error) {
	ls := &agentListeners{}
	lo, err := net.Listen("tcp", loopback)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", loopback, err)
	}
	serving := []net.Listener{lo}
	if advertise != "" {
		adv, err := net.Listen("tcp", advertise)
		if err != nil {
			_ = lo.Close()
			return nil, fmt.Errorf("listen %s: %w", advertise, err)
		}
		ls.advertise = newClusterListener(adv)
		serving = append(serving, ls.advertise)
	}
	for _, ln := range serving {
		go serveOn(srv, ln, certFile, keyFile)
	}
	return ls, nil
}

func serveOn(srv *http.Server, ln net.Listener, certFile, keyFile string) {
	var err error
	if certFile != "" {
		err = srv.ServeTLS(ln, certFile, keyFile)
	} else {
		err = srv.Serve(ln)
	}
	// Shutdown ends both with ErrServerClosed, and CloseAdvertise ends the
	// cluster-facing one with net.ErrClosed. Anything else is the panel
	// losing a listener, which is fatal here as it is for a manager's.
	if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		slog.Error("server failed", "component", "agent", "addr", ln.Addr().String(), "error", err)
		os.Exit(1)
	}
}

// CloseAdvertise closes the cluster-facing listener and the connections it
// accepted, and leaves loopback serving. Once only; a no-op when there is no
// such listener.
func (l *agentListeners) CloseAdvertise() error {
	if l == nil || l.advertise == nil {
		return nil
	}
	l.closeOnce.Do(func() { l.closeErr = l.advertise.closeAll() })
	return l.closeErr
}

// clusterListener is the cluster-facing listener. It keeps the connections it
// accepted so that removal can end them too: closing a listener stops only new
// connections, and a manager's kept-alive connection, or a terminal or event
// stream it has open, would otherwise outlive the removal. Closed the ordinary
// way (srv.Shutdown) it leaves them to the server, as a plain listener does.
type clusterListener struct {
	net.Listener
	mu    sync.Mutex
	conns map[*clusterConn]struct{} // nil once closeAll ran
}

func newClusterListener(ln net.Listener) *clusterListener {
	return &clusterListener{Listener: ln, conns: map[*clusterConn]struct{}{}}
}

func (l *clusterListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	cc := &clusterConn{Conn: c, l: l}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.conns == nil { // accepted as closeAll ran
		_ = c.Close()
		return nil, net.ErrClosed
	}
	l.conns[cc] = struct{}{}
	return cc, nil
}

// closeAll closes the listener and every connection it accepted.
func (l *clusterListener) closeAll() error {
	l.mu.Lock()
	conns := l.conns
	l.conns = nil
	l.mu.Unlock()
	err := l.Close()
	for c := range conns {
		_ = c.Conn.Close()
	}
	return err
}

// clusterConn is a connection the cluster-facing listener accepted; closing it
// takes it off the listener's list.
type clusterConn struct {
	net.Conn
	l *clusterListener
}

func (c *clusterConn) Close() error {
	c.l.mu.Lock()
	delete(c.l.conns, c)
	c.l.mu.Unlock()
	return c.Conn.Close()
}

// staticNodeIdentity gives the alert manager an agent's node ID: the agent has
// no cluster.Manager to ask, and the ID does not change while it runs.
type staticNodeIdentity string

func (s staticNodeIdentity) LocalNodeID() string { return string(s) }
