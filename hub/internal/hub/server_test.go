package hub_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net"
	"path/filepath"
	"testing"
	"time"

	"xiaobaodesk/hub/internal/auth"
	"xiaobaodesk/hub/internal/config"
	"xiaobaodesk/hub/internal/hub"
	"xiaobaodesk/hub/internal/proto"
	"xiaobaodesk/hub/internal/tlsutil"
)

func startTestHub(t *testing.T, allowedDevices []string) (tcpAddr string, udpAddr string, secret string, certFP string, cancel context.CancelFunc) {
	t.Helper()
	dir := t.TempDir()
	cfg, _, err := config.LoadOrInit(filepath.Join(dir, "hub-config.json"), dir)
	if err != nil {
		t.Fatalf("LoadOrInit: %v", err)
	}
	cfg.AllowedDeviceIDs = allowedDevices
	cfg.RelayTokenTTLSeconds = 5

	cert, fp, err := tlsutil.EnsureCert(cfg.CertPath, cfg.KeyPath, cfg.FingerprintPath)
	if err != nil {
		t.Fatalf("EnsureCert: %v", err)
	}

	tcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("tcp listen: %v", err)
	}
	udpPC, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		_ = tcpLn.Close()
		t.Fatalf("udp listen: %v", err)
	}

	ctx, cancelFn := context.WithCancel(context.Background())
	srv := hub.NewServer(cfg, tlsutil.ServerTLSConfig(cert), log.New(io.Discard, "", 0))

	go func() { _ = srv.ServeTCP(ctx, tcpLn) }()
	go func() { _ = srv.ServeUDP(ctx, udpPC) }()

	t.Cleanup(func() {
		cancelFn()
		_ = tcpLn.Close()
		_ = udpPC.Close()
	})

	return tcpLn.Addr().String(), udpPC.LocalAddr().String(), cfg.AuthSecret, fp, cancelFn
}

func dialPinnedTLS(t *testing.T, addr, expectedFP string) *tls.Conn {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // Verified manually via SHA-256 pinning below
	})
	if err != nil {
		t.Fatalf("tls.Dial: %v", err)
	}
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		t.Fatalf("no peer certificates")
	}
	gotFP := tlsutil.CertSHA256Hex(certs[0].Raw)
	if gotFP != expectedFP {
		_ = conn.Close()
		t.Fatalf("cert fingerprint mismatch: got %s want %s", gotFP, expectedFP)
	}
	return conn
}

func authenticateConn(t *testing.T, conn net.Conn, deviceID, role, secret string, sendHello bool) map[string]any {
	t.Helper()
	if sendHello {
		if err := proto.WriteCtrl(conn, map[string]any{
			"type":      "hello",
			"device_id": deviceID,
			"role":      role,
		}); err != nil {
			t.Fatalf("write hello: %v", err)
		}
	}

	var ch map[string]any
	if _, err := proto.ReadCtrl(conn, &ch); err != nil {
		t.Fatalf("read challenge: %v", err)
	}
	if ch["type"] != "challenge" {
		t.Fatalf("expected challenge, got %v", ch)
	}
	nonce, _ := ch["nonce"].(string)
	sig := auth.ComputeHMAC(nonce, secret)

	if err := proto.WriteCtrl(conn, map[string]any{
		"type":      "auth",
		"device_id": deviceID,
		"role":      role,
		"hmac":      sig,
	}); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	var resp map[string]any
	if _, err := proto.ReadCtrl(conn, &resp); err != nil {
		t.Fatalf("read auth resp: %v", err)
	}
	return resp
}

