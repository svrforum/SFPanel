package config

import "testing"

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
						Enabled:          true,
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
