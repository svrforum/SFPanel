package files

import (
	"path/filepath"
	"strings"
)

// webServedPrefixes is the set of paths that a typical Linux server may
// auto-serve via Apache or Nginx. Uploads into these prefixes get the
// extension blocklist applied so that an operator dragging a shell script
// into the wrong directory doesn't end up exposing it as an RCE.
//
// This is intentionally a small, conservative list. Operators with custom
// docroots can extend it via the future server.upload_blocklist_paths
// config (out of scope for this change).
var webServedPrefixes = []string{
	"/var/www/",
	"/srv/www/",
	"/srv/http/",
	"/usr/share/nginx/",
	"/usr/share/apache2/",
	"/usr/share/httpd/",
}

// webExecutableExts is the extension blocklist applied inside web-served
// directories. PHP, JSP, ASP variants, plus generic CGI handlers — every
// real-world panel breach involving "I uploaded a shell to /var/www" went
// through one of these.
var webExecutableExts = map[string]bool{
	".php":   true,
	".phps":  true,
	".phtml": true,
	".php3":  true,
	".php4":  true,
	".php5":  true,
	".php7":  true,
	".jsp":   true,
	".jspx":  true,
	".asp":   true,
	".aspx":  true,
	".cgi":   true,
	".pl":    true,
	".py":    true, // mod_python / wsgi
	".rb":    true,
	".sh":    true,
	".bash":  true,
	// Java web containers
	".war": true,
	".ear": true,
}

// webBlocklistBasenames covers filenames (not just extensions) that change
// Apache/Nginx behaviour or expose attack surface even when the file
// content itself is innocuous.
var webBlocklistBasenames = map[string]bool{
	".htaccess":  true, // Apache override — can enable PHP, redirects, etc.
	".htpasswd":  true, // password store
	"web.config": true, // IIS equivalent
}

func isWebServedPath(p string) bool {
	clean := filepath.Clean(p) + "/"
	for _, prefix := range webServedPrefixes {
		if strings.HasPrefix(clean, prefix) {
			return true
		}
	}
	return false
}

func hasWebExecutableExtension(filename string) bool {
	if webExecutableExts[strings.ToLower(filepath.Ext(filename))] {
		return true
	}
	// Match by basename too — .htaccess has no "extension" per
	// filepath.Ext (it's all extension), and operators who drag-and-drop
	// these into /var/www routinely don't realize they'll be live.
	return webBlocklistBasenames[strings.ToLower(filepath.Base(filename))]
}
