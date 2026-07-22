package auth

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// KeyedLimiter is a bounded-lifetime in-memory limiter for source addresses.
// Account/email cooldowns remain durable in SQLite.
type KeyedLimiter struct {
	mu      sync.Mutex
	entries map[string]*limiterEntry
	limit   rate.Limit
	burst   int
	expiry  time.Duration
}

func NewKeyedLimiter(count int, window time.Duration) *KeyedLimiter {
	return &KeyedLimiter{
		entries: make(map[string]*limiterEntry),
		limit:   rate.Every(window / time.Duration(count)),
		burst:   count,
		expiry:  2 * window,
	}
}

func (l *KeyedLimiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.entries[key]
	if entry == nil {
		entry = &limiterEntry{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.entries[key] = entry
	}
	entry.lastSeen = now
	if len(l.entries) > 1024 {
		for candidate, value := range l.entries {
			if now.Sub(value.lastSeen) > l.expiry {
				delete(l.entries, candidate)
			}
		}
	}
	return entry.limiter.Allow()
}
