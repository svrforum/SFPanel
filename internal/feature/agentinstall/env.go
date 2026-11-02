package agentinstall

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/common/exec"
	sfdb "github.com/svrforum/SFPanel/internal/db"
	"github.com/svrforum/SFPanel/internal/feature/audit"
	"github.com/svrforum/SFPanel/internal/release"
	"github.com/svrforum/SFPanel/scripts"
)

// ArchivesEnv names the directory an unreleased version's archives are
// installed from: a test rig's, which cannot sign a release.
const ArchivesEnv = "SFPANEL_AGENT_ARCHIVES"

// ClusterEnv is Env over this node's cluster manager, the release
// downloads, its own install.sh and its tailscale command.
type ClusterEnv struct {
	getMgr  func() *cluster.Manager
	version string
	audit   *sfdb.AsyncWriter
	cmd     exec.Commander
}

// NewClusterEnv returns the Env the panel runs with: version is this
// binary's, audit where the install's start and end are recorded, cmd what
// runs `tailscale status`.
func NewClusterEnv(getMgr func() *cluster.Manager, version string, audit *sfdb.AsyncWriter, cmd exec.Commander) *ClusterEnv {
	return &ClusterEnv{getMgr: getMgr, version: version, audit: audit, cmd: cmd}
}

func (c *ClusterEnv) mgr() (*cluster.Manager, error) {
	if m := c.getMgr(); m != nil {
		return m, nil
	}
	return nil, cluster.ErrNotLeader
}

func (c *ClusterEnv) IsLeader() bool {
	m, err := c.mgr()
	return err == nil && m.IsLeader()
}

func (c *ClusterEnv) Version() string { return c.version }

func (c *ClusterEnv) ManagerVersions() map[string]string {
	m, err := c.mgr()
	if err != nil {
		return nil
	}
	hb := m.GetHeartbeat()
	out := map[string]string{}
	for _, n := range m.GetNodes() {
		if !n.IsManager() || n.ID == m.LocalNodeID() {
			continue
		}
		name := n.Name
		if name == "" {
			name = n.ID
		}
		out[name] = ""
		if hb != nil {
			if met := hb.GetMetrics(n.ID); met != nil {
				out[name] = met.Version
			}
		}
	}
	return out
}

func (c *ClusterEnv) ActiveRun() string {
	m, err := c.mgr()
	if err != nil {
		return ""
	}
	runs, err := m.ListRuns()
	if err != nil {
		return ""
	}
	for _, r := range runs {
		if r.Active() {
			return r.ID
		}
	}
	return ""
}

func (c *ClusterEnv) Nodes() []cluster.Node {
	m, err := c.mgr()
	if err != nil {
		return nil
	}
	var out []cluster.Node
	for _, n := range m.GetNodes() {
		out = append(out, *n)
	}
	return out
}

func (c *ClusterEnv) ReadyForAgentRegistration(name string, ip net.IP) error {
	m, err := c.mgr()
	if err != nil {
		return err
	}
	return m.ReadyForAgentRegistration(name, ip)
}

func (c *ClusterEnv) JoinAddressFor(target net.IP) (string, error) {
	m, err := c.mgr()
	if err != nil {
		return "", err
	}
	return m.JoinAddressFor(target)
}

func (c *ClusterEnv) CAPin() (string, error) {
	m, err := c.mgr()
	if err != nil {
		return "", err
	}
	spki, err := m.CAPublicKey()
	if err != nil {
		return "", err
	}
	return cluster.CAPin(spki), nil
}

func (c *ClusterEnv) CreateAgentToken(ip net.IP) (string, error) {
	m, err := c.mgr()
	if err != nil {
		return "", err
	}
	t, err := m.CreateAgentTokenFor(ip, cluster.AgentInstallTokenTTL)
	if err != nil {
		return "", err
	}
	return t.Token, nil
}

func (c *ClusterEnv) RevokeToken(token string) error {
	m, err := c.mgr()
	if err != nil {
		return err
	}
	return m.RevokeJoinTokenValue(token)
}

func (c *ClusterEnv) Heartbeat(id string) (heard, online bool, version string) {
	m, err := c.mgr()
	if err != nil {
		return false, false, ""
	}
	hb := m.GetHeartbeat()
	if hb == nil {
		return false, false, ""
	}
	met := hb.GetMetrics(id)
	if met == nil {
		return false, false, ""
	}
	return true, hb.LiveHealth()[id] == cluster.StatusOnline, met.Version
}

// healthPath is the panel's public health route: a node that answers it
// is up, on agents too.
const healthPath = "/api/v1/health"

func (c *ClusterEnv) ProxyHealth(ctx context.Context, id string) (int, error) {
	m, err := c.mgr()
	if err != nil {
		return 0, err
	}
	status, _, err := m.ProxyToNode(ctx, id, http.MethodGet, healthPath, nil, "")
	return status, err
}

func (c *ClusterEnv) PanelHealth(ctx context.Context, id string) (int, error) {
	m, err := c.mgr()
	if err != nil {
		return 0, err
	}
	node := m.GetNode(id)
	if node == nil {
		return 0, cluster.ErrNodeNotFound
	}
	ep, err := m.PanelEndpointChecked(node)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ep.BaseURL+healthPath, nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: ep.TLSConfig}}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, nil
}

func (c *ClusterEnv) RemoveNode(id string) error {
	m, err := c.mgr()
	if err != nil {
		return err
	}
	return m.RemoveNode(id)
}

func (c *ClusterEnv) UpdateNodeLabels(id string, labels map[string]string) error {
	m, err := c.mgr()
	if err != nil {
		return err
	}
	return m.UpdateNodeLabels(id, labels)
}

func (c *ClusterEnv) PutRun(r cluster.RunRecord) error {
	m, err := c.mgr()
	if err != nil {
		return err
	}
	return m.PutRun(r)
}

func (c *ClusterEnv) PutRunNode(runID, key string, rec cluster.RunNodeRecord) error {
	m, err := c.mgr()
	if err != nil {
		return err
	}
	return m.PutRunNode(runID, key, rec)
}

func (c *ClusterEnv) Audit(user, method, path string, status int) {
	nodeID := ""
	if m := c.getMgr(); m != nil {
		nodeID = m.LocalNodeID()
	}
	audit.RecordEvent(c.audit, user, method, path, status, nodeID)
}

// releaseArches are the CPUs a release is built for.
var releaseArches = []string{"amd64", "arm64"}

func (c *ClusterEnv) Checksums(ctx context.Context, version string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	return release.VerifiedChecksums(ctx, client, release.TaggedRelease(version, releaseArches...).Assets, version, version, nil)
}

func (c *ClusterEnv) Archive(ctx context.Context, version, arch string, checksums []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "sfpanel-agent-archive-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	client := &http.Client{Timeout: 5 * time.Minute}
	path, err := release.DownloadArchive(ctx, client, release.TaggedRelease(version, releaseArches...).Assets,
		release.ArchiveName(version, arch), checksums, dir)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// Archives is read only when the version is no release (pinVersion).
func (c *ClusterEnv) Archives() string { return os.Getenv(ArchivesEnv) }

func (c *ClusterEnv) InstallScript() []byte { return scripts.InstallScript }

func (c *ClusterEnv) Tailnet(ctx context.Context) (Tailnet, error) { return readTailnet(ctx, c.cmd) }

func (c *ClusterEnv) Now() time.Time { return time.Now() }
