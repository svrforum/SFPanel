package release

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeDownloads serves files under DownloadPrefix the way GitHub serves a
// tag's assets, and counts what was asked for. A name it does not hold
// answers 404, as GitHub does for an asset a release lacks.
type fakeDownloads struct {
	mu    sync.Mutex
	files map[string][]byte // "<version>/<name>" → body
	hits  map[string]int
}

func serveDownloads(t *testing.T) *fakeDownloads {
	t.Helper()
	f := &fakeDownloads{files: map[string][]byte{}, hits: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/download/v")
		f.mu.Lock()
		f.hits[key]++
		body, ok := f.files[key]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	prev := DownloadPrefix
	DownloadPrefix = srv.URL + "/download/v"
	t.Cleanup(func() { DownloadPrefix = prev })
	return f
}

func (f *fakeDownloads) put(version, name string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[version+"/"+name] = body
}

func (f *fakeDownloads) hitsOf(version, name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[version+"/"+name]
}

// withoutAssets is assets minus the named ones, the shape of a release that
// never published them.
func withoutAssets(assets []Asset, names ...string) []Asset {
	var out []Asset
	for _, a := range assets {
		drop := false
		for _, n := range names {
			drop = drop || a.Name == n
		}
		if !drop {
			out = append(out, a)
		}
	}
	return out
}

func checksumsFor(name string, body []byte) []byte {
	sum := sha256.Sum256(body)
	return []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name))
}

func TestTaggedReleaseNamesTheAssetsOfOneTag(t *testing.T) {
	rel := TaggedRelease("0.81.0", "amd64", "arm64")
	if rel.TagName != "v0.81.0" {
		t.Errorf("tag = %q, want v0.81.0", rel.TagName)
	}
	want := []string{
		"sfpanel_0.81.0_linux_amd64.tar.gz",
		"sfpanel_0.81.0_linux_arm64.tar.gz",
		"checksums.txt",
		"checksums.txt.sig",
		"checksums.txt.pem",
		"checksums.txt.sigstore.json",
	}
	if len(rel.Assets) != len(want) {
		t.Fatalf("assets = %+v, want the %d names %v", rel.Assets, len(want), want)
	}
	for i, name := range want {
		a := rel.Assets[i]
		if a.Name != name {
			t.Errorf("asset %d is %q, want %q", i, a.Name, name)
		}
		if url := DownloadPrefix + "0.81.0/" + name; a.BrowserDownloadURL != url {
			t.Errorf("asset %q is at %q, want %q", a.Name, a.BrowserDownloadURL, url)
		}
	}
	if got := ArchiveName("0.81.0", "arm64"); got != "sfpanel_0.81.0_linux_arm64.tar.gz" {
		t.Errorf("ArchiveName = %q", got)
	}
}

