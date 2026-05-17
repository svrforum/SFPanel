package firewall

import "testing"

func TestRuleAllowsPort(t *testing.T) {
	cases := []struct {
		name string
		rule UFWRule
		port int
		want bool
	}{
		{"allow 22 tcp", UFWRule{Action: "ALLOW", To: "22/tcp"}, 22, true},
		{"allow 22 (no proto)", UFWRule{Action: "ALLOW", To: "22"}, 22, true},
		{"allow 3628 tcp", UFWRule{Action: "ALLOW", To: "3628/tcp"}, 3628, true},
		{"allow IN port", UFWRule{Action: "ALLOW IN", To: "22/tcp"}, 22, true},
		{"deny 22", UFWRule{Action: "DENY", To: "22/tcp"}, 22, false},
		{"allow different port", UFWRule{Action: "ALLOW", To: "80/tcp"}, 22, false},
		{"allow service name openssh", UFWRule{Action: "ALLOW", To: "OpenSSH"}, 22, true},   // 'OpenSSH' app profile maps to 22
		{"empty to", UFWRule{Action: "ALLOW", To: ""}, 22, false},
		{"port range covers", UFWRule{Action: "ALLOW", To: "20:25/tcp"}, 22, true},
		{"port range outside", UFWRule{Action: "ALLOW", To: "100:200/tcp"}, 22, false},
	}
	for _, c := range cases {
		got := ruleAllowsPort(c.rule, c.port)
		if got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestHasAccessRule(t *testing.T) {
	rulesSSHOnly := []UFWRule{{Action: "ALLOW", To: "22/tcp"}}
	rulesPanelOnly := []UFWRule{{Action: "ALLOW", To: "3628/tcp"}}
	rulesNone := []UFWRule{{Action: "ALLOW", To: "80/tcp"}, {Action: "DENY", To: "22/tcp"}}
	rulesBoth := []UFWRule{{Action: "ALLOW", To: "22/tcp"}, {Action: "ALLOW", To: "3628/tcp"}}

	if !hasAccessRule(rulesSSHOnly, 3628) {
		t.Error("SSH rule alone should be enough")
	}
	if !hasAccessRule(rulesPanelOnly, 3628) {
		t.Error("Panel-port rule alone should be enough")
	}
	if hasAccessRule(rulesNone, 3628) {
		t.Error("No matching ALLOW should fail-closed")
	}
	if !hasAccessRule(rulesBoth, 3628) {
		t.Error("Both rules present should pass")
	}
}
