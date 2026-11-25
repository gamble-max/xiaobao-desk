package hubclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/xiaobaodesk/win-host/internal/config"
	"github.com/xiaobaodesk/win-host/internal/protocol"
	"github.com/xiaobaodesk/win-host/internal/security"
)

// RelayHandler is invoked when xb-hub splices a client onto the host's TLS stream via relay_bind.
// The handler takes ownership of conn and fc.
type RelayHandler func(conn net.Conn, fc *protocol.FrameConn, bind protocol.RelayBindMsg)

// Client manages outbound TLS 1.3 signaling + UDP 21116 endpoint reflection to xb-hub.
type Client struct {
	cfg           *config.Config
	hostCertSHA   string
	listenPort    int
	onRelay       RelayHandler
}

func New(cfg *config.Config, hostCertSHA string, onRelay RelayHandler) *Client {
	port := 21118
	if _, pStr, err := net.SplitHostPort(cfg.ListenAddr); err == nil {
		if p, err := strconv.Atoi(pStr); err == nil && p > 0 {
			port = p
		}
	}
	return &Client{
		cfg:         cfg,
		hostCertSHA: hostCertSHA,
		listenPort:  port,
		onRelay:     onRelay,
	}
}

// Run maintains a persistent signaling & standby relay connection to xb-hub until ctx is canceled.
func (c *Client) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		handedOff, err := c.connectAndServe(ctx)
		if handedOff {
			// Stream was handed off to a client relay session; immediately reconnect a fresh standby stream.
			backoff = time.Second
			continue
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("[hubclient] disconnected: %v (retrying in %s)", err, backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (c *Client) dialAndAuthHub(ctx context.Context) (*tls.Conn, *protocol.FrameConn, error) {
	tlsCfg := security.NewPinnedClientTLSConfig(c.cfg.HubCertSHA256)
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	rawConn, err := dialer.DialContext(ctx, "tcp", c.cfg.HubAddr)
	if err != nil {
		if resolved := resolveViaFallbackDNS(ctx, c.cfg.HubAddr); resolved != "" && resolved != c.cfg.HubAddr {
			if retryConn, retryErr := dialer.DialContext(ctx, "tcp", resolved); retryErr == nil {
				rawConn = retryConn
				err = nil
			}
		}
		if err != nil {
			return nil, nil, err
		}
	}
	tlsConn := tls.Client(rawConn, tlsCfg)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = tlsConn.Close()
		return nil, nil, fmt.Errorf("tls handshake: %w", err)
	}

	fc := protocol.NewFrameConn(tlsConn)

	// Authenticate with xb-hub (role=host)
	_ = tlsConn.SetDeadline(time.Now().Add(15 * time.Second))
	f, err := fc.ReadFrame()
	if err != nil {
		_ = tlsConn.Close()
		return nil, nil, fmt.Errorf("read challenge: %w", err)
	}
	if f.Channel != protocol.ChannelCTRL {
		_ = tlsConn.Close()
		return nil, nil, fmt.Errorf("expected CTRL challenge, got 0x%02x", f.Channel)
	}
	var ch protocol.ChallengeMsg
	if err := json.Unmarshal(f.Payload, &ch); err != nil || ch.Type != "challenge" {
		_ = tlsConn.Close()
		return nil, nil, fmt.Errorf("invalid challenge payload")
	}

	secret := c.cfg.HubAuthSecret
	if secret == "" {
		secret = c.cfg.HostPassword
	}
	authMsg := protocol.AuthMsg{
		Type:     "auth",
		DeviceID: c.cfg.HostID,
		Role:     "host",
		HMAC:     security.ComputeAuthHMAC(ch.Nonce, []byte(secret)),
	}
	if err := fc.WriteCTRL(authMsg); err != nil {
		_ = tlsConn.Close()
		return nil, nil, fmt.Errorf("write auth: %w", err)
	}

	resp, err := fc.ReadFrame()
	if err != nil {
		_ = tlsConn.Close()
		return nil, nil, fmt.Errorf("read auth_ok: %w", err)
	}
	var env protocol.CtrlEnvelope
	_ = json.Unmarshal(resp.Payload, &env)
	if env.Type != "auth_ok" {
		_ = tlsConn.Close()
		return nil, nil, fmt.Errorf("hub auth failed: %s", string(resp.Payload))
	}
	_ = tlsConn.SetDeadline(time.Time{})
	return tlsConn, fc, nil
}

func (c *Client) openRelayStream(ctx context.Context, bind protocol.RelayBindMsg) {
	if c.onRelay == nil {
		return
	}
	relayConn, relayFC, err := c.dialAndAuthHub(ctx)
	if err != nil {
		log.Printf("[hubclient] relay dial/auth failed (session=%s): %v", bind.SessionID, err)
		return
	}

	_ = relayConn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := relayFC.WriteCTRL(map[string]any{
		"type":       "relay_bind",
		"role":       "host",
		"session_id": bind.SessionID,
	}); err != nil {
		_ = relayConn.Close()
		log.Printf("[hubclient] relay_bind write failed (session=%s): %v", bind.SessionID, err)
		return
	}

	resp, err := relayFC.ReadFrame()
	if err != nil {
		_ = relayConn.Close()
		log.Printf("[hubclient] relay_ready read failed (session=%s): %v", bind.SessionID, err)
		return
	}
	var env protocol.CtrlEnvelope
	if err := json.Unmarshal(resp.Payload, &env); err != nil || env.Type != "relay_ready" {
		_ = relayConn.Close()
		log.Printf("[hubclient] expected relay_ready (session=%s), got: %s", bind.SessionID, string(resp.Payload))
		return
	}
	_ = relayConn.SetDeadline(time.Time{})

	log.Printf("[hubclient] relay_ready (session=%s), handing off fresh TLS stream", bind.SessionID)
	c.onRelay(relayConn, relayFC, bind)
}

func (c *Client) connectAndServe(ctx context.Context) (handedOff bool, err error) {
	tlsConn, fc, err := c.dialAndAuthHub(ctx)
	if err != nil {
		return false, err
	}

	// 2. Collect local endpoints + UDP 21116 reflection and register with xb-hub
	endpoints := c.discoverEndpoints(ctx)
	if err := fc.WriteCTRL(protocol.PeerRegisterMsg{
		Type:      "peer_register",
		HostID:    c.cfg.HostID,
		Endpoints: endpoints,
		CertSHA:   c.hostCertSHA,
	}); err != nil {
		_ = tlsConn.Close()
		return false, fmt.Errorf("peer_register: %w", err)
	}
	log.Printf("[hubclient] registered host %s with %d endpoints", c.cfg.HostID, len(endpoints))

	// 3. Keepalive ticker
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopPing:
				return
			case <-ticker.C:
				_ = fc.WriteCTRL(protocol.PingPongMsg{Type: "ping", Ts: time.Now().UnixMilli()})
			}
		}
	}()

	// 4. Control loop waiting for relay_bind or ping/pong
	for {
		if ctx.Err() != nil {
			_ = tlsConn.Close()
			return false, ctx.Err()
		}
		frame, err := fc.ReadFrame()
		if err != nil {
			_ = tlsConn.Close()
			return false, err
		}
		if frame.Channel != protocol.ChannelCTRL {
			continue
		}
		var msgEnv protocol.CtrlEnvelope
		if err := json.Unmarshal(frame.Payload, &msgEnv); err != nil {
			continue
		}
		switch msgEnv.Type {
		case "ping":
			_ = fc.WriteCTRL(protocol.PingPongMsg{Type: "pong", Ts: time.Now().UnixMilli()})
		case "relay_bind":
			var bind protocol.RelayBindMsg
			_ = json.Unmarshal(frame.Payload, &bind)
			log.Printf("[hubclient] relay_bind requested (session=%s), opening fresh relay stream", bind.SessionID)
			go c.openRelayStream(ctx, bind)
		}
	}
}

