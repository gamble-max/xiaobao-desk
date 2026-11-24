package e2e

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	chCTRL  byte = 0x01
	chVIDEO byte = 0x02
	chAUDIO byte = 0x03
	chINPUT byte = 0x04
	chTERM  byte = 0x05
	chFILE  byte = 0x06

	termOpen   byte = 0x01
	termStdin  byte = 0x02
	termStdout byte = 0x03
	termClose  byte = 0x05

	fileListDir     byte = 0x01
	fileListResp    byte = 0x02
	fileDownloadReq byte = 0x03
	fileUploadStart byte = 0x04
	fileChunk       byte = 0x05
	fileDone        byte = 0x06
	fileErr         byte = 0x07
)

type frame struct {
	Channel byte
	Payload []byte
}

type frameConn struct {
	conn net.Conn
	wmu  sync.Mutex
}

func newFrameConn(conn net.Conn) *frameConn {
	return &frameConn{conn: conn}
}

func (fc *frameConn) Close() error {
	return fc.conn.Close()
}

func (fc *frameConn) ReadFrame() (frame, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(fc.conn, hdr[:]); err != nil {
		return frame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[1:5])
	if n > 32*1024*1024 {
		return frame{}, fmt.Errorf("frame too large: %d", n)
	}
	payload := make([]byte, n)
	if n > 0 {
		if _, err := io.ReadFull(fc.conn, payload); err != nil {
			return frame{}, err
		}
	}
	return frame{Channel: hdr[0], Payload: payload}, nil
}

func (fc *frameConn) WriteFrame(ch byte, payload []byte) error {
	fc.wmu.Lock()
	defer fc.wmu.Unlock()
	var hdr [5]byte
	hdr[0] = ch
	binary.BigEndian.PutUint32(hdr[1:5], uint32(len(payload)))
	if _, err := fc.conn.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := fc.conn.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

func (fc *frameConn) WriteCTRL(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return fc.WriteFrame(chCTRL, b)
}

func (fc *frameConn) WriteSubcmdBytes(ch, subcmd byte, data []byte) error {
	buf := make([]byte, 1+len(data))
	buf[0] = subcmd
	copy(buf[1:], data)
	return fc.WriteFrame(ch, buf)
}

func (fc *frameConn) WriteSubcmdJSON(ch, subcmd byte, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return fc.WriteSubcmdBytes(ch, subcmd, b)
}

func dialPinnedTLS(addr, expectedSHA256 string, timeout time.Duration) (*tls.Conn, error) {
	expected := strings.ToLower(strings.TrimSpace(expectedSHA256))
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, // Verified manually via SHA-256 cert pinning below.
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("no peer certificate")
			}
			sum := sha256.Sum256(rawCerts[0])
			got := hex.EncodeToString(sum[:])
			if got != expected {
				return fmt.Errorf("cert sha256 mismatch: got %s want %s", got, expected)
			}
			return nil
		},
	}
	dialer := &net.Dialer{Timeout: timeout}
	return tls.DialWithDialer(dialer, "tcp", addr, cfg)
}

func computeHMAC(nonce string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(nonce))
	return hex.EncodeToString(mac.Sum(nil))
}

