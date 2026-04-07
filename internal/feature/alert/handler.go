package alert

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/feature/alert/channels"
)

type Handler struct {
	DB      *sql.DB
	Manager *Manager
}

type AlertChannel struct {
	ID        int    `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	Config    string `json:"config"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
}

type AlertRule struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Condition  string `json:"condition"`
	ChannelIDs string `json:"channel_ids"`
	Severity   string `json:"severity"`
	Cooldown   int    `json:"cooldown"`
	NodeScope  string `json:"node_scope"`
	NodeIDs    string `json:"node_ids"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  string `json:"created_at"`
}

type AlertHistoryEntry struct {
	ID           int    `json:"id"`
	RuleID       int    `json:"rule_id"`
	RuleName     string `json:"rule_name"`
	Type         string `json:"type"`
	Severity     string `json:"severity"`
	Message      string `json:"message"`
	NodeID       string `json:"node_id"`
	SentChannels string `json:"sent_channels"`
	CreatedAt    string `json:"created_at"`
}

// --- Channel CRUD ---

func (h *Handler) ListChannels(c echo.Context) error {
	rows, err := h.DB.Query("SELECT id, type, name, config, enabled, created_at FROM alert_channels ORDER BY id")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to list channels")
	}
	defer rows.Close()

	var list []AlertChannel
	for rows.Next() {
		var ch AlertChannel
		var enabled int
		if err := rows.Scan(&ch.ID, &ch.Type, &ch.Name, &ch.Config, &enabled, &ch.CreatedAt); err != nil {
			continue
		}
		ch.Enabled = enabled == 1
		list = append(list, ch)
	}
	if list == nil {
		list = []AlertChannel{}
	}
	return response.OK(c, list)
}

func (h *Handler) CreateChannel(c echo.Context) error {
	var req struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Config  string `json:"config"`
		Enabled *bool  `json:"enabled"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	if req.Type != "discord" && req.Type != "telegram" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "type must be discord or telegram")
	}
	if req.Name == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "name is required")
	}
	if !json.Valid([]byte(req.Config)) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "config must be valid JSON")
	}

	enabled := 1
	if req.Enabled != nil && !*req.Enabled {
		enabled = 0
	}

	result, err := h.DB.Exec("INSERT INTO alert_channels (type, name, config, enabled) VALUES (?, ?, ?, ?)",
		req.Type, req.Name, req.Config, enabled)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to create channel")
	}
	id, _ := result.LastInsertId()
	return response.OK(c, map[string]int64{"id": id})
}

