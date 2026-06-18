package compose

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These exercise the REAL data archive→restore primitives against a live docker
// daemon (distinct volume/image names, so it proves actual data movement — not a
// shared-volume artifact). Gated behind SFPANEL_MIGRATION_IT=1 + docker presence
// so the normal `make test` (no docker) skips them.
func requireDockerIT(t *testing.T) {
	t.Helper()
	if os.Getenv("SFPANEL_MIGRATION_IT") != "1" {
		t.Skip("set SFPANEL_MIGRATION_IT=1 to run docker integration tests")
	}
	if exec.Command("docker", "info").Run() != nil {
		t.Skip("docker not available")
	}
}

func TestVolumeArchiveRestoreRoundTrip(t *testing.T) {
	requireDockerIT(t)
	ctx := context.Background()
	const src, dst = "sfmig_it_src", "sfmig_it_dst"
	defer removeVolume(ctx, src)
	defer removeVolume(ctx, dst)

	if _, err := createVolumeIfAbsent(ctx, src); err != nil {
		t.Fatalf("create src: %v", err)
	}
	// Write a known payload into the source volume.
	if out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "-v", src+":/d",
		migrationHelperImage, "sh", "-c", "echo migrated-payload > /d/marker.txt").CombinedOutput(); err != nil {
		t.Fatalf("seed src: %v: %s", err, out)
	}

	arc := filepath.Join(t.TempDir(), "vol.tar")
	n, sha, err := archiveVolumeToFile(ctx, src, arc)
	if err != nil || n <= 0 || len(sha) != 64 {
		t.Fatalf("archive volume: n=%d sha=%q err=%v", n, sha, err)
	}

	if _, err := createVolumeIfAbsent(ctx, dst); err != nil {
		t.Fatalf("create dst: %v", err)
	}
	if err := restoreVolumeFromFile(ctx, dst, arc); err != nil {
		t.Fatalf("restore volume: %v", err)
	}

	out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "-v", dst+":/d",
		migrationHelperImage, "cat", "/d/marker.txt").Output()
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if strings.TrimSpace(string(out)) != "migrated-payload" {
		t.Fatalf("dst volume content = %q, want migrated-payload", strings.TrimSpace(string(out)))
	}
}

func TestBindArchiveRestoreRoundTrip(t *testing.T) {
	requireDockerIT(t)
	ctx := context.Background()
	srcDir := t.TempDir()
	sub := filepath.Join(srcDir, "config")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "app.conf"), []byte("key=value"), 0o644); err != nil {
		t.Fatal(err)
	}
	arc := filepath.Join(t.TempDir(), "bind.tar")
	if _, _, err := archiveBindToFile(ctx, sub, arc); err != nil {
		t.Fatalf("archive bind: %v", err)
	}
	targetParent := t.TempDir()
	if err := extractTarToDir(ctx, targetParent, arc); err != nil {
		t.Fatalf("extract bind: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(targetParent, "config", "app.conf"))
	if err != nil || string(got) != "key=value" {
		t.Fatalf("restored bind = %q err=%v, want key=value", got, err)
	}
}

func TestImageArchiveLoadRoundTrip(t *testing.T) {
	requireDockerIT(t)
	ctx := context.Background()
	// alpine is the helper image, already present. Save then load (idempotent).
	arc := filepath.Join(t.TempDir(), "img.tar")
	n, sha, err := archiveImageToFile(ctx, migrationHelperImage, arc)
	if err != nil || n <= 0 || len(sha) != 64 {
		t.Fatalf("archive image: n=%d sha=%q err=%v", n, sha, err)
	}
	if err := loadImageFromFile(ctx, arc); err != nil {
		t.Fatalf("load image: %v", err)
	}
}

// TestRestoreDataVolumeOverwriteGate verifies the blocker-1 fix: restoreData
// refuses a pre-existing volume without overwrite ack, and on ack produces an
// EXACT replica (clears stale data) rather than an overlay/merge.
func TestRestoreDataVolumeOverwriteGate(t *testing.T) {
	requireDockerIT(t)
	ctx := context.Background()
	const tgt, src = "sfmig_it_ow_tgt", "sfmig_it_ow_src"
	defer removeVolume(ctx, tgt)
	defer removeVolume(ctx, src)

	// Source volume → archive (marker=NEW + only-new.txt).
	if _, err := createVolumeIfAbsent(ctx, src); err != nil {
		t.Fatal(err)
	}
	exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "-v", src+":/d", migrationHelperImage,
		"sh", "-c", "echo NEW > /d/marker.txt; echo NEW > /d/only-new.txt").Run()
	arc := filepath.Join(t.TempDir(), "v.tar")
	_, sha, err := archiveVolumeToFile(ctx, src, arc)
	if err != nil {
		t.Fatal(err)
	}
	m := MigrationManifest{StackID: "x", Volumes: []VolumeSpec{{Docker: tgt, Copy: true, Archive: "volumes/v.tar", Sha256: sha}}}
	staged := map[string]string{"volumes/v.tar": arc}

	// Pre-existing target volume with STALE data (marker=OLD + only-old.txt).
	if _, err := createVolumeIfAbsent(ctx, tgt); err != nil {
		t.Fatal(err)
	}
	exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "-v", tgt+":/d", migrationHelperImage,
		"sh", "-c", "echo OLD > /d/marker.txt; echo OLD > /d/only-old.txt").Run()

	// Without overwrite ack → refuse (no silent overlay).
	if _, _, _, err := restoreData(ctx, m, staged, t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("pre-existing volume without overwrite ack must be refused")
	}

	// With overwrite ack → EXACT replica (stale only-old.txt gone, marker=NEW).
	m.Overwrite = true
	if _, _, _, err := restoreData(ctx, m, staged, t.TempDir(), t.TempDir()); err != nil {
		t.Fatalf("overwrite restore: %v", err)
	}
	out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "-v", tgt+":/d", migrationHelperImage,
		"sh", "-c", "cat /d/marker.txt; echo ---; ls /d").Output()
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "NEW") || strings.Contains(s, "only-old.txt") || !strings.Contains(s, "only-new.txt") {
		t.Fatalf("overwrite was not an exact replica: %q", s)
	}
}

