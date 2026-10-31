package auth

import (
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Limiter counts failed logins per key (an IP, or a username) in a sliding window
// and locks the key out once it reaches the limit.
type Limiter struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	limit    int
	window   time.Duration
	now      func() time.Time
}

func NewLimiter(limit int, window time.Duration) *Limiter {
	return &Limiter{failures: map[string][]time.Time{}, limit: limit, window: window, now: time.Now}
}

// Allowed reports whether key may try again, and if not, how long until it may.
func (l *Limiter) Allowed(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.prune(key)
	if len(recent) < l.limit {
		return true, 0
	}
	return false, recent[0].Add(l.window).Sub(l.now())
}

func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[key] = append(l.prune(key), l.now())
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

func (l *Limiter) prune(key string) []time.Time {
	cutoff := l.now().Add(-l.window)
	ts := l.failures[key]
	i := 0
	for i < len(ts) && !ts[i].After(cutoff) {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(l.failures, key)
		return nil
	}
	l.failures[key] = ts
	return ts
}

// UserKey normalizes a username into a limiter key, case-insensitively like the
// users table.
func UserKey(username string) string { return "user:" + strings.ToLower(username) }

// IPKey is the limiter key for a client address. IPv6 clients are keyed by
// their /64, the block one home or one host is handed: a client rotating
// through privacy addresses in it still counts as one.
func IPKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return "ip:" + ip
	}
	if a = a.Unmap(); a.Is6() {
		if p, err := a.Prefix(64); err == nil {
			return "ip:" + p.String()
		}
	}
	return "ip:" + a.String()
}
