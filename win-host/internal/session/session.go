package session

import (
	"encoding/json"
	"log"
	"net"
	"sync"
	"time"

	"github.com/xiaobaodesk/win-host/internal/config"
	"github.com/xiaobaodesk/win-host/internal/filetransfer"
	"github.com/xiaobaodesk/win-host/internal/ipc"
	"github.com/xiaobaodesk/win-host/internal/protocol"
	"github.com/xiaobaodesk/win-host/internal/security"
	"github.com/xiaobaodesk/win-host/internal/term"
)

type cfgPolicy struct {
	cfg *config.Config
}

func (p cfgPolicy) CandidateAuthSecrets() [][]byte { return p.cfg.CandidateAuthSecrets() }
func (p cfgPolicy) GetTOTPSecret() string          { return p.cfg.TOTPSecret }
func (p cfgPolicy) IsTOTPRequired() bool           { return p.cfg.RequireTOTP }
func (p cfgPolicy) GetAllowedDeviceIDs() []string  { return p.cfg.AllowedDeviceIDs }
func (p cfgPolicy) IsStrictAllowlist() bool        { return p.cfg.StrictAllowlist }

// HandleConnection authenticates a remote client (direct P2P or hub relay)
// and serves CTRL, INPUT, TERM (ConPTY), FILE, and media IPC bridging.
func HandleConnection(conn net.Conn, fc *protocol.FrameConn, cfg *config.Config, sup *ipc.Supervisor, source string) {
	defer fc.Close()

	sessionID, deviceID, err := security.AuthenticateClient(conn, fc, cfgPolicy{cfg: cfg})
	if err != nil {
		log.Printf("[session] auth failed (%s): %v", source, err)
		return
	}
	log.Printf("[session] authenticated (%s) device=%s session=%s", source, deviceID, sessionID)

	if sup != nil {
		sup.SetClientSink(fc)
		_ = sup.SendCTRL(map[string]any{"type": "view_only", "enable": false})
		_ = sup.SendCTRL(map[string]any{"type": "preserve_cursor", "enable": true})
		_ = sup.SendCTRL(map[string]any{"type": "idr"})
		defer func() {
			sup.SetClientSink(nil)
			_ = sup.SendCTRL(map[string]any{"type": "view_only", "enable": false})
			_ = sup.SendCTRL(map[string]any{"type": "preserve_cursor", "enable": true})
			_ = sup.SendCTRL(map[string]any{"type": "privacy_screen", "enable": false})
			_ = sup.SendCTRL(map[string]any{"type": "stream_enable", "enable": true})
			_ = sup.SendCTRL(map[string]any{"type": "mute_host", "enable": false})
			_ = sup.SendCTRL(map[string]any{"type": "set_superscreen", "enable": false})
			if cfg.AutoLockOnDisconnect {
				_ = sup.SendCTRL(map[string]any{"type": "desktop_action", "cmd": "lock"})
			}
		}()
	}

	ft := filetransfer.NewHandler(cfg.FileRootDir)
	defer ft.Close()

	var termMu sync.Mutex
	var pty *term.ConPTY
	defer func() {
		termMu.Lock()
		if pty != nil {
			_ = pty.Close()
			pty = nil
		}
		termMu.Unlock()
	}()

	for {
		frame, err := fc.ReadFrame()
		if err != nil {
			log.Printf("[session] closed (%s, session=%s): %v", source, sessionID, err)
			return
		}

		switch frame.Channel {
		case protocol.ChannelCTRL:
			var env protocol.CtrlEnvelope
			if err := json.Unmarshal(frame.Payload, &env); err != nil {
				continue
			}
			switch env.Type {
			case "ping":
				_ = fc.WriteCTRL(protocol.PingPongMsg{Type: "pong", Ts: time.Now().UnixMilli()})
			case "set_encoder", "set_superscreen", "mute_host", "view_only", "preserve_cursor", "desktop_action", "display_config", "host_action", "clipboard", "stream_enable", "privacy_screen":
				if sup != nil {
					_ = sup.SendToMedia(protocol.ChannelCTRL, frame.Payload)
				}
			}

		case protocol.ChannelINPUT:
			if sup != nil {
				_ = sup.SendToMedia(protocol.ChannelINPUT, frame.Payload)
			}

		case protocol.ChannelTERM:
			if len(frame.Payload) < 1 {
				continue
			}
			subcmd := frame.Payload[0]
			body := frame.Payload[1:]
			switch subcmd {
			case protocol.TermOpen:
				var sz protocol.TermSizeMsg
				_ = json.Unmarshal(body, &sz)
				termMu.Lock()
				if pty != nil {
					_ = pty.Close()
					pty = nil
				}
				newPTY, err := term.StartPowerShell(sz.Cols, sz.Rows, fc)
				if err != nil {
					log.Printf("[term] ConPTY start failed: %v", err)
					_ = fc.WriteSubcmdBytes(protocol.ChannelTERM, protocol.TermStdout, []byte("\r\n[ConPTY error: "+err.Error()+"]\r\n"))
				} else {
					pty = newPTY
				}
				termMu.Unlock()

			case protocol.TermStdin:
				termMu.Lock()
				cur := pty
				termMu.Unlock()
				if cur != nil {
					_ = cur.WriteStdin(body)
				}

			case protocol.TermResize:
				var sz protocol.TermSizeMsg
				if json.Unmarshal(body, &sz) == nil {
					termMu.Lock()
					cur := pty
					termMu.Unlock()
					if cur != nil {
						_ = cur.Resize(sz.Cols, sz.Rows)
					}
				}

			case protocol.TermClose:
				termMu.Lock()
				if pty != nil {
					_ = pty.Close()
					pty = nil
				}
				termMu.Unlock()
			}

		case protocol.ChannelFILE:
			ft.HandleFrame(fc, frame.Payload)
		}
	}
}
