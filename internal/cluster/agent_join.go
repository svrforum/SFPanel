package cluster

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"github.com/svrforum/SFPanel/internal/config"
)

// AgentJoinEngine joins this server to a cluster as an agent (`sfpanel agent
// join`). It makes the agent's key pair — the private key never leaves this
// server — has the leader sign a certificate for it, and writes the
// certificate, the cluster CA, the agent's own internal-proxy key and an
// agent config.
type AgentJoinEngine struct {
	Config     *config.Config
	ConfigPath string
	// Version is this binary's, sent with the registration; the leader logs
	// it with the new agent.
	Version string
	Name    string // the name to register under; empty: the host name
	// NodeID is the ID to register under: a manager converted to an agent
	// keeps its own, with a token the leader made for it. Empty, the leader
	// assigns one.
	NodeID string
	// CheckPanelCert refuses a panel certificate the agent could not be
	// reached with, before anything is sent; nil checks nothing. The CLI
	// supplies it, as it does panelCA, so this package need not import
	// paneltls.
	CheckPanelCert func(advertise string) error

	// hostname is what the agent is named after when Name is empty; nil is
	// os.Hostname. Tests set it: their leader runs on the same host and
	// already carries the host's name, and a cluster holds each name once.
	hostname func() (string, error)
	// listenOn checks this server can listen on the advertise address at the
	// cluster gRPC port, as the agent's gRPC server will; nil tries it. Tests
	// register at documentation addresses no host has.
	listenOn func(ip net.IP, port int) error
}

// validNodeName is what a node may be called: what the UI, the CLI and a
// ?node= URL can carry as it is.
var validNodeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// ValidNodeName refuses a name a node may not have.
func ValidNodeName(name string) error {
	if !validNodeName.MatchString(name) {
		return fmt.Errorf("the node name %q is not one: 1-63 letters, digits, dots, hyphens or underscores, starting with a letter or digit", name)
	}
	return nil
}

// AgentJoinResult is what a join tells the operator.
type AgentJoinResult struct {
	NodeID, ClusterName string
	Managers            []config.ManagerRef
}

// ErrAlreadyClustered refuses a join on a server that is in a cluster
// already, as a manager or as an agent.
var ErrAlreadyClustered = errors.New("this server is already in a cluster")

// maxLeaderRedirects is how many times a join follows a manager that names
// the leader elsewhere: once for a command aimed at a follower, once more
// for a leader change in between.
const maxLeaderRedirects = 2

