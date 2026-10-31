package household

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLimiterTakeIsAllOrNothing(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewLimiter(15 * time.Minute)
	l.now = func() time.Time { return now }
	tight, wide := Bucket{"a", 2}, Bucket{"b", 3}
	for i := range 2 {
		if ok, _ := l.Take(tight, wide); !ok {
			t.Fatalf("take %d refused", i)
		}
	}
	ok, wait := l.Take(tight, wide)
	if ok || wait != 15*time.Minute {
		t.Fatalf("third take: %v %v, want refused for 15m", ok, wait)
	}
	// The refused take charged nothing: wide still has one slot.
	if ok, _ := l.Take(wide); !ok {
		t.Fatal("a refused take charged the other bucket")
	}
	if l.Allowed(wide) {
		t.Fatal("wide should be full")
	}
	// A refund gives one slot back; the window frees the rest.
	l.Refund(wide)
	if !l.Allowed(wide) {
		t.Fatal("refund gave nothing back")
	}
	now = now.Add(15*time.Minute + time.Second)
	if ok, _ := l.Take(tight); !ok {
		t.Fatal("window did not expire")
	}
	l.Reset(tight)
	if ok, _ := l.Take(tight, tight); !ok {
		t.Fatal("reset did not clear")
	}
}

// Many takes at once never charge more than the limit.
func TestLimiterTakeIsAtomic(t *testing.T) {
	l := NewLimiter(time.Minute)
	b := Bucket{"k", PINAddressLimit}
	var granted atomic.Int64
	var wg sync.WaitGroup
	for range 500 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Take(b); ok {
				granted.Add(1)
			}
		}()
	}
	wg.Wait()
	if granted.Load() != PINAddressLimit {
		t.Fatalf("granted %d, want %d", granted.Load(), PINAddressLimit)
	}
}