func TestAuthAndPeerRegisterQuery(t *testing.T) {
	tcpAddr, _, secret, fp, _ := startTestHub(t, []string{"host-win-1", "client-android-1"})

	// 1. Disallowed device rejected
	badConn := dialPinnedTLS(t, tcpAddr, fp)
	badResp := authenticateConn(t, badConn, "rogue-device", "client", secret, false)
	if badResp["type"] != "auth_err" {
		t.Fatalf("expected auth_err for disallowed device, got %v", badResp)
	}
	_ = badConn.Close()

	// 2. Wrong HMAC rejected
	wrongSigConn := dialPinnedTLS(t, tcpAddr, fp)
	wrongResp := authenticateConn(t, wrongSigConn, "client-android-1", "client", "wrong-secret", true)
	if wrongResp["type"] != "auth_err" {
		t.Fatalf("expected auth_err for wrong hmac, got %v", wrongResp)
	}
	_ = wrongSigConn.Close()

	// 3. Valid host registers endpoints
	hostConn := dialPinnedTLS(t, tcpAddr, fp)
	defer hostConn.Close()
	if resp := authenticateConn(t, hostConn, "host-win-1", "host", secret, true); resp["type"] != "auth_ok" {
		t.Fatalf("host auth failed: %v", resp)
	}

	if err := proto.WriteCtrl(hostConn, map[string]any{
		"type":      "peer_register",
		"host_id":   "win-pc",
		"endpoints": []string{"[2001:db8::10]:21118", "192.168.1.50:21118"},
	}); err != nil {
		t.Fatalf("peer_register: %v", err)
	}
	var regResp map[string]any
	if _, err := proto.ReadCtrl(hostConn, &regResp); err != nil {
		t.Fatalf("read peer_register_ok: %v", err)
	}
	if regResp["type"] != "peer_register_ok" {
		t.Fatalf("expected peer_register_ok, got %v", regResp)
	}

	// 4. Valid client queries peer endpoints + ping/pong
	clientConn := dialPinnedTLS(t, tcpAddr, fp)
	defer clientConn.Close()
	if resp := authenticateConn(t, clientConn, "client-android-1", "client", secret, false); resp["type"] != "auth_ok" {
		t.Fatalf("client auth failed: %v", resp)
	}

	if err := proto.WriteCtrl(clientConn, map[string]any{"type": "ping", "ts": 42}); err != nil {
		t.Fatalf("ping: %v", err)
	}
	var pong map[string]any
	if _, err := proto.ReadCtrl(clientConn, &pong); err != nil {
		t.Fatalf("pong: %v", err)
	}
	if pong["type"] != "pong" {
		t.Fatalf("expected pong, got %v", pong)
	}

	if err := proto.WriteCtrl(clientConn, map[string]any{"type": "peer_query", "host_id": "win-pc"}); err != nil {
		t.Fatalf("peer_query: %v", err)
	}
	var qResp map[string]any
	if _, err := proto.ReadCtrl(clientConn, &qResp); err != nil {
		t.Fatalf("read peer_endpoints: %v", err)
	}
	if qResp["type"] != "peer_endpoints" || qResp["found"] != true || qResp["online"] != true {
		t.Fatalf("unexpected peer_endpoints: %v", qResp)
	}
	eps, ok := qResp["endpoints"].([]any)
	if !ok || len(eps) != 2 {
		t.Fatalf("expected 2 endpoints, got %v", qResp["endpoints"])
	}
}

