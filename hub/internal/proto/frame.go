package proto

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	ChanCtrl  byte = 0x01
	ChanVideo byte = 0x02
	ChanAudio byte = 0x03
	ChanInput byte = 0x04
	ChanTerm  byte = 0x05
	ChanFile  byte = 0x06

	HeaderSize     = 5
	MaxCtrlSize    = 64 * 1024        // 64 KiB
	MaxRelaySize   = 8 * 1024 * 1024  // 8 MiB
	DefaultMaxSize = MaxRelaySize
)

var ErrFrameTooLarge = errors.New("frame payload exceeds maximum allowed size")

// Frame represents a single protocol frame: [1B ChannelID][4B BigEndian Len][Payload].
type Frame struct {
	Channel byte
	Payload []byte
}

// ReadFrame reads a single framed message from r.
func ReadFrame(r io.Reader, maxLen uint32) (Frame, error) {
	if maxLen == 0 {
		maxLen = DefaultMaxSize
	}
	var hdr [HeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Frame{}, err
	}
	ch := hdr[0]
	length := binary.BigEndian.Uint32(hdr[1:5])
	if length > maxLen {
		return Frame{}, fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, length, maxLen)
	}
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return Frame{}, err
		}
	}
	return Frame{Channel: ch, Payload: payload}, nil
}

// WriteFrame writes a single framed message to w.
func WriteFrame(w io.Writer, channel byte, payload []byte) error {
	maxLen := MaxRelaySize
	if channel == ChanCtrl {
		maxLen = MaxCtrlSize
	}
	if len(payload) > maxLen {
		return fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, len(payload), maxLen)
	}
	buf := make([]byte, HeaderSize+len(payload))
	buf[0] = channel
	binary.BigEndian.PutUint32(buf[1:5], uint32(len(payload)))
	copy(buf[HeaderSize:], payload)
	_, err := w.Write(buf)
	return err
}

// WriteCtrl marshals v as JSON and writes it on ChanCtrl (0x01).
func WriteCtrl(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return WriteFrame(w, ChanCtrl, data)
}

// ReadCtrl reads a frame and ensures it is ChanCtrl, unmarshaling JSON into out.
func ReadCtrl(r io.Reader, out any) (Frame, error) {
	f, err := ReadFrame(r, MaxCtrlSize)
	if err != nil {
		return f, err
	}
	if f.Channel != ChanCtrl {
		return f, fmt.Errorf("expected ChanCtrl (0x01), got 0x%02x", f.Channel)
	}
	if out != nil {
		if err := json.Unmarshal(f.Payload, out); err != nil {
			return f, err
		}
	}
	return f, nil
}
