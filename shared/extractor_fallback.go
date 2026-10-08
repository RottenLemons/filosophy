//go:build no_kreuzberg

package shared

import (
	"os"
	"path/filepath"
	"strings"
)

func extractFileContent(path string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".txt", ".md", ".csv", ".json", ".xml", ".html", ".htm", ".go", ".py", ".js", ".ts":
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(data), nil
	default:
		return "", nil
	}
}
