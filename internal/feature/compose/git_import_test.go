package compose

import (
	"testing"

	httpauth "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/stretchr/testify/require"
)

func TestValidateImportRequest_URLPattern(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		want   bool
		reason string
	}{
		{"happy github https", "https://github.com/foo/bar.git", true, ""},
		{"happy github https no .git", "https://github.com/foo/bar", true, ""},
		{"reject http (insecure)", "http://github.com/foo/bar.git", false, "https only"},
		{"reject ssh form", "git@github.com:foo/bar.git", false, "https only"},
		{"reject non-github", "https://gitlab.com/foo/bar.git", false, "github only"},
		{"reject empty", "", false, "url required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateImportRequest(ImportRequest{URL: tc.url, Name: "stack"})
			if tc.want {
				require.NoError(t, err, tc.reason)
			} else {
				require.Error(t, err, tc.reason)
			}
		})
	}
}

func TestValidateImportRequest_NameRules(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"my-stack", true},
		{"a", true},
		{"123abc", true},
		{"My-Stack", false}, // uppercase rejected
		{"my_stack", false}, // underscore rejected
		{"my stack", false}, // space rejected
		{"", false},         // empty rejected
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateImportRequest(ImportRequest{
				URL:  "https://github.com/foo/bar.git",
				Name: tc.name,
			})
			if tc.want {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestBuildCloneOptions(t *testing.T) {
	t.Run("defaults: depth 1, single-branch, no auth, no ref", func(t *testing.T) {
		opts := buildCloneOptions(ImportRequest{
			URL:  "https://github.com/foo/bar.git",
			Name: "stack",
		})
		require.Equal(t, "https://github.com/foo/bar.git", opts.URL)
		require.Equal(t, 1, opts.Depth)
		require.True(t, opts.SingleBranch)
		require.Empty(t, string(opts.ReferenceName))
		require.Nil(t, opts.Auth)
	})

	t.Run("branch -> plumbing reference", func(t *testing.T) {
		opts := buildCloneOptions(ImportRequest{
			URL:    "https://github.com/foo/bar.git",
			Branch: "main",
			Name:   "stack",
		})
		require.Equal(t, "refs/heads/main", string(opts.ReferenceName))
	})

	t.Run("token -> BasicAuth with PAT password", func(t *testing.T) {
		opts := buildCloneOptions(ImportRequest{
			URL:   "https://github.com/foo/bar.git",
			Token: "ghp_secret",
			Name:  "stack",
		})
		require.NotNil(t, opts.Auth)
		basic, ok := opts.Auth.(*httpauth.BasicAuth)
		require.True(t, ok, "expected *http.BasicAuth")
		require.Equal(t, "x-access-token", basic.Username)
		require.Equal(t, "ghp_secret", basic.Password)
	})
}
