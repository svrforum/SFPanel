package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/svrforum/SFPanel/internal/feature/alert/channels"
	"github.com/svrforum/SFPanel/internal/monitor"
)

var logger = slog.Default().With("component", "alert")

// NodeIdentity supplies the local cluster node ID so the manager can filter
// rules whose `node_scope` restricts them to a subset of voters. Pass nil
// from main.go when the cluster feature is disabled — the manager then
// treats every rule as "apply here" (single-node behavior).
//
// *cluster.Manager already satisfies this with its LocalNodeID() method, so
// nothing in cmd/sfpanel/main.go needs an adapter. The interface exists to
// keep this package free of the heavyweight cluster import (and to make
// table-driven tests trivial — see fakeNodeIdentity in manager_test.go).
type NodeIdentity interface {
	LocalNodeID() string
}

// fireQueueDepth bounds the number of pending notification dispatches. Sends
// are the slow part (each webhook POST has a 10s timeout), so we move them off
// the caller's goroutine — the periodic evaluate ticker and, more importantly,
// the single serial docker-events listener (internal/monitor/docker_events.go)
// must never block on a slow/hung webhook.
const fireQueueDepth = 256

// fireWorkers is the number of goroutines draining fireQueue concurrently.
const fireWorkers = 2

type Manager struct {
	db       *sql.DB
	identity NodeIdentity
	mu       sync.RWMutex
	lastSent map[int]time.Time // rule_id -> last successful send (cooldown)
	inFlight map[int]bool      // rule_id -> a dispatch is queued/running
	cancel   context.CancelFunc

	fireQueue chan AlertFire
	wg        sync.WaitGroup

	// deliver performs the actual (slow, network) channel sends for a fire and
	// returns the names of channels that succeeded. A field so tests can
	// substitute a fast/blocking stub; defaults to deliverToChannels.
	deliver func(f AlertFire) []string
}

// NewManager constructs a single-node alert manager (no cluster filtering).
func NewManager(db *sql.DB) *Manager {
	return NewManagerWithIdentity(db, nil)
}

// NewManagerWithIdentity wires the cluster node identity so rules tagged
// with `node_scope="specific"` only fire on nodes whose ID appears in
// `node_ids`. Pass nil for single-node deployments.
func NewManagerWithIdentity(db *sql.DB, identity NodeIdentity) *Manager {
	m := &Manager{
		db:        db,
		identity:  identity,
		lastSent:  make(map[int]time.Time),
		inFlight:  make(map[int]bool),
		fireQueue: make(chan AlertFire, fireQueueDepth),
	}
	m.deliver = m.deliverToChannels
	return m
}

func (m *Manager) Start(ctx context.Context) {
	ctx, m.cancel = context.WithCancel(ctx)

	// Background dispatch workers drain fireQueue so notification sends never
	// block the caller (evaluate ticker / docker-events listener).
	for i := 0; i < fireWorkers; i++ {
		m.wg.Add(1)
		go m.dispatchWorker(ctx)
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	logger.Info("alert manager started", "interval", "60s")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.evaluate()
		}
	}
}

func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}

// dispatchWorker drains queued fires and performs the slow channel sends.
func (m *Manager) dispatchWorker(ctx context.Context) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case f := <-m.fireQueue:
			m.runFire(f)
		}
	}
}

type ruleCondition struct {
	Operator  string  `json:"operator"`  // ">", "<", ">=", "<="
	Threshold float64 `json:"threshold"` // e.g. 90.0
}