func generateTOTP(secretBase32 string, t time.Time) (string, error) {
	clean := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secretBase32), " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(clean)
	if err != nil {
		key, err = base32.StdEncoding.DecodeString(clean)
		if err != nil {
			return "", err
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

func randomUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func waitTCPReady(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s to accept TCP", addr)
}

func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	_ = cmd.Wait()
}

type hubConfig struct {
	ListenAddr string `json:"listen_addr"`
	AuthSecret string `json:"auth_secret"`
}

type hostConfig struct {
	HostID            string   `json:"host_id"`
	HubAddr           string   `json:"hub_addr"`
	HubCertSHA256     string   `json:"hub_cert_sha256"`
	HubAuthSecret     string   `json:"hub_auth_secret"`
	ListenAddr        string   `json:"listen_addr"`
	CertPath          string   `json:"cert_path"`
	KeyPath           string   `json:"key_path"`
	FingerprintPath   string   `json:"fingerprint_path"`
	HostPassword      string   `json:"host_password"`
	PasswordSaltHex   string   `json:"password_salt_hex"`
	PasswordPBKDF2Hex string   `json:"password_pbkdf2_hex"`
	TOTPSecret        string   `json:"totp_secret"`
	RequireTOTP       bool     `json:"require_totp"`
	AllowedDeviceIDs  []string `json:"allowed_device_ids"`
	StrictAllowlist   bool     `json:"strict_allowlist"`
	MediaCorePath     string   `json:"media_core_path"`
	FileRootDir       string   `json:"file_root_dir"`
}

type testEnv struct {
	hubAddr       string
	hubCertSHA256 string
	hubAuthSecret string
	hostID        string
	hostPassword  string
	totpSecret    string
	fileRootDir   string
}

func startTestEnv(t *testing.T) *testEnv {
	t.Helper()

	distDir, err := filepath.Abs(filepath.Join("..", "dist"))
	if err != nil {
		t.Fatalf("abs distDir: %v", err)
	}
	hubExe := filepath.Join(distDir, "xb-hub-windows-amd64.exe")
	hostExe := filepath.Join(distDir, "xb-host.exe")
	mediaExe := filepath.Join(distDir, "xb-media-core.exe")
	for _, p := range []string{hubExe, hostExe, mediaExe} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("missing binary %s: %v", p, err)
		}
	}

	tmpDir := t.TempDir()
	hubDir := filepath.Join(tmpDir, "hub")
	hostDir := filepath.Join(tmpDir, "host")
	fileRoot := filepath.Join(tmpDir, "files")
	for _, d := range []string{hubDir, hostDir, fileRoot} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	hubPort := freePort(t)
	hubAddr := fmt.Sprintf("127.0.0.1:%d", hubPort)
	hubCfgPath := filepath.Join(hubDir, "hub-config.json")

	initHub := exec.Command(hubExe, "--init-config", "--config", hubCfgPath, "--data-dir", hubDir, "--listen", hubAddr)
	if out, err := initHub.CombinedOutput(); err != nil {
		t.Fatalf("hub --init-config failed: %v\n%s", err, out)
	}

	hubCfgBytes, err := os.ReadFile(hubCfgPath)
	if err != nil {
		t.Fatalf("read hub-config.json: %v", err)
	}
	var hCfg hubConfig
	if err := json.Unmarshal(hubCfgBytes, &hCfg); err != nil {
		t.Fatalf("parse hub-config.json: %v", err)
	}
	hubFPBytes, err := os.ReadFile(filepath.Join(hubDir, "hub-fingerprint.txt"))
	if err != nil {
		t.Fatalf("read hub-fingerprint.txt: %v", err)
	}
	hubFP := strings.TrimSpace(string(hubFPBytes))

	hubCmd := exec.Command(hubExe, "--config", hubCfgPath, "--data-dir", hubDir, "--listen", hubAddr)
	var hubLog bytes.Buffer
	hubCmd.Stdout = &hubLog
	hubCmd.Stderr = &hubLog
	if err := hubCmd.Start(); err != nil {
		t.Fatalf("start xb-hub: %v", err)
	}
	t.Cleanup(func() {
		killProcessTree(hubCmd)
		if t.Failed() {
			t.Logf("=== xb-hub log ===\n%s", hubLog.String())
		}
	})
	waitTCPReady(t, hubAddr, 5*time.Second)

	hostPort := freePort(t)
	hostListen := fmt.Sprintf("[::]:%d", hostPort)
	hostLocal := fmt.Sprintf("127.0.0.1:%d", hostPort)
	hostCfgPath := filepath.Join(hostDir, "xb-host-config.json")

	initHost := exec.Command(hostExe, "--init-config", "--config", hostCfgPath)
	if out, err := initHost.CombinedOutput(); err != nil {
		t.Fatalf("host --init-config failed: %v\n%s", err, out)
	}

	hostCfgBytes, err := os.ReadFile(hostCfgPath)
	if err != nil {
		t.Fatalf("read xb-host-config.json: %v", err)
	}
	var wCfg hostConfig
	if err := json.Unmarshal(hostCfgBytes, &wCfg); err != nil {
		t.Fatalf("parse xb-host-config.json: %v", err)
	}

	wCfg.HubAddr = hubAddr
	wCfg.HubCertSHA256 = hubFP
	wCfg.HubAuthSecret = hCfg.AuthSecret
	wCfg.ListenAddr = hostListen
	wCfg.MediaCorePath = mediaExe
	wCfg.FileRootDir = fileRoot
	wCfg.RequireTOTP = true

	updatedHostCfg, err := json.MarshalIndent(wCfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal host config: %v", err)
	}
	if err := os.WriteFile(hostCfgPath, updatedHostCfg, 0600); err != nil {
		t.Fatalf("write host config: %v", err)
	}

	hostCmd := exec.Command(hostExe, "--config", hostCfgPath)
	var hostLog bytes.Buffer
	hostCmd.Stdout = &hostLog
	hostCmd.Stderr = &hostLog
	if err := hostCmd.Start(); err != nil {
		t.Fatalf("start xb-host: %v", err)
	}
	t.Cleanup(func() {
		killProcessTree(hostCmd)
		if t.Failed() {
			t.Logf("=== xb-host log ===\n%s", hostLog.String())
		}
	})
	waitTCPReady(t, hostLocal, 5*time.Second)

	return &testEnv{
		hubAddr:       hubAddr,
		hubCertSHA256: hubFP,
		hubAuthSecret: hCfg.AuthSecret,
		hostID:        wCfg.HostID,
		hostPassword:  wCfg.HostPassword,
		totpSecret:    wCfg.TOTPSecret,
		fileRootDir:   fileRoot,
	}
}

