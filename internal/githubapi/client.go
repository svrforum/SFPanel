// Package githubapi reads public and private GitHub repositories through the
// REST API: one file, one folder listing, a branch's commit. Compose's Git
// import and the cluster's task import both go through it, so a refused
// token, a used-up rate limit and a missing path are told apart once, and
// answered with the same codes by both.
//
// The token, when there is one, travels only in the Authorization header. It
// is never put in a URL: a URL ends up in error messages, and in the logs of
// whatever sits in between.
package githubapi

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
)

// BaseURL is the GitHub REST API root. A package var so tests can point it at
// an httptest server.
var BaseURL = "https://api.github.com"

// CallTimeout bounds each call to GitHub. One file or one listing comes back
// in well under a second; 30 s is a generous slow-network margin.
const CallTimeout = 30 * time.Second

// listReadCap bounds a folder listing. GitHub lists at most 1,000 entries in
// one folder, well under this.
const listReadCap = 2 << 20

var (
	// ErrInvalidURL: not https://github.com/<owner>/<repo>.
	ErrInvalidURL = errors.New("not a https://github.com/<owner>/<repo> URL")
	// ErrInvalidPath: a path or ref with an empty, "." or ".." segment, a
	// backslash or a NUL. Refused before anything is sent.
	ErrInvalidPath = errors.New("not a clean repository path")
	// ErrAuthFailed: GitHub refused the token (401), or a 403 that is not a
	// rate limit: a token without the permission, or SSO enforcement.
	ErrAuthFailed = errors.New("GitHub refused the credentials")
	// ErrRepoNotFound: the repository does not exist or the caller cannot see
	// it. GitHub does not say which; a private repository without a token
	// looks missing.
	ErrRepoNotFound = errors.New("repository not found")
	// ErrPathNotFound: GitHub answered 404 for a path, or had no commit for a
	// ref. A 404 on a path is also what a missing repository answers;
	// RepoExists tells the two apart.
	ErrPathNotFound = errors.New("path not found in the repository")
	// ErrTooLarge: the answer was longer than the caller accepts.
	ErrTooLarge = errors.New("file larger than allowed")
)

// RateLimitError is GitHub refusing a call because the hourly allowance of
// this address or token is used up, or because of its secondary limit. Each
// field is zero when GitHub did not say.
type RateLimitError struct {
	// Limit is the allowance per hour (X-RateLimit-Limit).
	Limit int
	// Reset is the earliest moment to try again: now plus Retry-After when
	// GitHub sent it, otherwise X-RateLimit-Reset.
	Reset time.Time
	// RetryAfter is GitHub's Retry-After.
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.Reset.IsZero() {
		return "GitHub rate limit reached"
	}
	return "GitHub rate limit reached until " + e.Reset.UTC().Format(time.RFC3339)
}

// Repo is one GitHub repository.
type Repo struct{ Owner, Name string }

