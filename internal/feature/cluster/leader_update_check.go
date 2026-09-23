package featurecluster

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/paneltls"
)

// leaderNeedsUpdate asks this node's own panel whether a newer release is
// available, the same question the update page asks. See runClusterUpdate for
// why the leader must know before it shuts its cluster manager down.
func (h *Handler) leaderNeedsUpdate() (bool, error) {
	const path = "/api/v1/system/update-check"
	self := paneltls.Self{
		TLSEnabled: h.Config.Server.TLS.Enabled,
		Dir:        h.Config.Server.TLS.Dir,
		CertFile:   h.Config.Server.TLS.CertFile,
		CAFile:     h.Config.Server.TLS.CAFile,
		Port:       h.Config.Server.Port,
	}
	client, err := self.HTTPClient(20 * time.Second)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequest(http.MethodGet, self.URL(path), nil)
	if err != nil {
		return false, err
	}
	if sig := auth.SignProxyRequestV2(http.MethodGet, path); sig != "" {
		req.Header.Set(auth.InternalProxyHeaderV2, sig)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return false, err
	}
	return parseUpdateAvailable(resp.StatusCode, body)
}

// parseUpdateAvailable reads update_available out of an update-check
// response. Anything but a successful answer that says so is an error, never
// a quiet "false" or "true": the caller decides what an unknown means.
func parseUpdateAvailable(status int, body []byte) (bool, error) {
	if status < 200 || status > 299 {
		return false, fmt.Errorf("update check returned HTTP %d", status)
	}
	var r struct {
		Success bool `json:"success"`
		Data    struct {
			UpdateAvailable *bool `json:"update_available"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return false, fmt.Errorf("update check: %w", err)
	}
	if !r.Success || r.Data.UpdateAvailable == nil {
		return false, fmt.Errorf("update check gave no answer")
	}
	return *r.Data.UpdateAvailable, nil
}
