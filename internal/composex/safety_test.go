package composex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every pattern below was an outright rejection before the two tiers existed.
// None of them is the panel's own secrets, so each is now a *risky* finding:
// still refused unacknowledged, allowed once the operator says so. The rule
// name is asserted, not just "something was found" — the tier a pattern sits in
// is the whole contract.
func TestValidateAdvancedCompose_TableDriven(t *testing.T) {
	tests := []struct {
		name       string
		yaml       string
		wantRule   string // "" = nothing should be found
		wantErrSub string // substring of the expected finding message
	}{
		// --- happy path ---
		{
			name: "minimal valid service",
			yaml: `services:
  web:
    image: nginx`,
		},
		{
			name: "named volume is fine",
			yaml: `services:
  db:
    image: postgres
    volumes:
      - dbdata:/var/lib/postgresql/data`,
		},

		// --- already-blocked patterns (existing behaviour we MUST preserve) ---
		{
			name:     "privileged",
			yaml:     "services:\n  evil:\n    privileged: true\n",
			wantRule: "privileged", wantErrSub: "privileged: true",
		},
		{
			name:     "pid: host short form",
			yaml:     "services:\n  evil:\n    pid: host\n",
			wantRule: "namespace", wantErrSub: "pid: host",
		},
		{
			name:     "network: host short form",
			yaml:     "services:\n  evil:\n    network: host\n",
			wantRule: "namespace", wantErrSub: "network: host",
		},
		{
			name:     "ipc: host short form",
			yaml:     "services:\n  evil:\n    ipc: host\n",
			wantRule: "namespace", wantErrSub: "ipc: host",
		},
		{
			name:     "userns_mode: host",
			yaml:     "services:\n  evil:\n    userns_mode: host\n",
			wantRule: "namespace", wantErrSub: "userns_mode: host",
		},
		{
			name:     "cap_add SYS_ADMIN unprefixed",
			yaml:     "services:\n  evil:\n    cap_add:\n      - SYS_ADMIN\n",
			wantRule: "capability", wantErrSub: "SYS_ADMIN",
		},
		{
			name:     "cap_add ALL",
			yaml:     "services:\n  evil:\n    cap_add:\n      - ALL\n",
			wantRule: "capability", wantErrSub: "ALL",
		},
		{
			name:     "security_opt apparmor:unconfined",
			yaml:     "services:\n  evil:\n    security_opt:\n      - apparmor:unconfined\n",
			wantRule: "security-opt", wantErrSub: "apparmor:unconfined",
		},
		{
			name:     "bind mount of /",
			yaml:     "services:\n  evil:\n    volumes:\n      - /:/hostfs\n",
			wantRule: "bind", wantErrSub: "/",
		},
		{
			name:     "bind mount of /etc",
			yaml:     "services:\n  evil:\n    volumes:\n      - /etc:/etc:ro\n",
			wantRule: "bind", wantErrSub: "/etc",
		},
		{
			name:     "docker socket bind",
			yaml:     "services:\n  evil:\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock\n",
			wantRule: "bind", wantErrSub: "docker.sock",
		},
		{
			name:     "devices passthrough",
			yaml:     "services:\n  evil:\n    devices:\n      - /dev/sda:/dev/sda\n",
			wantRule: "device", wantErrSub: "devices",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertRisky(t, tc.yaml, tc.wantRule, tc.wantErrSub)
		})
	}
}

