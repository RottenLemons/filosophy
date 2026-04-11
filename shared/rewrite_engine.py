import io

file_path = r"c:\Users\lqyau\OneDrive\Documents\GitHub\filosophy\shared\engine.go"
with io.open(file_path, "r", encoding="utf-8") as f:
    text = f.read()

# 1. Struct Modification
text = text.replace("""	textCollectionID  int                         // cached collection ID for text embeddings
	imageCollectionID int                         // cached collection ID for image embeddings
	mu                sync.RWMutex // RWMutex: concurrent reads (Search) don't block each other
	initTableOnce     sync.Once    // ensures InitIndexTables runs at most once per Engine
}""", """	textCollectionID  int                         // cached collection ID for text embeddings
	imageCollectionID int                         // cached collection ID for image embeddings
	vDB               *sql.DB                     // connection to vectors.db
	initTableOnce     sync.Once                   // ensures InitIndexTables runs at most once per Engine
	writeChan         chan WriteOperation
	writerWg          sync.WaitGroup
}

type WriteOpType int

const (
	OpIndexText WriteOpType = iota
	OpIndexImage
	OpIndexMetadata
	OpMarkContentIndexed
	OpIndexPathsFTS
	OpDeletePaths
	OpRenamePaths
	OpResetContentIndex
)

type WriteOperation struct {
	Op         WriteOpType
	Paths      []string
	FilePaths  []string
	FileHashes []int64
	FileMtimes []int64
	FileSizes  []int64
	FileCtimes []int64
	FileAtimes []int64
	Contents   []string
	SqEmbs     []*core.Embedding
	OldPaths   []string
	NewPaths   []string
}""")

# 2. Split DB implementation in New()
old_db_open = """	log.Println("[Engine 1] Opening sqvect database at", dbPath)
	// Open sqvect database for vector operations
	cfg := sqvect.Config{
		Path:         dbPath,
		Dimensions:   0, // auto-detect
		SimilarityFn: core.CosineSimilarity,
		IndexType:    core.IndexTypeHNSW,
	}
	db, err := sqvect.Open(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqvect database: %w", err)
	}

	// Open a separate sql.DB for the files dedup table
	log.Println("[Engine 2] Opening companion SQL database...")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to open sql database: %w", err)
	}

	_, availBytes := GetMemoryInfo()
	availMB := availBytes / (1024 * 1024)
	cacheKB := availMB * 5
	if cacheKB < 20000 {
		cacheKB = 20000 // minimum 20MB cache
	}

	tempStore := "MEMORY"
	if availBytes < 6*1024*1024*1024 { // switch to FILE temp_store if under 6GB available
		tempStore = "FILE"
	}

	// WAL mode and pragmas for concurrency
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		fmt.Sprintf("PRAGMA temp_store=%s", tempStore),
		"PRAGMA mmap_size=536870912",
		fmt.Sprintf("PRAGMA cache_size=-%d", cacheKB),
		"PRAGMA busy_timeout=10000",
	} {
		if _, err := sqlDB.Exec(pragma); err != nil {
			log.Printf("pragma warning: %v", err)
		}
	}
	// Allow parallel SQLite readers across goroutines (WAL supports concurrent reads).
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)"""

