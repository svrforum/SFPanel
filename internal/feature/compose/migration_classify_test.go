package compose

import "testing"

func TestClassifyBind(t *testing.T) {
	cases := []struct {
		host, stackDir, want string
	}{
		{"./config", "/opt/stacks/jelly", "in-stack"},
		{"data", "/opt/stacks/jelly", "in-stack"},
		{"/opt/stacks/jelly/media", "/opt/stacks/jelly", "in-stack"},
		{"/mnt/media", "/opt/stacks/jelly", "abs"},
		{"/var/run/docker.sock", "/opt/stacks/jelly", "system"},
		{"/run/udev", "/opt/stacks/jelly", "system"},
		{"/opt/stacks", "/opt/stacks/jelly", "system"},
	}
	for _, c := range cases {
		if got := classifyBind(c.host, c.stackDir); got != c.want {
			t.Errorf("classifyBind(%q,%q)=%q want %q", c.host, c.stackDir, got, c.want)
		}
	}
}