// repoURLRe takes the owner and the name, with an optional .git that
// ParseRepoURL strips. A trailing slash, a deeper path such as the browser's
// /tree/<branch>/<dir>, and any other host are refused.
var repoURLRe = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)$`)

// ParseRepoURL reads https://github.com/<owner>/<repo>[.git].
func ParseRepoURL(s string) (Repo, error) {
	m := repoURLRe.FindStringSubmatch(s)
	if m == nil {
		return Repo{}, ErrInvalidURL
	}
	r := Repo{Owner: m[1], Name: strings.TrimSuffix(m[2], ".git")}
	if !r.valid() {
		return Repo{}, ErrInvalidURL
	}
	return r, nil
}

// valid refuses what the pattern lets through but names no repository:
// "https://github.com/acme/.git", "https://github.com/../..".
func (r Repo) valid() bool {
	for _, s := range []string{r.Owner, r.Name} {
		if s == "" || s == "." || s == ".." || !repoPartRe.MatchString(s) {
			return false
		}
	}
	return true
}

var repoPartRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// URL is the repository's canonical address, without .git: what a task
// imported from it records as its source.
func (r Repo) URL() string { return "https://github.com/" + r.Owner + "/" + r.Name }

func (r Repo) api() string {
	return BaseURL + "/repos/" + url.PathEscape(r.Owner) + "/" + url.PathEscape(r.Name)
}

// escapePath checks a repository path and escapes it segment by segment, so
// a # or ? in a name stays part of the name. One leading or trailing slash is
// forgiven; an empty path is allowed only where it means the root.
func escapePath(p string, allowEmpty bool) (string, error) {
	p = strings.Trim(p, "/")
	if p == "" {
		if allowEmpty {
			return "", nil
		}
		return "", ErrInvalidPath
	}
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if s == "" || s == "." || s == ".." || strings.ContainsAny(s, "\\\x00") {
			return "", ErrInvalidPath
		}
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/"), nil
}

// withRef adds the ref as a query value; an empty ref leaves GitHub's
// default branch.
func withRef(u, ref string) string {
	if ref == "" {
		return u
	}
	return u + "?" + url.Values{"ref": {ref}}.Encode()
}

// get sends one GET and reads at most readCap bytes of a 200. Any other
// status comes back as the error classify makes of it, with the status.
func get(ctx context.Context, apiURL, token, accept string, readCap int64) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "SFPanel")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// *url.Error names the URL, which never holds the token.
		return nil, 0, fmt.Errorf("ask GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, resp.StatusCode, classify(resp)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, readCap+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read GitHub's answer: %w", err)
	}
	if int64(len(body)) > readCap {
		return nil, resp.StatusCode, fmt.Errorf("%w (over %d bytes)", ErrTooLarge, readCap)
	}
	return body, resp.StatusCode, nil
}

// classify is the one reading of a status other than 200, for every call.
// GitHub signals a rate limit with 429, or with 403 plus either
// X-RateLimit-Remaining: 0 (the hourly allowance) or Retry-After (the
// secondary limit). Every other 403 is a refusal of the credentials.
func classify(resp *http.Response) error {
	h := resp.Header
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrAuthFailed
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusForbidden && (h.Get("X-RateLimit-Remaining") == "0" || h.Get("Retry-After") != ""):
		return rateLimitOf(h, time.Now())
	case resp.StatusCode == http.StatusForbidden:
		return ErrAuthFailed
	case resp.StatusCode == http.StatusNotFound:
		return ErrPathNotFound
	default:
		return fmt.Errorf("GitHub answered HTTP %d", resp.StatusCode)
	}
}

// rateLimitOf reads when to try again. GitHub's rule: wait Retry-After when it
// is given; otherwise, when nothing remains, until X-RateLimit-Reset.
func rateLimitOf(h http.Header, now time.Time) *RateLimitError {
	e := &RateLimitError{}
	e.Limit, _ = strconv.Atoi(h.Get("X-RateLimit-Limit"))
	if s, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil && s > 0 {
		e.RetryAfter = time.Duration(s) * time.Second
		e.Reset = now.Add(e.RetryAfter).UTC().Truncate(time.Second)
	} else if h.Get("X-RateLimit-Remaining") == "0" {
		if n, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil && n > 0 {
			e.Reset = time.Unix(n, 0).UTC()
		}
	}
	return e
}

var commitSHARe = regexp.MustCompile(`^[0-9a-f]{40}$`)

// ResolveRef answers the commit SHA a branch, tag or SHA points at now. An
// unknown ref (422), a repository the caller cannot see (404) and an empty
// repository (409) all come back as ErrPathNotFound: none has that commit.
func ResolveRef(ctx context.Context, r Repo, ref, token string) (string, error) {
	if !r.valid() {
		return "", ErrInvalidURL
	}
	escaped, err := escapePath(ref, false)
	if err != nil {
		return "", err
	}
	body, status, err := get(ctx, r.api()+"/commits/"+escaped, token, "application/vnd.github.sha", 1<<10)
	if status == http.StatusUnprocessableEntity || status == http.StatusConflict {
		return "", ErrPathNotFound
	}
	if err != nil {
		return "", err
	}
	sha := string(bytes.TrimSpace(body))
	if !commitSHARe.MatchString(sha) {
		return "", errors.New("GitHub's answer to a commit lookup is not a commit SHA")
	}
	return sha, nil
}

// Entry is one item of a folder listing.
type Entry struct {
	// Type is "file", "dir", "symlink" or "submodule".
	Type string
	Name string
	Path string
	// SHA is the git object ID: for a file, the blob SHA BlobSHA computes.
	SHA  string
	Size int64
}

type contentItem struct {
	Type        string  `json:"type"`
	Name        string  `json:"name"`
	Path        string  `json:"path"`
	SHA         string  `json:"sha"`
	Size        int64   `json:"size"`
	DownloadURL *string `json:"download_url"`
}

func (c contentItem) entry() Entry {
	t := c.Type
	// A folder listing shows a submodule as "file" for compatibility; it
	// is the one "file" with nothing to download.
	if t == "file" && c.DownloadURL == nil {
		t = "submodule"
	}
	return Entry{Type: t, Name: c.Name, Path: c.Path, SHA: c.SHA, Size: c.Size}
}

// ListDir lists one folder, one level deep, at ref. An empty dir is the
// repository root. A path that names a file answers that one file. What to
// keep (files, by suffix) is the caller's choice.
func ListDir(ctx context.Context, r Repo, ref, dir, token string) ([]Entry, error) {
	if !r.valid() {
		return nil, ErrInvalidURL
	}
	escaped, err := escapePath(dir, true)
	if err != nil {
		return nil, err
	}
	u := r.api() + "/contents"
	if escaped != "" {
		u += "/" + escaped
	}
	body, _, err := get(ctx, withRef(u, ref), token, "application/vnd.github+json", listReadCap)
	if err != nil {
		return nil, err
	}
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '{' {
		var one contentItem
		if err := json.Unmarshal(body, &one); err != nil {
			return nil, fmt.Errorf("read GitHub's listing: %w", err)
		}
		return []Entry{one.entry()}, nil
	}
	var items []contentItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("read GitHub's listing: %w", err)
	}
	out := make([]Entry, 0, len(items))
	for _, it := range items {
		out = append(out, it.entry())
	}
	return out, nil
}

// FetchFile reads one file's raw bytes at ref. A file longer than max is
// ErrTooLarge, never cut short.
func FetchFile(ctx context.Context, r Repo, ref, path, token string, max int64) ([]byte, error) {
	if !r.valid() {
		return nil, ErrInvalidURL
	}
	escaped, err := escapePath(path, false)
	if err != nil {
		return nil, err
	}
	body, _, err := get(ctx, withRef(r.api()+"/contents/"+escaped, ref), token, "application/vnd.github.raw", max)
	return body, err
}

// RepoExists reports whether the caller can see the repository. A rate
// limit, or any answer but 200 and 404, is an error: the probe has learned
// nothing, and must not read as "missing".
func RepoExists(ctx context.Context, r Repo, token string) (bool, error) {
	if !r.valid() {
		return false, ErrInvalidURL
	}
	_, status, err := get(ctx, r.api(), token, "application/vnd.github+json", 1<<20)
	if status == http.StatusNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// BlobSHA is git's object ID for b as a file: what a folder listing's sha is
// for a file, so stored content can be compared without fetching it again.
func BlobSHA(b []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(b))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// Refusal is how both imports answer an error from this package: the HTTP
// status, the code, an English fallback message (the screen words the code)
// and the details the message names.
func Refusal(err error) (status int, code, message string, details map[string]string) {
	var rl *RateLimitError
	switch {
	case errors.As(err, &rl):
		d := map[string]string{}
		if !rl.Reset.IsZero() {
			d["reset_at"] = rl.Reset.UTC().Format(time.RFC3339)
		}
		if rl.Limit > 0 {
			d["limit"] = strconv.Itoa(rl.Limit)
		}
		if rl.RetryAfter > 0 {
			d["retry_after"] = strconv.Itoa(int(rl.RetryAfter / time.Second))
		}
		return http.StatusTooManyRequests, response.ErrGitRateLimited,
			"GitHub's request allowance is used up; try again later, or add a token", d
	case errors.Is(err, ErrInvalidURL):
		return http.StatusBadRequest, response.ErrInvalidRequest,
			"the URL must be https://github.com/<owner>/<repo>", nil
	case errors.Is(err, ErrInvalidPath):
		return http.StatusBadRequest, response.ErrInvalidRequest,
			"the path or branch has an empty, \".\" or \"..\" segment, a backslash or a NUL", nil
	case errors.Is(err, ErrAuthFailed):
		// 400, not 401: GitHub refused the token, and the browser reads a
		// 401 as this panel's session ending.
		return http.StatusBadRequest, response.ErrGitAuthFailed,
			"GitHub refused the token, or the repository needs one with access", nil
	case errors.Is(err, ErrRepoNotFound):
		return http.StatusNotFound, response.ErrGitRepoNotFound,
			"repository not found; if it is private, add a token", nil
	case errors.Is(err, ErrPathNotFound):
		return http.StatusNotFound, response.ErrGitPathNotFound,
			"nothing at that path on that branch", nil
	case errors.Is(err, ErrTooLarge):
		return http.StatusBadRequest, response.ErrFileTooLarge,
			response.SanitizeOutput(err.Error()), nil
	default:
		// The page words this code, so the cause rides in the details too:
		// the sentence would otherwise hide the only clue to what went wrong.
		cause := response.SanitizeOutput(err.Error())
		return http.StatusInternalServerError, response.ErrGitCloneFailed, cause, map[string]string{"cause": cause}
	}
}