new_db_open = """	dir := filepath.Dir(dbPath)
	base := filepath.Base(dbPath)
	vectorsBase := "vectors.db"
	if base != "filosophy.db" {
		vectorsBase = strings.TrimSuffix(base, filepath.Ext(base)) + "_vectors" + filepath.Ext(base)
	}
	vectorsDBPath := filepath.Join(dir, vectorsBase)

	log.Println("[Engine 1] Opening SQL databases...")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sql database: %w", err)
	}
	
	vDB, err := sql.Open("sqlite", vectorsDBPath)
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("failed to open vectors sql database: %w", err)
	}

	_, availBytes := GetMemoryInfo()
	availMB := availBytes / (1024 * 1024)
	cacheKB := availMB * 5
	if cacheKB < 20000 {
		cacheKB = 20000 // minimum 20MB cache
	}

	tempStore := "MEMORY"
	if availBytes < 6*1024*1024*1024 { // switch to FILE temp_store if under 6GB available
		tempStore = "FILE"
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		fmt.Sprintf("PRAGMA temp_store=%s", tempStore),
		"PRAGMA mmap_size=536870912",
		fmt.Sprintf("PRAGMA cache_size=-%d", cacheKB),
		"PRAGMA busy_timeout=10000",
	}

	for _, dbConn := range []*sql.DB{sqlDB, vDB} {
		for _, pragma := range pragmas {
			if _, err := dbConn.Exec(pragma); err != nil {
				log.Printf("pragma warning: %v", err)
			}
		}
		dbConn.SetMaxOpenConns(4)
		dbConn.SetMaxIdleConns(4)
	}

	log.Println("[Engine 2] Opening sqvect database at", vectorsDBPath)
	cfg := sqvect.Config{
		Path:         vectorsDBPath,
		Dimensions:   0, // auto-detect
		SimilarityFn: core.CosineSimilarity,
		IndexType:    core.IndexTypeHNSW,
	}
	db, err := sqvect.Open(cfg)
	if err != nil {
		sqlDB.Close()
		vDB.Close()
		return nil, fmt.Errorf("failed to open sqvect database: %w", err)
	}"""
text = text.replace(old_db_open, new_db_open)

# 3. New() Return & RunWriter Call
old_ret = """	return &Engine{
		db:                db,
		sqlDB:             sqlDB,
		staticEmb:         staticEmb,
		clipTok:           clipTok,
		clipTextSession:   clipTextSession,
		clipVisionSession: clipVisionSession,
		rerankerSession:   rerankerSession,
		rerankerTok:       rerankerTok,
		textCollectionID:  textCol.ID,
		imageCollectionID: imageCol.ID,
	}, nil"""
new_ret = """	engine := &Engine{
		db:                db,
		sqlDB:             sqlDB,
		vDB:               vDB,
		staticEmb:         staticEmb,
		clipTok:           clipTok,
		clipTextSession:   clipTextSession,
		clipVisionSession: clipVisionSession,
		rerankerSession:   rerankerSession,
		rerankerTok:       rerankerTok,
		textCollectionID:  textCol.ID,
		imageCollectionID: imageCol.ID,
		writeChan:         make(chan WriteOperation, 10000),
	}
	go engine.runWriter()
	return engine, nil"""
text = text.replace(old_ret, new_ret)

# 4. runWriter and Close() logic
old_close = """// Close shuts down the Engine, releasing all resources.
func (s *Engine) Close() error {
	var errs []error
	if s.clipVisionSession != nil {
		if err := s.clipVisionSession.Destroy(); err != nil {
			errs = append(errs, err)
		}
	}"""
