package system

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// backupCheckInterval is how often the scheduler wakes to see whether a backup
// is due. A var so tests can shorten it; the actual cadence is driven by the
// operator-configured interval_hours, this just bounds the lag.
var backupCheckInterval = 10 * time.Minute

// backupFileRe matches the timestamped names the runner writes, and gates the
// download/delete handlers so a crafted ?name= can't traverse out of the dir.
var backupFileRe = regexp.MustCompile(`^sfpanel-backup-[0-9]{8}-[0-9]{6}\.tar\.gz$`)

// BackupScheduleConfig is the operator-facing schedule state.
type BackupScheduleConfig struct {
	Enabled       bool       `json:"enabled"`
	IntervalHours int        `json:"interval_hours"`
	Retention     int        `json:"retention"`
	LastRun       *time.Time `json:"last_run"`
	LastStatus    string     `json:"last_status"`
	LastError     string     `json:"last_error"`
}

// BackupFile describes one archive on disk.
type BackupFile struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

// backupDirFor returns the local directory scheduled backups live in: a
// "backups" sibling of the SQLite DB (so it follows the data dir on any
// install layout).
func backupDirFor(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "backups")
}

func (h *Handler) backupDir() string { return backupDirFor(h.DBPath) }

// createBackupFile writes a new timestamped archive into dir via a temp file +
// rename so a crash mid-write never leaves a truncated archive that a later
// restore would choke on. Returns the file name.
func createBackupFile(dir, dbPath, configPath, composePath string) (string, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}
	name := fmt.Sprintf("sfpanel-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
	final := filepath.Join(dir, name)
	tmp := final + ".tmp"

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return "", err
	}
	if err := writeBackupArchive(f, dbPath, configPath, composePath); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return name, nil
}

// listBackupFiles returns the archives in dir, newest first. A missing dir is
// not an error — it just means no backups have been taken yet.
func listBackupFiles(dir string) ([]BackupFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []BackupFile{}, nil
		}
		return nil, err
	}
	out := make([]BackupFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !backupFileRe.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupFile{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModTime.After(out[j].ModTime) })
	return out, nil
}

// pruneBackups deletes all but the newest `retention` archives.
func pruneBackups(dir string, retention int) {
	if retention <= 0 {
		return
	}
	files, err := listBackupFiles(dir)
	if err != nil {
		return
	}
	for i := retention; i < len(files); i++ {
		if err := os.Remove(filepath.Join(dir, files[i].Name)); err != nil {
			slog.Warn("backup prune: remove failed", "component", "system", "file", files[i].Name, "error", err)
		}
	}
}

func readBackupSchedule(db *sql.DB) (BackupScheduleConfig, error) {
	var cfg BackupScheduleConfig
	var enabled int
	var lastRun sql.NullTime
	var lastStatus, lastErr sql.NullString
	err := db.QueryRow(`SELECT enabled, interval_hours, retention, last_run, last_status, last_error
		FROM backup_schedule WHERE id = 1`).
		Scan(&enabled, &cfg.IntervalHours, &cfg.Retention, &lastRun, &lastStatus, &lastErr)
	if err != nil {
		return cfg, err
	}
	cfg.Enabled = enabled == 1
	if lastRun.Valid {
		cfg.LastRun = &lastRun.Time
	}
	cfg.LastStatus = lastStatus.String
	cfg.LastError = lastErr.String
	return cfg, nil
}

// recordRun stamps the outcome of a backup run onto the schedule row.
func recordRun(db *sql.DB, status, errMsg string) {
	if _, err := db.Exec(`UPDATE backup_schedule SET last_run = ?, last_status = ?, last_error = ? WHERE id = 1`,
		time.Now(), status, errMsg); err != nil {
		slog.Warn("backup: failed to record run", "component", "system", "error", err)
	}
}

// performScheduledBackup creates one archive, prunes to retention, and records
// the outcome. Shared by the runner and the run-now handler.
func performScheduledBackup(db *sql.DB, dbPath, configPath, composePath string, retention int) (string, error) {
	dir := backupDirFor(dbPath)
	name, err := createBackupFile(dir, dbPath, configPath, composePath)
	if err != nil {
		slog.Error("scheduled backup failed", "component", "system", "error", err)
		recordRun(db, "error", err.Error())
		return "", err
	}
	pruneBackups(dir, retention)
	recordRun(db, "success", "")
	slog.Info("scheduled backup created", "component", "system", "file", name)
	return name, nil
}

