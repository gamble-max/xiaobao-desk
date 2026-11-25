package filetransfer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/xiaobaodesk/win-host/internal/protocol"
)

const (
	ChunkSize            = 64 * 1024
	MaxUploadSize  int64 = 4 * 1024 * 1024 * 1024 // 4 GiB
	MaxConcurrentUploads = 4
)

type uploadState struct {
	id           string
	targetPath   string
	tempPath     string
	file         *os.File
	expectedSize int64
	expectedSHA  string
	written      int64
	hasher       hash.Hash
}

// Handler processes 0x06 FILE subcommands for directory browsing and chunked SHA-256 transfers.
type Handler struct {
	rootDir      string
	resolvedRoot string
	mu           sync.Mutex
	uploads      map[string]*uploadState
}

func NewHandler(rootDir string) *Handler {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			home = os.TempDir()
		}
		rootDir = filepath.Join(home, "Downloads", "XiaobaoDeskTransfer")
	}
	_ = os.MkdirAll(rootDir, 0755)
	cleanRoot := filepath.Clean(rootDir)
	if abs, err := filepath.Abs(cleanRoot); err == nil {
		cleanRoot = abs
	}
	resolvedRoot := cleanRoot
	if eval, err := filepath.EvalSymlinks(cleanRoot); err == nil {
		resolvedRoot = eval
	}
	return &Handler{
		rootDir:      cleanRoot,
		resolvedRoot: resolvedRoot,
		uploads:      make(map[string]*uploadState),
	}
}

func (h *Handler) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, st := range h.uploads {
		if st.file != nil {
			_ = st.file.Close()
		}
		_ = os.Remove(st.tempPath)
		delete(h.uploads, id)
	}
}

