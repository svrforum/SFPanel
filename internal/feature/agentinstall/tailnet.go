package agentinstall

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/svrforum/SFPanel/internal/api/response"
	"github.com/svrforum/SFPanel/internal/cluster"
	"github.com/svrforum/SFPanel/internal/common/exec"
	"github.com/svrforum/SFPanel/internal/sshexec"
)

// The leader's tailnet: the devices the install form offers to pick from,
// and the host keys Tailscale reports for them, which a row's fetched key
// is compared with. Both are read from `tailscale status --json` on the
// leader, the node that reaches the servers.

// Tailnet is what `tailscale status --json` says that an install uses.
type Tailnet struct {
	// BackendState is Running when the leader is on its tailnet.
	BackendState string                  `json:"BackendState"`
	Self         *TailnetPeer            `json:"Self"`
	Peer         map[string]*TailnetPeer `json:"Peer"`
}

// TailnetPeer is one device of a tailnet. DNSName is its MagicDNS name
// without the trailing dot; SSHHostKeys the host keys Tailscale reports for
// it, in authorized_keys form ("ssh-ed25519 AAAA…").
type TailnetPeer struct {
	HostName     string   `json:"HostName"`
	DNSName      string   `json:"DNSName"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	OS           string   `json:"OS"`
	Online       bool     `json:"Online"`
	SSHHostKeys  []string `json:"sshHostKeys"`
}

// ipv4 is the device's IPv4 Tailscale address, or nil.
func (p *TailnetPeer) ipv4() net.IP {
	for _, a := range p.TailscaleIPs {
		if ip := net.ParseIP(a); ip != nil && cluster.IsTailscaleIP(ip) {
			return ip.To4()
		}
	}
	return nil
}

// holds reports whether ip is one of the device's Tailscale addresses.
func (p *TailnetPeer) holds(ip net.IP) bool {
	for _, a := range p.TailscaleIPs {
		if net.ParseIP(a).Equal(ip) {
			return true
		}
	}
	return false
}

// errNoTailscale is a leader without the tailscale command.
var errNoTailscale = errors.New("tailscale is not installed")

// errTailscaledDown is a tailscale command that reaches no tailscaled: the
// daemon is stopped, or its socket gone.
var errTailscaledDown = errors.New("tailscaled is not running")

// TailnetError is `tailscale status --json` failing, or answering with
// nothing this panel reads. Its message is sanitized: it is shown.
type TailnetError struct{ msg string }

func (e *TailnetError) Error() string { return e.msg }

// tailnetTimeout bounds one read of the tailnet.
const tailnetTimeout = 10 * time.Second

// readTailnet runs `tailscale status --json` through cmd, which is
// errNoTailscale without the command, errTailscaledDown when the command
// reaches no daemon, and a *TailnetError when it fails otherwise.
func readTailnet(ctx context.Context, cmd exec.Commander) (Tailnet, error) {
	if !cmd.Exists("tailscale") {
		return Tailnet{}, errNoTailscale
	}
	ctx, cancel := context.WithTimeout(ctx, tailnetTimeout)
	defer cancel()
	out, err := cmd.RunCtx(ctx, "tailscale", "status", "--json")
	if err != nil {
		msg := strings.TrimSpace(out)
		// "failed to connect to local tailscaled …", whether the daemon is
		// stopped or its socket does not answer.
		if strings.Contains(strings.ToLower(msg), "failed to connect to local tailscale") {
			return Tailnet{}, errTailscaledDown
		}
		if msg == "" {
			msg = err.Error()
		}
		return Tailnet{}, &TailnetError{msg: "tailscale status: " + response.SanitizeOutput(msg)}
	}
	// The output is stdout and stderr together: a warning tailscale prints
	// around the JSON (a client and daemon of different versions) is skipped.
	var t Tailnet
	i := strings.IndexByte(out, '{')
	if i < 0 || json.NewDecoder(strings.NewReader(out[i:])).Decode(&t) != nil {
		return Tailnet{}, &TailnetError{msg: "tailscale status gave no status this panel reads"}
	}
	for _, p := range t.Peer {
		if p != nil {
			p.DNSName = strings.TrimSuffix(p.DNSName, ".")
		}
	}
	if t.Self != nil {
		t.Self.DNSName = strings.TrimSuffix(t.Self.DNSName, ".")
	}
	return t, nil
}

// Why a leader is not on Tailscale, as GET /cluster/agent-installs/tailnet
// answers it.
const (
	tailnetNotInstalled = "not_installed"
	tailnetNotRunning   = "not_running"
	tailnetNoAddress    = "no_address"
)

// TailnetAnswer is GET /cluster/agent-installs/tailnet's answer: whether
// the leader is on Tailscale (Reason says why not), and the Linux devices
// of its tailnet an agent can be installed on, online first, then by name.
type TailnetAnswer struct {
	OnTailscale bool            `json:"on_tailscale"`
	Reason      string          `json:"reason"`
	Peers       []TailnetDevice `json:"peers"`
}

// TailnetDevice is one device of the leader's tailnet: IP its IPv4
// Tailscale address, SSH whether Tailscale reports SSH host keys for it,
// Node the cluster node, manager or agent, that holds that address ("" for
// none).
type TailnetDevice struct {
	Name    string `json:"name"`
	DNSName string `json:"dns_name"`
	IP      string `json:"ip"`
	OS      string `json:"os"`
	Online  bool   `json:"online"`
	SSH     bool   `json:"ssh"`
	Node    string `json:"node"`
}

// Tailnet is the leader's tailnet for the install form. Only Linux devices
// with an IPv4 Tailscale address are listed: an install supports no other.
// A leader not on Tailscale is an answer with its reason; a command that
// fails is a *TailnetError.
func (e *Engine) Tailnet(ctx context.Context) (TailnetAnswer, error) {
	ans := TailnetAnswer{Peers: []TailnetDevice{}}
	t, err := e.env.Tailnet(ctx)
	switch {
	case errors.Is(err, errNoTailscale):
		ans.Reason = tailnetNotInstalled
		return ans, nil
	case errors.Is(err, errTailscaledDown):
		ans.Reason = tailnetNotRunning
		return ans, nil
	case err != nil:
		return TailnetAnswer{}, err
	case t.BackendState != "Running":
		ans.Reason = tailnetNotRunning
		return ans, nil
	case t.Self == nil || t.Self.ipv4() == nil:
		ans.Reason = tailnetNoAddress
		return ans, nil
	}
	ans.OnTailscale = true
	held := map[string]string{}
	for _, n := range e.env.Nodes() {
		name := n.Name
		if name == "" {
			name = n.ID
		}
		for _, a := range n.AllAddresses() {
			if ip := net.ParseIP(a); ip != nil {
				held[ip.String()] = name
			}
		}
	}
	for _, p := range t.Peer {
		if p == nil || p.OS != "linux" {
			continue
		}
		ip := p.ipv4()
		if ip == nil {
			continue
		}
		ans.Peers = append(ans.Peers, TailnetDevice{
			Name: p.HostName, DNSName: p.DNSName, IP: ip.String(), OS: p.OS,
			Online: p.Online, SSH: len(p.SSHHostKeys) > 0, Node: held[ip.String()],
		})
	}
	sort.Slice(ans.Peers, func(i, j int) bool {
		a, b := ans.Peers[i], ans.Peers[j]
		if a.Online != b.Online {
			return a.Online
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.IP < b.IP
	})
	return ans, nil
}

// How a fetched host key compares with Tailscale's (RunNodeRecord.
// HostKeyTailscale).
const (
	hostKeyTailscaleMatch    = "match"
	hostKeyTailscaleMismatch = "mismatch"
)

// againstTailscale compares hk, the host key the server at the row's
// address presented, with the keys of its type Tailscale reports for the
// device holding that address: match, mismatch, or "" when Tailscale says
// nothing of it. A LAN address reads nothing. It is shown beside the
// question and decides nothing: the approval stays the operator's. A
// tailnet that cannot be read is said once in the connect phase's log.
func (r *row) againstTailscale(ctx context.Context, hk sshexec.HostKey, log *phaseLog) string {
	if !cluster.IsTailscaleIP(r.ip) || hk.Key == nil {
		return ""
	}
	t, err := r.s.env.Tailnet(ctx)
	switch {
	case err != nil:
		if ctx.Err() == nil {
			log.printf("the host key is not compared with Tailscale's: the tailnet could not be read (%s)", err)
		}
		return ""
	case t.BackendState != "Running":
		log.printf("the host key is not compared with Tailscale's: Tailscale on this leader is %s", t.BackendState)
		return ""
	}
	for _, p := range t.Peer {
		if p == nil || !p.holds(r.ip) {
			continue
		}
		switch v := compareHostKey(hk.Key, p.SSHHostKeys); v {
		case hostKeyTailscaleMatch:
			log.printf("Tailscale reports the same host key for %s", r.ip)
			return v
		case hostKeyTailscaleMismatch:
			log.printf("Tailscale reports another host key of this type for %s", r.ip)
			return v
		}
		return ""
	}
	return ""
}

// compareHostKey is match when listed, keys in authorized_keys form, holds
// key, mismatch when it holds keys of key's type and none is key, and ""
// when it holds none of that type.
func compareHostKey(key ssh.PublicKey, listed []string) string {
	want := key.Marshal()
	sameType := false
	for _, line := range listed {
		k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
		if err != nil || k.Type() != key.Type() {
			continue
		}
		if bytes.Equal(k.Marshal(), want) {
			return hostKeyTailscaleMatch
		}
		sameType = true
	}
	if sameType {
		return hostKeyTailscaleMismatch
	}
	return ""
}
