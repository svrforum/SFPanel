package cluster

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/svrforum/SFPanel/internal/config"
)

// conversionTokenTTL is how long the token a conversion or a promotion hands
// its node lives. The node spends it seconds after it gets it: once it has
// left Raft, or, promoted, at once.
const conversionTokenTTL = 30 * time.Minute

// refuseConversion is a gate's refusal of a conversion: nothing has changed.
func refuseConversion(reason error) error {
	return fmt.Errorf("%w: %w", ErrConvertRefused, reason)
}

// ReadyForAgentConversion reports whether the manager id can be converted to
// an agent now (leader only). Every gate is checked before the node leaves
// Raft: one that only its registration checked would find the node out of
// the cluster with nowhere to go. The node must be a manager other than this
// leader, online on a version that knows agents; the managers left must keep
// their quorum; and none of the registration's own refusals may apply — the
// proxy secret still switching, another manager not ready for agents, no room
// for another agent, a name or an address an agent may not have or another
// node has, no CA key to sign with. A refusal wraps ErrConvertRefused.
func (m *Manager) ReadyForAgentConversion(id string) error {
	if m.raft == nil || !m.raft.IsLeader() {
		return ErrNotLeader
	}
	state := m.raft.GetFSM().GetState()
	node, ok := state.Nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	if id == m.nodeID {
		return refuseConversion(ErrConvertLeader)
	}
	if !node.IsManager() {
		return refuseConversion(ErrConvertNotManager)
	}
	name := node.Name
	if name == "" {
		name = id
	}
	// The node leaves and registers again itself, on code that knows agents.
	health := m.heartbeat.CheckHealth()
	switch met := m.heartbeat.GetMetrics(id); {
	case met == nil:
		return refuseConversion(fmt.Errorf("%s has not been heard from: it leaves the cluster and registers again itself, so it must be online", name))
	case health[id] != StatusOnline:
		return refuseConversion(fmt.Errorf("%s is %s: it leaves the cluster and registers again itself, so it must be online", name, health[id]))
	case !slices.Contains(met.Capabilities, CapAgents):
		return refuseConversion(fmt.Errorf("%s runs %s, which predates agents: upgrade it first", name, met.Version))
	}
	// Counted as the leave's own quorum guard counts, each voter by its
	// heartbeat. This leader is up — it is answering — whether or not its
	// own metrics loop has recorded it.
	voters := make(map[string]NodeStatus, len(state.Nodes))
	for vid, n := range state.Nodes {
		if n.Role != RoleVoter {
			continue
		}
		status, ok := health[vid]
		if !ok {
			status = StatusOffline
		}
		voters[vid] = status
	}
	if _, ok := voters[m.nodeID]; ok {
		voters[m.nodeID] = StatusOnline
	}
	if reason, blocked := wouldDropBelowQuorumOnLeave(id, voters); blocked {
		return refuseConversion(fmt.Errorf("without %s the managers left would have no quorum: %s", name, reason))
	}
	// The registration's refusals, read as it reads them: without the node's
	// record, which its leave takes.
	if !m.ProxyRotationSettled(time.Now()) {
		return refuseConversion(ErrProxyRotationPending)
	}
	delete(state.Nodes, id)
	if waiting := m.managersNotReadyForAgents(state); len(waiting) > 0 {
		return refuseConversion(fmt.Errorf("%w; waiting for %s", ErrManagersNotReadyForAgents, strings.Join(waiting, ", ")))
	}
	if countKind(state, KindAgent) >= m.agentCap() {
		return refuseConversion(ErrMaxAgentsReached)
	}
	if err := ValidNodeName(node.Name); err != nil {
		return refuseConversion(err)
	}
	ip, ok := ipAndPort(node.GRPCAddress)
	if !ok {
		return refuseConversion(fmt.Errorf("the cluster has %s at %q, which is not an IP and a port: an agent registers at an IP, which its certificate names", name, node.GRPCAddress))
	}
	if identityTaken(state, node.Name, ip) {
		return refuseConversion(fmt.Errorf("%w: %s or %s", ErrNodeIdentityTaken, node.Name, ip))
	}
	if !m.tls.HasCAKey() && state.Config[configKeyCAKey] == "" {
		return refuseConversion(ErrCAKeyUnavailable)
	}
	return nil
}

