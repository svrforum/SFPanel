package monitor

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/svrforum/SFPanel/internal/common/safe"
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
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}

	var release struct {
		TagName     string `json:"tag_name"`
		Body        string `json:"body"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	updateMu.Lock()
	cachedLatest = latest
	updateMu.Unlock()
}

type UpdateInfo struct {
	UpdateAvailable bool   `json:"update_available"`
	LatestVersion   string `json:"latest_version,omitempty"`
}

// GetUpdateInfo returns cached update status.
// currentVersion may be ldflags-injected as "v0.13.0" while cachedLatest is
// already stripped to "0.13.0"; normalize before comparing so a node running
// the latest release doesn't claim the same release is "available".
//
// Dev builds carry a "-N-gHASH" suffix from `git describe` (e.g.
// "0.13.0-8-g61f85c0"). They're commits *past* cachedLatest, so don't
// flag them as needing an update — that's noise for anyone running off
// of a personal build.
func GetUpdateInfo(currentVersion string) UpdateInfo {
	updateMu.RLock()
	defer updateMu.RUnlock()
	if cachedLatest == "" {
		return UpdateInfo{}
	}
	current := strings.TrimPrefix(currentVersion, "v")
	if current == cachedLatest || strings.HasPrefix(current, cachedLatest+"-") {
		return UpdateInfo{}
	}
	return UpdateInfo{
		UpdateAvailable: true,
		LatestVersion:   cachedLatest,
	}
}
