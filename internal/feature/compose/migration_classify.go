package compose

import (
	"path/filepath"
	"strings"
)

// systemBindPrefixes are host paths whose data is provided by the target host,
// never copied (docker socket, the stacks root self-mount, udev, etc.).
var systemBindPrefixes = []string{
	"/var/run/docker.sock", "/run/docker.sock",
	"/run/udev", "/dev", "/sys", "/proc",
	"/opt/stacks", // self-mount (e.g. Dockge) — provided by the target, not copied
}

// classifyBind returns "in-stack", "abs", or "system" for a compose bind source.
// Relative sources resolve under stackDir → "in-stack". Absolute sources under
// stackDir are also "in-stack". A small deny-list marks host-provided paths as
// "system" (their data is never copied). Everything else absolute is "abs".
//
// The in-stack check runs before the systemBindPrefixes deny-list so that a path
// inside the stack dir is always "in-stack" even when the stack dir itself lives
// under a system prefix (e.g. stackDir "/opt/stacks/jelly" under "/opt/stacks").
func classifyBind(host, stackDir string) string {
	if !strings.HasPrefix(host, "/") {
		return "in-stack" // relative → under the stack working dir
	}
	clean := filepath.Clean(host)
	sd := filepath.Clean(stackDir)
	if clean == sd || strings.HasPrefix(clean+"/", sd+"/") {
		return "in-stack"
	}
	for _, p := range systemBindPrefixes {
		if clean == p || strings.HasPrefix(clean+"/", p+"/") {
			return "system"
		}
	}
	return "abs"
}