func TestVerifiedChecksumsRefusesWhatTheUpdateRefuses(t *testing.T) {
	const v = "0.81.0"
	sums := checksumsFor(ArchiveName(v, "amd64"), []byte("archive"))

	t.Run("a good bundle returns the checksums, never asking for the legacy pair", func(t *testing.T) {
		f := serveDownloads(t)
		leafPEM, sigDER := signTestBlob(t, sums, testBundleSubject, testBundleIssuer)
		f.put(v, "checksums.txt", sums)
		f.put(v, "checksums.txt.sigstore.json", mintTestBundleJSON(t, leafPEM, sigDER, sums, nil))

		var said []string
		got, err := VerifiedChecksums(context.Background(), http.DefaultClient, TaggedRelease(v, "amd64").Assets, v, v,
			func(m string) { said = append(said, m) })
		if err != nil {
			t.Fatalf("VerifiedChecksums: %v", err)
		}
		if string(got) != string(sums) {
			t.Errorf("checksums = %q, want %q", got, sums)
		}
		if n := f.hitsOf(v, "checksums.txt.sig") + f.hitsOf(v, "checksums.txt.pem"); n != 0 {
			t.Errorf("the legacy pair was fetched %d times beside a bundle", n)
		}
		if !strings.Contains(strings.Join(said, "\n"), "Sigstore bundle") {
			t.Errorf("progress = %q, want it to name the bundle", said)
		}
	})

	t.Run("a bundle over other bytes fails the signature", func(t *testing.T) {
		f := serveDownloads(t)
		leafPEM, sigDER := signTestBlob(t, []byte("other bytes"), testBundleSubject, testBundleIssuer)
		f.put(v, "checksums.txt", sums)
		f.put(v, "checksums.txt.sigstore.json", mintTestBundleJSON(t, leafPEM, sigDER, []byte("other bytes"), nil))

		_, err := VerifiedChecksums(context.Background(), http.DefaultClient, TaggedRelease(v, "amd64").Assets, v, v, nil)
		if err == nil || !strings.Contains(err.Error(), "Signature verification failed") {
			t.Fatalf("err = %v, want Signature verification failed", err)
		}
	})

	t.Run("a signed version without a signature is refused", func(t *testing.T) {
		f := serveDownloads(t)
		f.put(v, "checksums.txt", sums)
		assets := withoutAssets(TaggedRelease(v, "amd64").Assets,
			"checksums.txt.sigstore.json", "checksums.txt.sig", "checksums.txt.pem")

		_, err := VerifiedChecksums(context.Background(), http.DefaultClient, assets, v, v, nil)
		if err == nil || !strings.Contains(err.Error(), "Signature required") {
			t.Fatalf("err = %v, want Signature required", err)
		}
	})

	t.Run("a release before signing falls back to the hash alone", func(t *testing.T) {
		const old = "0.12.5"
		f := serveDownloads(t)
		oldSums := checksumsFor(ArchiveName(old, "amd64"), []byte("archive"))
		f.put(old, "checksums.txt", oldSums)
		assets := withoutAssets(TaggedRelease(old, "amd64").Assets,
			"checksums.txt.sigstore.json", "checksums.txt.sig", "checksums.txt.pem")

		var said []string
		got, err := VerifiedChecksums(context.Background(), http.DefaultClient, assets, "0.12.4", old,
			func(m string) { said = append(said, m) })
		if err != nil {
			t.Fatalf("VerifiedChecksums: %v", err)
		}
		if string(got) != string(oldSums) {
			t.Errorf("checksums = %q, want %q", got, oldSums)
		}
		if !strings.Contains(strings.Join(said, "\n"), "falling back to SHA-256 only") {
			t.Errorf("progress = %q, want the fallback named", said)
		}
	})

	t.Run("the legacy signature pair verifies", func(t *testing.T) {
		f := serveDownloads(t)
		leafPEM, sigDER := signTestBlob(t, sums, testBundleSubject, testBundleIssuer)
		f.put(v, "checksums.txt", sums)
		f.put(v, "checksums.txt.sig", []byte(base64.StdEncoding.EncodeToString(sigDER)))
		f.put(v, "checksums.txt.pem", leafPEM)
		assets := withoutAssets(TaggedRelease(v, "amd64").Assets, "checksums.txt.sigstore.json")

		got, err := VerifiedChecksums(context.Background(), http.DefaultClient, assets, v, v, nil)
		if err != nil {
			t.Fatalf("VerifiedChecksums: %v", err)
		}
		if string(got) != string(sums) {
			t.Errorf("checksums = %q, want %q", got, sums)
		}
	})

	t.Run("no checksums.txt is refused", func(t *testing.T) {
		serveDownloads(t)
		assets := withoutAssets(TaggedRelease(v, "amd64").Assets, "checksums.txt")
		_, err := VerifiedChecksums(context.Background(), http.DefaultClient, assets, v, v, nil)
		if err == nil || !strings.Contains(err.Error(), "Release checksums.txt not found; refusing unsigned update") {
			t.Fatalf("err = %v, want checksums.txt not found", err)
		}
	})

	t.Run("checksums.txt over 64 KiB is refused, signed or not", func(t *testing.T) {
		f := serveDownloads(t)
		// Validly signed, so only the size cap stands between it and success.
		big := append([]byte(strings.Repeat("#\n", 32*1024)), sums...)
		leafPEM, sigDER := signTestBlob(t, big, testBundleSubject, testBundleIssuer)
		f.put(v, "checksums.txt", big)
		f.put(v, "checksums.txt.sigstore.json", mintTestBundleJSON(t, leafPEM, sigDER, big, nil))

		_, err := VerifiedChecksums(context.Background(), http.DefaultClient, TaggedRelease(v, "amd64").Assets, v, v, nil)
		if err == nil || !strings.Contains(err.Error(), "checksums.txt exceeds 65536 bytes") {
			t.Fatalf("err = %v, want checksums.txt exceeds 65536 bytes", err)
		}
	})
}