// PrepareAgentConversion readies the conversion of the manager id to an agent
// (leader only). Once every gate has passed it makes a token that admits id
// alone, at the IP of its gRPC address, and records what the node is known
// by — its name, labels and join time — for its registration under its own
// ID to take back: its leave removes its record. Nothing else changes until
// the node, handed the token, leaves.
func (m *Manager) PrepareAgentConversion(id string) (*JoinToken, error) {
	if m.raft == nil || !m.raft.IsLeader() {
		return nil, ErrNotLeader
	}
	// As in HandleJoin: still the leader of a quorum, and the FSM holds what
	// earlier terms committed — the gates read it.
	if err := m.raft.Barrier(2 * time.Second); err != nil {
		return nil, fmt.Errorf("leader barrier failed: %w", err)
	}
	if err := m.ReadyForAgentConversion(id); err != nil {
		return nil, err
	}
	node := m.raft.GetFSM().GetNode(id)
	if node == nil {
		return nil, ErrNodeNotFound
	}
	ip, _ := ipAndPort(node.GRPCAddress) // an IP: the gates refused any other
	tok, err := m.CreateBoundJoinToken(conversionTokenTTL, KindAgent, id, ip.String())
	if err != nil {
		return nil, err
	}
	intent := ConversionIntent{Name: node.Name, Labels: node.Labels, JoinedAt: node.JoinedAt}
	if err := m.SetConfig(ConversionIntentKey(id), string(mustJSON(intent))); err != nil {
		if rerr := m.tokens.Revoke(tokenFingerprint(tok.Token)); rerr != nil {
			slog.Warn("a conversion's token was not revoked", "component", "cluster", "node_id", id, "error", rerr)
		}
		return nil, fmt.Errorf("record the conversion: %w", err)
	}
	slog.Info("conversion to an agent prepared", "component", "cluster", "node_id", id, "name", node.Name)
	return tok, nil
}

// AbandonAgentConversion takes back what PrepareAgentConversion made — the
// token and the intent record — for a node that refused its conversion. Not
// for one whose answer is unknown: that node may be on its way out, and it
// registers with the token.
func (m *Manager) AbandonAgentConversion(id string, tok *JoinToken) error {
	var errs []error
	if err := m.tokens.Revoke(tokenFingerprint(tok.Token)); err != nil && !errors.Is(err, ErrTokenNotFound) {
		errs = append(errs, fmt.Errorf("revoke the token: %w", err))
	}
	if m.raft == nil {
		return errors.Join(append(errs, ErrNotLeader)...)
	}
	if err := m.raft.Apply(Command{Type: CmdDeleteConfig, Key: ConversionIntentKey(id)}, 5*time.Second); err != nil {
		errs = append(errs, fmt.Errorf("remove the intent record: %w", err))
	}
	return errors.Join(errs...)
}

// ManagerFiles are the files a manager keeps that hold the cluster's keys
// and that an agent needs none of: the CA key; the Raft log, stable store and
// snapshots, whose state carries every agent's key, the accounts and the
// managers' proxy secret; the leader's join tokens; the proxy secret's floor;
// the CA rotation's floor; and a Raft recovery's peer lists. A directory left
// unset names nothing.
func ManagerFiles(c *config.ClusterConfig) []string {
	var files []string
	if c.CertDir != "" {
		files = append(files, filepath.Join(c.CertDir, "ca.key"))
	}
	if c.DataDir != "" {
		for _, f := range []string{"raft-log.db", "raft-stable.db", "snapshots", "tokens.json", proxySecretFloorFile, caRotationFloorFile, "peers.json", "peers.info"} {
			files = append(files, filepath.Join(c.DataDir, f))
		}
	}
	return files
}

// WipeManagerFiles removes ManagerFiles(c) and returns the ones it removed.
// It goes on past one that will not go, and returns every failure.
func WipeManagerFiles(c *config.ClusterConfig) (removed []string, err error) {
	var errs []error
	for _, p := range ManagerFiles(c) {
		if _, statErr := os.Lstat(p); errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if rmErr := os.RemoveAll(p); rmErr != nil {
			errs = append(errs, rmErr)
			continue
		}
		removed = append(removed, p)
	}
	return removed, errors.Join(errs...)
}

