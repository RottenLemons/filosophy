package shared

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type AppConfig struct {
	ExcludedPaths   []string `json:"ExcludedPaths"`
	GPUEnabled      bool     `json:"GPUEnabled"`
	HasGPU          bool     `json:"HasGPU"`
	HasCheckedGPU   bool     `json:"HasCheckedGPU"`
	path            string
	mu              sync.RWMutex
	normalizedPaths []string `json:"-"`
}


func (c *AppConfig) rebuildCacheLocked() {
	c.normalizedPaths = make([]string, len(c.ExcludedPaths))
	for i, p := range c.ExcludedPaths {
		v := strings.ToLower(filepath.ToSlash(p))
		if !strings.HasSuffix(v, "/") {
			v += "/"
		}
		c.normalizedPaths[i] = v
	}
}

func LoadConfig(dir string) *AppConfig {
	path := filepath.Join(dir, "filosophy_config.json")
	// Try to migrate from old excluded config if it exists
	oldPath := filepath.Join(dir, "filosophy_excluded.json")
	
	config := &AppConfig{
		ExcludedPaths: []string{".git", "node_modules", "anaconda3", "miniconda3"},
		GPUEnabled:    false,
		HasGPU:        false,
		HasCheckedGPU: false,
		path:         path,
	}

	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, config)
	} else if oldData, err := os.ReadFile(oldPath); err == nil {
		// Migration path
		var paths []string
		json.Unmarshal(oldData, &paths)
		config.ExcludedPaths = append(config.ExcludedPaths, paths...)
		config.Save()
		os.Remove(oldPath) // Cleanup old file
	}

	config.rebuildCacheLocked()
	return config
}


func (c *AppConfig) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	// Normalize paths
	unique := make(map[string]bool)
	var paths []string
	for _, p := range c.ExcludedPaths {
		l := strings.ToLower(p)
		if !unique[l] {
			unique[l] = true
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	c.ExcludedPaths = paths
	c.rebuildCacheLocked()

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0644)
}

func (c *AppConfig) IsExcluded(path string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	lower := strings.ToLower(filepath.ToSlash(path))
	for _, excl := range c.normalizedPaths {
		if lower == strings.TrimSuffix(excl, "/") || strings.HasPrefix(lower, excl) {
			return true
		}
	}
	return false
}

func (c *AppConfig) SetExcluded(path string, excluded bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	lower := strings.ToLower(path)
	newPaths := []string{}
	found := false
	
	for _, p := range c.ExcludedPaths {
		if strings.ToLower(p) == lower {
			if excluded {
				newPaths = append(newPaths, p)
				found = true
			}
		} else {
			newPaths = append(newPaths, p)
		}
	}
	
	if excluded && !found {
		newPaths = append(newPaths, path)
	}
	
	if !excluded {
		// If we are removing an exclusion, we just filter it out
		c.ExcludedPaths = newPaths
	} else {
		c.ExcludedPaths = newPaths
	}
	c.rebuildCacheLocked()
}