func (h *Handler) UpdateChannel(c echo.Context) error {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "invalid channel id")
	}

	var req struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Config  string `json:"config"`
		Enabled *bool  `json:"enabled"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	if req.Type != "" && req.Type != "discord" && req.Type != "telegram" {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "type must be discord or telegram")
	}
	if req.Config != "" && !json.Valid([]byte(req.Config)) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "config must be valid JSON")
	}

	enabled := 1
	if req.Enabled != nil && !*req.Enabled {
		enabled = 0
	}

	res, err := h.DB.Exec("UPDATE alert_channels SET type=COALESCE(NULLIF(?,''),type), name=COALESCE(NULLIF(?,''),name), config=COALESCE(NULLIF(?,''),config), enabled=? WHERE id=?",
		req.Type, req.Name, req.Config, enabled, id)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to update channel")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "channel not found")
	}
	return response.OK(c, nil)
}

func (h *Handler) DeleteChannel(c echo.Context) error {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "invalid channel id")
	}
	res, err := h.DB.Exec("DELETE FROM alert_channels WHERE id=?", id)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to delete channel")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "channel not found")
	}
	return response.OK(c, nil)
}

func (h *Handler) TestChannel(c echo.Context) error {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "invalid channel id")
	}

	var ch AlertChannel
	var enabled int
	err = h.DB.QueryRow("SELECT id, type, name, config, enabled, created_at FROM alert_channels WHERE id=?", id).
		Scan(&ch.ID, &ch.Type, &ch.Name, &ch.Config, &enabled, &ch.CreatedAt)
	if err == sql.ErrNoRows {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "channel not found")
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to query channel")
	}

	title := "SFPanel Test Alert"
	message := "This is a test notification from SFPanel alert system."

	switch ch.Type {
	case "discord":
		var cfg struct {
			WebhookURL string `json:"webhook_url"`
		}
		if err := json.Unmarshal([]byte(ch.Config), &cfg); err != nil || cfg.WebhookURL == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "invalid discord config: webhook_url required")
		}
		if err := channels.SendDiscord(cfg.WebhookURL, title, message, "info"); err != nil {
			return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, err.Error())
		}
	case "telegram":
		var cfg struct {
			BotToken string `json:"bot_token"`
			ChatID   string `json:"chat_id"`
		}
		if err := json.Unmarshal([]byte(ch.Config), &cfg); err != nil || cfg.BotToken == "" || cfg.ChatID == "" {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "invalid telegram config: bot_token and chat_id required")
		}
		if err := channels.SendTelegram(cfg.BotToken, cfg.ChatID, title, message, "info"); err != nil {
			return response.Fail(c, http.StatusBadGateway, response.ErrInternalError, err.Error())
		}
	default:
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "unsupported channel type")
	}

	return response.OK(c, map[string]string{"status": "sent"})
}

// --- Rule CRUD ---

func (h *Handler) ListRules(c echo.Context) error {
	rows, err := h.DB.Query("SELECT id, name, type, condition, channel_ids, severity, cooldown, node_scope, node_ids, enabled, created_at FROM alert_rules ORDER BY id")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to list rules")
	}
	defer rows.Close()

	var list []AlertRule
	for rows.Next() {
		var r AlertRule
		var enabled int
		if err := rows.Scan(&r.ID, &r.Name, &r.Type, &r.Condition, &r.ChannelIDs, &r.Severity, &r.Cooldown, &r.NodeScope, &r.NodeIDs, &enabled, &r.CreatedAt); err != nil {
			continue
		}
		r.Enabled = enabled == 1
		list = append(list, r)
	}
	if list == nil {
		list = []AlertRule{}
	}
	return response.OK(c, list)
}

func (h *Handler) CreateRule(c echo.Context) error {
	var req struct {
		Name       string `json:"name"`
		Type       string `json:"type"`
		Condition  string `json:"condition"`
		ChannelIDs string `json:"channel_ids"`
		Severity   string `json:"severity"`
		Cooldown   *int   `json:"cooldown"`
		NodeScope  string `json:"node_scope"`
		NodeIDs    string `json:"node_ids"`
		Enabled    *bool  `json:"enabled"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	if req.Name == "" || req.Type == "" || req.Condition == "" {
		return response.Fail(c, http.StatusBadRequest, response.ErrMissingFields, "name, type, and condition are required")
	}
	if !json.Valid([]byte(req.Condition)) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "condition must be valid JSON")
	}

	if req.ChannelIDs == "" {
		req.ChannelIDs = "[]"
	}
	if req.Severity == "" {
		req.Severity = "warning"
	}
	cooldown := 300
	if req.Cooldown != nil {
		cooldown = *req.Cooldown
	}
	if req.NodeScope == "" {
		req.NodeScope = "all"
	}
	if req.NodeIDs == "" {
		req.NodeIDs = "[]"
	}
	enabled := 1
	if req.Enabled != nil && !*req.Enabled {
		enabled = 0
	}

	result, err := h.DB.Exec("INSERT INTO alert_rules (name, type, condition, channel_ids, severity, cooldown, node_scope, node_ids, enabled) VALUES (?,?,?,?,?,?,?,?,?)",
		req.Name, req.Type, req.Condition, req.ChannelIDs, req.Severity, cooldown, req.NodeScope, req.NodeIDs, enabled)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to create rule")
	}
	id, _ := result.LastInsertId()
	return response.OK(c, map[string]int64{"id": id})
}