new_close = """func (s *Engine) runWriter() {
	s.writerWg.Add(1)
	defer s.writerWg.Done()

	var batch []WriteOperation
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}

		ctx := context.Background()
		tx, err := s.sqlDB.BeginTx(ctx, nil)
		if err != nil {
			log.Printf("writer begin tx error: %v", err)
			batch = batch[:0]
			return
		}

		var sqEmbsBatch []*core.Embedding
		var deletePathIDs []string

		for _, op := range batch {
			switch op.Op {
			case OpIndexText, OpIndexImage:
				stmt, _ := tx.PrepareContext(ctx, `
					INSERT INTO files(path, hash, mtime, size, content_indexed, ext, ctime, atime)
					VALUES (?, ?, ?, ?, 1, ?, ?, ?)
					ON CONFLICT(path) DO UPDATE SET
						hash=excluded.hash, mtime=excluded.mtime, size=excluded.size, content_indexed=1,
						ext=excluded.ext, atime=excluded.atime`)
				if stmt != nil {
					for i := range op.FilePaths {
						ext := strings.ToLower(filepath.Ext(op.FilePaths[i]))
						stmt.ExecContext(ctx, op.FilePaths[i], op.FileHashes[i], op.FileMtimes[i], op.FileSizes[i], ext, op.FileCtimes[i], op.FileAtimes[i])
					}
					stmt.Close()
				}
				sqEmbsBatch = append(sqEmbsBatch, op.SqEmbs...)

			case OpIndexMetadata:
				filesStmt, _ := tx.PrepareContext(ctx, `
					INSERT OR IGNORE INTO files(path, hash, mtime, size, content_indexed, ext, ctime, atime)
					VALUES (?, 0, ?, ?, 0, ?, ?, ?)`)
				ftsStmt, _ := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO paths_fts(searchable, path) VALUES (?, ?)`)
				if filesStmt != nil && ftsStmt != nil {
					for i := range op.Paths {
						ext := strings.ToLower(filepath.Ext(op.Paths[i]))
						filesStmt.ExecContext(ctx, op.Paths[i], op.FileMtimes[i], op.FileSizes[i], ext, op.FileCtimes[i], op.FileAtimes[i])
						ftsStmt.ExecContext(ctx, pathUnescape(op.Paths[i]), op.Paths[i])
					}
					filesStmt.Close()
					ftsStmt.Close()
				}

			case OpMarkContentIndexed:
				stmt, _ := tx.PrepareContext(ctx, `UPDATE files SET content_indexed=1 WHERE path=?`)
				if stmt != nil {
					for _, p := range op.Paths {
						stmt.ExecContext(ctx, p)
					}
					stmt.Close()
				}

			case OpIndexPathsFTS:
				stmt, _ := tx.PrepareContext(ctx, "INSERT INTO paths_fts(searchable, path) VALUES (?, ?)")
				if stmt != nil {
					for _, p := range op.Paths {
						stmt.ExecContext(ctx, pathUnescape(p), p)
					}
					stmt.Close()
				}

			case OpDeletePaths:
				for _, path := range op.Paths {
					rows, errQ := s.vDB.QueryContext(ctx, "SELECT id FROM embeddings WHERE json_extract(metadata, '$.path') = ?", path)
					if errQ == nil && rows != nil {
						var ids []string
						for rows.Next() {
							var id string
							rows.Scan(&id)
							ids = append(ids, id)
						}
						rows.Close()
						deletePathIDs = append(deletePathIDs, ids...)
					}
					tx.ExecContext(ctx, "DELETE FROM files WHERE path = ?", path)
					tx.ExecContext(ctx, "DELETE FROM paths_fts WHERE path = ?", path)
				}

			case OpRenamePaths:
				for i, oldPath := range op.OldPaths {
					newPath := op.NewPaths[i]
					s.vDB.ExecContext(ctx, `UPDATE embeddings SET metadata = json_set(metadata, '$.path', ?) WHERE json_extract(metadata, '$.path') = ?`, newPath, oldPath)
					tx.ExecContext(ctx, "UPDATE files SET path = ? WHERE path = ?", newPath, oldPath)
					tx.ExecContext(ctx, "DELETE FROM paths_fts WHERE path = ?", oldPath)
					tx.ExecContext(ctx, "INSERT INTO paths_fts(searchable, path) VALUES (?, ?)", pathUnescape(newPath), newPath)
				}

			case OpResetContentIndex:
				tx.ExecContext(ctx, `UPDATE files SET content_indexed = 0, hash = 0`)
				tx.ExecContext(ctx, `DELETE FROM paths_fts`)
			}
		}

		if err := tx.Commit(); err != nil {
			log.Printf("writer commit error: %v", err)
		}

		if len(sqEmbsBatch) > 0 {
			if err := s.db.Vector().UpsertBatch(ctx, sqEmbsBatch); err != nil {
				log.Printf("writer sqvect upsert error: %v", err)
			}
		}

		if len(deletePathIDs) > 0 {
			if err := s.db.Vector().DeleteBatch(ctx, deletePathIDs); err != nil {
				log.Printf("writer sqvect delete error: %v", err)
			}
		}

		batch = batch[:0]
	}

	for {
		select {
		case op, ok := <-s.writeChan:
			if !ok {
				flush()
				return
			}
			batch = append(batch, op)
			if len(batch) >= 50 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// Close shuts down the Engine, releasing all resources.
func (s *Engine) Close() error {
	if s.writeChan != nil {
		close(s.writeChan)
		s.writerWg.Wait()
	}

	ctx := context.Background()
	if s.sqlDB != nil {
		s.sqlDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);")
	}
	if s.vDB != nil {
		s.vDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);")
	}

	var errs []error
	if s.clipVisionSession != nil {
		if err := s.clipVisionSession.Destroy(); err != nil {
			errs = append(errs, err)
		}
	}"""