func hubSignalingHandshake(t *testing.T, env *testEnv, deviceID string) (*frameConn, error) {
	t.Helper()
	conn, err := dialPinnedTLS(env.hubAddr, env.hubCertSHA256, 5*time.Second)
	if err != nil {
		return nil, err
	}
	fc := newFrameConn(conn)

	// Android-identical client sends `hello` first!
	if err := fc.WriteCTRL(map[string]any{
		"type":      "hello",
		"device_id": deviceID,
		"role":      "client",
	}); err != nil {
		_ = fc.Close()
		return nil, err
	}

	var nonce string
	for {
		f, err := fc.ReadFrame()
		if err != nil {
			_ = fc.Close()
			return nil, err
		}
		if f.Channel != chCTRL {
			continue
		}
		var msg map[string]any
		if json.Unmarshal(f.Payload, &msg) != nil {
			continue
		}
		if msg["type"] == "challenge" {
			nonce, _ = msg["nonce"].(string)
			break
		}
	}

	if err := fc.WriteCTRL(map[string]any{
		"type":      "auth",
		"device_id": deviceID,
		"role":      "client",
		"hmac":      computeHMAC(nonce, []byte(env.hubAuthSecret)),
	}); err != nil {
		_ = fc.Close()
		return nil, err
	}

	f, err := fc.ReadFrame()
	if err != nil {
		_ = fc.Close()
		return nil, err
	}
	var resp map[string]any
	if err := json.Unmarshal(f.Payload, &resp); err != nil {
		_ = fc.Close()
		return nil, err
	}
	if resp["type"] != "auth_ok" {
		_ = fc.Close()
		return nil, fmt.Errorf("hub auth failed: %v", resp)
	}
	return fc, nil
}

func discoverHostViaHub(t *testing.T, env *testEnv, deviceID string) (resolvedHostID string, certSHA256 string, endpoints []string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		fc, err := hubSignalingHandshake(t, env, deviceID)
		if err != nil {
			time.Sleep(150 * time.Millisecond)
			continue
		}
		// Empty host_id auto-discovery matching Android client.
		if err := fc.WriteCTRL(map[string]any{
			"type":    "peer_query",
			"host_id": "",
		}); err == nil {
			f, err := fc.ReadFrame()
			if err == nil && f.Channel == chCTRL {
				var msg struct {
					Type       string   `json:"type"`
					HostID     string   `json:"host_id"`
					CertSHA256 string   `json:"cert_sha256"`
					Endpoints  []string `json:"endpoints"`
				}
				if json.Unmarshal(f.Payload, &msg) == nil && msg.Type == "peer_endpoints" && len(msg.Endpoints) > 0 && msg.CertSHA256 != "" {
					_ = fc.Close()
					return msg.HostID, msg.CertSHA256, msg.Endpoints
				}
			}
		}
		_ = fc.Close()
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("peer_query auto-discovery did not return host endpoints & cert_sha256 in time")
	return "", "", nil
}

