package cluster

import (
	"errors"

	"github.com/svrforum/SFPanel/internal/auth"
)

const agentKeyPrefix = "agent_key/"

// AgentKeyConfigKey is where the managers' replicated state keeps an agent's
// internal-proxy key. Only managers hold it; the agent holds its own.
func AgentKeyConfigKey(nodeID string) string { return agentKeyPrefix + nodeID }

var (
	errNoProxySecret = errors.New("no internal-proxy secret installed")
	errSignFailed    = errors.New("could not sign the internal request")
)

// SignProxyRequestFor returns the internal-proxy header to send to node.
//
// A header signed with the managers' secret is accepted once by every manager
// within the clock-skew window, so it is a credential: it may go to managers
// only. An agent gets a header made with its own key, which no manager
// accepts; with no key for it there is no header and the caller must not send.
func (m *Manager) SignProxyRequestFor(node *Node, method, path string) (string, error) {
	key, err := m.proxyKeyFor(node)
	if err != nil {
		return "", err
	}
	if v := auth.SignProxyRequestV2With(key, method, path); v != "" {
		return v, nil
	}
	return "", errSignFailed
}

// StepUpProofFor is a manager's word to an agent that it checked the
// operator's password for the request signed with proxyHeader, carrying body
// (auth.StepUpHeader). It is made with that agent's key; a manager checks a
// password itself and is never sent one.
func (m *Manager) StepUpProofFor(node *Node, proxyHeader string, body []byte) (string, error) {
	if node == nil || node.IsManager() {
		return "", errors.New("a step-up proof is for an agent only")
	}
	key, err := m.proxyKeyFor(node)
	if err != nil {
		return "", err
	}
	return auth.StepUpProofWith(key, proxyHeader, body), nil
}

// CanSignFor reports what SignProxyRequestFor would refuse for node, without
// signing anything. The WebSocket relay must know before it upgrades the
// browser's connection, and the path it signs is settled only after.
func (m *Manager) CanSignFor(node *Node) error {
	_, err := m.proxyKeyFor(node)
	return err
}

// proxyKeyFor is the key a request to node is signed with. Which one depends
// on the node's kind in replicated state, never on anything the request says.
func (m *Manager) proxyKeyFor(node *Node) (string, error) {
	if node == nil {
		return "", ErrNodeNotFound
	}
	if node.IsManager() {
		if secret := auth.ClusterProxySecret(); secret != "" {
			return secret, nil
		}
		return "", errNoProxySecret
	}
	var key string
	if m != nil && m.raft != nil {
		key, _ = m.raft.GetFSM().GetConfigValue(AgentKeyConfigKey(node.ID))
	}
	if key == "" {
		return "", ErrNoAgentKey
	}
	return key, nil
}
