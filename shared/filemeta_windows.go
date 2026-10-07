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

// IsOfflineFile checks if the file is a cloud-only placeholder or offline file.
func IsOfflineFile(fi os.FileInfo) bool {
	sys, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return false
	}
	// 0x1000 = FILE_ATTRIBUTE_OFFLINE
	// 0x400000 = FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS (OneDrive / Cloud files)
	const fileAttributeOffline = 0x1000
	const fileAttributeRecallOnDataAccess = 0x400000

	return (sys.FileAttributes&fileAttributeOffline != 0) || (sys.FileAttributes&fileAttributeRecallOnDataAccess != 0)
}
