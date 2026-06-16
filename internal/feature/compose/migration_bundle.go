package compose

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const manifestEntry = "manifest.json"

// maxSmallEntryBytes caps the in-memory entries (manifest + compose/.env). Those
// are KB in practice; the cap stops a hostile bundle whose manifest or a
// non-data entry is sized to OOM the receiver before the stream sha is checked.
const maxSmallEntryBytes = 16 << 20 // 16 MiB

// readSmallEntry reads an in-memory bundle entry, refusing anything over the cap.
func readSmallEntry(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxSmallEntryBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxSmallEntryBytes {
		return nil, fmt.Errorf("bundle entry exceeds %d-byte limit", maxSmallEntryBytes)
	}
	return data, nil
}

type BundleWriter struct{ tw *tar.Writer }

func NewBundleWriter(w io.Writer) *BundleWriter { return &BundleWriter{tw: tar.NewWriter(w)} }

func (b *BundleWriter) WriteManifest(m MigrationManifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return b.WriteFile(manifestEntry, data, 0o600)
}

func (b *BundleWriter) WriteFile(name string, data []byte, mode int64) error {
	if err := b.tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := b.tw.Write(data)
	return err
}

// WriteFileFromPath streams a file on disk into the bundle as entry `name`,
// without reading it into memory — used for GB-scale volume/bind/image archives.
func (b *BundleWriter) WriteFileFromPath(name, srcPath string, mode int64) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if err := b.tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: fi.Size(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err = io.Copy(b.tw, f)
	return err
}

func (b *BundleWriter) Close() error { return b.tw.Close() }

type BundleReader struct{ r io.Reader }

func NewBundleReader(r io.Reader) *BundleReader { return &BundleReader{r: r} }

// safeBundlePath rejects absolute paths and traversal — the bundle is untrusted
// input on the receiving node.
func safeBundlePath(name string) error {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return fmt.Errorf("unsafe bundle entry name: %q", name)
	}
	return nil
}

// isDataEntry reports whether a bundle entry is a large streamed data archive
// (volume / bind / image) rather than a small in-memory definition file.
func isDataEntry(name string) bool {
	return strings.HasPrefix(name, "volumes/") ||
		strings.HasPrefix(name, "binds/") ||
		strings.HasPrefix(name, "images/")
}

// manifestEntryShas maps each data archive's bundle name to its expected sha256.
func manifestEntryShas(m MigrationManifest) map[string]string {
	out := map[string]string{}
	for _, v := range m.Volumes {
		if v.Archive != "" {
			out[v.Archive] = v.Sha256
		}
	}
	for _, b := range m.Binds {
		if b.Archive != "" {
			out[b.Archive] = b.Sha256
		}
	}
	for _, im := range m.Images {
		if im.Archive != "" {
			out[im.Archive] = im.Sha256
		}
	}
	return out
}

// streamToFileSha copies r to dst, returning the lowercase-hex sha256 of the data.
func streamToFileSha(dst string, r io.Reader) (string, error) {
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), r); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ReadAll parses the manifest, keeps small definition files (compose/.env) in
// memory, and STREAMS large data entries (volumes/binds/images) to files under
// destRoot — verifying each against the manifest's per-entry sha256. Returns the
// manifest, the in-memory small files, and a map of data-entry name → staged
// file path. The manifest must precede any data entry (the packager writes it
// first); an out-of-order or unexpected/corrupt data entry is rejected.
func (b *BundleReader) ReadAll(destRoot string) (MigrationManifest, map[string][]byte, map[string]string, error) {
	tr := tar.NewReader(b.r)
	files := map[string][]byte{}
	staged := map[string]string{}
	var m MigrationManifest
	var entryShas map[string]string
	haveManifest := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, nil, nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if err := safeBundlePath(hdr.Name); err != nil {
			return m, nil, nil, err
		}
		if hdr.Name == manifestEntry {
			data, err := readSmallEntry(tr)
			if err != nil {
				return m, nil, nil, err
			}
			if err := json.Unmarshal(data, &m); err != nil {
				return m, nil, nil, fmt.Errorf("parse manifest: %w", err)
			}
			entryShas = manifestEntryShas(m)
			haveManifest = true
			continue
		}
		if isDataEntry(hdr.Name) {
			if !haveManifest {
				return m, nil, nil, fmt.Errorf("data entry %q before manifest", hdr.Name)
			}
			dst := filepath.Join(destRoot, filepath.FromSlash(hdr.Name))
			if !withinRoot(destRoot, dst) {
				return m, nil, nil, fmt.Errorf("data entry escapes staging dir: %q", hdr.Name)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
				return m, nil, nil, err
			}
			got, err := streamToFileSha(dst, tr)
			if err != nil {
				return m, nil, nil, err
			}
			want, ok := entryShas[hdr.Name]
			if !ok || want == "" || got != want {
				return m, nil, nil, fmt.Errorf("data entry %q checksum mismatch or unexpected", hdr.Name)
			}
			staged[hdr.Name] = dst
			continue
		}
		data, err := readSmallEntry(tr)
		if err != nil {
			return m, nil, nil, err
		}
		files[hdr.Name] = data
	}
	if !haveManifest {
		return m, nil, nil, fmt.Errorf("bundle missing manifest.json")
	}
	return m, files, staged, nil
}
