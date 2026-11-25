package hub

import (
	"context"
	"encoding/json"
	"errors"
	"net"
)

type udpPacket struct {
	Type         string `json:"type"`
	HostID       string `json:"host_id,omitempty"`
	DeviceID     string `json:"device_id,omitempty"`
	FromDeviceID string `json:"from_device_id,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	TS           int64  `json:"ts,omitempty"`
}

// ServeUDP handles UDP endpoint reflection and NAT hole-punch coordination on pc until ctx is cancelled.
func (s *Server) ServeUDP(ctx context.Context, pc net.PacketConn) error {
	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()

	buf := make([]byte, 4096)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}

		var pkt udpPacket
		if err := json.Unmarshal(buf[:n], &pkt); err != nil {
			continue
		}

		remoteStr := addr.String()
		ip, port, _ := net.SplitHostPort(remoteStr)

		switch pkt.Type {
		case "udp_ping", "ping":
			if pkt.HostID != "" {
				s.Registry.UpdateObservedUDP(pkt.HostID, remoteStr)
			}
			resp, _ := json.Marshal(map[string]any{
				"type":          "udp_pong",
				"observed_addr": remoteStr,
				"ip":            ip,
				"port":          port,
				"ts":            pkt.TS,
			})
			_, _ = pc.WriteTo(resp, addr)

		case "punch_req":
			rec, found := s.Registry.QueryPeer(pkt.HostID)
			if found && rec.ObservedUDP != "" {
				if hostAddr, err := net.ResolveUDPAddr("udp", rec.ObservedUDP); err == nil {
					notify := map[string]any{
						"type":           "punch_notify",
						"peer_addr":      remoteStr,
						"from_device_id": pkt.FromDeviceID,
						"session_id":     pkt.SessionID,
					}
					notifyBytes, _ := json.Marshal(notify)
					_, _ = pc.WriteTo(notifyBytes, hostAddr)
					if rec.Control != nil {
						_ = rec.Control.WriteCtrl(notify)
					}
				}
			}
			ack, _ := json.Marshal(map[string]any{
				"type":       "punch_ack",
				"found":      found && rec.ObservedUDP != "",
				"host_id":    pkt.HostID,
				"host_udp":   rec.ObservedUDP,
				"your_udp":   remoteStr,
				"session_id": pkt.SessionID,
			})
			_, _ = pc.WriteTo(ack, addr)
		}
	}
}
