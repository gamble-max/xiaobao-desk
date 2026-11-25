package auth

import "testing"

func TestNonceAndHMAC(t *testing.T) {
	n1, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce: %v", err)
	}
	n2, err := GenerateNonce()
	if err != nil {
		t.Fatalf("GenerateNonce 2: %v", err)
	}
	if len(n1) != 64 || len(n2) != 64 {
		t.Fatalf("expected 64-char hex nonces, got %d and %d", len(n1), len(n2))
	}
	if n1 == n2 {
		t.Fatalf("expected distinct nonces")
	}

	secret := "super-secret-enrollment-token"
	sig := ComputeHMAC(n1, secret)
	if !VerifyHMAC(n1, secret, sig) {
		t.Fatalf("expected valid HMAC to verify")
	}
	if VerifyHMAC(n1, "wrong-secret", sig) {
		t.Fatalf("expected wrong secret to fail")
	}
	if VerifyHMAC(n2, secret, sig) {
		t.Fatalf("expected wrong nonce to fail")
	}
	if VerifyHMAC(n1, secret, "not-hex") {
		t.Fatalf("expected malformed hex to fail")
	}
	if VerifyHMAC(n1, "", sig) || VerifyHMAC("", secret, sig) || VerifyHMAC(n1, secret, "") {
		t.Fatalf("expected empty inputs to fail")
	}
}

func TestIsDeviceAllowed(t *testing.T) {
	if IsDeviceAllowed("", nil) {
		t.Fatalf("empty device ID must be rejected")
	}
	if !IsDeviceAllowed("dev-1", nil) {
		t.Fatalf("empty allowlist should allow non-empty device ID")
	}
	if !IsDeviceAllowed("dev-1", []string{"dev-1", "dev-2"}) {
		t.Fatalf("expected dev-1 to be allowed")
	}
	if IsDeviceAllowed("dev-3", []string{"dev-1", "dev-2"}) {
		t.Fatalf("expected dev-3 to be rejected")
	}
}