func TestRelayBindBidirectionalSplice(t *testing.T) {
	tcpAddr, _, secret, fp, _ := startTestHub(t, nil)

	// Host connects control stream and registers
	hostCtrl := dialPinnedTLS(t, tcpAddr, fp)
	defer hostCtrl.Close()
	if resp := authenticateConn(t, hostCtrl, "host-1", "host", secret, true); resp["type"] != "auth_ok" {
		t.Fatalf("host auth: %v", resp)
	}
	if err := proto.WriteCtrl(hostCtrl, map[string]any{
		"type":      "peer_register",
		"host_id":   "host-1",
		"endpoints": []string{"192.168.1.50:21118"},
	}); err != nil {
		t.Fatalf("peer_register: %v", err)
	}
	var regOK map[string]any
	if _, err := proto.ReadCtrl(hostCtrl, &regOK); err != nil {
		t.Fatalf("regOK: %v", err)
	}

	// Host goroutine listens on hostCtrl for relay_bind notification, dials relay stream, and echoes frames
	hostDone := make(chan error, 1)
	go func() {
		var req map[string]any
		if _, err := proto.ReadCtrl(hostCtrl, &req); err != nil {
			hostDone <- err
			return
		}
		if req["type"] != "relay_bind" {
			hostDone <- io.ErrUnexpectedEOF
			return
		}
		sessID, _ := req["session_id"].(string)

		hRelay := dialPinnedTLS(t, tcpAddr, fp)
		defer hRelay.Close()
		if resp := authenticateConn(t, hRelay, "host-1", "host", secret, false); resp["type"] != "auth_ok" {
			hostDone <- io.ErrUnexpectedEOF
			return
		}
		if err := proto.WriteCtrl(hRelay, map[string]any{
			"type":       "relay_bind",
			"session_id": sessID,
			"role":       "host",
		}); err != nil {
			hostDone <- err
			return
		}
		var ready map[string]any
		if _, err := proto.ReadCtrl(hRelay, &ready); err != nil {
			hostDone <- err
			return
		}

		// Read ChanInput frame from client and reply with ChanVideo frame
		f, err := proto.ReadFrame(hRelay, proto.DefaultMaxSize)
		if err != nil {
			hostDone <- err
			return
		}
		if f.Channel != proto.ChanInput || string(f.Payload) != `{"type":"mouse_move","x":0.5,"y":0.5}` {
			hostDone <- io.ErrUnexpectedEOF
			return
		}
		if err := proto.WriteFrame(hRelay, proto.ChanVideo, []byte{0x00, 0x00, 0x00, 0x01, 0x65}); err != nil {
			hostDone <- err
			return
		}
		hostDone <- nil
	}()

	// Client dials relay stream
	clientRelay := dialPinnedTLS(t, tcpAddr, fp)
	defer clientRelay.Close()
	if resp := authenticateConn(t, clientRelay, "client-1", "client", secret, true); resp["type"] != "auth_ok" {
		t.Fatalf("client auth: %v", resp)
	}
	if err := proto.WriteCtrl(clientRelay, map[string]any{
		"type":       "relay_bind",
		"host_id":    "host-1",
		"session_id": "sess-relay-123",
		"role":       "client",
	}); err != nil {
		t.Fatalf("client relay_bind: %v", err)
	}
	var ready map[string]any
	if _, err := proto.ReadCtrl(clientRelay, &ready); err != nil {
		t.Fatalf("client relay_ready: %v", err)
	}
	if ready["type"] != "relay_ready" || ready["session_id"] != "sess-relay-123" {
		t.Fatalf("unexpected relay_ready: %v", ready)
	}

	// Send input frame -> expect video frame back over spliced TLS stream
	inputPayload := []byte(`{"type":"mouse_move","x":0.5,"y":0.5}`)
	if err := proto.WriteFrame(clientRelay, proto.ChanInput, inputPayload); err != nil {
		t.Fatalf("client write input frame: %v", err)
	}
	vf, err := proto.ReadFrame(clientRelay, proto.DefaultMaxSize)
	if err != nil {
		t.Fatalf("client read video frame: %v", err)
	}
	if vf.Channel != proto.ChanVideo || !bytes.Equal(vf.Payload, []byte{0x00, 0x00, 0x00, 0x01, 0x65}) {
		t.Fatalf("unexpected video frame: ch=0x%02x payload=%v", vf.Channel, vf.Payload)
	}

	select {
	case err := <-hostDone:
		if err != nil {
			t.Fatalf("host relay error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for host relay goroutine")
	}
}

func TestUDPReflectionAndHolePunch(t *testing.T) {
	_, udpAddr, _, _, _ := startTestHub(t, nil)

	hostUDP, err := net.Dial("udp", udpAddr)
	if err != nil {
		t.Fatalf("host dial udp: %v", err)
	}
	defer hostUDP.Close()

	pingBytes, _ := json.Marshal(map[string]any{
		"type":    "udp_ping",
		"host_id": "host-udp-1",
		"ts":      100,
	})
	if _, err := hostUDP.Write(pingBytes); err != nil {
		t.Fatalf("host udp write: %v", err)
	}

	buf := make([]byte, 2048)
	_ = hostUDP.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := hostUDP.Read(buf)
	if err != nil {
		t.Fatalf("host udp read: %v", err)
	}
	var pong map[string]any
	if err := json.Unmarshal(buf[:n], &pong); err != nil {
		t.Fatalf("unmarshal pong: %v", err)
	}
	if pong["type"] != "udp_pong" || pong["observed_addr"] == "" {
		t.Fatalf("unexpected udp_pong: %v", pong)
	}

	// Client sends punch_req
	clientUDP, err := net.Dial("udp", udpAddr)
	if err != nil {
		t.Fatalf("client dial udp: %v", err)
	}
	defer clientUDP.Close()

	punchReq, _ := json.Marshal(map[string]any{
		"type":           "punch_req",
		"host_id":        "host-udp-1",
		"from_device_id": "android-1",
		"session_id":     "punch-1",
	})
	if _, err := clientUDP.Write(punchReq); err != nil {
		t.Fatalf("client punch_req: %v", err)
	}

	_ = clientUDP.SetReadDeadline(time.Now().Add(3 * time.Second))
	cn, err := clientUDP.Read(buf)
	if err != nil {
		t.Fatalf("client read punch_ack: %v", err)
	}
	var ack map[string]any
	if err := json.Unmarshal(buf[:cn], &ack); err != nil {
		t.Fatalf("unmarshal punch_ack: %v", err)
	}
	if ack["type"] != "punch_ack" || ack["found"] != true || ack["host_udp"] != pong["observed_addr"] {
		t.Fatalf("unexpected punch_ack: %v", ack)
	}

	// Host should receive punch_notify
	_ = hostUDP.SetReadDeadline(time.Now().Add(3 * time.Second))
	hn, err := hostUDP.Read(buf)
	if err != nil {
		t.Fatalf("host read punch_notify: %v", err)
	}
	var notify map[string]any
	if err := json.Unmarshal(buf[:hn], &notify); err != nil {
		t.Fatalf("unmarshal punch_notify: %v", err)
	}
	if notify["type"] != "punch_notify" || notify["peer_addr"] != ack["your_udp"] {
		t.Fatalf("unexpected punch_notify: %v", notify)
	}
}

func TestCertSHA256AndEmptyHostIDResolution(t *testing.T) {
	tcpAddr, _, secret, fp, _ := startTestHub(t, nil)

	hostConn := dialPinnedTLS(t, tcpAddr, fp)
	defer hostConn.Close()
	if resp := authenticateConn(t, hostConn, "host-auto-1", "host", secret, true); resp["type"] != "auth_ok" {
		t.Fatalf("host auth failed: %v", resp)
	}

	const hostCertFP = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	if err := proto.WriteCtrl(hostConn, map[string]any{
		"type":        "peer_register",
		"host_id":     "win-sole-host",
		"cert_sha256": hostCertFP,
		"endpoints":   []string{"192.168.1.99:21118"},
	}); err != nil {
		t.Fatalf("peer_register: %v", err)
	}
	var regResp map[string]any
	if _, err := proto.ReadCtrl(hostConn, &regResp); err != nil {
		t.Fatalf("read peer_register_ok: %v", err)
	}

	clientConn := dialPinnedTLS(t, tcpAddr, fp)
	defer clientConn.Close()
	if resp := authenticateConn(t, clientConn, "client-auto-1", "client", secret, true); resp["type"] != "auth_ok" {
		t.Fatalf("client auth failed: %v", resp)
	}

	for _, queryID := range []string{"", "default", "default-host"} {
		if err := proto.WriteCtrl(clientConn, map[string]any{
			"type":    "peer_query",
			"host_id": queryID,
		}); err != nil {
			t.Fatalf("peer_query(%q): %v", queryID, err)
		}
		var qResp map[string]any
		if _, err := proto.ReadCtrl(clientConn, &qResp); err != nil {
			t.Fatalf("read peer_endpoints(%q): %v", queryID, err)
		}
		if qResp["found"] != true || qResp["host_id"] != "win-sole-host" || qResp["cert_sha256"] != hostCertFP {
			t.Fatalf("unexpected peer_endpoints(%q): %v", queryID, qResp)
		}
	}

	// Verify relay_bind with empty host_id also notifies the sole active host.
	if err := proto.WriteCtrl(clientConn, map[string]any{
		"type":       "relay_bind",
		"host_id":    "",
		"session_id": "sess-auto-host",
		"role":       "client",
	}); err != nil {
		t.Fatalf("relay_bind empty host_id: %v", err)
	}
	_ = hostConn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var hostNotify map[string]any
	if _, err := proto.ReadCtrl(hostConn, &hostNotify); err != nil {
		t.Fatalf("host read relay_bind notify: %v", err)
	}
	if hostNotify["type"] != "relay_bind" || hostNotify["host_id"] != "win-sole-host" || hostNotify["session_id"] != "sess-auto-host" {
		t.Fatalf("unexpected host relay_bind notify: %v", hostNotify)
	}
}

func TestAuthRateLimitBlocksAfterFiveFailures(t *testing.T) {
	tcpAddr, _, _, fp, _ := startTestHub(t, nil)

	for i := 0; i < 5; i++ {
		c := dialPinnedTLS(t, tcpAddr, fp)
		resp := authenticateConn(t, c, "client-brute", "client", "wrong-secret", false)
		if resp["type"] != "auth_err" {
			t.Fatalf("attempt %d: expected auth_err, got %v", i+1, resp)
		}
		_ = c.Close()
	}

	// 6th attempt from the same IP should be dropped immediately before TLS handshake / challenge.
	blockedConn, err := tls.Dial("tcp", tcpAddr, &tls.Config{
		MinVersion:         tls.VersionTLS13,
		MaxVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true,
	})
	if err == nil {
		defer blockedConn.Close()
		_ = blockedConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var ch map[string]any
		if _, err := proto.ReadCtrl(blockedConn, &ch); err == nil {
			t.Fatalf("expected blocked connection to be closed without challenge, got %v", ch)
		}
	}
}
