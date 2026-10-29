package auth

import "testing"

// A manager's word that it checked the operator's password is good for the
// one request it rides on — that request's internal-proxy header, which is
// refused if replayed, and its body — and only from a holder of the agent's
// key.
func TestStepUpProofBindsItsRequest(t *testing.T) {
	t.Cleanup(func() { SetProxySecrets("", nil) })
	SetProxySecrets("agent-key", []string{"agent-key"})
	proof := StepUpProofWith("agent-key", "1700000000000:aa:bb", []byte(`{"advanced":true}`))
	if !VerifyStepUpProof("1700000000000:aa:bb", []byte(`{"advanced":true}`), proof) {
		t.Fatal("the proof for this request and body was refused")
	}
	for name, ok := range map[string]bool{
		"another request": VerifyStepUpProof("1700000000001:cc:dd", []byte(`{"advanced":true}`), proof),
		"another body":    VerifyStepUpProof("1700000000000:aa:bb", []byte(`{"advanced":true,"compose":"x"}`), proof),
		"another key":     VerifyStepUpProof("1700000000000:aa:bb", []byte(`{"advanced":true}`), StepUpProofWith("managers-secret", "1700000000000:aa:bb", []byte(`{"advanced":true}`))),
		"no proof":        VerifyStepUpProof("1700000000000:aa:bb", []byte(`{"advanced":true}`), ""),
		"no request":      VerifyStepUpProof("", []byte(`{"advanced":true}`), StepUpProofWith("agent-key", "", []byte(`{"advanced":true}`))),
	} {
		if ok {
			t.Errorf("%s: accepted", name)
		}
	}
	// A node holding no key takes no word, not one anybody can make.
	SetProxySecrets("", nil)
	if VerifyStepUpProof("1700000000000:aa:bb", []byte(`{"advanced":true}`), StepUpProofWith("", "1700000000000:aa:bb", []byte(`{"advanced":true}`))) {
		t.Error("no key here: accepted")
	}
}

// While an agent moves to a new key its managers may sign with either, and a
// step-up made with either goes through; once the old key is gone, so is its
// word.
func TestStepUpProofTakesEveryKeyTheAgentAccepts(t *testing.T) {
	t.Cleanup(func() { SetProxySecrets("", nil) })
	header, body := "1700000000000:aa:bb", []byte(`{"advanced":true}`)
	SetProxySecrets("new-key", []string{"new-key", "old-key"})
	if !VerifyStepUpProof(header, body, StepUpProofWith("old-key", header, body)) {
		t.Fatal("a step-up made with a key the agent still accepts was refused")
	}
	SetProxySecrets("new-key", []string{"new-key"})
	if VerifyStepUpProof(header, body, StepUpProofWith("old-key", header, body)) {
		t.Fatal("a step-up made with a key the agent no longer accepts went through")
	}
}
