package compose

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ErrPathNotFound is returned when the repo exists but the requested compose
// file path does not.
var ErrPathNotFound = errors.New("compose path not found in repo")

// ErrAuthFailed / ErrRepoNotFound are typed errors the handler maps to specific
// HTTP codes without parsing string contents.
var (
	ErrAuthFailed   = errors.New("git auth failed")
	ErrRepoNotFound = errors.New("git repo not found")
)

// importFetchTimeout bounds the GitHub fetch. A single compose file over the
// Contents API returns in well under a second; 30s is a generous slow-network
// margin (the handler also wraps this in a request-scoped context).
const importFetchTimeout = 30 * time.Second

// githubAPIBase is the GitHub REST API root. A package var so tests can point it
// at an httptest server.
var githubAPIBase = "https://api.github.com"

// ImportRequest is the payload for POST /api/v1/compose/import.
// Token is used once to fetch and is never persisted.
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

// validateImportRequest enforces format constraints. It does NOT touch the
// network and does NOT validate the token's value (a wrong token surfaces as a
// 401 from the fetch step).
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

// ownerRepoFromURL extracts owner/repo from a validated github.com HTTPS URL.
func ownerRepoFromURL(repoURL string) (owner, repo string, err error) {
	rest := strings.TrimPrefix(repoURL, "https://github.com/")
	rest = strings.TrimSuffix(rest, ".git")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid github url")
	}
	return parts[0], parts[1], nil
}

// fetchComposeFile downloads a single compose file from a GitHub repo via the
// Contents API (raw media type). It works for public and private (PAT) repos and
// honours the branch via ?ref. Returns ErrAuthFailed / ErrRepoNotFound /
// ErrPathNotFound for the cases the handler maps to specific HTTP statuses.
func fetchComposeFile(ctx context.Context, req ImportRequest) (string, error) {
	owner, repo, err := ownerRepoFromURL(req.URL)
	if err != nil {
		return "", err
	}
	path := req.Path
	if path == "" {
		path = "docker-compose.yml"
	}
	api := fmt.Sprintf("%s/repos/%s/%s/contents/%s", githubAPIBase, owner, repo, strings.TrimPrefix(path, "/"))
	if req.Branch != "" {
		api += "?ref=" + url.QueryEscape(req.Branch)
	}

	body, status, err := githubGet(ctx, api, req.Token, "application/vnd.github.raw")
	if err != nil {
		return "", fmt.Errorf("fetch: %w", err)
	}
	switch status {
	case http.StatusOK:
		return string(body), nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", ErrAuthFailed
	case http.StatusNotFound:
		// The Contents API 404s for both a missing repo and a missing path; one
		// cheap probe distinguishes them so the user gets the right message.
		if repoExists(ctx, owner, repo, req.Token) {
			return "", ErrPathNotFound
		}
		return "", ErrRepoNotFound
	default:
		return "", fmt.Errorf("github contents api returned status %d", status)
	}
}

// repoExists reports whether the repo is visible to the caller (200 vs 404).
func repoExists(ctx context.Context, owner, repo, token string) bool {
	_, status, err := githubGet(ctx, fmt.Sprintf("%s/repos/%s/%s", githubAPIBase, owner, repo), token, "application/vnd.github+json")
	return err == nil && status == http.StatusOK
}

// githubGet performs a GET against the GitHub API with optional bearer auth and
// reads a bounded body.
func githubGet(ctx context.Context, apiURL, token, accept string) ([]byte, int, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, 0, err
	}
	r.Header.Set("Accept", accept)
	r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	r.Header.Set("User-Agent", "SFPanel")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	// Cap the read — a compose file is tiny; never trust the upstream length.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20)) // 8 MiB
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}
