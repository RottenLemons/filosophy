//go:build !windows

package shared

import (
	"time"
)

func hiresNow() int64 {
	return time.Now().UnixNano()
}

func hiresElapsedMs(start, end int64) float64 {
	return float64(end-start) / 1e6
}
