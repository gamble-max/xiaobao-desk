package ipc

import (
	"net"
	"testing"
	"time"

	"github.com/xiaobaodesk/win-host/internal/protocol"
)

func TestAcceptAndAuthRejectsBadAndMissingToken(t *testing.T) {
	const expectedToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	cases := []struct {
		name string
		send func(fc *protocol.FrameConn)
	}{
		{
			name: "wrong_channel",
			send: func(fc *protocol.FrameConn) {
				_ = fc.WriteFrame(protocol.ChannelVIDEO, []byte{1, 2, 3})
			},
		},
		{
			name: "missing_auth_type",
			send: func(fc *protocol.FrameConn) {
				_ = fc.WriteCTRL(map[string]any{"type": "ping"})
			},
		},
		{
			name: "missing_token",
			send: func(fc *protocol.FrameConn) {
				_ = fc.WriteCTRL(map[string]any{"type": "auth"})
			},
		},
		{
			name: "wrong_token",
			send: func(fc *protocol.FrameConn) {
				_ = fc.WriteCTRL(map[string]any{"type": "auth", "token": "deadbeef"})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("Listen: %v", err)
			}
			defer ln.Close()

			done := make(chan struct{})
			go func() {
				defer close(done)
				c, err := net.DialTimeout("tcp", ln.Addr().String(), 2*time.Second)
				if err != nil {
					return
				}
				defer c.Close()
				fc := protocol.NewFrameConn(c)
				tc.send(fc)
			}()

			fc, err := acceptAndAuth(ln, expectedToken)
			if err == nil {
				_ = fc.Close()
				t.Fatalf("expected auth rejection for %s, got nil error", tc.name)
			}
			<-done
		})
	}
}

func TestAcceptAndAuthAcceptsValidTokenAndClosesListener(t *testing.T) {
	const expectedToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	clientErr := make(chan error, 1)
	go func() {
		c, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			clientErr <- err
			return
		}
		defer c.Close()
		fc := protocol.NewFrameConn(c)
		clientErr <- fc.WriteCTRL(map[string]any{"type": "auth", "token": expectedToken})
	}()

	fc, err := acceptAndAuth(ln, expectedToken)
	if err != nil {
		t.Fatalf("expected valid auth to succeed, got: %v", err)
	}
	defer fc.Close()

	if err := <-clientErr; err != nil {
		t.Fatalf("client write failed: %v", err)
	}

	// Listener must be closed immediately after Accept() so a second process cannot connect.
	if c2, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
		_ = c2.Close()
		t.Fatalf("expected listener to be closed after first accept")
	}
}

func TestSessionLeaseTracking(t *testing.T) {
	sup := NewSupervisor("xb-media-core.exe")
	if sup.ActiveSessionCount() != 0 {
		t.Fatalf("expected 0 active sessions initially")
	}

	lease1 := sup.AcquireSession()
	if sup.ActiveSessionCount() != 1 {
		t.Fatalf("expected 1 active session after lease1")
	}

	// Overlapping new session acquires lease2 before lease1 disconnects.
	lease2 := sup.AcquireSession()
	if lease2 == lease1 {
		t.Fatalf("expected distinct lease IDs")
	}

	// Stale disconnect for lease1 must NOT release lease2.
	if sup.ReleaseSession(lease1) {
		t.Fatalf("expected ReleaseSession(lease1) to return false when lease2 is active")
	}
	if sup.ActiveSessionCount() != 1 {
		t.Fatalf("expected ActiveSessionCount to remain 1 after stale release")
	}

	if !sup.ReleaseSession(lease2) {
		t.Fatalf("expected ReleaseSession(lease2) to return true")
	}
	if sup.ActiveSessionCount() != 0 {
		t.Fatalf("expected 0 active sessions after lease2 release")
	}
}
