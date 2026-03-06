package handlers

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

type AuditHandler struct {
	DB *sql.DB
}

type AuditLogEntry struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Status    int    `json:"status"`
	IP        string `json:"ip"`
	CreatedAt string `json:"created_at"`
}

type AuditLogsResponse struct {
	Logs  []AuditLogEntry `json:"logs"`
	Total int             `json:"total"`
}

func (h *AuditHandler) ListAuditLogs(c echo.Context) error {
	page, _ := strconv.Atoi(c.QueryParam("page"))
	if page < 1 {
		page = 1
	}
	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	if limit < 1 || limit > 100 {
		limit = 50
	}
	offset := (page - 1) * limit

	var total int
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM audit_logs").Scan(&total); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to count audit logs")
	}

	rows, err := h.DB.Query(
		"SELECT id, username, method, path, status, ip, created_at FROM audit_logs ORDER BY id DESC LIMIT ? OFFSET ?",
		limit, offset,
	)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to query audit logs")
	}
	defer rows.Close()

	var logs []AuditLogEntry
	for rows.Next() {
		var entry AuditLogEntry
		if err := rows.Scan(&entry.ID, &entry.Username, &entry.Method, &entry.Path, &entry.Status, &entry.IP, &entry.CreatedAt); err != nil {
			continue
		}
		logs = append(logs, entry)
	}
	if logs == nil {
		logs = []AuditLogEntry{}
	}

	return response.OK(c, AuditLogsResponse{
		Logs:  logs,
		Total: total,
	})
}

func (h *AuditHandler) ClearAuditLogs(c echo.Context) error {
	if _, err := h.DB.Exec("DELETE FROM audit_logs"); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to clear audit logs")
	}
	return response.OK(c, map[string]string{"message": "Audit logs cleared"})
}
