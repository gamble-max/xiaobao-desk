package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config holds xb-hub runtime configuration.
type Config struct {
	ListenAddr           string   `json:"listen_addr"`
	CertPath             string   `json:"cert_path"`
	KeyPath              string   `json:"key_path"`
	FingerprintPath      string   `json:"fingerprint_path"`
	AuthSecret           string   `json:"auth_secret"`
	AllowedDeviceIDs     []string `json:"allowed_device_ids"`
	PeerTTLSeconds       int      `json:"peer_ttl_seconds"`
	RelayTokenTTLSeconds int      `json:"relay_token_ttl_seconds"`
}

// DefaultConfig returns a Config with sensible defaults relative to dataDir.
func DefaultConfig(dataDir string) *Config {
	return &Config{
		ListenAddr:           ":21116",
		CertPath:             filepath.Join(dataDir, "hub-cert.pem"),
		KeyPath:              filepath.Join(dataDir, "hub-key.pem"),
		FingerprintPath:      filepath.Join(dataDir, "hub-fingerprint.txt"),
		AllowedDeviceIDs:     []string{},
		PeerTTLSeconds:       120,
		RelayTokenTTLSeconds: 60,
	}
}

// LoadOrInit loads Config from path, or creates a new Config with a random 32-byte hex auth_secret if missing.
func LoadOrInit(path string, dataDir string) (*Config, bool, error) {
	if dataDir == "" {
		dataDir = filepath.Dir(path)
	}
	cfg := DefaultConfig(dataDir)

	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, false, fmt.Errorf("parse config %s: %w", path, err)
		}
		if cfg.ListenAddr == "" {
			cfg.ListenAddr = ":21116"
		}
		if cfg.CertPath == "" {
			cfg.CertPath = filepath.Join(dataDir, "hub-cert.pem")
		}
		if cfg.KeyPath == "" {
			cfg.KeyPath = filepath.Join(dataDir, "hub-key.pem")
		}
		if cfg.FingerprintPath == "" {
			cfg.FingerprintPath = filepath.Join(dataDir, "hub-fingerprint.txt")
		}
		if cfg.PeerTTLSeconds <= 0 {
			cfg.PeerTTLSeconds = 120
		}
		if cfg.RelayTokenTTLSeconds <= 0 {
			cfg.RelayTokenTTLSeconds = 60
		}
		if cfg.AuthSecret == "" {
			secret, err := randomSecretHex()
			if err != nil {
				return nil, false, err
			}
			cfg.AuthSecret = secret
			if err := Save(path, cfg); err != nil {
				return nil, false, err
			}
		}
		return cfg, false, nil
	}

	if !os.IsNotExist(err) {
		return nil, false, fmt.Errorf("read config %s: %w", path, err)
	}

	secret, err := randomSecretHex()
	if err != nil {
		return nil, false, err
	}
	cfg.AuthSecret = secret
	if err := Save(path, cfg); err != nil {
		return nil, false, err
	}
	return cfg, true, nil
}

// Save writes cfg to path with 0600 permissions.
func Save(path string, cfg *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("mkdir config dir: %w", err)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

func randomSecretHex() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate auth secret: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
