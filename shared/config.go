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
	ExtraDirs       []string `json:"ExtraDirs,omitempty"`
	GPUEnabled      bool     `json:"GPUEnabled"`
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
		ExcludedPaths: []string{},
		GPUEnabled:    false,
		path:         path,
	}

	data, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(data, config)
	} else if oldData, err := os.ReadFile(oldPath); err == nil {
		// Migration path
		var paths []string
		json.Unmarshal(oldData, &paths)
		config.ExcludedPaths = paths
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

// GetExtraDirs returns a copy of the extra directories list.
func (c *AppConfig) GetExtraDirs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, len(c.ExtraDirs))
	copy(out, c.ExtraDirs)
	return out
}

// AddExtraDir adds an arbitrary directory (e.g. network drive) to watch/index.
func (c *AppConfig) AddExtraDir(dir string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	lower := strings.ToLower(dir)
	for _, d := range c.ExtraDirs {
		if strings.ToLower(d) == lower {
			return false // already exists
		}
	}
	c.ExtraDirs = append(c.ExtraDirs, dir)
	return true
}

// RemoveExtraDir removes an extra directory by path.
func (c *AppConfig) RemoveExtraDir(dir string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	lower := strings.ToLower(dir)
	for i, d := range c.ExtraDirs {
		if strings.ToLower(d) == lower {
			c.ExtraDirs = append(c.ExtraDirs[:i], c.ExtraDirs[i+1:]...)
			return true
		}
	}
	return false
}
