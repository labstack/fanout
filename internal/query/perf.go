package query

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ParquetGlob returns an optimized glob pattern for parquet files
// based on the time window. Instead of scanning all partitions,
// it only includes hours that could contain data within the window.
func ParquetGlob(lakeDir, signal string, windowMinutes int) string {
	now := time.Now().UTC()
	start := now.Add(-time.Duration(windowMinutes) * time.Minute)

	// If window is large (>24h), fall back to full glob
	if windowMinutes > 24*60 {
		return fmt.Sprintf("%s/%s/year=*/month=*/day=*/hour=*/part-*.parquet", lakeDir, signal)
	}

	// Collect all hour partitions we need
	var patterns []string
	seen := make(map[string]bool)

	for t := start; !t.After(now); t = t.Add(time.Hour) {
		pattern := fmt.Sprintf("%s/%s/year=%d/month=%02d/day=%02d/hour=%02d/part-*.parquet",
			lakeDir, signal, t.Year(), t.Month(), t.Day(), t.Hour())
		if !seen[pattern] {
			seen[pattern] = true
			patterns = append(patterns, pattern)
		}
	}

	// Also include current hour (in case we're at minute 0)
	pattern := fmt.Sprintf("%s/%s/year=%d/month=%02d/day=%02d/hour=%02d/part-*.parquet",
		lakeDir, signal, now.Year(), now.Month(), now.Day(), now.Hour())
	if !seen[pattern] {
		patterns = append(patterns, pattern)
	}

	// DuckDB supports list of files
	if len(patterns) == 1 {
		return patterns[0]
	}
	return fmt.Sprintf("[%s]", strings.Join(wrapQuotes(patterns), ","))
}

func wrapQuotes(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fmt.Sprintf("'%s'", s)
	}
	return out
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
