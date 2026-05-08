package monitor

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	updateMu     sync.RWMutex
	cachedLatest string
)

// StartUpdateChecker polls GitHub releases every hour in background.
func StartUpdateChecker(currentVersion string) {
	go func() {
		checkUpdate(currentVersion)
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			checkUpdate(currentVersion)
		}
	}()
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
func GetUpdateInfo(currentVersion string) UpdateInfo {
	updateMu.RLock()
	defer updateMu.RUnlock()
	if cachedLatest == "" {
		return UpdateInfo{}
	}
	current := strings.TrimPrefix(currentVersion, "v")
	return UpdateInfo{
		UpdateAvailable: cachedLatest != current,
		LatestVersion:   cachedLatest,
	}
}