// ManagerBackups is the scheduled-backup directory when it holds a backup,
// else "". A backup archives sfpanel.db and config.yaml, so one a manager
// took holds the cluster's secrets until they are rotated. A conversion
// leaves them — they are the operator's — and says where they are. The
// directory is the database's "backups" sibling, where the backup scheduler
// keeps it (backupDirFor in internal/feature/system).
func ManagerBackups(cfg *config.Config) string {
	if cfg.Database.Path == "" {
		return ""
	}
	dir := filepath.Join(filepath.Dir(cfg.Database.Path), "backups")
	if entries, err := os.ReadDir(dir); err != nil || len(entries) == 0 {
		return ""
	}
	return dir
}

// ConvertRequest is what a converting manager registers again with: the
// token the leader made for it, the fingerprint of the cluster CA and the
// leader's gRPC address, the address to register at, and the CA behind its
// panel's certificate with the check that the panel can be reached with it —
// both from paneltls, which this package does not import.
type ConvertRequest struct {
	Token, Pin, Manager, Advertise string
	PanelCA                        []byte
	CheckPanelCert                 func(string) error
}

// ConvertToAgent is a conversion on the converting manager itself, run in its
// own process with its live manager mgr, its config cfg at cfgPath and its
// database db once the leader's gates have passed (PrepareAgentConversion).
// Each step runs only once the one before it has succeeded:
//
//  1. It leaves the cluster (Manager.Leave), and the leader removes its record
//     and its Raft membership. A leave that fails ends it there with nothing
//     else changed, and the node restarts as a manager; when no leader
//     confirmed the leave, ErrLeaveUnacknowledged says what to check.
//  2. It saves a standalone config with a JWT key of its own.
//  3. It removes what a manager keeps: ManagerFiles, the local admin account
//     and the copies an update leaves of the config and the database.
//  4. It registers under its own ID and name (AgentJoinEngine), which writes
//     the agent's files and config.
//
// The caller restarts the process afterwards whatever the outcome: a node
// runs one role per boot.
func ConvertToAgent(mgr *Manager, cfg *config.Config, cfgPath string, db *sql.DB, version string, req ConvertRequest) error {
	// Refused while nothing has changed: every config write below needs it.
	if cfgPath == "" {
		return errors.New("convert to an agent: no config file to write the agent's config to")
	}
	id, name := mgr.LocalNodeID(), mgr.nodeName
	if n := mgr.GetNode(id); n != nil && n.Name != "" {
		name = n.Name // what the cluster knows it by, and the gates checked
	}
	slog.Info("converting to an agent: leaving the cluster", "component", "cluster", "step", "leave", "node_id", id)
	if err := mgr.Leave(); err != nil {
		return fmt.Errorf("leave the cluster: %w", err)
	}
	return convertLeftManager(id, name, cfg, cfgPath, db, version, req)
}

// convertLeftManager is the rest of a conversion, once the node has left the
// cluster: steps 2 to 4 of ConvertToAgent.
func convertLeftManager(id, name string, cfg *config.Config, cfgPath string, db *sql.DB, version string, req ConvertRequest) error {
	log := slog.With("component", "cluster", "node_id", id)

	// The manager config names a membership the leave has ended. Saved
	// standalone before the wipe, so that every stop from here — a file that
	// will not go, a refused registration, a crash — boots a standalone panel,
	// not a manager with no Raft that holds the cluster's JWT key; and 'agent
	// join' there names any file left (readyForAgentJoin).
	log.Info("converting to an agent: saving a standalone config", "step", "standalone")
	next := *cfg
	next.Cluster.Enabled = false
	next.Auth.JWTSecret = config.NewSecret()
	data, err := yaml.Marshal(&next)
	if err != nil {
		return fmt.Errorf("encode the standalone config: %w", err)
	}
	if err := replaceFile(cfgPath, data); err != nil {
		return fmt.Errorf("save the standalone config: %w", err)
	}
	*cfg = next

	const join = "join it as an agent with 'sfpanel agent join' and a token from 'sfpanel cluster token --agent'"
	log.Info("converting to an agent: removing what a manager keeps", "step", "wipe")
	if err := wipeManagerSecrets(cfg, cfgPath, db); err != nil {
		// No registration: an agent keeps none of the cluster's keys.
		return fmt.Errorf("remove a manager's secrets: %w — this server is a standalone panel now; remove them by hand, then %s", err, join)
	}
	if dir := ManagerBackups(cfg); dir != "" {
		log.Warn("backups taken while this node was a manager hold the cluster's secrets until they are rotated", "dir", dir)
	}

	log.Info("converting to an agent: registering under its own ID", "step", "register")
	e := &AgentJoinEngine{Config: cfg, ConfigPath: cfgPath, Version: version, Name: name, NodeID: id, CheckPanelCert: req.CheckPanelCert}
	if _, err := e.Execute(req.Manager, req.Token, req.Advertise, req.Pin, req.PanelCA); err != nil {
		return fmt.Errorf("register as an agent: %w — this server is a standalone panel now: %s", err, join)
	}
	log.Info("converted to an agent", "step", "done", "name", name)
	return nil
}

