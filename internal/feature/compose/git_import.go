package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	httpauth "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
)

// ErrPathNotFound is returned when the repo cloned but the requested
// compose file path does not exist in the resolved tree.
var ErrPathNotFound = errors.New("compose path not found in repo")

// ErrAuthFailed / ErrRepoNotFound are typed errors returned by
// cloneShallow so the handler can map them to specific HTTP codes
// without parsing string contents.
var (
	ErrAuthFailed   = errors.New("git auth failed")
	ErrRepoNotFound = errors.New("git repo not found")
)

// importCloneTimeout bounds the clone step. Most GitHub repos clone in <2s
// at depth=1; 30s gives slow networks and large compose mono-repos a margin.
const importCloneTimeout = 30 * time.Second

// cloneShallow clones the given URL (depth=1) into an in-memory
// repository. Returns one of ErrAuthFailed / ErrRepoNotFound for
// the two cases the handler maps to specific HTTP statuses.
func cloneShallow(ctx context.Context, url, branch, token string) (*git.Repository, error) {
	opts := &git.CloneOptions{
		URL:          url,
		Depth:        1,
		SingleBranch: true,
	}
	if branch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(branch)
	}
	if token != "" {
		opts.Auth = &httpauth.BasicAuth{Username: "x-access-token", Password: token}
	}
	r, err := git.CloneContext(ctx, memory.NewStorage(), memfs.New(), opts)
	if err != nil {
		// go-git surfaces auth and not-found errors with text bodies.
		// Inspect to map cleanly without leaking transport details.
		msg := strings.ToLower(err.Error())
		switch {
		case strings.Contains(msg, "authentication required"),
			strings.Contains(msg, "authorization failed"),
			strings.Contains(msg, "401"):
			return nil, ErrAuthFailed
		case strings.Contains(msg, "repository not found"),
			strings.Contains(msg, "not found"),
			strings.Contains(msg, "404"):
			return nil, ErrRepoNotFound
		}
		return nil, fmt.Errorf("clone: %w", err)
	}
	return r, nil
}

// ImportRequest is the payload for POST /api/v1/compose/import.
// Token is used once to clone and is never persisted.
type ImportRequest struct {
	URL    string `json:"url"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	Token  string `json:"token"`
	Name   string `json:"name"`
}

var (
	// GitHub HTTPS only: https://github.com/<user>/<repo>(.git)?
	githubURLRe = regexp.MustCompile(`^https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(\.git)?$`)
	// SFPanel project naming: lowercase, digits, hyphen.
	stackNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,49}$`)
)

// validateImportRequest enforces format constraints. It does NOT touch
// the network and does NOT validate the token's value (a wrong token
// surfaces as a 401 from the clone step).
func validateImportRequest(req ImportRequest) error {
	if req.URL == "" {
		return fmt.Errorf("url required")
	}
	if !strings.HasPrefix(req.URL, "https://") {
		return fmt.Errorf("only https github URLs are supported")
	}
	if !githubURLRe.MatchString(req.URL) {
		return fmt.Errorf("only github.com URLs are supported")
	}
	if !stackNameRe.MatchString(req.Name) {
		return fmt.Errorf("stack name must be 1-50 chars, lowercase/digits/hyphen, start with letter or digit")
	}
	return nil
}

// buildCloneOptions translates a validated ImportRequest into a
// *git.CloneOptions ready for go-git. Token, when present, is sent as
// the GitHub PAT-over-Basic auth pattern (any non-empty username + the
// token as password). Branch, when empty, leaves go-git on the remote
// default branch (HEAD).
func buildCloneOptions(req ImportRequest) *git.CloneOptions {
	opts := &git.CloneOptions{
		URL:          req.URL,
		Depth:        1,
		SingleBranch: true,
	}
	if req.Branch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(req.Branch)
	}
	if req.Token != "" {
		opts.Auth = &httpauth.BasicAuth{
			Username: "x-access-token",
			Password: req.Token,
		}
	}
	return opts
}

// readComposeFromRepo extracts the file at `path` on `branch` from an
// already-opened git.Repository. Branch defaults to HEAD if empty,
// path defaults to "docker-compose.yml" if empty.
func readComposeFromRepo(ctx context.Context, r *git.Repository, branch, path string) (string, error) {
	if path == "" {
		path = "docker-compose.yml"
	}
	var ref *plumbing.Reference
	var err error
	if branch == "" {
		ref, err = r.Head()
	} else {
		// Try local branch first; fall back to remote branch (for cloned repos).
		ref, err = r.Reference(plumbing.NewBranchReferenceName(branch), true)
		if err != nil {
			ref, err = r.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
		}
	}
	if err != nil {
		return "", fmt.Errorf("resolve branch %q: %w", branch, err)
	}
	commit, err := r.CommitObject(ref.Hash())
	if err != nil {
		return "", fmt.Errorf("commit object: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return "", fmt.Errorf("commit tree: %w", err)
	}
	file, err := tree.File(path)
	if err != nil {
		if errors.Is(err, object.ErrFileNotFound) {
			return "", ErrPathNotFound
		}
		return "", fmt.Errorf("tree.File: %w", err)
	}
	rdr, err := file.Reader()
	if err != nil {
		return "", fmt.Errorf("file reader: %w", err)
	}
	defer rdr.Close()
	body, err := io.ReadAll(rdr)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	_ = ctx // ctx not used by go-git tree walk; reserved for future cancel
	return string(body), nil
}
