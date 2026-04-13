package shared

import (
	"log"
	"runtime"
)

// HardwareConfig holds the adaptive resource limits for different hardware tiers.
type HardwareConfig struct {
	Tier               int    // 1: Low, 2: Mid, 3: High
	TierName           string
	VIPSCap            int    // Number of concurrent VIPS processes
	VIPSThreads        int    // Internal VIPS concurrency per process
	VIPSCache          int    // VIPS cache size in MB (0 to disable)
	GCInterval         int    // Number of files between explicit FreeOSMemory() calls
	DirectMLKneecap    bool   // Disable ORT memory pattern and arena for stability
}

// DetermineHardwareProfile calculates the hardware tier and associated limits.
func DetermineHardwareProfile() HardwareConfig {
	totalRAM, _ := GetMemoryInfo()
	totalGB := int(totalRAM / (1024 * 1024 * 1024))
	numCPU := runtime.NumCPU()

	var cfg HardwareConfig

	if totalGB < 4 {
		// Tier 1: Low-end / Resource-constrained (< 4GB)
		cfg = HardwareConfig{
			Tier:            1,
			TierName:        "Low",
			VIPSCap:         2,
			VIPSThreads:     1,
			VIPSCache:       0,
			GCInterval:      50,
			DirectMLKneecap: true,
		}
	} else if totalGB < 16 {
		// Tier 2: Mid-range (4GB - 16GB)
		cap := numCPU / 4
		if cap < 2 {
			cap = 2
		}
		cfg = HardwareConfig{
			Tier:            2,
			TierName:        "Mid",
			VIPSCap:         cap,
			VIPSThreads:     1,
			VIPSCache:       50,
			GCInterval:      200,
			DirectMLKneecap: false,
		}
	} else {
		// Tier 3: High-end (> 16GB)
		cap := numCPU / 2
		if cap < 2 {
			cap = 2
		}
		cfg = HardwareConfig{
			Tier:            3,
			TierName:        "High",
			VIPSCap:         cap,
			VIPSThreads:     1,
			VIPSCache:       200,
			GCInterval:      500,
			DirectMLKneecap: false,
		}
	}

	log.Printf("[Profiler] Detected %d GB RAM / %d CPUs -> Tier %d (%s)", 
		totalGB, numCPU, cfg.Tier, cfg.TierName)
	log.Printf("[Profiler] Config: VIPS_Cap=%d, VIPS_Threads=%d, VIPS_Cache=%dMB, GC_Interval=%d, DirectML_Kneecap=%v",
		cfg.VIPSCap, cfg.VIPSThreads, cfg.VIPSCache, cfg.GCInterval, cfg.DirectMLKneecap)

	return cfg
}
