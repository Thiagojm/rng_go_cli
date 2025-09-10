//go:build windows

package bbusb

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// GUID for all USB devices: {A5DCBF10-6530-11D2-901F-00C04FB951ED}
var guidDevInterfaceUsbDevice = windows.GUID{Data1: 0xA5DCBF10, Data2: 0x6530, Data3: 0x11D2, Data4: [8]byte{0x90, 0x1F, 0x00, 0xC0, 0x4F, 0xB9, 0x51, 0xED}}

const (
	// SetupDi flags
	_DIGCF_DEFAULT         = 0x00000001
	_DIGCF_PRESENT         = 0x00000002
	_DIGCF_ALLCLASSES      = 0x00000004
	_DIGCF_PROFILE         = 0x00000008
	_DIGCF_DEVICEINTERFACE = 0x00000010

	// Registry property codes
	_SPDRP_DEVICEDESC     = 0x00000000
	_SPDRP_HARDWAREID     = 0x00000001
	_SPDRP_FRIENDLYNAME   = 0x0000000C
	_SPDRP_LOCATION_PATHS = 0x00000023
)

// Windows API types mirroring setupapi.h structures.
type spDeviceInterfaceData struct {
	cbSize             uint32
	InterfaceClassGuid windows.GUID
	Flags              uint32
	Reserved           uintptr
}

type spDeviceInterfaceDetailDataW struct {
	cbSize     uint32
	DevicePath [1]uint16 // variable-length
}

type spDevinfoData struct {
	cbSize    uint32
	ClassGuid windows.GUID
	DevInst   uint32
	Reserved  uintptr
}

var (
	modSetupapi                           = windows.NewLazySystemDLL("setupapi.dll")
	procSetupDiGetClassDevsW              = modSetupapi.NewProc("SetupDiGetClassDevsW")
	procSetupDiEnumDeviceInterfaces       = modSetupapi.NewProc("SetupDiEnumDeviceInterfaces")
	procSetupDiGetDeviceInterfaceDetailW  = modSetupapi.NewProc("SetupDiGetDeviceInterfaceDetailW")
	procSetupDiEnumDeviceInfo             = modSetupapi.NewProc("SetupDiEnumDeviceInfo")
	procSetupDiGetDeviceRegistryPropertyW = modSetupapi.NewProc("SetupDiGetDeviceRegistryPropertyW")
	procSetupDiDestroyDeviceInfoList      = modSetupapi.NewProc("SetupDiDestroyDeviceInfoList")
)

// DeviceInfo contains key metadata for a detected BitBabbler device.
//
// Fields may be empty if not available on the current system.
type DeviceInfo struct {
	// DevicePath is the system path to the device interface, e.g. \\?\usb#vid_0403&pid_7840#... (if available).
	DevicePath string
	// HardwareIDs is the list of hardware IDs from the registry, e.g. ["USB\\VID_0403&PID_7840", ...].
	HardwareIDs []string
	// FriendlyName is a human-friendly device label if present.
	FriendlyName string
}

// IsBitBabblerConnected returns whether a BitBabbler device (VID 0x0403, PID 0x7840)
// is present and a slice of device infos.
//
// Windows implementation notes:
// - Enumerates present USB device interfaces via SetupAPI
// - Matches devices by VID/PID using hardware IDs and device paths
// - Populates friendly name and path when available
func IsBitBabblerConnected() (bool, []DeviceInfo, error) {
	devices, err := listUsbDevicesMatchingVIDPID(0x0403, 0x7840)
	if err != nil {
		return false, nil, err
	}
	return len(devices) > 0, devices, nil
}

