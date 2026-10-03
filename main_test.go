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
