package featureserveragents

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/common/platform"
	"github.com/svrforum/SFPanel/internal/serveragents"
)

// argvCmd answers each command by its whole command line, "name arg…", and
// refuses any line no test scripted. The MockCommander it holds records every
// call.
type argvCmd struct {
	*exec.MockCommander
	mu      sync.Mutex
	answers map[string]exec.MockResult
	run     []string            // every command line, in order
	envs    map[string][]string // the environment each line last ran with
}

func newArgvCmd() *argvCmd {
	return &argvCmd{MockCommander: exec.NewMockCommander(), answers: map[string]exec.MockResult{}, envs: map[string][]string{}}
}

// on scripts the answer to one command line.
func (c *argvCmd) on(line, out string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.answers[line] = exec.MockResult{Output: out, Err: err}
}

func (c *argvCmd) answer(env []string, name string, args ...string) (string, error) {
	_, _ = c.MockCommander.Run(name, args...)
	line := strings.Join(append([]string{name}, args...), " ")
	c.mu.Lock()
	defer c.mu.Unlock()
	c.run = append(c.run, line)
	c.envs[line] = env
	if r, ok := c.answers[line]; ok {
		return r.Output, r.Err
	}
	return "", errors.New("not scripted: " + line)
}

// lines are the command lines run so far, in order.
func (c *argvCmd) lines() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.run)
}

// env is the environment the command line last ran with.
func (c *argvCmd) env(line string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.envs[line]
}

// ran reports whether any command line run so far starts with prefix.
func (c *argvCmd) ran(prefix string) bool {
	return slices.ContainsFunc(c.lines(), func(l string) bool { return strings.HasPrefix(l, prefix) })
}

func (c *argvCmd) Run(name string, args ...string) (string, error) {
	return c.answer(nil, name, args...)
}

func (c *argvCmd) RunWithTimeout(_ time.Duration, name string, args ...string) (string, error) {
	return c.answer(nil, name, args...)
}

func (c *argvCmd) RunWithEnv(env []string, name string, args ...string) (string, error) {
	return c.answer(env, name, args...)
}

func (c *argvCmd) RunWithInput(_ string, name string, args ...string) (string, error) {
	return c.answer(nil, name, args...)
}

func (c *argvCmd) RunCtx(_ context.Context, name string, args ...string) (string, error) {
	return c.answer(nil, name, args...)
}

func (c *argvCmd) RunWithEnvCtx(_ context.Context, env []string, name string, args ...string) (string, error) {
	return c.answer(env, name, args...)
}

func (c *argvCmd) Exists(name string) bool {
	_, err := c.answer(nil, "exists:"+name)
	return err == nil
}

// dpkgLine is the dpkg-query command line that reads pkg.
func dpkgLine(pkg string) string {
	return "dpkg-query -W -f=" + serveragents.DpkgFormat + " " + pkg
}

// hostFixture is a host under a temporary root, with scripted commands, a
// scripted network and its own clock.
type hostFixture struct {
	t      *testing.T
	root   string
	cmd    *argvCmd
	mu     sync.Mutex
	dials  map[string]error // "host:port" → the dial's error; nil connects
	dialed []string
	now    time.Time
}

// fixtureNow is far from the real clock, so code that reads the real time
// instead of Host.Now cannot pass by accident.
var fixtureNow = time.Date(2031, 3, 4, 10, 0, 0, 0, time.UTC)

func newHostFixture(t *testing.T) (*hostFixture, *Host) {
	t.Helper()
	f := &hostFixture{t: t, root: t.TempDir(), cmd: newArgvCmd(), dials: map[string]error{}, now: fixtureNow}
	h := &Host{
		Root: f.root,
		Cmd:  f.cmd,
		Dial: func(_ context.Context, _, addr string) (net.Conn, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.dialed = append(f.dialed, addr)
			if err := f.dials[addr]; err != nil {
				return nil, err
			}
			c, peer := net.Pipe()
			t.Cleanup(func() { _ = peer.Close() })
			return c, nil
		},
		HTTP:     &http.Client{Transport: noNetwork{}},
		KeyURLs:  map[string]string{},
		Platform: func() platform.Info { return platform.Info{} },
		Now:      func() time.Time { return f.now },
	}
	return f, h
}

// noNetwork refuses every HTTP request a test did not set up a server for.
type noNetwork struct{}

func (noNetwork) RoundTrip(r *http.Request) (*http.Response, error) {
	return nil, errors.New("no network in tests: " + r.URL.String())
}

// write puts a file under the fixture's root, modified at the fixture's now.
func (f *hostFixture) write(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.chtime(rel, f.now)
}

// chtime sets when a file under the root was last modified.
func (f *hostFixture) chtime(rel string, at time.Time) {
	f.t.Helper()
	if err := os.Chtimes(filepath.Join(f.root, rel), at, at); err != nil {
		f.t.Fatal(err)
	}
}

func (f *hostFixture) dialedAddrs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.dialed)
}

// The os-release files of the systems the tests run on.
const (
	osNoble    = "PRETTY_NAME=\"Ubuntu 24.04.3 LTS\"\nNAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nVERSION_CODENAME=noble\nID=ubuntu\nID_LIKE=debian\nUBUNTU_CODENAME=noble\n"
	osQuesting = "PRETTY_NAME=\"Ubuntu 25.10\"\nNAME=\"Ubuntu\"\nVERSION_ID=\"25.10\"\nVERSION_CODENAME=questing\nID=ubuntu\nID_LIKE=debian\nUBUNTU_CODENAME=questing\n"
	osFedora   = "NAME=\"Fedora Linux\"\nVERSION_ID=40\nID=fedora\nPRETTY_NAME=\"Fedora Linux 40 (Server Edition)\"\n"
)

// system writes the host's os-release.
func (f *hostFixture) system(osRelease string) { f.write("etc/os-release", osRelease) }

// pkg scripts dpkg's answer for an installed package, given as
// "<Status>|<Version>" such as "install ok installed|4.14.10-1".
func (f *hostFixture) pkg(name, statusVersion string) {
	f.cmd.on(dpkgLine(name), statusVersion+"\n", nil)
}

// noPkg scripts dpkg's answer for packages that are not installed.
func (f *hostFixture) noPkg(names ...string) {
	for _, name := range names {
		f.cmd.on(dpkgLine(name), "dpkg-query: no packages found matching "+name+"\n", errors.New("exit status 1"))
	}
}

// service scripts systemctl is-active for a unit; anything but active exits 3.
func (f *hostFixture) service(unit, word string) {
	var err error
	if word != "active" {
		err = errors.New("exit status 3")
	}
	f.cmd.on("systemctl is-active "+unit, word+"\n", err)
}

// unit scripts the LoadState systemctl reports for zabbix-agent2.service.
func (f *hostFixture) unit(loadState string) {
	f.cmd.on("systemctl show -p LoadState --value zabbix-agent2.service", loadState+"\n", nil)
}

// agentdState writes wazuh-agentd.state with a status and keepalive.
func (f *hostFixture) agentdState(status, keepalive, ack string) {
	f.write("var/ossec/var/run/wazuh-agentd.state", "# State file for wazuh-agentd\n# THIS FILE WILL BE OVERWRITTEN.\n\nstatus='"+status+"'\nlast_keepalive='"+keepalive+"'\nlast_ack='"+ack+"'\nmsg_count='12'\nmsg_sent='12'\n")
}

// testdata reads one of the pure package's fixtures.
func testdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "serveragents", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