func TestDownloadArchiveChecksItsHash(t *testing.T) {
	const v = "0.81.0"
	name := ArchiveName(v, "amd64")
	body := []byte("a release archive")

	t.Run("a matching archive lands in dir", func(t *testing.T) {
		f := serveDownloads(t)
		f.put(v, name, body)
		dir := t.TempDir()

		path, err := DownloadArchive(context.Background(), http.DefaultClient, TaggedRelease(v, "amd64").Assets, name, checksumsFor(name, body), dir)
		if err != nil {
			t.Fatalf("DownloadArchive: %v", err)
		}
		if path != filepath.Join(dir, name) {
			t.Errorf("path = %q, want %q", path, filepath.Join(dir, name))
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(body) {
			t.Errorf("file = %q (%v), want %q", got, err, body)
		}
	})

	t.Run("a different hash is refused and leaves nothing", func(t *testing.T) {
		f := serveDownloads(t)
		f.put(v, name, body)
		dir := t.TempDir()

		_, err := DownloadArchive(context.Background(), http.DefaultClient, TaggedRelease(v, "amd64").Assets, name, checksumsFor(name, []byte("other")), dir)
		if err == nil || !strings.Contains(err.Error(), "Checksum verification failed") {
			t.Fatalf("err = %v, want Checksum verification failed", err)
		}
		assertEmptyDir(t, dir)
	})

	t.Run("an archive over the cap is refused and leaves nothing", func(t *testing.T) {
		f := serveDownloads(t)
		prev := archiveCap
		archiveCap = int64(len(body)) - 1
		t.Cleanup(func() { archiveCap = prev })
		f.put(v, name, body)
		dir := t.TempDir()

		// The hash matches, so only the cap stands between it and success.
		_, err := DownloadArchive(context.Background(), http.DefaultClient, TaggedRelease(v, "amd64").Assets, name, checksumsFor(name, body), dir)
		if err == nil || !strings.Contains(err.Error(), "Archive exceeds") {
			t.Fatalf("err = %v, want Archive exceeds", err)
		}
		assertEmptyDir(t, dir)
	})

	t.Run("an archive the release lacks is named", func(t *testing.T) {
		serveDownloads(t)
		_, err := DownloadArchive(context.Background(), http.DefaultClient, TaggedRelease(v, "arm64").Assets, name, checksumsFor(name, body), t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "Release asset not found: "+name) {
			t.Fatalf("err = %v, want Release asset not found", err)
		}
	})

	t.Run("a name that is not a file name is refused", func(t *testing.T) {
		f := serveDownloads(t)
		const evil = "../escaped.tar.gz"
		f.put(v, evil, body)
		parent := t.TempDir()
		dir := filepath.Join(parent, "dl")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		assets := []Asset{{Name: evil, BrowserDownloadURL: DownloadPrefix + v + "/" + evil}}

		_, err := DownloadArchive(context.Background(), http.DefaultClient, assets, evil, checksumsFor(evil, body), dir)
		if err == nil || !strings.Contains(err.Error(), "is not a file name") {
			t.Fatalf("err = %v, want is not a file name", err)
		}
		if _, statErr := os.Stat(filepath.Join(parent, "escaped.tar.gz")); statErr == nil {
			t.Error("the archive was written outside dir")
		}
	})
}

// The agent installer's sequence — the tag's assets, its verified checksums,
// one arch's archive — runs against whatever DownloadPrefix points at.
func TestTaggedReleaseVerifiesAndDownloadsOneArch(t *testing.T) {
	const v = "0.81.0"
	f := serveDownloads(t)
	archive := []byte("arm64 archive")
	sums := checksumsFor(ArchiveName(v, "arm64"), archive)
	leafPEM, sigDER := signTestBlob(t, sums, testBundleSubject, testBundleIssuer)
	f.put(v, "checksums.txt", sums)
	f.put(v, "checksums.txt.sigstore.json", mintTestBundleJSON(t, leafPEM, sigDER, sums, nil))
	f.put(v, ArchiveName(v, "arm64"), archive)

	rel := TaggedRelease(v, "arm64")
	got, err := VerifiedChecksums(context.Background(), http.DefaultClient, rel.Assets, v, v, nil)
	if err != nil {
		t.Fatalf("VerifiedChecksums: %v", err)
	}
	path, err := DownloadArchive(context.Background(), http.DefaultClient, rel.Assets, ArchiveName(v, "arm64"), got, t.TempDir())
	if err != nil {
		t.Fatalf("DownloadArchive: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(archive) {
		t.Errorf("archive = %q, want %q", b, archive)
	}
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("dir holds %d entries after a refusal, want none", len(entries))
	}
}
