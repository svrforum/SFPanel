package compose

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/githubapi"
)

// importFetchTimeout bounds the GitHub fetch. A single compose file over the
// Contents API returns in well under a second; 30s is a generous slow-network
// margin (the handler also wraps this in a request-scoped context).
const importFetchTimeout = 30 * time.Second

// ImportRequest is the payload for POST /api/v1/docker/compose/import.
// Token is used once to fetch and is never persisted.
type ImportRequest struct {
	URL    string `json:"url"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	Token  string `json:"token"`
	Name   string `json:"name"`
	// AcknowledgeRisks lifts the risky tier of the compose safety analyser for
	// the fetched file, once the operator has been shown what it asks for. It
	// never lifts the forbidden tier.
	AcknowledgeRisks bool `json:"acknowledge_risks"`
}

// SFPanel project naming: lowercase, digits, hyphen.
var stackNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,49}$`)

// maxComposeFileBytes is the largest compose file the import takes. A larger
// one is refused, not cut short.
const maxComposeFileBytes = 8 << 20

// validateImportRequest enforces format constraints. It does NOT touch the
// network and does NOT validate the token's value (GitHub refusing it surfaces
// from the fetch step, answered 400 GIT_AUTH_FAILED).
func validateImportRequest(req ImportRequest) error {
	if req.URL == "" {
		return fmt.Errorf("url required")
	}
	if !strings.HasPrefix(req.URL, "https://") {
		return fmt.Errorf("only https github URLs are supported")
	}
	if _, err := githubapi.ParseRepoURL(req.URL); err != nil {
		return fmt.Errorf("only https://github.com/<owner>/<repo> URLs are supported")
	}
	if !stackNameRe.MatchString(req.Name) {
		return fmt.Errorf("stack name must be 1-50 chars, lowercase/digits/hyphen, start with letter or digit")
	}
	return nil
}

// fetchComposeFile downloads a single compose file from a GitHub repo via the
// Contents API. It works for public and private (PAT) repos and honours the
// branch via ref. Errors are githubapi's, for githubapi.Refusal to answer.
func fetchComposeFile(ctx context.Context, req ImportRequest) (string, error) {
	repo, err := githubapi.ParseRepoURL(req.URL)
	if err != nil {
		return "", err
	}
	path := req.Path
	if path == "" {
		path = "docker-compose.yml"
	}
	body, err := githubapi.FetchFile(ctx, repo, req.Branch, path, req.Token, maxComposeFileBytes)
	if errors.Is(err, githubapi.ErrPathNotFound) {
		// The Contents API 404s for both a missing repo and a missing path; one
		// cheap probe distinguishes them so the user gets the right message.
		exists, perr := githubapi.RepoExists(ctx, repo, req.Token)
		if perr != nil {
			return "", perr
		}
		if !exists {
			return "", githubapi.ErrRepoNotFound
		}
		return "", githubapi.ErrPathNotFound
	}
	if err != nil {
		return "", err
	}
	return string(body), nil
}
