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

type Manager struct {
	db       *sql.DB
	mu       sync.RWMutex
	lastSent map[int]time.Time // rule_id -> last sent time (cooldown)
	cancel   context.CancelFunc
}

func NewManager(db *sql.DB) *Manager {
	return &Manager{
		db:       db,
		lastSent: make(map[int]time.Time),
	}
}

func (m *Manager) Start(ctx context.Context) {
	ctx, m.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	slog.Info("alert manager started", "interval", "60s")
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
		slog.Error("alert: failed to load rules", "error", err)
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

		// Check cooldown
		m.mu.RLock()
		lastSent, hasCooldown := m.lastSent[r.ID]
		m.mu.RUnlock()
		if hasCooldown && time.Since(lastSent) < time.Duration(r.Cooldown)*time.Second {
			continue
		}

		// Parse condition
		var cond ruleCondition
		if err := json.Unmarshal([]byte(r.Condition), &cond); err != nil {
			slog.Warn("alert: invalid condition JSON", "rule", r.Name, "error", err)
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
		title := fmt.Sprintf("SFPanel Alert: %s", r.Name)

		// Send to channels
		var channelIDs []int
		json.Unmarshal([]byte(r.ChannelIDs), &channelIDs)

		sentChannelNames := make([]string, 0)
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
					sendErr = channels.SendDiscord(cfg.WebhookURL, title, message, r.Severity)
				}
			case "telegram":
				var cfg struct {
					BotToken string `json:"bot_token"`
					ChatID   string `json:"chat_id"`
				}
				if err := json.Unmarshal([]byte(ch.Config), &cfg); err == nil && cfg.BotToken != "" && cfg.ChatID != "" {
					sendErr = channels.SendTelegram(cfg.BotToken, cfg.ChatID, title, message, r.Severity)
				}
			}

			if sendErr != nil {
				slog.Warn("alert: send failed", "channel", ch.Name, "error", sendErr)
			} else {
				sentChannelNames = append(sentChannelNames, ch.Name)
			}
		}

		// Record cooldown only if at least one channel send succeeded
		if len(sentChannelNames) > 0 {
			m.mu.Lock()
			m.lastSent[r.ID] = time.Now()
			m.mu.Unlock()
		}

		// Store in history only if at least one channel send succeeded
		if len(sentChannelNames) > 0 {
			sentJSON, _ := json.Marshal(sentChannelNames)
			_, err := m.db.Exec("INSERT INTO alert_history (rule_id, rule_name, type, severity, message, node_id, sent_channels) VALUES (?,?,?,?,?,?,?)",
				r.ID, r.Name, r.Type, r.Severity, message, "", string(sentJSON))
			if err != nil {
				slog.Warn("alert: failed to record history", "error", err)
			}
			slog.Info("alert triggered", "rule", r.Name, "type", r.Type, "value", valueLabel, "severity", r.Severity)
		} else {
			slog.Warn("alert: all channel sends failed, skipping history", "rule", r.Name, "type", r.Type)
		}
	}
}
