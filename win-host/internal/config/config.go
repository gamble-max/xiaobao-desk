package config

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"

	"github.com/xiaobaodesk/win-host/internal/security"
)

const PBKDF2Iterations = 100000

type Config struct {
	HostID               string   `json:"host_id"`
	HubAddr              string   `json:"hub_addr"`
	HubCertSHA256        string   `json:"hub_cert_sha256"`
	HubAuthSecret        string   `json:"hub_auth_secret"`
	ListenAddr           string   `json:"listen_addr"`
	CertPath             string   `json:"cert_path"`
	KeyPath              string   `json:"key_path"`
	FingerprintPath      string   `json:"fingerprint_path"`
	HostPassword         string   `json:"host_password"`
	PasswordSaltHex      string   `json:"password_salt_hex"`
	PasswordPBKDF2Hex    string   `json:"password_pbkdf2_hex"`
	TOTPSecret           string   `json:"totp_secret"`
	RequireTOTP          bool     `json:"require_totp"`
	AllowedDeviceIDs     []string `json:"allowed_device_ids"`
	StrictAllowlist      bool     `json:"strict_allowlist"`
	MediaCorePath        string   `json:"media_core_path"`
	AutoLockOnDisconnect bool     `json:"auto_lock_on_disconnect"`
	FileRootDir          string   `json:"file_root_dir,omitempty"`
	EnableTerminal       bool     `json:"enable_terminal"`
}

// DefaultConfigPath returns xb-host-config.json in the executable directory.
func DefaultConfigPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "xb-host-config.json"
	}
	return filepath.Join(filepath.Dir(exe), "xb-host-config.json")
}

// InitConfig generates a fresh configuration with random strong password, PBKDF2 key,
// TOTP secret, and ECDSA P-256 TLS certificate, writing it ONLY to a restricted file.
// It never prints secrets to stdout/stderr.
func InitConfig(configPath string, force bool) (*Config, string, error) {
	if !force {
		if _, err := os.Stat(configPath); err == nil {
			return nil, "", fmt.Errorf("config already exists at %s (use --force to overwrite)", configPath)
		}
	}

	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, "", err
	}

	hostID, err := security.RandomUUIDv4()
	if err != nil {
		return nil, "", err
	}
	pw, err := security.GenerateStrongPassword(24)
	if err != nil {
		return nil, "", err
	}
	saltHex, err := security.RandomHex(16)
	if err != nil {
		return nil, "", err
	}
	saltBytes, _ := hex.DecodeString(saltHex)
	pbkdf2Key := security.PBKDF2SHA256([]byte(pw), saltBytes, PBKDF2Iterations, 32)

	totpSecret, err := security.GenerateTOTPSecret()
	if err != nil {
		return nil, "", err
	}

	certPath := filepath.Join(dir, "host-cert.pem")
	keyPath := filepath.Join(dir, "host-key.pem")
	fpPath := filepath.Join(dir, "host-fingerprint.txt")

	_, fp, err := security.EnsureSelfSignedCert(certPath, keyPath, "xb-host-"+hostID[:8])
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(fpPath, []byte(fp+"\n"), 0644); err != nil {
		return nil, "", err
	}

	exeDir := dir
	if exe, err := os.Executable(); err == nil {
		exeDir = filepath.Dir(exe)
	}

	cfg := &Config{
		HostID:               hostID,
		HubAddr:              "",
		HubCertSHA256:        "",
		HubAuthSecret:        "",
		ListenAddr:           "[::]:21118",
		CertPath:             certPath,
		KeyPath:              keyPath,
		FingerprintPath:      fpPath,
		HostPassword:         pw,
		PasswordSaltHex:      saltHex,
		PasswordPBKDF2Hex:    hex.EncodeToString(pbkdf2Key),
		TOTPSecret:           totpSecret,
		RequireTOTP:          false,
		AllowedDeviceIDs:     []string{},
		StrictAllowlist:      false,
		MediaCorePath:        filepath.Join(exeDir, "xb-media-core.exe"),
		AutoLockOnDisconnect: false,
		EnableTerminal:       false,
	}

	_ = RestrictDirPermissions(dir)
	if err := SaveRestricted(configPath, cfg); err != nil {
		return nil, "", err
	}
	_ = RestrictFilePermissions(keyPath)
	return cfg, fp, nil
}

// Load reads and validates a Config from disk.
func Load(configPath string) (*Config, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "[::]:21118"
	}
	if cfg.HostPassword == "" && cfg.PasswordPBKDF2Hex == "" {
		return nil, errors.New("config missing host_password / password_pbkdf2_hex")
	}
	return &cfg, nil
}

// SaveRestricted writes config JSON with 0600 permissions and Windows ACL restriction.
func SaveRestricted(path string, cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	_ = RestrictDirPermissions(filepath.Dir(path))
	return RestrictFilePermissions(path)
}

// RestrictFilePermissions restricts file ACLs on Windows to the current user only.
func RestrictFilePermissions(path string) error {
	if runtime.GOOS != "windows" {
		return os.Chmod(path, 0600)
	}
	u, err := user.Current()
	if err != nil || u.Username == "" {
		return nil
	}
	cmd := exec.Command("icacls", path, "/inheritance:r", "/grant:r", u.Username+":(R,W)")
	_ = cmd.Run()
	return nil
}

// RestrictDirPermissions restricts directory ACLs on Windows to the current user only.
func RestrictDirPermissions(dir string) error {
	if runtime.GOOS != "windows" {
		return os.Chmod(dir, 0700)
	}
	u, err := user.Current()
	if err != nil || u.Username == "" {
		return nil
	}
	cmd := exec.Command("icacls", dir, "/inheritance:r", "/grant:r", u.Username+":(OI)(CI)F")
	_ = cmd.Run()
	return nil
}

// CandidateAuthSecrets returns all valid secret byte slices for verifying client HMACs.
func (c *Config) CandidateAuthSecrets() [][]byte {
	var out [][]byte
	if c.HostPassword != "" {
		out = append(out, []byte(c.HostPassword))
	}
	if c.PasswordPBKDF2Hex != "" {
		if raw, err := hex.DecodeString(c.PasswordPBKDF2Hex); err == nil {
			out = append(out, raw)
		}
		out = append(out, []byte(c.PasswordPBKDF2Hex))
	}
	return out
}