// assertRisky is the shape every pre-existing case now takes: the analyser
// reports the named rule in the risky tier and nothing in the forbidden one,
// the unacknowledged request is still refused, and the forbidden-only wrapper
// lets it through. An empty rule asserts a clean document instead.
func assertRisky(t *testing.T, yaml, wantRule, wantSub string) {
	t.Helper()
	report, err := Analyze(yaml)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	// None of these is the panel's own secrets, so none may be forbidden —
	// that tier is what no acknowledgement lifts.
	if len(report.Forbidden) != 0 {
		t.Fatalf("forbidden = %+v, want none", report.Forbidden)
	}
	if wantRule == "" {
		if len(report.Risky) != 0 {
			t.Fatalf("expected no findings, got %+v", report.Risky)
		}
		if err := ValidateAdvancedCompose(yaml); err != nil {
			t.Fatalf("expected accept, got %v", err)
		}
		return
	}
	var found *Finding
	for i := range report.Risky {
		if report.Risky[i].Rule == wantRule {
			found = &report.Risky[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("rule %q missing from %+v", wantRule, report.Risky)
	}
	if wantSub != "" && !strings.Contains(found.Message, wantSub) {
		t.Fatalf("expected message to contain %q, got %q", wantSub, found.Message)
	}
	if err := report.Error(false); err == nil {
		t.Fatalf("unacknowledged risky compose accepted")
	}
	if err := ValidateAdvancedCompose(yaml); err != nil {
		t.Fatalf("wrapper refused risky-but-allowed content: %v", err)
	}
}

func TestValidateAdvancedCompose_NewGapsRejected(t *testing.T) {
	tests := []struct {
		name       string
		yaml       string
		wantRule   string
		wantErrSub string
	}{
		// --- P0-19 gap A: long-form *_mode host ---
		{
			name:       "pid_mode: host long form",
			yaml:       "services:\n  evil:\n    pid_mode: host\n",
			wantRule:   "namespace",
			wantErrSub: "pid_mode: host",
		},
		{
			name:       "network_mode: host long form",
			yaml:       "services:\n  evil:\n    network_mode: host\n",
			wantRule:   "namespace",
			wantErrSub: "network_mode: host",
		},
		{
			name:       "ipc_mode: host long form",
			yaml:       "services:\n  evil:\n    ipc_mode: host\n",
			wantRule:   "namespace",
			wantErrSub: "ipc_mode: host",
		},

		// --- P0-19 gap B: CAP_-prefixed cap_add ---
		{
			name:       "cap_add CAP_SYS_ADMIN canonical form",
			yaml:       "services:\n  evil:\n    cap_add:\n      - CAP_SYS_ADMIN\n",
			wantRule:   "capability",
			wantErrSub: "SYS_ADMIN",
		},
		{
			name:       "cap_add cap_sys_admin lowercase canonical",
			yaml:       "services:\n  evil:\n    cap_add:\n      - cap_sys_admin\n",
			wantRule:   "capability",
			wantErrSub: "SYS_ADMIN",
		},

		// --- P0-19 gap C: group_add joining sensitive host groups ---
		{
			name:       "group_add docker",
			yaml:       "services:\n  evil:\n    group_add:\n      - docker\n",
			wantRule:   "group",
			wantErrSub: "docker",
		},
		{
			name:       "group_add disk",
			yaml:       "services:\n  evil:\n    group_add:\n      - disk\n",
			wantRule:   "group",
			wantErrSub: "disk",
		},
		{
			name:       "group_add sudo",
			yaml:       "services:\n  evil:\n    group_add:\n      - sudo\n",
			wantRule:   "group",
			wantErrSub: "sudo",
		},
		{
			name:       "group_add wheel",
			yaml:       "services:\n  evil:\n    group_add:\n      - wheel\n",
			wantRule:   "group",
			wantErrSub: "wheel",
		},
		{
			name:       "group_add root",
			yaml:       "services:\n  evil:\n    group_add:\n      - root\n",
			wantRule:   "group",
			wantErrSub: "root",
		},
		{
			name:       "group_add kvm",
			yaml:       "services:\n  evil:\n    group_add:\n      - kvm\n",
			wantRule:   "group",
			wantErrSub: "kvm",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertRisky(t, tc.yaml, tc.wantRule, tc.wantErrSub)
		})
	}
}

func TestValidateAdvancedCompose_SeparatorAndStringFormGaps(t *testing.T) {
	tests := []struct {
		name       string
		yaml       string
		wantRule   string
		wantErrSub string
	}{
		// --- gap A: daemon state dirs in bind-mount blocklist ---
		{
			name:       "bind mount of /var/lib/docker",
			yaml:       "services:\n  evil:\n    volumes:\n      - /var/lib/docker:/mnt\n",
			wantRule:   "bind",
			wantErrSub: "/var/lib/docker",
		},
		{
			name:       "bind mount of /var/lib/docker subpath",
			yaml:       "services:\n  evil:\n    volumes:\n      - /var/lib/docker/volumes:/mnt\n",
			wantRule:   "bind",
			wantErrSub: "/var/lib/docker/volumes",
		},
		{
			name:       "bind mount of /run/containerd",
			yaml:       "services:\n  evil:\n    volumes:\n      - /run/containerd:/mnt\n",
			wantRule:   "bind",
			wantErrSub: "/run/containerd",
		},
		{
			// /var/run is a symlink to /run on the target platform, so this
			// reaches the same runtime state — must not slip past the blocklist.
			name:       "bind mount of /var/run/containerd alias",
			yaml:       "services:\n  evil:\n    volumes:\n      - /var/run/containerd:/mnt\n",
			wantRule:   "bind",
			wantErrSub: "containerd",
		},
		{
			name:       "bind mount of /var/run/docker.sock alias",
			yaml:       "services:\n  evil:\n    volumes:\n      - /var/run/docker.sock:/x\n",
			wantRule:   "bind",
			wantErrSub: "docker.sock",
		},

		// --- gap B: security_opt '=' separator ---
		{
			name:       "security_opt apparmor=unconfined",
			yaml:       "services:\n  evil:\n    security_opt:\n      - apparmor=unconfined\n",
			wantRule:   "security-opt",
			wantErrSub: "apparmor=unconfined",
		},
		{
			name:       "security_opt seccomp=unconfined",
			yaml:       "services:\n  evil:\n    security_opt:\n      - seccomp=unconfined\n",
			wantRule:   "security-opt",
			wantErrSub: "seccomp=unconfined",
		},
		{
			name:       "security_opt systempaths:unconfined colon form",
			yaml:       "services:\n  evil:\n    security_opt:\n      - systempaths:unconfined\n",
			wantRule:   "security-opt",
			wantErrSub: "systempaths:unconfined",
		},

		// --- gap C: privileged as string ---
		{
			name:       "privileged quoted true",
			yaml:       "services:\n  evil:\n    privileged: \"true\"\n",
			wantRule:   "privileged",
			wantErrSub: "privileged: true",
		},
		{
			name:       "privileged quoted True",
			yaml:       "services:\n  evil:\n    privileged: \"True\"\n",
			wantRule:   "privileged",
			wantErrSub: "privileged: true",
		},
		{
			name:       "privileged quoted 1",
			yaml:       "services:\n  evil:\n    privileged: \"1\"\n",
			wantRule:   "privileged",
			wantErrSub: "privileged: true",
		},
		{
			// Unquoted YAML-1.1 bool: yaml.v3 leaves "yes" a string for
			// untyped decode, but compose resolves it to a truthy bool.
			name:       "privileged unquoted yes",
			yaml:       "services:\n  evil:\n    privileged: yes\n",
			wantRule:   "privileged",
			wantErrSub: "privileged: true",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertRisky(t, tc.yaml, tc.wantRule, tc.wantErrSub)
		})
	}
}

