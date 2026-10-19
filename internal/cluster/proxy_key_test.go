package cluster

import (
	"errors"
	"net/http"
	"testing"

	"github.com/svrforum/SFPanel/internal/auth"
)

// To a manager: the managers' secret. To an agent: that agent's key, which no
// manager accepts — and never the managers' secret, which every manager does.
func TestSignProxyRequestFor(t *testing.T) {
	t.Cleanup(func() { auth.SetProxySecrets("", nil) })
	fsm := fsmWithConfig(t, map[string]string{AgentKeyConfigKey("a1"): "agent-one-key"})
	m := &Manager{raft: &RaftNode{fsm: fsm}}
	auth.SetProxySecrets("manager-secret", []string{"manager-secret"})
	path := "/api/v1/system/info"

	mh, err := m.SignProxyRequestFor(&Node{ID: "m2", Kind: KindManager}, http.MethodGet, path)
	if err != nil {
		t.Fatal(err)
	}
	if !accepts("manager-secret", mh, path) {
		t.Fatal("manager target: not signed with the managers' secret")
	}
	ah, err := m.SignProxyRequestFor(&Node{ID: "a1", Kind: KindAgent}, http.MethodGet, path)
	if err != nil {
		t.Fatal(err)
	}
	if !accepts("agent-one-key", ah, path) {
		t.Fatal("agent target: not signed with its key")
	}
	if accepts("manager-secret", ah, path) {
		t.Fatal("agent target: a manager accepts the header — it is a manager credential")
	}
	if _, err := m.SignProxyRequestFor(&Node{ID: "a2", Kind: KindAgent}, http.MethodGet, path); !errors.Is(err, ErrNoAgentKey) {
		t.Fatalf("agent without a key: %v", err)
	}
}

// accepts reports whether a node accepting only secret takes header.
func accepts(secret, header, path string) bool {
	auth.SetProxySecrets(secret, []string{secret})
	r, _ := http.NewRequest(http.MethodGet, path, nil)
	r.Header.Set(auth.InternalProxyHeaderV2, header)
	return auth.IsInternalProxyRequest(r)
}

// Relays to an agent need its panel CA: without it the agent key and the
// request body would cross the network in clear.
func TestPanelEndpointCheckedRefusesAnInsecureAgent(t *testing.T) {
	m := &Manager{raft: &RaftNode{fsm: NewFSM()}}
	if _, err := m.PanelEndpointChecked(&Node{ID: "a1", Kind: KindAgent, APIAddress: "192.0.2.10:3628"}); !errors.Is(err, ErrAgentPanelInsecure) {
		t.Fatalf("agent without a panel CA: %v", err)
	}
	if ep, err := m.PanelEndpointChecked(&Node{ID: "m2", Kind: KindManager, APIAddress: "192.0.2.2:3628"}); err != nil || ep.BaseURL == "" {
		t.Fatalf("a manager without TLS keeps working over http: %v %+v", err, ep)
	}
}
