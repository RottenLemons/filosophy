//go:build windows

package shared

import (
	"fmt"
	"golang.org/x/sys/windows"
)

// NetworkDrives returns the root paths of all currently mapped network drives
// (e.g. "Z:\"). It uses GetLogicalDrives + GetDriveType(DRIVE_REMOTE).
func NetworkDrives() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil
	}
	var drives []string
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		root := fmt.Sprintf("%c:\\", 'A'+i)
		// Null-terminated UTF16 pointer required by GetDriveType.
		rootPtr, _ := windows.UTF16PtrFromString(root)
		if windows.GetDriveType(rootPtr) == windows.DRIVE_REMOTE {
			drives = append(drives, root)
		}
	}
	return drives
}
