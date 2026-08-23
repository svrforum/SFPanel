package files

import "testing"

func TestNewMatcher(t *testing.T) {
	cases := []struct {
		query   string
		name    string
		want    bool
		because string
	}{
		// Substring stays the default: it is what typing half a name means.
		{"compose", "docker-compose.yml", true, "substring"},
		{"COMPOSE", "docker-compose.yml", true, "case-insensitive"},
		{"compose", "nginx.conf", false, "no match"},
		{"yml", "docker-compose.yml", true, "substring anywhere"},

		// The case the old search could not express. Typing *.yml matched
		// nothing at all, because the asterisk was compared literally.
		{"*.yml", "docker-compose.yml", true, "glob suffix"},
		{"*.yml", "nginx.conf", false, "glob suffix miss"},
		{"*.yml", "yml-notes.txt", false, "glob anchors the whole name"},
		{"docker-*", "docker-compose.yml", true, "glob prefix"},
		{"*.y?l", "docker-compose.yml", true, "single-character wildcard"},
		{"*.[jy]son", "package.json", true, "character class"},
		{"*.[jy]son", "notes.txt", false, "character class miss"},
		{"*.YML", "docker-compose.yml", true, "glob is case-insensitive too"},

		// An unclosed bracket makes filepath.Match error on every candidate,
		// which would return nothing for a query that looks reasonable on
		// screen. Degrade to substring rather than disappear.
		{"[unclosed", "a[unclosed-name.txt", true, "malformed glob falls back to substring"},

		{"", "anything.txt", false, "empty query matches nothing"},
		{"   ", "anything.txt", false, "whitespace-only query matches nothing"},
	}
	for _, tc := range cases {
		if got := NewMatcher(tc.query)(tc.name); got != tc.want {
			t.Errorf("%s: NewMatcher(%q)(%q) = %v, want %v", tc.because, tc.query, tc.name, got, tc.want)
		}
	}
}