func (m *Manager) evaluate() {
	rows, err := m.db.Query("SELECT id, name, type, condition, channel_ids, severity, cooldown, node_scope, node_ids, enabled FROM alert_rules WHERE enabled=1")
	if err != nil {
		logger.Error("failed to load rules", "error", err)
		return
	}
	defer rows.Close()

	metrics, metricsErr := monitor.GetMetrics()

	for rows.Next() {
		var r AlertRule
		var enabled int
		if err := rows.Scan(&r.ID, &r.Name, &r.Type, &r.Condition, &r.ChannelIDs, &r.Severity, &r.Cooldown, &r.NodeScope, &r.NodeIDs, &enabled); err != nil {
			continue
		}

		// Cluster node-scope filter. In single-node mode (identity==nil)
		// every rule applies. In cluster mode, "specific" scope requires
		// the local node ID to appear in the rule's node_ids JSON array.
		if !ruleAppliesToNode(m.identity, r.NodeScope, r.NodeIDs) {
			continue
		}

		// Parse condition
		var cond ruleCondition
		if err := json.Unmarshal([]byte(r.Condition), &cond); err != nil {
			logger.Warn("invalid condition JSON", "rule", r.Name, "error", err)
			continue
		}

		// Get current value based on type
		var currentValue float64
		var valueLabel string
		switch r.Type {
		case "cpu":
			if metricsErr != nil {
				continue
			}
			currentValue = metrics.CPU
			valueLabel = fmt.Sprintf("%.1f%%", currentValue)
		case "memory":
			if metricsErr != nil {
				continue
			}
			currentValue = metrics.MemPercent
			valueLabel = fmt.Sprintf("%.1f%%", currentValue)
		case "disk":
			if metricsErr != nil {
				continue
			}
			currentValue = metrics.DiskPercent
			valueLabel = fmt.Sprintf("%.1f%%", currentValue)
		default:
			continue
		}

		// Evaluate condition
		triggered := false
		switch cond.Operator {
		case ">":
			triggered = currentValue > cond.Threshold
		case ">=":
			triggered = currentValue >= cond.Threshold
		case "<":
			triggered = currentValue < cond.Threshold
		case "<=":
			triggered = currentValue <= cond.Threshold
		default:
			triggered = currentValue > cond.Threshold
		}

		if !triggered {
			continue
		}

		message := fmt.Sprintf("%s usage is %s (threshold: %s %.0f%%)", r.Type, valueLabel, cond.Operator, cond.Threshold)

		// evaluate() doesn't take a context; pass Background for now.
		m.Fire(context.Background(), AlertFire{
			RuleID:     r.ID,
			RuleName:   r.Name,
			Type:       r.Type,
			Severity:   r.Severity,
			Message:    message,
			ChannelIDs: r.ChannelIDs,
			Cooldown:   r.Cooldown,
		})
	}
}

// Fire gates an AlertFire through the cooldown + in-flight check and enqueues
// it for asynchronous delivery. It never performs network I/O itself, so it is
// safe to call from the serial docker-events listener and the evaluate ticker.
//
// The cooldown read and the in-flight reservation happen atomically under a
// single lock, so two concurrent fires for the same rule (container-event path
// + periodic evaluate) cannot both pass the gate and double-send.
func (m *Manager) Fire(_ context.Context, f AlertFire) {
	m.mu.Lock()
	if lastSent, ok := m.lastSent[f.RuleID]; ok &&
		time.Since(lastSent) < time.Duration(f.Cooldown)*time.Second {
		m.mu.Unlock()
		return
	}
	if m.inFlight[f.RuleID] {
		m.mu.Unlock() // a dispatch for this rule is already queued/running
		return
	}
	m.inFlight[f.RuleID] = true
	m.mu.Unlock()

	select {
	case m.fireQueue <- f:
	default:
		// Queue saturated: drop this fire and release the reservation so a
		// later fire for the same rule can retry rather than being wedged.
		m.mu.Lock()
		delete(m.inFlight, f.RuleID)
		m.mu.Unlock()
		logger.Warn("alert dispatch queue full, dropping fire", "rule", f.RuleName, "type", f.Type)
	}
}

// runFire performs the slow send for one queued fire and updates cooldown,
// in-flight, and history. Runs on a dispatchWorker goroutine.
func (m *Manager) runFire(f AlertFire) {
	sentChannelNames := m.deliver(f)

	m.mu.Lock()
	delete(m.inFlight, f.RuleID)
	if len(sentChannelNames) > 0 {
		// Cooldown starts only after a successful send, so a transient
		// all-channels-down failure is retried on the next fire.
		m.lastSent[f.RuleID] = time.Now()
	}
	m.mu.Unlock()

	// A fire that reached nobody is still a fire, and the one worth seeing.
	//
	// The history row used to be written only on a successful send, so an
	// alert whose channels had all failed — a rotated Discord webhook, a
	// deleted channel, a webhook host that is down — left nothing behind but
	// a line in the server log. The rule went on listing itself as Active,
	// the history stayed empty, and the operator's evidence that alerting
	// works was the absence of evidence that it does not. sent_channels is an
	// empty array in that case, which is exactly what happened.
	if len(sentChannelNames) == 0 {
		logger.Warn("all channel sends failed", "rule", f.RuleName, "type", f.Type)
	}

	sentJSON, _ := json.Marshal(sentChannelNames)
	if _, err := m.db.Exec("INSERT INTO alert_history (rule_id, rule_name, type, severity, message, node_id, sent_channels) VALUES (?,?,?,?,?,?,?)",
		f.RuleID, f.RuleName, f.Type, f.Severity, f.Message, "", string(sentJSON)); err != nil {
		logger.Warn("failed to record history", "error", err)
	}
	logger.Info("triggered", "rule", f.RuleName, "type", f.Type, "severity", f.Severity,
		"delivered", len(sentChannelNames))
}

