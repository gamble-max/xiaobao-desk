package hub

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"xiaobaodesk/hub/internal/auth"
	"xiaobaodesk/hub/internal/config"
	"xiaobaodesk/hub/internal/proto"
)

const (
	handshakeTimeout  = 10 * time.Second
	maxAuthFailures   = 5
	authFailWindow    = 5 * time.Minute
	authBlockDuration = 5 * time.Minute
)

type authFailRecord struct {
	failures     int
	firstFailure time.Time
	blockedUntil time.Time
}

type authRateLimiter struct {
	mu      sync.Mutex
	records map[string]*authFailRecord
}

func newAuthRateLimiter() *authRateLimiter {
	return &authRateLimiter{
		records: make(map[string]*authFailRecord),
	}
}

func (l *authRateLimiter) isBlocked(ip string, now time.Time) bool {
	if ip == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.records[ip]
	if !ok {
		return false
	}
	if !rec.blockedUntil.IsZero() && now.Before(rec.blockedUntil) {
		return true
	}
	if !rec.blockedUntil.IsZero() && !now.Before(rec.blockedUntil) {
		delete(l.records, ip)
	}
	return false
}

func (l *authRateLimiter) recordFailure(ip string, now time.Time) bool {
	if ip == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	rec, ok := l.records[ip]
	if !ok {
		rec = &authFailRecord{firstFailure: now}
		l.records[ip] = rec
	} else if !rec.blockedUntil.IsZero() && now.Before(rec.blockedUntil) {
		return true
	} else if now.Sub(rec.firstFailure) > authFailWindow {
		rec.failures = 0
		rec.firstFailure = now
		rec.blockedUntil = time.Time{}
	}

	rec.failures++
	if rec.failures >= maxAuthFailures {
		rec.blockedUntil = now.Add(authBlockDuration)
		return true
	}
	return false
}

func (l *authRateLimiter) reset(ip string) {
	if ip == "" {
		return
	}
	l.mu.Lock()
	delete(l.records, ip)
	l.mu.Unlock()
}

// Server hosts the TCP TLS 1.3 control/relay server and UDP reflection server.
type Server struct {
	Cfg         *config.Config
	TLS         *tls.Config
	Registry    *Registry
	Logger      *log.Logger
	authLimiter *authRateLimiter
	limiterOnce sync.Once
}

// NewServer constructs a hub Server.
func NewServer(cfg *config.Config, tlsCfg *tls.Config, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		Cfg:         cfg,
		TLS:         tlsCfg,
		Registry:    NewRegistry(time.Duration(cfg.PeerTTLSeconds) * time.Second),
		Logger:      logger,
		authLimiter: newAuthRateLimiter(),
	}
}

func (s *Server) limiter() *authRateLimiter {
	s.limiterOnce.Do(func() {
		if s.authLimiter == nil {
			s.authLimiter = newAuthRateLimiter()
		}
	})
	return s.authLimiter
}

// ServeTCP accepts TLS 1.3 connections on ln until ctx is cancelled.
func (s *Server) ServeTCP(ctx context.Context, ln net.Listener) error {
	tlsLn := tls.NewListener(ln, s.TLS)
	go func() {
		<-ctx.Done()
		_ = tlsLn.Close()
	}()

	for {
		conn, err := tlsLn.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handleTCPConn(ctx, conn)
	}
}

type ctrlEnvelope struct {
	Type         string   `json:"type"`
	DeviceID     string   `json:"device_id,omitempty"`
	Role         string   `json:"role,omitempty"`
	HMAC         string   `json:"hmac,omitempty"`
	TOTP         string   `json:"totp,omitempty"`
	HostID       string   `json:"host_id,omitempty"`
	CertSHA256   string   `json:"cert_sha256,omitempty"`
	Endpoints    []string `json:"endpoints,omitempty"`
	SessionID    string   `json:"session_id,omitempty"`
	FromDeviceID string   `json:"from_device_id,omitempty"`
	TS           int64    `json:"ts,omitempty"`
}

func extractRemoteIP(remote string) string {
	ip, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return ip
}

