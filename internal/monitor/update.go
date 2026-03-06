package monitor

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

var (
	updateMu        sync.RWMutex
	cachedLatest    string
	cachedNotes     string
	cachedPublished string
	lastCheck       time.Time
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
	resp, err := client.Get("https://api.github.com/repos/sfpanel/sfpanel/releases/latest")
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
	cachedNotes = release.Body
	cachedPublished = release.PublishedAt
	lastCheck = time.Now()
	updateMu.Unlock()
}

type UpdateInfo struct {
	UpdateAvailable bool   `json:"update_available"`
	LatestVersion   string `json:"latest_version,omitempty"`
}

// GetUpdateInfo returns cached update status.
func GetUpdateInfo(currentVersion string) UpdateInfo {
	updateMu.RLock()
	defer updateMu.RUnlock()
	if cachedLatest == "" {
		return UpdateInfo{}
	}
	return UpdateInfo{
		UpdateAvailable: cachedLatest != currentVersion,
		LatestVersion:   cachedLatest,
	}
}
