package ratelimit

import (
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	l := New(3, time.Second)
	now := time.Unix(1000, 0)
	l.Prune(now) // not pruned again inside Allow below
	for i := range 3 {
		if !l.Allow("bob", now) {
			t.Fatalf("action %d refused", i+1)
		}
	}
	if l.Allow("bob", now) {
		t.Fatal("fourth action at once allowed")
	}
	if !l.Allow("alice", now) {
		t.Fatal("limits are per key")
	}
	if l.Allow("bob", now.Add(900*time.Millisecond)) {
		t.Fatal("allowed before a refill")
	}
	if !l.Allow("bob", now.Add(1900*time.Millisecond)) || l.Allow("bob", now.Add(1900*time.Millisecond)) {
		t.Fatal("one action per refill")
	}

	// Idle keys are forgotten once their bucket is full again.
	l.Prune(now.Add(2 * time.Second))
	if _, ok := l.buckets["bob"]; !ok || len(l.buckets) != 1 {
		t.Fatalf("buckets %v: only bob's is still not full", l.buckets)
	}
	l.Prune(now.Add(time.Minute))
	if len(l.buckets) != 0 {
		t.Fatalf("%d buckets after a minute", len(l.buckets))
	}
}

func TestLimiterPrunesItself(t *testing.T) {
	l := New(1, time.Second)
	now := time.Unix(1000, 0)
	for _, k := range []string{"a", "b", "c"} {
		l.Allow(k, now)
	}
	l.Allow("d", now.Add(pruneInterval))
	if len(l.buckets) != 1 {
		t.Fatalf("%d buckets, want only d's", len(l.buckets))
	}
}

func TestAddrKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7:4242":           "203.0.113.7",
		"203.0.113.7":                "203.0.113.7",
		"[::ffff:203.0.113.7]:1":     "203.0.113.7",
		"[2001:db8:1:2:3:4:5:6]:443": "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff::1":       "2001:db8:1:2::/64",
		"[fe80::1%eth0]:80":          "fe80::/64",
		"not an address":             "not an address",
	} {
		if got := AddrKey(in); got != want {
			t.Errorf("AddrKey(%q) = %q, want %q", in, got, want)
		}
	}
}
