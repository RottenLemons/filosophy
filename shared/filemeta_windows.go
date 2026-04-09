//go:build windows

package shared

import (
	"os"
	"syscall"
)

// FileExtraTimesFromInfo extracts creation and last-access times from an
// already-loaded os.FileInfo, avoiding a redundant os.Stat syscall.
func FileExtraTimesFromInfo(fi os.FileInfo) (ctime, atime int64) {
	sys, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return 0, 0
	}
	return sys.CreationTime.Nanoseconds(), sys.LastAccessTime.Nanoseconds()
}

// fileExtraTimes is retained for callers that only hold a path.
// Prefer FileExtraTimesFromInfo when an os.FileInfo is already available.
func fileExtraTimes(path string) (ctime, atime int64) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	return FileExtraTimesFromInfo(fi)
}
