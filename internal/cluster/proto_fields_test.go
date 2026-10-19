package cluster

import (
	"testing"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
)

// The two fields plan A puts on the wire: the CA public key a joining node
// pins, and the capabilities a node reports on its heartbeat.
func TestProtoCarriesPinAndCapabilities(t *testing.T) {
	pf := &pb.PreFlightResponse{CaPublicKey: []byte{1, 2}}
	if len(pf.GetCaPublicKey()) != 2 {
		t.Fatal("PreFlightResponse lost ca_public_key")
	}
	ping := &pb.HeartbeatPing{Capabilities: []string{"x"}}
	if len(ping.GetCapabilities()) != 1 {
		t.Fatal("HeartbeatPing lost capabilities")
	}
}

// The wire shapes plan B adds: agent registration, the agent's panel CA
// report, and the pong fields an agent follows the leader with.
func TestProtoCarriesAgentRegistration(t *testing.T) {
	req := &pb.RegisterAgentRequest{Token: "t", Csr: []byte{1}, NodeName: "a", ApiAddress: "192.0.2.10:3628", GrpcAddress: "192.0.2.10:3629", PanelCaCert: []byte{2}, Version: "v"}
	resp := &pb.RegisterAgentResponse{Success: true, NodeId: "id", NodeCert: []byte{3}, CaCert: []byte{4}, AgentKey: "k", Managers: []*pb.NodeInfo{{Id: "m1", GrpcAddress: "192.0.2.1:3629", Kind: "manager"}}, ClusterName: "c", LeaderGrpcAddress: "192.0.2.1:3629"}
	rep := &pb.ReportPanelCARequest{CaCert: []byte{5}}
	repResp := &pb.ReportPanelCAResponse{Success: true, LeaderGrpcAddress: "192.0.2.1:3629"}
	pong := &pb.HeartbeatPong{LeaderId: "m1", LeaderGrpcAddress: "192.0.2.1:3629", Managers: resp.Managers, FromLeader: true}
	if req.GetPanelCaCert() == nil || resp.GetAgentKey() != "k" || rep.GetCaCert() == nil || !repResp.GetSuccess() || !pong.GetFromLeader() || pong.GetManagers()[0].GetKind() != "manager" {
		t.Fatal("agent registration fields did not round-trip")
	}
}
