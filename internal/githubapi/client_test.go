package githubapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/svrforum/SFPanel/internal/api/response"
)

// fakeGitHub points BaseURL at h for the test and counts the requests that
// reach it.
func fakeGitHub(t *testing.T, h http.HandlerFunc) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		h(w, r)
	}))
	prev := BaseURL
	BaseURL = srv.URL
	t.Cleanup(func() { BaseURL = prev; srv.Close() })
	return &calls
}

var ops = Repo{Owner: "acme", Name: "ops"}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

// refusedWith runs one FetchFile against a fake that answers status with the
// given headers, and returns how Refusal answers the error.
func refusedWith(t *testing.T, status int, headers map[string]string) (int, string, map[string]string) {
	t.Helper()
	fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
	})
	_, err := FetchFile(ctx(t), ops, "main", "docker-compose.yml", "", 1<<20)
	require.Error(t, err)
	st, code, _, details := Refusal(err)
	return st, code, details
}

func TestARateLimitedForbiddenIsARateLimitWithTheResetTime(t *testing.T) {
	reset := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	st, code, details := refusedWith(t, http.StatusForbidden, map[string]string{
		"X-RateLimit-Limit":     "60",
		"X-RateLimit-Remaining": "0",
		"X-RateLimit-Reset":     strconv.FormatInt(reset.Unix(), 10),
	})
	require.Equal(t, http.StatusTooManyRequests, st)
	require.Equal(t, response.ErrGitRateLimited, code)
	require.Equal(t, "2026-11-01T12:00:00Z", details["reset_at"])
	require.Equal(t, "60", details["limit"])
}

func TestAForbiddenWithOnlyRetryAfterIsARateLimit(t *testing.T) {
	st, code, details := refusedWith(t, http.StatusForbidden, map[string]string{"Retry-After": "60"})
	require.Equal(t, http.StatusTooManyRequests, st)
	require.Equal(t, response.ErrGitRateLimited, code)
	require.Equal(t, "60", details["retry_after"])
	require.NotEmpty(t, details["reset_at"], "the moment to retry is known from Retry-After")
}

func TestA429IsARateLimit(t *testing.T) {
	st, code, _ := refusedWith(t, http.StatusTooManyRequests, nil)
	require.Equal(t, http.StatusTooManyRequests, st)
	require.Equal(t, response.ErrGitRateLimited, code)
}

// A 403 that carries no rate-limit signal is a token without the permission,
// or SSO enforcement: an auth failure, answered 400 like a refused token.
func TestAForbiddenWithoutRateLimitHeadersIsAnAuthFailure(t *testing.T) {
	st, code, _ := refusedWith(t, http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "59"})
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, response.ErrGitAuthFailed, code)
}

// GitHub refusing the token is not the panel's session ending: the browser
// reads a 401 as that, so the answer is 400.
func TestARefusedTokenIsA400AuthFailure(t *testing.T) {
	st, code, _ := refusedWith(t, http.StatusUnauthorized, nil)
	require.Equal(t, http.StatusBadRequest, st)
	require.Equal(t, response.ErrGitAuthFailed, code)
}

func TestAnUnexpectedStatusIsAFetchFailure(t *testing.T) {
	st, code, _ := refusedWith(t, http.StatusBadGateway, nil)
	require.Equal(t, http.StatusInternalServerError, st)
	require.Equal(t, response.ErrGitCloneFailed, code)
}

func TestRefusalMapsEachSentinel(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{ErrInvalidURL, http.StatusBadRequest, response.ErrInvalidRequest},
		{ErrInvalidPath, http.StatusBadRequest, response.ErrInvalidRequest},
		{ErrRepoNotFound, http.StatusNotFound, response.ErrGitRepoNotFound},
		{ErrPathNotFound, http.StatusNotFound, response.ErrGitPathNotFound},
		{ErrTooLarge, http.StatusBadRequest, response.ErrFileTooLarge},
	}
	for _, tc := range cases {
		st, code, msg, _ := Refusal(tc.err)
		assert.Equal(t, tc.status, st, tc.err.Error())
		assert.Equal(t, tc.code, code, tc.err.Error())
		assert.NotEmpty(t, msg)
	}
}

// A # or ? in a file name is part of the name: it reaches GitHub whole, and
// the ref still arrives as a query value.
func TestAPathWithAHashReachesGitHubAsWrittenWithItsRef(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/ops/contents/tasks/a#b?.yaml" || r.URL.Query().Get("ref") != "feature/x" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("name: x\n"))
	})
	got, err := FetchFile(ctx(t), ops, "feature/x", "tasks/a#b?.yaml", "", 1<<20)
	require.NoError(t, err)
	require.Equal(t, "name: x\n", string(got))
}