text = text.replace(old_close, new_close)

old_close2 = """	if s.sqlDB != nil {
		if err := s.sqlDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			errs = append(errs, err)
		}
	}"""
new_close2 = """	if s.vDB != nil {
		if err := s.vDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.sqlDB != nil {
		if err := s.sqlDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			errs = append(errs, err)
		}
	}"""
text = text.replace(old_close2, new_close2)

# 5. Remove upsertFiles
import re
text = re.sub(r'// upsertFiles inserts or updates file-level hashes.*?return tx\.Commit\(\)\n}', '', text, flags=re.DOTALL)

# 6. IndexText
old_itext = """	// Acquire write lock only for the database writes.
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.upsertFiles(ctx, filePaths, fileHashes, fileMtimes, fileSizes, fileCtimes, fileAtimes); err != nil {
		return fmt.Errorf("upsert files failed: %w", err)
	}
	if err := s.db.Vector().UpsertBatch(ctx, sqEmbs); err != nil {
		return fmt.Errorf("vector upsert failed: %w", err)
	}
	return nil"""
new_itext = """	s.writeChan <- WriteOperation{
		Op:         OpIndexText,
		FilePaths:  filePaths,
		FileHashes: fileHashes,
		FileMtimes: fileMtimes,
		FileSizes:  fileSizes,
		FileCtimes: fileCtimes,
		FileAtimes: fileAtimes,
		SqEmbs:     sqEmbs,
	}
	return nil"""
text = text.replace(old_itext, new_itext)

# 7. IndexImage
old_img = """	// Acquire write lock only for the database writes.
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.upsertFiles(ctx, filePaths, fileHashes, fileMtimes, fileSizes, fileCtimes, fileAtimes); err != nil {
		return fmt.Errorf("upsert files failed: %w", err)
	}
	if err := s.db.Vector().UpsertBatch(ctx, sqEmbs); err != nil {
		return fmt.Errorf("vector upsert failed: %w", err)
	}
	return nil"""
new_img = """	s.writeChan <- WriteOperation{
		Op:         OpIndexImage,
		FilePaths:  filePaths,
		FileHashes: fileHashes,
		FileMtimes: fileMtimes,
		FileSizes:  fileSizes,
		FileCtimes: fileCtimes,
		FileAtimes: fileAtimes,
		SqEmbs:     sqEmbs,
	}
	return nil"""
text = text.replace(old_img, new_img)

