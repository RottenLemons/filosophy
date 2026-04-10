//go:build windows

package shared

import (
	"syscall"
	"unsafe"
)

// memoryStatusEx mirrors the MEMORYSTATUSEX Win32 struct.
// See: https://learn.microsoft.com/en-us/windows/win32/api/sysinfoapi/ns-sysinfoapi-memorystatusex
type memoryStatusEx struct {
	dwLength                uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

var (
	kernel32              = syscall.NewLazyDLL("kernel32.dll")
	procGlobalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

// GetMemoryInfo returns the total and currently available physical RAM in bytes.
// Uses a direct kernel32.dll call for maximum version compatibility.
func GetMemoryInfo() (totalBytes, availableBytes uint64) {
	var ms memoryStatusEx
	ms.dwLength = uint32(unsafe.Sizeof(ms))
	ret, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	if ret == 0 {
		return 0, 0
	}
	return ms.ullTotalPhys, ms.ullAvailPhys
}
