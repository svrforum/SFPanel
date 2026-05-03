package featuredocker

import (
	"database/sql"
	"net/http"
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