// TestRestoreDataPrebakRestoresPriorVolume verifies the A2 fix: on an overwrite,
// restoreData archives the pre-existing volume's data to a prebak BEFORE wiping
// it, and that prebak can restore the prior tenant's data if the import later
// fails — so a failed overwrite import never destroys pre-existing volume data.
func TestRestoreDataPrebakRestoresPriorVolume(t *testing.T) {
	requireDockerIT(t)
	ctx := context.Background()
	const tgt, src = "sfmig_it_pb_tgt", "sfmig_it_pb_src"
	defer removeVolume(ctx, tgt)
	defer removeVolume(ctx, src)

	// Incoming (NEW) data archive.
	if _, err := createVolumeIfAbsent(ctx, src); err != nil {
		t.Fatal(err)
	}
	exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "-v", src+":/d", migrationHelperImage,
		"sh", "-c", "echo NEW > /d/marker.txt").Run()
	arc := filepath.Join(t.TempDir(), "v.tar")
	_, sha, err := archiveVolumeToFile(ctx, src, arc)
	if err != nil {
		t.Fatal(err)
	}
	m := MigrationManifest{StackID: "x", Overwrite: true, Volumes: []VolumeSpec{{Docker: tgt, Copy: true, Archive: "volumes/v.tar", Sha256: sha}}}
	staged := map[string]string{"volumes/v.tar": arc}

	// Pre-existing target volume with PRIOR (other-tenant) data.
	if _, err := createVolumeIfAbsent(ctx, tgt); err != nil {
		t.Fatal(err)
	}
	exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "-v", tgt+":/d", migrationHelperImage,
		"sh", "-c", "echo PRIOR > /d/marker.txt; echo PRIOR > /d/only-prior.txt").Run()

	prebakDir := t.TempDir()
	_, prebaks, _, err := restoreData(ctx, m, staged, t.TempDir(), prebakDir)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(prebaks) != 1 || prebaks[0].Docker != tgt {
		t.Fatalf("want 1 prebak for %s, got %+v", tgt, prebaks)
	}
	if _, err := os.Stat(prebaks[0].Archive); err != nil {
		t.Fatalf("prebak archive missing: %v", err)
	}

	// Simulate failedImportData's restore: clear + extract the prebak (trusted path).
	if err := clearVolume(ctx, tgt); err != nil {
		t.Fatal(err)
	}
	if err := extractArchiveToVolume(ctx, tgt, prebaks[0].Archive); err != nil {
		t.Fatalf("prebak restore: %v", err)
	}
	out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", "none", "-v", tgt+":/d", migrationHelperImage,
		"sh", "-c", "cat /d/marker.txt; ls /d").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); !strings.Contains(got, "PRIOR") || !strings.Contains(got, "only-prior.txt") {
		t.Fatalf("prebak did not restore prior data, got: %q", got)
	}
}
