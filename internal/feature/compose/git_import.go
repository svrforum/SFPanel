package compose

import (
	"fmt"
	"regexp"
	"strings"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	httpauth "github.com/go-git/go-git/v5/plumbing/transport/http"
)

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