# 8. IndexPathsFTS
old_fts = """// IndexPathsFTS inserts unique paths into the paths_fts FTS5 table.
// Called from the processor layer after indexing embeddings.
func (s *Engine) IndexPathsFTS(paths []string) {
	if len(paths) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := context.Background()
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("paths_fts: failed to begin tx: %v", err)
		return
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO paths_fts(searchable, path) VALUES (?, ?)")
	if err != nil {
		tx.Rollback()
		log.Printf("paths_fts: failed to prepare stmt: %v", err)
		return
	}
	defer stmt.Close()
	for _, p := range paths {
		decoded := pathUnescape(p)
		if _, err := stmt.ExecContext(ctx, decoded, p); err != nil {
			log.Printf("paths_fts insert warning: %v", err)
		}
	}
	tx.Commit()
}"""
new_fts = """// IndexPathsFTS inserts unique paths into the paths_fts FTS5 table.
// Called from the processor layer after indexing embeddings.
func (s *Engine) IndexPathsFTS(paths []string) {
	if len(paths) == 0 {
		return
	}
	s.writeChan <- WriteOperation{
		Op:    OpIndexPathsFTS,
		Paths: paths,
	}
}"""
text = text.replace(old_fts, new_fts)

# 9. IndexMetadata
old_meta = """// IndexMetadata inserts file paths, mtimes, sizes, and file-system metadata
// (extension, creation time, last-access time) into the files table without
// marking them as content-indexed, and populates paths_fts so path-based search
// works immediately after Pass 1. Already-present rows are left untouched (INSERT OR IGNORE).
// ctimes and atimes are extracted from os.FileInfo by the caller \u2014 no extra os.Stat here.
func (s *Engine) IndexMetadata(paths []string, mtimes, sizes, ctimes, atimes []int64) error {
	if len(paths) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := context.Background()
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	filesStmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO files(path, hash, mtime, size, content_indexed, ext, ctime, atime)
		VALUES (?, 0, ?, ?, 0, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer filesStmt.Close()

	// IX-5 fix: populate the searchable column so FTS5 can index paths immediately.
	// Previously only path was inserted (searchable=NULL), making all Pass-1 files
	// invisible to path keyword search until Pass 2 re-ran IndexPathsFTS.
	ftsStmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO paths_fts(searchable, path) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer ftsStmt.Close()

	for i := range paths {
		ext := strings.ToLower(filepath.Ext(paths[i]))
		if _, err := filesStmt.ExecContext(ctx, paths[i], mtimes[i], sizes[i], ext, ctimes[i], atimes[i]); err != nil {
			return err
		}
		if _, err := ftsStmt.ExecContext(ctx, pathUnescape(paths[i]), paths[i]); err != nil {
			return err
		}
	}
	return tx.Commit()
}"""
new_meta = """// IndexMetadata inserts file paths, mtimes, sizes, and file-system metadata
// (extension, creation time, last-access time) into the files table without
// marking them as content-indexed, and populates paths_fts so path-based search
// works immediately after Pass 1. Already-present rows are left untouched (INSERT OR IGNORE).
// ctimes and atimes are extracted from os.FileInfo by the caller \u2014 no extra os.Stat here.
func (s *Engine) IndexMetadata(paths []string, mtimes, sizes, ctimes, atimes []int64) error {
	if len(paths) == 0 {
		return nil
	}
	s.writeChan <- WriteOperation{
		Op:         OpIndexMetadata,
		Paths:      paths,
		FileMtimes: mtimes,
		FileSizes:  sizes,
		FileCtimes: ctimes,
		FileAtimes: atimes,
	}
	return nil
}"""
text = text.replace(old_meta, new_meta)

# 10. MarkContentIndexed
old_mark = """// MarkContentIndexed sets content_indexed=1 for the given paths without storing
// any embedding. Used for files that were processed but yielded no extractable
// content (binaries, unsupported formats, etc.) so they are never re-queued.
func (s *Engine) MarkContentIndexed(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	ctx := context.Background()
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `UPDATE files SET content_indexed=1 WHERE path=?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, p := range paths {
		if _, err := stmt.ExecContext(ctx, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}"""
