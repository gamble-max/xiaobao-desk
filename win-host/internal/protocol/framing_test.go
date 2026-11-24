package protocol

import (
	"bytes"
	"errors"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	wantPayload := []byte(`{"type":"ping","ts":12345}`)
	if err := WriteFrame(&buf, ChannelCTRL, wantPayload); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if got.Channel != ChannelCTRL {
		t.Fatalf("channel mismatch: got 0x%02x want 0x%02x", got.Channel, ChannelCTRL)
	}
	if !bytes.Equal(got.Payload, wantPayload) {
		t.Fatalf("payload mismatch: got %q want %q", got.Payload, wantPayload)
	}
}

func TestReadFrameRejectsOversize(t *testing.T) {
	// Channel 0x02, Length = MaxPayloadLen + 1 (0x04000001)
	raw := []byte{ChannelVIDEO, 0x04, 0x00, 0x00, 0x01}
	_, err := ReadFrame(bytes.NewReader(raw))
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("expected ErrPayloadTooLarge, got %v", err)
	}
}

func TestFileChunkEncodeDecode(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555555"
	data := []byte("hello-chunk-data")
	enc, err := EncodeFileChunk(id, 4096, data)
	if err != nil {
		t.Fatalf("EncodeFileChunk: %v", err)
	}
	gotID, gotOff, gotData, err := DecodeFileChunk(enc)
	if err != nil {
		t.Fatalf("DecodeFileChunk: %v", err)
	}
	if gotID != id || gotOff != 4096 || !bytes.Equal(gotData, data) {
		t.Fatalf("decoded mismatch: id=%s off=%d data=%q", gotID, gotOff, gotData)
	}
}
