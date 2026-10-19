package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestObservabilityValidation(t *testing.T) {
	cases := []struct {
		name      string
		retention string
		eventsRet string
		wantOK    bool
	}{
		{"defaults are valid", "24h", "30d", true},
		{"6h metrics ok", "6h", "30d", true},
		{"72h metrics ok", "72h", "30d", true},
		{"7d events ok", "24h", "7d", true},
		{"90d events ok", "24h", "90d", true},
		{"invalid metrics retention", "5m", "30d", false},
		{"invalid events retention", "24h", "1y", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{
				Server:   ServerConfig{Port: 19443},
				Database: DatabaseConfig{Path: "/tmp/x.db"},
				Auth:     AuthConfig{JWTSecret: "0123456789abcdef0123456789abcdef"},
				Docker: DockerConfig{
					Socket: "unix:///var/run/docker.sock",
					Observability: ObservabilityConfig{
						Enabled:          ptrBool(true),
						MetricsRetention: c.retention,
						EventsRetention:  c.eventsRet,
					},
				},
			}
			err := cfg.Validate()
			if c.wantOK && err != nil {
				t.Errorf("expected OK, got %v", err)
			}
			if !c.wantOK && err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestLoadObservabilityDefaults(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantOK  bool
		checkFn func(*testing.T, *Config)
	}{
		{
			"no observability block → auto-enable with defaults",
			"server:\n  port: 19443\ndatabase:\n  path: /tmp/x.db\nauth:\n  jwt_secret: 0123456789abcdef0123456789abcdef\n",
			true,
			func(t *testing.T, c *Config) {
				if !c.Docker.Observability.IsEnabled() {
					t.Error("expected default-on")
				}
				if c.Docker.Observability.MetricsRetention != "24h" {
					t.Errorf("metrics_retention: got %q", c.Docker.Observability.MetricsRetention)
				}
				if c.Docker.Observability.EventsRetention != "30d" {
					t.Errorf("events_retention: got %q", c.Docker.Observability.EventsRetention)
				}
			},
		},
		{
			"enabled: false → stays disabled",
			"server:\n  port: 19443\ndatabase:\n  path: /tmp/x.db\nauth:\n  jwt_secret: 0123456789abcdef0123456789abcdef\ndocker:\n  observability:\n    enabled: false\n",
			true,
			func(t *testing.T, c *Config) {
				if c.Docker.Observability.IsEnabled() {
					t.Error("operator set enabled: false; expected disabled")
				}
			},
		},
		{
			"garbage metrics_retention rejected even when disabled",
			"server:\n  port: 19443\ndatabase:\n  path: /tmp/x.db\nauth:\n  jwt_secret: 0123456789abcdef0123456789abcdef\ndocker:\n  observability:\n    enabled: false\n    metrics_retention: garbage\n",
			false,
			nil,
		},
		{
			"explicit retention override",
			"server:\n  port: 19443\ndatabase:\n  path: /tmp/x.db\nauth:\n  jwt_secret: 0123456789abcdef0123456789abcdef\ndocker:\n  observability:\n    metrics_retention: 6h\n    events_retention: 7d\n",
			true,
			func(t *testing.T, c *Config) {
				if c.Docker.Observability.MetricsRetention != "6h" {
					t.Errorf("metrics_retention: got %q", c.Docker.Observability.MetricsRetention)
				}
				if c.Docker.Observability.EventsRetention != "7d" {
					t.Errorf("events_retention: got %q", c.Docker.Observability.EventsRetention)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, err := os.CreateTemp("", "sfpanel-cfg-*.yaml")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(f.Name())
			if _, err := f.WriteString(c.yaml); err != nil {
				t.Fatal(err)
			}
			f.Close()

			cfg, err := Load(f.Name())
			if c.wantOK && err != nil {
				t.Fatalf("expected OK, got %v", err)
			}
			if !c.wantOK && err == nil {
				t.Fatal("expected error, got nil")
			}
			if c.checkFn != nil && cfg != nil {
				c.checkFn(t, cfg)
			}
		})
	}
}

// CheckFile exists to predict Load, for a restore that installs config.yaml
// and then exits for systemd. Load refuses an explicitly empty database.path,
// so a CheckFile that filled the default in passed a file the restarted panel
// would then refuse on every restart.
func TestCheckFileRefusesAnEmptyDatabasePathAsLoadDoes(t *testing.T) {
	t.Setenv("SFPANEL_DB_PATH", "")
	p := filepath.Join(t.TempDir(), "config.yaml")
	body := "server:\n  port: 3628\ndatabase:\n  path: \"\"\nauth:\n  jwt_secret: 0123456789abcdef0123456789abcdef\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "database.path") {
		t.Fatalf("Load: got %v, want a database.path refusal", err)
	}
	if err := CheckFile(p); err == nil || !strings.Contains(err.Error(), "database.path") {
		t.Fatalf("CheckFile: got %v, want the database.path refusal Load gives", err)
	}
}

func ptrBool(b bool) *bool { return &b }
