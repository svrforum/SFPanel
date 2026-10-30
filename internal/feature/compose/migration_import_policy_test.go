package compose

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/svrforum/SFPanel/internal/auth"
	"github.com/svrforum/SFPanel/internal/docker"
)

func TestImportPolicyFrom(t *testing.T) {
	m := MigrationManifest{Overwrite: true}
	if p := importPolicyFrom(http.Header{}, m); !p.Overwrite || p.AgentSource {
		t.Fatalf("a sender from before policies: %+v (want the manifest's overwrite, a manager source)", p)
	}
	h := http.Header{}
	importPolicy{Overwrite: false, AgentSource: true}.setHeaders(h)
	if p := importPolicyFrom(h, m); p.Overwrite || !p.AgentSource || len(p.AckedRisks) != 0 {
		t.Fatalf("a policy that says no: %+v — the bundle's overwrite must not count", p)
	}
}

// The acknowledged findings cross as a header: each must arrive as the
// sentence the operator saw, whatever path it quotes.
func TestImportPolicyCarriesTheAcknowledgedFindings(t *testing.T) {
	risks := []string{
		`service "a, b" binds sensitive host path "/home/u/\"x\", y" at "/data"`,
		`service "웹" binds sensitive host path "/srv/데이터; z" at "/d"`,
		"service \"x\" sets privileged: true",
	}
	h := http.Header{}
	importPolicy{AgentSource: true, AckedRisks: risks}.setHeaders(h)
	got := importPolicyFrom(h, MigrationManifest{}).AckedRisks
	if len(got) != len(risks) {
		t.Fatalf("got %q, want %q", got, risks)
	}
	for i := range risks {
		if got[i] != risks[i] {
			t.Errorf("finding %d: got %q, want %q", i, got[i], risks[i])
		}
	}
	// A header that is not the encoding acknowledges nothing.
	h.Set(migrationAckRisksHeader, "true")
	if p := importPolicyFrom(h, MigrationManifest{}); len(p.AckedRisks) != 0 {
		t.Fatalf("a bare true acknowledged %q", p.AckedRisks)
	}
}

