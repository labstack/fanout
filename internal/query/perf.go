package query

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ParquetGlob returns an optimized glob pattern for parquet files
// based on the time window. Instead of scanning all partitions,
// it only includes hours that could contain data within the window.
func ParquetGlob(lakeDir, signal string, windowMinutes int) string {
	now := time.Now() // Use local time to match lake writer partitions
	start := now.Add(-time.Duration(windowMinutes) * time.Minute)

	// If window is large (>=24h), fall back to full glob
	if windowMinutes >= 24*60 {
		return sqlQuote(fmt.Sprintf("%s/%s/year=*/month=*/day=*/hour=*/part-*.parquet", lakeDir, signal))
	}

	// Expand hour patterns to actual existing files, otherwise DuckDB errors
	// when any pattern matches zero files.
	startHour := start.Truncate(time.Hour)
	endHour := now.Truncate(time.Hour)

	filesSet := make(map[string]struct{})
	for t := startHour; !t.After(endHour); t = t.Add(time.Hour) {
		pattern := fmt.Sprintf("%s/%s/year=%d/month=%02d/day=%02d/hour=%02d/part-*.parquet",
			lakeDir, signal, t.Year(), t.Month(), t.Day(), t.Hour())
		matches, _ := filepath.Glob(pattern)
		for _, m := range matches {
			filesSet[m] = struct{}{}
		}
	}

	// If there are no files in the window, return a broad glob. Callers may
	// treat the resulting read_parquet error as "no data yet".
	if len(filesSet) == 0 {
		return sqlQuote(fmt.Sprintf("%s/%s/year=*/month=*/day=*/hour=*/part-*.parquet", lakeDir, signal))
	}

	files := make([]string, 0, len(filesSet))
	for f := range filesSet {
		files = append(files, f)
	}
	sort.Strings(files)

	// DuckDB supports list of files (all paths must be quoted).
	if len(files) == 1 {
		return sqlQuote(files[0])
	}
	return fmt.Sprintf("[%s]", strings.Join(wrapQuotes(files), ","))
}

func wrapQuotes(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = sqlQuote(s)
	}
	return out
}

func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// Cache provides a simple TTL cache for query results
type Cache struct {
	mu    sync.RWMutex
	items map[string]cacheItem
	ttl   time.Duration
}

type cacheItem struct {
	value     any
	expiresAt time.Time
}

// NewCache creates a new cache with the given TTL
func NewCache(ttl time.Duration) *Cache {
	c := &Cache{
		items: make(map[string]cacheItem),
		ttl:   ttl,
	}
	go c.cleanup()
	return c
}

// Get retrieves a value from the cache
func (c *Cache) Get(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, ok := c.items[key]
	if !ok || time.Now().After(item.expiresAt) {
		return nil, false
	}
	return item.value, true
}

// Set stores a value in the cache
func (c *Cache) Set(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items[key] = cacheItem{
		value:     value,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// cleanup removes expired items periodically
func (c *Cache) cleanup() {
	ticker := time.NewTicker(c.ttl * 2)
	for range ticker.C {
		c.mu.Lock()
		now := time.Now()
		for k, v := range c.items {
			if now.After(v.expiresAt) {
				delete(c.items, k)
			}
		}
		c.mu.Unlock()
	}
}

// Global cache instance (10 second TTL)
var QueryCache = NewCache(10 * time.Second)
