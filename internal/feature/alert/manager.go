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

type Manager struct {
	db       *sql.DB
	identity NodeIdentity
	mu       sync.RWMutex
	lastSent map[int]time.Time // rule_id -> last sent time (cooldown)
	cancel   context.CancelFunc
}

// NewManager constructs a single-node alert manager (no cluster filtering).
func NewManager(db *sql.DB) *Manager {
	return NewManagerWithIdentity(db, nil)
}

// NewManagerWithIdentity wires the cluster node identity so rules tagged
// with `node_scope="specific"` only fire on nodes whose ID appears in
// `node_ids`. Pass nil for single-node deployments.
func NewManagerWithIdentity(db *sql.DB, identity NodeIdentity) *Manager {
	return &Manager{
		db:       db,
		identity: identity,
		lastSent: make(map[int]time.Time),
	}
}

func (m *Manager) Start(ctx context.Context) {
	ctx, m.cancel = context.WithCancel(ctx)
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

// Fire delivers an AlertFire through cooldown gate → channel routing →
// alert_history insert. Returns silently if cooldown blocks the fire.
// Used by ContainerDispatcher and by Manager.evaluate after refactor.
func (m *Manager) Fire(_ context.Context, f AlertFire) {
	// Cooldown gate
	m.mu.RLock()
	lastSent, hasCooldown := m.lastSent[f.RuleID]
	m.mu.RUnlock()
	if hasCooldown && time.Since(lastSent) < time.Duration(f.Cooldown)*time.Second {
		return
	}

	title := fmt.Sprintf("SFPanel Alert: %s", f.RuleName)

	// Channel routing
	var channelIDs []int
	if err := json.Unmarshal([]byte(f.ChannelIDs), &channelIDs); err != nil {
		logger.Warn("invalid channel_ids JSON", "rule", f.RuleName, "error", err)
		return
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
		}

		if sendErr != nil {
			logger.Warn("send failed", "channel", ch.Name, "error", sendErr)
		} else {
			sentChannelNames = append(sentChannelNames, ch.Name)
		}
	}

	// Cooldown + history (only if at least one channel succeeded)
	if len(sentChannelNames) > 0 {
		m.mu.Lock()
		m.lastSent[f.RuleID] = time.Now()
		m.mu.Unlock()

		sentJSON, _ := json.Marshal(sentChannelNames)
		_, err := m.db.Exec("INSERT INTO alert_history (rule_id, rule_name, type, severity, message, node_id, sent_channels) VALUES (?,?,?,?,?,?,?)",
			f.RuleID, f.RuleName, f.Type, f.Severity, f.Message, "", string(sentJSON))
		if err != nil {
			logger.Warn("failed to record history", "error", err)
		}
		logger.Info("triggered", "rule", f.RuleName, "type", f.Type, "severity", f.Severity)
	} else {
		logger.Warn("all channel sends failed, skipping history", "rule", f.RuleName, "type", f.Type)
	}
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
