//go:build !windows

package shared

import "os"

// FileExtraTimesFromInfo extracts ctime/atime from an already-loaded FileInfo.
// On non-Windows platforms these are not reliably available; mtime is returned for both.
func FileExtraTimesFromInfo(fi os.FileInfo) (ctime, atime int64) {
	t := fi.ModTime().UnixNano()
	return t, t
}

// fileExtraTimes is retained for callers that only hold a path.
func fileExtraTimes(path string) (ctime, atime int64) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	return FileExtraTimesFromInfo(fi)
}

// IsOfflineFile checks if the file is a cloud-only placeholder or offline file.
// On non-Windows platforms, this currently returns false.
func IsOfflineFile(fi os.FileInfo) bool {
	return false
}
