package featuredocker

import (
	"database/sql"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// ObservabilityHandler exposes the read endpoints introduced by theme F.
// Depends only on the DB; collection happens in package monitor and
// retention in monitor's pruners.
type ObservabilityHandler struct {
	DB                   *sql.DB
	ObservabilityEnabled bool
}

func (h *ObservabilityHandler) GetMetrics(c echo.Context) error {
	id := c.Param("id")
	rangeStr := c.QueryParam("range")
	if rangeStr == "" {
		rangeStr = "1h"
	}
	dur, ok := parseRange(rangeStr)
	if !ok {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidRequest, "range must be 1h, 6h, or 24h")
	}
	if !h.ObservabilityEnabled {
		return response.OK(c, map[string]any{"observability_disabled": true, "data": []any{}})
	}
	cutoff := time.Now().Add(-dur).UnixMilli()
	rows, err := h.DB.Query(
		`SELECT ts, cpu_percent, mem_percent, mem_bytes FROM container_metrics_history WHERE container_id = ? AND ts >= ? ORDER BY ts ASC`,
		id, cutoff,
	)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "metrics query failed")
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var ts int64
		var cpu, mem float64
		var memBytes int64
		if err := rows.Scan(&ts, &cpu, &mem, &memBytes); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"ts": ts, "cpu_percent": cpu, "mem_percent": mem, "mem_bytes": memBytes,
		})
	}
	return response.OK(c, out)
}

func parseRange(s string) (time.Duration, bool) {
	switch s {
	case "1h":
		return 1 * time.Hour, true
	case "6h":
		return 6 * time.Hour, true
	case "24h":
		return 24 * time.Hour, true
	}
	return 0, false
}

// GetEvents returns container_events rows for the given container, newest
// first, with cursor pagination via ?before=<ts>.
func (h *ObservabilityHandler) GetEvents(c echo.Context) error {
	id := c.Param("id")
	limit := 50
	if v := c.QueryParam("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	beforeTS := int64(0)
	if v := c.QueryParam("before"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			beforeTS = n
		}
	}
	if !h.ObservabilityEnabled {
		return response.OK(c, map[string]any{"observability_disabled": true, "data": []any{}})
	}
	q := `SELECT ts, event_type, exit_code, detail FROM container_events WHERE container_id = ?`
	args := []any{id}
	if beforeTS > 0 {
		q += ` AND ts < ?`
		args = append(args, beforeTS)
	}
	q += ` ORDER BY ts DESC LIMIT ?`
	args = append(args, limit)
	rows, err := h.DB.Query(q, args...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "events query failed")
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var ts int64
		var eventType string
		var exitCode sql.NullInt64
		var detail sql.NullString
		if err := rows.Scan(&ts, &eventType, &exitCode, &detail); err != nil {
			continue
		}
		row := map[string]any{
			"ts":         ts,
			"event_type": eventType,
			"exit_code":  nil,
			"detail":     nil,
		}
		if exitCode.Valid {
			row["exit_code"] = exitCode.Int64
		}
		if detail.Valid {
			row["detail"] = detail.String
		}
		out = append(out, row)
	}
	return response.OK(c, out)
}

// GetRecentEvents returns the most recent events across all containers.
func (h *ObservabilityHandler) GetRecentEvents(c echo.Context) error {
	limit := 50
	if v := c.QueryParam("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	if !h.ObservabilityEnabled {
		return response.OK(c, map[string]any{"observability_disabled": true, "data": []any{}})
	}
	rows, err := h.DB.Query(
		`SELECT container_id, container_name, ts, event_type, exit_code, detail FROM container_events ORDER BY ts DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "events query failed")
	}
	defer rows.Close()
	out := make([]map[string]any, 0)
	for rows.Next() {
		var id, name, eventType string
		var ts int64
		var exitCode sql.NullInt64
		var detail sql.NullString
		rows.Scan(&id, &name, &ts, &eventType, &exitCode, &detail)
		row := map[string]any{
			"container_id":   id,
			"container_name": name,
			"ts":             ts,
			"event_type":     eventType,
			"exit_code":      nil,
			"detail":         nil,
		}
		if exitCode.Valid {
			row["exit_code"] = exitCode.Int64
		}
		if detail.Valid {
			row["detail"] = detail.String
		}
		out = append(out, row)
	}
	return response.OK(c, out)
}
