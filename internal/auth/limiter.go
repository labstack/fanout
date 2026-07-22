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

// KeyedLimiter is a bounded-lifetime in-memory limiter for source-derived bucket keys.
// Account/email cooldowns remain durable in SQLite.
type KeyedLimiter struct {
	mu        sync.Mutex
	entries   map[string]*limiterEntry
	limit     rate.Limit
	burst     int
	expiry    time.Duration
	nextSweep time.Time
	maxKeys   int
	now       func() time.Time
}

func NewKeyedLimiter(count int, window time.Duration) *KeyedLimiter {
	return &KeyedLimiter{
		entries: make(map[string]*limiterEntry),
		limit:   rate.Every(window / time.Duration(count)),
		burst:   count,
		expiry:  2 * window,
		maxKeys: 4096,
		now:     time.Now,
	}
}

func (l *KeyedLimiter) Allow(key string) bool {
	now := l.now()
	if key == "" {
		key = "<unknown>"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nextSweep.IsZero() || !now.Before(l.nextSweep) {
		for candidate, value := range l.entries {
			if now.Sub(value.lastSeen) > l.expiry {
				delete(l.entries, candidate)
			}
		}
		l.nextSweep = now.Add(l.expiry / 2)
	}
	entry := l.entries[key]
	if entry == nil {
		if len(l.entries) >= l.maxKeys {
			oldestKey := ""
			var oldest time.Time
			for candidate, value := range l.entries {
				if oldestKey == "" || value.lastSeen.Before(oldest) {
					oldestKey, oldest = candidate, value.lastSeen
				}
			}
			delete(l.entries, oldestKey)
		}
		entry = &limiterEntry{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.entries[key] = entry
	}
	entry.lastSeen = now
	return entry.limiter.Allow()
}
