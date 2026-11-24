package ipc

import (
	"context"
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

// Supervisor launches xb-media-core.exe over local TCP IPC (--tcp 127.0.0.1:<port> --token <hex>)
// and bridges CTRL, VIDEO, AUDIO, and INPUT frames with the active remote session.
type Supervisor struct {
	exePath string
	mu      sync.Mutex
	cmd     *exec.Cmd
	conn    *protocol.FrameConn
	sink    *protocol.FrameConn
}

func NewSupervisor(exePath string) *Supervisor {
	return &Supervisor{exePath: exePath}
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
	token, err := security.RandomHex(16)
	if err != nil {
		return err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer ln.Close()

	addr := ln.Addr().String()
	cmd := exec.CommandContext(ctx, s.exePath, "--tcp", addr, "--token", token)
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

	defer func() {
		s.mu.Lock()
		if s.conn != nil {
			_ = s.conn.Close()
			s.conn = nil
		}
		s.cmd = nil
		s.mu.Unlock()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	// Accept media-core connection with 10s timeout
	if tcpLn, ok := ln.(*net.TCPListener); ok {
		_ = tcpLn.SetDeadline(time.Now().Add(10 * time.Second))
	}
	rawConn, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("accept media-core: %w", err)
	}
	fc := protocol.NewFrameConn(rawConn)

	// Optional initial token verification if media-core sends an auth frame first
	_ = rawConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	firstFrame, err := fc.ReadFrame()
	if err != nil {
		return fmt.Errorf("read initial frame: %w", err)
	}
	_ = rawConn.SetReadDeadline(time.Time{})

	s.mu.Lock()
	s.conn = fc
	s.mu.Unlock()

	if firstFrame.Channel == protocol.ChannelCTRL {
		var env struct {
			Type  string `json:"type"`
			Token string `json:"token"`
		}
		if json.Unmarshal(firstFrame.Payload, &env) == nil && env.Type == "auth" {
			if env.Token != token {
				return errors.New("media-core invalid IPC token")
			}
		} else {
			s.forwardFromMedia(firstFrame)
		}
	} else {
		s.forwardFromMedia(firstFrame)
	}

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