func (c *Client) discoverEndpoints(ctx context.Context) []string {
	seen := make(map[string]struct{})
	var eps []string
	add := func(ep string) {
		if ep == "" {
			return
		}
		if _, ok := seen[ep]; !ok {
			seen[ep] = struct{}{}
			eps = append(eps, ep)
		}
	}

	// Query UDP 21116 endpoint reflection from xb-hub
	if pub := c.reflectUDPEndpoint(ctx); pub != "" {
		add(pub)
	}

	// Enumerate local IPv6 and IPv4 (LAN / Radmin VPN) interfaces
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				ipNet, ok := addr.(*net.IPNet)
				if !ok {
					continue
				}
				ip := ipNet.IP
				if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
					continue
				}
				if ip4 := ip.To4(); ip4 != nil {
					add(net.JoinHostPort(ip4.String(), strconv.Itoa(c.listenPort)))
				} else if ip.To16() != nil {
					add(net.JoinHostPort(ip.String(), strconv.Itoa(c.listenPort)))
				}
			}
		}
	}
	return eps
}

func resolveViaFallbackDNS(ctx context.Context, addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) != nil {
		return ""
	}
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			return d.DialContext(ctx, "udp", "223.5.5.5:53")
		},
	}
	ips, err := r.LookupHost(ctx, host)
	if err != nil || len(ips) == 0 {
		return ""
	}
	return net.JoinHostPort(ips[0], port)
}

type udpReflectReq struct {
	Type   string `json:"type"`
	HostID string `json:"host_id"`
	Port   int    `json:"port"`
}

type udpReflectResp struct {
	Type     string `json:"type"`
	Endpoint string `json:"endpoint"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
}

func (c *Client) reflectUDPEndpoint(ctx context.Context) string {
	raddr, err := net.ResolveUDPAddr("udp", c.cfg.HubAddr)
	if err != nil {
		return ""
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return ""
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	reqBytes, _ := json.Marshal(udpReflectReq{
		Type:   "udp_reflect",
		HostID: c.cfg.HostID,
		Port:   c.listenPort,
	})
	if _, err := conn.Write(reqBytes); err != nil {
		return ""
	}

	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		return ""
	}
	var resp udpReflectResp
	if err := json.Unmarshal(buf[:n], &resp); err == nil {
		if resp.Endpoint != "" {
			return resp.Endpoint
		}
		if resp.IP != "" && resp.Port > 0 {
			return net.JoinHostPort(resp.IP, strconv.Itoa(resp.Port))
		}
	}
	// Fallback if hub echoed plain "ip:port" text
	txt := strings.TrimSpace(string(buf[:n]))
	if _, _, err := net.SplitHostPort(txt); err == nil {
		return txt
	}
	return ""
}
