package hub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"xiaobaodesk/hub/internal/proto"
)

var (
	ErrRelayTimeout   = errors.New("relay bind timed out waiting for peer")
	ErrRelayDuplicate = errors.New("relay role already bound for session")
)

// ControlSession wraps an authenticated control connection for thread-safe writes.
type ControlSession struct {
	Conn     net.Conn
	DeviceID string
	Role     string
	writeMu  sync.Mutex
}

// WriteCtrl safely writes a JSON control message to the control connection.
func (cs *ControlSession) WriteCtrl(v any) error {
	if cs == nil || cs.Conn == nil {
		return errors.New("nil control session")
	}
	cs.writeMu.Lock()
	defer cs.writeMu.Unlock()
	_ = cs.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := proto.WriteCtrl(cs.Conn, v)
	_ = cs.Conn.SetWriteDeadline(time.Time{})
	return err
}

// PeerRecord stores the latest known endpoints and control session for a host.
type PeerRecord struct {
	HostID      string
	DeviceID    string
	CertSHA256  string
	Endpoints   []string
	ObservedTCP string
	ObservedUDP string
	LastSeen    time.Time
	Control     *ControlSession
}

type relaySlot struct {
	mu       sync.Mutex
	client   net.Conn
	host     net.Conn
	paired   bool
	readyCh  chan struct{}
	spliceCh chan struct{}
	pairErr  error
	doneOnce sync.Once
}

// Registry manages registered hosts and pending relay_bind rendezvous slots.
type Registry struct {
	mu      sync.RWMutex
	peers   map[string]*PeerRecord
	peerTTL time.Duration

	relayMu sync.Mutex
	relays  map[string]*relaySlot
}

// NewRegistry creates a new Registry with the given peer TTL.
func NewRegistry(peerTTL time.Duration) *Registry {
	if peerTTL <= 0 {
		peerTTL = 120 * time.Second
	}
	return &Registry{
		peers:   make(map[string]*PeerRecord),
		peerTTL: peerTTL,
		relays:  make(map[string]*relaySlot),
	}
}

// RegisterPeer stores or updates a host's endpoints, TLS fingerprint, and active control session.
func (r *Registry) RegisterPeer(hostID, deviceID, certSHA256 string, endpoints []string, observedTCP string, ctrl *ControlSession) PeerRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	rec, ok := r.peers[hostID]
	if !ok {
		rec = &PeerRecord{HostID: hostID}
		r.peers[hostID] = rec
	}
	rec.DeviceID = deviceID
	if certSHA256 != "" {
		rec.CertSHA256 = certSHA256
	}
	rec.Endpoints = append([]string(nil), endpoints...)
	if observedTCP != "" {
		rec.ObservedTCP = observedTCP
	}
	rec.LastSeen = time.Now().UTC()
	if ctrl != nil {
		rec.Control = ctrl
	}
	return *rec
}

// UpdateObservedUDP updates the reflected UDP address for a registered host (or creates a stub if needed).
func (r *Registry) UpdateObservedUDP(hostID, observedUDP string) {
	if hostID == "" || observedUDP == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	rec, ok := r.peers[hostID]
	if !ok {
		rec = &PeerRecord{HostID: hostID}
		r.peers[hostID] = rec
	}
	rec.ObservedUDP = observedUDP
	rec.LastSeen = time.Now().UTC()
}

// UnregisterControl clears the control session if it matches ctrl.
func (r *Registry) UnregisterControl(hostID string, ctrl *ControlSession) {
	if hostID == "" || ctrl == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if rec, ok := r.peers[hostID]; ok && rec.Control == ctrl {
		rec.Control = nil
	}
}

