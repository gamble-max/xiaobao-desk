package proto

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	channels := []byte{ChanCtrl, ChanVideo, ChanAudio, ChanInput, ChanTerm, ChanFile}
	for i, ch := range channels {
		payload := []byte{byte(i), 'x', 'b', '-', 'd', 'e', 's', 'k'}
		if err := WriteFrame(&buf, ch, payload); err != nil {
			t.Fatalf("WriteFrame(%x): %v", ch, err)
		}
	}

	for i, ch := range channels {
		f, err := ReadFrame(&buf, DefaultMaxSize)
		if err != nil {
			t.Fatalf("ReadFrame(%x): %v", ch, err)
		}
		if f.Channel != ch {
			t.Fatalf("channel mismatch: got 0x%02x want 0x%02x", f.Channel, ch)
		}
		want := []byte{byte(i), 'x', 'b', '-', 'd', 'e', 's', 'k'}
		if !bytes.Equal(f.Payload, want) {
			t.Fatalf("payload mismatch: got %v want %v", f.Payload, want)
		}
	}
}

func TestFrameTooLarge(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, ChanVideo, make([]byte, 128)); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	_, err := ReadFrame(&buf, 64)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected ErrFrameTooLarge, got %v", err)
	}
}

func TestFrameTruncatedEOF(t *testing.T) {
	var buf bytes.Buffer
	buf.Write([]byte{ChanCtrl, 0, 0, 0, 10, 'a', 'b'})
	_, err := ReadFrame(&buf, DefaultMaxSize)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected ErrUnexpectedEOF, got %v", err)
	}
}

func TestFrameCaps(t *testing.T) {
	var buf bytes.Buffer
	// ChanCtrl must reject > 64 KiB
	if err := WriteFrame(&buf, ChanCtrl, make([]byte, MaxCtrlSize+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected ErrFrameTooLarge for oversized ChanCtrl, got %v", err)
	}
	// ChanVideo must reject > 8 MiB
	if err := WriteFrame(&buf, ChanVideo, make([]byte, MaxRelaySize+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("expected ErrFrameTooLarge for oversized ChanVideo, got %v", err)
	}
	// ReadCtrl must reject > 64 KiB even if written as a raw frame
	if err := WriteFrame(&buf, ChanCtrl, make([]byte, 16)); err != nil {
		t.Fatalf("WriteFrame small ctrl: %v", err)
	}
	if _, err := ReadCtrl(&buf, nil); err != nil {
		t.Fatalf("ReadCtrl small ctrl: %v", err)
	}
}