// listUsbDevicesMatchingVIDPID enumerates USB device interfaces and filters by VID/PID.
func listUsbDevicesMatchingVIDPID(vendorID uint16, productID uint16) ([]DeviceInfo, error) {
	// Create a device information set for present USB devices exposing an interface.
	h, err := setupDiGetClassDevs(&guidDevInterfaceUsbDevice, 0, 0, _DIGCF_PRESENT|_DIGCF_DEVICEINTERFACE)
	if err != nil {
		return nil, err
	}
	defer setupDiDestroyDeviceInfoList(h)

	var (
		index   uint32
		results []DeviceInfo
	)

	for {
		var ifData spDeviceInterfaceData
		ifData.cbSize = uint32(unsafe.Sizeof(ifData))

		ok, errEnum := setupDiEnumDeviceInterfaces(h, 0, &guidDevInterfaceUsbDevice, index, &ifData)
		if !ok {
			if errors.Is(errEnum, windows.ERROR_NO_MORE_ITEMS) {
				break
			}
			return nil, fmt.Errorf("SetupDiEnumDeviceInterfaces failed at index %d: %w", index, errEnum)
		}

		// First call to get required buffer size
		reqSize := uint32(0)
		var devInfo spDevinfoData
		devInfo.cbSize = uint32(unsafe.Sizeof(devInfo))
		// Passing nil buffer to get size
		_ = setupDiGetDeviceInterfaceDetailW(h, &ifData, nil, 0, &reqSize, &devInfo)
		if reqSize == 0 {
			index++
			continue
		}

		buf := make([]byte, reqSize)
		// We need to set cbSize on the detail struct depending on arch
		var detail *spDeviceInterfaceDetailDataW = (*spDeviceInterfaceDetailDataW)(unsafe.Pointer(&buf[0]))
		if runtime.GOARCH == "386" || runtime.GOARCH == "arm" {
			detail.cbSize = 6 // sizeof(DWORD) + 2 bytes for first WCHAR
		} else {
			detail.cbSize = 8 // on 64-bit
		}

		if err := setupDiGetDeviceInterfaceDetailW(h, &ifData, detail, reqSize, nil, &devInfo); err != nil {
			return nil, fmt.Errorf("SetupDiGetDeviceInterfaceDetailW failed: %w", err)
		}

		// Extract device path
		devicePath := windows.UTF16PtrToString(&detail.DevicePath[0])

		// Get hardware IDs to check VID/PID
		hwIDs, _ := setupDiGetDeviceRegistryMultiSz(h, &devInfo, _SPDRP_HARDWAREID)
		friendly, _ := setupDiGetDeviceRegistryString(h, &devInfo, _SPDRP_FRIENDLYNAME)
		if friendly == "" {
			friendly, _ = setupDiGetDeviceRegistryString(h, &devInfo, _SPDRP_DEVICEDESC)
		}

		if hasVIDPID(hwIDs, vendorID, productID) || devicePathHasVIDPID(devicePath, vendorID, productID) {
			results = append(results, DeviceInfo{
				DevicePath:   devicePath,
				HardwareIDs:  hwIDs,
				FriendlyName: friendly,
			})
		}

		index++
	}

	return results, nil
}

func hasVIDPID(hwIDs []string, vid uint16, pid uint16) bool {
	vidStr := fmt.Sprintf("VID_%04X", vid)
	pidStr := fmt.Sprintf("PID_%04X", pid)
	for _, s := range hwIDs {
		ss := strings.ToUpper(s)
		if strings.Contains(ss, vidStr) && strings.Contains(ss, pidStr) {
			return true
		}
	}
	return false
}

func devicePathHasVIDPID(path string, vid uint16, pid uint16) bool {
	upper := strings.ToUpper(path)
	return strings.Contains(upper, fmt.Sprintf("VID_%04X", vid)) && strings.Contains(upper, fmt.Sprintf("PID_%04X", pid))
}

// setupDiGetClassDevs wraps SetupDiGetClassDevsW.
func setupDiGetClassDevs(classGUID *windows.GUID, enumerator uint16, hwndParent uintptr, flags uint32) (windows.Handle, error) {
	var guidPtr *windows.GUID
	if classGUID != nil {
		guidPtr = classGUID
	}

	r0, _, e1 := procSetupDiGetClassDevsW.Call(
		uintptr(unsafe.Pointer(guidPtr)),
		uintptr(unsafe.Pointer(&enumerator)), // passing 0 is fine
		uintptr(hwndParent),
		uintptr(flags),
	)
	if r0 == 0 || r0 == ^uintptr(0) { // INVALID_HANDLE_VALUE
		if e1 != nil {
			return 0, e1
		}
		return 0, windows.ERROR_PROC_NOT_FOUND
	}
	return windows.Handle(r0), nil
}

// setupDiEnumDeviceInterfaces wraps SetupDiEnumDeviceInterfaces.
func setupDiEnumDeviceInterfaces(hdevinfo windows.Handle, devInfo uintptr, classGUID *windows.GUID, index uint32, out *spDeviceInterfaceData) (bool, error) {
	r1, _, e1 := procSetupDiEnumDeviceInterfaces.Call(
		uintptr(hdevinfo),
		devInfo,
		uintptr(unsafe.Pointer(classGUID)),
		uintptr(index),
		uintptr(unsafe.Pointer(out)),
	)
	if r1 == 0 {
		if e1 == windows.ERROR_NO_MORE_ITEMS {
			return false, e1
		}
		return false, e1
	}
	return true, nil
}

