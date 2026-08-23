package files

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// safeJoin is the Zip Slip defence and the whole reason extraction needs care:
// an entry named ../../etc/cron.d/x writes outside the directory the operator
// chose, and an absolute name does the same.
func TestSafeJoinRefusesEscapes(t *testing.T) {
	dest := "/tmp/extract-here"
	escapes := []string{
		"../evil",
		"../../etc/cron.d/backdoor",
		"a/../../../etc/passwd",
		"./../sneaky",
		"",
	}
	for _, name := range escapes {
		if got, err := safeJoin(dest, name); err == nil {
			t.Errorf("safeJoin(%q) allowed a write to %q", name, got)
		}
	}

	// A sibling directory whose name merely starts with the destination is not
	// inside it — the separator in the prefix check is what stops this.
	if _, err := safeJoin("/tmp/dest", "../dest-evil/x"); err == nil {
		t.Error("safeJoin allowed /tmp/dest-evil to pass as a child of /tmp/dest")
	}
}

// An absolute entry name is contained rather than refused: filepath.Join makes
// "/etc/passwd" land at "<dest>/etc/passwd", which is inside the directory the
// operator chose. That is what GNU tar does when it says "removing leading /
// from member names", and it is the safe outcome — the entry is written where
// the extraction was asked to write, not where the archive asked for.
func TestSafeJoinContainsAbsoluteEntryNames(t *testing.T) {
	dest := "/tmp/extract-here"
	for _, name := range []string{"/etc/passwd", "/absolute/anywhere", "/etc/sfpanel/config.yaml"} {
		got, err := safeJoin(dest, name)
		if err != nil {
			t.Errorf("safeJoin(%q) errored: %v", name, err)
			continue
		}
		if !strings.HasPrefix(got, dest+"/") {
			t.Errorf("safeJoin(%q) = %q, which escapes %q", name, got, dest)
		}
	}
}

func TestSafeJoinAllowsOrdinaryEntries(t *testing.T) {
	dest := "/tmp/extract-here"
	for _, name := range []string{"file.txt", "dir/file.txt", "a/b/c/deep.txt", "./file.txt"} {
		got, err := safeJoin(dest, name)
		if err != nil {
			t.Errorf("safeJoin(%q) refused an ordinary entry: %v", name, err)
			continue
		}
		if !strings.HasPrefix(got, dest) {
			t.Errorf("safeJoin(%q) = %q, which is outside %q", name, got, dest)
		}
	}
}

func writeTarball(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
}

// The end-to-end version: a real archive carrying a traversal entry must not
// write outside the destination, whatever safeJoin does in isolation.
func TestExtractTarRefusesTraversalEntry(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.tar.gz")
	dest := filepath.Join(dir, "dest")
	writeTarball(t, archive, map[string]string{"../escaped.txt": "pwned"})

	if _, err := extractTar(archive, dest); err == nil {
		t.Fatal("extraction of a traversal entry succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); err == nil {
		t.Error("a file was written outside the destination directory")
	}
}

func TestExtractTarRoundTrip(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "ok.tar.gz")
	dest := filepath.Join(dir, "dest")
	writeTarball(t, archive, map[string]string{
		"compose.yml":   "services:\n",
		"conf/app.conf": "key = value\n",
	})

	count, err := extractTar(archive, dest)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if count != 2 {
		t.Errorf("entries = %d, want 2", count)
	}
	got, err := os.ReadFile(filepath.Join(dest, "conf", "app.conf"))
	if err != nil {
		t.Fatalf("nested entry missing: %v", err)
	}
	if string(got) != "key = value\n" {
		t.Errorf("content = %q", got)
	}
}

func TestExtractZipRefusesTraversalEntry(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.zip")
	dest := filepath.Join(dir, "dest")

	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("../escaped.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("pwned")); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	f.Close()

	if _, err := extractZip(archive, dest); err == nil {
		t.Fatal("extraction of a traversal entry succeeded")
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped.txt")); err == nil {
		t.Error("a file was written outside the destination directory")
	}
}

// A zip bomb is a few kilobytes that expands to gigabytes. Without a ceiling
// the first one to arrive fills the filesystem the panel itself runs on.
func TestWriteExtractedRespectsTheBudget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "big.bin")

	if _, err := writeExtracted(target, strings.NewReader(strings.Repeat("a", 100)), 0o644, 0); err == nil {
		t.Error("an exhausted budget still wrote")
	}
	if _, err := writeExtracted(target, strings.NewReader(strings.Repeat("a", 100)), 0o644, 50); err == nil {
		t.Error("content larger than the remaining budget was accepted")
	}

	n, err := writeExtracted(target, strings.NewReader("small"), 0o644, 1000)
	if err != nil {
		t.Fatalf("a file inside the budget was refused: %v", err)
	}
	if n != 5 {
		t.Errorf("wrote %d bytes, want 5", n)
	}
}

// The symlink variant of Zip Slip, which the containment check alone does not
// stop and which an earlier revision of this file was exploitable to.
//
// An archive holds two entries:
//
//	evil              -> /somewhere/outside   (symlink)
//	evil/planted.txt                          (regular file)
//
// Both are lexically inside the destination, so safeJoin passes both. Writing
// the second follows the first and lands outside. The fix refuses to create
// link entries at all; this proves the bytes stay put.
func TestExtractRefusesSymlinkTraversal(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "dest")
	archive := filepath.Join(dir, "evil.tar.gz")

	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "evil", Typeflag: tar.TypeSymlink, Linkname: outside, Mode: 0o777}); err != nil {
		t.Fatal(err)
	}
	body := "PWNED"
	if err := tw.WriteHeader(&tar.Header{Name: "evil/planted.txt", Typeflag: tar.TypeReg, Size: int64(len(body)), Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	f.Close()

	if _, err := extractTar(archive, dest); err != nil {
		t.Logf("extraction reported: %v", err)
	}
	if data, rerr := os.ReadFile(filepath.Join(outside, "planted.txt")); rerr == nil {
		t.Fatalf("escaped the destination: wrote %q outside it", data)
	}
	// The link entry must have been ignored rather than created. What is at
	// that name afterwards is an ordinary directory, made on the way to the
	// second entry — which is the safe outcome: the file lands inside the
	// destination instead of following a link out of it.
	if info, lerr := os.Lstat(filepath.Join(dest, "evil")); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
		t.Error("a symlink entry was created; link entries are refused")
	}
	if data, rerr := os.ReadFile(filepath.Join(dest, "evil", "planted.txt")); rerr != nil || string(data) != "PWNED" {
		t.Errorf("the file should have landed inside the destination; got %q, %v", data, rerr)
	}
}

// The archive can no longer create a link, but one may already be there —
// left by an earlier extraction or by anything else on the box. os.MkdirAll
// follows an existing symlink component without comment.
func TestExtractRefusesPreExistingSymlinkInPath(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dest, "evil")); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(dir, "ordinary.tar.gz")
	writeTarball(t, archive, map[string]string{"evil/planted.txt": "PWNED"})

	if _, err := extractTar(archive, dest); err == nil {
		t.Error("extraction through a pre-existing symlink was allowed")
	}
	if data, rerr := os.ReadFile(filepath.Join(outside, "planted.txt")); rerr == nil {
		t.Fatalf("escaped through a pre-existing symlink: wrote %q outside dest", data)
	}
}
