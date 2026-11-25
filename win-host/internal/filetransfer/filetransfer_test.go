package filetransfer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/xiaobaodesk/win-host/internal/protocol"
)

func TestListDirDownloadAndUpload(t *testing.T) {
	root := t.TempDir()
	srcData := bytes.Repeat([]byte("XiaobaoDesk-FileTransfer-"), 5000) // ~125 KiB (multi-chunk)
	srcSum := sha256.Sum256(srcData)
	srcSHA := hex.EncodeToString(srcSum[:])

	if err := os.WriteFile(filepath.Join(root, "sample.bin"), srcData, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	srvConn, cliConn := net.Pipe()
	defer srvConn.Close()
	defer cliConn.Close()

	srvFC := protocol.NewFrameConn(srvConn)
	cliFC := protocol.NewFrameConn(cliConn)

	h := NewHandler(root)
	defer h.Close()

	go func() {
		for {
			f, err := srvFC.ReadFrame()
			if err != nil {
				return
			}
			if f.Channel == protocol.ChannelFILE {
				h.HandleFrame(srvFC, f.Payload)
			}
		}
	}()

	// 1. Test list_dir
	if err := cliFC.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileListDir, protocol.FileListDirMsg{Path: "/"}); err != nil {
		t.Fatalf("list_dir write: %v", err)
	}
	f, err := cliFC.ReadFrame()
	if err != nil {
		t.Fatalf("list_dir read: %v", err)
	}
	if f.Payload[0] != protocol.FileListResp {
		t.Fatalf("expected FileListResp, got 0x%02x", f.Payload[0])
	}
	var listResp protocol.FileListRespMsg
	if err := json.Unmarshal(f.Payload[1:], &listResp); err != nil {
		t.Fatalf("unmarshal list_resp: %v", err)
	}
	if len(listResp.Entries) != 1 || listResp.Entries[0].Name != "sample.bin" {
		t.Fatalf("unexpected entries: %+v", listResp.Entries)
	}

	// 2. Test chunked download_req
	dlID := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	if err := cliFC.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileDownloadReq, protocol.FileDownloadReqMsg{
		ID:   dlID,
		Path: "sample.bin",
	}); err != nil {
		t.Fatalf("download_req write: %v", err)
	}

	var downloaded bytes.Buffer
	for {
		fr, err := cliFC.ReadFrame()
		if err != nil {
			t.Fatalf("download read: %v", err)
		}
		sub := fr.Payload[0]
		if sub == protocol.FileChunk {
			id, off, chunk, err := protocol.DecodeFileChunk(fr.Payload[1:])
			if err != nil {
				t.Fatalf("DecodeFileChunk: %v", err)
			}
			if id != dlID || int(off) != downloaded.Len() {
				t.Fatalf("chunk mismatch: id=%s off=%d expected=%d", id, off, downloaded.Len())
			}
			downloaded.Write(chunk)
		} else if sub == protocol.FileDone {
			var done protocol.FileDoneMsg
			_ = json.Unmarshal(fr.Payload[1:], &done)
			if done.SHA256 != srcSHA || done.Size != int64(len(srcData)) {
				t.Fatalf("download done mismatch: %+v", done)
			}
			break
		} else {
			t.Fatalf("unexpected download subcmd 0x%02x: %s", sub, string(fr.Payload[1:]))
		}
	}
	if !bytes.Equal(downloaded.Bytes(), srcData) {
		t.Fatalf("downloaded bytes do not match source")
	}

	// 3. Test chunked upload_start -> file_chunk -> file_done
	upID := "11111111-2222-4333-8444-555555555555"
	if err := cliFC.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileUploadStart, protocol.FileUploadStartMsg{
		ID:     upID,
		Path:   "uploaded/copy.bin",
		Size:   int64(len(srcData)),
		SHA256: srcSHA,
	}); err != nil {
		t.Fatalf("upload_start write: %v", err)
	}

	for off := 0; off < len(srcData); off += 32768 {
		end := off + 32768
		if end > len(srcData) {
			end = len(srcData)
		}
		cp, _ := protocol.EncodeFileChunk(upID, uint64(off), srcData[off:end])
		if err := cliFC.WriteSubcmdBytes(protocol.ChannelFILE, protocol.FileChunk, cp); err != nil {
			t.Fatalf("upload chunk write: %v", err)
		}
	}
	if err := cliFC.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileDone, protocol.FileDoneMsg{
		ID:     upID,
		SHA256: srcSHA,
		Size:   int64(len(srcData)),
	}); err != nil {
		t.Fatalf("upload done write: %v", err)
	}

	upResp, err := cliFC.ReadFrame()
	if err != nil {
		t.Fatalf("upload done read: %v", err)
	}
	if upResp.Payload[0] != protocol.FileDone {
		t.Fatalf("expected FileDone, got 0x%02x: %s", upResp.Payload[0], string(upResp.Payload[1:]))
	}
	gotDisk, err := os.ReadFile(filepath.Join(root, "uploaded", "copy.bin"))
	if err != nil || !bytes.Equal(gotDisk, srcData) {
		t.Fatalf("uploaded file on disk mismatch: err=%v", err)
	}
}
