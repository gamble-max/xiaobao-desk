package ipc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/xiaobaodesk/win-host/internal/protocol"
	"github.com/xiaobaodesk/win-host/internal/security"
)

// Supervisor launches xb-media-core.exe over local TCP IPC (--tcp 127.0.0.1:<port>)
// with XB_IPC_TOKEN passed via environment variable, and bridges CTRL, VIDEO, AUDIO, and INPUT frames.
type Supervisor struct {
	exePath     string
	mu          sync.Mutex
	cmd         *exec.Cmd
	conn        *protocol.FrameConn
	sink        *protocol.FrameConn
	nextLease   uint64
	activeLease uint64
}

func NewSupervisor(exePath string) *Supervisor {
	return &Supervisor{exePath: exePath}
}

// AcquireSession registers a new active client lease and returns its unique lease ID.
func (s *Supervisor) AcquireSession() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextLease++
	s.activeLease = s.nextLease
	return s.activeLease
}

// ReleaseSession releases the lease if it is still the active session, clearing the client sink.
// Returns true if this call released the active session (so overlapping disconnects do not clobber newer sessions).
func (s *Supervisor) ReleaseSession(lease uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if lease == 0 || s.activeLease != lease {
		return false
	}
	s.activeLease = 0
	s.sink = nil
	return true
}

// ActiveSessionCount returns 1 if a client session lease is currently active, else 0.
func (s *Supervisor) ActiveSessionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeLease != 0 {
		return 1
	}
	return 0
}

// SetClientSink attaches (or detaches when nil) the active remote client FrameConn.
func (s *Supervisor) SetClientSink(fc *protocol.FrameConn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sink = fc
}

func (s *Supervisor) getSink() *protocol.FrameConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sink
}

func (s *Supervisor) getMediaConn() *protocol.FrameConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn
}

// SendToMedia forwards a frame (0x01 CTRL or 0x04 INPUT) to xb-media-core.exe.
func (s *Supervisor) SendToMedia(ch byte, payload []byte) error {
	mc := s.getMediaConn()
	if mc == nil {
		return errors.New("ipc: media core not connected")
	}
	return mc.WriteFrame(ch, payload)
}

// SendCTRL marshals and sends a CTRL message to xb-media-core.exe.
func (s *Supervisor) SendCTRL(v any) error {
	mc := s.getMediaConn()
	if mc == nil {
		return errors.New("ipc: media core not connected")
	}
	return mc.WriteCTRL(v)
}

// Run supervises xb-media-core.exe until ctx is canceled, restarting with backoff on unexpected exit.
func (s *Supervisor) Run(ctx context.Context) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if _, err := os.Stat(s.exePath); err != nil {
			log.Printf("[ipc] waiting for %s (%v)", s.exePath, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		err := s.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		log.Printf("[ipc] xb-media-core exited: %v (restarting in %s)", err, backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func (s *Supervisor) runOnce(ctx context.Context) error {
	token, err := security.RandomHex(32)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer ln.Close()

	addr := ln.Addr().String()
	cmd := exec.Command(s.exePath, "--tcp", addr)
	cmd.Env = append(os.Environ(), "XB_IPC_TOKEN="+token)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start media-core: %w", err)
	}
	log.Printf("[ipc] launched %s (pid=%d, ipc=%s)", s.exePath, cmd.Process.Pid, addr)

	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()

	procExited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(procExited)
	}()

	stopDone := make(chan struct{})
	defer close(stopDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = ln.Close()
			if mc := s.getMediaConn(); mc != nil {
				_ = mc.WriteCTRL(map[string]any{"type": "shutdown"})
			}
			select {
			case <-procExited:
			case <-time.After(600 * time.Millisecond):
				if cmd.Process != nil {
					_ = cmd.Process.Kill()
				}
			}
			if mc := s.getMediaConn(); mc != nil {
				_ = mc.Close()
			}
		case <-stopDone:
		}
	}()

	defer func() {
		s.mu.Lock()
		mc := s.conn
		s.conn = nil
		s.cmd = nil
		s.mu.Unlock()
		if mc != nil {
			_ = mc.WriteCTRL(map[string]any{"type": "shutdown"})
			_ = mc.Close()
		}
		select {
		case <-procExited:
		case <-time.After(600 * time.Millisecond):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-procExited
		}
	}()

	fc, err := acceptAndAuth(ln, token)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.conn = fc
	s.mu.Unlock()

	hbDone := make(chan struct{})
	defer close(hbDone)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-hbDone:
				return
			case <-ticker.C:
				if s.ActiveSessionCount() > 0 {
					if err := fc.WriteCTRL(map[string]any{"type": "ping"}); err != nil {
						return
					}
				}
			}
		}
	}()

	// Read loop forwarding VIDEO, AUDIO, and CTRL frames to the active remote client
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		frame, err := fc.ReadFrame()
		if err != nil {
			return err
		}
		s.forwardFromMedia(frame)
	}
}

// acceptAndAuth accepts a single local IPC connection, closes the listener immediately,
// and enforces strict first-frame CTRL auth with constant-time token verification.
func acceptAndAuth(ln net.Listener, expectedToken string) (*protocol.FrameConn, error) {
	if tcpLn, ok := ln.(*net.TCPListener); ok {
		_ = tcpLn.SetDeadline(time.Now().Add(10 * time.Second))
	}
	rawConn, err := ln.Accept()
	if err != nil {
		return nil, fmt.Errorf("accept media-core: %w", err)
	}
	_ = ln.Close()

	fc := protocol.NewFrameConn(rawConn)
	_ = rawConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	firstFrame, err := fc.ReadFrame()
	if err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("read initial frame: %w", err)
	}
	_ = rawConn.SetReadDeadline(time.Time{})

	if firstFrame.Channel != protocol.ChannelCTRL {
		_ = rawConn.Close()
		return nil, errors.New("media-core first frame must be CTRL auth")
	}
	var env struct {
		Type  string `json:"type"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(firstFrame.Payload, &env); err != nil || env.Type != "auth" {
		_ = rawConn.Close()
		return nil, errors.New("media-core first frame must be auth")
	}
	if subtle.ConstantTimeCompare([]byte(env.Token), []byte(expectedToken)) != 1 {
		_ = rawConn.Close()
		return nil, errors.New("media-core invalid IPC token")
	}
	return fc, nil
}

func (s *Supervisor) forwardFromMedia(frame protocol.Frame) {
	sink := s.getSink()
	if sink == nil {
		return
	}
	switch frame.Channel {
	case protocol.ChannelCTRL, protocol.ChannelVIDEO, protocol.ChannelAUDIO:
		if err := sink.WriteFrame(frame.Channel, frame.Payload); err != nil {
			log.Printf("[ipc] forward to client failed: %v", err)
		}
	}
}
