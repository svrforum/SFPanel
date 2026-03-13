package release

import (
	"fmt"
	"strings"
)

// Asset represents a release asset from GitHub.
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// FindAssetURL returns the download URL for the named asset, or empty string if not found.
func FindAssetURL(assets []Asset, name string) string {
	for _, a := range assets {
		if a.Name == name {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

// ParseExpectedSHA256 extracts the SHA-256 hash for the given archive name
// from a checksums.txt file (GoReleaser format: "<hash>  <filename>").
func ParseExpectedSHA256(checksums []byte, archiveName string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == archiveName {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("checksum not found for %s", archiveName)
}
