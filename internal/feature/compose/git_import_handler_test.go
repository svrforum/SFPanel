package compose

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// refusalOf reads error.code and error.details out of a refused answer.
func refusalOf(t *testing.T, rec *httptest.ResponseRecorder) (string, map[string]string) {
	t.Helper()
	var resp struct {
		Success bool `json:"success"`
		Error   struct {
			Code    string            `json:"code"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), rec.Body.String())
	require.False(t, resp.Success)
	return resp.Error.Code, resp.Error.Details
}

// GitHub's hourly allowance running out is not the token being refused: an
// anonymous caller behind a shared address hits it with a public repository,
// and "add a token" is the wrong advice. The answer is 429 GIT_RATE_LIMITED
// with the moment the allowance comes back.
func TestImportFromGitTellsARateLimitFromARefusedToken(t *testing.T) {
	reset := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	withFakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "60")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	})
	h, root := newComposeHandler(t)

	rec := postJSON(t, h.ImportFromGit, http.MethodPost, "/api/v1/docker/compose/import",
		`{"url":"https://github.com/acme/ops","name":"ops"}`, nil)

	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	code, details := refusalOf(t, rec)
	require.Equal(t, "GIT_RATE_LIMITED", code)
	require.Equal(t, "2026-11-01T12:00:00Z", details["reset_at"])
	require.Equal(t, "60", details["limit"])
	_, statErr := os.Stat(filepath.Join(root, "ops"))
	require.True(t, os.IsNotExist(statErr), "a refused import must not write a stack")
}

// A 429 is GitHub saying the same thing; it is not the panel failing.
func TestImportFromGitAnswersGitHubs429AsARateLimit(t *testing.T) {
	withFakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	h, _ := newComposeHandler(t)

	rec := postJSON(t, h.ImportFromGit, http.MethodPost, "/api/v1/docker/compose/import",
		`{"url":"https://github.com/acme/ops","name":"ops"}`, nil)

	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	code, details := refusalOf(t, rec)
	require.Equal(t, "GIT_RATE_LIMITED", code)
	require.Equal(t, "30", details["retry_after"])
}

// https://github.com/acme/.git names no repository. It is the operator's
// input that is wrong, so the answer is 400 INVALID_REQUEST, asked before
// anything reaches GitHub.
func TestImportFromGitRefusesAURLWithNoRepositoryName(t *testing.T) {
	calls := 0
	withFakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	})
	h, _ := newComposeHandler(t)

	rec := postJSON(t, h.ImportFromGit, http.MethodPost, "/api/v1/docker/compose/import",
		`{"url":"https://github.com/acme/.git","name":"ops"}`, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	code, _ := refusalOf(t, rec)
	require.Equal(t, "INVALID_REQUEST", code)
	require.Zero(t, calls, "an invalid URL must not reach GitHub")
}

// A compose file over 8 MiB is refused, not cut at 8 MiB and deployed as
// whatever the first 8 MiB happened to be.
func TestImportFromGitRefusesAFileOverEightMiB(t *testing.T) {
	big := benignCompose + "# " + strings.Repeat("x", 8<<20) + "\n"
	withFakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(big)) })
	h, root := newComposeHandler(t)

	rec := postJSON(t, h.ImportFromGit, http.MethodPost, "/api/v1/docker/compose/import",
		`{"url":"https://github.com/acme/ops","name":"ops"}`, nil)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	code, _ := refusalOf(t, rec)
	require.Equal(t, "FILE_TOO_LARGE", code)
	_, statErr := os.Stat(filepath.Join(root, "ops"))
	require.True(t, os.IsNotExist(statErr), "an oversized file must not become a stack")
}

// A path with # or ? in a file name is fetched as written: the name reaches
// GitHub whole, and the branch still rides along as ref.
func TestImportFromGitFetchesAPathWithAHashAsWritten(t *testing.T) {
	withFakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/ops/contents/stacks/web#1?.yml" || r.URL.Query().Get("ref") != "prod" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(benignCompose))
	})
	h, root := newComposeHandler(t)

	rec := postJSON(t, h.ImportFromGit, http.MethodPost, "/api/v1/docker/compose/import",
		`{"url":"https://github.com/acme/ops","branch":"prod","path":"stacks/web#1?.yml","name":"web"}`, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	written, err := os.ReadFile(filepath.Join(root, "web", "docker-compose.yml"))
	require.NoError(t, err)
	require.Equal(t, benignCompose, string(written))
}
