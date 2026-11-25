package security

import (
	"crypto/tls"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/xiaobaodesk/win-host/internal/protocol"
)

func TestPBKDF2AndHMAC(t *testing.T) {
	dk1 := PBKDF2SHA256([]byte("secret-pass"), []byte("salt-123"), 1000, 32)
	dk2 := PBKDF2SHA256([]byte("secret-pass"), []byte("salt-123"), 1000, 32)
	if len(dk1) != 32 || string(dk1) != string(dk2) {
		t.Fatalf("PBKDF2 not deterministic")
	}

	nonce := "0123456789abcdef0123456789abcdef"
	macHex := ComputeAuthHMAC(nonce, dk1)
	if !VerifyAuthHMAC(nonce, macHex, dk1) {
		t.Fatalf("VerifyAuthHMAC failed for valid MAC")
	}
	if VerifyAuthHMAC(nonce, macHex, []byte("wrong-key")) {
		t.Fatalf("VerifyAuthHMAC accepted wrong key")
	}
}

func TestTOTPGenerationAndVerification(t *testing.T) {
	// RFC 6238 / RFC 4226 base32 secret
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}
	now := time.Unix(1700000000, 0)
	code, err := GenerateTOTP(secret, now)
	if err != nil {
		t.Fatalf("GenerateTOTP: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("expected 6-digit TOTP, got %q", code)
	}
	if !VerifyTOTP(secret, code, now, 1) {
		t.Fatalf("VerifyTOTP rejected current code")
	}
	if !VerifyTOTP(secret, code, now.Add(30*time.Second), 1) {
		t.Fatalf("VerifyTOTP rejected +1 skew code")
	}
	if VerifyTOTP(secret, code, now.Add(90*time.Second), 1) {
		t.Fatalf("VerifyTOTP accepted expired code outside skew")
	}
	if VerifyTOTP(secret, "000000", now, 0) && code != "000000" {
		t.Fatalf("VerifyTOTP accepted wrong code")
	}
}

func TestDeviceAllowlist(t *testing.T) {
	if IsDeviceAllowed("", nil, false) {
		t.Fatalf("empty device_id must be rejected")
	}
	if IsDeviceAllowed("dev-1", nil, true) {
		t.Fatalf("strict allowlist with empty list must reject")
	}
	if !IsDeviceAllowed("dev-1", nil, false) {
		t.Fatalf("non-strict empty allowlist should allow")
	}
	if !IsDeviceAllowed("dev-2", []string{"dev-1", "dev-2"}, true) {
		t.Fatalf("expected dev-2 to be allowed")
	}
	if IsDeviceAllowed("dev-3", []string{"dev-1", "dev-2"}, true) {
		t.Fatalf("expected dev-3 to be rejected")
	}
}