func dialDirect(t *testing.T, endpoints []string, certSHA256 string) *frameConn {
	t.Helper()
	var lastErr error
	for _, ep := range endpoints {
		conn, err := dialPinnedTLS(ep, certSHA256, 2*time.Second)
		if err == nil {
			return newFrameConn(conn)
		}
		lastErr = err
	}
	t.Fatalf("failed to dial any direct endpoint %v: %v", endpoints, lastErr)
	return nil
}

func dialRelay(t *testing.T, env *testEnv, deviceID, hostID string) *frameConn {
	t.Helper()
	fc, err := hubSignalingHandshake(t, env, deviceID)
	if err != nil {
		t.Fatalf("hub handshake for relay: %v", err)
	}
	if err := fc.WriteCTRL(map[string]any{
		"type":    "relay_bind",
		"role":    "client",
		"host_id": hostID,
	}); err != nil {
		_ = fc.Close()
		t.Fatalf("write relay_bind: %v", err)
	}
	_ = fc.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	f, err := fc.ReadFrame()
	_ = fc.conn.SetReadDeadline(time.Time{})
	if err != nil {
		_ = fc.Close()
		t.Fatalf("read relay_ready: %v", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(f.Payload, &msg); err != nil || msg["type"] != "relay_ready" {
		_ = fc.Close()
		t.Fatalf("expected relay_ready, got: %s", string(f.Payload))
	}
	return fc
}

func authenticateHostSession(t *testing.T, fc *frameConn, deviceID, password, totpSecret string) error {
	t.Helper()
	// Android-identical client sends `hello` first!
	if err := fc.WriteCTRL(map[string]any{
		"type":      "hello",
		"device_id": deviceID,
		"role":      "client",
	}); err != nil {
		return err
	}

	var nonce string
	var totpReq bool
	for {
		f, err := fc.ReadFrame()
		if err != nil {
			return err
		}
		if f.Channel != chCTRL {
			continue
		}
		var msg struct {
			Type         string `json:"type"`
			Nonce        string `json:"nonce"`
			TOTPRequired bool   `json:"totp_required"`
		}
		if json.Unmarshal(f.Payload, &msg) == nil && msg.Type == "challenge" {
			nonce = msg.Nonce
			totpReq = msg.TOTPRequired
			break
		}
	}

	totpCode := ""
	if totpReq && totpSecret != "" {
		var err error
		totpCode, err = generateTOTP(totpSecret, time.Now())
		if err != nil {
			return err
		}
	}

	if err := fc.WriteCTRL(map[string]any{
		"type":      "auth",
		"device_id": deviceID,
		"role":      "client",
		"hmac":      computeHMAC(nonce, []byte(password)),
		"totp":      totpCode,
	}); err != nil {
		return err
	}

	f, err := fc.ReadFrame()
	if err != nil {
		return err
	}
	var resp struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(f.Payload, &resp); err != nil {
		return err
	}
	if resp.Type == "auth_err" {
		return fmt.Errorf("auth_err: %s", resp.Reason)
	}
	if resp.Type != "auth_ok" {
		return fmt.Errorf("unexpected auth response: %s", string(f.Payload))
	}
	return nil
}

var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

func hasTermOutputLine(raw, target string) bool {
	clean := ansiEscapeRe.ReplaceAllString(raw, "")
	clean = strings.ReplaceAll(clean, "\r\n", "\n")
	clean = strings.ReplaceAll(clean, "\r", "\n")
	for _, line := range strings.Split(clean, "\n") {
		if strings.TrimSpace(line) == target {
			return true
		}
	}
	return false
}

func hasAnnexBStartCode(b []byte) bool {
	return bytes.Contains(b, []byte{0x00, 0x00, 0x00, 0x01}) || bytes.Contains(b, []byte{0x00, 0x00, 0x01})
}

func writeTestWAV(path string, sampleRate, durationMs int) error {
	numSamples := sampleRate * durationMs / 1000
	dataSize := numSamples * 2 * 2 // stereo 16-bit
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+dataSize))
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1)) // PCM
	_ = binary.Write(&buf, binary.LittleEndian, uint16(2)) // stereo
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate*4))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(4))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(dataSize))
	// Low-amplitude 440Hz-like square/triangle or quiet non-zero samples
	samples := make([]byte, dataSize)
	for i := 0; i < numSamples; i++ {
		var v int16 = 100
		if (i/55)%2 == 0 {
			v = -100
		}
		binary.LittleEndian.PutUint16(samples[i*4:i*4+2], uint16(v))
		binary.LittleEndian.PutUint16(samples[i*4+2:i*4+4], uint16(v))
	}
	buf.Write(samples)
	return os.WriteFile(path, buf.Bytes(), 0644)
}

