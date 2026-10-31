package api

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// A linked friend's server may carry a bandwidth cap (milestone 9, item 3).
// The cap is a token bucket per server, shared by every stream it has open,
// so three films at once share the cap rather than getting it three times.
// It is applied here, on the serving side, to whatever the server writes to
// that peer: files, converted streams and HLS segments alike.

type bandwidthBuckets struct {
	mu sync.Mutex
	m  map[int64]*bucket
}

func (b *bandwidthBuckets) get(tokenID, kbps int64) *bucket {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.m == nil {
		b.m = map[int64]*bucket{}
	}
	k := b.m[tokenID]
	rate := float64(kbps) * 1000 / 8 // bytes per second
	if k == nil || k.rate != rate {
		// A tenth of a second's worth at a time keeps the stream smooth
		// and the cap close even over short spans.
		burst := max(512, int(rate/10))
		k = &bucket{rate: rate, burst: burst, avail: float64(burst), last: time.Now()}
		b.m[tokenID] = k
	}
	return k
}

func (b *bandwidthBuckets) writer(ctx context.Context, w http.ResponseWriter, tokenID, kbps int64) http.ResponseWriter {
	return &throttledWriter{ResponseWriter: w, b: b.get(tokenID, kbps), ctx: ctx}
}

type bucket struct {
	mu    sync.Mutex
	rate  float64
	burst int
	avail float64
	last  time.Time
}

// take reserves n bytes and sleeps until the bucket has paid for them. The
// balance may go negative, so callers queue fairly behind each other.
func (k *bucket) take(ctx context.Context, n int) error {
	k.mu.Lock()
	now := time.Now()
	k.avail = min(float64(k.burst), k.avail+now.Sub(k.last).Seconds()*k.rate)
	k.last = now
	k.avail -= float64(n)
	debt := -k.avail
	k.mu.Unlock()
	if debt <= 0 {
		return nil
	}
	t := time.NewTimer(time.Duration(debt / k.rate * float64(time.Second)))
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// throttledWriter paces writes through its bucket. It deliberately has no
// ReadFrom, so http.ServeContent copies through Write and cannot take the
// sendfile path around the cap.
type throttledWriter struct {
	http.ResponseWriter
	b   *bucket
	ctx context.Context
}

func (t *throttledWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		n := min(len(p), t.b.burst)
		if err := t.b.take(t.ctx, n); err != nil {
			return written, err
		}
		m, err := t.ResponseWriter.Write(p[:n])
		written += m
		if err != nil {
			return written, err
		}
		p = p[n:]
	}
	return written, nil
}

func (t *throttledWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }
