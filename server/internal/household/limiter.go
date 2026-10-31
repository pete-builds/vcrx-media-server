package household

import (
	"strconv"
	"sync"
	"time"
)

// PIN attempt limits per window. A PIN is four to eight digits, so the
// address limit is tight. The profile's LAN bucket is shared by every device
// at home, so it is wider, as #35 widens the sign-in limiter's LAN account
// bucket: otherwise one device failing five times would keep the right PIN
// out on every other device. Away (off the LAN, Tailscale included) nobody
// should be guessing, so that bucket is as tight as the address.
const (
	PINAddressLimit    = 5
	PINLANProfileLimit = 25
	PINAwayLimit       = 5
	PINWindow          = 15 * time.Minute
)

// Bucket is one counter a PIN attempt is charged to.
type Bucket struct {
	Key   string
	Limit int
}

// AddressBucket, LANProfileBucket and AwayProfileBucket are the buckets a PIN
// attempt counts against. The address key is the sign-in limiter's IPKey.
func AddressBucket(ipKey string) Bucket { return Bucket{"pinaddr:" + ipKey, PINAddressLimit} }

func LANProfileBucket(id int64) Bucket {
	return Bucket{"pinlan:" + strconv.FormatInt(id, 10), PINLANProfileLimit}
}

func AwayProfileBucket(id int64) Bucket {
	return Bucket{"pinaway:" + strconv.FormatInt(id, 10), PINAwayLimit}
}

// Limiter counts PIN attempts in a sliding window. Take checks and records an
// attempt in one locked step, before the PIN is hashed: checking first and
// recording after the hash let concurrent wrong PINs all pass the check while
// each was still being hashed.
type Limiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	window time.Duration
	now    func() time.Time
}

func NewLimiter(window time.Duration) *Limiter {
	return &Limiter{hits: map[string][]time.Time{}, window: window, now: time.Now}
}

// Take charges one attempt to every bucket, or to none when any is full, in
// which case it returns false and how long until the fullest one frees a slot.
func (l *Limiter) Take(buckets ...Bucket) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	var wait time.Duration
	for _, b := range buckets {
		if recent := l.prune(b.Key, now); len(recent) >= b.Limit {
			wait = max(wait, recent[len(recent)-b.Limit].Add(l.window).Sub(now))
		}
	}
	if wait > 0 {
		return false, wait
	}
	for _, b := range buckets {
		l.hits[b.Key] = append(l.hits[b.Key], now)
	}
	return true, 0
}

// Allowed reports whether Take would succeed, recording nothing. It must
// never gate an attempt on its own: that is the race Take exists to close.
func (l *Limiter) Allowed(buckets ...Bucket) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, b := range buckets {
		if len(l.prune(b.Key, l.now())) >= b.Limit {
			return false
		}
	}
	return true
}

// Refund gives back the attempt a successful PIN was charged, so the right
// PIN never uses up a slot. Earlier failures stay counted.
func (l *Limiter) Refund(buckets ...Bucket) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, b := range buckets {
		if ts := l.hits[b.Key]; len(ts) > 0 {
			l.hits[b.Key] = ts[:len(ts)-1]
		}
	}
}

// Reset forgets a bucket, as when an admin sets a new PIN.
func (l *Limiter) Reset(b Bucket) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, b.Key)
}

func (l *Limiter) prune(key string, now time.Time) []time.Time {
	cutoff := now.Add(-l.window)
	ts := l.hits[key]
	i := 0
	for i < len(ts) && !ts[i].After(cutoff) {
		i++
	}
	ts = ts[i:]
	if len(ts) == 0 {
		delete(l.hits, key)
		return nil
	}
	l.hits[key] = ts
	return ts
}
