package compose

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLeadingBytes(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"1234\t/v", 1234},
		{"  56 /x", 56},
		{"0\t/empty", 0},
		{"nope", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseLeadingBytes([]byte(c.in)); got != c.want {
			t.Errorf("parseLeadingBytes(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestArchiveBindToFileRoundTrip(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "bind.tar")
	n, sha, err := archiveBindToFile(context.Background(), src, dst)
	if err != nil {
		t.Fatalf("archiveBindToFile: %v", err)
	}
	if n <= 0 || len(sha) != 64 {
		t.Fatalf("size=%d sha=%q, want size>0 and 64-hex sha", n, sha)
	}
	// The archive must carry the entry under its basename so restore recreates it.
	base := filepath.Base(src)
	f, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name == base+"/hello.txt" || strings.HasSuffix(hdr.Name, "/hello.txt") {
			found = true
		}
	}
	if !found {
		t.Errorf("archive missing %s/hello.txt", base)
	}
}

func TestArchiveBindSkipsMissing(t *testing.T) {
	_, _, err := archiveBindToFile(context.Background(), "/no/such/path/xyzzy", filepath.Join(t.TempDir(), "x.tar"))
	if !errors.Is(err, errSkipSpecial) {
		t.Fatalf("err = %v, want errSkipSpecial", err)
	}
}

func TestArchiveBindSkipsSocket(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skipf("cannot create unix socket: %v", err)
	}
	defer l.Close()
	_, _, err = archiveBindToFile(context.Background(), sock, filepath.Join(t.TempDir(), "x.tar"))
	if !errors.Is(err, errSkipSpecial) {
		t.Fatalf("err = %v, want errSkipSpecial for a socket", err)
	}
}

func TestTarMemberSafe(t *testing.T) {
	safe := []string{"a", "a/b", "a/b/c.txt", "config/app.conf", "./x"}
	for _, s := range safe {
		if !tarMemberSafe(s) {
			t.Errorf("tarMemberSafe(%q) = false, want true", s)
		}
	}
	unsafe := []string{"", "/abs", "/etc/passwd", "../x", "a/../../b", "../../etc", "a/..", ".."}
	for _, s := range unsafe {
		if tarMemberSafe(s) {
			t.Errorf("tarMemberSafe(%q) = true, want false", s)
		}
	}
}

func TestValidateTarSafeRejectsTraversal(t *testing.T) {
	// Clean archive passes.
	good := filepath.Join(t.TempDir(), "good.tar")
	writeTar(t, good, []tar.Header{
		{Name: "data/file.txt", Typeflag: tar.TypeReg, Size: 3, Mode: 0o644},
	}, []string{"abc"})
	if err := validateTarSafe(good); err != nil {
		t.Fatalf("clean archive rejected: %v", err)
	}
	// Traversal member rejected.
	bad := filepath.Join(t.TempDir(), "bad.tar")
	writeTar(t, bad, []tar.Header{
		{Name: "../../etc/cron.d/evil", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644},
	}, []string{"x"})
	if err := validateTarSafe(bad); err == nil {
		t.Fatal("traversal member must be rejected")
	}
	// Absolute symlink rejected.
	link := filepath.Join(t.TempDir(), "link.tar")
	writeTar(t, link, []tar.Header{
		{Name: "evil", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777},
	}, []string{""})
	if err := validateTarSafe(link); err == nil {
		t.Fatal("absolute symlink must be rejected")
	}
}

func TestValidateTarSafeRejectsSpecialAndSetuid(t *testing.T) {
	// Char device node rejected — extraction as root would materialize it.
	dev := filepath.Join(t.TempDir(), "dev.tar")
	writeTar(t, dev, []tar.Header{
		{Name: "data/null", Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3, Mode: 0o666},
	}, []string{""})
	if err := validateTarSafe(dev); err == nil {
		t.Fatal("char device member must be rejected")
	}
	// FIFO rejected.
	fifo := filepath.Join(t.TempDir(), "fifo.tar")
	writeTar(t, fifo, []tar.Header{
		{Name: "data/pipe", Typeflag: tar.TypeFifo, Mode: 0o644},
	}, []string{""})
	if err := validateTarSafe(fifo); err == nil {
		t.Fatal("fifo member must be rejected")
	}
	// setuid regular file rejected — would plant a setuid-root binary on the target.
	suid := filepath.Join(t.TempDir(), "suid.tar")
	writeTar(t, suid, []tar.Header{
		{Name: "data/rootshell", Typeflag: tar.TypeReg, Size: 1, Mode: 0o4755},
	}, []string{"x"})
	if err := validateTarSafe(suid); err == nil {
		t.Fatal("setuid regular file must be rejected")
	}
	// setgid regular file rejected.
	sgid := filepath.Join(t.TempDir(), "sgid.tar")
	writeTar(t, sgid, []tar.Header{
		{Name: "data/sgid", Typeflag: tar.TypeReg, Size: 1, Mode: 0o2755},
	}, []string{"y"})
	if err := validateTarSafe(sgid); err == nil {
		t.Fatal("setgid regular file must be rejected")
	}
	// setgid DIRECTORY allowed — legitimate group-inheritance, not an exec vector.
	sgidDir := filepath.Join(t.TempDir(), "sgiddir.tar")
	writeTar(t, sgidDir, []tar.Header{
		{Name: "data", Typeflag: tar.TypeDir, Mode: 0o2755},
	}, []string{""})
	if err := validateTarSafe(sgidDir); err != nil {
		t.Fatalf("setgid dir must be allowed: %v", err)
	}
}

func writeTar(t *testing.T, path string, hdrs []tar.Header, bodies []string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	for i, h := range hdrs {
		hc := h
		if err := tw.WriteHeader(&hc); err != nil {
			t.Fatal(err)
		}
		if hc.Typeflag == tar.TypeReg && bodies[i] != "" {
			if _, err := tw.Write([]byte(bodies[i])); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestManifestIdentifierRegexes(t *testing.T) {
	for _, ok := range []string{"proj_data", "nginx", "alpine", "ghcr.io/x/y:v1", "a.b-c_d"} {
		if !validDockerVolume.MatchString(ok) && !validImageRef.MatchString(ok) {
			t.Errorf("%q rejected by both regexes", ok)
		}
	}
	for _, bad := range []string{"-rf", "--label=x", "-v", "", "a b", "a;b", "$(x)"} {
		if validDockerVolume.MatchString(bad) {
			t.Errorf("validDockerVolume accepted hostile %q", bad)
		}
		if validImageRef.MatchString(bad) {
			t.Errorf("validImageRef accepted hostile %q", bad)
		}
	}
}

func TestTarTopLevelIs(t *testing.T) {
	// Archive whose top-level entry matches → ok.
	ok := filepath.Join(t.TempDir(), "ok.tar")
	writeTar(t, ok, []tar.Header{
		{Name: "media", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "media/file.txt", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644},
	}, []string{"", "x"})
	if err := tarTopLevelIs(ok, "media"); err != nil {
		t.Fatalf("matching top-level rejected: %v", err)
	}
	// Archive smuggling a SIBLING top-level entry → rejected.
	bad := filepath.Join(t.TempDir(), "bad.tar")
	writeTar(t, bad, []tar.Header{
		{Name: "media/file.txt", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644},
		{Name: "neighbor/evil.conf", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644},
	}, []string{"x", "y"})
	if err := tarTopLevelIs(bad, "media"); err == nil {
		t.Fatal("sibling top-level entry must be rejected")
	}
}
