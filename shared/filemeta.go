//go:build !windows

package shared

import "os"

// fileExtraTimes returns creation and access times. On non-Windows platforms
// these are not reliably available so we fall back to mtime for both.
func fileExtraTimes(path string) (ctime, atime int64) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, 0
	}
	t := fi.ModTime().UnixNano()
	return t, t
}
