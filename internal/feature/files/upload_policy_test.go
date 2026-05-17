package files

import "testing"

func TestIsWebServedPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/var/www/html/index.php", true},
		{"/srv/www/site/upload.txt", true},
		{"/usr/share/nginx/html/note", true},
		{"/opt/stacks/myapp/script.sh", false},
		{"/home/user/file", false},
		{"/var/log/sfpanel.log", false},
	}
	for _, c := range cases {
		if got := isWebServedPath(c.path); got != c.want {
			t.Errorf("isWebServedPath(%q) = %v; want %v", c.path, got, c.want)
		}
	}
}

func TestHasWebExecutableExtension(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"shell.php", true}, {"x.PHP", true}, {"x.phtml", true},
		{"foo.jsp", true}, {"foo.aspx", true}, {"x.cgi", true},
		{"backup.sh", true}, {"setup.bash", true},
		// Java web containers.
		{"app.war", true}, {"app.ear", true},
		// Basename rules (no real extension).
		{".htaccess", true}, {".HTACCESS", true},
		{".htpasswd", true},
		{"web.config", true}, {"Web.Config", true},
		// Innocuous.
		{"image.png", false}, {"data.json", false},
		{"noext", false}, {".hidden", false},
	}
	for _, c := range cases {
		if got := hasWebExecutableExtension(c.name); got != c.want {
			t.Errorf("hasWebExecutableExtension(%q) = %v; want %v", c.name, got, c.want)
		}
	}
}
