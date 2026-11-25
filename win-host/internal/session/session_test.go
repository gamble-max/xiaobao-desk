package session

import (
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/xiaobaodesk/win-host/internal/config"
	"github.com/xiaobaodesk/win-host/internal/protocol"
	"github.com/xiaobaodesk/win-host/internal/security"
)

func TestEnableTerminalFalseBlocksConPTY(t *testing.T) {
	srvConn, cliConn := net.Pipe()
	defer srvConn.Close()
	defer cliConn.Close()

	srvFC := protocol.NewFrameConn(srvConn)
	cliFC := protocol.NewFrameConn(cliConn)

	cfg := &config.Config{
		HostPassword:   "TestStrongPassword!123",
		EnableTerminal: false,
		FileRootDir:    t.TempDir(),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		HandleConnection(srvConn, srvFC, cfg, nil, "test")
	}()

	// Complete challenge-response authentication as client.
	chFrame, err := cliFC.ReadFrame()
	if err != nil {
		t.Fatalf("read challenge: %v", err)
	}
	var ch protocol.ChallengeMsg
	if err := json.Unmarshal(chFrame.Payload, &ch); err != nil {
		t.Fatalf("unmarshal challenge: %v", err)
	}

	hmacHex := security.ComputeAuthHMAC(ch.Nonce, []byte(cfg.HostPassword))
	if err := cliFC.WriteCTRL(protocol.AuthMsg{
		Type:     "auth",
		DeviceID: "test-device",
		Role:     "client",
		HMAC:     hmacHex,
	}); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	authResp, err := cliFC.ReadFrame()
	if err != nil {
		t.Fatalf("read auth_ok: %v", err)
	}
	var env protocol.CtrlEnvelope
	if err := json.Unmarshal(authResp.Payload, &env); err != nil || env.Type != "auth_ok" {
		t.Fatalf("expected auth_ok, got %s", string(authResp.Payload))
	}

	// Send TermOpen when EnableTerminal == false.
	if err := cliFC.WriteSubcmdJSON(protocol.ChannelTERM, protocol.TermOpen, protocol.TermSizeMsg{Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("write TermOpen: %v", err)
	}
	termResp, err := cliFC.ReadFrame()
	if err != nil {
		t.Fatalf("read TermOpen response: %v", err)
	}
	if termResp.Channel != protocol.ChannelTERM || len(termResp.Payload) < 2 || termResp.Payload[0] != protocol.TermStdout {
		t.Fatalf("expected ChannelTERM TermStdout error message, got ch=0x%02x", termResp.Channel)
	}
	if !strings.Contains(string(termResp.Payload[1:]), "disabled") {
		t.Fatalf("expected explicit disabled message, got %q", string(termResp.Payload[1:]))
	}

	// Send TermStdin when EnableTerminal == false.
	if err := cliFC.WriteSubcmdBytes(protocol.ChannelTERM, protocol.TermStdin, []byte("whoami\r\n")); err != nil {
		t.Fatalf("write TermStdin: %v", err)
	}
	stdinResp, err := cliFC.ReadFrame()
	if err != nil {
		t.Fatalf("read TermStdin response: %v", err)
	}
	if stdinResp.Channel != protocol.ChannelTERM || len(stdinResp.Payload) < 2 || stdinResp.Payload[0] != protocol.TermStdout {
		t.Fatalf("expected ChannelTERM TermStdout disabled message, got ch=0x%02x", stdinResp.Channel)
	}
	if !strings.Contains(string(stdinResp.Payload[1:]), "disabled") {
		t.Fatalf("expected explicit disabled message for TermStdin, got %q", string(stdinResp.Payload[1:]))
	}

	_ = cliFC.Close()
	<-done
}