new_mark = """// MarkContentIndexed sets content_indexed=1 for the given paths without storing
// any embedding. Used for files that were processed but yielded no extractable
// content (binaries, unsupported formats, etc.) so they are never re-queued.
func (s *Engine) MarkContentIndexed(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	s.writeChan <- WriteOperation{
		Op:    OpMarkContentIndexed,
		Paths: paths,
	}
	return nil
}"""
text = text.replace(old_mark, new_mark)

# 11. ResetContentIndex
old_reset = """// ResetContentIndex clears content_indexed/hashes and paths_fts, forcing a full
// re-index on the next run. Called when --index is passed.
func (s *Engine) ResetContentIndex() error {
	ctx := context.Background()
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE files SET content_indexed = 0, hash = 0`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM paths_fts`); err != nil {
		return err
	}
	return tx.Commit()
}"""
new_reset = """// ResetContentIndex clears content_indexed/hashes and paths_fts, forcing a full
// re-index on the next run. Called when --index is passed.
func (s *Engine) ResetContentIndex() error {
	s.writeChan <- WriteOperation{
		Op: OpResetContentIndex,
	}
	return nil
}"""
text = text.replace(old_reset, new_reset)

# 12. DeletePaths
old_del = """// DeletePaths removes all embeddings for the given file paths.
func (s *Engine) DeletePaths(paths []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()

	for _, path := range paths {
		// Find embedding IDs by path in sqvect's embeddings table
		rows, err := s.sqlDB.QueryContext(ctx,
			"SELECT id FROM embeddings WHERE json_extract(metadata, '$.path') = ?", path)
		if err != nil {
			return fmt.Errorf("query embeddings by path failed: %w", err)
		}

		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()

		// Delete from sqvect
		if len(ids) > 0 {
			if err := s.db.Vector().DeleteBatch(ctx, ids); err != nil {
				return fmt.Errorf("delete embeddings failed: %w", err)
			}
		}

		// Delete from files table
		if _, err := s.sqlDB.ExecContext(ctx, "DELETE FROM files WHERE path = ?", path); err != nil {
			return fmt.Errorf("delete files failed: %w", err)
		}

		// Delete from paths FTS5
		if _, err := s.sqlDB.ExecContext(ctx,
			"DELETE FROM paths_fts WHERE path = ?", path); err != nil {
			log.Printf("paths_fts delete warning: %v", err)
		}
	}
	return nil
}"""
new_del = """// DeletePaths removes all embeddings for the given file paths.
func (s *Engine) DeletePaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	s.writeChan <- WriteOperation{
		Op:    OpDeletePaths,
		Paths: paths,
	}
	return nil
}"""
text = text.replace(old_del, new_del)

# 13. RenamePaths
old_ren = """// RenamePaths updates path references in sqvect embeddings and the files table.
func (s *Engine) RenamePaths(oldPaths, newPaths []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for i, oldPath := range oldPaths {
		newPath := newPaths[i]
		// Update path in sqvect's embeddings metadata JSON
		if _, err := tx.ExecContext(ctx,
			`UPDATE embeddings SET metadata = json_set(metadata, '$.path', ?) WHERE json_extract(metadata, '$.path') = ?`,
			newPath, oldPath); err != nil {
			return fmt.Errorf("rename embedding paths failed: %w", err)
		}
		// Update files table
		if _, err := tx.ExecContext(ctx, "UPDATE files SET path = ? WHERE path = ?", newPath, oldPath); err != nil {
			return fmt.Errorf("rename files failed: %w", err)
		}
		// Update paths FTS5 (delete old, insert new)
		tx.ExecContext(ctx, "DELETE FROM paths_fts WHERE path = ?", oldPath)
		tx.ExecContext(ctx, "INSERT INTO paths_fts(searchable, path) VALUES (?, ?)", pathUnescape(newPath), newPath)
	}

	return tx.Commit()
}"""
new_ren = """// RenamePaths updates path references in sqvect embeddings and the files table.
func (s *Engine) RenamePaths(oldPaths, newPaths []string) error {
	if len(oldPaths) == 0 {
		return nil
	}
	s.writeChan <- WriteOperation{
		Op:       OpRenamePaths,
		OldPaths: oldPaths,
		NewPaths: newPaths,
	}
	return nil
}"""
text = text.replace(old_ren, new_ren)

