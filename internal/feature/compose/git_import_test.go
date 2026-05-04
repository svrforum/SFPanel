package compose

import (
	"context"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	httpauth "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
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

// makeFakeRepo returns a *git.Repository in memory containing the
// given file at the given path on the default branch (HEAD = main).
func makeFakeRepo(t *testing.T, path, content string) *git.Repository {
	t.Helper()
	fs := memfs.New()
	storer := memory.NewStorage()
	r, err := git.InitWithOptions(storer, fs, git.InitOptions{DefaultBranch: plumbing.Main})
	require.NoError(t, err)

	w, err := r.Worktree()
	require.NoError(t, err)

	f, err := fs.Create(path)
	require.NoError(t, err)
	_, err = f.Write([]byte(content))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = w.Add(path)
	require.NoError(t, err)

	_, err = w.Commit("seed", &git.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "t@x", When: time.Now()},
	})
	require.NoError(t, err)
	return r
}

func TestReadComposeFromRepo_HappyPath(t *testing.T) {
	yaml := "services:\n  web:\n    image: nginx:1.25\n"
	r := makeFakeRepo(t, "docker-compose.yml", yaml)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := readComposeFromRepo(ctx, r, "main", "docker-compose.yml")
	require.NoError(t, err)
	require.Equal(t, yaml, got)
}

func TestReadComposeFromRepo_PathNotFound(t *testing.T) {
	r := makeFakeRepo(t, "docker-compose.yml", "services: {}\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := readComposeFromRepo(ctx, r, "main", "missing.yml")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPathNotFound)
}
