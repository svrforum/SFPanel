package compose

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// tarMemberSafe reports whether a tar entry name can be extracted without
// escaping the destination dir: not absolute, and no ".." path component.
func tarMemberSafe(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// tarTopLevelIs verifies every archive member's first path component equals top.
// Bind archives are extracted one level above the bind dir (tar -C parent), so
// pinning the top component to the bind's own basename stops a hostile archive
// (different/extra top-level entries) from creating or merging into SIBLING dirs
// under the shared parent — the gate clears only the intended leaf.
func tarTopLevelIs(archivePath, top string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("scan archive: %w", err)
		}
		name := strings.TrimPrefix(filepath.ToSlash(hdr.Name), "./")
		name = strings.TrimPrefix(name, "/")
		if name == "" || name == "." {
			continue
		}
		first := name
		if i := strings.IndexByte(name, '/'); i >= 0 {
			first = name[:i]
		}
		if first != top {
			return fmt.Errorf("archive member %q is outside expected top-level %q", hdr.Name, top)
		}
	}
}

// validateTarSafe scans an archive and rejects any entry that could escape the
// extraction dir — absolute or "..": member names, or symlink/hardlink targets
// that are absolute or resolve outside the root. The archive's CONTENTS come
// from another (cluster-internal but potentially compromised) node and are only
// integrity-checked (sha), not trusted, so this runs BEFORE the static file is
// handed to `tar -x` (host or helper container).
func validateTarSafe(archivePath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("scan archive: %w", err)
		}
		if !tarMemberSafe(hdr.Name) {
			return fmt.Errorf("unsafe archive member %q", hdr.Name)
		}
		// Reject special-file entries (char/block device, FIFO/socket): a migrated
		// bind/volume payload is data, never a device node. Extraction runs as root
		// (host `tar -x` for binds, the volume helper for volumes), so a device or
		// FIFO entry in a hostile bundle would be materialized verbatim on the
		// target. Only regular files, dirs, and (already-validated) sym/hardlinks
		// are data-bearing. FileInfo().Mode() classifies reg/dir without touching
		// the deprecated TypeRegA.
		mode := hdr.FileInfo().Mode()
		switch {
		case mode.IsRegular(), mode.IsDir(), hdr.Typeflag == tar.TypeSymlink, hdr.Typeflag == tar.TypeLink:
			// data-bearing or link entries — allowed (link targets validated below)
		default:
			return fmt.Errorf("unsupported archive member type %d for %q", hdr.Typeflag, hdr.Name)
		}
		// Reject setuid/setgid regular files: `tar -x` as root preserves the bits,
		// so a hostile bundle could plant a setuid-root executable on the target.
		// (setgid/sticky on DIRS is legitimate group-inheritance and left alone.)
		if mode.IsRegular() && mode&(os.ModeSetuid|os.ModeSetgid) != 0 {
			return fmt.Errorf("archive member %q carries setuid/setgid bits", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeSymlink:
			// Resolve the link relative to the entry's directory; it must stay in-root.
			if path.IsAbs(hdr.Linkname) || !tarMemberSafe(filepath.Join(filepath.Dir(hdr.Name), hdr.Linkname)) {
				return fmt.Errorf("unsafe symlink %q -> %q", hdr.Name, hdr.Linkname)
			}
		case tar.TypeLink:
			// Hardlink targets are archive-root-relative.
			if path.IsAbs(hdr.Linkname) || !tarMemberSafe(hdr.Linkname) {
				return fmt.Errorf("unsafe hardlink %q -> %q", hdr.Name, hdr.Linkname)
			}
		}
	}
}

// migrationHelperImage is the throwaway image used to read/write named-volume
// data (mounted into a container, then tar'd). alpine is tiny and near-ubiquitous
// on docker hosts; if absent docker pulls it once.
const migrationHelperImage = "alpine"

// migrationHelperLabel tags every throwaway helper container so an orphaned one
// (left behind when the parent migration process is SIGKILLed mid-tar — `--rm`
// only fires on a clean exit, and the detached container can outlive the killed
// CLI) can be swept at boot. See SweepMigrationHelperContainers.
const migrationHelperLabel = "sfpanel.migration.helper=1"

// helperRunFlags are the common `docker run` flags for the throwaway helper:
//   - `--network none`: it only tars a mounted volume, never needs networking,
//     and this avoids a hard failure on hosts whose default bridge (docker0) is
//     absent or custom (where a bare `docker run` can't attach to it).
//   - `--label`: tags it for the orphan sweep above.
var helperRunFlags = []string{"--network", "none", "--label", migrationHelperLabel}

