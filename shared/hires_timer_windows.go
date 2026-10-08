//go:build windows

package shared

import (
	"syscall"
	"unsafe"
)

var (
	kernel32DLL = syscall.NewLazyDLL("kernel32.dll")
	procQPC     = kernel32DLL.NewProc("QueryPerformanceCounter")
	procQPF     = kernel32DLL.NewProc("QueryPerformanceFrequency")
	qpcFreq     int64
)

func init() {
	var freq int64
	procQPF.Call(uintptr(unsafe.Pointer(&freq)))
	qpcFreq = freq
}

func hiresNow() int64 {
	if qpcFreq > 0 {
		var count int64
		procQPC.Call(uintptr(unsafe.Pointer(&count)))
		return count
	}
	return 0
}

func hiresElapsedMs(start, end int64) float64 {
	if qpcFreq > 0 {
		return float64(end-start) * 1000.0 / float64(qpcFreq)
	}
	return 0
}
