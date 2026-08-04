package query

import (
	"context"
	"sync"
	"time"
)

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
