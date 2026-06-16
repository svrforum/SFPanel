package compose

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const manifestEntry = "manifest.json"

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

// ReadAll parses the manifest and returns all regular-file entries as bytes,
// keyed by their (validated) bundle path. M1 keeps small files in memory; later
// milestones stream large data entries to disk under destRoot instead.
func (b *BundleReader) ReadAll(destRoot string) (MigrationManifest, map[string][]byte, error) {
	_ = destRoot // used by later milestones for streamed data entries
	tr := tar.NewReader(b.r)
	files := map[string][]byte{}
	var m MigrationManifest
	haveManifest := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if err := safeBundlePath(hdr.Name); err != nil {
			return m, nil, err
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return m, nil, err
		}
		if hdr.Name == manifestEntry {
			if err := json.Unmarshal(data, &m); err != nil {
				return m, nil, fmt.Errorf("parse manifest: %w", err)
			}
			haveManifest = true
			continue
		}
		files[hdr.Name] = data
	}
	if !haveManifest {
		return m, nil, fmt.Errorf("bundle missing manifest.json")
	}
	return m, files, nil
}
