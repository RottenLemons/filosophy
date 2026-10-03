package shared

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/liliang-cn/sqvect/v2/pkg/core"
)

func TestWriterReplacesVectorsAndMarksOnlyCompletedFiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	mainDB, err := sql.Open("sqlite", filepath.Join(dir, "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer mainDB.Close()
	if _, err := mainDB.Exec(`CREATE TABLE files (
		path TEXT PRIMARY KEY, hash INTEGER NOT NULL DEFAULT 0, mtime INTEGER NOT NULL DEFAULT 0,
		size INTEGER NOT NULL DEFAULT 0, content_indexed INTEGER NOT NULL DEFAULT 0,
		ext TEXT NOT NULL DEFAULT '', ctime INTEGER NOT NULL DEFAULT 0, atime INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := mainDB.Exec(`CREATE VIRTUAL TABLE paths_fts USING fts5(searchable, path UNINDEXED)`); err != nil {
		t.Fatal(err)
	}

	vectorPath := filepath.Join(dir, "vectors.db")
	store, err := core.New(vectorPath, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Init(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	collection, err := store.CreateCollection(ctx, "text", 2)
	if err != nil {
		t.Fatal(err)
	}
	vectorDB, err := sql.Open("sqlite", vectorPath)
	if err != nil {
		t.Fatal(err)
	}
	defer vectorDB.Close()

	path := filepath.Join(dir, "report.pdf")
	if _, err := mainDB.Exec(`INSERT INTO files(path, content_indexed) VALUES (?, 1)`, path); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(ctx, &core.Embedding{
		ID: "old-vector", CollectionID: collection.ID, Vector: []float32{1, 0}, Content: "old",
		Metadata: map[string]string{"path": path},
	}); err != nil {
		t.Fatal(err)
	}

	engine := &Engine{
		db: store, sqlDB: mainDB, vDB: vectorDB,
		writeChan: make(chan WriteOperation, 32),
	}
	go engine.runWriter()
	t.Cleanup(func() {
		close(engine.writeChan)
		engine.writerWg.Wait()
	})

	queue := func(op WriteOperation) {
		t.Helper()
		engine.writeChan <- op
		if err := engine.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	vector := func(id, content string) *core.Embedding {
		return &core.Embedding{
			ID: id, CollectionID: collection.ID, Vector: []float32{0, 1}, Content: content,
			Metadata: map[string]string{"path": path},
		}
	}
	countVectors := func() int {
		t.Helper()
		var count int
		if err := vectorDB.QueryRow(`SELECT COUNT(*) FROM embeddings WHERE json_extract(metadata, '$.path')=?`, path).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	indexed := func() int {
		t.Helper()
		var value int
		if err := mainDB.QueryRow(`SELECT content_indexed FROM files WHERE path=?`, path).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	fileMetadata := WriteOperation{
		FilePaths: []string{path}, FileHashes: []int64{12}, FileMtimes: []int64{34},
		FileSizes: []int64{56}, FileCtimes: []int64{78}, FileAtimes: []int64{90},
	}

	first := fileMetadata
	first.Op = OpIndexText
	first.SqEmbs = []*core.Embedding{vector("replacement-1", "new content")}
	first.CompletePaths = []string{path}
	queue(first)
	if countVectors() != 1 || indexed() != 1 {
		t.Fatalf("re-index left %d vectors and content_indexed=%d; want 1 and 1", countVectors(), indexed())
	}

	partial := fileMetadata
	partial.Op = OpIndexText
	partial.SqEmbs = []*core.Embedding{vector("chunk-1", "first chunk")}
	queue(partial)
	if indexed() != 0 {
		t.Fatal("file was marked indexed before its final chunk was written")
	}
	queue(WriteOperation{
		Op: OpIndexText, SqEmbs: []*core.Embedding{vector("chunk-2", "final chunk")},
		CompletePaths: []string{path},
	})
	if countVectors() != 2 || indexed() != 1 {
		t.Fatalf("completed file has %d vectors and content_indexed=%d; want 2 and 1", countVectors(), indexed())
	}

	queue(WriteOperation{Op: OpResetContentIndex})
	if countVectors() != 0 || indexed() != 0 {
		t.Fatalf("full reset left %d vectors and content_indexed=%d; want 0 and 0", countVectors(), indexed())
	}
}
