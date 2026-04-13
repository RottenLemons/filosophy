package shared

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// vipsSemaphore restricts total concurrent vipsthumbnail.exe processes.
var vipsSemaphore chan struct{}
var vipsOnce sync.Once

// InitVipsSemaphore initializes the global VIPS concurrency limit.
func InitVipsSemaphore(cap int) {
	vipsOnce.Do(func() {
		if cap < 1 {
			cap = 1
		}
		vipsSemaphore = make(chan struct{}, cap)
	})
}

// ImageJob holds all metadata required for a single image thumbnail job.
type ImageJob struct {
	Path  string
	Hash  int64
	MTime int64
	Size  int64
	CTime int64
	ATime int64
}

// VipsBatcher accumulates images and runs vipsthumbnail.exe in bulk
// to eliminate process forking overhead.
type VipsBatcher struct {
	mu           sync.Mutex
	cfg          *ProcessorConfig
	pending      []ImageJob
	maxBatchSize int
	flushTimer   *time.Timer
}

// NewVipsBatcher creates a new batcher
func NewVipsBatcher(cfg *ProcessorConfig, maxBatch int) *VipsBatcher {
	return &VipsBatcher{
		cfg:          cfg,
		maxBatchSize: maxBatch,
	}
}

// Add pushes a job to the batch. If the batch reaches max size, it flushes synchronously
// in the caller's goroutine (which inherently limits concurrency and avoids blowing up memory).
func (v *VipsBatcher) Add(job ImageJob) {
	v.mu.Lock()
	v.pending = append(v.pending, job)
	batch := v.pending
	
	if v.flushTimer != nil {
		v.flushTimer.Stop()
	}

	if len(v.pending) >= v.maxBatchSize {
		v.pending = nil
		v.mu.Unlock()
		v.flushBatch(batch)
		return
	}

	// Schedule a flush for lingering items
	v.flushTimer = time.AfterFunc(200*time.Millisecond, func() {
		v.Flush()
	})
	v.mu.Unlock()
}

// Flush forces any pending jobs to be processed immediately
func (v *VipsBatcher) Flush() {
	v.mu.Lock()
	batch := v.pending
	v.pending = nil
	if v.flushTimer != nil {
		v.flushTimer.Stop()
	}
	v.mu.Unlock()

	if len(batch) > 0 {
		v.flushBatch(batch)
	}
}

// flushBatch processes a grouped batch of images. It handles base-name collisions
// by splitting into sub-batches if necessary.
func (v *VipsBatcher) flushBatch(jobs []ImageJob) {
	// vipsthumbnail saves outputs based on "%s" (original filename without extension).
	// We must ensure no two files in a single exec call have the same base name,
	// otherwise vips will overwrite the generated thumbnail.
	subBatches := [][]ImageJob{}
	
	// Create sub-batches guaranteeing unique base names per batch
	for len(jobs) > 0 {
		var currentBatch []ImageJob
		var remaining []ImageJob
		seen := make(map[string]bool)

		for _, j := range jobs {
			base := strings.TrimSuffix(filepath.Base(j.Path), filepath.Ext(j.Path))
			if !seen[base] {
				seen[base] = true
				currentBatch = append(currentBatch, j)
			} else {
				remaining = append(remaining, j)
			}
		}
		subBatches = append(subBatches, currentBatch)
		jobs = remaining
	}

	// Process each conflict-free sub-batch
	for _, batch := range subBatches {
		v.runVipsExec(batch)
	}
}

func (v *VipsBatcher) runVipsExec(jobs []ImageJob) {
	if len(jobs) == 0 {
		return
	}

	args := make([]string, 0, len(jobs)+10)
	
	// Inject dynamic resource flags based on hardware profile.
	args = append(args, fmt.Sprintf("--vips-concurrency=%d", v.cfg.Hardware.VIPSThreads))
	args = append(args, fmt.Sprintf("--vips-cache-max=%d", v.cfg.Hardware.VIPSCache))
	args = append(args, fmt.Sprintf("--vips-cache-max-memory=%d", v.cfg.Hardware.VIPSCache))

	for _, j := range jobs {
		args = append(args, j.Path)
	}
	
	// -s 256x256! forces exact fit.
	args = append(args, "-s", "256x256!")
	// -o outputs to temp dir with the original basename + .jpg
	outFmt := filepath.Join(v.cfg.TempDir, "%s_v.jpg")
	args = append(args, "-o", outFmt+"[Q=80,strip]")

	cmd := exec.Command(vipsThumbnailPath(), args...)
	
	// Use semaphore to restrict concurrency of external VIPS calls.
	vipsSemaphore <- struct{}{}
	defer func() { <-vipsSemaphore }()
	
	// We don't necessarily care about individual errors stopping the whole batch
	if output, err := cmd.CombinedOutput(); err != nil {
		outStr := strings.TrimSpace(string(output))
		var filtered []string
		for _, line := range strings.Split(outStr, "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.Contains(line, "VIPS-WARNING") && !strings.Contains(line, "libvips warning") {
				filtered = append(filtered, line)
			}
		}
		if len(filtered) > 0 {
			log.Printf("vips batch warning/error: \n  %s", strings.Join(filtered, "\n  "))
		}
	}

	// Verify and rename outputs
	for _, j := range jobs {
		base := strings.TrimSuffix(filepath.Base(j.Path), filepath.Ext(j.Path))
		expectedOut := filepath.Join(v.cfg.TempDir, base+"_v.jpg")
		finalOut := filepath.Join(v.cfg.TempDir, fmt.Sprintf("%d.jpg", j.Hash))

		if _, err := os.Stat(expectedOut); err == nil {
			// Success: Rename to hash.jpg
			os.Rename(expectedOut, finalOut)
			HandleChunk(v.cfg.Images, v.cfg.Engine, "image", finalOut, j.Path, j.Hash, j.MTime, j.Size, j.CTime, j.ATime)
		} else {
			// Failed for this specific image: Create empty sentinel to prevent endless retrying
			HandleChunk(v.cfg.Chunks, v.cfg.Engine, "text", "", j.Path, empty, j.MTime, j.Size, j.CTime, j.ATime)
		}
	}
}
