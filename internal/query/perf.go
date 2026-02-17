package query

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ParquetGlob returns an optimized glob pattern for parquet files
// scoped to a single tenant/namespace partition.
// Path structure: lake/{signal}/tenant={tenant}/namespace={namespace}/year=*/month=*/day=*/hour=*/part-*.parquet
func ParquetGlob(lakeDir, signal, tenant, namespace string, windowMinutes int) string {
	now := time.Now() // Use local time to match lake writer partitions
	start := now.Add(-time.Duration(windowMinutes) * time.Minute)

	filesSet := make(map[string]struct{})

	// For windows > 24h, use day-level globs to reduce filesystem calls
	if windowMinutes > 1440 {
		startDay := start.Truncate(24 * time.Hour)
		endDay := now.Truncate(24 * time.Hour)
		for t := startDay; !t.After(endDay); t = t.Add(24 * time.Hour) {
			dayBase := fmt.Sprintf("%s/%s/tenant=%s/namespace=%s/year=%d/month=%02d/day=%02d",
				lakeDir, signal, tenant, namespace, t.Year(), t.Month(), t.Day())
			// Match hourly part files and day-level compacted files
			for _, pattern := range []string{
				filepath.Join(dayBase, "hour=*/part-*.parquet"),
				filepath.Join(dayBase, "compacted.parquet"),
			} {
				matches, _ := filepath.Glob(pattern)
				for _, m := range matches {
					filesSet[m] = struct{}{}
				}
			}
		}
	} else {
		// For smaller windows, use hour-level precision
		startHour := start.Truncate(time.Hour)
		endHour := now.Truncate(time.Hour)
		for t := startHour; !t.After(endHour); t = t.Add(time.Hour) {
			pattern := fmt.Sprintf("%s/%s/tenant=%s/namespace=%s/year=%d/month=%02d/day=%02d/hour=%02d/part-*.parquet",
				lakeDir, signal, tenant, namespace, t.Year(), t.Month(), t.Day(), t.Hour())
			matches, _ := filepath.Glob(pattern)
			for _, m := range matches {
				filesSet[m] = struct{}{}
			}
		}
	}

	// If there are no files in the window, return a broad glob for the partition.
	// Callers may treat the resulting read_parquet error as "no data yet".
	if len(filesSet) == 0 {
		return sqlQuote(fmt.Sprintf("%s/%s/tenant=%s/namespace=%s/year=*/month=*/day=*/hour=*/part-*.parquet",
			lakeDir, signal, tenant, namespace))
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

// NewCache creates a new cache with the given TTL.
// The cleanup goroutine stops when ctx is cancelled.
func NewCache(ctx context.Context, ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	c := &Cache{
		items: make(map[string]cacheItem),
		ttl:   ttl,
	}
	go c.cleanup(ctx)
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

// cleanup removes expired items periodically (every ttl*2).
// Get() still enforces TTL on every access, so expired items are never
// returned — they just linger in memory until the next cleanup tick.
func (c *Cache) cleanup(ctx context.Context) {
	ticker := time.NewTicker(c.ttl * 2)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.mu.Lock()
			now := time.Now()
			for k, v := range c.items {
				if now.After(v.expiresAt) {
					delete(c.items, k)
				}
			}
			c.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

// QueryCache is the global cache instance, initialized by InitQueryCache.
var QueryCache *Cache

// InitQueryCache initializes the global QueryCache. The cleanup goroutine
// stops when ctx is cancelled.
func InitQueryCache(ctx context.Context) {
	QueryCache = NewCache(ctx, 10*time.Second)
}

// GetCached retrieves a value from QueryCache. Returns (nil, false) if the
// cache is not initialized or the key is missing/expired.
func GetCached(key string) (any, bool) {
	if QueryCache == nil {
		return nil, false
	}
	return QueryCache.Get(key)
}

// SetCached stores a value in QueryCache. No-op if the cache is not initialized.
func SetCached(key string, value any) {
	if QueryCache == nil {
		return
	}
	QueryCache.Set(key, value)
}
