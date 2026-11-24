package p2p

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"log"
	"net"
	"strings"

	"github.com/xiaobaodesk/win-host/internal/protocol"
)

// ConnHandler handles an accepted TLS 1.3 P2P client connection.
type ConnHandler func(conn net.Conn, fc *protocol.FrameConn)

// Listener runs the dual-stack [::]:21118 TLS 1.3 direct P2P listener
// and optional UDP hole-punch / LAN discovery listener on the same port.
type Listener struct {
	addr       string
	tlsCfg     *tls.Config
	handler    ConnHandler
	hostID     string
	hostName   string
	listenPort int
	certSHA    string
}

func NewListener(addr string, tlsCfg *tls.Config, handler ConnHandler) *Listener {
	return &Listener{
		addr:       addr,
		tlsCfg:     tlsCfg,
		handler:    handler,
		listenPort: 21118,
	}
}

// SetAnnounceInfo sets public host identity metadata returned to LAN UDP discovery probes.
func (l *Listener) SetAnnounceInfo(hostID, hostName string, listenPort int, certSHA256 string) {
	l.hostID = hostID
	l.hostName = hostName
	if listenPort > 0 {
		l.listenPort = listenPort
	}
	l.certSHA = certSHA256
}

// Serve starts the TCP TLS 1.3 listener and UDP hole-punch responder until ctx is canceled.
func (l *Listener) Serve(ctx context.Context) error {
	ln, err := tls.Listen("tcp", l.addr, l.tlsCfg)
	if err != nil {
		return err
	}
	defer ln.Close()

	// Also listen on UDP for hole-punch coordination packets
	if udpAddr, err := net.ResolveUDPAddr("udp", l.addr); err == nil {
		if udpConn, err := net.ListenUDP("udp", udpAddr); err == nil {
			defer udpConn.Close()
			go l.serveUDP(ctx, udpConn)
		}
	}

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	log.Printf("[p2p] TLS 1.3 dual-stack listener active on %s", l.addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("[p2p] accept error: %v", err)
			continue
		}
		fc := protocol.NewFrameConn(conn)
		go l.handler(conn, fc)
	}
}

func (l *Listener) serveUDP(ctx context.Context, conn *net.UDPConn) {
	buf := make([]byte, 2048)
	for {
		if ctx.Err() != nil {
			return
		}
		n, raddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n > 0 && n <= 256 {
			msg := strings.TrimSpace(string(buf[:n]))
			if l.certSHA != "" && (strings.Contains(msg, "xb_discover") || msg == "XB_DISCOVER_V1") {
				resp, _ := json.Marshal(map[string]any{
					"type":        "xb_announce",
					"host_id":     l.hostID,
					"name":        l.hostName,
					"port":        l.listenPort,
					"cert_sha256": l.certSHA,
				})
				_, _ = conn.WriteToUDP(resp, raddr)
				continue
			}
			// Echo small hole-punch probe packets
			_, _ = conn.WriteToUDP(buf[:n], raddr)
		}
	}
}
