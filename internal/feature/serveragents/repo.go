package featureserveragents

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/serveragents"
)

const (
	keyFetchTimeout = 30 * time.Second
	// keyFileLimit bounds a vendor's key file; theirs are a few KiB.
	keyFileLimit = 256 << 10
)

// Where apt reads its sources.
const (
	aptSourcesList = "/etc/apt/sources.list"
	aptSourcesDir  = "/etc/apt/sources.list.d"
)

// vendorRepo is a vendor's apt repository and the files that give it to apt.
type vendorRepo struct {
	Agent       string
	Host        string // the repository's host, as sources name it
	KeyURL      string // where the vendor publishes its signing key
	Fingerprint string // the one key in that file apt is to trust
	Keyring     string
	SourcesFile string
	Line        string // the source line, signed by Keyring
	PrefFile    string // "" for no pin
	Pref        string
}

// addRepo gives apt the vendor's repository: its signing key — only the key
// with the pinned fingerprint, taken out of the file the vendor publishes —
// then the pin, if any, then the source, so a source on disk means the rest
// is there too. When any source already names the vendor's host, that one is
// used as it is and nothing is written. added reports whether files were
// written.
func (d *Deployer) addRepo(ctx context.Context, r vendorRepo, io JobIO) (added bool, err error) {
	if file := d.aptSourceFor(r.Host); file != "" {
		io.Logf("apt already has a source for %s, in %s; it is used as it is", r.Host, file)
		return false, nil
	}
	io.Logf("fetching the %s signing key from %s", r.Agent, r.KeyURL)
	file, err := d.fetchKey(ctx, r.KeyURL)
	if err != nil {
		return false, err
	}
	keyring, err := serveragents.SelectKey(file, r.Fingerprint)
	switch {
	case errors.Is(err, serveragents.ErrKeyNotFound):
		return false, fmt.Errorf("the signing key at %s does not have fingerprint %s", r.KeyURL, r.Fingerprint)
	case err != nil:
		return false, fmt.Errorf("the signing key at %s cannot be used: %w", r.KeyURL, err)
	}
	io.Logf("the key with fingerprint %s goes to %s", r.Fingerprint, r.Keyring)
	if err := d.writeFile(r.Keyring, keyring); err != nil {
		return false, fmt.Errorf("writing %s: %w", r.Keyring, err)
	}
	if r.PrefFile != "" {
		if err := d.writeFile(r.PrefFile, []byte(r.Pref+"\n")); err != nil {
			return false, fmt.Errorf("writing %s: %w", r.PrefFile, err)
		}
		io.Logf("pinned the agent's packages to %s in %s", r.Host, r.PrefFile)
	}
	if err := d.writeFile(r.SourcesFile, []byte(r.Line+"\n")); err != nil {
		return false, fmt.Errorf("writing %s: %w", r.SourcesFile, err)
	}
	io.Logf("added %s: %s", r.SourcesFile, r.Line)
	return true, nil
}

// fetchKey downloads a vendor's key file.
func (h *Host) fetchKey(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, keyFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching the signing key from %s failed: %s", url, response.SanitizeOutput(err.Error()))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the signing key at %s answered %s", url, response.SanitizeOutput(resp.Status))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, keyFileLimit+1))
	if err != nil {
		return nil, fmt.Errorf("fetching the signing key from %s failed: %s", url, response.SanitizeOutput(err.Error()))
	}
	if len(b) > keyFileLimit {
		return nil, fmt.Errorf("the signing key at %s is larger than %d KiB", url, keyFileLimit>>10)
	}
	return b, nil
}

// aptSourceFor finds a source file with a line, not commented out, that
// names host: the file's path as the host sees it, or "".
func (h *Host) aptSourceFor(host string) string {
	files := []string{aptSourcesList}
	for _, pattern := range []string{"*.list", "*.sources"} {
		matches, _ := filepath.Glob(filepath.Join(h.path(aptSourcesDir), pattern))
		for _, m := range matches {
			files = append(files, filepath.Join(aptSourcesDir, filepath.Base(m)))
		}
	}
	for _, f := range files {
		b, err := os.ReadFile(h.path(f))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line != "" && line[0] != '#' && namesHost(line, host) {
				return f
			}
		}
	}
	return ""
}

// namesHost reports whether a source line holds a URI on host: "://host"
// followed by a path, a port, a space or the end of the line.
func namesHost(line, host string) bool {
	for rest := line; ; {
		i := strings.Index(rest, "://"+host)
		if i < 0 {
			return false
		}
		rest = rest[i+len("://"+host):]
		if rest == "" || strings.ContainsRune("/: \t", rune(rest[0])) {
			return true
		}
	}
}
