package compose

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/distribution/reference"
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
// (no registry dependency) per the operator's choice. A ref with no tag is
// saved as the :latest the stack runs: `docker save nginx` would carry every
// nginx tag this node has, the target would refuse the archive for them
// (checkImageArchive), and a target before that check would set them all.
func archiveImageToFile(ctx context.Context, ref, dstPath string) (int64, string, error) {
	return runStreamToFile(ctx, dstPath, "docker", "save", normalizeImageRef(ref))
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

// estimateTransferBytes best-effort sizes what will be copied, split by which
// filesystem it lands on so the pre-flight disk check can compare each against
// the right free space: volume + image data lands on the docker storage FS
// (e.g. /var/lib/docker); bind data lands on the stacks FS (in-stack binds) or a
// bind's own absolute path (counted toward stacks as an approximation — abs
// binds are usually small config dirs). Best-effort: an un-sizable entry
// contributes 0 rather than failing.
func estimateTransferBytes(ctx context.Context, facts stackConfigFacts) (dockerBytes, stacksBytes int64) {
	for _, v := range facts.Volumes {
		if v.Copy {
			dockerBytes += volumeSizeBytes(ctx, v.Docker)
		}
	}
	for _, im := range facts.Images {
		dockerBytes += imageSizeBytes(ctx, im.Ref)
	}
	for _, b := range facts.Binds {
		if b.Copy {
			stacksBytes += dirSizeBytes(ctx, b.Host)
		}
	}
	return dockerBytes, stacksBytes
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

// imageArchiveTags reads the tags a `docker save` archive has docker load set:
// manifest.json's RepoTags, which the classic image store sets, and the image
// names in index.json, which the containerd image store sets instead when the
// archive is an OCI layout (docker save writes one since Docker 25). The
// archive is the source's, so it is read whole, the way load reads it. Load
// unpacks it to disk first, where a later entry at a path replaces an earlier
// one and a path through a link lands where the link points; either could put
// a manifest.json behind the one read here. So a second manifest.json or
// index.json, one that is not a regular file, and an entry written through a
// link are refused.
func imageArchiveTags(archivePath string) ([]string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var tags []string
	seen := map[string]bool{}  // manifest.json and index.json, once each
	links := map[string]bool{} // where the archive has put a link
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read the image archive: %w", err)
		}
		// Where the entry lands: ./manifest.json, /manifest.json and
		// a/../manifest.json are all manifest.json.
		name := strings.TrimPrefix(path.Clean("/"+hdr.Name), "/")
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			if links[dir] {
				return nil, fmt.Errorf("the image archive writes %s through a link", hdr.Name)
			}
		}
		if hdr.Typeflag == tar.TypeSymlink || hdr.Typeflag == tar.TypeLink {
			links[name] = true
		} else {
			delete(links, name) // the entry replaces the link
		}
		if name != "manifest.json" && name != "index.json" {
			continue
		}
		if seen[name] {
			return nil, fmt.Errorf("the image archive has more than one %s", name)
		}
		seen[name] = true
		if hdr.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("the image archive's %s is not a regular file", name)
		}
		names, err := imageArchiveNames(name, io.LimitReader(tr, 1<<20))
		if err != nil {
			return nil, fmt.Errorf("read the image archive's %s: %w", name, err)
		}
		tags = append(tags, names...)
	}
	if !seen["manifest.json"] {
		return nil, errors.New("the image archive has no manifest.json")
	}
	return tags, nil
}

// imageArchiveNames reads the names in an image archive's manifest.json (its
// RepoTags) or index.json (for each image, the name containerd gives it:
// io.containerd.image.name, or else org.opencontainers.image.ref.name).
func imageArchiveNames(name string, r io.Reader) ([]string, error) {
	var names []string
	if name == "manifest.json" {
		var entries []struct{ RepoTags []string }
		if err := json.NewDecoder(r).Decode(&entries); err != nil {
			return nil, err
		}
		for _, e := range entries {
			names = append(names, e.RepoTags...)
		}
		return names, nil
	}
	var index struct {
		Manifests []struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"manifests"`
	}
	if err := json.NewDecoder(r).Decode(&index); err != nil {
		return nil, err
	}
	for _, m := range index.Manifests {
		if n := m.Annotations["io.containerd.image.name"]; n != "" {
			names = append(names, n)
		} else if n := m.Annotations["org.opencontainers.image.ref.name"]; n != "" {
			names = append(names, n)
		}
	}
	return names, nil
}

// checkImageArchive refuses an image archive that would set any tag but ref:
// docker load sets every tag the archive names, whatever the manifest says the
// image is. A ref by digest is saved with no tag and loads with none.
func checkImageArchive(archivePath, ref string) error {
	tags, err := imageArchiveTags(archivePath)
	if err != nil {
		return err
	}
	if strings.Contains(ref, "@") {
		if len(tags) > 0 {
			return fmt.Errorf("the image archive for %s sets the tags %v: an image by digest loads with none", ref, tags)
		}
		return nil
	}
	want := normalizeImageRef(ref)
	refused := len(tags) == 0
	for _, tag := range tags {
		refused = refused || normalizeImageRef(tag) != want
	}
	if refused {
		return fmt.Errorf("the image archive for %s sets the tags %v: it may set %s only", ref, tags, want)
	}
	return nil
}

// normalizeImageRef writes ref as docker save and docker load name an image:
// with the tag docker takes for a name without one (nginx is nginx:latest),
// and in the short form (docker.io/library/nginx:1.25 is nginx:1.25). A ref
// docker cannot parse is left as it is; load refuses such a name.
func normalizeImageRef(ref string) string {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return ref
	}
	return reference.FamiliarString(reference.TagNameOnly(named))
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
