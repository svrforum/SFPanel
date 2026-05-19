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

func TestWouldLockOutOnAdd(t *testing.T) {
	const panelPort = 9443
	cases := []struct {
		name string
		req  AddRuleRequest
		want bool
	}{
		{"allow ssh", AddRuleRequest{Action: "allow", Port: "22"}, false},
		{"deny ssh", AddRuleRequest{Action: "deny", Port: "22"}, true},
		{"reject ssh", AddRuleRequest{Action: "reject", Port: "22"}, true},
		{"limit ssh", AddRuleRequest{Action: "limit", Port: "22"}, true},
		{"deny ssh with tcp proto in port", AddRuleRequest{Action: "deny", Port: "22/tcp"}, true},
		{"deny panel port", AddRuleRequest{Action: "deny", Port: "9443"}, true},
		{"deny other port", AddRuleRequest{Action: "deny", Port: "8080"}, false},
		{"deny range covering 22", AddRuleRequest{Action: "deny", Port: "20:30"}, true},
		{"deny range covering panel", AddRuleRequest{Action: "deny", Port: "9000:9500"}, true},
		{"deny range missing both", AddRuleRequest{Action: "deny", Port: "8000:8080"}, false},
		{"deny OpenSSH app profile", AddRuleRequest{Action: "deny", Port: "OpenSSH"}, true},
		{"allow with range", AddRuleRequest{Action: "allow", Port: "20:30"}, false},
		{"empty action benign", AddRuleRequest{Port: "22"}, false},
		{"deny empty port", AddRuleRequest{Action: "deny", Port: ""}, false},
		{"deny garbage port", AddRuleRequest{Action: "deny", Port: "abc"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := wouldLockOutOnAdd(tc.req, panelPort)
			if got != tc.want {
				t.Errorf("wouldLockOutOnAdd(%+v) = %v, want %v", tc.req, got, tc.want)
			}
		})
	}
}