// A .. segment is refused before anything is sent.
func TestADotDotSegmentIsRefusedWithoutARequest(t *testing.T) {
	calls := fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("x")) })
	for _, p := range []string{"../secrets.yaml", "tasks/../../x.yaml", "tasks/./x.yaml", "tasks//x.yaml", `tasks\x.yaml`, "tasks/\x00.yaml", ""} {
		_, err := FetchFile(ctx(t), ops, "main", p, "", 1<<20)
		assert.ErrorIs(t, err, ErrInvalidPath, "%q", p)
	}
	_, err := ListDir(ctx(t), ops, "main", "tasks/..", "")
	require.ErrorIs(t, err, ErrInvalidPath)
	require.Zero(t, calls.Load())
}

func TestParseRepoURL(t *testing.T) {
	good := map[string]Repo{
		"https://github.com/acme/ops":             {"acme", "ops"},
		"https://github.com/acme/ops.git":         {"acme", "ops"},
		"https://github.com/my-org/my.repo.git":   {"my-org", "my.repo"},
		"https://github.com/Acme_1/Ops-Tasks.git": {"Acme_1", "Ops-Tasks"},
	}
	for in, want := range good {
		got, err := ParseRepoURL(in)
		assert.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{
		"https://github.com/acme/.git",
		"https://github.com/../..",
		"https://github.com/acme/..",
		"https://github.com/./ops",
		"https://github.com/acme/ops/",
		"https://github.com/acme",
		"https://github.com/acme/ops/tree/main/tasks",
		"http://github.com/acme/ops",
		"git@github.com:acme/ops.git",
		"https://gitlab.com/acme/ops",
		"",
	} {
		_, err := ParseRepoURL(in)
		assert.ErrorIs(t, err, ErrInvalidURL, in)
	}
}

func TestRepoURLIsTheCanonicalHTTPSForm(t *testing.T) {
	r, err := ParseRepoURL("https://github.com/acme/ops.git")
	require.NoError(t, err)
	require.Equal(t, "https://github.com/acme/ops", r.URL())
}

func TestListDirReadsAFolder(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/acme/ops/contents/tasks", r.URL.Path)
		assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", r.URL.Query().Get("ref"))
		assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
		_, _ = w.Write([]byte(`[
			{"type":"file","name":"web.yaml","path":"tasks/web.yaml","sha":"aaa","size":812,"download_url":"https://example.invalid/web.yaml"},
			{"type":"dir","name":"lib","path":"tasks/lib","sha":"bbb","size":0,"download_url":null},
			{"type":"file","name":"vendor","path":"tasks/vendor","sha":"ccc","size":0,"download_url":null},
			{"type":"file","name":"README.md","path":"tasks/README.md","sha":"ddd","size":10,"download_url":"https://example.invalid/README.md"}
		]`))
	})
	got, err := ListDir(ctx(t), ops, "0123456789abcdef0123456789abcdef01234567", "tasks", "")
	require.NoError(t, err)
	require.Equal(t, []Entry{
		{Type: "file", Name: "web.yaml", Path: "tasks/web.yaml", SHA: "aaa", Size: 812},
		{Type: "dir", Name: "lib", Path: "tasks/lib", SHA: "bbb"},
		// A submodule is listed as "file" for compatibility; it is told
		// apart by having nothing to download.
		{Type: "submodule", Name: "vendor", Path: "tasks/vendor", SHA: "ccc"},
		{Type: "file", Name: "README.md", Path: "tasks/README.md", SHA: "ddd", Size: 10},
	}, got)
}

func TestListDirOnAFileIsThatOneFile(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/acme/ops/contents/tasks/web.yaml", r.URL.Path)
		_, _ = w.Write([]byte(`{"type":"file","name":"web.yaml","path":"tasks/web.yaml","sha":"aaa","size":812,
			"download_url":"https://example.invalid/web.yaml","encoding":"base64","content":"bmFtZTogd2ViCg=="}`))
	})
	got, err := ListDir(ctx(t), ops, "main", "tasks/web.yaml", "")
	require.NoError(t, err)
	require.Equal(t, []Entry{{Type: "file", Name: "web.yaml", Path: "tasks/web.yaml", SHA: "aaa", Size: 812}}, got)
}

