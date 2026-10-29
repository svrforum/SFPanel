package cluster

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	pb "github.com/svrforum/SFPanel/internal/cluster/proto"
	"google.golang.org/protobuf/proto"
)

// The leader keeps what a ping says for a rotation, and which CA signed the
// certificate the stream's connection presented — what the rotation goes by,
// not what the node says of itself.
func TestTheLeaderRecordsWhatARotationGoesBy(t *testing.T) {
	mgr, addr, _, _ := realLeader(t)
	res, tm := registeredAgent(t, mgr)
	client, err := DialManager(addr, tm, func() []string { return []string{mgr.LocalNodeID()} })
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := client.OpenAgentHeartbeat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&pb.HeartbeatPing{NodeId: res.NodeID, Timestamp: time.Now().Unix(), Version: "test",
		CaTrustPins: []string{"sha256:said"}, JwtGeneration: proto.Int64(4)}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	met := mgr.GetHeartbeat().GetMetrics(res.NodeID)
	leaderCA := mgr.GetTLS().TrustPins()[0]
	if met == nil || !slices.Equal(met.CATrustPins, []string{"sha256:said"}) || met.JWTGeneration == nil || *met.JWTGeneration != 4 || met.CertCAPin != leaderCA {
		t.Fatalf("recorded %+v, want the agent's word and the CA %s behind its connection", met, leaderCA)
	}
	// Compared with the same metrics without them, not searched for a pin:
	// the generation is a bare number.
	data, _ := json.Marshal(met)
	without := *met
	without.JWTGeneration, without.CATrustPins, without.CertCAPin = nil, nil, ""
	if want, _ := json.Marshal(&without); strings.Contains(string(data), "sha256:") || string(data) != string(want) {
		t.Fatalf("the metrics show the rotation's fields: %s", data)
	}
}
