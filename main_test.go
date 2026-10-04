package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveModelAssetDirPrefersCompleteExecutableAssets(t *testing.T) {
	exeDir := t.TempDir()
	cwd := t.TempDir()
	writeModelAssets(t, exeDir)

	if got := resolveModelAssetDir(exeDir, cwd); got != exeDir {
		t.Fatalf("resolveModelAssetDir() = %q, want executable directory %q", got, exeDir)
	}
}

func TestResolveModelAssetDirFallsBackWhenExecutableAssetsAreIncomplete(t *testing.T) {
	exeDir := t.TempDir()
	cwd := t.TempDir()
	writeModelAssets(t, exeDir)
	if err := os.Remove(filepath.Join(exeDir, "image", "vision_model.onnx")); err != nil {
		t.Fatal(err)
	}

	if got := resolveModelAssetDir(exeDir, cwd); got != cwd {
		t.Fatalf("resolveModelAssetDir() = %q, want working directory %q", got, cwd)
	}
}

func TestSystemPathIndexSkipsGeneratedAndInstalledAppDirs(t *testing.T) {
	for _, name := range []string{"Program Files", "AppData", "node_modules", "build", "target"} {
		if !isSystemPathSkippedDir(name) {
			t.Errorf("isSystemPathSkippedDir(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"Documents", "Projects", "src", "pkg"} {
		if isSystemPathSkippedDir(name) {
			t.Errorf("isSystemPathSkippedDir(%q) = true, want false", name)
		}
	}
}

func TestIndexingStatusDistinguishesFastAndEnhancedSearch(t *testing.T) {
	app := NewApp()
	initial := app.GetIndexingStatus()
	if !initial.IsIndexing || initial.SearchReady || initial.EnhancedReady {
		t.Fatalf("initial status = %+v, want startup busy with neither index ready", initial)
	}
	if initial.StatusMessage != "Starting search engine..." {
		t.Fatalf("initial status message = %q, want startup message", initial.StatusMessage)
	}

	app.setReadiness(true, false)
	fast := app.GetIndexingStatus()
	if !fast.SearchReady || fast.EnhancedReady {
		t.Fatalf("fast-pass status = %+v, want basic search ready only", fast)
	}

	app.setReadiness(true, true)
	complete := app.GetIndexingStatus()
	if !complete.SearchReady || !complete.EnhancedReady {
		t.Fatalf("complete status = %+v, want both search modes ready", complete)
	}
}

func writeModelAssets(t *testing.T, root string) {
	t.Helper()
	for _, file := range modelAssetFiles {
		path := filepath.Join(root, file)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("model"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
