package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Auth     AuthConfig     `yaml:"auth"`
	Docker   DockerConfig   `yaml:"docker"`
	Log      LogConfig      `yaml:"log"`
	Cluster  ClusterConfig  `yaml:"cluster"`
}

type ServerConfig struct {
	Host       string `yaml:"host"`
	Port       int    `yaml:"port"`
	StacksPath string `yaml:"stacks_path"` // Docker Compose project root scanned at startup
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type AuthConfig struct {
	JWTSecret   string `yaml:"jwt_secret"`
	TokenExpiry string `yaml:"token_expiry"`
}

type DockerConfig struct {
	Socket string `yaml:"socket"`
}

type LogConfig struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

type ClusterConfig struct {
	Enabled          bool   `yaml:"enabled"`
	Name             string `yaml:"name"`
	NodeID           string `yaml:"node_id"`
	NodeName         string `yaml:"node_name"`
	GRPCPort         int    `yaml:"grpc_port"`
	APIPort          int    `yaml:"-"` // Set from Server.Port at runtime, not persisted
	DataDir          string `yaml:"data_dir"`
	CertDir          string `yaml:"cert_dir"`
	AdvertiseAddress string `yaml:"advertise_address"`
	RaftTLS          bool   `yaml:"raft_tls"` // TLS encryption for Raft transport (set on init)
}

func (c *Config) Validate() error {
	if c.Server.Port <= 0 {
		return fmt.Errorf("server.port must be positive, got %d", c.Server.Port)
	}
	if c.Database.Path == "" {
		return fmt.Errorf("database.path is required")
	}
	return nil
}

func (c *Config) ApplyEnvOverrides() {
	if v := os.Getenv("SFPANEL_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			c.Server.Port = port
		}
	}
	if v := os.Getenv("SFPANEL_JWT_SECRET"); v != "" {
		c.Auth.JWTSecret = v
	}
	if v := os.Getenv("SFPANEL_DB_PATH"); v != "" {
		c.Database.Path = v
	}
	if v := os.Getenv("SFPANEL_LOG_LEVEL"); v != "" {
		c.Log.Level = v
	}
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Server:   ServerConfig{Host: "0.0.0.0", Port: 8443, StacksPath: "/opt/stacks"},
		Database: DatabaseConfig{Path: "./sfpanel.db"},
		Auth:     AuthConfig{TokenExpiry: "24h"},
		Docker:   DockerConfig{Socket: "unix:///var/run/docker.sock"},
		Log:      LogConfig{Level: "info"},
		Cluster: ClusterConfig{
			GRPCPort: 9444,
			DataDir:  "/var/lib/sfpanel/cluster",
			CertDir:  "/etc/sfpanel/cluster",
		},
	}
	needsSave := false
	data, err := os.ReadFile(path)
	if err != nil {
		cfg.Auth.JWTSecret = generateRandomSecret()
		slog.Warn("no config file found, using defaults with random JWT secret")
		needsSave = true
	} else {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}
	if cfg.Auth.JWTSecret == "" {
		cfg.Auth.JWTSecret = generateRandomSecret()
		slog.Info("generated random JWT secret")
		needsSave = true
	}
	if cfg.Server.StacksPath == "" {
		cfg.Server.StacksPath = "/opt/stacks"
	}
	cfg.ApplyEnvOverrides()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}

	// Persist generated secrets so tokens survive restarts
	if needsSave {
		if saveData, err := yaml.Marshal(cfg); err == nil {
			if writeErr := AtomicWriteFile(path, saveData, 0600); writeErr != nil {
				slog.Warn("failed to persist generated config", "path", path, "error", writeErr)
			} else {
				slog.Info("config persisted with generated secrets", "path", path)
			}
		}
	}

	return cfg, nil
}

// AtomicWriteFile writes data to a temp file then renames to target path.
// This prevents partial writes from corrupting the config.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func generateRandomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate random secret: %v", err))
	}
	return hex.EncodeToString(b)
}