func TestValidateAdvancedCompose_SeparatorAndStringFormBenignAllowed(t *testing.T) {
	cases := []string{
		// no-new-privileges is a hardening option, not a sandbox escape.
		"services:\n  app:\n    image: x\n    security_opt:\n      - no-new-privileges:true\n",
		"services:\n  app:\n    image: x\n    security_opt:\n      - no-new-privileges=true\n",
		// seccomp pointing at a profile file is fine; only unconfined is blocked.
		"services:\n  app:\n    image: x\n    security_opt:\n      - seccomp=/etc/seccomp/profile.json\n",
		// privileged false in both shapes.
		"services:\n  app:\n    image: x\n    privileged: false\n",
		"services:\n  app:\n    image: x\n    privileged: \"false\"\n",
		// Sibling path that merely shares the blocked prefix string.
		"services:\n  app:\n    image: x\n    volumes:\n      - /var/lib/dockerdata:/mnt\n",
	}
	for i, y := range cases {
		// Benign now means *no finding at all*. Asserting only that the
		// wrapper returns nil would no longer prove anything: it accepts
		// risky content by design.
		t.Run(fmt.Sprintf("case %d", i), func(t *testing.T) {
			assertRisky(t, y, "", "")
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
		// As above: benign means the analyser finds nothing, not merely that
		// the forbidden-only wrapper lets it through.
		t.Run(fmt.Sprintf("case %d", i), func(t *testing.T) {
			assertRisky(t, y, "", "")
		})
	}
}

// The catalog is the panel's own content: a compose file the App Store
// installs must stay openable in the editor, which means risky-but-
// acknowledgeable, never forbidden. Reading the shipped files rather than a
// fixture is the point — a new catalog app that trips the forbidden tier has
// to turn this red.
func TestAnalyzeAcceptsEveryCatalogApp(t *testing.T) {
	files, err := filepath.Glob("../../appstore/apps/*/docker-compose.yml")
	if err != nil || len(files) == 0 {
		t.Fatalf("catalog not found: %v", err)
	}
	risky := 0
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		report, err := Analyze(string(body))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		app := filepath.Base(filepath.Dir(f))
		if len(report.Forbidden) > 0 {
			t.Errorf("%s: forbidden %+v — a catalog app must stay installable and editable", app, report.Forbidden)
		}
		if len(report.Risky) > 0 {
			risky++
		}
		if err := report.Error(true); err != nil {
			t.Errorf("%s: acknowledged install refused: %v", app, err)
		}
	}
	// Measured on the shipped catalog: 15 apps ask for something risky.
	// A change to that number is a catalog change and should be seen.
	if risky != 15 {
		t.Errorf("risky catalog apps = %d, want 15 (the spec's table)", risky)
	}
}

func TestAnalyzeForbidsThePanelsOwnSecrets(t *testing.T) {
	for _, path := range []string{
		"/etc/sfpanel", "/etc/sfpanel/config.yaml", "/var/lib/sfpanel",
		"/var/lib/sfpanel/sfpanel.db", "/root/.ssh", "/etc/sudoers.d",
		// Respellings of the same directories. The kernel resolves all of
		// these to the protected path and Docker cleans the mount source
		// too, so a tier that matched the string as written would refuse
		// /etc/sfpanel and wave /etc/./sfpanel through to the same files.
		"/etc//sfpanel", "/etc/./sfpanel", "/etc/foo/../sfpanel",
		"/var/lib//sfpanel", "//etc/sfpanel", "/root/.ssh/",
	} {
		yaml := "services:\n  a:\n    image: x\n    volumes:\n      - " + path + ":/mnt\n"
		report, err := Analyze(yaml)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if len(report.Forbidden) != 1 || report.Forbidden[0].Rule != "bind" {
			t.Fatalf("%s: forbidden = %+v, want one bind finding", path, report.Forbidden)
		}
		// No acknowledgement lifts it: this tier is what stops a hole here
		// from becoming the ability to mint tokens and node certificates.
		if err := report.Error(true); err == nil {
			t.Errorf("%s: acknowledged request accepted a forbidden bind", path)
		}
		if err := ValidateAdvancedCompose(yaml); err == nil {
			t.Errorf("%s: the forbidden-only wrapper accepted it", path)
		}
	}
}

func TestAnalyzeReportsEveryFindingNotTheFirst(t *testing.T) {
	yaml := `services:
  a:
    image: x
    privileged: true
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
  b:
    image: y
    network_mode: host
`
	report, err := Analyze(yaml)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Risky) != 3 {
		t.Fatalf("risky = %d (%+v), want 3 — the dialog lists what a stack asks for, so one finding per pattern", len(report.Risky), report.Risky)
	}
	rules := map[string]bool{}
	for _, f := range report.Risky {
		rules[f.Rule] = true
	}
	for _, want := range []string{"privileged", "bind", "namespace"} {
		if !rules[want] {
			t.Errorf("rule %q missing from %+v", want, report.Risky)
		}
	}
	// Unacknowledged it is refused, and the message names every finding.
	err = report.Error(false)
	if err == nil {
		t.Fatal("unacknowledged risky compose accepted")
	}
	for _, want := range []string{"docker.sock", "privileged", "host"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q does not mention %q", err.Error(), want)
		}
	}
	if err := report.Error(true); err != nil {
		t.Errorf("acknowledged risky compose refused: %v", err)
	}
	// The forbidden-only wrapper lets risky content through: an operator who
	// said yes must not be blocked by the path that cannot ask.
	if err := ValidateAdvancedCompose(yaml); err != nil {
		t.Errorf("wrapper refused risky-but-allowed content: %v", err)
	}
}

