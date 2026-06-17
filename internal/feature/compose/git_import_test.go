package compose

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestOwnerRepoFromURL(t *testing.T) {
	cases := []struct {
		url, owner, repo string
		ok               bool
	}{
		{"https://github.com/foo/bar", "foo", "bar", true},
		{"https://github.com/foo/bar.git", "foo", "bar", true},
		{"https://github.com/my-org/my.repo.git", "my-org", "my.repo", true},
		{"https://github.com/foo", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			owner, repo, err := ownerRepoFromURL(tc.url)
			if !tc.ok {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.owner, owner)
			require.Equal(t, tc.repo, repo)
		})
	}
}

// withFakeGitHub points githubAPIBase at the given handler for the test's scope.
func withFakeGitHub(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	prev := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = prev; srv.Close() })
}

func TestFetchComposeFile_HappyPath(t *testing.T) {
	yaml := "services:\n  web:\n    image: nginx:1.25\n"
	withFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/repos/foo/bar/contents/docker-compose.yml", r.URL.Path)
		require.Equal(t, "main", r.URL.Query().Get("ref"))
		require.Equal(t, "application/vnd.github.raw", r.Header.Get("Accept"))
		require.Empty(t, r.Header.Get("Authorization")) // no token
		_, _ = w.Write([]byte(yaml))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := fetchComposeFile(ctx, ImportRequest{URL: "https://github.com/foo/bar", Branch: "main", Path: "docker-compose.yml"})
	require.NoError(t, err)
	require.Equal(t, yaml, got)
}

func TestFetchComposeFile_TokenSent(t *testing.T) {
	withFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer ghp_secret", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("services: {}\n"))
	})
	ctx := context.Background()
	_, err := fetchComposeFile(ctx, ImportRequest{URL: "https://github.com/foo/bar", Token: "ghp_secret"})
	require.NoError(t, err)
}

func TestFetchComposeFile_AuthFailed(t *testing.T) {
	withFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	_, err := fetchComposeFile(context.Background(), ImportRequest{URL: "https://github.com/foo/bar", Token: "bad"})
	require.ErrorIs(t, err, ErrAuthFailed)
}

func TestFetchComposeFile_PathNotFound(t *testing.T) {
	withFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		// contents path 404, but the repo probe returns 200 -> ErrPathNotFound.
		if strings.HasPrefix(r.URL.Path, "/repos/foo/bar/contents/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK) // repo exists
	})
	_, err := fetchComposeFile(context.Background(), ImportRequest{URL: "https://github.com/foo/bar", Path: "missing.yml"})
	require.ErrorIs(t, err, ErrPathNotFound)
}

func TestFetchComposeFile_RepoNotFound(t *testing.T) {
	withFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // both contents and repo probe 404
	})
	_, err := fetchComposeFile(context.Background(), ImportRequest{URL: "https://github.com/foo/bar"})
	require.ErrorIs(t, err, ErrRepoNotFound)
}