func TestTLSPinningAndGateHandshake(t *testing.T) {
	tmp := t.TempDir()
	cert, fp, err := EnsureSelfSignedCert(filepath.Join(tmp, "c.pem"), filepath.Join(tmp, "k.pem"), "test-host")
	if err != nil {
		t.Fatalf("EnsureSelfSignedCert: %v", err)
	}
	if len(fp) != 64 {
		t.Fatalf("expected 64-char sha256 hex, got %d", len(fp))
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", NewServerTLSConfig(cert))
	if err != nil {
		t.Fatalf("tls.Listen: %v", err)
	}
	defer ln.Close()

	// Wrong fingerprint must fail handshake
	badCfg := NewPinnedClientTLSConfig("0000000000000000000000000000000000000000000000000000000000000000")
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.(*tls.Conn).Handshake()
			_ = c.Close()
		}
	}()
	if c, err := tls.Dial("tcp", ln.Addr().String(), badCfg); err == nil {
		_ = c.Close()
		t.Fatalf("expected TLS pinning mismatch to fail")
	}

	// Valid fingerprint + full AuthenticateClient gate
	totpSec, _ := GenerateTOTPSecret()
	policy := StaticPolicy{
		Secrets:          [][]byte{[]byte("super-secret")},
		TOTPSecret:       totpSec,
		RequireTOTP:      true,
		AllowedDeviceIDs: []string{"android-pixel-9"},
		StrictAllowlist:  true,
	}

	errCh := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer c.Close()
		fc := protocol.NewFrameConn(c)
		_, dev, err := AuthenticateClient(c, fc, policy)
		if err != nil {
			errCh <- err
			return
		}
		if dev != "android-pixel-9" {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	goodConn, err := tls.Dial("tcp", ln.Addr().String(), NewPinnedClientTLSConfig(fp))
	if err != nil {
		t.Fatalf("tls.Dial with valid pin: %v", err)
	}
	defer goodConn.Close()
	clientFC := protocol.NewFrameConn(goodConn)

	chFrame, err := clientFC.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame challenge: %v", err)
	}
	var ch protocol.ChallengeMsg
	if err := json.Unmarshal(chFrame.Payload, &ch); err != nil {
		t.Fatalf("unmarshal challenge: %v", err)
	}
	code, _ := GenerateTOTP(totpSec, time.Now())
	if err := clientFC.WriteCTRL(protocol.AuthMsg{
		Type:     "auth",
		DeviceID: "android-pixel-9",
		Role:     "client",
		HMAC:     ComputeAuthHMAC(ch.Nonce, []byte("super-secret")),
		TOTP:     code,
	}); err != nil {
		t.Fatalf("WriteCTRL auth: %v", err)
	}
	okFrame, err := clientFC.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame auth_ok: %v", err)
	}
	var okMsg protocol.AuthOKMsg
	if err := json.Unmarshal(okFrame.Payload, &okMsg); err != nil || okMsg.Type != "auth_ok" || okMsg.SessionID == "" {
		t.Fatalf("unexpected auth response: %s", string(okFrame.Payload))
	}

	if srvErr := <-errCh; srvErr != nil {
		t.Fatalf("server AuthenticateClient error: %v", srvErr)
	}

	// Also verify AuthenticateClient rejects invalid TOTP over net.Pipe
	p1, p2 := net.Pipe()
	defer p1.Close()
	defer p2.Close()
	go func() {
		fc1 := protocol.NewFrameConn(p1)
		_, _, err := AuthenticateClient(p1, fc1, policy)
		errCh <- err
	}()
	fc2 := protocol.NewFrameConn(p2)
	f, _ := fc2.ReadFrame()
	var ch2 protocol.ChallengeMsg
	_ = json.Unmarshal(f.Payload, &ch2)
	_ = fc2.WriteCTRL(protocol.AuthMsg{
		Type:     "auth",
		DeviceID: "android-pixel-9",
		Role:     "client",
		HMAC:     ComputeAuthHMAC(ch2.Nonce, []byte("super-secret")),
		TOTP:     "999999",
	})
	resp2, _ := fc2.ReadFrame()
	var env2 protocol.CtrlEnvelope
	_ = json.Unmarshal(resp2.Payload, &env2)
	if env2.Type != "auth_err" {
		t.Fatalf("expected auth_err for bad TOTP, got %s", string(resp2.Payload))
	}
	if srvErr := <-errCh; srvErr == nil {
		t.Fatalf("expected server error for bad TOTP")
	}
}

func TestGateIgnoresHelloBeforeAuth(t *testing.T) {
	policy := StaticPolicy{
		Secrets:          [][]byte{[]byte("super-secret")},
		RequireTOTP:      false,
		AllowedDeviceIDs: []string{"android-client-1"},
		StrictAllowlist:  true,
	}

	p1, p2 := net.Pipe()
	defer p1.Close()
	defer p2.Close()

	type gateResult struct {
		dev string
		err error
	}
	resCh := make(chan gateResult, 1)
	go func() {
		fc1 := protocol.NewFrameConn(p1)
		_, dev, err := AuthenticateClient(p1, fc1, policy)
		resCh <- gateResult{dev: dev, err: err}
	}()

	fc2 := protocol.NewFrameConn(p2)
	f, err := fc2.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame challenge: %v", err)
	}
	var ch protocol.ChallengeMsg
	if err := json.Unmarshal(f.Payload, &ch); err != nil {
		t.Fatalf("unmarshal challenge: %v", err)
	}

	// Send hello before auth (as Android XbConnectionManager.kt does)
	if err := fc2.WriteCTRL(map[string]any{"type": "hello", "device_id": "android-client-1", "role": "client"}); err != nil {
		t.Fatalf("WriteCTRL hello: %v", err)
	}
	if err := fc2.WriteCTRL(protocol.AuthMsg{
		Type:     "auth",
		DeviceID: "android-client-1",
		Role:     "client",
		HMAC:     ComputeAuthHMAC(ch.Nonce, []byte("super-secret")),
	}); err != nil {
		t.Fatalf("WriteCTRL auth: %v", err)
	}

	resp, err := fc2.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame auth_ok: %v", err)
	}
	var okMsg protocol.AuthOKMsg
	if err := json.Unmarshal(resp.Payload, &okMsg); err != nil || okMsg.Type != "auth_ok" || okMsg.SessionID == "" {
		t.Fatalf("expected auth_ok, got %s", string(resp.Payload))
	}

	res := <-resCh
	if res.err != nil || res.dev != "android-client-1" {
		t.Fatalf("AuthenticateClient failed: dev=%q err=%v", res.dev, res.err)
	}
}