// Execute registers this server with the cluster at managerAddr. pin is the
// CA fingerprint from the join command, and the token travels only over a
// connection verified against it. advertise is where managers reach this
// server; empty means cluster.advertise_address, then the local address on
// the route to managerAddr. panelCA is the PEM of the CA behind this
// server's panel HTTPS certificate; the CLI prepares it so this package
// need not import paneltls.
//
// Nothing is written until the leader has registered the agent and its
// answer checks out. The files go first and the config last; a failure in
// between removes the files written. The config file is replaced in one
// rename, so a failure leaves it as it was and there are no old bytes to put
// back.
func (e *AgentJoinEngine) Execute(managerAddr, token, advertise, pin string, panelCA []byte) (*AgentJoinResult, error) {
	if e.Config.Cluster.Enabled {
		return nil, ErrAlreadyClustered
	}
	if e.ConfigPath == "" {
		return nil, errors.New("agent join: no config file to write the agent's config to")
	}
	pin, err := NormalizeCAPin(pin)
	if err != nil {
		return nil, err
	}
	key, keyPEM, csr, err := newAgentKey()
	if err != nil {
		return nil, err
	}
	grpcPort := e.Config.Cluster.GRPCPort
	if grpcPort == 0 {
		grpcPort = DefaultGRPCPort
	}
	advertise, err = e.advertiseAddress(advertise, managerAddr, grpcPort)
	if err != nil {
		return nil, err
	}
	if e.CheckPanelCert != nil {
		if err := e.CheckPanelCert(advertise); err != nil {
			return nil, err
		}
	}
	name := e.Name
	if name == "" {
		name = e.nodeName()
	}
	if err := ValidNodeName(name); err != nil {
		if e.Name == "" {
			return nil, fmt.Errorf("%w — it is this server's host name: pass --name with one that is", err)
		}
		return nil, err
	}

	slog.Info("registering with the cluster as an agent", "component", "agent",
		"manager", managerAddr, "advertise", advertise, "name", name)
	resp, leaderAddr, err := registerWithLeader(managerAddr, pin, &pb.RegisterAgentRequest{
		Token:       token,
		Csr:         csr,
		NodeName:    name,
		ApiAddress:  net.JoinHostPort(advertise, strconv.Itoa(e.Config.Server.Port)),
		GrpcAddress: net.JoinHostPort(advertise, strconv.Itoa(grpcPort)),
		PanelCaCert: panelCA,
		Version:     e.Version,
		NodeId:      e.NodeID,
	}, advertise)
	if err != nil {
		return nil, err
	}
	// A leader from before node_id ignores it and assigns a new ID: this
	// server would come back as another node, the history and alert scope
	// that name its ID left behind.
	if e.NodeID != "" && resp.NodeId != e.NodeID {
		return nil, registeredAnyway(resp.NodeId, name, fmt.Errorf("the leader registered this server as node %s, not under its own ID %s: every manager must be upgraded to this version first", resp.NodeId, e.NodeID))
	}

	// The leader has recorded the agent and spent the token: from here a
	// failure leaves that record behind, which the error says how to clear.
	ca, err := pinnedCA(resp.CaCert, pin)
	if err == nil {
		err = checkAgentCert(resp.NodeCert, ca, key, resp.NodeId)
	}
	if err != nil {
		return nil, registeredAnyway(resp.NodeId, name, err)
	}

	next := *e.Config
	next.Cluster.Enabled = true
	next.Cluster.Role = config.ClusterRoleAgent
	next.Cluster.Name = resp.ClusterName
	next.Cluster.NodeID = resp.NodeId
	next.Cluster.NodeName = name
	next.Cluster.AdvertiseAddress = advertise
	next.Cluster.GRPCPort = grpcPort
	next.Cluster.Managers = managerRefs(resp.Managers)
	if next.Cluster.CertDir == "" {
		next.Cluster.CertDir = DefaultCertDir
	}
	// A server that was a manager still has the cluster's JWT key in its
	// config; an agent's key is its own.
	next.Auth.JWTSecret = config.NewSecret()
	// The agent boots from this config with no fallback to a standalone
	// panel, so one that would not load is refused before anything is
	// written.
	if err := next.Validate(); err != nil {
		return nil, registeredAnyway(resp.NodeId, name, fmt.Errorf("the registration does not make a valid agent config: %w", err))
	}
	data, err := yaml.Marshal(&next)
	if err != nil {
		return nil, registeredAnyway(resp.NodeId, name, fmt.Errorf("encode the agent config: %w", err))
	}

	certDir := next.Cluster.CertDir
	written, err := writeAgentFiles(NewTLSManager(certDir), resp, keyPEM)
	if err != nil {
		removeAgentFiles(certDir, written)
		return nil, registeredAnyway(resp.NodeId, name, fmt.Errorf("write the agent's files: %w", err))
	}
	// replaceFile syncs before the rename: a config.yaml a crash left empty
	// would load as a new standalone panel, its setup screen open.
	if err := replaceFile(e.ConfigPath, data); err != nil {
		removeAgentFiles(certDir, written)
		return nil, registeredAnyway(resp.NodeId, name, fmt.Errorf("save the agent config: %w", err))
	}
	*e.Config = next

	slog.Info("joined the cluster as an agent", "component", "agent",
		"cluster", resp.ClusterName, "node_id", resp.NodeId, "leader", leaderAddr)
	return &AgentJoinResult{NodeID: resp.NodeId, ClusterName: resp.ClusterName, Managers: next.Cluster.Managers}, nil
}

// newAgentKey makes the agent's TLS key — ECDSA P-256, the only kind the
// leader signs (csrPublicKey) — and a certificate request for it. The
// request names nothing: the leader sets the name, role and address itself.
func newAgentKey() (key *ecdsa.PrivateKey, keyPEM, csrDER []byte, err error) {
	key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate the agent key: %w", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode the agent key: %w", err)
	}
	csrDER, err = x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("make the certificate request: %w", err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), csrDER, nil
}

