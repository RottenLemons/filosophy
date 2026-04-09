//go:build !windows

package shared

// NetworkDrives returns mapped network drive roots.
// On non-Windows platforms this is a no-op.
func NetworkDrives() []string {
	return nil
}