// StartBackupScheduler launches the background loop that takes scheduled
// backups. It checks every backupCheckInterval whether the configured interval
// has elapsed since the last run; nothing happens while the schedule is
// disabled. Bound to ctx so a shutdown stops it.
func StartBackupScheduler(ctx context.Context, db *sql.DB, dbPath, configPath, composePath string) {
	go func() {
		ticker := time.NewTicker(backupCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cfg, err := readBackupSchedule(db)
				if err != nil || !cfg.Enabled {
					continue
				}
				if cfg.LastRun != nil && time.Since(*cfg.LastRun) < time.Duration(cfg.IntervalHours)*time.Hour {
					continue
				}
				performScheduledBackup(db, dbPath, configPath, composePath, cfg.Retention)
			}
		}
	}()
}

// --- HTTP handlers ---

// GetBackupSchedule returns the schedule config plus the list of archives on disk.
func (h *Handler) GetBackupSchedule(c echo.Context) error {
	cfg, err := readBackupSchedule(h.DB)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to read backup schedule")
	}
	files, err := listBackupFiles(h.backupDir())
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, "failed to list backups")
	}
	return response.OK(c, map[string]interface{}{"schedule": cfg, "files": files})
}

// UpdateBackupSchedule sets enabled/interval/retention.
func (h *Handler) UpdateBackupSchedule(c echo.Context) error {
	var req struct {
		Enabled       bool `json:"enabled"`
		IntervalHours int  `json:"interval_hours"`
		Retention     int  `json:"retention"`
	}
	if err := c.Bind(&req); err != nil {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidBody, "invalid request body")
	}
	if req.IntervalHours < 1 || req.IntervalHours > 168 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "interval_hours must be between 1 and 168")
	}
	if req.Retention < 1 || req.Retention > 100 {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidValue, "retention must be between 1 and 100")
	}
	enabled := 0
	if req.Enabled {
		enabled = 1
	}
	if _, err := h.DB.Exec(`UPDATE backup_schedule SET enabled = ?, interval_hours = ?, retention = ? WHERE id = 1`,
		enabled, req.IntervalHours, req.Retention); err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to update backup schedule")
	}
	return response.OK(c, map[string]string{"message": "backup schedule updated"})
}

// RunBackupNow takes a backup immediately, honoring the configured retention.
func (h *Handler) RunBackupNow(c echo.Context) error {
	cfg, err := readBackupSchedule(h.DB)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrDBError, "failed to read backup schedule")
	}
	name, err := performScheduledBackup(h.DB, h.DBPath, h.ConfigPath, h.ComposePath, cfg.Retention)
	if err != nil {
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, response.SanitizeOutput(err.Error()))
	}
	return response.OK(c, map[string]string{"message": "backup created", "name": name})
}

// DownloadBackupFile streams a stored archive by name.
func (h *Handler) DownloadBackupFile(c echo.Context) error {
	name := c.QueryParam("name")
	if !backupFileRe.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "invalid backup name")
	}
	path := filepath.Join(h.backupDir(), name)
	if _, err := os.Stat(path); err != nil {
		return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "backup not found")
	}
	c.Response().Header().Set("Content-Disposition", "attachment; filename="+name)
	return c.File(path)
}

// DeleteBackupFile removes a stored archive by name.
func (h *Handler) DeleteBackupFile(c echo.Context) error {
	name := c.QueryParam("name")
	if !backupFileRe.MatchString(name) {
		return response.Fail(c, http.StatusBadRequest, response.ErrInvalidName, "invalid backup name")
	}
	path := filepath.Join(h.backupDir(), name)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return response.Fail(c, http.StatusNotFound, response.ErrNotFound, "backup not found")
		}
		return response.Fail(c, http.StatusInternalServerError, response.ErrFileError, "failed to delete backup")
	}
	return response.OK(c, map[string]string{"message": "backup deleted", "name": name})
}