// errSkipSpecial marks a bind whose host path is a socket/device/irregular file
// or is missing — it can't (and shouldn't) be archived. The caller flips the
// bind's Copy flag off and records a pre-flight warning instead.
var errSkipSpecial = errors.New("special or missing bind path, not archived")

// runStreamToFile runs name+args and streams the command's stdout to dstPath
// while hashing it, returning the bytes written and the lowercase-hex sha256.
// stderr is captured for the error message. os/exec directly (not Commander)
// because the output is multi-GB streaming data; ctx kills the subprocess on a
// cancel/disconnect so a dead migration doesn't leak a docker save/tar process.
func runStreamToFile(ctx context.Context, dstPath, name string, args ...string) (int64, string, error) {
	f, err := os.Create(dstPath)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = f.Close() }()

	cmd := exec.CommandContext(ctx, name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, "", err
	}
	if err := cmd.Start(); err != nil {
		_ = stdout.Close() // Wait (which closes the pipe) never runs on a Start error
		return 0, "", err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(f, h), stdout)
	waitErr := cmd.Wait()
	if copyErr != nil {
		return 0, "", fmt.Errorf("stream %s: %w", name, copyErr)
	}
	if waitErr != nil {
		return 0, "", fmt.Errorf("%s failed: %w: %s", name, waitErr, strings.TrimSpace(stderr.String()))
	}
	if err := f.Sync(); err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// archiveVolumeToFile tars a named volume's contents into dstPath via the helper
// image (read-only mount). Works for any volume driver docker can mount.
func archiveVolumeToFile(ctx context.Context, dockerVol, dstPath string) (int64, string, error) {
	args := append([]string{"run", "--rm"}, helperRunFlags...)
	args = append(args, "-v", dockerVol+":/from:ro", "-w", "/from", migrationHelperImage, "tar", "-cf", "-", ".")
	return runStreamToFile(ctx, dstPath, "docker", args...)
}

// archiveBindToFile tars a host bind path (dir or regular file) into dstPath. The
// archive carries the entry's basename so restore can recreate it under the
// target's parent dir. Special files (socket/device/pipe) and missing paths are
// reported via errSkipSpecial.
func archiveBindToFile(ctx context.Context, hostPath, dstPath string) (int64, string, error) {
	fi, err := os.Lstat(hostPath)
	if err != nil {
		return 0, "", errSkipSpecial
	}
	if fi.Mode()&(os.ModeSocket|os.ModeDevice|os.ModeCharDevice|os.ModeNamedPipe|os.ModeIrregular) != 0 {
		return 0, "", errSkipSpecial
	}
	return runStreamToFile(ctx, dstPath,
		"tar", "-C", filepath.Dir(hostPath), "-cf", "-", filepath.Base(hostPath))
}

// archiveImageToFile streams `docker save <ref>` into dstPath. Always save/load
// (no registry dependency) per the operator's choice.
func archiveImageToFile(ctx context.Context, ref, dstPath string) (int64, string, error) {
	return runStreamToFile(ctx, dstPath, "docker", "save", ref)
}

// parseLeadingBytes pulls the integer at the start of `du -sb` output ("1234\t/v").
func parseLeadingBytes(out []byte) int64 {
	s := strings.TrimSpace(string(out))
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// volumeSizeBytes best-effort returns a named volume's apparent size (du -sb via
// the helper image). 0 on any error — pre-flight sizing degrades, never fails.
func volumeSizeBytes(ctx context.Context, dockerVol string) int64 {
	args := append([]string{"run", "--rm"}, helperRunFlags...)
	args = append(args, "-v", dockerVol+":/v:ro", migrationHelperImage, "du", "-sb", "/v")
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return 0
	}
	return parseLeadingBytes(out)
}

// dirSizeBytes best-effort returns a host path's apparent size (du -sb). 0 on error.
func dirSizeBytes(ctx context.Context, path string) int64 {
	out, err := exec.CommandContext(ctx, "du", "-sb", path).Output()
	if err != nil {
		return 0
	}
	return parseLeadingBytes(out)
}

// imageSizeBytes best-effort returns an image's on-disk size. 0 on error.
func imageSizeBytes(ctx context.Context, ref string) int64 {
	out, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Size}}", ref).Output()
	if err != nil {
		return 0
	}
	return parseLeadingBytes(out)
}

