package featureserveragents

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/serveragents"
	"github.com/svrforum/SFPanel/internal/tasks"
)

// aptCall is one package transaction a deploy asked for.
type aptCall struct {
	Kind   string
	Names  []string
	Update bool
}

// fakeIO is the job a deploy runs in: it keeps the log lines and the apt
// transactions, and onApt plays what apt does to the host.
type fakeIO struct {
	logs  []string
	apts  []aptCall
	onApt func(aptCall) error
}

func (f *fakeIO) Logf(format string, args ...any) {
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
}

func (f *fakeIO) Apt(_ context.Context, kind string, names []string, update bool) error {
	c := aptCall{Kind: kind, Names: slices.Clone(names), Update: update}
	f.apts = append(f.apts, c)
	if f.onApt != nil {
		return f.onApt(c)
	}
	return nil
}

// logged reports whether any log line holds s.
func (f *fakeIO) logged(s string) bool {
	return slices.ContainsFunc(f.logs, func(l string) bool { return strings.Contains(l, s) })
}

// The vendors' key files, as the key server serves them.
const (
	zabbixKeyPath = "/zabbix-official-repo.key"
	wazuhKeyPath  = "/key/GPG-KEY-WAZUH"
)

// deployFixture is a host fixture with a deployer on it: a key server that
// serves the vendors' real key files, a job that records what it is asked,
// and a clock that Sleep moves forward instead of waiting.
type deployFixture struct {
	*hostFixture
	h  *Host
	d  *Deployer
	io *fakeIO

	keyMu   sync.Mutex
	keys    map[string][]byte // URL path → the file served there
	keyHits int

	sleeps  int
	onSleep func(n int)
}

func newDeployFixture(t *testing.T) *deployFixture {
	t.Helper()
	hf, h := newHostFixture(t)
	f := &deployFixture{hostFixture: hf, h: h, io: &fakeIO{}, keys: map[string][]byte{
		zabbixKeyPath: []byte(testdata(t, "zabbix-official-repo.key")),
		wazuhKeyPath:  []byte(testdata(t, "GPG-KEY-WAZUH")),
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.keyMu.Lock()
		f.keyHits++
		body, ok := f.keys[r.URL.Path]
		f.keyMu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	h.HTTP = srv.Client()
	h.KeyURLs = map[string]string{
		serveragents.Zabbix: srv.URL + zabbixKeyPath,
		serveragents.Wazuh:  srv.URL + wazuhKeyPath,
	}
	d := NewDeployer(h)
	d.Wait, d.Poll, d.Settle = 60*time.Second, 5*time.Second, 10*time.Second
	d.Sleep = func(ctx context.Context, dur time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		f.now = f.now.Add(dur)
		f.sleeps++
		if f.onSleep != nil {
			f.onSleep(f.sleeps)
		}
		return nil
	}
	f.d = d
	return f
}

// serve puts a file at a path of the key server.
func (f *deployFixture) serve(path string, body []byte) {
	f.keyMu.Lock()
	defer f.keyMu.Unlock()
	f.keys[path] = body
}

func (f *deployFixture) hits() int {
	f.keyMu.Lock()
	defer f.keyMu.Unlock()
	return f.keyHits
}

// read reads a file under the root; "" when it is not there.
func (f *deployFixture) read(rel string) string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.root, rel))
	if err != nil {
		return ""
	}
	return string(b)
}

func (f *deployFixture) has(rel string) bool {
	_, err := os.Stat(filepath.Join(f.root, rel))
	return err == nil
}

// tree is every file under the root and what it holds.
func (f *deployFixture) tree() map[string]string {
	f.t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(f.root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !e.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(f.root, p)
			files[rel] = string(b)
		}
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return files
}

// zabbixStep is an agent step for Zabbix with these values, for node-a.
func zabbixStep(server, active, metadata string) tasks.AgentStep {
	return tasks.AgentStep{
		Name:     serveragents.Zabbix,
		NodeName: "node-a",
		Settings: &serveragents.Settings{Zabbix: serveragents.ZabbixSettings{Server: server, ServerActive: active, HostMetadata: metadata}},
	}
}

var backupName = regexp.MustCompile(`\.sfpanel-\d{8}T\d{6}Z\.bak$`)

