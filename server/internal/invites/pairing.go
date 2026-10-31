package invites

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
)

// TV pairing, the device-code flow of OAuth (RFC 8628) without any central
// service: everything happens between the TV, this server and a phone on the
// same network.
//
//  1. The TV asks to pair (POST /api/v1/pair). It gets a long device code it
//     keeps to itself and a short user code it shows, with a QR code of the
//     server's approval page for that code.
//  2. An admin's phone scans the QR code (or the admin types the code under
//     Settings > Devices), sees which TV is asking, picks a scope and
//     approves. Only an admin, on the local network, can do that.
//  3. The TV polls with its device code. Once approved, the poll answers with
//     the TV's session token, once; the pairing is then gone.
//
// Pairings live in memory: they last ten minutes, and a restart only means
// the TV shows a new code. Each user code works once, and the server keeps at
// most MaxPending of them, so nobody can fill its memory.

var (
	// ErrPairingNotFound covers unknown, expired, denied and already used codes.
	ErrPairingNotFound = errors.New("no such pairing code: it may have expired or been used")
	ErrTooManyPairings = errors.New("too many devices are waiting to pair; try again in a few minutes")
)

const (
	PairingTTL = 10 * time.Minute
	// PollInterval is how often the TV should ask whether it was approved.
	PollInterval = 5 * time.Second
	MaxPending   = 64
	// The user code: 8 letters from 20 consonants (no vowels, so no words, and
	// nothing to confuse with a digit), about 34.6 bits, shown as XXXX-XXXX.
	userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"
	userCodeLen      = 8
)

// Pairing is one TV waiting to be approved.
type Pairing struct {
	UserCode  string
	Device    auth.Device
	IP        string
	CreatedAt time.Time
	ExpiresAt time.Time
	// TokenID is set once an admin approved the pairing.
	TokenID int64
	denied  bool
	device  [32]byte
}

// Status of a pairing, as the TV's poll sees it.
const (
	Pending  = "pending"
	Approved = "approved"
)

type Pairings struct {
	mu       sync.Mutex
	byCode   map[string]*Pairing
	byDevice map[[32]byte]*Pairing
	Now      func() time.Time
}

func (p *Pairings) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// prune drops expired pairings. The caller holds mu.
func (p *Pairings) prune() {
	now := p.now()
	for code, x := range p.byCode {
		if !now.Before(x.ExpiresAt) {
			delete(p.byCode, code)
			delete(p.byDevice, x.device)
		}
	}
}

// NormalizeCode uppercases a typed user code and drops anything that is not
// in its alphabet, so "bcdf-ghjk" and "BCDF GHJK" are the same code.
func NormalizeCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if strings.ContainsRune(userCodeAlphabet, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// FormatCode shows a user code as XXXX-XXXX.
func FormatCode(code string) string {
	if len(code) != userCodeLen {
		return code
	}
	return code[:4] + "-" + code[4:]
}

func newUserCode() (string, error) {
	b := make([]byte, userCodeLen)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, userCodeLen)
	for i, x := range b {
		// 256 is not a multiple of 20; rejecting the top of the range keeps
		// every letter equally likely.
		for x >= 240 {
			var one [1]byte
			if _, err := rand.Read(one[:]); err != nil {
				return "", err
			}
			x = one[0]
		}
		out[i] = userCodeAlphabet[int(x)%len(userCodeAlphabet)]
	}
	return string(out), nil
}

func deviceHash(code string) ([32]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(code)
	if err != nil || len(raw) != 32 {
		return [32]byte{}, false
	}
	return sha256.Sum256(raw), true
}

// Start registers a TV and returns its device code, which only the TV holds,
// and the pairing with its user code.
func (p *Pairings) Start(dev auth.Device, ip string) (string, Pairing, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byCode == nil {
		p.byCode, p.byDevice = map[string]*Pairing{}, map[[32]byte]*Pairing{}
	}
	p.prune()
	if len(p.byCode) >= MaxPending {
		return "", Pairing{}, ErrTooManyPairings
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Pairing{}, err
	}
	deviceCode := base64.RawURLEncoding.EncodeToString(raw)
	var code string
	for {
		c, err := newUserCode()
		if err != nil {
			return "", Pairing{}, err
		}
		if p.byCode[c] == nil {
			code = c
			break
		}
	}
	now := p.now()
	x := &Pairing{UserCode: code, Device: dev, IP: ip, CreatedAt: now, ExpiresAt: now.Add(PairingTTL), device: sha256.Sum256(raw)}
	p.byCode[code], p.byDevice[x.device] = x, x
	return deviceCode, *x, nil
}

// Lookup returns a pairing still waiting for approval.
func (p *Pairings) Lookup(code string) (Pairing, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	x := p.byCode[NormalizeCode(code)]
	if x == nil || x.denied || x.TokenID != 0 {
		return Pairing{}, ErrPairingNotFound
	}
	return *x, nil
}

// Approve runs create for a waiting pairing and records the token it made.
// create runs under the lock, so one code can be approved only once even when
// two phones press Approve together.
func (p *Pairings) Approve(code string, create func(Pairing) (int64, error)) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	x := p.byCode[NormalizeCode(code)]
	if x == nil || x.denied || x.TokenID != 0 {
		return ErrPairingNotFound
	}
	id, err := create(*x)
	if err != nil {
		return err
	}
	x.TokenID = id
	return nil
}

// Deny turns a TV away. Its next poll learns the code is gone.
func (p *Pairings) Deny(code string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	x := p.byCode[NormalizeCode(code)]
	if x == nil || x.TokenID != 0 {
		return ErrPairingNotFound
	}
	x.denied = true
	return nil
}

// Poll is the TV asking whether it was approved. Approved, it runs collect
// with the token id, under the lock, and on success forgets the pairing, so
// the device code yields one session and never a second.
func (p *Pairings) Poll(deviceCode string, collect func(Pairing) error) (string, error) {
	h, ok := deviceHash(deviceCode)
	if !ok {
		return "", ErrPairingNotFound
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	x := p.byDevice[h]
	if x == nil {
		return "", ErrPairingNotFound
	}
	if x.denied {
		delete(p.byCode, x.UserCode)
		delete(p.byDevice, h)
		return "", ErrPairingNotFound
	}
	if x.TokenID == 0 {
		return Pending, nil
	}
	if err := collect(*x); err != nil {
		return "", err
	}
	delete(p.byCode, x.UserCode)
	delete(p.byDevice, h)
	return Approved, nil
}
