package tlsutil

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureCertAndReload(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "hub-cert.pem")
	keyPath := filepath.Join(dir, "hub-key.pem")
	fpPath := filepath.Join(dir, "hub-fingerprint.txt")

	cert1, fp1, err := EnsureCert(certPath, keyPath, fpPath)
	if err != nil {
		t.Fatalf("EnsureCert create: %v", err)
	}
	if len(fp1) != 64 {
		t.Fatalf("expected 64-char sha256 hex, got %q", fp1)
	}
	if len(cert1.Certificate) == 0 {
		t.Fatalf("expected non-empty cert chain")
	}

	fpBytes, err := os.ReadFile(fpPath)
	if err != nil {
		t.Fatalf("read fingerprint file: %v", err)
	}
	if strings.TrimSpace(string(fpBytes)) != fp1 {
		t.Fatalf("fingerprint file mismatch: got %q want %q", strings.TrimSpace(string(fpBytes)), fp1)
	}

	// Reload should produce the exact same fingerprint
	_, fp2, err := EnsureCert(certPath, keyPath, fpPath)
	if err != nil {
		t.Fatalf("EnsureCert reload: %v", err)
	}
	if fp1 != fp2 {
		t.Fatalf("fingerprint changed on reload: %q vs %q", fp1, fp2)
	}

	tlsCfg := ServerTLSConfig(cert1)
	if tlsCfg.MinVersion != tls.VersionTLS13 || tlsCfg.MaxVersion != tls.VersionTLS13 {
		t.Fatalf("expected strict TLS 1.3")
	}
}