func (s *Server) handleTCPConn(ctx context.Context, conn net.Conn) {
	remote := conn.RemoteAddr().String()
	remoteIP := extractRemoteIP(remote)
	defer conn.Close()

	lim := s.limiter()
	if lim.isBlocked(remoteIP, time.Now()) {
		s.Logger.Printf("blocked connection from rate-limited IP %s", remoteIP)
		return
	}

	// Complete TLS handshake and auth explicitly within 10s deadline.
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))
	if tc, ok := conn.(*tls.Conn); ok {
		if err := tc.HandshakeContext(ctx); err != nil {
			s.Logger.Printf("tls handshake failed from %s: %v", remote, err)
			return
		}
	}

	nonce, err := auth.GenerateNonce()
	if err != nil {
		s.Logger.Printf("nonce gen failed: %v", err)
		return
	}

	if err := proto.WriteCtrl(conn, map[string]any{
		"type":          "challenge",
		"nonce":         nonce,
		"totp_required": false,
	}); err != nil {
		return
	}

	var authedDeviceID, authedRole string
	for {
		var msg ctrlEnvelope
		if _, err := proto.ReadCtrl(conn, &msg); err != nil {
			return
		}
		if msg.Type == "hello" {
			continue
		}
		if msg.Type != "auth" {
			lim.recordFailure(remoteIP, time.Now())
			_ = proto.WriteCtrl(conn, map[string]any{
				"type":   "auth_err",
				"reason": "expected auth",
			})
			return
		}
		if !auth.IsDeviceAllowed(msg.DeviceID, s.Cfg.AllowedDeviceIDs) {
			blocked := lim.recordFailure(remoteIP, time.Now())
			s.Logger.Printf("auth failed (disallowed device) from %s (blocked=%v)", remoteIP, blocked)
			_ = proto.WriteCtrl(conn, map[string]any{
				"type":   "auth_err",
				"reason": "device_id not allowed",
			})
			return
		}
		if !auth.VerifyHMAC(nonce, s.Cfg.AuthSecret, msg.HMAC) {
			blocked := lim.recordFailure(remoteIP, time.Now())
			s.Logger.Printf("auth failed (invalid hmac) from %s (blocked=%v)", remoteIP, blocked)
			_ = proto.WriteCtrl(conn, map[string]any{
				"type":   "auth_err",
				"reason": "invalid hmac",
			})
			return
		}
		lim.reset(remoteIP)
		authedDeviceID = msg.DeviceID
		authedRole = msg.Role
		if authedRole == "" {
			authedRole = "client"
		}
		break
	}

	// Clear auth deadline
	_ = conn.SetDeadline(time.Time{})

	sessionID := NewSessionID()
	if err := proto.WriteCtrl(conn, map[string]any{
		"type":       "auth_ok",
		"session_id": sessionID,
	}); err != nil {
		return
	}

	cs := &ControlSession{
		Conn:     conn,
		DeviceID: authedDeviceID,
		Role:     authedRole,
	}

	var registeredHostID string
	defer func() {
		if registeredHostID != "" {
			s.Registry.UnregisterControl(registeredHostID, cs)
		}
	}()

	for {
		if ctx.Err() != nil {
			return
		}
		frame, err := proto.ReadFrame(conn, proto.MaxCtrlSize)
		if err != nil {
			return
		}
		if frame.Channel != proto.ChanCtrl {
			continue
		}
		var msg ctrlEnvelope
		if err := json.Unmarshal(frame.Payload, &msg); err != nil {
			continue
		}

		switch msg.Type {
		case "ping":
			_ = cs.WriteCtrl(map[string]any{
				"type": "pong",
				"ts":   msg.TS,
			})

		case "peer_register":
			hostID := msg.HostID
			if hostID == "" {
				hostID = authedDeviceID
			}
			registeredHostID = hostID
			obsIP, _, _ := net.SplitHostPort(remote)
			rec := s.Registry.RegisterPeer(hostID, authedDeviceID, msg.CertSHA256, msg.Endpoints, remote, cs)
			_ = cs.WriteCtrl(map[string]any{
				"type":        "peer_register_ok",
				"host_id":     rec.HostID,
				"observed_ip": obsIP,
			})

		case "peer_query":
			rec, found := s.Registry.QueryPeer(msg.HostID)
			resp := map[string]any{
				"type":    "peer_endpoints",
				"host_id": msg.HostID,
				"found":   found,
			}
			if found {
				resp["host_id"] = rec.HostID
				resp["cert_sha256"] = rec.CertSHA256
				resp["endpoints"] = rec.Endpoints
				resp["observed_tcp"] = rec.ObservedTCP
				resp["observed_udp"] = rec.ObservedUDP
				resp["online"] = rec.Control != nil
			}
			_ = cs.WriteCtrl(resp)

		case "relay_bind":
			role := msg.Role
			if role == "" {
				role = authedRole
			}
			rSessID := msg.SessionID
			if rSessID == "" {
				rSessID = NewSessionID()
			}
			if role != "host" {
				if rec, ok := s.Registry.QueryPeer(msg.HostID); ok && rec.Control != nil {
					_ = rec.Control.WriteCtrl(map[string]any{
						"type":           "relay_bind",
						"session_id":     rSessID,
						"host_id":        rec.HostID,
						"from_device_id": authedDeviceID,
					})
				}
			}

			relayTimeout := time.Duration(s.Cfg.RelayTokenTTLSeconds) * time.Second
			peerConn, spliceCh, isLeader, err := s.Registry.BindRelay(ctx, rSessID, role, conn, relayTimeout)
			if err != nil {
				_ = cs.WriteCtrl(map[string]any{
					"type":       "relay_err",
					"session_id": rSessID,
					"reason":     err.Error(),
				})
				return
			}

			if isLeader {
				spliceConns(conn, peerConn)
				close(spliceCh)
			} else {
				select {
				case <-spliceCh:
				case <-ctx.Done():
				}
			}
			return
		}
	}
}

func spliceConns(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	copyDir := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		_ = dst.Close()
		_ = src.Close()
	}

	go copyDir(b, a)
	go copyDir(a, b)
	wg.Wait()
}
