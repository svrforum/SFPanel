package cluster

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"strings"
	"testing"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func ctxWithPeerCert(cn, role string) context.Context {
	leaf := &x509.Certificate{Subject: pkix.Name{CommonName: cn}}
	if role != "" {
		leaf.Subject.OrganizationalUnit = []string{role}
	}
	return peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}},
	}})
}

func policyServer(t *testing.T) (*GRPCServer, *FSM) {
	t.Helper()
	fsm := NewFSM()
	addTestNode(t, fsm, Node{ID: "m1", Kind: KindManager, Role: RoleVoter})
	addTestNode(t, fsm, Node{ID: "m0", Role: RoleVoter}) // written before Kind existed
	addTestNode(t, fsm, Node{ID: "a1", Kind: KindAgent})
	return &GRPCServer{manager: &Manager{raft: &RaftNode{fsm: fsm}}}, fsm
}

const (
	methodProxy     = "/sfpanel.cluster.ClusterService/ProxyRequest"
	methodHeartbeat = "/sfpanel.cluster.ClusterService/Heartbeat"
)

func TestAuthorizeCaller(t *testing.T) {
	s, _ := policyServer(t)
	cases := []struct {
		name, method, cn, role string
		want                   codes.Code
	}{
		{"manager proxies", methodProxy, "m1", CertRoleManager, codes.OK},
		{"manager from before roles proxies", methodProxy, "m0", "", codes.OK},
		{"agent may not proxy", methodProxy, "a1", CertRoleAgent, codes.PermissionDenied},
		{"agent certificate under a manager's ID may not proxy", methodProxy, "m1", CertRoleAgent, codes.PermissionDenied},
		{"a stranger is refused", methodProxy, "x9", CertRoleManager, codes.PermissionDenied},
		{"an agent may heartbeat", methodHeartbeat, "a1", CertRoleAgent, codes.OK},
		{"an unlisted method is refused", "/sfpanel.cluster.ClusterService/Shiny", "m1", CertRoleManager, codes.PermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := s.authorizeCaller(ctxWithPeerCert(tc.cn, tc.role), tc.method)
			if got := status.Code(err); got != tc.want {
				t.Fatalf("code = %v (%v), want %v", got, err, tc.want)
			}
		})
	}
	if got := status.Code(s.authorizeCaller(context.Background(), methodProxy)); got != codes.Unauthenticated {
		t.Fatalf("no peer certificate: code = %v, want Unauthenticated", got)
	}
}

// A node that has just joined has an empty FSM until replication catches up:
// it refuses the leader for that moment, says why, and accepts once the
// leader's record arrives.
func TestAuthorizeCallerAcceptsOnceTheCallerIsReplicated(t *testing.T) {
	fsm := NewFSM()
	s := &GRPCServer{manager: &Manager{raft: &RaftNode{fsm: fsm}}}
	err := s.authorizeCaller(ctxWithPeerCert("leader", CertRoleManager), methodProxy)
	if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "not a member of this cluster") {
		t.Fatalf("before replication: %v", err)
	}
	addTestNode(t, fsm, Node{ID: "leader", Kind: KindManager, Role: RoleVoter})
	if err := s.authorizeCaller(ctxWithPeerCert("leader", CertRoleManager), methodProxy); err != nil {
		t.Fatalf("after replication: %v", err)
	}
}

// A member may take only itself out of the cluster.
func TestLeaveRefusesAnotherNode(t *testing.T) {
	s, _ := policyServer(t)
	_, err := s.Leave(ctxWithPeerCert("m1", CertRoleManager), &pb.LeaveRequest{NodeId: "m0"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("Leave(m0) from m1: %v, want PermissionDenied", err)
	}
}
