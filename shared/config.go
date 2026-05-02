package shared

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// AppConfig is the persisted application configuration.
//
// The exported fields are serialized to filosophy_config.json. The unexported
// normalized slices are rebuilt after every mutation so path checks stay cheap
// during large indexing walks.
type AppConfig struct {
	ExcludedPaths []string `json:"ExcludedPaths"`
	IncludedDirs  []string `json:"IncludedDirs,omitempty"`
	PathOnlyDirs  []string `json:"PathOnlyDirs,omitempty"`
	ExtraDirs     []string `json:"ExtraDirs,omitempty"`
	GPUEnabled    bool     `json:"GPUEnabled"`
	HasGPU        bool     `json:"HasGPU"`
	HasCheckedGPU bool     `json:"HasCheckedGPU"`

	// External REST API
	APIEnabled bool   `json:"APIEnabled"`
	APIPort    int    `json:"APIPort"`
	APIKey     string `json:"APIKey"`

	// MCP SSE server (shares APIPort, separate key)
	MCPEnabled bool   `json:"MCPEnabled"`
	MCPKey     string `json:"MCPKey"`

	path                   string
	mu                     sync.RWMutex
	normalizedPaths        []string `json:"-"`
	normalizedIncludedDirs []string `json:"-"`
	normalizedPathOnlyDirs []string `json:"-"`
}

// DefaultIndexedFolderNames names the home-directory folders enabled for
// indexing when no user-selected include list exists yet.
var DefaultIndexedFolderNames = []string{"Desktop", "Documents", "Downloads"}

// DefaultIndexedDirs expands DefaultIndexedFolderNames beneath home.
func DefaultIndexedDirs(home string) []string {
	dirs := make([]string, 0, len(DefaultIndexedFolderNames))
	for _, name := range DefaultIndexedFolderNames {
		dirs = append(dirs, filepath.Join(home, name))
	}
	return dirs
}

// rebuildCacheLocked refreshes normalized path lookup caches.
//
// Callers must hold c.mu. Paths are stored in slash form with a trailing slash
// so descendant checks can be made with simple prefix comparisons.
func (c *AppConfig) rebuildCacheLocked() {
	c.normalizedPaths = make([]string, len(c.ExcludedPaths))
	for i, p := range c.ExcludedPaths {
		v := strings.ToLower(filepath.ToSlash(p))
		if !strings.HasSuffix(v, "/") {
			v += "/"
		}
		c.normalizedPaths[i] = v
	}
	c.normalizedIncludedDirs = make([]string, len(c.IncludedDirs))
	for i, p := range c.IncludedDirs {
		v := strings.ToLower(filepath.ToSlash(p))
		if !strings.HasSuffix(v, "/") {
			v += "/"
		}
		c.normalizedIncludedDirs[i] = v
	}
	c.normalizedPathOnlyDirs = make([]string, len(c.PathOnlyDirs))
	for i, p := range c.PathOnlyDirs {
		v := strings.ToLower(filepath.ToSlash(p))
		if !strings.HasSuffix(v, "/") {
			v += "/"
		}
		c.normalizedPathOnlyDirs[i] = v
	}
}

// LoadConfig reads filosophy_config.json from dir, applies defaults, and
// migrates the old filosophy_excluded.json file when present.
//
// Invalid or missing config files fall back to defaults so app startup is not
// blocked by a corrupt local settings file.
func LoadConfig(dir string) *AppConfig {
	path := filepath.Join(dir, "filosophy_config.json")
	// Try to migrate from old excluded config if it exists
	oldPath := filepath.Join(dir, "filosophy_excluded.json")

	config := &AppConfig{
		ExcludedPaths: []string{".git", "node_modules", "anaconda3", "miniconda3"},
		GPUEnabled:    false,
		HasGPU:        false,
		HasCheckedGPU: false,
		APIEnabled:    false,
		APIPort:       7700,
		path:          path,
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

// EnsureDefaultIncludedDirs initializes the include list to the standard home
// folders the first time the app runs.
func (c *AppConfig) EnsureDefaultIncludedDirs(home string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.IncludedDirs) > 0 {
		c.rebuildCacheLocked()
		return
	}
	c.IncludedDirs = DefaultIndexedDirs(home)
	c.rebuildCacheLocked()
}

// Save de-duplicates, sorts, and writes the config file.
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

	unique = make(map[string]bool)
	var included []string
	for _, p := range c.IncludedDirs {
		l := strings.ToLower(p)
		if !unique[l] {
			unique[l] = true
			included = append(included, p)
		}
	}
	sort.Strings(included)
	c.IncludedDirs = included
	c.rebuildCacheLocked()

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0644)
}

// IsExcluded reports whether path is exactly excluded or is inside an excluded
// directory.
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

// IsIncluded reports whether path is exactly included or is inside an included
// directory.
func (c *AppConfig) IsIncluded(path string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	lower := strings.ToLower(filepath.ToSlash(path))
	for _, included := range c.normalizedIncludedDirs {
		if lower == strings.TrimSuffix(included, "/") || strings.HasPrefix(lower, included) {
			return true
		}
	}
	return false
}

// SetIncluded adds or removes path from the content indexing include list.
func (c *AppConfig) SetIncluded(path string, included bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	lower := strings.ToLower(path)
	newDirs := []string{}
	found := false
	for _, p := range c.IncludedDirs {
		if strings.ToLower(p) == lower {
			found = true
			if included {
				newDirs = append(newDirs, p)
			}
		} else {
			newDirs = append(newDirs, p)
		}
	}
	if included && !found {
		newDirs = append(newDirs, path)
	}
	c.IncludedDirs = newDirs
	c.rebuildCacheLocked()
}

// IsPathOnly returns true if path is under a path-only directory.
// Files in path-only directories are indexed for path/filename search only —
// content extraction and semantic embedding are skipped.
func (c *AppConfig) IsPathOnly(path string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	lower := strings.ToLower(filepath.ToSlash(path))
	for _, po := range c.normalizedPathOnlyDirs {
		if lower == strings.TrimSuffix(po, "/") || strings.HasPrefix(lower, po) {
			return true
		}
	}
	return false
}

// SetPathOnly adds or removes a directory from the path-only list.
func (c *AppConfig) SetPathOnly(path string, pathOnly bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	lower := strings.ToLower(path)
	var newDirs []string
	found := false
	for _, p := range c.PathOnlyDirs {
		if strings.ToLower(p) == lower {
			found = true
			if pathOnly {
				newDirs = append(newDirs, p)
			}
			// if !pathOnly, omit it (removes from list)
		} else {
			newDirs = append(newDirs, p)
		}
	}
	if pathOnly && !found {
		newDirs = append(newDirs, path)
	}
	c.PathOnlyDirs = newDirs
	c.rebuildCacheLocked()
}

// SetExcluded adds or removes path from the exclusion list.
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

	c.ExcludedPaths = newPaths
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
