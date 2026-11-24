package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// EnsureCert loads an existing ECDSA P-256 cert/key pair or generates a new self-signed one.
// It returns the tls.Certificate and the lowercase hex SHA-256 fingerprint of the leaf DER cert.
// If fpPath is non-empty, the fingerprint is also written to fpPath.
func EnsureCert(certPath, keyPath, fpPath string) (tls.Certificate, string, error) {
	if fileExists(certPath) && fileExists(keyPath) {
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return tls.Certificate{}, "", fmt.Errorf("load keypair: %w", err)
		}
		if len(cert.Certificate) == 0 {
			return tls.Certificate{}, "", fmt.Errorf("empty certificate chain in %s", certPath)
		}
		fp := CertSHA256Hex(cert.Certificate[0])
		if fpPath != "" {
			if err := writeFingerprintFile(fpPath, fp); err != nil {
				return tls.Certificate{}, "", err
			}
		}
		return cert, fp, nil
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("generate ecdsa key: %w", err)
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("generate serial: %w", err)
	}

	dnsNames := []string{"localhost"}
	if extra := os.Getenv("XB_HUB_DNS_NAME"); extra != "" {
		dnsNames = append(dnsNames, extra)
	}
	now := time.Now().UTC()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "xb-hub",
			Organization: []string{"XiaobaoDesk"},
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("create certificate: %w", err)
	}

	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("marshal ec key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(certPath), 0755); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("mkdir cert dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("mkdir key dir: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes})

	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("write cert: %w", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("write key: %w", err)
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("parse generated keypair: %w", err)
	}

	fp := CertSHA256Hex(derBytes)
	if fpPath != "" {
		if err := writeFingerprintFile(fpPath, fp); err != nil {
			return tls.Certificate{}, "", err
		}
	}
	return cert, fp, nil
}

// CertSHA256Hex computes the lowercase hex SHA-256 fingerprint of a DER-encoded X.509 certificate.
func CertSHA256Hex(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// ServerTLSConfig returns a strict TLS 1.3 server configuration.
func ServerTLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
	}
}

func writeFingerprintFile(fpPath, fp string) error {
	if err := os.MkdirAll(filepath.Dir(fpPath), 0755); err != nil {
		return fmt.Errorf("mkdir fingerprint dir: %w", err)
	}
	if err := os.WriteFile(fpPath, []byte(fp+"\n"), 0644); err != nil {
		return fmt.Errorf("write fingerprint: %w", err)
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