func isWithinRoot(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func (h *Handler) resolvePath(p string) (string, error) {
	if p == "" || p == "/" || p == `\` {
		return h.rootDir, nil
	}
	if filepath.VolumeName(p) != "" || strings.HasPrefix(p, `\\`) {
		return "", fmt.Errorf("path escapes root directory")
	}
	trimmed := strings.TrimLeft(p, `/\`)
	if trimmed == "" {
		return h.rootDir, nil
	}
	joined := filepath.Clean(filepath.Join(h.rootDir, trimmed))
	if !isWithinRoot(h.rootDir, joined) {
		return "", fmt.Errorf("path escapes root directory")
	}

	// Resolve symlinks/junctions on the target or its deepest existing ancestor.
	cur := joined
	for {
		if _, err := os.Lstat(cur); err == nil {
			resolved, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", fmt.Errorf("path symlink resolution failed: %w", err)
			}
			if !isWithinRoot(h.resolvedRoot, resolved) {
				return "", fmt.Errorf("path escapes root directory via symlink")
			}
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf("path escapes root directory")
		}
		cur = parent
	}
	return joined, nil
}

// HandleFrame dispatches a single 0x06 FILE frame payload.
func (h *Handler) HandleFrame(fc *protocol.FrameConn, payload []byte) {
	if len(payload) < 1 {
		return
	}
	subcmd := payload[0]
	body := payload[1:]

	switch subcmd {
	case protocol.FileListDir:
		var req protocol.FileListDirMsg
		if err := json.Unmarshal(body, &req); err != nil {
			_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{Error: err.Error()})
			return
		}
		h.handleListDir(fc, req.Path)

	case protocol.FileDownloadReq:
		var req protocol.FileDownloadReqMsg
		if err := json.Unmarshal(body, &req); err != nil {
			_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{Error: err.Error()})
			return
		}
		go h.handleDownload(fc, req)

	case protocol.FileUploadStart:
		var req protocol.FileUploadStartMsg
		if err := json.Unmarshal(body, &req); err != nil {
			_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{Error: err.Error()})
			return
		}
		h.handleUploadStart(fc, req)

	case protocol.FileChunk:
		h.handleUploadChunk(fc, body)

	case protocol.FileDone:
		var req protocol.FileDoneMsg
		if err := json.Unmarshal(body, &req); err != nil {
			_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{Error: err.Error()})
			return
		}
		h.handleUploadDone(fc, req)
	}
}

func (h *Handler) handleListDir(fc *protocol.FrameConn, reqPath string) {
	target, err := h.resolvePath(reqPath)
	if err != nil {
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{Error: err.Error()})
		return
	}

	dirEntries, err := os.ReadDir(target)
	if err != nil {
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{Error: err.Error()})
		return
	}

	entries := make([]protocol.FileEntry, 0, len(dirEntries))
	for _, de := range dirEntries {
		info, err := de.Info()
		if err != nil {
			continue
		}
		entries = append(entries, protocol.FileEntry{
			Name:  de.Name(),
			IsDir: de.IsDir(),
			Size:  info.Size(),
			Mtime: info.ModTime().Unix(),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileListResp, protocol.FileListRespMsg{
		Path:    target,
		Entries: entries,
	})
}

func (h *Handler) handleDownload(fc *protocol.FrameConn, req protocol.FileDownloadReqMsg) {
	target, err := h.resolvePath(req.Path)
	if err != nil {
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: req.ID, Error: err.Error()})
		return
	}
	f, err := os.Open(target)
	if err != nil {
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: req.ID, Error: err.Error()})
		return
	}
	defer f.Close()

	hasher := sha256.New()
	buf := make([]byte, ChunkSize)
	var offset uint64

	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			hasher.Write(buf[:n])
			chunkPayload, err := protocol.EncodeFileChunk(req.ID, offset, buf[:n])
			if err != nil {
				_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: req.ID, Error: err.Error()})
				return
			}
			if err := fc.WriteSubcmdBytes(protocol.ChannelFILE, protocol.FileChunk, chunkPayload); err != nil {
				return
			}
			offset += uint64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: req.ID, Error: readErr.Error()})
			return
		}
	}

	sum := hex.EncodeToString(hasher.Sum(nil))
	_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileDone, protocol.FileDoneMsg{
		ID:     req.ID,
		SHA256: sum,
		Size:   int64(offset),
	})
}

func (h *Handler) handleUploadStart(fc *protocol.FrameConn, req protocol.FileUploadStartMsg) {
	if len(req.ID) != 36 {
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: req.ID, Error: "id must be 36-char UUID"})
		return
	}
	if req.Size < 0 || req.Size > MaxUploadSize {
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: req.ID, Error: "upload size exceeds 4 GiB limit"})
		return
	}
	h.mu.Lock()
	tooMany := len(h.uploads) >= MaxConcurrentUploads
	h.mu.Unlock()
	if tooMany {
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: req.ID, Error: "too many concurrent uploads"})
		return
	}
	target, err := h.resolvePath(req.Path)
	if err != nil {
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: req.ID, Error: err.Error()})
		return
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: req.ID, Error: err.Error()})
		return
	}

	tempPath := target + ".xbpart." + req.ID
	f, err := os.OpenFile(tempPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: req.ID, Error: err.Error()})
		return
	}

	h.mu.Lock()
	if len(h.uploads) >= MaxConcurrentUploads {
		h.mu.Unlock()
		_ = f.Close()
		_ = os.Remove(tempPath)
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: req.ID, Error: "too many concurrent uploads"})
		return
	}
	h.uploads[req.ID] = &uploadState{
		id:           req.ID,
		targetPath:   target,
		tempPath:     tempPath,
		file:         f,
		expectedSize: req.Size,
		expectedSHA:  strings.ToLower(strings.TrimSpace(req.SHA256)),
		hasher:       sha256.New(),
	}
	h.mu.Unlock()
}

func (h *Handler) handleUploadChunk(fc *protocol.FrameConn, body []byte) {
	id, offset, data, err := protocol.DecodeFileChunk(body)
	if err != nil {
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{Error: err.Error()})
		return
	}

	h.mu.Lock()
	st, ok := h.uploads[id]
	h.mu.Unlock()
	if !ok {
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: id, Error: "unknown upload id"})
		return
	}

	if int64(offset) != st.written {
		h.abortUpload(id)
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{
			ID:    id,
			Error: fmt.Sprintf("offset mismatch: got %d expected %d", offset, st.written),
		})
		return
	}
	if st.written+int64(len(data)) > MaxUploadSize || (st.expectedSize > 0 && st.written+int64(len(data)) > st.expectedSize) {
		h.abortUpload(id)
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{
			ID:    id,
			Error: "upload size exceeds limit",
		})
		return
	}

	if _, err := st.file.Write(data); err != nil {
		h.abortUpload(id)
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: id, Error: err.Error()})
		return
	}
	st.hasher.Write(data)
	st.written += int64(len(data))
}

func (h *Handler) handleUploadDone(fc *protocol.FrameConn, req protocol.FileDoneMsg) {
	h.mu.Lock()
	st, ok := h.uploads[req.ID]
	if ok {
		delete(h.uploads, req.ID)
	}
	h.mu.Unlock()

	if !ok {
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: req.ID, Error: "unknown upload id"})
		return
	}

	_ = st.file.Close()
	actualSHA := hex.EncodeToString(st.hasher.Sum(nil))
	expectedSHA := st.expectedSHA
	if req.SHA256 != "" {
		expectedSHA = strings.ToLower(strings.TrimSpace(req.SHA256))
	}

	if expectedSHA != "" && actualSHA != expectedSHA {
		_ = os.Remove(st.tempPath)
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{
			ID:    req.ID,
			Error: fmt.Sprintf("sha256 mismatch: got %s expected %s", actualSHA, expectedSHA),
		})
		return
	}
	if st.expectedSize > 0 && st.written != st.expectedSize {
		_ = os.Remove(st.tempPath)
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{
			ID:    req.ID,
			Error: fmt.Sprintf("size mismatch: got %d expected %d", st.written, st.expectedSize),
		})
		return
	}

	if err := os.Rename(st.tempPath, st.targetPath); err != nil {
		_ = os.Remove(st.tempPath)
		_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileErr, protocol.FileErrMsg{ID: req.ID, Error: err.Error()})
		return
	}

	_ = fc.WriteSubcmdJSON(protocol.ChannelFILE, protocol.FileDone, protocol.FileDoneMsg{
		ID:     req.ID,
		SHA256: actualSHA,
		Size:   st.written,
	})
}

func (h *Handler) abortUpload(id string) {
	h.mu.Lock()
	st, ok := h.uploads[id]
	if ok {
		delete(h.uploads, id)
	}
	h.mu.Unlock()
	if ok {
		if st.file != nil {
			_ = st.file.Close()
		}
		_ = os.Remove(st.tempPath)
	}
}
