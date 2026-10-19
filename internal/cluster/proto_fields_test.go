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
