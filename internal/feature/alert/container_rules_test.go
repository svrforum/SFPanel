package alert

import "testing"

func TestMatchContainerPattern(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"*", "anything", true},
		{"nginx-*", "nginx-app", true},
		{"nginx-*", "nginx-", true},
		{"nginx-*", "apache-app", false},
		{"nginx-app", "nginx-app", true},
		{"nginx-app", "nginx-app-2", false},
		{"*-prod", "myapp-prod", true},
		{"*-prod", "myapp-dev", false},
		{"foo?bar", "fooXbar", true},
		{"foo?bar", "fooXYbar", false},
		// Regex special characters treated as literals.
		{"a.b", "a.b", true},
		{"a.b", "axb", false},
		// Empty pattern never matches anything.
		{"", "x", false},
	}
	for _, c := range cases {
		got := matchContainerPattern(c.pattern, c.name)
		if got != c.want {
			t.Errorf("match(%q, %q) = %v; want %v", c.pattern, c.name, got, c.want)
		}
	}
}