// advertiseAddress is the address the agent registers at. It must be an IP
// of this server that managers can dial: the agent's certificate names it,
// and the leader takes nothing else. Its gRPC server will listen there at
// grpcPort, which must be free.
func (e *AgentJoinEngine) advertiseAddress(given, managerAddr string, grpcPort int) (string, error) {
	addr := given
	if addr == "" {
		addr = e.Config.Cluster.AdvertiseAddress
	}
	if addr == "" {
		detected, err := DetectAdvertiseAddress(managerAddr)
		if err != nil {
			return "", fmt.Errorf("cannot detect this server's address; pass --advertise: %w", err)
		}
		addr = detected
	}
	ip := net.ParseIP(addr)
	if ip == nil || ip.IsUnspecified() {
		return "", fmt.Errorf("the advertise address %q is not an IP address managers can reach this server at; an agent registers at one, which its certificate names", addr)
	}
	// An agent boots with its panel and gRPC listeners on this address
	// itself, not on every address: one this server does not hold — behind
	// NAT, or an old one the config kept — would fail at every boot.
	listenOn := e.listenOn
	if listenOn == nil {
		listenOn = canListenOn
	}
	if err := listenOn(ip, grpcPort); err != nil {
		// The leader would record an agent whose every boot then fails.
		if errors.Is(err, syscall.EADDRINUSE) {
			return "", fmt.Errorf("the cluster gRPC port %d is in use on %s, where the agent's gRPC server listens; stop what holds it, or set cluster.grpc_port to a free port: %w", grpcPort, ip, err)
		}
		return "", fmt.Errorf("the advertise address %s is not an address of this server, and an agent listens on the address it registers at; pass --advertise with one of this server's addresses: %w", ip, err)
	}
	return ip.String(), nil
}