// wipeManagerSecrets removes what a manager keeps that an agent must not
// hold: the files with the cluster's keys (ManagerFiles), the local admin
// account — its password hash, its TOTP seed in plain text, its recovery
// codes — and the copies an update leaves of the config and the database.
// It removes what it can, and names what would not go.
func wipeManagerSecrets(cfg *config.Config, cfgPath string, db *sql.DB) error {
	removed, err := WipeManagerFiles(&cfg.Cluster)
	errs := []error{err}
	slog.Info("removed a manager's files", "component", "cluster", "files", removed)
	if db != nil {
		if _, err := db.Exec(`DELETE FROM admin`); err != nil {
			errs = append(errs, fmt.Errorf("remove the local admin account: %w", err))
		}
	}
	for _, p := range []string{cfgPath, cfg.Database.Path} {
		if p == "" {
			continue
		}
		if err := os.Remove(p + ".bak"); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// refusePromotion is a gate's refusal of a promotion: nothing has changed.
func refusePromotion(reason error) error {
	return fmt.Errorf("%w: %w", ErrPromoteRefused, reason)
}

// PrepareManagerPromotion readies the promotion of the agent id to a manager
// (leader only). The gates come first, while a refusal changes nothing: the
// node must be an agent and online — it joins again itself — and its join
// must have nothing to be refused for that can be told here: no room for
// another manager, no CA key to sign its certificate with, a record address
// the token cannot be bound to. Met only at the join, those would restart
// the agent for nothing. Then it makes a manager token that admits id alone,
// at the IP of its gRPC address. Nothing else changes until the node joins
// with it, and the join (HandleJoin) turns the agent's record into a
// manager's, keeping its name and labels. A refusal wraps ErrPromoteRefused.
func (m *Manager) PrepareManagerPromotion(id string) (*JoinToken, error) {
	if m.raft == nil || !m.raft.IsLeader() {
		return nil, ErrNotLeader
	}
	// As in PrepareAgentConversion: the gates read what earlier terms
	// committed.
	if err := m.raft.Barrier(2 * time.Second); err != nil {
		return nil, fmt.Errorf("leader barrier failed: %w", err)
	}
	state := m.raft.GetFSM().GetState()
	node, ok := state.Nodes[id]
	if !ok {
		return nil, ErrNodeNotFound
	}
	if node.IsManager() {
		return nil, refusePromotion(ErrPromoteNotAgent)
	}
	name := node.Name
	if name == "" {
		name = id
	}
	health := m.heartbeat.CheckHealth()
	switch met := m.heartbeat.GetMetrics(id); {
	case met == nil:
		return nil, refusePromotion(fmt.Errorf("%s has not been heard from: it joins the cluster again itself, so it must be online", name))
	case health[id] != StatusOnline:
		return nil, refusePromotion(fmt.Errorf("%s is %s: it joins the cluster again itself, so it must be online", name, health[id]))
	}
	if countKind(state, KindManager) >= MaxNodes {
		return nil, refusePromotion(ErrMaxNodesReached)
	}
	if !m.tls.HasCAKey() && state.Config[configKeyCAKey] == "" {
		return nil, refusePromotion(ErrCAKeyUnavailable)
	}
	ip, ok := ipAndPort(node.GRPCAddress)
	if !ok {
		return nil, refusePromotion(fmt.Errorf("the cluster has %s at %q, which is not an IP and a port: the token it joins with is bound to the IP of its address", name, node.GRPCAddress))
	}
	tok, err := m.CreateBoundJoinToken(conversionTokenTTL, KindManager, id, ip.String())
	if err != nil {
		return nil, err
	}
	slog.Info("promotion to a manager prepared", "component", "cluster", "node_id", id, "name", name)
	return tok, nil
}

// AbandonManagerPromotion takes back the token PrepareManagerPromotion made,
// for a promotion the node refused or may not have started. Revoked once a
// join has spent it, it changes nothing.
func (m *Manager) AbandonManagerPromotion(tok *JoinToken) error {
	if err := m.tokens.Revoke(tokenFingerprint(tok.Token)); err != nil && !errors.Is(err, ErrTokenNotFound) {
		return fmt.Errorf("revoke the token: %w", err)
	}
	return nil
}

// agentOwnFiles are the files an agent keeps that a manager has no use
// for: its internal-proxy key, which the leader removes from replicated state
// when it makes the node a manager, and its link's state.
func agentOwnFiles(c *config.ClusterConfig) []string {
	var files []string
	if c.CertDir != "" {
		files = append(files, filepath.Join(c.CertDir, "agent.key"))
	}
	if c.DataDir != "" {
		files = append(files, filepath.Join(c.DataDir, AgentStateFile))
	}
	return files
}

// PromoteToManager is an agent's promotion to a manager, run in the agent's
// own process with its config cfg at cfgPath and its database db once the
// leader's gates have passed (PrepareManagerPromotion). req carries the token
// the leader made for it, the cluster CA's fingerprint, the leader's gRPC
// address and the address this server advertises. Each step runs only once
// the one before it has succeeded:
//
//  1. It removes what an earlier membership as a manager left (ManagerFiles)
//     while the server is still an agent, which uses none of it: once the
//     config is a manager's, the manager opens the Raft log and stable store
//     it finds, their old term and peers with them.
//  2. It joins as a manager under its own ID and name (JoinEngine). The
//     leader turns the agent's record into a manager's, keeping its name and
//     labels, and the engine writes the manager's certificate and config: no
//     role, no managers list, the cluster's JWT key. The leader first dials
//     this server's Raft port, which the caller holds open through this call
//     (BecomeManager), and refuses a server it cannot reach there
//     (ErrRaftPortUnreachable). A join the leader refuses writes nothing. One
//     it admitted that this server then cannot finish — an answer that fails
//     the pin, a file it cannot write — leaves the leader listing a manager
//     this server is not: remove the node on a manager and join this server
//     again.
//  3. It removes the agent's own files (agentOwnFiles).
//
// On success cfg is the config it saved. The caller restarts the process
// afterwards whatever the outcome: a node runs one role per boot, and an
// agent whose promotion stopped before its join comes back as that agent.
func PromoteToManager(cfg *config.Config, cfgPath string, db *sql.DB, req ConvertRequest) error {
	// Refused while nothing has changed.
	if !cfg.Cluster.IsAgent() {
		return errors.New("promote to a manager: this server is not an agent")
	}
	if cfgPath == "" {
		return errors.New("promote to a manager: no config file to write the manager's config to")
	}
	id := cfg.Cluster.NodeID
	log := slog.With("component", "cluster", "node_id", id)

	log.Info("promoting to a manager: removing what an earlier membership left", "step", "leftovers")
	removed, err := WipeManagerFiles(&cfg.Cluster)
	if err != nil {
		return fmt.Errorf("remove what an earlier membership as a manager left: %w", err)
	}
	if len(removed) > 0 {
		log.Warn("removed the files an earlier membership as a manager left", "files", removed)
	}

	log.Info("promoting to a manager: joining under its own ID", "step", "join", "manager", req.Manager)
	next := *cfg
	next.Cluster.Enabled = false
	next.Cluster.Role = ""
	next.Cluster.Managers = nil
	e := &JoinEngine{Config: &next, ConfigPath: cfgPath, DB: db, NodeID: id, Name: cfg.Cluster.NodeName, SkipPortCheck: true}
	if _, err := e.Execute(req.Manager, req.Token, req.Advertise, req.Pin); err != nil {
		return fmt.Errorf("join as a manager: %w", err)
	}
	*cfg = next

	log.Info("promoting to a manager: removing the agent's files", "step", "agent-files")
	var errs []error
	for _, p := range agentOwnFiles(&next.Cluster) {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("joined the cluster as a manager, but the agent's files stayed: %w — a manager does not use them; remove them by hand", err)
	}
	log.Info("promoted to a manager", "step", "done")
	return nil
}
