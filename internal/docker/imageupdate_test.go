package docker

import "testing"

func TestParseImageTag(t *testing.T) {
	cases := map[string]string{
		"nginx:latest":                       "latest",
		"nginx":                              "latest",
		"lscr.io/linuxserver/sonarr:develop": "develop",
		"ghcr.io/foo/bar":                    "latest",
		"registry:5000/img:1.2.3":            "1.2.3",
	}
	for in, want := range cases {
		if got := parseImageTag(in); got != want {
			t.Errorf("parseImageTag(%q)=%q want %q", in, got, want)
		}
	}
}

func TestShortSha(t *testing.T) {
	if got := shortSha("sha256:abcdef0123456789"); got != "abcdef012345" {
		t.Errorf("shortSha=%q", got)
	}
	if got := shortSha("abc"); got != "abc" {
		t.Errorf("shortSha short=%q", got)
	}
}

func TestShortRepoDigest(t *testing.T) {
	rd := []string{"nginx@sha256:abcdef0123456789aaaa"}
	if got := shortRepoDigest(rd); got != "abcdef012345" {
		t.Errorf("shortRepoDigest=%q", got)
	}
	if got := shortRepoDigest(nil); got != "" {
		t.Errorf("shortRepoDigest empty=%q", got)
	}
}