// Two mounts of one source at two targets are two things the stack asks for.
// The messages are what the confirm dialog lists, so identical sentences are
// not a cosmetic problem: the operator sees one line where the stack asked for
// two mounts, and React keys the list by the line.
func TestAnalyzeTellsTwoMountsOfOneSourceApart(t *testing.T) {
	yaml := `services:
  a:
    image: x
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - /var/run/docker.sock:/tmp/sock
`
	report, err := Analyze(yaml)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Risky) != 2 {
		t.Fatalf("risky = %+v, want two bind findings", report.Risky)
	}
	if report.Risky[0].Message == report.Risky[1].Message {
		t.Fatalf("both mounts produced the same sentence %q", report.Risky[0].Message)
	}
	for _, want := range []string{`at "/var/run/docker.sock"`, `at "/tmp/sock"`} {
		if !strings.Contains(report.Risky[0].Message+report.Risky[1].Message, want) {
			t.Errorf("no finding names %s: %+v", want, report.Risky)
		}
	}
}

// The long form is the shape `docker compose config` emits, and it is what the
// deploy-time guard analyses. A source written with a stray space reaches the
// same directory, so the tier has to trim it exactly as the short form does.
func TestAnalyzeTrimsTheLongFormSource(t *testing.T) {
	yaml := `services:
  thief:
    image: alpine
    volumes:
      - type: bind
        source: "  /etc/sfpanel  "
        target: /loot
`
	report, err := Analyze(yaml)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Forbidden) != 1 || report.Forbidden[0].Rule != "bind" {
		t.Fatalf("forbidden = %+v, want one bind finding", report.Forbidden)
	}
	if err := report.Error(true); err == nil {
		t.Error("an acknowledged request bound /etc/sfpanel through the long form")
	}
}