func (h *Handler) UpdateRule(c echo.Context) error {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "invalid rule id")
	}

	var req struct {
		Name       *string `json:"name"`
		Type       *string `json:"type"`
		Condition  *string `json:"condition"`
		ChannelIDs *string `json:"channel_ids"`
		Severity   *string `json:"severity"`
		Cooldown   *int    `json:"cooldown"`
		NodeScope  *string `json:"node_scope"`
		NodeIDs    *string `json:"node_ids"`
		Enabled    *bool   `json:"enabled"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}

	// Read current rule
	var current AlertRule
	var currentEnabled int
	err = h.DB.QueryRow("SELECT id, name, type, condition, channel_ids, severity, cooldown, node_scope, node_ids, enabled, created_at FROM alert_rules WHERE id=?", id).
		Scan(&current.ID, &current.Name, &current.Type, &current.Condition, &current.ChannelIDs, &current.Severity, &current.Cooldown, &current.NodeScope, &current.NodeIDs, &currentEnabled, &current.CreatedAt)
	if err == sql.ErrNoRows {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "rule not found")
	}
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to query rule")
	}

	if req.Name != nil {
		current.Name = *req.Name
	}
	if req.Type != nil {
		current.Type = *req.Type
	}
	if req.Condition != nil {
		if !json.Valid([]byte(*req.Condition)) {
			return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "condition must be valid JSON")
		}
		current.Condition = *req.Condition
	}
	if req.ChannelIDs != nil {
		current.ChannelIDs = *req.ChannelIDs
	}
	if req.Severity != nil {
		current.Severity = *req.Severity
	}
	if req.Cooldown != nil {
		current.Cooldown = *req.Cooldown
	}
	if req.NodeScope != nil {
		current.NodeScope = *req.NodeScope
	}
	if req.NodeIDs != nil {
		current.NodeIDs = *req.NodeIDs
	}
	enabled := currentEnabled
	if req.Enabled != nil {
		if *req.Enabled {
			enabled = 1
		} else {
			enabled = 0
		}
	}

	_, err = h.DB.Exec("UPDATE alert_rules SET name=?, type=?, condition=?, channel_ids=?, severity=?, cooldown=?, node_scope=?, node_ids=?, enabled=? WHERE id=?",
		current.Name, current.Type, current.Condition, current.ChannelIDs, current.Severity, current.Cooldown, current.NodeScope, current.NodeIDs, enabled, id)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to update rule")
	}
	return response.OK(c, nil)
}

func (h *Handler) DeleteRule(c echo.Context) error {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidID, "invalid rule id")
	}
	res, err := h.DB.Exec("DELETE FROM alert_rules WHERE id=?", id)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to delete rule")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "rule not found")
	}
	return response.OK(c, nil)
}

// --- History ---

func (h *Handler) ListHistory(c echo.Context) error {
	page, _ := strconv.Atoi(c.QueryParam("page"))
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	offset := (page - 1) * limit

	var total int
	h.DB.QueryRow("SELECT COUNT(*) FROM alert_history").Scan(&total)

	rows, err := h.DB.Query("SELECT id, rule_id, rule_name, type, severity, message, node_id, sent_channels, created_at FROM alert_history ORDER BY id DESC LIMIT ? OFFSET ?", limit, offset)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to list history")
	}
	defer rows.Close()

	var list []AlertHistoryEntry
	for rows.Next() {
		var e AlertHistoryEntry
		if err := rows.Scan(&e.ID, &e.RuleID, &e.RuleName, &e.Type, &e.Severity, &e.Message, &e.NodeID, &e.SentChannels, &e.CreatedAt); err != nil {
			continue
		}
		list = append(list, e)
	}
	if list == nil {
		list = []AlertHistoryEntry{}
	}

	return response.OK(c, map[string]interface{}{
		"items": list,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

func (h *Handler) ClearHistory(c echo.Context) error {
	_, err := h.DB.Exec("DELETE FROM alert_history")
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to clear history")
	}
	return response.OK(c, nil)
}
