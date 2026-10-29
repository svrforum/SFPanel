package cluster

import (
	"slices"
	"testing"
	"time"
)

// Two real managers move to a new internal proxy secret: the follower takes
// the next one from its own FSM and says so on its heartbeat, which is what
// lets the leader switch, and both then sign with it alone. The secrets each
// manager installed are read from it — the process-wide auth state is shared
// by both in this test.
func TestTwoManagersRotateTheirProxySecret(t *testing.T) {
	m1, addr, token, pin := realLeader(t)
	settleRotation(t, m1)
	m2 := joinedManager(t, addr, token, pin)
	id2 := m2.LocalNodeID()
	waitForVoter(t, m1, id2)
	applied := func(m *Manager) (string, []string) {
		m.appliedSecretsMu.Lock()
		defer m.appliedSecretsMu.Unlock()
		return m.appliedSign, slices.Clone(m.appliedAccept)
	}
	waitUntil(t, 60*time.Second, "m2's heartbeat to say it follows a re-rotation", func() bool {
		met := m1.GetHeartbeat().GetMetrics(id2)
		return met != nil && slices.Contains(met.Capabilities, CapProxyRotation) && met.ProxyGeneration != nil
	})

	gen, err := m1.StartProxyRotation()
	if err != nil {
		t.Fatal(err)
	}
	k := proxyRecord(t, m1)
	if k.Generation != gen {
		t.Fatalf("started generation %d, the record holds %d", gen, k.Generation)
	}
	// m2 applies the record as its FSM changes; the switch waits for its
	// next heartbeat, thirty seconds apart, so this is seen before it.
	waitUntil(t, 5*time.Second, "m2 to accept the next secret and sign with the current one", func() bool {
		sign, accept := applied(m2)
		return sign == k.Previous && slices.Equal(accept, []string{k.Previous, k.Secret})
	})
	waitUntil(t, 90*time.Second, "the switch", func() bool {
		_ = m1.MaybeAdvanceProxyRotation()
		return proxyRecord(t, m1).Phase() == ProxyPhaseSwitched
	})
	waitUntil(t, 10*time.Second, "m2 to sign with the new secret", func() bool {
		sign, _ := applied(m2)
		return sign == k.Secret
	})
	if err := m1.advanceProxyRotation(time.Now().Add(proxyRotationSettled)); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, "m2 to accept the new secret alone", func() bool {
		sign, accept := applied(m2)
		return sign == k.Secret && slices.Equal(accept, []string{k.Secret})
	})
}
