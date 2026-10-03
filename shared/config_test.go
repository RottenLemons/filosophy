package shared

import (
	"path/filepath"
	"testing"
)

func TestAppConfigPathScopes(t *testing.T) {
	cfg := &AppConfig{
		ExcludedPaths: []string{filepath.Join("C:\\Users", "Mahir", "Cache")},
		IncludedDirs:  []string{filepath.Join("C:\\Users", "Mahir", "Documents")},
		PathOnlyDirs:  []string{filepath.Join("C:\\Users", "Mahir", "Pictures")},
	}
	cfg.rebuildCacheLocked()

	if !cfg.IsExcluded(`c:\users\mahir\cache\tmp`) {
		t.Error("expected excluded descendants to match case-insensitively")
	}
	if cfg.IsExcluded(`C:\Users\Mahir\CacheBackup\file.txt`) {
		t.Error("excluded directory prefix must not match sibling names")
	}
	if !cfg.IsIncluded(`C:\Users\Mahir\Documents\report.pdf`) {
		t.Error("expected included descendants to match")
	}
	if !cfg.IsPathOnly(`C:\Users\Mahir\Pictures\trip\photo.jpg`) {
		t.Error("expected path-only descendants to match")
	}
}

func TestAppConfigSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	cfg := LoadConfig(dir)
	cfg.ExcludedPaths = append(cfg.ExcludedPaths, "Custom", "custom")
	cfg.SetIncluded(filepath.Join(dir, "Documents"), true)
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded := LoadConfig(dir)
	if !loaded.IsExcluded("custom\\child") {
		t.Error("saved exclusion was not loaded")
	}
	if !loaded.IsIncluded(filepath.Join(dir, "Documents", "report.txt")) {
		t.Error("saved include directory was not loaded")
	}
	if !containsString(loaded.ExcludedPaths, "Custom") || containsString(loaded.ExcludedPaths, "custom") {
		t.Errorf("exclusions were not de-duplicated: %v", loaded.ExcludedPaths)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
