package composex

import (
	"strings"
	"testing"
)

func TestValidateAdvancedCompose_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		yaml       string
		wantReject bool
		wantErrSub string // substring of expected error message
	}{
		// --- happy path ---
		{
			name: "minimal valid service",
			yaml: `services:
  web:
    image: nginx`,
			wantReject: false,
		},
		{
			name: "named volume is fine",
			yaml: `services:
  db:
    image: postgres
    volumes:
      - dbdata:/var/lib/postgresql/data`,
			wantReject: false,
		},

		// --- already-blocked patterns (existing behaviour we MUST preserve) ---
		{
			name:       "privileged",
			yaml:       "services:\n  evil:\n    privileged: true\n",
			wantReject: true, wantErrSub: "privileged: true",
		},
		{
			name:       "pid: host short form",
			yaml:       "services:\n  evil:\n    pid: host\n",
			wantReject: true, wantErrSub: "pid: host",
		},
		{
			name:       "network: host short form",
			yaml:       "services:\n  evil:\n    network: host\n",
			wantReject: true, wantErrSub: "network: host",
		},
		{
			name:       "ipc: host short form",
			yaml:       "services:\n  evil:\n    ipc: host\n",
			wantReject: true, wantErrSub: "ipc: host",
		},
		{
			name:       "userns_mode: host",
			yaml:       "services:\n  evil:\n    userns_mode: host\n",
			wantReject: true, wantErrSub: "userns_mode: host",
		},
		{
			name:       "cap_add SYS_ADMIN unprefixed",
			yaml:       "services:\n  evil:\n    cap_add:\n      - SYS_ADMIN\n",
			wantReject: true, wantErrSub: "SYS_ADMIN",
		},
		{
			name:       "cap_add ALL",
			yaml:       "services:\n  evil:\n    cap_add:\n      - ALL\n",
			wantReject: true, wantErrSub: "ALL",
		},
		{
			name:       "security_opt apparmor:unconfined",
			yaml:       "services:\n  evil:\n    security_opt:\n      - apparmor:unconfined\n",
			wantReject: true, wantErrSub: "apparmor:unconfined",
		},
		{
			name:       "bind mount of /",
			yaml:       "services:\n  evil:\n    volumes:\n      - /:/hostfs\n",
			wantReject: true, wantErrSub: "/",
		},
		{
			name:       "bind mount of /etc",
			yaml:       "services:\n  evil:\n    volumes:\n      - /etc:/etc:ro\n",
			wantReject: true, wantErrSub: "/etc",
		},
		{
			name:       "docker socket bind",
			yaml:       "services:\n  evil:\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock\n",
			wantReject: true, wantErrSub: "docker.sock",
		},
		{
			name:       "devices passthrough",
			yaml:       "services:\n  evil:\n    devices:\n      - /dev/sda:/dev/sda\n",
			wantReject: true, wantErrSub: "devices",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAdvancedCompose(tc.yaml)
			if tc.wantReject {
				if err == nil {
					t.Fatalf("expected rejection, got nil")
				}
				if tc.wantErrSub != "" && !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("expected error to contain %q, got %q", tc.wantErrSub, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected accept, got %v", err)
			}
		})
	}
}

func TestValidateAdvancedCompose_NewGapsRejected(t *testing.T) {
	tests := []struct {
		name       string
		yaml       string
		wantErrSub string
	}{
		// --- P0-19 gap A: long-form *_mode host ---
		{
			name:       "pid_mode: host long form",
			yaml:       "services:\n  evil:\n    pid_mode: host\n",
			wantErrSub: "pid_mode: host",
		},
		{
			name:       "network_mode: host long form",
			yaml:       "services:\n  evil:\n    network_mode: host\n",
			wantErrSub: "network_mode: host",
		},
		{
			name:       "ipc_mode: host long form",
			yaml:       "services:\n  evil:\n    ipc_mode: host\n",
			wantErrSub: "ipc_mode: host",
		},

		// --- P0-19 gap B: CAP_-prefixed cap_add ---
		{
			name:       "cap_add CAP_SYS_ADMIN canonical form",
			yaml:       "services:\n  evil:\n    cap_add:\n      - CAP_SYS_ADMIN\n",
			wantErrSub: "SYS_ADMIN",
		},
		{
			name:       "cap_add cap_sys_admin lowercase canonical",
			yaml:       "services:\n  evil:\n    cap_add:\n      - cap_sys_admin\n",
			wantErrSub: "SYS_ADMIN",
		},

		// --- P0-19 gap C: group_add joining sensitive host groups ---
		{
			name:       "group_add docker",
			yaml:       "services:\n  evil:\n    group_add:\n      - docker\n",
			wantErrSub: "docker",
		},
		{
			name:       "group_add disk",
			yaml:       "services:\n  evil:\n    group_add:\n      - disk\n",
			wantErrSub: "disk",
		},
		{
			name:       "group_add sudo",
			yaml:       "services:\n  evil:\n    group_add:\n      - sudo\n",
			wantErrSub: "sudo",
		},
		{
			name:       "group_add wheel",
			yaml:       "services:\n  evil:\n    group_add:\n      - wheel\n",
			wantErrSub: "wheel",
		},
		{
			name:       "group_add root",
			yaml:       "services:\n  evil:\n    group_add:\n      - root\n",
			wantErrSub: "root",
		},
		{
			name:       "group_add kvm",
			yaml:       "services:\n  evil:\n    group_add:\n      - kvm\n",
			wantErrSub: "kvm",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateAdvancedCompose(tc.yaml)
			if err == nil {
				t.Fatalf("expected rejection, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("expected error to contain %q, got %q", tc.wantErrSub, err.Error())
			}
		})
	}
}

func TestValidateAdvancedCompose_GroupAddBenignAllowed(t *testing.T) {
	// Non-privileged group names and numeric GIDs should NOT be rejected.
	// Guards against being too aggressive (only block known-dangerous).
	cases := []string{
		"services:\n  app:\n    image: x\n    group_add:\n      - audio\n",
		"services:\n  app:\n    image: x\n    group_add:\n      - users\n",
		"services:\n  app:\n    image: x\n    group_add:\n      - \"1234\"\n",
	}
	for i, y := range cases {
		if err := ValidateAdvancedCompose(y); err != nil {
			t.Fatalf("case %d: benign group_add should be allowed, got %v", i, err)
		}
	}
}
