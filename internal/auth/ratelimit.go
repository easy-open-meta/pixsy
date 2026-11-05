package auth

import (
	"sort"
	"sync"
	"time"
)

// Limiter is a per-key token bucket kept in memory (§3.2: 5 requests/minute
// for login and register).
type Limiter struct {
	limit  float64
	period time.Duration

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

// maxBuckets bounds the map between sweeps: an attacker rotating keys at high
// frequency cannot grow memory unboundedly inside one cleanup period.
const maxBuckets = 10000

type bucket struct {
	tokens float64
	seen   time.Time
}

// NewLimiter allows limit requests per period for each key.
func NewLimiter(limit int, period time.Duration) *Limiter {
	if limit <= 0 {
		limit = 1
	}
	if period <= 0 {
		period = time.Minute
	}
	return &Limiter{
		limit:     float64(limit),
		period:    period,
		buckets:   make(map[string]*bucket),
		lastSweep: time.Now(),
	}
}

// Allow consumes one token for key, reporting whether the call is allowed.
func (l *Limiter) Allow(key string) bool {
	return l.AllowAt(key, time.Now())
}

// AllowAt is Allow with an injectable clock (used by tests).
func (l *Limiter) AllowAt(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: l.limit - 1, seen: now}
		return true
	}
	// Refill proportionally to the elapsed time.
	elapsed := now.Sub(b.seen)
	if elapsed > 0 {
		b.tokens += (float64(elapsed) / float64(l.period)) * l.limit
		if b.tokens > l.limit {
			b.tokens = l.limit
		}
		b.seen = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops buckets that have fully refilled, keeping memory bounded. When
// the map still exceeds maxBuckets (all buckets are fresh), the oldest half is
// evicted so a key-rotation flood cannot grow memory without limit.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.period && len(l.buckets) < maxBuckets {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if now.Sub(b.seen) >= l.period {
			delete(l.buckets, k)
		}
	}
	if len(l.buckets) < maxBuckets {
		return
	}
	// 收集并按 seen 淘汰最旧的一半；极端情况下才走到这里，O(n log n) 可接受。
	type kv struct {
		k string
		b *bucket
	}
	all := make([]kv, 0, len(l.buckets))
	for k, b := range l.buckets {
		all = append(all, kv{k, b})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].b.seen.Before(all[j].b.seen) })
	for _, e := range all[:len(all)/2] {
		delete(l.buckets, e.k)
	}
}
