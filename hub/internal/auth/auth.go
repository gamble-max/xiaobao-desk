package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

// GenerateNonce returns a 32-byte random nonce encoded as 64 lowercase hex characters.
func GenerateNonce() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ComputeHMAC computes lowercase hex HMAC-SHA256(key=secret, msg=nonce).
func ComputeHMAC(nonce, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(nonce))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyHMAC performs a constant-time comparison of the provided hex HMAC against ComputeHMAC(nonce, secret).
func VerifyHMAC(nonce, secret, providedHex string) bool {
	if secret == "" || nonce == "" || providedHex == "" {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimSpace(providedHex))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(nonce))
	expected := mac.Sum(nil)
	return hmac.Equal(expected, provided)
}

// ConstantTimeEqual compares two strings in constant time when their lengths match.
func ConstantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// IsDeviceAllowed returns true if allowlist is empty or deviceID is present in allowlist.
func IsDeviceAllowed(deviceID string, allowlist []string) bool {
	if deviceID == "" {
		return false
	}
	if len(allowlist) == 0 {
		return true
	}
	for _, id := range allowlist {
		if ConstantTimeEqual(id, deviceID) {
			return true
		}
	}
	return false
}
