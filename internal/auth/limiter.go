package auth

import (
	"container/list"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type limiterEntry struct {
	key      string
	limiter  *rate.Limiter
	lastSeen time.Time
	element  *list.Element
}

// KeyedLimiter is a bounded-lifetime in-memory limiter for source-derived bucket keys.
// Account/email cooldowns remain durable in SQLite.
type KeyedLimiter struct {
	mu                sync.Mutex
	entries           map[string]*limiterEntry
	recency           *list.List
	limit             rate.Limit
	burst             int
	expiry            time.Duration
	nextSweep         time.Time
	maxKeys           int
	now               func() time.Time
	lastSaturationLog time.Time
}

func NewKeyedLimiter(count int, window time.Duration) *KeyedLimiter {
	return &KeyedLimiter{
		entries: make(map[string]*limiterEntry),
		recency: list.New(),
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

	// The list is ordered by last use, so expiry cleanup touches only entries
	// that are actually removed instead of scanning the whole map.
	if l.nextSweep.IsZero() || !now.Before(l.nextSweep) {
		for element := l.recency.Back(); element != nil; {
			entry := element.Value.(*limiterEntry)
			if now.Sub(entry.lastSeen) <= l.expiry {
				break
			}
			previous := element.Prev()
			delete(l.entries, entry.key)
			l.recency.Remove(element)
			element = previous
		}
		l.nextSweep = now.Add(l.expiry / 2)
	}

	entry := l.entries[key]
	if entry == nil {
		if len(l.entries) >= l.maxKeys {
			oldest := l.recency.Back()
			// Never evict a throttled bucket. When capacity is exhausted by
			// active buckets, fail closed for new attacker-controlled keys.
			if oldest == nil || oldest.Value.(*limiterEntry).limiter.TokensAt(now) < float64(l.burst) {
				if l.lastSaturationLog.IsZero() || now.Sub(l.lastSaturationLog) >= time.Minute {
					slog.Warn("authentication rate limiter saturated", "max_keys", l.maxKeys)
					l.lastSaturationLog = now
				}
				return false
			}
			evicted := oldest.Value.(*limiterEntry)
			delete(l.entries, evicted.key)
			l.recency.Remove(oldest)
		}
		entry = &limiterEntry{key: key, limiter: rate.NewLimiter(l.limit, l.burst)}
		entry.element = l.recency.PushFront(entry)
		l.entries[key] = entry
	} else {
		l.recency.MoveToFront(entry.element)
	}
	entry.lastSeen = now
	return entry.limiter.AllowN(now, 1)
}