// QueryPeer returns a copy of the PeerRecord if present and not expired.
// If hostID is "", "default", or "default-host", it resolves to the sole active host (or most recently seen active host).
func (r *Registry) QueryPeer(hostID string) (PeerRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if hostID == "" || hostID == "default" || hostID == "default-host" {
		var best *PeerRecord
		for _, rec := range r.peers {
			if rec.Control == nil && time.Since(rec.LastSeen) > r.peerTTL {
				continue
			}
			if best == nil {
				best = rec
				continue
			}
			// Prefer online (Control != nil) hosts, then most recently seen.
			if (rec.Control != nil) != (best.Control != nil) {
				if rec.Control != nil {
					best = rec
				}
				continue
			}
			if rec.LastSeen.After(best.LastSeen) {
				best = rec
			}
		}
		if best == nil {
			return PeerRecord{}, false
		}
		out := *best
		out.Endpoints = append([]string(nil), best.Endpoints...)
		return out, true
	}

	rec, ok := r.peers[hostID]
	if !ok {
		return PeerRecord{}, false
	}
	if rec.Control == nil && time.Since(rec.LastSeen) > r.peerTTL {
		return PeerRecord{}, false
	}
	out := *rec
	out.Endpoints = append([]string(nil), rec.Endpoints...)
	return out, true
}

// BindRelay pairs a client and host connection on sessionID.
// When both sides have bound, it writes relay_ready to both connections.
// Exactly one side receives isLeader=true and should call spliceConns(conn, peerConn) then close(spliceCh).
// The other side receives isLeader=false and should wait on <-spliceCh before returning.
func (r *Registry) BindRelay(ctx context.Context, sessionID, role string, conn net.Conn, timeout time.Duration) (peerConn net.Conn, spliceCh chan struct{}, isLeader bool, err error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	r.relayMu.Lock()
	slot, exists := r.relays[sessionID]
	if !exists {
		slot = &relaySlot{
			readyCh:  make(chan struct{}),
			spliceCh: make(chan struct{}),
		}
		r.relays[sessionID] = slot
	}
	r.relayMu.Unlock()

	slot.mu.Lock()
	if role == "host" {
		if slot.host != nil {
			slot.mu.Unlock()
			return nil, nil, false, ErrRelayDuplicate
		}
		slot.host = conn
	} else {
		if slot.client != nil {
			slot.mu.Unlock()
			return nil, nil, false, ErrRelayDuplicate
		}
		slot.client = conn
	}

	if slot.client != nil && slot.host != nil && !slot.paired {
		slot.paired = true
		cConn := slot.client
		hConn := slot.host
		sCh := slot.spliceCh
		slot.mu.Unlock()

		// Remove from active pending map
		r.relayMu.Lock()
		delete(r.relays, sessionID)
		r.relayMu.Unlock()

		readyMsg := map[string]any{
			"type":       "relay_ready",
			"session_id": sessionID,
		}
		if err := proto.WriteCtrl(cConn, readyMsg); err != nil {
			slot.mu.Lock()
			slot.pairErr = fmt.Errorf("write relay_ready to client: %w", err)
			slot.mu.Unlock()
			slot.doneOnce.Do(func() { close(slot.readyCh) })
			return nil, nil, false, slot.pairErr
		}
		if err := proto.WriteCtrl(hConn, readyMsg); err != nil {
			slot.mu.Lock()
			slot.pairErr = fmt.Errorf("write relay_ready to host: %w", err)
			slot.mu.Unlock()
			slot.doneOnce.Do(func() { close(slot.readyCh) })
			return nil, nil, false, slot.pairErr
		}

		slot.doneOnce.Do(func() { close(slot.readyCh) })
		// The second arriver acts as the splice leader.
		if role == "host" {
			return cConn, sCh, true, nil
		}
		return hConn, sCh, true, nil
	}
	slot.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-slot.readyCh:
		slot.mu.Lock()
		defer slot.mu.Unlock()
		if slot.pairErr != nil {
			return nil, nil, false, slot.pairErr
		}
		if role == "host" {
			return slot.client, slot.spliceCh, false, nil
		}
		return slot.host, slot.spliceCh, false, nil
	case <-timer.C:
		r.cleanupRelaySlot(sessionID, slot)
		return nil, nil, false, ErrRelayTimeout
	case <-ctx.Done():
		r.cleanupRelaySlot(sessionID, slot)
		return nil, nil, false, ctx.Err()
	}
}

func (r *Registry) cleanupRelaySlot(sessionID string, slot *relaySlot) {
	r.relayMu.Lock()
	if cur, ok := r.relays[sessionID]; ok && cur == slot {
		delete(r.relays, sessionID)
	}
	r.relayMu.Unlock()
}

// NewSessionID generates a random 16-byte hex session ID.
func NewSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
