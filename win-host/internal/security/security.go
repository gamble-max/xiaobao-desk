package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#%^&*-_=+"

// RandomHex returns n random bytes encoded as lowercase hex (2*n chars).
func RandomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// RandomUUIDv4 generates a standard 36-char lowercase UUID v4 string.
func RandomUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// GenerateStrongPassword generates a cryptographically random password of length n.
func GenerateStrongPassword(n int) (string, error) {
	if n < 16 {
		n = 16
	}
	out := make([]byte, n)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = passwordAlphabet[idx.Int64()]
	}
	return string(out), nil
}

// PBKDF2SHA256 derives a key using RFC 2898 PBKDF2 with HMAC-SHA256.
func PBKDF2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := func(key, data []byte) []byte {
		h := hmac.New(sha256.New, key)
		h.Write(data)
		return h.Sum(nil)
	}
	numBlocks := (keyLen + sha256.Size - 1) / sha256.Size
	var dk []byte
	var u [4]byte
	for block := 1; block <= numBlocks; block++ {
		binary.BigEndian.PutUint32(u[:], uint32(block))
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write(u[:])
		uPrev := mac.Sum(nil)
		t := make([]byte, len(uPrev))
		copy(t, uPrev)
		for i := 2; i <= iter; i++ {
			uPrev = prf(password, uPrev)
			for j := range t {
				t[j] ^= uPrev[j]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}

// ComputeAuthHMAC computes lowercase hex HMAC-SHA256(secret, nonce).
func ComputeAuthHMAC(nonce string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(nonce))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyAuthHMAC verifies providedHex against HMAC-SHA256(secret, nonce) for any candidate secret in constant time.
func VerifyAuthHMAC(nonce, providedHex string, secrets ...[]byte) bool {
	provided, err := hex.DecodeString(strings.TrimSpace(providedHex))
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	matched := false
	for _, sec := range secrets {
		if len(sec) == 0 {
			continue
		}
		mac := hmac.New(sha256.New, sec)
		mac.Write([]byte(nonce))
		expected := mac.Sum(nil)
		if hmac.Equal(provided, expected) {
			matched = true
		}
	}
	return matched
}

// GenerateTOTPSecret generates a 20-byte (160-bit) base32 TOTP secret without padding.
func GenerateTOTPSecret() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

// GenerateTOTP computes a 6-digit RFC 6238 TOTP code for the given time (30s step, HMAC-SHA1).
func GenerateTOTP(secretBase32 string, t time.Time) (string, error) {
	clean := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secretBase32), " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(clean)
	if err != nil {
		// Retry with standard padding in case caller supplied padded base32
		key, err = base32.StdEncoding.DecodeString(clean)
		if err != nil {
			return "", fmt.Errorf("invalid base32 totp secret: %w", err)
		}
	}
	counter := uint64(t.Unix()) / 30
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	return fmt.Sprintf("%06d", code%1000000), nil
}

// VerifyTOTP checks code against RFC 6238 TOTP with +/- skewSteps (30s each) in constant time.
func VerifyTOTP(secretBase32, code string, t time.Time, skewSteps int) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}
	if skewSteps < 0 {
		skewSteps = 0
	}
	ok := false
	for step := -skewSteps; step <= skewSteps; step++ {
		expected, err := GenerateTOTP(secretBase32, t.Add(time.Duration(step)*30*time.Second))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(code), []byte(expected)) == 1 {
			ok = true
		}
	}
	return ok
}

// IsDeviceAllowed checks whether deviceID is permitted by the allowlist.
func IsDeviceAllowed(deviceID string, allowlist []string, strict bool) bool {
	deviceID = strings.TrimSpace(deviceID)
	if deviceID == "" {
		return false
	}
	if len(allowlist) == 0 {
		return !strict
	}
	for _, allowed := range allowlist {
		if subtle.ConstantTimeCompare([]byte(deviceID), []byte(strings.TrimSpace(allowed))) == 1 {
			return true
		}
	}
	return false
}

// CertFingerprintSHA256 returns the lowercase hex SHA-256 fingerprint of a DER-encoded X.509 certificate.
func CertFingerprintSHA256(rawCert []byte) string {
	sum := sha256.Sum256(rawCert)
	return hex.EncodeToString(sum[:])
}

// EnsureSelfSignedCert loads an existing ECDSA P-256 cert/key pair or generates a new one.
// Returns the tls.Certificate and its lowercase hex SHA-256 fingerprint.
func EnsureSelfSignedCert(certPath, keyPath, commonName string) (tls.Certificate, string, error) {
	if _, err := os.Stat(certPath); err == nil {
		if _, err := os.Stat(keyPath); err == nil {
			cert, err := tls.LoadX509KeyPair(certPath, keyPath)
			if err == nil && len(cert.Certificate) > 0 {
				return cert, CertFingerprintSHA256(cert.Certificate[0]), nil
			}
		}
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if commonName == "" {
		commonName = "xb-host"
	}
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"XiaobaoDesk"},
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{commonName, "localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, "", err
	}

	if err := os.MkdirAll(filepath.Dir(certPath), 0700); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return tls.Certificate{}, "", err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return tls.Certificate{}, "", err
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	return cert, CertFingerprintSHA256(derBytes), nil
}

// NewServerTLSConfig builds a strict TLS 1.3 server configuration.
func NewServerTLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
		MaxVersion:   tls.VersionTLS13,
	}
}

// NewPinnedClientTLSConfig builds a strict TLS 1.3 client configuration that verifies
// the peer certificate SHA-256 fingerprint in constant time.
func NewPinnedClientTLSConfig(expectedFingerprintHex string) *tls.Config {
	expected := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(expectedFingerprintHex), ":", ""))
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // Verified manually via VerifyPeerCertificate SHA-256 pinning
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("tls pinning: server presented no certificates")
			}
			if expected == "" {
				return errors.New("tls pinning: expected hub_cert_sha256 is empty")
			}
			actual := CertFingerprintSHA256(rawCerts[0])
			if subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) != 1 {
				return fmt.Errorf("tls pinning: certificate SHA-256 mismatch (got %s)", actual)
			}
			return nil
		},
	}
}
