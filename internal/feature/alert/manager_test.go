package alert

import (
	"encoding/json"
	"strings"
	"testing"
)

// fakeNodeIdentity implements NodeIdentity for tests.
type fakeNodeIdentity struct{ id string }

func (f fakeNodeIdentity) LocalNodeID() string { return f.id }

// TestRuleAppliesToNode covers the four node_scope cases the manager has to
// reason about before evaluating a rule:
//
//  1. Single-node mode (identity is nil)         → always applies
//  2. Cluster + scope="all"                       → always applies
//  3. Cluster + scope="specific" + match          → applies
//  4. Cluster + scope="specific" + miss           → skipped
//
// Bug history: prior to this test, the manager loaded node_scope/node_ids
// from alert_rules but never consulted them, so every node in a cluster
// fired every rule — silently spamming the configured channels.
func TestRuleAppliesToNode(t *testing.T) {
	cases := []struct {
		name      string
		identity  NodeIdentity
		nodeScope string
		nodeIDs   []string
		want      bool
	}{
		{
			name:      "single node mode (nil identity) ignores scope",
			identity:  nil,
			nodeScope: "specific",
			nodeIDs:   []string{"other-node"},
			want:      true,
		},
		{
			name:      "cluster scope=all evaluates on every node",
			identity:  fakeNodeIdentity{id: "node-a"},
			nodeScope: "all",
			nodeIDs:   nil,
			want:      true,
		},
		{
			name:      "cluster scope empty (default) evaluates on every node",
			identity:  fakeNodeIdentity{id: "node-a"},
			nodeScope: "",
			nodeIDs:   nil,
			want:      true,
		},
		{
			name:      "cluster scope=specific evaluates when local node matches",
			identity:  fakeNodeIdentity{id: "node-a"},
			nodeScope: "specific",
			nodeIDs:   []string{"node-a", "node-b"},
			want:      true,
		},
		{
			name:      "cluster scope=specific skips when local node missing",
			identity:  fakeNodeIdentity{id: "node-c"},
			nodeScope: "specific",
			nodeIDs:   []string{"node-a", "node-b"},
			want:      false,
		},
		{
			name:      "cluster scope=specific with empty list skips everyone",
			identity:  fakeNodeIdentity{id: "node-a"},
			nodeScope: "specific",
			nodeIDs:   []string{},
			want:      false,
		},
		{
			name:      "cluster scope=specific with malformed list skips (fail-closed)",
			identity:  fakeNodeIdentity{id: "node-a"},
			nodeScope: "specific",
			nodeIDs:   nil, // signals "use raw bad JSON" — set below
			want:      false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var raw string
			if c.name == "cluster scope=specific with malformed list skips (fail-closed)" {
				raw = "not json"
			} else if c.nodeIDs == nil {
				raw = "[]"
			} else {
				b, _ := json.Marshal(c.nodeIDs)
				raw = string(b)
			}
			got := ruleAppliesToNode(c.identity, c.nodeScope, raw)
			if got != c.want {
				t.Fatalf("got %v, want %v (scope=%q ids=%q)", got, c.want, c.nodeScope, raw)
			}
		})
	}
}

// TestMaskChannelConfig covers the secret-masking applied to the
// /api/v1/alerts/channels list response. Bug history: ListChannels used to
// return the raw config JSON, exposing Discord webhook URLs and Telegram
// bot tokens in any browser session that hit the page.
func TestMaskChannelConfig(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantHas  []string // substrings that MUST appear (e.g. last-4 hint, non-secret keys)
		wantMiss []string // substrings that MUST NOT appear
	}{
		{
			name:     "discord webhook url masked but tail retained",
			in:       `{"webhook_url":"https://discord.com/api/webhooks/123456789/abcdefghijklmnop"}`,
			wantHas:  []string{"webhook_url", "mnop"},
			wantMiss: []string{"abcdefghij", "discord.com"},
		},
		{
			name: "telegram bot token + chat id masked",
			in:   `{"bot_token":"123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11","chat_id":"-1001234567890"}`,
			// Last 4 chars retained for identification; the rest stripped.
			wantHas:  []string{"bot_token", "chat_id", "ew11", "7890"},
			wantMiss: []string{"ABC-DEF1234", "123456:ABC"},
		},
		{
			name:     "unknown keys pass through",
			in:       `{"label":"production","color":"red"}`,
			wantHas:  []string{"production", "red"},
			wantMiss: []string{"***"},
		},
		{
			name:     "non-object input passes through unchanged",
			in:       `"not an object"`,
			wantHas:  []string{`"not an object"`},
			wantMiss: nil,
		},
		{
			name:     "malformed JSON returns marker, not original",
			in:       `{not json`,
			wantHas:  []string{"***"},
			wantMiss: []string{"not json"},
		},
		{
			name:     "short secret fully masked (no last-4 leak)",
			in:       `{"webhook_url":"abc"}`,
			wantHas:  []string{"webhook_url", "***"},
			wantMiss: []string{`"abc"`},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := maskChannelConfig(c.in)
			for _, s := range c.wantHas {
				if !strings.Contains(got, s) {
					t.Errorf("output missing %q: %s", s, got)
				}
			}
			for _, s := range c.wantMiss {
				if strings.Contains(got, s) {
					t.Errorf("output should NOT contain %q: %s", s, got)
				}
			}
		})
	}
}
