//go:build windows

package term

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"github.com/xiaobaodesk/win-host/internal/protocol"
)

var (
	kernel32                           = syscall.NewLazyDLL("kernel32.dll")
	procCreatePseudoConsole            = kernel32.NewProc("CreatePseudoConsole")
	procResizePseudoConsole            = kernel32.NewProc("ResizePseudoConsole")
	procClosePseudoConsole             = kernel32.NewProc("ClosePseudoConsole")
	procInitializeProcThreadAttrList   = kernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute      = kernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttributeList  = kernel32.NewProc("DeleteProcThreadAttributeList")
)

const (
	procThreadAttributePseudoConsole = 0x00020016
	extendedStartupInfoPresent       = 0x00080000
)

type startupInfoExW struct {
	StartupInfo   syscall.StartupInfo
	AttributeList unsafe.Pointer
}

func packCoord(cols, rows uint16) uintptr {
	return uintptr(uint32(cols) | (uint32(rows) << 16))
}

// ConPTY manages an interactive Windows PowerShell session via CreatePseudoConsole.
type ConPTY struct {
	hPC        syscall.Handle
	inWrite    *os.File
	outRead    *os.File
	procHandle syscall.Handle
	threadH    syscall.Handle

	mu     sync.Mutex
	closed bool
}

// StartPowerShell spawns an interactive PowerShell session inside a Windows ConPTY
// and streams stdout bytes as 0x05 TERM / 0x03 stdout_data frames to sink.
func StartPowerShell(cols, rows uint16, sink *protocol.FrameConn) (*ConPTY, error) {
	if cols == 0 {
		cols = 120
	}
	if rows == 0 {
		rows = 36
	}

	var inRead, inWrite syscall.Handle
	if err := syscall.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("CreatePipe in: %w", err)
	}
	var outRead, outWrite syscall.Handle
	if err := syscall.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		_ = syscall.CloseHandle(inRead)
		_ = syscall.CloseHandle(inWrite)
		return nil, fmt.Errorf("CreatePipe out: %w", err)
	}

	var hPC syscall.Handle
	hr, _, _ := procCreatePseudoConsole.Call(
		packCoord(cols, rows),
		uintptr(inRead),
		uintptr(outWrite),
		0,
		uintptr(unsafe.Pointer(&hPC)),
	)
	// Close child-side pipe ends in parent once CreatePseudoConsole duplicates them
	_ = syscall.CloseHandle(inRead)
	_ = syscall.CloseHandle(outWrite)

	if hr != 0 {
		_ = syscall.CloseHandle(inWrite)
		_ = syscall.CloseHandle(outRead)
		return nil, fmt.Errorf("CreatePseudoConsole failed: 0x%x", hr)
	}

	var attrListSize uintptr
	_, _, _ = procInitializeProcThreadAttrList.Call(0, 1, 0, uintptr(unsafe.Pointer(&attrListSize)))
	if attrListSize == 0 {
		_, _, _ = procClosePseudoConsole.Call(uintptr(hPC))
		_ = syscall.CloseHandle(inWrite)
		_ = syscall.CloseHandle(outRead)
		return nil, errors.New("InitializeProcThreadAttributeList returned 0 size")
	}

	attrBuf := make([]byte, attrListSize)
	attrList := unsafe.Pointer(&attrBuf[0])
	r1, _, err := procInitializeProcThreadAttrList.Call(
		uintptr(attrList),
		1,
		0,
		uintptr(unsafe.Pointer(&attrListSize)),
	)
	if r1 == 0 {
		_, _, _ = procClosePseudoConsole.Call(uintptr(hPC))
		_ = syscall.CloseHandle(inWrite)
		_ = syscall.CloseHandle(outRead)
		return nil, fmt.Errorf("InitializeProcThreadAttributeList: %w", err)
	}
	defer procDeleteProcThreadAttributeList.Call(uintptr(attrList))

	r1, _, err = procUpdateProcThreadAttribute.Call(
		uintptr(attrList),
		0,
		procThreadAttributePseudoConsole,
		uintptr(hPC),
		unsafe.Sizeof(hPC),
		0,
		0,
	)
	if r1 == 0 {
		_, _, _ = procClosePseudoConsole.Call(uintptr(hPC))
		_ = syscall.CloseHandle(inWrite)
		_ = syscall.CloseHandle(outRead)
		return nil, fmt.Errorf("UpdateProcThreadAttribute: %w", err)
	}

	var siEx startupInfoExW
	siEx.StartupInfo.Cb = uint32(unsafe.Sizeof(siEx))
	siEx.StartupInfo.Flags = syscall.STARTF_USESTDHANDLES
	siEx.AttributeList = attrList

	cmdLine, err := syscall.UTF16PtrFromString("powershell.exe -NoLogo")
	if err != nil {
		_, _, _ = procClosePseudoConsole.Call(uintptr(hPC))
		_ = syscall.CloseHandle(inWrite)
		_ = syscall.CloseHandle(outRead)
		return nil, err
	}

	var pi syscall.ProcessInformation
	err = syscall.CreateProcess(
		nil,
		cmdLine,
		nil,
		nil,
		false,
		extendedStartupInfoPresent,
		nil,
		nil,
		&siEx.StartupInfo,
		&pi,
	)
	if err != nil {
		_, _, _ = procClosePseudoConsole.Call(uintptr(hPC))
		_ = syscall.CloseHandle(inWrite)
		_ = syscall.CloseHandle(outRead)
		return nil, fmt.Errorf("CreateProcess powershell: %w", err)
	}

	c := &ConPTY{
		hPC:        hPC,
		inWrite:    os.NewFile(uintptr(inWrite), "conpty-in"),
		outRead:    os.NewFile(uintptr(outRead), "conpty-out"),
		procHandle: pi.Process,
		threadH:    pi.Thread,
	}

	go c.readLoop(sink)
	return c, nil
}

func (c *ConPTY) readLoop(sink *protocol.FrameConn) {
	buf := make([]byte, 8192)
	for {
		n, err := c.outRead.Read(buf)
		if n > 0 && sink != nil {
			_ = sink.WriteSubcmdBytes(protocol.ChannelTERM, protocol.TermStdout, buf[:n])
		}
		if err != nil {
			if err != io.EOF {
				// ConPTY closed
			}
			return
		}
	}
}

func (c *ConPTY) WriteStdin(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.inWrite == nil {
		return errors.New("conpty closed")
	}
	_, err := c.inWrite.Write(data)
	return err
}

func (c *ConPTY) Resize(cols, rows uint16) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.hPC == 0 {
		return errors.New("conpty closed")
	}
	hr, _, _ := procResizePseudoConsole.Call(uintptr(c.hPC), packCoord(cols, rows))
	if hr != 0 {
		return fmt.Errorf("ResizePseudoConsole failed: 0x%x", hr)
	}
	return nil
}

func (c *ConPTY) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	hPC := c.hPC
	c.hPC = 0
	procH := c.procHandle
	c.procHandle = 0
	threadH := c.threadH
	c.threadH = 0
	c.mu.Unlock()

	if c.inWrite != nil {
		_ = c.inWrite.Close()
	}
	if hPC != 0 {
		_, _, _ = procClosePseudoConsole.Call(uintptr(hPC))
	}
	if c.outRead != nil {
		_ = c.outRead.Close()
	}
	if procH != 0 {
		_ = syscall.TerminateProcess(procH, 0)
		_ = syscall.CloseHandle(procH)
	}
	if threadH != 0 {
		_ = syscall.CloseHandle(threadH)
	}
	return nil
}