// setupDiGetDeviceInterfaceDetailW wraps SetupDiGetDeviceInterfaceDetailW.
func setupDiGetDeviceInterfaceDetailW(hdevinfo windows.Handle, ifData *spDeviceInterfaceData, detail *spDeviceInterfaceDetailDataW, detailSize uint32, requiredSize *uint32, devInfo *spDevinfoData) error {
	r1, _, e1 := procSetupDiGetDeviceInterfaceDetailW.Call(
		uintptr(hdevinfo),
		uintptr(unsafe.Pointer(ifData)),
		uintptr(unsafe.Pointer(detail)),
		uintptr(detailSize),
		uintptr(unsafe.Pointer(requiredSize)),
		uintptr(unsafe.Pointer(devInfo)),
	)
	if r1 == 0 {
		// When probing for size (detail == nil) ERROR_INSUFFICIENT_BUFFER is expected.
		if detail == nil && errors.Is(e1, windows.ERROR_INSUFFICIENT_BUFFER) {
			return nil
		}
		if e1 != nil {
			return e1
		}
		return errors.New("SetupDiGetDeviceInterfaceDetailW failed")
	}
	return nil
}

// setupDiGetDeviceRegistryMultiSz retrieves a REG_MULTI_SZ property for a device.
func setupDiGetDeviceRegistryMultiSz(hdevinfo windows.Handle, devInfo *spDevinfoData, prop uint32) ([]string, error) {
	// Query size
	var dataType uint32
	var required uint32
	r1, _, e1 := procSetupDiGetDeviceRegistryPropertyW.Call(
		uintptr(hdevinfo),
		uintptr(unsafe.Pointer(devInfo)),
		uintptr(prop),
		uintptr(unsafe.Pointer(&dataType)),
		0,
		0,
		uintptr(unsafe.Pointer(&required)),
	)
	if r1 == 0 && !errors.Is(e1, windows.ERROR_INSUFFICIENT_BUFFER) {
		return nil, e1
	}
	if required == 0 {
		return nil, nil
	}

	buf := make([]uint16, required/2)
	r2, _, e2 := procSetupDiGetDeviceRegistryPropertyW.Call(
		uintptr(hdevinfo),
		uintptr(unsafe.Pointer(devInfo)),
		uintptr(prop),
		uintptr(unsafe.Pointer(&dataType)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(required),
		uintptr(unsafe.Pointer(&required)),
	)
	if r2 == 0 {
		if e2 != nil {
			return nil, e2
		}
		return nil, errors.New("SetupDiGetDeviceRegistryPropertyW read failed")
	}

	// Parse MULTI_SZ (sequence of NUL-terminated strings, ending with extra NUL)
	var out []string
	start := 0
	for i, v := range buf {
		if v == 0 {
			if i == start {
				break
			}
			out = append(out, windows.UTF16ToString(buf[start:i]))
			start = i + 1
		}
	}
	return out, nil
}

// setupDiGetDeviceRegistryString retrieves a REG_SZ property for a device and returns it as string.
func setupDiGetDeviceRegistryString(hdevinfo windows.Handle, devInfo *spDevinfoData, prop uint32) (string, error) {
	var dataType uint32
	var required uint32
	r1, _, e1 := procSetupDiGetDeviceRegistryPropertyW.Call(
		uintptr(hdevinfo),
		uintptr(unsafe.Pointer(devInfo)),
		uintptr(prop),
		uintptr(unsafe.Pointer(&dataType)),
		0,
		0,
		uintptr(unsafe.Pointer(&required)),
	)
	if r1 == 0 && !errors.Is(e1, windows.ERROR_INSUFFICIENT_BUFFER) {
		return "", e1
	}
	if required == 0 {
		return "", nil
	}
	buf := make([]uint16, required/2)
	r2, _, e2 := procSetupDiGetDeviceRegistryPropertyW.Call(
		uintptr(hdevinfo),
		uintptr(unsafe.Pointer(devInfo)),
		uintptr(prop),
		uintptr(unsafe.Pointer(&dataType)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(required),
		uintptr(unsafe.Pointer(&required)),
	)
	if r2 == 0 {
		if e2 != nil {
			return "", e2
		}
		return "", errors.New("SetupDiGetDeviceRegistryPropertyW read failed")
	}
	return windows.UTF16ToString(buf), nil
}

// setupDiDestroyDeviceInfoList wraps SetupDiDestroyDeviceInfoList.
func setupDiDestroyDeviceInfoList(hdevinfo windows.Handle) error {
	r1, _, e1 := procSetupDiDestroyDeviceInfoList.Call(uintptr(hdevinfo))
	if r1 == 0 {
		if e1 != nil {
			return e1
		}
		return errors.New("SetupDiDestroyDeviceInfoList failed")
	}
	return nil
}