func TestListDirOfTheRootAsksForContentsItself(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/acme/ops/contents", r.URL.Path)
		_, _ = w.Write([]byte(`[]`))
	})
	got, err := ListDir(ctx(t), ops, "main", "", "")
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestListDirOfAMissingFolderIsPathNotFound(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	_, err := ListDir(ctx(t), ops, "main", "nope", "")
	require.ErrorIs(t, err, ErrPathNotFound)
}

func TestResolveRefAsksForTheBareSHA(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/acme/ops/commits/feature/x", r.URL.Path)
		assert.Equal(t, "application/vnd.github.sha", r.Header.Get("Accept"))
		_, _ = w.Write([]byte(sha))
	})
	got, err := ResolveRef(ctx(t), ops, "feature/x", "")
	require.NoError(t, err)
	require.Equal(t, sha, got)
}

func TestResolveRefRefusesABodyThatIsNotASHA(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sha":"0123456789abcdef0123456789abcdef01234567"}`))
	})
	_, err := ResolveRef(ctx(t), ops, "main", "")
	require.Error(t, err)
	st, code, _, _ := Refusal(err)
	require.Equal(t, http.StatusInternalServerError, st)
	require.Equal(t, response.ErrGitCloneFailed, code)
}

// An unknown branch is 422 "No commit found"; a repository the caller cannot
// see is 404; an empty repository is 409. None of them has the commit asked for.
func TestResolveRefOfAnUnknownRefIsPathNotFound(t *testing.T) {
	for _, status := range []int{http.StatusUnprocessableEntity, http.StatusNotFound, http.StatusConflict} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
			_, err := ResolveRef(ctx(t), ops, "nope", "")
			require.ErrorIs(t, err, ErrPathNotFound)
		})
	}
}

// The token travels in the Authorization header and nowhere else: never in
// the URL, which browsers, proxies and error messages keep.
func TestTheTokenIsSentOnlyAsABearerHeader(t *testing.T) {
	const token = "github_pat_example"
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer "+token, r.Header.Get("Authorization"))
		assert.NotContains(t, r.URL.RawQuery, token)
		assert.NotContains(t, r.URL.Path, token)
		switch {
		case strings.Contains(r.URL.Path, "/commits/"):
			_, _ = w.Write([]byte("0123456789abcdef0123456789abcdef01234567"))
		case strings.HasSuffix(r.URL.Path, "/contents/tasks"):
			_, _ = w.Write([]byte(`[]`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	})
	_, err := ResolveRef(ctx(t), ops, "main", token)
	require.NoError(t, err)
	_, err = ListDir(ctx(t), ops, "main", "tasks", token)
	require.NoError(t, err)
	_, err = FetchFile(ctx(t), ops, "main", "tasks/web.yaml", token, 1<<20)
	require.NoError(t, err)
	ok, err := RepoExists(ctx(t), ops, token)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestWithoutATokenNoAuthorizationIsSent(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		assert.Equal(t, "application/vnd.github.raw", r.Header.Get("Accept"))
		assert.Equal(t, "SFPanel", r.Header.Get("User-Agent"))
		_, _ = w.Write([]byte("x"))
	})
	_, err := FetchFile(ctx(t), ops, "main", "x.yaml", "", 10)
	require.NoError(t, err)
}

func TestFetchFileRefusesOneByteOverTheLimit(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("12345678901")) })
	_, err := FetchFile(ctx(t), ops, "main", "x.yaml", "", 10)
	require.ErrorIs(t, err, ErrTooLarge)

	fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("1234567890")) })
	got, err := FetchFile(ctx(t), ops, "main", "x.yaml", "", 10)
	require.NoError(t, err)
	require.Equal(t, "1234567890", string(got))
}

func TestRepoExists(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/repos/acme/ops", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	})
	ok, err := RepoExists(ctx(t), ops, "")
	require.NoError(t, err)
	require.False(t, ok)
}

// A probe that hit the rate limit has not learned the repository is missing.
func TestRepoExistsReportsARateLimitAsAnError(t *testing.T) {
	fakeGitHub(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTooManyRequests) })
	_, err := RepoExists(ctx(t), ops, "")
	var rl *RateLimitError
	require.True(t, errors.As(err, &rl), "got %v", err)
}

func TestBlobSHAIsGitsObjectID(t *testing.T) {
	require.Equal(t, "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391", BlobSHA([]byte("")))
	// git hash-object of "hello\n"
	require.Equal(t, "ce013625030ba8dba906f756967f9e9ca394464a", BlobSHA([]byte("hello\n")))
}
