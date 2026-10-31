package auth

import (
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Limiter counts failed logins per key (an IP, or a username) in a sliding window
// and locks the key out once it reaches the limit. LAN account keys
// (LANUserKey) get LANAccountFactor times the limit: they exist to stop a
// device on the LAN rotating addresses, not to trip over a mistyped password.
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
	if len(recent) < l.limitFor(key) {
		return true, 0
	}
	return false, recent[0].Add(l.window).Sub(l.now())
}

// LANAccountFactor sets the LAN account limit: 10 failures per address, 50
// per account, per window with the server's defaults.
const LANAccountFactor = 5

func (l *Limiter) limitFor(key string) int {
	if strings.HasPrefix(key, lanUserPrefix) {
		return l.limit * LANAccountFactor
	}
	return l.limit
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

const lanUserPrefix = "lanuser:"

// LANUserKey is the account's bucket for sign-ins from the LAN, kept apart from
// UserKey so failures from the internet can never lock the account at home.
func LANUserKey(username string) string { return lanUserPrefix + strings.ToLower(username) }

// Tailscale hands out addresses from these: 100.64.0.0/10 is shared address
// space (not private, so never LAN) and fd7a:115c:a1e0::/48 is a unique local
// range that IsLAN-style checks would otherwise treat as the home network.
var tailscale = []netip.Prefix{netip.MustParsePrefix("100.64.0.0/10"), netip.MustParsePrefix("fd7a:115c:a1e0::/48")}

// IsTailscale reports whether ip is a Tailscale address.
func IsTailscale(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range tailscale {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// IPKey is the limiter key for a client address. IPv6 clients are keyed by
// their /64, the block one home or one host is handed: a client rotating
// through privacy addresses in it still counts as one. Tailscale IPv6 is the
// exception: every peer in a tailnet shares one /64-like range, and each
// address is a different device, so one bad peer must not lock out the rest.
func IPKey(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return "ip:" + ip
	}
	if a = a.Unmap(); a.Is6() && !IsTailscale(a.String()) {
		if p, err := a.Prefix(64); err == nil {
			return "ip:" + p.String()
		}
	}
	return "ip:" + a.String()
}
