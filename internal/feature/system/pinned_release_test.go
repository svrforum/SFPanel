package system

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/release"
)

// noReleaseAPI fails the test if the release API is asked anything.
func noReleaseAPI(t *testing.T) *atomic.Int32 {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	withReleaseAPI(t, srv.URL)
	return &hits
}

func withDownloadPrefix(t *testing.T, prefix string) {
	t.Helper()
	prev := releaseDownloadPrefix
	releaseDownloadPrefix = prefix
	t.Cleanup(func() { releaseDownloadPrefix = prev })
}

func runPinnedUpdate(t *testing.T, h *Handler, rel any) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"release": rel})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/update?force=true", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	if err := h.RunUpdate(echo.New().NewContext(req, rec)); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	return rec
}

func assetsAt(base, tag, version string) []release.Asset {
	var out []release.Asset
	for _, name := range []string{fmt.Sprintf("sfpanel_%s_linux_%s.tar.gz", version, runtime.GOARCH), "checksums.txt", "checksums.txt.sigstore.json"} {
		out = append(out, release.Asset{Name: name, BrowserDownloadURL: base + tag + "/" + name})
	}
	return out
}

// A cluster update sends each node the release the leader looked up, so the
// API is asked once per update instead of once per node: it allows sixty
// unauthenticated requests an hour per address, which one update of a site's
// nodes behind one address used up, and the nodes after that stopped the
// update with "GitHub API error".
func TestRunUpdateDownloadsThePinnedRelease(t *testing.T) {
	api := noReleaseAPI(t)
	var archive atomic.Int32
	assets := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".tar.gz") {
			archive.Add(1)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer assets.Close()
	withDownloadPrefix(t, assets.URL+"/download/")

	rec := runPinnedUpdate(t, newHandler("0.13.10"), map[string]any{
		"tag_name": "v0.14.0", "assets": assetsAt(assets.URL+"/download/", "v0.14.0", "0.14.0"),
	})
	if api.Load() != 0 {
		t.Fatalf("the release API was asked %d times", api.Load())
	}
	if archive.Load() == 0 {
		t.Fatalf("the pinned archive was never fetched: %d %s", rec.Code, rec.Body)
	}
}

// What the orchestrator sends decides where to download from, never what is
// installed — but a download from elsewhere is refused before anything is
// fetched.
func TestRunUpdateRefusesAPinnedAssetElsewhere(t *testing.T) {
	api := noReleaseAPI(t)
	rec := runPinnedUpdate(t, newHandler("0.13.10"), map[string]any{
		"tag_name": "v0.14.0",
		"assets":   []release.Asset{{Name: fmt.Sprintf("sfpanel_0.14.0_linux_%s.tar.gz", runtime.GOARCH), BrowserDownloadURL: "https://example.com/sfpanel.tar.gz"}},
	})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "UPDATE_FAILED") || !strings.Contains(rec.Body.String(), "release download") {
		t.Fatalf("an asset elsewhere: %d %s", rec.Code, rec.Body)
	}
	if api.Load() != 0 {
		t.Fatal("the release API was asked")
	}
}

// A node already on the pinned release answers at once, without asking the
// API — an up-to-date node used to spend a request on finding that out.
func TestRunUpdatePinnedToTheCurrentVersionIsUpToDate(t *testing.T) {
	api := noReleaseAPI(t)
	rec := runPinnedUpdate(t, newHandler("0.14.0"), map[string]any{"tag_name": "v0.14.0", "assets": []release.Asset{}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "up_to_date") || api.Load() != 0 {
		t.Fatalf("pinned to the current version: %d %s (api %d)", rec.Code, rec.Body, api.Load())
	}
}

// A release as GitHub lists it is taken whole: a cluster update sends every
// asset, and any one that is not a download of the tag refuses the update,
// fetched or not. These are v0.79.0's twenty as the API listed them on
// 2026-10-20, and this is the test that holds releaseDownloadPrefix's own
// value to GitHub's addresses.
func TestRunUpdateAcceptsAPinnedReleaseAsGitHubListsIt(t *testing.T) {
	api := noReleaseAPI(t)
	const dir = "https://github.com/svrforum/SFPanel/releases/download/v0.79.0/"
	var assets []release.Asset
	for _, name := range []string{
		"android-checksums.txt", "checksums.txt", "checksums.txt.pem", "checksums.txt.sig", "checksums.txt.sigstore.json",
		"latest.json", "SFPanel-0.79.0-1.x86_64.rpm", "SFPanel-Android-0.1.6.apk", "SFPanel.app.tar.gz", "SFPanel.app.tar.gz.sig",
		"SFPanel_0.79.0_aarch64.dmg", "SFPanel_0.79.0_amd64.AppImage", "SFPanel_0.79.0_amd64.AppImage.sig", "SFPanel_0.79.0_amd64.deb",
		"sfpanel_0.79.0_linux_amd64.tar.gz", "sfpanel_0.79.0_linux_arm64.tar.gz", "SFPanel_0.79.0_x64-setup.exe",
		"SFPanel_0.79.0_x64-setup.exe.sig", "SFPanel_0.79.0_x64_en-US.msi", "SFPanel_portable_windows_amd64.exe",
	} {
		assets = append(assets, release.Asset{Name: name, BrowserDownloadURL: dir + name})
	}
	rec := runPinnedUpdate(t, newHandler("0.79.0"), map[string]any{"tag_name": "v0.79.0", "assets": assets})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "up_to_date") || api.Load() != 0 {
		t.Fatalf("v0.79.0 as GitHub lists it: %d %s (api %d)", rec.Code, rec.Body, api.Load())
	}
}

// Each way a pinned release can point outside the release it names is
// refused, for that reason. A body that is no pinned release is refused too,
// rather than read as no pin: the node would then install whatever is latest
// in place of what the update named.
func TestPinnedReleaseRefuses(t *testing.T) {
	const dir = "https://github.com/svrforum/SFPanel/releases/download/"
	pin := func(tag string, a release.Asset) string {
		b, _ := json.Marshal(map[string]any{"release": map[string]any{"tag_name": tag, "assets": []release.Asset{a}}})
		return string(b)
	}
	climb := "v0.14.0/../../../../elsewhere/repo/releases/download/v1"
	for _, tc := range []struct{ name, body, want string }{
		{"another tag's download", pin("v0.14.0", release.Asset{Name: "checksums.txt", BrowserDownloadURL: dir + "v0.13.0/checksums.txt"}), "is not a release download"},
		{"a name that climbs out of the tag", pin("v0.14.0", release.Asset{Name: "../v0.13.0/checksums.txt", BrowserDownloadURL: dir + "v0.14.0/../v0.13.0/checksums.txt"}), "is not a release download"},
		{"a tag that climbs out of the project", pin(climb, release.Asset{Name: "checksums.txt", BrowserDownloadURL: dir + climb + "/checksums.txt"}), "is not a version"},
		{"too large", `{"release":{"tag_name":"v0.14.0"},"pad":"` + strings.Repeat("x", maxPinnedReleaseBody) + `"}`, "too large"},
		{"not JSON", "release=v0.14.0", "not JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/system/update", strings.NewReader(tc.body))
			rel, err := pinnedRelease(echo.New().NewContext(req, httptest.NewRecorder()))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %+v, %v; want an error saying %q", rel, err, tc.want)
			}
		})
	}
}
