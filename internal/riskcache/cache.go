// Package riskcache is an on-disk cache of the entities a risk scan
// resolves for a given (source, query term, limit) combination, so
// re-running an overlapping set of query terms doesn't re-fetch
// identical data from scratch every time -- something this project's
// own usage does constantly (the same handful of org names checked
// over and over across many scans). This is opt-in, not automatic: the
// risk command only consults it when --cache-ttl is set, since this
// tool's whole point is checking *current* public registry state, and
// silently serving stale data by default would work against that.
//
// It deliberately doesn't cache sanctions-screen or full-text-mention
// results -- those change meaningfully day to day in a way that
// matters more (a name added to a sanctions list yesterday should show
// up today), unlike charity/company registration data.
package riskcache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bennett-17/paper-trail/internal/risk"
)

// cacheVersion guards against loading a cache file written by an
// incompatible future format; bump it if the value shape ever changes.
const cacheVersion = 1

type entry struct {
	Entities []risk.Entity `json:"entities"`
	CachedAt time.Time     `json:"cachedAt"`
}

type cacheFile struct {
	Version int              `json:"version"`
	Entries map[string]entry `json:"entries"`
}

// Cache is a pure optimization: a missing, unreadable, or expired
// cache entry is always safe to treat as a miss, never an error, and a
// failed write is non-fatal -- it shouldn't break the scan that
// triggered it.
type Cache struct {
	// Dir is the cache directory, os.UserCacheDir()/paper-trail by
	// default; set to "" to disable caching entirely (every Get is a
	// miss, every Set/Save is a no-op).
	Dir string

	mu      sync.Mutex
	entries map[string]entry
	dirty   bool
	loaded  bool
}

// New builds a Cache using the default OS cache directory. Caching is
// silently disabled if the OS cache directory can't be determined.
func New() *Cache {
	dir := ""
	if d, err := os.UserCacheDir(); err == nil {
		dir = filepath.Join(d, "paper-trail")
	}
	return &Cache{Dir: dir}
}

func (c *Cache) path() string {
	if c.Dir == "" {
		return ""
	}
	return filepath.Join(c.Dir, "risk-scan-cache.json")
}

// load lazily reads the on-disk cache into memory on first use.
func (c *Cache) load() {
	if c.loaded {
		return
	}
	c.loaded = true
	c.entries = map[string]entry{}

	path := c.path()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var file cacheFile
	if err := json.Unmarshal(data, &file); err != nil || file.Version != cacheVersion {
		return
	}
	c.entries = file.Entries
}

// Key builds a cache key from a source name, query term, and the
// --limit in effect when it was fetched -- a result cached at a lower
// limit could be missing candidates a higher limit would need, so the
// limit is part of the key, not just the query.
func Key(source, query string, limit int) string {
	return fmt.Sprintf("%s|%s|%d", source, strings.ToLower(strings.TrimSpace(query)), limit)
}

// Get returns cached entities for key if present and younger than ttl.
func (c *Cache) Get(key string, ttl time.Duration) ([]risk.Entity, bool) {
	if c.Dir == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	e, ok := c.entries[key]
	if !ok || time.Since(e.CachedAt) > ttl {
		return nil, false
	}
	return e.Entities, true
}

// Set stores entities under key, to be persisted on the next Save.
func (c *Cache) Set(key string, entities []risk.Entity) {
	if c.Dir == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.load()
	c.entries[key] = entry{Entities: entities, CachedAt: time.Now()}
	c.dirty = true
}

// Save persists the cache to disk if anything changed since the last
// Save (or since it was loaded).
func (c *Cache) Save() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.dirty {
		return
	}
	path := c.path()
	if path == "" {
		return
	}
	data, err := json.Marshal(cacheFile{Version: cacheVersion, Entries: c.entries})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return
	}
	c.dirty = false
}
