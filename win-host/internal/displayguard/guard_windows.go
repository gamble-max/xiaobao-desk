//go:build windows

package displayguard

import (
	"log"
	"strings"
	"syscall"
	"unsafe"
)

const (
	displayDeviceAttachedToDesktop = 0x00000001
	displayDevicePrimaryDevice     = 0x00000004

	dmPosition   = 0x00000020
	dmPelsWidth  = 0x00080000
	dmPelsHeight = 0x00100000

	cdsUpdateRegistry = 0x00000001
	cdsNoReset        = 0x10000000

	sdcTopologyInternal = 0x00000001
	sdcApply            = 0x00000080
)

type displayDeviceW struct {
	cb           uint32
	DeviceName   [32]uint16
	DeviceString [128]uint16
	StateFlags   uint32
	DeviceID     [128]uint16
	DeviceKey    [128]uint16
}

type pointL struct {
	X int32
	Y int32
}

type devModeW struct {
	dmDeviceName       [32]uint16
	dmSpecVersion      uint16
	dmDriverVersion    uint16
	dmSize             uint16
	dmDriverExtra      uint16
	dmFields           uint32
	dmPosition         pointL
	dmDisplayOrient    uint32
	dmDisplayFixedOut  uint32
	dmColor            int16
	dmDuplex           int16
	dmYResolution      int16
	dmTTOption         int16
	dmCollate          int16
	dmFormName         [32]uint16
	dmLogPixels        uint16
	dmBitsPerPel       uint32
	dmPelsWidth        uint32
	dmPelsHeight       uint32
	dmDisplayFlags     uint32
	dmDisplayFrequency uint32
	dmICMMethod        uint32
	dmICMIntent        uint32
	dmMediaType        uint32
	dmDitherType       uint32
	dmReserved1        uint32
	dmReserved2        uint32
	dmPanningWidth     uint32
	dmPanningHeight    uint32
}

var (
	user32                     = syscall.NewLazyDLL("user32.dll")
	procEnumDisplayDevicesW    = user32.NewProc("EnumDisplayDevicesW")
	procChangeDisplaySettingsW = user32.NewProc("ChangeDisplaySettingsExW")
	procSetDisplayConfig       = user32.NewProc("SetDisplayConfig")
	procClipCursor             = user32.NewProc("ClipCursor")
)

// EnsureNoVirtualDisplayAttached detaches any attached non-primary virtual display
// (Virtual Display Driver / MttVDD), restores internal display topology, and releases ClipCursor.
func EnsureNoVirtualDisplayAttached() {
	detachedAny := false
	for i := uint32(0); ; i++ {
		var dd displayDeviceW
		dd.cb = uint32(unsafe.Sizeof(dd))
		r, _, _ := procEnumDisplayDevicesW.Call(0, uintptr(i), uintptr(unsafe.Pointer(&dd)), 0)
		if r == 0 {
			break
		}
		attached := (dd.StateFlags & displayDeviceAttachedToDesktop) != 0
		primary := (dd.StateFlags & displayDevicePrimaryDevice) != 0
		if !attached || primary {
			continue
		}

		devStr := strings.ToLower(syscall.UTF16ToString(dd.DeviceString[:]))
		devID := strings.ToLower(syscall.UTF16ToString(dd.DeviceID[:]))
		if !strings.Contains(devStr, "virtual display driver") &&
			!strings.Contains(devStr, "mttvdd") &&
			!strings.Contains(devID, "mttvdd") {
			continue
		}

		devName := syscall.UTF16ToString(dd.DeviceName[:])
		var dm devModeW
		dm.dmSize = uint16(unsafe.Sizeof(dm))
		dm.dmFields = dmPosition | dmPelsWidth | dmPelsHeight
		dm.dmPelsWidth = 0
		dm.dmPelsHeight = 0

		ret, _, _ := procChangeDisplaySettingsW.Call(
			uintptr(unsafe.Pointer(&dd.DeviceName[0])),
			uintptr(unsafe.Pointer(&dm)),
			0,
			uintptr(cdsUpdateRegistry|cdsNoReset),
			0,
		)
		log.Printf("[displayguard] detached virtual display %s (%s), ChangeDisplaySettingsExW=%d", devName, syscall.UTF16ToString(dd.DeviceString[:]), ret)
		detachedAny = true
	}

	if detachedAny {
		_, _, _ = procChangeDisplaySettingsW.Call(0, 0, 0, 0, 0)
		ret, _, _ := procSetDisplayConfig.Call(0, 0, 0, 0, uintptr(sdcTopologyInternal|sdcApply))
		log.Printf("[displayguard] applied SDC_TOPOLOGY_INTERNAL | SDC_APPLY, SetDisplayConfig=%d", ret)
	}

	_, _, _ = procClipCursor.Call(0)
}
