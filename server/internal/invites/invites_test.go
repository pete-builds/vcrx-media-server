package invites

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
)

func TestSecretsAreLongRandomAndHashed(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		s, err := NewSecret()
		if err != nil {
			t.Fatal(err)
		}
		if seen[s] {
			t.Fatalf("secret %q repeated", s)
		}
		seen[s] = true
		h, ok := HashSecret(s)
		if !ok || len(h) != 32 {
			t.Fatalf("hash of %q: %x %v", s, h, ok)
		}
		// Case, dashes and spaces do not change which secret it is.
		h2, _ := HashSecret(strings.ToUpper(s[:5]) + "-" + s[5:10] + " " + s[10:])
		if string(h) != string(h2) {
			t.Fatalf("reformatted %q hashes differently", s)
		}
	}
	if SecretBytes*8 < 128 {
		t.Fatalf("secrets carry %d bits, want at least 128", SecretBytes*8)
	}
	// Anything that is not a whole secret has no hash, so it matches nothing.
	for _, bad := range []string{"", "abc", strings.Repeat("a", 31), strings.Repeat("a", 33), strings.Repeat("1", 32), strings.Repeat("a", 200)} {
		if _, ok := HashSecret(bad); ok {
			t.Errorf("%q hashed", bad)
		}
	}
}

func TestUserCodes(t *testing.T) {
	for range 500 {
		c, err := newUserCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(c) != userCodeLen || NormalizeCode(c) != c {
			t.Fatalf("code %q", c)
		}
	}
	if got := NormalizeCode(" bcdf-ghjk "); got != "BCDFGHJK" {
		t.Errorf("normalized %q", got)
	}
	if got := FormatCode("BCDFGHJK"); got != "BCDF-GHJK" {
		t.Errorf("formatted %q", got)
	}
}

// A pairing lasts ten minutes, is approved once, collected once, and a denied
// one is gone at the TV's next poll.
func TestPairingLifecycle(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	p := &Pairings{Now: func() time.Time { return now }}
	dc, x, err := p.Start(auth.Device{Name: "Den TV"}, "192.168.1.9")
	if err != nil {
		t.Fatal(err)
	}
	if st, err := p.Poll(dc, nil); st != Pending || err != nil {
		t.Fatalf("poll: %q %v", st, err)
	}
	calls := 0
	create := func(Pairing) (int64, error) { calls++; return 7, nil }
	if err := p.Approve(strings.ToLower(x.UserCode), create); err != nil {
		t.Fatal(err)
	}
	if err := p.Approve(x.UserCode, create); !errors.Is(err, ErrPairingNotFound) || calls != 1 {
		t.Fatalf("second approval: %v, create ran %d times", err, calls)
	}
	if _, err := p.Lookup(x.UserCode); !errors.Is(err, ErrPairingNotFound) {
		t.Fatalf("lookup of an approved code: %v", err)
	}
	var got int64
	if st, err := p.Poll(dc, func(x Pairing) error { got = x.TokenID; return nil }); st != Approved || err != nil || got != 7 {
		t.Fatalf("collect: %q %v %d", st, err, got)
	}
	if _, err := p.Poll(dc, func(Pairing) error { t.Fatal("collected twice"); return nil }); !errors.Is(err, ErrPairingNotFound) {
		t.Fatalf("second collect: %v", err)
	}
	// A failed collect keeps the pairing, so the TV can try again.
	dc2, x2, _ := p.Start(auth.Device{}, "192.168.1.9")
	_ = p.Approve(x2.UserCode, create)
	if _, err := p.Poll(dc2, func(Pairing) error { return errors.New("database busy") }); err == nil {
		t.Fatal("a failed collect reported success")
	}
	if st, _ := p.Poll(dc2, func(Pairing) error { return nil }); st != Approved {
		t.Fatalf("retry after a failed collect: %q", st)
	}
	// Expiry.
	dc3, x3, _ := p.Start(auth.Device{}, "192.168.1.9")
	now = now.Add(PairingTTL)
	if _, err := p.Lookup(x3.UserCode); !errors.Is(err, ErrPairingNotFound) {
		t.Fatalf("lookup at expiry: %v", err)
	}
	if _, err := p.Poll(dc3, nil); !errors.Is(err, ErrPairingNotFound) {
		t.Fatalf("poll at expiry: %v", err)
	}
	// Deny.
	dc4, x4, _ := p.Start(auth.Device{}, "192.168.1.9")
	if err := p.Deny(x4.UserCode); err != nil {
		t.Fatal(err)
	}
	if err := p.Approve(x4.UserCode, create); !errors.Is(err, ErrPairingNotFound) {
		t.Fatalf("approve after deny: %v", err)
	}
	if _, err := p.Poll(dc4, nil); !errors.Is(err, ErrPairingNotFound) {
		t.Fatalf("poll after deny: %v", err)
	}
	// A made-up device code is unknown, not a crash.
	for _, bad := range []string{"", "x", strings.Repeat("A", 43)} {
		if _, err := p.Poll(bad, nil); !errors.Is(err, ErrPairingNotFound) {
			t.Fatalf("poll %q: %v", bad, err)
		}
	}
}

// The table of waiting TVs is bounded.
func TestPairingTableIsBounded(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	p := &Pairings{Now: func() time.Time { return now }}
	for i := range MaxPending {
		if _, _, err := p.Start(auth.Device{}, "192.168.1.9"); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if _, _, err := p.Start(auth.Device{}, "192.168.1.9"); !errors.Is(err, ErrTooManyPairings) {
		t.Fatalf("one past the limit: %v", err)
	}
	now = now.Add(PairingTTL)
	if _, _, err := p.Start(auth.Device{}, "192.168.1.9"); err != nil {
		t.Fatalf("after the old ones expired: %v", err)
	}
}

func TestQRCodeIsAPNG(t *testing.T) {
	b, err := QRPNG("http://192.168.1.10:8096/#/pair/BCDFGHJK")
	if err != nil || len(b) < 8 || string(b[1:4]) != "PNG" {
		t.Fatalf("qr: %v %q", err, b[:min(8, len(b))])
	}
}
