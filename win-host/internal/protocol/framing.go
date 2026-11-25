package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	ChannelCTRL  byte = 0x01
	ChannelVIDEO byte = 0x02
	ChannelAUDIO byte = 0x03
	ChannelINPUT byte = 0x04
	ChannelTERM  byte = 0x05
	ChannelFILE  byte = 0x06
)

const (
	TermOpen   byte = 0x01
	TermStdin  byte = 0x02
	TermStdout byte = 0x03
	TermResize byte = 0x04
	TermClose  byte = 0x05
)

const (
	FileListDir     byte = 0x01
	FileListResp    byte = 0x02
	FileDownloadReq byte = 0x03
	FileUploadStart byte = 0x04
	FileChunk       byte = 0x05
	FileDone        byte = 0x06
	FileErr         byte = 0x07
)

// MaxPayloadLen caps a single frame payload at 64 MiB to prevent OOM on corrupt headers.
const MaxPayloadLen = 64 * 1024 * 1024

// FileChunkHeaderSize is 36B UTF-8 UUID + 8B BigEndian offset.
const FileChunkHeaderSize = 36 + 8

var ErrPayloadTooLarge = errors.New("protocol: payload length exceeds maximum")

// Frame represents a single framed message on TLS streams or local IPC pipes.
type Frame struct {
	Channel byte
	Payload []byte
}

// ReadFrame reads one `[1B ChannelID][4B BigEndian PayloadLen][PayloadBytes]` frame.
func ReadFrame(r io.Reader) (Frame, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	ch := hdr[0]
	length := binary.BigEndian.Uint32(hdr[1:5])
	if length > MaxPayloadLen {
		return Frame{}, fmt.Errorf("%w: %d bytes", ErrPayloadTooLarge, length)
	}
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return Frame{}, err
		}
	}
	return Frame{Channel: ch, Payload: payload}, nil
}

// WriteFrame writes one `[1B ChannelID][4B BigEndian PayloadLen][PayloadBytes]` frame.
func WriteFrame(w io.Writer, ch byte, payload []byte) error {
	if len(payload) > MaxPayloadLen {
		return fmt.Errorf("%w: %d bytes", ErrPayloadTooLarge, len(payload))
	}
	var hdr [5]byte
	hdr[0] = ch
	binary.BigEndian.PutUint32(hdr[1:5], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// FrameConn wraps an io.ReadWriteCloser with a write mutex for concurrent frame sending.
type FrameConn struct {
	rwc io.ReadWriteCloser
	mu  sync.Mutex
}

func NewFrameConn(rwc io.ReadWriteCloser) *FrameConn {
	return &FrameConn{rwc: rwc}
}

func (c *FrameConn) ReadFrame() (Frame, error) {
	return ReadFrame(c.rwc)
}

func (c *FrameConn) WriteFrame(ch byte, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return WriteFrame(c.rwc, ch, payload)
}

func (c *FrameConn) WriteCTRL(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.WriteFrame(ChannelCTRL, data)
}

func (c *FrameConn) WriteSubcmdJSON(ch, subcmd byte, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	buf := make([]byte, 1+len(data))
	buf[0] = subcmd
	copy(buf[1:], data)
	return c.WriteFrame(ch, buf)
}

func (c *FrameConn) WriteSubcmdBytes(ch, subcmd byte, data []byte) error {
	buf := make([]byte, 1+len(data))
	buf[0] = subcmd
	copy(buf[1:], data)
	return c.WriteFrame(ch, buf)
}

func (c *FrameConn) Close() error {
	return c.rwc.Close()
}

// CtrlEnvelope is used to inspect the "type" field of any 0x01 CTRL JSON message.
type CtrlEnvelope struct {
	Type string `json:"type"`
}

type ChallengeMsg struct {
	Type         string `json:"type"`
	Nonce        string `json:"nonce"`
	TOTPRequired bool   `json:"totp_required"`
}

type AuthMsg struct {
	Type     string `json:"type"`
	DeviceID string `json:"device_id"`
	Role     string `json:"role"`
	HMAC     string `json:"hmac"`
	TOTP     string `json:"totp,omitempty"`
}

type AuthOKMsg struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}

type AuthErrMsg struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

type PeerRegisterMsg struct {
	Type      string   `json:"type"`
	HostID    string   `json:"host_id"`
	Endpoints []string `json:"endpoints"`
	CertSHA   string   `json:"cert_sha256,omitempty"`
}

type RelayBindMsg struct {
	Type           string `json:"type"`
	SessionID      string `json:"session_id"`
	ClientDeviceID string `json:"client_device_id,omitempty"`
}

type RelayReadyMsg struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
}

type PingPongMsg struct {
	Type string `json:"type"`
	Ts   int64  `json:"ts,omitempty"`
}

type TermSizeMsg struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type FileListDirMsg struct {
	Path string `json:"path"`
}

type FileEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
}

type FileListRespMsg struct {
	Path    string      `json:"path"`
	Entries []FileEntry `json:"entries"`
}

type FileDownloadReqMsg struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type FileUploadStartMsg struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type FileDoneMsg struct {
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type FileErrMsg struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

// EncodeFileChunk builds `[36B UTF-8 UUID id][8B BigEndian offset][raw bytes]`.
func EncodeFileChunk(id string, offset uint64, data []byte) ([]byte, error) {
	if len(id) != 36 {
		return nil, fmt.Errorf("file chunk id must be 36 bytes, got %d", len(id))
	}
	buf := make([]byte, FileChunkHeaderSize+len(data))
	copy(buf[0:36], id)
	binary.BigEndian.PutUint64(buf[36:44], offset)
	copy(buf[44:], data)
	return buf, nil
}

// DecodeFileChunk parses `[36B UTF-8 UUID id][8B BigEndian offset][raw bytes]`.
func DecodeFileChunk(payload []byte) (id string, offset uint64, data []byte, err error) {
	if len(payload) < FileChunkHeaderSize {
		return "", 0, nil, fmt.Errorf("file chunk too short: %d", len(payload))
	}
	id = string(payload[0:36])
	offset = binary.BigEndian.Uint64(payload[36:44])
	data = payload[44:]
	return id, offset, data, nil
}