// backups are the backups of a file under the root, oldest first.
func (f *deployFixture) backups(rel string) []string {
	f.t.Helper()
	matches, err := filepath.Glob(filepath.Join(f.root, rel) + ".sfpanel-*.bak")
	if err != nil {
		f.t.Fatal(err)
	}
	slices.Sort(matches)
	return matches
}

func TestAFileIsReplacedWithItsModeOwnerAndABackup(t *testing.T) {
	f := newDeployFixture(t)
	f.write("etc/zabbix/zabbix_agent2.conf", "Server=127.0.0.1\n")
	p := filepath.Join(f.root, "etc/zabbix/zabbix_agent2.conf")
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}

	backup, err := f.h.writeConf("/etc/zabbix/zabbix_agent2.conf", []byte("Server=192.0.2.10\n"))
	if err != nil {
		t.Fatal(err)
	}

	if got := f.read("etc/zabbix/zabbix_agent2.conf"); got != "Server=192.0.2.10\n" {
		t.Errorf("file = %q, want the new bytes", got)
	}
	after, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want the file's own 0640", after.Mode().Perm())
	}
	ob, ab := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	if ob.Uid != ab.Uid || ob.Gid != ab.Gid {
		t.Errorf("owner = %d:%d, want the file's own %d:%d", ab.Uid, ab.Gid, ob.Uid, ob.Gid)
	}
	if !strings.HasPrefix(backup, "/etc/zabbix/zabbix_agent2.conf.sfpanel-") || !backupName.MatchString(backup) {
		t.Fatalf("backup = %q, want /etc/zabbix/zabbix_agent2.conf.sfpanel-<UTC time>.bak", backup)
	}
	if want := "/etc/zabbix/zabbix_agent2.conf.sfpanel-20310304T100000Z.bak"; backup != want {
		t.Errorf("backup = %q, want %q, named at the host's clock in UTC", backup, want)
	}
	if got := f.read(backup); got != "Server=127.0.0.1\n" {
		t.Errorf("backup holds %q, want the old bytes", got)
	}
	if bi, err := os.Stat(filepath.Join(f.root, backup)); err != nil {
		t.Errorf("backup: %v", err)
	} else if bi.Mode().Perm() != 0o640 {
		t.Errorf("backup mode = %v, want 0640 like the file it keeps", bi.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(f.root, "etc/zabbix/.*")); len(left) != 0 {
		t.Errorf("temporary files left: %q", left)
	}
}

func TestOnlyTheThreeNewestBackupsAreKept(t *testing.T) {
	f := newDeployFixture(t)
	f.write("etc/zabbix/zabbix_agent2.conf", "Server=v0\n")
	var made []string
	for i := 1; i <= 4; i++ {
		f.now = f.now.Add(time.Second)
		backup, err := f.h.writeConf("/etc/zabbix/zabbix_agent2.conf", []byte(fmt.Sprintf("Server=v%d\n", i)))
		if err != nil {
			t.Fatal(err)
		}
		made = append(made, filepath.Join(f.root, backup))
	}

	got := f.backups("etc/zabbix/zabbix_agent2.conf")
	if !slices.Equal(got, made[1:]) {
		t.Fatalf("backups = %q, want the three newest %q", got, made[1:])
	}
	if b, _ := os.ReadFile(got[0]); string(b) != "Server=v1\n" {
		t.Errorf("oldest kept backup holds %q, want v1", b)
	}
}

func TestTheSameContentIsNotWritten(t *testing.T) {
	f := newDeployFixture(t)
	f.write("etc/zabbix/zabbix_agent2.conf", "Server=192.0.2.10\n")
	p := filepath.Join(f.root, "etc/zabbix/zabbix_agent2.conf")
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}

	backup, err := f.h.writeConf("/etc/zabbix/zabbix_agent2.conf", []byte("Server=192.0.2.10\n"))
	if err != nil {
		t.Fatal(err)
	}

	if backup != "" {
		t.Errorf("backup = %q, want none for the bytes the file already holds", backup)
	}
	after, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("the file was replaced, want it left alone")
	}
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("directory holds %d entries, want only the file", len(entries))
	}
}
