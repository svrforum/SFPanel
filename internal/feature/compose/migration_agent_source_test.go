package compose

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A risky stack from an agent needs the operator's acknowledgement on the
// target: its compose file is the agent's word, not the stack the operator
// created.
func TestAnAgentsComposeNeedsTheOperatorsAcknowledgement(t *testing.T) {
	risky := "services:\n  x:\n    image: alpine\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock\n"
	if err := checkImportCompose(risky, importPolicy{AgentSource: true}); err == nil || !strings.Contains(err.Error(), "docker.sock") {
		t.Fatalf("from an agent, unacknowledged: %v", err)
	}
	if err := checkImportCompose(risky, importPolicy{AgentSource: true, AckRisks: true}); err != nil {
		t.Fatalf("from an agent, acknowledged: %v", err)
	}
	if err := checkImportCompose(risky, importPolicy{AckRisks: true}); err != nil {
		t.Fatalf("from a manager, as before: %v", err)
	}
	forbidden := "services:\n  x:\n    image: alpine\n    volumes:\n      - /etc/sfpanel:/loot\n"
	if err := checkImportCompose(forbidden, importPolicy{AgentSource: true, AckRisks: true}); err == nil {
		t.Fatal("the forbidden tier was lifted")
	}
}

// An agent's migration restores data into the stack's own directory only: an
// absolute bind would write the agent's files as root anywhere the deny list
// does not name — /srv, /mnt, /data, most of /opt.
func TestAnAgentsAbsoluteBindIsRefused(t *testing.T) {
	root := t.TempDir()
	staged := map[string]string{"binds/b1.tar": filepath.Join(t.TempDir(), "b1.tar")}
	if err := os.WriteFile(staged["binds/b1.tar"], []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := MigrationManifest{StackID: "demo", Binds: []MountSpec{{Host: "/srv/data", Kind: "abs", Copy: true, Archive: "binds/b1.tar"}}}
	_, _, _, err := restoreData(context.Background(), m, staged, root, t.TempDir(), importPolicy{AgentSource: true})
	if err == nil || !strings.Contains(err.Error(), "/srv/data") {
		t.Fatalf("got %v", err)
	}
}

// fakeImageDockerScript answers the two docker calls the agent-source image rule
// makes: `image inspect --format {{.Id}} <ref>` reports $FAKE_IMG_ID when set
// (the tag is present as that image) or exits non-zero (the tag is absent), and
// every other call — `load -i <path>` above all — is logged and succeeds.
const fakeImageDockerScript = `#!/bin/sh
echo "$*" >> "$FAKE_DOCKER_LOG"
case "$1 $2" in
  "image inspect")
    if [ -n "$FAKE_IMG_ID" ]; then echo "$FAKE_IMG_ID"; exit 0; fi
    exit 1 ;;
esac
exit 0
`

// withFakeImageDocker puts that docker on PATH with the local image ID it
// reports for `image inspect` (empty = the tag is absent) and returns its log.
func withFakeImageDocker(t *testing.T, localID string) string {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "docker.log")
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(fakeImageDockerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv("FAKE_DOCKER_LOG", logPath)
	t.Setenv("FAKE_IMG_ID", localID)
	return logPath
}

// imageSaveTarWithConfig writes a `docker save` archive whose manifest.json
// names config (the image ID, as the Config path) and tags.
func imageSaveTarWithConfig(t *testing.T, config string, tags ...string) string {
	t.Helper()
	m, _ := json.Marshal([]map[string]any{{"Config": config, "RepoTags": tags, "Layers": []string{}}})
	return imageArchiveOf(t, tarFile("manifest.json", string(m)))
}

// An agent's image loads only when the target does not have that tag, or has it
// as the same image. A tag the target already holds as a DIFFERENT image is
// refused: loading over it would run the agent's image under every stack that
// already references the tag.
func TestAnAgentsImageLoadsOnlyUnderANewOrMatchingTag(t *testing.T) {
	id := "sha256:" + strings.Repeat("a", 64)
	for _, tc := range []struct {
		name    string
		localID string // what `docker image inspect` reports; "" = the tag is absent
		wantErr string // "" = restore succeeds
		loaded  bool   // whether docker load must have run
	}{
		{"the tag is new here", "", "", true},
		{"the same image", id, "", false},
		{"another image under the tag", "sha256:" + strings.Repeat("b", 64), "already", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logPath := withFakeImageDocker(t, tc.localID)
			archive := imageSaveTarWithConfig(t, "blobs/sha256/"+strings.Repeat("a", 64), "nginx:fake")
			m := MigrationManifest{StackID: "demo", Images: []ImageSpec{{Ref: "nginx:fake", Archive: "images/i.tar"}}}
			staged := map[string]string{"images/i.tar": archive}
			_, _, _, err := restoreData(context.Background(), m, staged, t.TempDir(), t.TempDir(), importPolicy{AgentSource: true})
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("want success, got %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), "nginx:fake")):
				t.Fatalf("want an error naming nginx:fake and %q, got %v", tc.wantErr, err)
			}
			loaded := strings.Contains(readFile(t, logPath), "load")
			if loaded != tc.loaded {
				t.Fatalf("docker load ran=%v, want %v\n%s", loaded, tc.loaded, readFile(t, logPath))
			}
		})
	}
}

// imageArchiveID reads the ID a `docker save` archive carries from its
// manifest.json Config path, in both the classic ("<id>.json") and OCI-layout
// ("blobs/sha256/<id>") forms, normalized to "sha256:<id>".
func TestImageArchiveIDReadsBothConfigForms(t *testing.T) {
	want := "sha256:" + strings.Repeat("a", 64)
	for name, config := range map[string]string{
		"classic":    strings.Repeat("a", 64) + ".json",
		"oci layout": "blobs/sha256/" + strings.Repeat("a", 64),
	} {
		got, err := imageArchiveID(imageSaveTarWithConfig(t, config, "nginx:fake"))
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v", name, got, err)
		}
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, _ := os.ReadFile(p)
	return string(b)
}
