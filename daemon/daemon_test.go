package daemon

import "testing"

func TestShouldFilterPathSkipsGeneratedTreesButKeepsProjectSource(t *testing.T) {
	for _, path := range []string{
		`C:\Users\Mahir\Documents\Project\node_modules\package\index.js`,
		`C:\Users\Mahir\Documents\Project\build\app.exe`,
		`C:\Program Files\Example\src\main.go`,
	} {
		if !shouldFilterPath(path) {
			t.Errorf("shouldFilterPath(%q) = false, want true", path)
		}
	}
	for _, path := range []string{
		`C:\Users\Mahir\Documents\Project\src\main.go`,
		`C:\Users\Mahir\Documents\Project\pkg\search.go`,
	} {
		if shouldFilterPath(path) {
			t.Errorf("shouldFilterPath(%q) = true, want false", path)
		}
	}
}
