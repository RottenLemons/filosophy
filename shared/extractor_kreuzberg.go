//go:build !no_kreuzberg

package shared

/*
#cgo LDFLAGS: -L${SRCDIR}/.. -lkreuzberg_ffi
*/
import "C"
import kreuzberg "github.com/kreuzberg-dev/kreuzberg/packages/go/v4"

func extractFileContent(path string) (string, error) {
	result, err := kreuzberg.ExtractFileSync(path, nil)
	if err != nil || result == nil {
		return "", err
	}
	return result.Content, nil
}
