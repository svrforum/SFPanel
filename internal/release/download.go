package release

import (
	"context"
	"fmt"
	"os"
	osExec "os/exec"
)

// DownloadInstaller fetches an installer script into a freshly created
// private temp file (0600, random name) and returns its path plus curl's
// combined output. A fixed name would let a local unprivileged user
// pre-create or swap the script between the VerifyInstaller hash check and
// execution (TOCTOU → root code execution). Scripts run via "sh/bash
// <path>", so no +x bit is needed. On success the caller must os.Remove the
// returned path (defer).
func DownloadInstaller(ctx context.Context, url string) (string, string, error) {
	f, err := os.CreateTemp("", "sfpanel-installer-*.sh")
	if err != nil {
		return "", "", fmt.Errorf("create installer temp file: %w", err)
	}
	path := f.Name()
	f.Close()

	// Part of the documented SSE install flow (os/exec exception); ctx kills
	// the download with the request. curl -o truncates the existing file in
	// place, preserving the 0600 mode and owner set by CreateTemp.
	cmd := osExec.CommandContext(ctx, "curl", "-fsSL", url, "-o", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.Remove(path)
		return "", string(out), err
	}
	return path, string(out), nil
}
