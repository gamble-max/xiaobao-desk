package security

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/xiaobaodesk/win-host/internal/protocol"
)

// AuthPolicy defines the host security gate policy.
type AuthPolicy interface {
	CandidateAuthSecrets() [][]byte
	GetTOTPSecret() string
	IsTOTPRequired() bool
	GetAllowedDeviceIDs() []string
	IsStrictAllowlist() bool
}

// StaticPolicy is a simple AuthPolicy implementation for tests and config.
type StaticPolicy struct {
	Secrets          [][]byte
	TOTPSecret       string
	RequireTOTP      bool
	AllowedDeviceIDs []string
	StrictAllowlist  bool
}

func (p StaticPolicy) CandidateAuthSecrets() [][]byte { return p.Secrets }
func (p StaticPolicy) GetTOTPSecret() string          { return p.TOTPSecret }
func (p StaticPolicy) IsTOTPRequired() bool           { return p.RequireTOTP }
func (p StaticPolicy) GetAllowedDeviceIDs() []string  { return p.AllowedDeviceIDs }
func (p StaticPolicy) IsStrictAllowlist() bool        { return p.StrictAllowlist }

// AuthenticateClient runs the PROTOCOL.md Section 3 challenge-response + TOTP + allowlist gate.
func AuthenticateClient(conn net.Conn, fc *protocol.FrameConn, policy AuthPolicy) (sessionID, deviceID string, err error) {
	if conn != nil {
		_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
		defer func() { _ = conn.SetDeadline(time.Time{}) }()
	}

	nonce, err := RandomHex(32)
	if err != nil {
		return "", "", err
	}
	totpReq := policy.IsTOTPRequired() && policy.GetTOTPSecret() != ""
	if err := fc.WriteCTRL(protocol.ChallengeMsg{
		Type:         "challenge",
		Nonce:        nonce,
		TOTPRequired: totpReq,
	}); err != nil {
		return "", "", err
	}

	var auth protocol.AuthMsg
	for {
		frame, err := fc.ReadFrame()
		if err != nil {
			return "", "", err
		}
		if frame.Channel != protocol.ChannelCTRL {
			_ = fc.WriteCTRL(protocol.AuthErrMsg{Type: "auth_err", Reason: "expected CTRL channel"})
			return "", "", errors.New("auth: expected CTRL channel")
		}
		var env protocol.CtrlEnvelope
		if err := json.Unmarshal(frame.Payload, &env); err == nil && env.Type == "hello" {
			continue
		}
		if err := json.Unmarshal(frame.Payload, &auth); err != nil || auth.Type != "auth" {
			_ = fc.WriteCTRL(protocol.AuthErrMsg{Type: "auth_err", Reason: "invalid auth message"})
			return "", "", errors.New("auth: invalid auth message")
		}
		break
	}

	if !IsDeviceAllowed(auth.DeviceID, policy.GetAllowedDeviceIDs(), policy.IsStrictAllowlist()) {
		_ = fc.WriteCTRL(protocol.AuthErrMsg{Type: "auth_err", Reason: "device_id not allowed"})
		return "", "", fmt.Errorf("auth: device_id %q not allowed", auth.DeviceID)
	}

	if !VerifyAuthHMAC(nonce, auth.HMAC, policy.CandidateAuthSecrets()...) {
		_ = fc.WriteCTRL(protocol.AuthErrMsg{Type: "auth_err", Reason: "invalid credentials"})
		return "", "", errors.New("auth: invalid HMAC")
	}

	if totpReq {
		if !VerifyTOTP(policy.GetTOTPSecret(), auth.TOTP, time.Now(), 1) {
			_ = fc.WriteCTRL(protocol.AuthErrMsg{Type: "auth_err", Reason: "invalid totp"})
			return "", "", errors.New("auth: invalid TOTP")
		}
	}

	sessionID, err = RandomUUIDv4()
	if err != nil {
		return "", "", err
	}
	if err := fc.WriteCTRL(protocol.AuthOKMsg{
		Type:      "auth_ok",
		SessionID: sessionID,
	}); err != nil {
		return "", "", err
	}
	return sessionID, auth.DeviceID, nil
}
