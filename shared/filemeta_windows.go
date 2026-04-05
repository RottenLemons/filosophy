//go:build windows

package shared

import (
	"os"
	"syscall"
)

// fileExtraTimes returns creation time and last-access time for path as
// nanoseconds since the Unix epoch. Falls back to zero if the stat fails.
func fileExtraTimes(path string) (ctime, atime int64) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	sys, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return 0, 0
	}
	return sys.CreationTime.Nanoseconds(), sys.LastAccessTime.Nanoseconds()
}
