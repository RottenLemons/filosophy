package shared

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/cespare/xxhash"
)

func TestHashFileUsesCompleteRawBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "document.pdf")
	content := bytes.Repeat([]byte("a"), 128*1024)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}

	first, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content[len(content)-1] = 'b'
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	second, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("change beyond the first 64 KiB was not included in the hash")
	}
	if want := int64(xxhash.Sum64(content)); second != want {
		t.Fatalf("HashFile() = %d, want raw-byte hash %d", second, want)
	}
}

func TestIndexBatchPlanKeepsContinuationChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "long.pdf")
	plan := makeIndexBatchPlan([]Metadata{
		{Content: "first", Path: path, Hash: 123, Mtime: 10, Size: 20},
		{Content: "middle", Path: path, Hash: empty},
		{Content: "last", Path: path, Hash: empty, FileDone: true},
		{Path: filepath.Join(t.TempDir(), "unsupported.bin"), Hash: 456, Mtime: 11, Size: 12},
	})

	if len(plan.contentItems) != 3 {
		t.Fatalf("content chunks = %d, want 3", len(plan.contentItems))
	}
	if len(plan.skipPaths) != 1 {
		t.Fatalf("skipped files = %d, want 1", len(plan.skipPaths))
	}
	if len(plan.filePaths) != 1 || plan.fileHashes[0] != 123 {
		t.Fatalf("file metadata = %v / %v, want one record with raw hash 123", plan.filePaths, plan.fileHashes)
	}
	if len(plan.completePaths) != 1 || plan.completePaths[0] != path {
		t.Fatalf("completion paths = %v, want [%s]", plan.completePaths, path)
	}
}
