package main

import (
	"testing"
	"time"
)

func TestRateLimiterBurstThenDeny(t *testing.T) {
	rl := newRateLimiter(3, 0.001) // 3 burst, negligible refill
	for i := range 3 {
		if !rl.allow("1.2.3.4") {
			t.Fatalf("call %d: denied within burst", i)
		}
	}
	if rl.allow("1.2.3.4") {
		t.Fatal("4th call allowed, want denied")
	}
	if !rl.allow("5.6.7.8") {
		t.Fatal("different key denied, want allowed")
	}
}

func TestRateLimiterRefill(t *testing.T) {
	rl := newRateLimiter(1, 100) // refills fast: 100 tokens/sec
	if !rl.allow("k") {
		t.Fatal("first call denied")
	}
	if rl.allow("k") {
		t.Fatal("second immediate call allowed")
	}
	time.Sleep(30 * time.Millisecond) // ~3 tokens refilled, capped at burst 1
	if !rl.allow("k") {
		t.Fatal("call after refill denied")
	}
}

func TestRateLimiterSweep(t *testing.T) {
	rl := newRateLimiter(1, 1)
	rl.allow("old")
	rl.sweep(0) // everything older than now is dropped
	rl.mu.Lock()
	n := len(rl.visitors)
	rl.mu.Unlock()
	if n != 0 {
		t.Fatalf("visitors after sweep = %d, want 0", n)
	}
}
