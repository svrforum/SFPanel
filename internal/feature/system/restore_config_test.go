package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stagedConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const goodConfig = `
server:
  host: 0.0.0.0
  port: 3628
database:
  path: /var/lib/sfpanel/sfpanel.db
auth:
  jwt_secret: 0123456789abcdef0123456789abcdef
`

func TestValidateStagedConfigAcceptsAUsableFile(t *testing.T) {
	if err := validateStagedConfig(stagedConfig(t, goodConfig)); err != nil {
		t.Fatalf("rejected a valid config: %v", err)
	}
}

// The restore replaces the live config and then exits for systemd. Anything
// that stops the panel from starting has to be caught before that, not after.
func TestValidateStagedConfigRejectsWhatWouldNotBoot(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"not yaml at all": {
			body: "server: [this is not\n  valid: yaml",
			want: "not valid YAML",
		},
		"port zero": {
			body: "server:\n  port: 0\ndatabase:\n  path: /x\nauth:\n  jwt_secret: 0123456789abcdef0123456789abcdef\n",
			want: "server.port",
		},
		"unusable retention value": {
			body: "server:\n  port: 3628\ndatabase:\n  path: /x\nauth:\n  jwt_secret: 0123456789abcdef0123456789abcdef\ndocker:\n  observability:\n    metrics_retention: forever\n",
			want: "metrics_retention",
		},
		"signing secret too short": {
			body: "server:\n  port: 3628\ndatabase:\n  path: /x\nauth:\n  jwt_secret: short\n",
			want: "jwt_secret",
		},
		"half a TLS pair": {
			body: "server:\n  port: 3628\n  tls:\n    cert_file: /a.pem\ndatabase:\n  path: /x\nauth:\n  jwt_secret: 0123456789abcdef0123456789abcdef\n",
			want: "cert_file",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := validateStagedConfig(stagedConfig(t, tc.body))
			if err == nil {
				t.Fatalf("accepted a config that would not boot")
			}
			// The reason, not just the refusal: every one of these paths
			// produces an error, and a test that only checked for one would
			// pass with the validation removed and the parse left in.
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), tc.want)
			}
		})
	}
}

// The check must not be stricter than the loader. A config.yaml that omits
// optional keys loads fine — Load fills them — so refusing it would reject
// backups that work. This caught a hand-written version of the check that
// validated a bare unmarshal without the defaults.
func TestValidateStagedConfigAcceptsWhatTheLoaderWouldFillIn(t *testing.T) {
	minimal := "server:\n  port: 3628\nauth:\n  jwt_secret: 0123456789abcdef0123456789abcdef\n"
	if err := validateStagedConfig(stagedConfig(t, minimal)); err != nil {
		t.Fatalf("rejected a config the loader would accept: %v", err)
	}
	// Including one with no secret at all, which Load generates.
	noSecret := "server:\n  port: 3628\ndatabase:\n  path: /x\n"
	if err := validateStagedConfig(stagedConfig(t, noSecret)); err != nil {
		t.Fatalf("rejected a config with no secret, which the loader generates: %v", err)
	}
}

func TestValidateStagedConfigRejectsAMissingFile(t *testing.T) {
	if err := validateStagedConfig(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("accepted a file that does not exist")
	}
}
