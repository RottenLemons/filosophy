package shared

import (
	"context"
	"database/sql"
	"log"
	"sort"
	"sync"
	"time"
)

// atimeStats caches whether atime tracking is meaningful for this machine.
// On Windows/Linux with noatime mounts, all atimes are 0 or identical to mtime.
// If we detect that, we skip the atime signal entirely so it doesn't add noise.
var (
	atimeOnce    sync.Once
	atimeEnabled bool // true if atime values vary meaningfully across files
)

// CheckAtimeEnabled probes the files table to determine whether atime values
// differ meaningfully across files. Called once at engine startup.
// If fewer than 10 files have non-zero atime, or the variance is zero, we
// consider atime unreliable and disable the signal.
func CheckAtimeEnabled(db *sql.DB) bool {
	atimeOnce.Do(func() {
		rows, err := db.QueryContext(context.Background(),
			`SELECT atime FROM files WHERE atime > 0 LIMIT 100`)
		if err != nil {
			atimeEnabled = false
			return
		}
		defer rows.Close()

		var vals []int64
		for rows.Next() {
			var v int64
			if rows.Scan(&v) == nil {
				vals = append(vals, v)
			}
		}
		if len(vals) < 10 {
			log.Println("[recency] atime: fewer than 10 files have non-zero atime — signal disabled")
			atimeEnabled = false
			return
		}

		// Check variance: if min == max, all atimes are identical → disabled.
		sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
		if vals[0] == vals[len(vals)-1] {
			log.Println("[recency] atime: all values identical — signal disabled")
			atimeEnabled = false
			return
		}

		log.Println("[recency] atime: signal enabled")
		atimeEnabled = true
	})
	return atimeEnabled
}

// AtimeEnabled returns the cached result of CheckAtimeEnabled without
// hitting the DB again.
func AtimeEnabled() bool {
	return atimeEnabled
}

// ApplyRecencyBoost adds a recency score bonus to each result in place.
// mtime values (nanoseconds since epoch) are read from the results themselves;
// the boost is  w * exp(-ageDays / halfLifeDays).
//
// Results are mutated directly — no allocation.
func ApplyRecencyBoost(results []SearchResult, w, halfLifeDays float64) {
	if w <= 0 || halfLifeDays <= 0 {
		return
	}
	now := time.Now().UnixNano()
	for i := range results {
		// Modified is stored as RFC3339 string; parse it to get ns.
		// We use a best-effort parse — if it fails the boost is 0 for that file.
		t, err := time.Parse(time.RFC3339, results[i].Modified)
		if err != nil {
			continue
		}
		ageDays := float64(now-t.UnixNano()) / float64(int64(24*time.Hour))
		if ageDays < 0 {
			ageDays = 0
		}
		boost := w * RecencyScore(t.UnixNano(), halfLifeDays)
		results[i].Score += boost
	}
}
