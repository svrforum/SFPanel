package disk

import (
	"path/filepath"
	"strings"
)

// protectedMountpoints is the deny-list for destructive filesystem operations.
// Operations targeting these paths (or any path *underneath* them) are
// refused. Reason: an operator with API access can otherwise unmount /,
// overwrite /etc, or shadow /var/lib/sfpanel and lock themselves out.
var protectedMountpoints = []string{
	"/",
	"/boot",
	"/boot/efi",
	"/etc",
	"/usr",
	"/var",
	"/var/lib",
	"/var/lib/sfpanel",
	"/var/lib/docker",
	"/proc",
	"/sys",
	"/dev",
	"/run",
	"/home",
	"/root",
}

// isProtectedMountpoint reports whether mp is one of the deny-listed paths or
// nests under one. Non-absolute or empty inputs are refused (true).
func isProtectedMountpoint(mp string) bool {
	if mp == "" || !filepath.IsAbs(mp) {
		return true
	}
	clean := filepath.Clean(mp)
	for _, p := range protectedMountpoints {
		if clean == p {
			return true
		}
		if strings.HasPrefix(clean, p+"/") {
			return true
		}
	}
	return false
}
