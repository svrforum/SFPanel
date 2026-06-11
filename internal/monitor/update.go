package monitor

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/svrforum/SFPanel/internal/common/safe"
	"github.com/svrforum/SFPanel/internal/release"
)

var (
	updateMu     sync.RWMutex
	cachedLatest string
)

// StartUpdateChecker polls GitHub releases every hour in background.
//
// isLeaderFn is consulted on each tick; when it returns false the tick is
// skipped. In standalone mode (nil), every tick proceeds. The leader-only
// gate prevents an N-node cluster from hammering api.github.com from every
// node on every hourly tick — a 5-node panel without this would do 5x the
// requests against the same shared upstream, and the rate-limit / 403
// behaviour from github.com is visible per-token-per-IP so this isn't
// theoretical.
func StartUpdateChecker(currentVersion string, isLeaderFn func() bool) {
	safe.Go("monitor-update-checker", func() {
		if isLeaderFn == nil || isLeaderFn() {
			checkUpdate(currentVersion)
		}
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if isLeaderFn != nil && !isLeaderFn() {
				continue
			}
			checkUpdate(currentVersion)
		}
	})
}

func checkUpdate(currentVersion string) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get("https://api.github.com/repos/svrforum/SFPanel/releases/latest")
	if err != nil {
		slog.Warn("update check: github request failed",
			"component", "monitor", "error", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		// 403 here is almost always the unauthenticated per-IP rate limit;
		// the cache just goes stale until a later tick succeeds.
		slog.Warn("update check: github returned non-200",
			"component", "monitor", "status", resp.StatusCode)
		return
	}

	var rel struct {
		TagName     string `json:"tag_name"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		slog.Warn("update check: decode release response failed",
			"component", "monitor", "error", err)
		return
	}

	latest := strings.TrimPrefix(rel.TagName, "v")
	updateMu.Lock()
	cachedLatest = latest
	updateMu.Unlock()
}

type UpdateInfo struct {
	UpdateAvailable bool   `json:"update_available"`
	LatestVersion   string `json:"latest_version,omitempty"`
}

// GetUpdateInfo returns cached update status.
//
// Comparison is semver (release.IsForwardUpdate), not string equality:
//   - a "v" prefix on currentVersion is tolerated (cachedLatest is
//     already stripped);
//   - dev builds with a "-N-gHASH" suffix from `git describe` compare as
//     their base version, so commits past a release aren't flagged
//     against that same release;
//   - a version locally *newer* than the latest published release is not
//     flagged as updatable (downgrades are noise, not updates);
//   - unparseable versions (the plain "dev" ldflags fallback in
//     cmd/sfpanel) are never flagged — nothing meaningful to compare.
func GetUpdateInfo(currentVersion string) UpdateInfo {
	updateMu.RLock()
	defer updateMu.RUnlock()
	if cachedLatest == "" {
		return UpdateInfo{}
	}
	forward, err := release.IsForwardUpdate(currentVersion, cachedLatest)
	if err != nil || !forward {
		return UpdateInfo{}
	}
	return UpdateInfo{
		UpdateAvailable: true,
		LatestVersion:   cachedLatest,
	}
}
