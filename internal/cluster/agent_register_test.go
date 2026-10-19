package cluster

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/svrforum/SFPanel/internal/auth"
	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"google.golang.org/protobuf/proto"
)

// settleRotation makes the leader's replicated state look like a cluster that
// switched proxy secrets long ago, which RegisterAgent requires.
func settleRotation(t *testing.T, m *Manager) {
	t.Helper()
	// The FSM change installs the replicated secret process-wide.
	t.Cleanup(func() { auth.SetClusterProxySecret("") })
	if err := m.SetConfig(configKeyProxySecret, "replicated-secret"); err != nil {
		t.Fatal(err)
	}
	if err := m.SetConfig(configKeyProxyRotatedAt, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
}

func agentCSR(t *testing.T) (csrDER []byte, key *ecdsa.PrivateKey) {
	t.Helper()
	key, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:     pkix.Name{CommonName: "the-leader", OrganizationalUnit: []string{CertRoleManager}}, // ignored by the leader
		IPAddresses: []net.IP{net.ParseIP("198.51.100.99")},                                             // ignored too
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return der, key
}

func panelCAPEM(t *testing.T) []byte {
	t.Helper()
	_, ca := setupCertDir(t)
	p, err := ca.LoadCACert()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func registerInput(t *testing.T, token string) RegisterAgentInput {
	t.Helper()
	csr, _ := agentCSR(t)
	return RegisterAgentInput{Token: token, CSR: csr, NodeName: "agent-host", APIAddress: "192.0.2.10:3628", GRPCAddress: "192.0.2.10:3629", PanelCA: panelCAPEM(t), Version: "test"}
}

func TestRegisterAgent(t *testing.T) {
	mgr, addr, managerToken, _ := realLeader(t)
	settleRotation(t, mgr)
	tok, err := mgr.CreateJoinToken(time.Hour, KindAgent)
	if err != nil {
		t.Fatal(err)
	}
	res, err := mgr.HandleRegisterAgent(registerInput(t, tok.Token))
	if err != nil {
		t.Fatal(err)
	}
	leaf := parseLeaf(t, res.NodeCert)
	if CertRole(leaf) != CertRoleAgent || leaf.Subject.CommonName != res.NodeID || res.NodeID == "the-leader" {
		t.Fatalf("certificate: role %q CN %q", CertRole(leaf), leaf.Subject.CommonName)
	}
	if len(leaf.IPAddresses) != 1 || leaf.IPAddresses[0].String() != "192.0.2.10" || len(leaf.DNSNames) != 0 {
		t.Fatalf("SANs must be the registered address only: %v %v", leaf.IPAddresses, leaf.DNSNames)
	}
	node := mgr.raft.GetFSM().GetNode(res.NodeID)
	if node == nil || node.Kind != KindAgent || node.Role == RoleVoter {
		t.Fatalf("FSM record: %+v", node)
	}
	if k, _ := mgr.raft.GetFSM().GetConfigValue(AgentKeyConfigKey(res.NodeID)); k == "" || k != res.AgentKey {
		t.Fatal("agent key not in replicated state")
	}
	if ep, err := mgr.PanelEndpointChecked(node); err != nil || ep.TLSConfig == nil {
		t.Fatalf("panel CA not stored: %v", err)
	}
	if len(res.Managers) != 1 || res.Managers[0].Id != mgr.LocalNodeID() {
		t.Fatalf("managers %+v", res.Managers)
	}
	if _, err := mgr.tokens.Peek(tok.Token); err == nil {
		t.Fatal("token not consumed")
	}
	// The response carries the agent's own key and nothing the managers share.
	wire, _ := proto.Marshal(&pb.RegisterAgentResponse{NodeCert: res.NodeCert, CaCert: res.CACert, AgentKey: res.AgentKey, Managers: res.Managers})
	jwt, _, _ := mgr.GetJWTAndAdmin()
	for _, secret := range []string{"replicated-secret", jwt} {
		if secret != "" && bytes.Contains(wire, []byte(secret)) {
			t.Fatal("the registration response carries a managers' secret")
		}
	}
	f := mgr.raft.raft.GetConfiguration()
	_ = f.Error()
	for _, s := range f.Configuration().Servers {
		if string(s.ID) == res.NodeID {
			t.Fatal("the agent entered the Raft configuration")
		}
	}

	// The RPC needs no client certificate — the agent has none yet — and
	// carries the result, or the refusal's text, which the joining side
	// matches on.
	t.Run("over the wire", func(t *testing.T) {
		caKey, err := mgr.CAPublicKey()
		if err != nil {
			t.Fatal(err)
		}
		c, err := DialNodePinned(addr, caKey)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		tok, err := mgr.CreateJoinToken(time.Hour, KindAgent)
		if err != nil {
			t.Fatal(err)
		}
		in := registerInput(t, tok.Token)
		req := &pb.RegisterAgentRequest{Token: in.Token, Csr: in.CSR, NodeName: "agent-wire", ApiAddress: "192.0.2.20:3628", GrpcAddress: "192.0.2.20:3629", PanelCaCert: in.PanelCA, Version: in.Version}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		resp, err := c.client.RegisterAgent(ctx, req)
		if err != nil || !resp.Success {
			t.Fatalf("register: %v %+v", err, resp)
		}
		key, _ := mgr.raft.GetFSM().GetConfigValue(AgentKeyConfigKey(resp.NodeId))
		caPEM, _ := mgr.tls.LoadCACert()
		if node := mgr.raft.GetFSM().GetNode(resp.NodeId); node == nil || node.Kind != KindAgent {
			t.Fatalf("FSM record: %+v", node)
		}
		if key == "" || resp.AgentKey != key || !bytes.Equal(resp.CaCert, caPEM) || resp.ClusterName != "victim" ||
			len(resp.Managers) != 1 || CertRole(parseLeaf(t, resp.NodeCert)) != CertRoleAgent {
			t.Fatalf("response: %+v", resp)
		}

		req.Token = managerToken
		if resp, err := c.client.RegisterAgent(ctx, req); err != nil || resp.Success || resp.Error != ErrTokenKind.Error() {
			t.Fatalf("manager token: %v %+v", err, resp)
		}
		follower := &GRPCServer{manager: &Manager{raft: &RaftNode{fsm: NewFSM()}}}
		if resp, err := follower.RegisterAgent(ctx, req); err != nil || resp.Success || resp.Error != ErrNotLeader.Error() {
			t.Fatalf("a manager that is not the leader: %v %+v", err, resp)
		}
	})

	// A registration that fails after its token is spent takes back what it
	// wrote and puts the token back.
	t.Run("rollback", func(t *testing.T) {
		tok, err := mgr.CreateJoinToken(time.Hour, KindAgent)
		if err != nil {
			t.Fatal(err)
		}
		used, err := mgr.tokens.Validate(tok.Token, KindAgent)
		if err != nil {
			t.Fatal(err)
		}
		const id = "agent-rolled-back"
		if err := mgr.SetConfig(AgentKeyConfigKey(id), "key"); err != nil {
			t.Fatal(err)
		}
		if err := mgr.raft.Apply(Command{Type: CmdAddNode, Value: mustJSON(Node{ID: id, Name: "rolled-back", Kind: KindAgent})}, 5*time.Second); err != nil {
			t.Fatal(err)
		}
		if err := mgr.SetPanelCA(id, string(panelCAPEM(t))); err != nil {
			t.Fatal(err)
		}
		mgr.undoRegisterAgent(id, used)
		fsm := mgr.raft.GetFSM()
		if _, ok := fsm.GetConfigValue(AgentKeyConfigKey(id)); ok {
			t.Fatal("agent key kept")
		}
		if fsm.GetNode(id) != nil {
			t.Fatal("record kept")
		}
		if mgr.NodeServesTLS(id) {
			t.Fatal("panel CA kept")
		}
		if _, err := mgr.tokens.Peek(tok.Token); err != nil {
			t.Fatalf("token not put back: %v", err)
		}
	})
}

// Every refusal says why and leaves the token usable.
func TestRegisterAgentRefusals(t *testing.T) {
	mgr, _, managerToken, _ := realLeader(t)

	// The RPC takes callers with no certificate; without a token they must
	// not get as far as the barrier, a Raft entry on every voter.
	t.Run("no token", func(t *testing.T) {
		before := mgr.raft.raft.LastIndex()
		if _, err := mgr.HandleRegisterAgent(RegisterAgentInput{Token: "not-a-token"}); !errors.Is(err, ErrTokenNotFound) {
			t.Fatalf("got %v", err)
		}
		if after := mgr.raft.raft.LastIndex(); after != before {
			t.Fatalf("a caller without a token wrote to the Raft log: index %d → %d", before, after)
		}
	})

	t.Run("before the proxy secret has settled", func(t *testing.T) {
		tok, _ := mgr.CreateJoinToken(time.Hour, KindAgent)
		if _, err := mgr.HandleRegisterAgent(registerInput(t, tok.Token)); !errors.Is(err, ErrProxyRotationPending) {
			t.Fatalf("got %v", err)
		}
		if _, err := mgr.tokens.Peek(tok.Token); err != nil {
			t.Fatal("token consumed")
		}
	})
	settleRotation(t, mgr)

	cases := []struct {
		name string
		mut  func(*RegisterAgentInput)
		tok  string
		want error
	}{
		{"manager token", nil, managerToken, ErrTokenKind},
		{"no panel CA", func(in *RegisterAgentInput) { in.PanelCA = nil }, "", ErrAgentPanelCARequired},
		{"forged CSR signature", func(in *RegisterAgentInput) { in.CSR[len(in.CSR)-3] ^= 0xff }, "", ErrBadCSR},
		{"ed25519 key", func(in *RegisterAgentInput) {
			_, k, _ := ed25519.GenerateKey(rand.Reader)
			in.CSR, _ = x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, k)
		}, "", ErrBadCSR},
		{"P-384 key", func(in *RegisterAgentInput) {
			k, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
			in.CSR, _ = x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, k)
		}, "", ErrBadCSR},
		{"the leader's name", func(in *RegisterAgentInput) { in.NodeName = mgr.raft.GetFSM().GetNode(mgr.LocalNodeID()).Name }, "", ErrNodeIdentityTaken},
		{"the leader's address", func(in *RegisterAgentInput) {
			in.APIAddress, in.GRPCAddress = "127.0.0.1:3628", "127.0.0.1:3629"
		}, "", ErrNodeIdentityTaken},
		// The same address in another spelling: its certificate would name it.
		{"the leader's address, IPv4-mapped", func(in *RegisterAgentInput) {
			in.APIAddress, in.GRPCAddress = "[::ffff:127.0.0.1]:3628", "[::ffff:127.0.0.1]:3629"
		}, "", ErrNodeIdentityTaken},
		{"no name", func(in *RegisterAgentInput) { in.NodeName = "" }, "", errAgentName},
		// The certificate names the panel's host; a gRPC address elsewhere
		// would be an address nothing checked.
		{"api and grpc on different hosts", func(in *RegisterAgentInput) { in.GRPCAddress = "192.0.2.11:3629" }, "", errAgentAddress},
		{"a host name for an address", func(in *RegisterAgentInput) {
			in.APIAddress, in.GRPCAddress = "agent.example:3628", "agent.example:3629"
		}, "", errAgentAddress},
		{"a port that is not one", func(in *RegisterAgentInput) { in.APIAddress = "192.0.2.10:0" }, "", errAgentAddress},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok := tc.tok
			if tok == "" {
				jt, err := mgr.CreateJoinToken(time.Hour, KindAgent)
				if err != nil {
					t.Fatal(err)
				}
				tok = jt.Token
			}
			in := registerInput(t, tok)
			if tc.mut != nil {
				tc.mut(&in)
			}
			if _, err := mgr.HandleRegisterAgent(in); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if _, err := mgr.tokens.Peek(tok); err != nil {
				t.Fatalf("the refusal consumed the token: %v", err)
			}
		})
	}

	t.Run("agent cap", func(t *testing.T) {
		mgr.config.MaxAgents = 1
		t.Cleanup(func() { mgr.config.MaxAgents = 0 })
		first, err := mgr.CreateJoinToken(time.Hour, KindAgent)
		if err != nil {
			t.Fatal(err)
		}
		// Minted while a place was free; registration checks again.
		second, err := mgr.CreateJoinToken(time.Hour, KindAgent)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mgr.HandleRegisterAgent(registerInput(t, first.Token)); err != nil {
			t.Fatal(err)
		}
		// CreateJoinToken itself refuses once the cap is reached.
		if _, err := mgr.CreateJoinToken(time.Hour, KindAgent); !errors.Is(err, ErrMaxAgentsReached) {
			t.Fatalf("got %v", err)
		}
		in := registerInput(t, second.Token)
		in.NodeName, in.APIAddress, in.GRPCAddress = "agent-host-2", "192.0.2.11:3628", "192.0.2.11:3629"
		if _, err := mgr.HandleRegisterAgent(in); !errors.Is(err, ErrMaxAgentsReached) {
			t.Fatalf("a token minted before the cap was reached: %v", err)
		}
		if _, err := mgr.tokens.Peek(second.Token); err != nil {
			t.Fatalf("the refusal consumed the token: %v", err)
		}
	})
}

// Registrations run one at a time: two at once could otherwise both pass the
// identity check before either is recorded, and two agents would hold
// certificates for one address.
func TestConcurrentRegistrationsTakeAnIdentityOnce(t *testing.T) {
	mgr, _, _, _ := realLeader(t)
	settleRotation(t, mgr)
	const n = 8
	inputs := make([]RegisterAgentInput, n)
	for i := range inputs {
		tok, err := mgr.CreateJoinToken(time.Hour, KindAgent)
		if err != nil {
			t.Fatal(err)
		}
		inputs[i] = registerInput(t, tok.Token) // one name and address for all
	}
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range inputs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = mgr.HandleRegisterAgent(inputs[i])
		}()
	}
	wg.Wait()
	registered := 0
	for _, err := range errs {
		switch {
		case err == nil:
			registered++
		case !errors.Is(err, ErrNodeIdentityTaken):
			t.Fatalf("unexpected refusal: %v", err)
		}
	}
	if agents := countKind(mgr.raft.GetFSM().GetState(), KindAgent); registered != 1 || agents != 1 {
		t.Fatalf("%d registrations succeeded and %d agents are recorded under one name and address", registered, agents)
	}
}
