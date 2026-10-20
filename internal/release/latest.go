package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// LatestURL is GitHub's latest-release endpoint for this project; tests point
// it elsewhere.
var LatestURL = "https://api.github.com/repos/svrforum/SFPanel/releases/latest"

// Release is a GitHub release as the update path reads it — also what a
// cluster update sends each node to install.
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// FetchLatest looks the latest release up. A cluster update calls it once
// and hands the answer to every node: the API allows sixty unauthenticated
// requests an hour per address.
func FetchLatest(ctx context.Context) (*Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LatestURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ask GitHub for the latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		hint := ""
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			hint = " — GitHub allows sixty unauthenticated requests an hour per address; try again later"
		}
		return nil, fmt.Errorf("GitHub answered HTTP %d for the latest release%s", resp.StatusCode, hint)
	}
	var rel Release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("read the latest release: %w", err)
	}
	if rel.TagName == "" {
		return nil, errors.New("GitHub's latest release has no tag")
	}
	return &rel, nil
}