// estimateTransferBytes best-effort sums the bytes that will actually be copied
// (volume + bind data + image sizes) so the pre-flight disk check uses a real
// figure. Best-effort: an un-sizable entry contributes 0 rather than failing.
func estimateTransferBytes(ctx context.Context, facts stackConfigFacts) int64 {
	var total int64
	for _, v := range facts.Volumes {
		if v.Copy {
			total += volumeSizeBytes(ctx, v.Docker)
		}
	}
	for _, b := range facts.Binds {
		if b.Copy {
			total += dirSizeBytes(ctx, b.Host)
		}
	}
	for _, im := range facts.Images {
		total += imageSizeBytes(ctx, im.Ref)
	}
	return total
}

// --- target-side restore primitives ---

// loadImageFromFile loads a saved-image tar into the local docker.
func loadImageFromFile(ctx context.Context, archivePath string) error {
	out, err := exec.CommandContext(ctx, "docker", "load", "-i", archivePath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker load: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// volumeExists reports whether a named docker volume already exists.
func volumeExists(ctx context.Context, dockerVol string) bool {
	return exec.CommandContext(ctx, "docker", "volume", "inspect", dockerVol).Run() == nil
}

// createVolumeIfAbsent creates the named volume only if it does not already
// exist, returning whether it was freshly created (so failure cleanup removes
// only volumes WE made, never a pre-existing tenant's).
func createVolumeIfAbsent(ctx context.Context, dockerVol string) (bool, error) {
	if volumeExists(ctx, dockerVol) {
		return false, nil
	}
	out, err := exec.CommandContext(ctx, "docker", "volume", "create", dockerVol).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("volume create %s: %w: %s", dockerVol, err, strings.TrimSpace(string(out)))
	}
	return true, nil
}

// removeVolume deletes a named volume (failure-cleanup of a freshly created one).
func removeVolume(ctx context.Context, dockerVol string) {
	_ = exec.CommandContext(ctx, "docker", "volume", "rm", "-f", dockerVol).Run()
}

// clearVolume empties a named volume's contents via the helper image, so an
// acked overwrite restores an EXACT copy of the source volume rather than
// overlaying the migrated files onto a pre-existing tenant's data.
func clearVolume(ctx context.Context, dockerVol string) error {
	args := append([]string{"run", "--rm"}, helperRunFlags...)
	args = append(args, "-v", dockerVol+":/to", "-w", "/to", migrationHelperImage,
		"sh", "-c", "rm -rf /to/* /to/.[!.]* /to/..?* 2>/dev/null; true")
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("clear volume %s: %w: %s", dockerVol, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// extractArchiveToVolume streams a tar archive into the named volume via the
// helper image (stdin = the tar). It does NOT validate the archive: callers
// restoring an UNTRUSTED cross-node archive must validateTarSafe first (use
// restoreVolumeFromFile); callers restoring a TRUSTED archive we made ourselves
// (a prebak of the target's OWN volume) skip that — re-running the hostile-input
// validator on our own backup would, e.g., reject a legitimate setuid file the
// volume already held and leave the prior tenant unrecoverable.
func extractArchiveToVolume(ctx context.Context, dockerVol, archivePath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	args := append([]string{"run", "-i", "--rm"}, helperRunFlags...)
	args = append(args, "-v", dockerVol+":/to", "-w", "/to", migrationHelperImage, "tar", "-xf", "-")
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = f
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("extract volume %s: %w: %s", dockerVol, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// restoreVolumeFromFile extracts an UNTRUSTED (cross-node) volume archive into
// the named volume, validating it for path traversal / unsafe entries first so
// the (busybox) extractor can't escape /to or materialize device/setuid entries.
func restoreVolumeFromFile(ctx context.Context, dockerVol, archivePath string) error {
	if err := validateTarSafe(archivePath); err != nil {
		return err
	}
	return extractArchiveToVolume(ctx, dockerVol, archivePath)
}

// extractTarToDir extracts an archive into targetParent (created if needed),
// recreating the archive's top-level entry there. The archive is validated for
// path traversal first so extraction can't escape targetParent.
func extractTarToDir(ctx context.Context, targetParent, archivePath string) error {
	if err := validateTarSafe(archivePath); err != nil {
		return err
	}
	if err := os.MkdirAll(targetParent, 0o755); err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, "tar", "-C", targetParent, "-xf", archivePath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("extract %s: %w: %s", filepath.Base(archivePath), err, strings.TrimSpace(string(out)))
	}
	return nil
}