func canListenOn(ip net.IP, port int) error {
	ln, err := net.Listen("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return ln.Close()
}

// nodeName is the name the agent registers under when it is given none: the
// host name or, when there is none, eight random hex digits in the shape of a
// node-ID prefix — the ID itself is the leader's to assign, after the name is
// sent.
func (e *AgentJoinEngine) nodeName() string {
	hostname := e.hostname
	if hostname == nil {
		hostname = os.Hostname
	}
	if name, err := hostname(); err == nil && name != "" {
		return name
	}
	name := uuid.New().String()[:8]
	slog.Warn("could not read the host name; registering under a generated one", "component", "agent", "name", name)
	return name
}

// registerWithLeader sends the registration over a connection verified
// against pin, to managerAddr or, when that manager is a follower, to the
// leader it names — dialled with the same pin. It returns the answer and the
// leader that gave it.
func registerWithLeader(managerAddr, pin string, req *pb.RegisterAgentRequest, advertise string) (*pb.RegisterAgentResponse, string, error) {
	target := managerAddr
	for redirects := 0; ; redirects++ {
		client, err := DialPinnedLeader(target, pin)
		if err != nil {
			return nil, "", err
		}
		resp, err := client.RegisterAgent(context.Background(), req)
		_ = client.Close()
		if status.Code(err) == codes.Unimplemented {
			return nil, "", fmt.Errorf("the manager at %s cannot register agents: every manager must be upgraded to this version first", target)
		}
		if err != nil {
			return nil, "", fmt.Errorf("registration request to %s failed: %w", target, err)
		}
		if resp.Success {
			return resp, target, nil
		}
		if resp.Error == ErrNotLeader.Error() && resp.LeaderGrpcAddress != "" {
			if redirects == maxLeaderRedirects {
				return nil, "", fmt.Errorf("registration rejected by %s: %s — the leadership moved during the join; run it again in a few seconds", target, resp.Error)
			}
			slog.Info("the manager is not the leader; registering with the leader it names", "component", "agent",
				"manager", target, "leader", resp.LeaderGrpcAddress)
			target = resp.LeaderGrpcAddress
			continue
		}
		return nil, "", fmt.Errorf("registration rejected by %s: %s", target, agentRefusalMessage(resp.Error, req.NodeName, advertise))
	}
}

// agentRefusalMessage words the leader's refusal of a registration for the
// operator. It keeps the leader's text, which names the refusal, and adds
// what to do. Not preFlightErrorMessage: that one words a manager join's,
// and its advice for ErrTokenKind is the opposite of this one's.
func agentRefusalMessage(errStr, name, advertise string) string {
	var hint string
	switch errStr {
	case ErrTokenNotFound.Error():
		// Tokens stay on the leader that made them; nothing replicates them.
		hint = "check it for typos; a token lives only on the leader that created it, so after a leader change create a new one with 'sfpanel cluster token --agent'"
	case ErrTokenExpired.Error():
		hint = "create a new one with 'sfpanel cluster token --agent'"
	case ErrTokenUsed.Error():
		hint = "a token admits one server; create a new one with 'sfpanel cluster token --agent'"
	case ErrTokenKind.Error():
		hint = "this is a manager token: create an agent token with 'sfpanel cluster token --agent', or join as a manager with 'sfpanel cluster join'"
	case ErrNotLeader.Error():
		hint = "the manager knows no leader yet (an election may be under way); run the join again in a few seconds"
	case ErrMaxAgentsReached.Error():
		hint = "remove an agent, or raise cluster.max_agents in the leader's config"
	case ErrNodeIdentityTaken.Error():
		hint = fmt.Sprintf("this server registers as %q at %s: pass --name with a name no other node has, or check --advertise; a node an earlier attempt left behind is removed on a manager with 'sfpanel cluster remove <node-id>'", name, advertise)
	default:
		// Shown as the leader worded it: the refusals that say what to do
		// already (ErrProxyRotationPending, ErrAgentPanelCARequired,
		// ErrCAKeyUnavailable), and the leader's own failures.
		return errStr
	}
	return errStr + " — " + hint
}

// pinnedCA parses the CA a registration answer carries and checks it is the
// one the join command pinned, as JoinEngine.Execute does with a join
// answer.
func pinnedCA(caPEM []byte, pin string) (*x509.Certificate, error) {
	block, _ := pem.Decode(caPEM)
	if block == nil {
		return nil, errors.New("the registration response carried no CA certificate")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil || CAPin(ca.RawSubjectPublicKeyInfo) != pin {
		return nil, errors.New("the CA in the registration response is not the pinned one")
	}
	return ca, nil
}

// checkAgentCert checks the certificate is the one the agent asked for:
// signed by the pinned CA, for this server's key, naming the node ID it came
// with, in the agent role. A wrong one would show only later — at the next
// boot (a key this server does not hold), in the heartbeats (a CA the
// managers do not trust; a CN the leader does not list, which reads as a
// removal) — or never: a manager's certificate is more than this server was
// admitted with. The validity period is not checked: the leader stamps it
// from its own clock.
func checkAgentCert(certPEM []byte, ca *x509.Certificate, key *ecdsa.PrivateKey, nodeID string) error {
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return errors.New("the registration response carried no certificate")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil || leaf.CheckSignatureFrom(ca) != nil || !key.PublicKey.Equal(leaf.PublicKey) ||
		leaf.Subject.CommonName != nodeID || CertRole(leaf) != CertRoleAgent {
		return errors.New("the certificate in the registration response is not this agent's")
	}
	return nil
}

// registeredAnyway words a failure that comes after the leader recorded the
// agent. The record stays — this server has no credential yet to remove it
// with — and its name and address refuse the next registration until it is
// removed on a manager.
func registeredAnyway(nodeID, name string, err error) error {
	if nodeID == "" {
		return fmt.Errorf("%w; the leader may have recorded this server, named %q: remove that node on a manager before joining again", err, name)
	}
	return fmt.Errorf("%w; the leader has already recorded this server as node %s: remove it on a manager ('sfpanel cluster remove %s') before joining again", err, nodeID, nodeID)
}

// writeAgentFiles writes the cluster CA, the agent's certificate and key and
// its internal-proxy key, and returns the files it wrote — on a failure, the
// ones it may have written part of too.
func writeAgentFiles(tm *TLSManager, resp *pb.RegisterAgentResponse, keyPEM []byte) (written []string, err error) {
	written = append(written, "ca.crt")
	if err := tm.SaveCACert(resp.CaCert); err != nil {
		return written, err
	}
	written = append(written, "node.crt", "node.key")
	if err := tm.SaveNodeCert(resp.NodeCert, keyPEM); err != nil {
		return written, err
	}
	written = append(written, "agent.key")
	return written, tm.SaveAgentKey(resp.AgentKey)
}

// removeAgentFiles takes back the files a failed join wrote. A file that
// will not go is logged: the config was not switched, so the server stays
// standalone either way, and the next join overwrites it.
func removeAgentFiles(certDir string, files []string) {
	for _, f := range files {
		if err := os.Remove(filepath.Join(certDir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("agent join rollback: a file was not removed", "component", "agent", "path", filepath.Join(certDir, f), "error", err)
		}
	}
}