// Each small entry is held in memory until the import is done; their sum was
// not bounded, and a source could exhaust the target's memory.
func TestReceiveBundleBoundsTheSmallEntries(t *testing.T) {
	var buf bytes.Buffer
	bw := NewBundleWriter(&buf)
	if err := bw.WriteManifest(MigrationManifest{SchemaVersion: 1, StackID: "demo", ComposeFile: "docker-compose.yml"}); err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte("x"), 15<<20)
	for i := 0; i < 6; i++ { // 90 MiB in small entries
		if err := bw.WriteFile("compose/extra-"+string(rune('a'+i)), chunk, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = bw.Close()
	_, _, _, err := NewBundleReader(&buf).ReadAll(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "small entries") {
		t.Fatalf("got %v", err)
	}
}

// dockerSaveTar is the shape of `docker save`: a manifest.json naming the
// image's tags.
func dockerSaveTar(t *testing.T, tags ...string) string {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	m, _ := json.Marshal([]map[string]any{{"Config": "cfg.json", "RepoTags": tags, "Layers": []string{}}})
	_ = tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(m)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(m)
	_ = tw.Close()
	p := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// docker load sets every tag the archive names, whatever the manifest said
// the image was: an archive listed as nginx:fake could carry postgres:16 and
// repoint it under every other stack on the target.
func TestAnImageArchiveMayCarryOnlyItsOwnTag(t *testing.T) {
	if err := checkImageArchive(dockerSaveTar(t, "nginx:fake"), "nginx:fake"); err != nil {
		t.Fatalf("its own tag: %v", err)
	}
	for name, path := range map[string]string{
		"another tag":   dockerSaveTar(t, "postgres:16"),
		"an extra tag":  dockerSaveTar(t, "nginx:fake", "postgres:16"),
		"no tag at all": dockerSaveTar(t),
	} {
		if err := checkImageArchive(path, "nginx:fake"); err == nil || !strings.Contains(err.Error(), "nginx:fake") {
			t.Errorf("%s: %v", name, err)
		}
	}

	// docker save names an image as docker load does, which is not always as
	// the compose file wrote it.
	digest := "nginx@sha256:" + strings.Repeat("a", 64)
	for name, tc := range map[string]struct{ path, ref string }{
		"a ref with no tag is its :latest": {dockerSaveTar(t, "nginx:latest"), "nginx"},
		"a ref written in full":            {dockerSaveTar(t, "nginx:1.25"), "docker.io/library/nginx:1.25"},
		"a ref by digest names nothing":    {dockerSaveTar(t), digest},
	} {
		if err := checkImageArchive(tc.path, tc.ref); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, tc := range map[string]struct{ path, ref string }{
		"a ref with no tag is not every tag": {dockerSaveTar(t, "nginx:1.25"), "nginx"},
		"a ref by digest sets no tag":        {dockerSaveTar(t, "postgres:16"), digest},
	} {
		if err := checkImageArchive(tc.path, tc.ref); err == nil || !strings.Contains(err.Error(), tc.ref) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

type tarEntry struct {
	hdr  tar.Header
	body string
}

func tarFile(name, body string) tarEntry {
	return tarEntry{tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}, body}
}

func tarLink(name, target string) tarEntry {
	return tarEntry{tar.Header{Name: name, Linkname: target, Mode: 0o777, Typeflag: tar.TypeSymlink}, ""}
}

func imageArchiveOf(t *testing.T, entries ...tarEntry) string {
	t.Helper()
	var hdrs []tar.Header
	var bodies []string
	for _, e := range entries {
		hdrs = append(hdrs, e.hdr)
		bodies = append(bodies, e.body)
	}
	p := filepath.Join(t.TempDir(), "image.tar")
	writeTar(t, p, hdrs, bodies)
	return p
}

func manifestJSON(tags ...string) string {
	m, _ := json.Marshal([]map[string]any{{"Config": "blobs/sha256/" + strings.Repeat("c", 64), "RepoTags": tags, "Layers": []string{}}})
	return string(m)
}

// indexJSON is index.json as docker save writes it since Docker 25: one entry
// per name, carrying the name the containerd image store gives the image.
func indexJSON(names ...string) string {
	var manifests []map[string]any
	for _, n := range names {
		manifests = append(manifests, map[string]any{
			"mediaType": "application/vnd.oci.image.manifest.v1+json",
			"digest":    "sha256:" + strings.Repeat("d", 64),
			"size":      2,
			"annotations": map[string]string{
				"io.containerd.image.name":          n,
				"org.opencontainers.image.ref.name": n[strings.LastIndex(n, ":")+1:],
			},
		})
	}
	m, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": manifests})
	return string(m)
}

// docker load reads the archive as it lands on disk — a later entry at a path
// replaces an earlier one, and a path through a link lands where the link
// points — or, with the containerd image store, takes the names from
// index.json. Each of these could set a tag the first manifest.json never
// named.
func TestAnImageArchiveIsReadAsDockerLoadReadsIt(t *testing.T) {
	const layout = `{"imageLayoutVersion": "1.0.0"}`
	own, other := manifestJSON("nginx:fake"), manifestJSON("postgres:16")
	saved := imageArchiveOf(t,
		tarFile("blobs/sha256/"+strings.Repeat("c", 64), "{}"),
		tarFile("index.json", indexJSON("docker.io/library/nginx:fake")),
		tarFile("manifest.json", own),
		tarFile("oci-layout", layout),
		tarFile("repositories", `{"nginx":{"fake":"`+strings.Repeat("e", 64)+`"}}`),
	)
	if err := checkImageArchive(saved, "nginx:fake"); err != nil {
		t.Fatalf("docker save's own shape: %v", err)
	}
	for name, tc := range map[string]struct{ path, want string }{
		"a second manifest.json":           {imageArchiveOf(t, tarFile("manifest.json", own), tarFile("manifest.json", other)), "more than one manifest.json"},
		"a second one spelled another way": {imageArchiveOf(t, tarFile("manifest.json", own), tarFile("./manifest.json", other)), "more than one manifest.json"},
		"one written through a link":       {imageArchiveOf(t, tarFile("manifest.json", own), tarLink("d", "."), tarFile("d/manifest.json", other)), "through a link"},
		"a manifest.json that is a link":   {imageArchiveOf(t, tarFile("other.json", other), tarLink("manifest.json", "other.json")), "not a regular file"},
		"an index.json naming another tag": {imageArchiveOf(t, tarFile("manifest.json", own), tarFile("oci-layout", layout), tarFile("index.json", indexJSON("docker.io/library/postgres:16"))), "postgres:16"},
		"a second index.json":              {imageArchiveOf(t, tarFile("manifest.json", own), tarFile("index.json", indexJSON("nginx:fake")), tarFile("index.json", indexJSON("nginx:fake"))), "more than one index.json"},
	} {
		if err := checkImageArchive(tc.path, "nginx:fake"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// restoreData checks an image archive before docker load sees it.
func TestRestoreDataLoadsNoArchiveThatSetsAnotherTag(t *testing.T) {
	logPath := withFakeDocker(t)
	m := MigrationManifest{StackID: "demo", Images: []ImageSpec{{Ref: "nginx:fake", Archive: "images/i.tar"}}}
	staged := map[string]string{"images/i.tar": dockerSaveTar(t, "postgres:16")}
	_, _, _, err := restoreData(context.Background(), m, staged, t.TempDir(), t.TempDir(), importPolicy{})
	if err == nil || !strings.Contains(err.Error(), "postgres:16") {
		t.Fatalf("got %v", err)
	}
	if log, _ := os.ReadFile(logPath); strings.Contains(string(log), "load") {
		t.Fatalf("docker load ran:\n%s", log)
	}
}

// docker save of a name with no tag carries every tag of that name on the
// source, and docker load would set them all on the target. The stack runs
// :latest, so :latest is what is saved.
func TestAnImageWithNoTagIsSavedAsTheTagTheStackRuns(t *testing.T) {
	logPath := withFakeDocker(t)
	for _, ref := range []string{"nginx", "nginx:fake"} {
		if _, _, err := archiveImageToFile(context.Background(), ref, filepath.Join(t.TempDir(), "i.tar")); err != nil {
			t.Fatal(err)
		}
	}
	if log, _ := os.ReadFile(logPath); string(log) != "save nginx:latest\nsave nginx:fake\n" {
		t.Fatalf("docker was asked:\n%s", log)
	}
}

// importDockerScript lets an import run up to `compose up`, which fails: what
// the test below needs to know is only whether the import got that far. A
// volume exists, as far as `volume inspect` says.
const importDockerScript = `#!/bin/sh
case "$*" in
  *" up -d") echo "no daemon in a test" >&2; exit 1 ;;
  *" config") printf 'services:\n  web:\n    image: nginx:fake\n' ;;
esac
exit 0
`

// A stack directory or volume that exists on the target is replaced only when
// the sending manager says so: the bundle's overwrite is the source's word,
// and the source may be an agent. A sender from before policies keeps the
// bundle's word, as it had.
func TestAnImportTakesItsOverwriteFromThePolicy(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(importDockerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	auth.SetClusterProxySecret("test-secret")
	defer auth.SetClusterProxySecret("")

	// A volume's data entry, as packageFullBundle writes one.
	volTar := imageArchiveOf(t, tarFile("data.txt", "x"))
	volData, err := os.ReadFile(volTar)
	if err != nil {
		t.Fatal(err)
	}
	volSum := sha256.Sum256(volData)

	no, yes := &importPolicy{AgentSource: true}, &importPolicy{Overwrite: true}
	for _, tc := range []struct {
		name          string
		existing      string // "stack" or "volume"
		policy        *importPolicy
		bundleSaysYes bool
		want          string
	}{
		{"a stack, the policy says no", "stack", no, true, "a stack with this id already exists"},
		{"a stack, the policy says yes", "stack", yes, false, "up: "},
		{"a stack, a sender from before policies", "stack", nil, true, "up: "},
		{"a volume, the policy says no", "volume", no, true, "already exists; ack overwrite"},
		{"a volume, the policy says yes", "volume", yes, false, "up: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stacks := t.TempDir()
			m := MigrationManifest{SchemaVersion: 1, StackID: "demo", ComposeFile: "docker-compose.yml", Overwrite: tc.bundleSaysYes}
			if tc.existing == "stack" {
				if err := os.MkdirAll(filepath.Join(stacks, "demo"), 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				m.Volumes = []VolumeSpec{{Docker: "demo_data", Copy: true, Archive: "volumes/v.tar", Sha256: hex.EncodeToString(volSum[:])}}
			}
			var buf bytes.Buffer
			bw := NewBundleWriter(&buf)
			if err := bw.WriteManifest(m); err != nil {
				t.Fatal(err)
			}
			if err := bw.WriteFile("compose/docker-compose.yml", []byte("services:\n  web:\n    image: nginx:fake\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.existing == "volume" {
				if err := bw.WriteFile("volumes/v.tar", volData, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := bw.Close(); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(buf.Bytes())
			hdr := http.Header{}
			if tc.policy != nil {
				tc.policy.setHeaders(hdr)
			}
			headers := map[string]string{
				auth.InternalProxyHeaderV2: auth.SignProxyRequestV2(http.MethodPost, "/api/v1/docker/compose/migrate-import"),
				migrationShaHeader:         hex.EncodeToString(sum[:]),
			}
			for k := range hdr {
				headers[k] = hdr.Get(k)
			}
			h := &Handler{Compose: docker.NewComposeManager(stacks, nil), ComposePath: stacks}
			c, rec := newImportCtx(echo.New(), buf.String(), headers)
			if err := h.MigrateImport(c); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("%d %s, want %q", rec.Code, rec.Body, tc.want)
			}
		})
	}
}
