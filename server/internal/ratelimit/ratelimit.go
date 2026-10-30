// Package ratelimit limits how often something may happen per key: chat
// messages per user, and requests per client address.
package ratelimit

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

// pruneInterval is how often Allow forgets keys that are idle again.
const pruneInterval = 5 * time.Minute

// Limiter is a token bucket per key: burst actions at once, then one more
// every refill.
type Limiter struct {
	burst  float64
	refill time.Duration

	mu      sync.Mutex
	buckets map[string]*bucket
	pruned  time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

func New(burst int, refill time.Duration) *Limiter {
	return &Limiter{burst: float64(burst), refill: refill, buckets: map[string]*bucket{}}
}

// Refill is how long the limiter takes to allow one more action.
func (l *Limiter) Refill() time.Duration { return l.refill }

func (l *Limiter) fill(b *bucket, now time.Time) {
	if now.After(b.at) {
		b.tokens = min(l.burst, b.tokens+float64(now.Sub(b.at))/float64(l.refill))
		b.at = now
	}
}

// Allow reports whether key may act now, and counts it if so. Every few
// minutes it also forgets idle keys.
func (l *Limiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.pruned) >= pruneInterval {
		l.pruneLocked(now)
	}
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, at: now}
		l.buckets[key] = b
	}
	l.fill(b, now)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Prune forgets keys whose bucket has filled up again.
func (l *Limiter) Prune(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneLocked(now)
}

func (l *Limiter) pruneLocked(now time.Time) {
	l.pruned = now
	for k, b := range l.buckets {
		if l.fill(b, now); b.tokens >= l.burst {
			delete(l.buckets, k)
		}
	}
}

// AddrKey is the key of a client address ("host:port" or a bare IP): the IP
// itself for IPv4, and its /64 network for IPv6, since one home or phone
// gets a whole /64 and can pick any address in it.
func AddrKey(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return addr
	}
	ip = ip.Unmap()
	if ip.Is4() {
		return ip.String()
	}
	p, _ := ip.WithZone("").Prefix(64)
	return p.String()
}
