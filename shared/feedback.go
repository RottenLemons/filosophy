package shared

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// FeedbackStore writes search telemetry to the search_feedback table.
// It holds a direct reference to the main sqlDB (filosophy.db) and is safe
// for concurrent use — each write is a single parameterized INSERT.
type FeedbackStore struct {
	db *sql.DB
}

// NewFeedbackStore wraps the given DB connection for feedback writes.
func NewFeedbackStore(db *sql.DB) *FeedbackStore {
	return &FeedbackStore{db: db}
}

// Submit records a single feedback event.
//
//   - sessionID: UUID generated once per app session (assigned by the caller).
//   - query:     the raw search string the user typed.
//   - path:      the result file path the user interacted with.
//   - rank:      zero-indexed position of this result in the displayed list.
//   - score:     the fusion score at display time.
//   - feedback:  +1 (thumbs up), -1 (thumbs down), 0 (click-through / implicit).
func (f *FeedbackStore) Submit(sessionID, query, path string, rank int, score float64, feedback int) error {
	if f.db == nil {
		return fmt.Errorf("feedback store: db not initialised")
	}
	_, err := f.db.ExecContext(
		context.Background(),
		`INSERT INTO search_feedback
			(session_id, query, result_path, result_rank, result_score, feedback, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sessionID, query, path, rank, score, feedback, time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("feedback store: insert: %w", err)
	}
	return nil
}

// ExportCSV returns all feedback rows since sinceUnix (Unix timestamp, inclusive)
// as a slice of maps — ready for JSON encoding or CSV export.
// Intended for the admin weight-tuning workflow.
func (f *FeedbackStore) ExportCSV(sinceUnix int64) ([]map[string]any, error) {
	rows, err := f.db.QueryContext(
		context.Background(),
		`SELECT id, session_id, query, result_path, result_rank, result_score, feedback, created_at
		 FROM search_feedback
		 WHERE created_at >= ?
		 ORDER BY created_at DESC`,
		sinceUnix,
	)
	if err != nil {
		return nil, fmt.Errorf("feedback export: %w", err)
	}
	defer rows.Close()

	var out []map[string]any
	for rows.Next() {
		var id, rank, fb, ts int64
		var sessionID, query, path string
		var score float64
		if err := rows.Scan(&id, &sessionID, &query, &path, &rank, &score, &fb, &ts); err != nil {
			continue
		}
		out = append(out, map[string]any{
			"id":           id,
			"session_id":   sessionID,
			"query":        query,
			"result_path":  path,
			"result_rank":  rank,
			"result_score": score,
			"feedback":     fb,
			"created_at":   ts,
		})
	}
	return out, rows.Err()
}
