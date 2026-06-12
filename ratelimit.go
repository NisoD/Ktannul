package main

import (
	"sync"
	"time"
)

// rateLimiter is a per-key token bucket: each key starts with `burst`
// tokens and refills at `perSec` tokens per second, capped at burst.
type rateLimiter struct {
	mu       sync.Mutex
	burst    float64
	perSec   float64
	visitors map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(burst int, perSec float64) *rateLimiter {
	return &rateLimiter{burst: float64(burst), perSec: perSec, visitors: map[string]*bucket{}}
}

func (rl *rateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.visitors[key]
	if !ok {
		rl.visitors[key] = &bucket{tokens: rl.burst - 1, last: now}
		return true
	}
	b.tokens += now.Sub(b.last).Seconds() * rl.perSec
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops keys idle longer than olderThan so the map can't grow forever.
func (rl *rateLimiter) sweep(olderThan time.Duration) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-olderThan)
	for k, b := range rl.visitors {
		if b.last.Before(cutoff) {
			delete(rl.visitors, k)
		}
	}
}
