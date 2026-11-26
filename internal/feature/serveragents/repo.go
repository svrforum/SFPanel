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
	// Path is what the source of the repository must name after its host —
	// a release series such as /zabbix/7.0/ — for one on the host to be
	// used; "" takes any source on the host.
	Path string
}

// addRepo gives apt the vendor's repository: its signing key — only the key
// with the pinned fingerprint, taken out of the file the vendor publishes —
// then the pin, if any, then the source, so a source on disk means the rest
// is there too. When a source the host has already is the repository —
// turned on, on the vendor's host and naming r.Path — it is used as it is
// and nothing is written; the other sources on the vendor's host are left
// as they are, and the log says why they were not used. added reports
// whether files were written.
func (d *Deployer) addRepo(ctx context.Context, r vendorRepo, io JobIO) (added bool, err error) {
	sources := d.aptSourcesFor(r.Host)
	for _, src := range sources {
		if src.Enabled && src.names(r.Host+r.Path) {
			io.Logf("apt already has a source for %s, in %s; it is used as it is", r.Host+r.Path, src.File)
			return false, nil
		}
	}
	for _, src := range sources {
		why := "turned off"
		if src.Enabled {
			why = "not for " + strings.Trim(r.Path, "/")
		}
		io.Logf("apt has a source for %s in %s that is not used, %s: %s", r.Host, src.File, why, response.SanitizeOutput(src.Line))
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

// aptSource is a source of apt's that names a host.
type aptSource struct {
	File string // as the host sees it
	// Line is the source's line naming the host: a one-line source, or a
	// deb822 stanza's URIs.
	Line    string
	Enabled bool // a deb822 stanza can be turned off with Enabled: no
}

// names reports whether the source holds a URI on hostPath: the host and
// any path it starts with.
func (s aptSource) names(hostPath string) bool { return namesHost(s.Line, hostPath) }

// aptSourcesFor lists apt's sources that name host: the lines not commented
// out of sources.list and of sources.list.d/*.list, and the stanzas of
// sources.list.d/*.sources (deb822) whose URIs do.
func (h *Host) aptSourcesFor(host string) []aptSource {
	files := []string{aptSourcesList}
	matches, _ := filepath.Glob(filepath.Join(h.path(aptSourcesDir), "*.list"))
	for _, m := range matches {
		files = append(files, filepath.Join(aptSourcesDir, filepath.Base(m)))
	}
	var found []aptSource
	for _, f := range files {
		b, err := os.ReadFile(h.path(f))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line != "" && line[0] != '#' && namesHost(line, host) {
				found = append(found, aptSource{File: f, Line: line, Enabled: true})
			}
		}
	}
	matches, _ = filepath.Glob(filepath.Join(h.path(aptSourcesDir), "*.sources"))
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		file := filepath.Join(aptSourcesDir, filepath.Base(m))
		for _, st := range deb822Stanzas(b) {
			if uris := st["uris"]; namesHost(uris, host) {
				found = append(found, aptSource{File: file, Line: "URIs: " + uris, Enabled: !strings.EqualFold(st["enabled"], "no")})
			}
		}
	}
	return found
}

// deb822Stanzas reads the stanzas of a deb822 sources file: its fields by
// lowercased name, a field's continuation lines joined to it with a space.
// Blank lines end a stanza; lines starting with # are comments.
func deb822Stanzas(b []byte) []map[string]string {
	var stanzas []map[string]string
	cur := map[string]string{}
	key := ""
	end := func() {
		if len(cur) > 0 {
			stanzas = append(stanzas, cur)
		}
		cur, key = map[string]string{}, ""
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		raw := sc.Text()
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			end()
		case line[0] == '#':
		case raw[0] == ' ' || raw[0] == '\t':
			if key != "" {
				cur[key] += " " + line
			}
		default:
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				key = ""
				continue
			}
			key = strings.ToLower(strings.TrimSpace(k))
			cur[key] = strings.TrimSpace(v)
		}
	}
	end()
	return stanzas
}

// namesHost reports whether a source line holds a URI on host, or on a
// host and path such as repo.zabbix.com/zabbix/7.0/: "://host" followed by
// a path, a port, a space or the end of the line — after a path that ends
// in /, anything.
func namesHost(line, host string) bool {
	for rest := line; ; {
		i := strings.Index(rest, "://"+host)
		if i < 0 {
			return false
		}
		rest = rest[i+len("://"+host):]
		if rest == "" || strings.HasSuffix(host, "/") || strings.ContainsRune("/: \t", rune(rest[0])) {
			return true
		}
	}
}
