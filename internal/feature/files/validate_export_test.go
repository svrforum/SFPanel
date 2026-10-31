package files

import (
	"strings"
	"testing"
)

// ValidatePathForWrite is the file manager's own write guard, for a task
// job's file step: the same paths are refused for the same reasons.
func TestValidatePathForWriteRefusesWhatTheFileManagerRefuses(t *testing.T) {
	for _, tc := range []struct {
		path   string
		reason string // "" when the write goes ahead
	}{
		{"/etc/sfpanel/config.yaml", "panel credentials"},
		{"/etc/sfpanel", "panel credentials"},
		{"/root/.ssh/authorized_keys", "panel credentials"},
		{"/etc/sudoers.d/operator", "panel credentials"},
		{"etc/nginx/nginx.conf", "must be absolute"},
		{"/etc/nginx/../sfpanel/config.yaml", "traversal"},
		{"", "path is required"},
		{"/etc/nginx/nginx.conf", ""},
		{"/opt/stacks/app/docker-compose.yml", ""},
	} {
		err := ValidatePathForWrite(tc.path)
		switch {
		case tc.reason == "" && err != nil:
			t.Errorf("ValidatePathForWrite(%q) = %v, want it allowed", tc.path, err)
		case tc.reason != "" && (err == nil || !strings.Contains(err.Error(), tc.reason)):
			t.Errorf("ValidatePathForWrite(%q) = %v, want a refusal saying %q", tc.path, err, tc.reason)
		}
	}
}
