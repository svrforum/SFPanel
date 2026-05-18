package audit

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// actionAuditLogCleared is recorded with protected=1 immediately before a
// clear so the wipe itself survives subsequent wipes. The marker rides in
// the `path` column rather than a new column because the audit_logs schema
// is already shared with the auth login-event recorder and we don't want to
// fork it for one constant.
const actionAuditLogCleared = "audit_log_cleared"

type Handler struct {
	DB *sql.DB
	// LocalNodeIDFn returns this cluster node's ID, used to stamp the
	// audit_log_cleared tombstone. nil-safe — left unset on non-cluster
	// installs, in which case the tombstone's node_id stays empty.
	LocalNodeIDFn func() string
}

type AuditLogEntry struct {
	ID        int    `json:"id"`
	Username  string `json:"username"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Status    int    `json:"status"`
	IP        string `json:"ip"`
	NodeID    string `json:"node_id,omitempty"`
	Protected bool   `json:"protected"`
	CreatedAt string `json:"created_at"`
}

type AuditLogsResponse struct {
	Logs  []AuditLogEntry `json:"logs"`
	Total int             `json:"total"`
}

// ClearAuditLogsResponse reports how many rows were deleted and confirms a
// tombstone was written. UI uses `deleted` to render a toast; `tombstone_id`
// gives operators a stable handle to point at when investigating later.
type ClearAuditLogsResponse struct {
	Message     string `json:"message"`
	Deleted     int64  `json:"deleted"`
	TombstoneID int64  `json:"tombstone_id"`
}

func (h *Handler) ListAuditLogs(c echo.Context) error {
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
		"SELECT id, username, method, path, status, ip, node_id, protected, created_at FROM audit_logs ORDER BY id DESC LIMIT ? OFFSET ?",
		limit, offset,
	)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to query audit logs")
	}
	defer rows.Close()

	var logs []AuditLogEntry
	for rows.Next() {
		var entry AuditLogEntry
		var protectedInt int
		if err := rows.Scan(&entry.ID, &entry.Username, &entry.Method, &entry.Path, &entry.Status, &entry.IP, &entry.NodeID, &protectedInt, &entry.CreatedAt); err != nil {
			continue
		}
		entry.Protected = protectedInt != 0
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

// ClearAuditLogs deletes audit rows. Three things happen in order:
//
//  1. The query parameters are validated. `?days=N` (N >= 1) keeps rows newer
//     than N*24h; `?before=ISO8601` keeps rows newer than the given instant.
//     The two are mutually exclusive — passing both is a 400. With neither
//     parameter set, the call falls back to the legacy "wipe everything
//     unprotected" behavior so existing UI keeps working.
//
//  2. A tombstone row is inserted with protected=1 *before* the DELETE runs.
//     The tombstone records who fired the wipe, from what IP, and how many
//     rows were targeted (counted with the same WHERE clause about to be
//     used). This is the row a future investigator finds after an attacker
//     clears their tracks — they cannot wipe it without root DB access.
//
//  3. DELETE runs with `WHERE protected = 0` plus whatever date filter was
//     selected. Protected rows are immune by construction.
func (h *Handler) ClearAuditLogs(c echo.Context) error {
	whereClause, args, err := parseClearScope(c)
	if err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, err.Error())
	}

	// Count target rows *before* deletion so the tombstone records what the
	// operator actually erased, not zero.
	countSQL := "SELECT COUNT(*) FROM audit_logs WHERE protected = 0"
	if whereClause != "" {
		countSQL += " AND " + whereClause
	}
	var target int64
	if err := h.DB.QueryRow(countSQL, args...).Scan(&target); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to count target audit rows")
	}

	username, _ := c.Get("username").(string)
	ip := c.RealIP()
	// Stamp the tombstone with the LOCAL node ID. The previous read from
	// c.QueryParam("node") was always empty because the cluster proxy strips
	// `?node=` before reaching the handler. Reviewers couldn't tell which
	// node's audit log was wiped.
	nodeID := ""
	if h.LocalNodeIDFn != nil {
		nodeID = h.LocalNodeIDFn()
	}
	// Encode the scope into the path column so an investigator can tell at a
	// glance whether the wipe was scoped or full. The fragment grammar
	// matches what recordLoginEvent uses (path#detail) so existing UI parsers
	// keep working.
	scopeMarker := "all"
	if whereClause != "" {
		scopeMarker = "scoped"
	}
	tombstonePath := fmt.Sprintf("/api/v1/audit/logs#%s:%s:deleted=%d", actionAuditLogCleared, scopeMarker, target)

	res, err := h.DB.Exec(
		"INSERT INTO audit_logs (username, method, path, status, ip, node_id, protected) VALUES (?, ?, ?, ?, ?, ?, 1)",
		username, http.MethodDelete, tombstonePath, http.StatusOK, ip, nodeID,
	)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to record audit clear event")
	}
	tombstoneID, _ := res.LastInsertId()

	deleteSQL := "DELETE FROM audit_logs WHERE protected = 0"
	if whereClause != "" {
		deleteSQL += " AND " + whereClause
	}
	delRes, err := h.DB.Exec(deleteSQL, args...)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "Failed to clear audit logs")
	}
	deleted, _ := delRes.RowsAffected()

	return response.OK(c, ClearAuditLogsResponse{
		Message:     "Audit logs cleared",
		Deleted:     deleted,
		TombstoneID: tombstoneID,
	})
}

// parseClearScope reads `?days=N` and `?before=ISO8601` and returns a SQL
// WHERE fragment (without the leading AND) plus its bind args. Empty string
// + nil means "no scope, delete every unprotected row". Errors are surfaced
// to the caller as 400s.
func parseClearScope(c echo.Context) (string, []any, error) {
	daysRaw := c.QueryParam("days")
	beforeRaw := c.QueryParam("before")

	if daysRaw != "" && beforeRaw != "" {
		return "", nil, fmt.Errorf("specify either days or before, not both")
	}

	switch {
	case daysRaw != "":
		days, err := strconv.Atoi(daysRaw)
		if err != nil || days < 1 {
			return "", nil, fmt.Errorf("days must be a positive integer")
		}
		cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
		return "created_at < ?", []any{cutoff.Format("2006-01-02 15:04:05")}, nil
	case beforeRaw != "":
		// Accept both full RFC3339 and the date-only shortcut common in UIs.
		var cutoff time.Time
		if t, err := time.Parse(time.RFC3339, beforeRaw); err == nil {
			cutoff = t.UTC()
		} else if t, err := time.Parse("2006-01-02", beforeRaw); err == nil {
			cutoff = t.UTC()
		} else {
			return "", nil, fmt.Errorf("before must be RFC3339 or YYYY-MM-DD")
		}
		return "created_at < ?", []any{cutoff.Format("2006-01-02 15:04:05")}, nil
	}
	return "", nil, nil
}