# 14. TruncateWAL
old_trunc = """// TruncateWAL executes a checkpoint TRUNCATE to clear out the massive -wal file 
// generated by batched inserts.
func (s *Engine) TruncateWAL() error {
	ctx := context.Background()
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.sqlDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);")
	if err != nil {
		log.Printf("WAL truncate error: %v", err)
	}
	return err
}"""
new_trunc = """// TruncateWAL executes a checkpoint TRUNCATE to clear out the massive -wal file 
// generated by batched inserts.
func (s *Engine) TruncateWAL() error {
	ctx := context.Background()
	_, err := s.sqlDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);")
	if err != nil {
		log.Printf("WAL truncate error: %v", err)
	}
	_, errV := s.vDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);")
	if errV != nil && err == nil {
		err = errV
	}
	return err
}"""
text = text.replace(old_trunc, new_trunc)

# 15. Locks & vDB queries
text = text.replace("""	// Acquire read lock for all DB operations.
	// RLock allows concurrent Search() calls to proceed in parallel;
	// write operations (IndexText/IndexImage) only block briefly during UpsertBatch.
	s.mu.RLock()
	defer s.mu.RUnlock()""", "")

text = text.replace("""	s.mu.RLock()
	defer s.mu.RUnlock()""", "")

text = text.replace("""	s.mu.Lock()
	defer s.mu.Unlock()""", "")

text = text.replace("""	rows, err := s.sqlDB.QueryContext(ctx, `
		SELECT json_extract(e.metadata, '$.path'), -bm25(chunks_fts)
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts)
		LIMIT 400
	`, ftsQuery)""", """	rows, err := s.vDB.QueryContext(ctx, `
		SELECT json_extract(e.metadata, '$.path'), -bm25(chunks_fts)
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts)
		LIMIT 400
	`, ftsQuery)""")

text = text.replace("""	rows, err := s.sqlDB.QueryContext(ctx, `
		SELECT json_extract(e.metadata, '$.path'), -bm25(chunks_fts)
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts)
		LIMIT 500
	`, ftsQuery)""", """	rows, err := s.vDB.QueryContext(ctx, `
		SELECT json_extract(e.metadata, '$.path'), -bm25(chunks_fts)
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts)
		LIMIT 500
	`, ftsQuery)""")

text = text.replace("""	rows, err := s.sqlDB.QueryContext(ctx, `
		SELECT json_extract(e.metadata, '$.path'), -bm25(chunks_fts)
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts)
		LIMIT 100
	`, ftsQuery)""", """	rows, err := s.vDB.QueryContext(ctx, `
		SELECT json_extract(e.metadata, '$.path'), -bm25(chunks_fts)
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts)
		LIMIT 100
	`, ftsQuery)""")

text = text.replace("""	rows, err := s.sqlDB.QueryContext(context.Background(), `
		SELECT json_extract(e.metadata, '$.path'), content
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		  AND json_extract(e.metadata, '$.path') IN (`+placeholders+`)
		  AND length(content) > 0
		ORDER BY bm25(chunks_fts)
		LIMIT ?
	`, append(args, len(paths)*maxSnippetsPerDoc)...)""", """	rows, err := s.vDB.QueryContext(context.Background(), `
		SELECT json_extract(e.metadata, '$.path'), content
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		  AND json_extract(e.metadata, '$.path') IN (`+placeholders+`)
		  AND length(content) > 0
		ORDER BY bm25(chunks_fts)
		LIMIT ?
	`, append(args, len(paths)*maxSnippetsPerDoc)...)""")

with io.open(file_path, "w", encoding="utf-8") as f:
    f.write(text)
