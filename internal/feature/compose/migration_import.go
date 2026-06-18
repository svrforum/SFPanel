package compose

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// validDockerVolume / validImageRef gate manifest-supplied identifiers before
// they reach `docker` argv on the target. The leading-alphanumeric rule blocks
// argv flag-smuggling (a value like "-foo" parsed as a flag); the character
// class blocks anything outside legal docker volume names / image references.
var validDockerVolume = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var validImageRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/@-]{0,255}$`)

// absBindDenyPrefixes are target paths that a migrated absolute bind must NEVER
// overwrite, even if the (cluster-internal but potentially compromised) source
// claims them. Restoring an abs bind writes attacker-influenced files as root, so
// critical system trees are refused — the bind is skipped with a warning.
var absBindDenyPrefixes = []string{
	"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/boot", "/root",
	// /var broadly: covers /var/lib (panel SQLite DB + Raft/BoltDB cluster
	// state + docker), /var/log, /var/spool/cron (cron RCE), /var/run.
	"/var",
	// /home: .ssh/authorized_keys is an RCE vector.
	"/home",
	"/sys", "/proc", "/dev", "/run", "/opt/stacks",
}

// pathHasContent reports whether p exists and is a non-empty dir or a file.
func pathHasContent(p string) bool {
	fi, err := os.Stat(p)
	if err != nil {
		return false
	}
	if !fi.IsDir() {
		return true
	}
	entries, err := os.ReadDir(p)
	return err == nil && len(entries) > 0
}

// absBindRestorable reports whether restored data may be written at the absolute
// host path p on the target. "/" and any deny-listed system tree are refused.
func absBindRestorable(p string) bool {
	clean := filepath.Clean(p)
	// Must be absolute: a relative Host (attacker-controlled in the manifest)
	// would otherwise resolve against the process CWD ("/" for the service) and
	// the leading-slash deny-list would never match — e.g. "etc/cron.d" → /etc.
	if !filepath.IsAbs(clean) {
		return false
	}
	if clean == "/" {
		return false
	}
	for _, d := range absBindDenyPrefixes {
		if clean == d || strings.HasPrefix(clean+"/", d+"/") {
			return false
		}
	}
	return true
}

// volPrebak records a pre-existing target volume that was archived aside before
// an overwrite clearVolume wiped it, so a failed import can restore the prior
// tenant's data. Archive is a tar of the volume's prior contents under prebakDir.
type volPrebak struct {
	Docker  string
	Archive string
}

// restoreData loads images, recreates named volumes from their archives, and
// restores copied bind dirs — AFTER restoreDefinition, BEFORE compose up. Every
// data archive was already sha256-verified by the bundle reader, so a clean
// return means the target holds intact data (which is what makes the source
// `delete` disposition safe). prebakDir is where pre-existing volumes are backed
// up before an overwrite wipes them. Returns the docker volumes it FRESHLY
// created and the pre-existing volumes it backed up (both for failure cleanup),
// plus any skipped-bind warnings.
func restoreData(ctx context.Context, m MigrationManifest, staged map[string]string, composeRoot, prebakDir string) (created []string, prebaks []volPrebak, warnings []string, err error) {
	stackDir := filepath.Join(composeRoot, m.StackID)

	// Gate every manifest identifier that reaches a docker argv (the manifest
	// comes from another node and is not trusted) BEFORE touching docker.
	for _, v := range m.Volumes {
		if v.Copy && v.Archive != "" && !validDockerVolume.MatchString(v.Docker) {
			return created, prebaks, warnings, fmt.Errorf("invalid volume name in manifest: %q", v.Docker)
		}
	}
	for _, im := range m.Images {
		if im.Archive != "" && !validImageRef.MatchString(im.Ref) {
			return created, prebaks, warnings, fmt.Errorf("invalid image ref in manifest: %q", im.Ref)
		}
	}

	for _, im := range m.Images {
		if im.Archive == "" {
			continue
		}
		path, ok := staged[im.Archive]
		if !ok {
			return created, prebaks, warnings, fmt.Errorf("image archive %q missing from bundle", im.Archive)
		}
		if lerr := loadImageFromFile(ctx, path); lerr != nil {
			return created, prebaks, warnings, lerr
		}
	}

	for i := range m.Volumes {
		v := m.Volumes[i]
		if !v.Copy || v.Archive == "" {
			continue
		}
		path, ok := staged[v.Archive]
		if !ok {
			return created, prebaks, warnings, fmt.Errorf("volume archive %q missing from bundle", v.Archive)
		}
		fresh, cerr := createVolumeIfAbsent(ctx, v.Docker)
		if cerr != nil {
			return created, prebaks, warnings, cerr
		}
		if fresh {
			created = append(created, v.Docker)
		} else {
			// A pre-existing volume (another stack/tenant, an external shared
			// volume, or an orphan) must NOT be silently overlaid. Require an
			// explicit overwrite ack, then wipe it so the restore is an exact copy.
			if !m.Overwrite {
				return created, prebaks, warnings, fmt.Errorf("target volume %q already exists; ack overwrite to replace it", v.Docker)
			}
			// Back up the prior contents BEFORE the irreversible clearVolume, so a
			// later import failure can restore the prior tenant exactly — mirroring
			// the .migbak definition backup. (Without this the rm -rf is unrecoverable.)
			prebakPath := filepath.Join(prebakDir, "prebak-"+shortHash(v.Docker)+".tar")
			if _, _, aerr := archiveVolumeToFile(ctx, v.Docker, prebakPath); aerr != nil {
				return created, prebaks, warnings, fmt.Errorf("back up pre-existing volume %q before overwrite: %w", v.Docker, aerr)
			}
			prebaks = append(prebaks, volPrebak{Docker: v.Docker, Archive: prebakPath})
			if werr := clearVolume(ctx, v.Docker); werr != nil {
				return created, prebaks, warnings, werr
			}
		}
		if rerr := restoreVolumeFromFile(ctx, v.Docker, path); rerr != nil {
			return created, prebaks, warnings, rerr
		}
	}

	for i := range m.Binds {
		b := m.Binds[i]
		if !b.Copy || b.Archive == "" {
			continue
		}
		path, ok := staged[b.Archive]
		if !ok {
			return created, prebaks, warnings, fmt.Errorf("bind archive %q missing from bundle", b.Archive)
		}
		var targetBind string
		if b.Kind == "in-stack" {
			rel := b.Rel
			if rel == "" {
				rel = filepath.Base(b.Host)
			}
			targetBind = filepath.Join(stackDir, rel)
			if !withinRoot(stackDir, targetBind) {
				return created, prebaks, warnings, fmt.Errorf("in-stack bind escapes stack dir: %q", rel)
			}
			if filepath.Clean(targetBind) == filepath.Clean(stackDir) {
				// rel "." / "" / "../<id>" would resolve to the stack dir itself;
				// wiping it would destroy the just-written compose definition.
				return created, prebaks, warnings, fmt.Errorf("in-stack bind resolves to the stack dir itself: %q", rel)
			}
			// In-stack data lives under the fresh/backed-up stack dir (a confined
			// descendant); clear any leftover so the restore is exact.
			_ = os.RemoveAll(targetBind)
		} else { // abs (system binds were never marked Copy)
			if !absBindRestorable(b.Host) {
				warnings = append(warnings, "skipped restoring bind to protected path "+b.Host)
				continue
			}
			targetBind = b.Host
			// NEVER RemoveAll an absolute host path: a hostile manifest could point
			// it at /opt, /srv, /data, … and the overwrite ack would then authorize
			// wiping an entire unrelated tree as root. Require the target path to be
			// empty/absent; the operator clears it themselves to intentionally replace.
			if pathHasContent(targetBind) {
				return created, prebaks, warnings, fmt.Errorf("target path %q is not empty; clear it on the target before migrating this bind", b.Host)
			}
		}
		// Pin the archive to the bind's own basename so extraction (tar -C parent)
		// writes ONLY into targetBind, never a sibling under the shared parent.
		if verr := tarTopLevelIs(path, filepath.Base(targetBind)); verr != nil {
			return created, prebaks, warnings, verr
		}
		if eerr := extractTarToDir(ctx, filepath.Dir(targetBind), path); eerr != nil {
			return created, prebaks, warnings, eerr
		}
	}
	return created, prebaks, warnings, nil
}

// validStackID requires a leading alphanumeric (so "." and ".." cannot match),
// then up to 62 more of [a-zA-Z0-9_.-]. The leading-alnum rule is what blocks
// traversal/reserved names; the explicit ".." check below is defense in depth.
var validStackID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

// withinRoot reports whether path is root or a lexical descendant of root.
func withinRoot(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// restoreDefinition writes the stack's compose + .env + extra files under
// composeRoot/<stackId>/. The stackId is validated (no traversal) and bundle
// entry names were already validated by the reader. Files keep 0600/0755.
func restoreDefinition(composeRoot string, m MigrationManifest, files map[string][]byte) error {
	if !validStackID.MatchString(m.StackID) || strings.Contains(m.StackID, "..") {
		return fmt.Errorf("invalid stack id %q", m.StackID)
	}
	stackDir := filepath.Join(composeRoot, m.StackID)
	if !withinRoot(composeRoot, stackDir) {
		return fmt.Errorf("stack dir escapes compose root: %q", m.StackID)
	}
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		return fmt.Errorf("create stack dir: %w", err)
	}
	composeData, ok := files["compose/"+m.ComposeFile]
	if !ok {
		return fmt.Errorf("bundle missing compose file %q", m.ComposeFile)
	}
	if err := writeFileAtomic(filepath.Join(stackDir, m.ComposeFile), composeData, 0o600); err != nil {
		return fmt.Errorf("write compose: %w", err)
	}
	if m.HasEnv {
		if env, ok := files["compose/.env"]; ok {
			if err := writeFileAtomic(filepath.Join(stackDir, ".env"), env, 0o600); err != nil {
				return fmt.Errorf("write .env: %w", err)
			}
		}
	}
	for _, extra := range m.ExtraFiles {
		if data, ok := files["compose/"+extra]; ok {
			dst := filepath.Join(stackDir, filepath.Clean(extra))
			if !withinRoot(stackDir, dst) {
				return fmt.Errorf("extra file escapes stack dir: %q", extra)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("create dir for %s: %w", extra, err)
			}
			if err := writeFileAtomic(dst, data, 0o600); err != nil {
				return fmt.Errorf("write extra %s: %w", extra, err)
			}
		}
	}
	return nil
}

// writeFileAtomic writes data to path via a temp file + rename so a crash
// mid-write can't leave a partial file. The mode is applied before rename.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-migrate-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
