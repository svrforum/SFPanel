package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Database DatabaseConfig `yaml:"database"`
	Auth     AuthConfig     `yaml:"auth"`
	Docker   DockerConfig   `yaml:"docker"`
	Log      LogConfig      `yaml:"log"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
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

func Load(path string) (*Config, error) {
	cfg := &Config{
		Server:   ServerConfig{Host: "0.0.0.0", Port: 8443},
		Database: DatabaseConfig{Path: "./sfpanel.db"},
		Auth:     AuthConfig{TokenExpiry: "24h"},
		Docker:   DockerConfig{Socket: "unix:///var/run/docker.sock"},
		Log:      LogConfig{Level: "info"},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		cfg.Auth.JWTSecret = generateRandomSecret()
		log.Println("[WARN] No config file found — using defaults with random JWT secret")
		return cfg, nil
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	if cfg.Auth.JWTSecret == "" {
		cfg.Auth.JWTSecret = generateRandomSecret()
		log.Println("[WARN] jwt_secret not set in config — generated random secret (tokens will invalidate on restart)")
	}
	return cfg, nil
}

func generateRandomSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate random secret: %v", err))
	}
	return hex.EncodeToString(b)
}