// deliverToChannels routes a fire to its configured channels and returns the
// names of those that accepted it. This is the slow path (one webhook POST per
// channel, each with its own timeout) and always runs on a worker goroutine.
func (m *Manager) deliverToChannels(f AlertFire) []string {
	title := fmt.Sprintf("SFPanel Alert: %s", f.RuleName)

	var channelIDs []int
	if err := json.Unmarshal([]byte(f.ChannelIDs), &channelIDs); err != nil {
		logger.Warn("invalid channel_ids JSON", "rule", f.RuleName, "error", err)
		return nil
	}

	sentChannelNames := make([]string, 0, len(channelIDs))
	for _, chID := range channelIDs {
		var ch AlertChannel
		var chEnabled int
		err := m.db.QueryRow("SELECT id, type, name, config, enabled FROM alert_channels WHERE id=?", chID).
			Scan(&ch.ID, &ch.Type, &ch.Name, &ch.Config, &chEnabled)
		if err != nil || chEnabled != 1 {
			continue
		}

		var sendErr error
		switch ch.Type {
		case "discord":
			var cfg struct {
				WebhookURL string `json:"webhook_url"`
			}
			if err := json.Unmarshal([]byte(ch.Config), &cfg); err == nil && cfg.WebhookURL != "" {
				sendErr = channels.SendDiscord(cfg.WebhookURL, title, f.Message, f.Severity)
			}
		case "telegram":
			var cfg struct {
				BotToken string `json:"bot_token"`
				ChatID   string `json:"chat_id"`
			}
			if err := json.Unmarshal([]byte(ch.Config), &cfg); err == nil && cfg.BotToken != "" && cfg.ChatID != "" {
				sendErr = channels.SendTelegram(cfg.BotToken, cfg.ChatID, title, f.Message, f.Severity)
			}
		case "webhook":
			var cfg struct {
				WebhookURL string `json:"webhook_url"`
			}
			if err := json.Unmarshal([]byte(ch.Config), &cfg); err == nil && cfg.WebhookURL != "" {
				sendErr = channels.SendWebhook(cfg.WebhookURL, title, f.Message, f.Severity)
			}
		}

		if sendErr != nil {
			logger.Warn("send failed", "channel", ch.Name, "error", sendErr)
		} else {
			sentChannelNames = append(sentChannelNames, ch.Name)
		}
	}
	return sentChannelNames
}

// ruleAppliesToNode decides whether a rule with the given node_scope /
// node_ids should be evaluated on the local node.
//
// Semantics (mirrors the schema default node_scope="all"):
//   - identity == nil          → single-node mode, always true
//   - scope == "" or "all"     → every node evaluates
//   - scope == "specific"      → only nodes whose ID appears in nodeIDsJSON
//   - any other scope value    → conservatively skip (fail-closed)
//
// Malformed nodeIDsJSON fails closed (skip) so a misconfigured rule can't
// silently fan out to every node. The router-side handler already
// normalizes empty input to "[]" on create/update, so this only fires for
// hand-edited DB rows.
func ruleAppliesToNode(identity NodeIdentity, scope, nodeIDsJSON string) bool {
	if identity == nil {
		return true
	}
	switch scope {
	case "", "all":
		return true
	case "specific":
		local := identity.LocalNodeID()
		if local == "" {
			return false
		}
		var ids []string
		if err := json.Unmarshal([]byte(nodeIDsJSON), &ids); err != nil {
			return false
		}
		for _, id := range ids {
			if id == local {
				return true
			}
		}
		return false
	default:
		return false
	}
}