func runSessionSuite(t *testing.T, name string, env *testEnv, dialFn func(*testing.T) *frameConn) {
	t.Helper()
	deviceID := "android-e2e-device"

	t.Run(name+"/1_RejectWrongPassword", func(t *testing.T) {
		fc := dialFn(t)
		defer fc.Close()
		err := authenticateHostSession(t, fc, deviceID, "wrong-password-12345", env.totpSecret)
		if err == nil || !strings.Contains(err.Error(), "auth_err") {
			t.Fatalf("expected auth_err for wrong password, got: %v", err)
		}
	})

	fc := dialFn(t)
	defer fc.Close()

	t.Run(name+"/2_AuthSuccessWithTOTP", func(t *testing.T) {
		if err := authenticateHostSession(t, fc, deviceID, env.hostPassword, env.totpSecret); err != nil {
			t.Fatalf("expected auth_ok, got: %v", err)
		}
	})

	// Shared reader loop for steps 3, 4, 5 so media frames don't block TERM/FILE responses.
	var (
		mu          sync.Mutex
		videoFrames int
		audioFrames int
		termOut     strings.Builder
		listRespCh  = make(chan []byte, 4)
		dlChunkCh   = make(chan []byte, 64)
		fileDoneCh  = make(chan []byte, 8)
		fileErrCh   = make(chan string, 4)
		readerDone  = make(chan error, 1)
	)

	go func() {
		for {
			f, err := fc.ReadFrame()
			if err != nil {
				readerDone <- err
				return
			}
			switch f.Channel {
			case chVIDEO:
				if len(f.Payload) > 9 && hasAnnexBStartCode(f.Payload[9:]) {
					mu.Lock()
					videoFrames++
					mu.Unlock()
				}
			case chAUDIO:
				if len(f.Payload) >= 11 {
					srDiv100 := binary.BigEndian.Uint16(f.Payload[8:10])
					channels := f.Payload[10]
					if srDiv100 == 480 && channels == 2 {
						mu.Lock()
						audioFrames++
						mu.Unlock()
					}
				}
			case chTERM:
				if len(f.Payload) >= 1 && f.Payload[0] == termStdout {
					mu.Lock()
					termOut.Write(f.Payload[1:])
					mu.Unlock()
				}
			case chFILE:
				if len(f.Payload) < 1 {
					continue
				}
				switch f.Payload[0] {
				case fileListResp:
					listRespCh <- append([]byte(nil), f.Payload[1:]...)
				case fileChunk:
					dlChunkCh <- append([]byte(nil), f.Payload[1:]...)
				case fileDone:
					fileDoneCh <- append([]byte(nil), f.Payload[1:]...)
				case fileErr:
					fileErrCh <- string(f.Payload[1:])
				}
			}
		}
	}()

	t.Run(name+"/3_VideoAndAudioStream", func(t *testing.T) {
		// Play a brief silent/low-tone WAV through Windows audio render so WASAPI loopback
		// emits active audio packets even on an otherwise silent host.
		wavPath := filepath.Join(t.TempDir(), "tone.wav")
		if err := writeTestWAV(wavPath, 48000, 1500); err == nil {
			psCmd := exec.Command("powershell", "-NoProfile", "-Command",
				fmt.Sprintf(`(New-Object Media.SoundPlayer '%s').PlaySync()`, wavPath))
			_ = psCmd.Start()
			t.Cleanup(func() {
				if psCmd.Process != nil {
					_ = psCmd.Process.Kill()
					_ = psCmd.Wait()
				}
			})
		}

		if err := fc.WriteCTRL(map[string]any{
			"type":         "set_encoder",
			"width":        1280,
			"height":       720,
			"fps":          30,
			"bitrate_kbps": 4000,
			"codec":        "h264",
		}); err != nil {
			t.Fatalf("write set_encoder: %v", err)
		}
		if err := fc.WriteCTRL(map[string]any{
			"type":   "view_only",
			"enable": true,
		}); err != nil {
			t.Fatalf("write view_only: %v", err)
		}

		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			vf, af := videoFrames, audioFrames
			mu.Unlock()
			if vf > 0 && af > 0 {
				t.Logf("%s received %d Annex-B VIDEO frames and %d WASAPI AUDIO frames", name, vf, af)
				return
			}
			select {
			case err := <-readerDone:
				t.Fatalf("connection closed while waiting for media: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
		}
		mu.Lock()
		vf, af := videoFrames, audioFrames
		mu.Unlock()
		t.Fatalf("timeout waiting for media: videoFrames=%d audioFrames=%d", vf, af)
	})

	t.Run(name+"/4_ConPTYTerminal", func(t *testing.T) {
		if err := fc.WriteSubcmdJSON(chTERM, termOpen, map[string]int{"cols": 120, "rows": 36}); err != nil {
			t.Fatalf("term open: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
		if err := fc.WriteSubcmdBytes(chTERM, termStdin, []byte("Write-Output \"XB_TERM_E2E_OK\"\r\n")); err != nil {
			t.Fatalf("term stdin: %v", err)
		}

		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			out := termOut.String()
			mu.Unlock()
			if hasTermOutputLine(out, "XB_TERM_E2E_OK") {
				_ = fc.WriteSubcmdBytes(chTERM, termClose, nil)
				t.Logf("%s ConPTY verified XB_TERM_E2E_OK", name)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		mu.Lock()
		out := termOut.String()
		mu.Unlock()
		t.Fatalf("timeout waiting for XB_TERM_E2E_OK in ConPTY stdout; got:\n%s", out)
	})

	t.Run(name+"/5_FileTransferSHA256RoundTrip", func(t *testing.T) {
		// 5a. list_dir
		if err := fc.WriteSubcmdJSON(chFILE, fileListDir, map[string]string{"path": ""}); err != nil {
			t.Fatalf("list_dir: %v", err)
		}
		select {
		case raw := <-listRespCh:
			var resp struct {
				Path    string `json:"path"`
				Entries []any  `json:"entries"`
			}
			if err := json.Unmarshal(raw, &resp); err != nil {
				t.Fatalf("parse list_resp: %v", err)
			}
		case errMsg := <-fileErrCh:
			t.Fatalf("list_dir error: %s", errMsg)
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout waiting for list_resp")
		}

		// 5b. upload_start -> file_chunk -> file_done (140 KiB > 64 KiB chunk size)
		payload := make([]byte, 140*1024)
		if _, err := rand.Read(payload); err != nil {
			t.Fatalf("rand.Read: %v", err)
		}
		sum := sha256.Sum256(payload)
		expectedSHA := hex.EncodeToString(sum[:])

		relName := fmt.Sprintf("e2e-%s.bin", strings.ToLower(name))
		upID := randomUUIDv4()
		if err := fc.WriteSubcmdJSON(chFILE, fileUploadStart, map[string]any{
			"id":     upID,
			"path":   relName,
			"size":   len(payload),
			"sha256": expectedSHA,
		}); err != nil {
			t.Fatalf("upload_start: %v", err)
		}

		const chunkSize = 64 * 1024
		for off := 0; off < len(payload); off += chunkSize {
			end := off + chunkSize
			if end > len(payload) {
				end = len(payload)
			}
			buf := make([]byte, 36+8+(end-off))
			copy(buf[0:36], []byte(upID))
			binary.BigEndian.PutUint64(buf[36:44], uint64(off))
			copy(buf[44:], payload[off:end])
			if err := fc.WriteSubcmdBytes(chFILE, fileChunk, buf); err != nil {
				t.Fatalf("write file_chunk: %v", err)
			}
		}

		if err := fc.WriteSubcmdJSON(chFILE, fileDone, map[string]any{
			"id":     upID,
			"sha256": expectedSHA,
			"size":   len(payload),
		}); err != nil {
			t.Fatalf("write file_done: %v", err)
		}

		select {
		case raw := <-fileDoneCh:
			var doneMsg struct {
				ID     string `json:"id"`
				SHA256 string `json:"sha256"`
				Size   int    `json:"size"`
			}
			if err := json.Unmarshal(raw, &doneMsg); err != nil || doneMsg.ID != upID || doneMsg.SHA256 != expectedSHA {
				t.Fatalf("unexpected upload file_done: %s", string(raw))
			}
		case errMsg := <-fileErrCh:
			t.Fatalf("upload file_err: %s", errMsg)
		case <-time.After(10 * time.Second):
			t.Fatalf("timeout waiting for upload file_done")
		}

		// 5c. download_req -> file_chunk -> file_done
		dlID := randomUUIDv4()
		if err := fc.WriteSubcmdJSON(chFILE, fileDownloadReq, map[string]any{
			"id":   dlID,
			"path": relName,
		}); err != nil {
			t.Fatalf("download_req: %v", err)
		}

		downloaded := make([]byte, len(payload))
		var receivedBytes int
		dlDeadline := time.After(10 * time.Second)
	dlLoop:
		for {
			select {
			case chunk := <-dlChunkCh:
				if len(chunk) < 44 || string(chunk[:36]) != dlID {
					continue
				}
				off := int(binary.BigEndian.Uint64(chunk[36:44]))
				data := chunk[44:]
				if off+len(data) > len(downloaded) {
					t.Fatalf("download chunk out of bounds: off=%d len=%d", off, len(data))
				}
				copy(downloaded[off:], data)
				receivedBytes += len(data)
			case raw := <-fileDoneCh:
				var doneMsg struct {
					ID     string `json:"id"`
					SHA256 string `json:"sha256"`
					Size   int    `json:"size"`
				}
				if err := json.Unmarshal(raw, &doneMsg); err == nil && doneMsg.ID == dlID {
					if doneMsg.SHA256 != expectedSHA || doneMsg.Size != len(payload) {
						t.Fatalf("download file_done mismatch: %+v", doneMsg)
					}
					break dlLoop
				}
			case errMsg := <-fileErrCh:
				t.Fatalf("download file_err: %s", errMsg)
			case <-dlDeadline:
				t.Fatalf("timeout waiting for download completion (received=%d/%d)", receivedBytes, len(payload))
			}
		}

		gotSum := sha256.Sum256(downloaded)
		if hex.EncodeToString(gotSum[:]) != expectedSHA {
			t.Fatalf("downloaded SHA-256 mismatch: got %x want %s", gotSum, expectedSHA)
		}
		t.Logf("%s file round-trip SHA-256 verified (%s, %d bytes)", name, expectedSHA[:12], len(payload))
	})
}

func TestFullE2E_DirectAndRelay(t *testing.T) {
	env := startTestEnv(t)

	resolvedHostID, hostCertSHA256, endpoints := discoverHostViaHub(t, env, "android-e2e-discover")
	if resolvedHostID != env.hostID {
		t.Fatalf("auto-discovered host_id mismatch: got %q want %q", resolvedHostID, env.hostID)
	}
	t.Logf("Discovered host_id=%s cert_sha256=%s endpoints=%v", resolvedHostID, hostCertSHA256, endpoints)

	runSessionSuite(t, "DirectP2P", env, func(st *testing.T) *frameConn {
		return dialDirect(st, endpoints, hostCertSHA256)
	})

	runSessionSuite(t, "HubTLSRelay", env, func(st *testing.T) *frameConn {
		return dialRelay(st, env, "android-e2e-relay", resolvedHostID)
	})
}
