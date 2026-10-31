package federation

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseBase(t *testing.T) {
	g := Guard{}
	for in, want := range map[string]string{
		"http://192.168.1.20:3750":          "http://192.168.1.20:3750",
		"http://192.168.1.20:3750/":         "http://192.168.1.20:3750",
		"192.168.1.20:3750":                 "http://192.168.1.20:3750",
		"https://vcrx.example.org":          "https://vcrx.example.org",
		"http://100.101.102.103:3750":       "http://100.101.102.103:3750", // Tailscale IPv4
		"http://[fd7a:115c:a1e0::5]:3750":   "http://[fd7a:115c:a1e0::5]:3750",
		"  http://10.0.0.5:3750  ":          "http://10.0.0.5:3750",
		"http://friend-box.tail1234.ts.net": "http://friend-box.tail1234.ts.net",
	} {
		if got, err := g.ParseBase(in); err != nil || got != want {
			t.Errorf("ParseBase(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for in, want := range map[string]error{
		"http://127.0.0.1:3750":                ErrBlockedAddress,
		"http://[::1]:3750":                    ErrBlockedAddress,
		"http://[::ffff:127.0.0.1]:3750":       ErrBlockedAddress,
		"http://localhost:3750":                ErrBlockedAddress,
		"http://vcrx.localhost:3750":           ErrBlockedAddress,
		"http://169.254.169.254":               ErrBlockedAddress,
		"http://[fe80::1]:3750":                ErrBlockedAddress,
		"http://0.0.0.0:3750":                  ErrBlockedAddress,
		"http://0.1.2.3:3750":                  ErrBlockedAddress,
		"http://224.0.0.1:3750":                ErrBlockedAddress,
		"http://255.255.255.255":               ErrBlockedAddress,
		"http://192.168.1.20:3750/api/v1/x":    ErrBadAddress,
		"http://192.168.1.20:3750/?a=b":        ErrBadAddress,
		"http://user:pw@192.168.1.20:3750":     ErrBadAddress,
		"http://192.168.1.20@169.254.169.254/": ErrBadAddress,
		"ftp://192.168.1.20":                   ErrBadAddress,
		"file:///etc/passwd":                   ErrBadAddress,
		"javascript:alert(1)":                  ErrBadAddress,
		"http://":                              ErrBadAddress,
		"http://exa mple.com":                  ErrBadAddress,
		"http://192.168.1.20:99999999":         ErrBadAddress,
		"":                                     ErrBadAddress,
	} {
		if got, err := g.ParseBase(in); !errors.Is(err, want) {
			t.Errorf("ParseBase(%q) = %q, %v; want %v", in, got, err, want)
		}
	}
	// Loopback only when allowed: development, two servers on one machine.
	if got, err := (Guard{AllowLoopback: true}).ParseBase("http://127.0.0.1:3972"); err != nil || got != "http://127.0.0.1:3972" {
		t.Errorf("loopback allowed: %q %v", got, err)
	}
	if _, err := (Guard{AllowLoopback: true}).ParseBase("http://169.254.169.254"); !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("link-local with loopback allowed: %v", err)
	}
}

func TestParseInvite(t *testing.T) {
	g := Guard{}
	secret := strings.Repeat("a", 32)
	base, got, err := g.ParseInvite("http://192.168.1.20:3750/#/link/"+secret, "")
	if err != nil || base != "http://192.168.1.20:3750" || got != secret {
		t.Fatalf("invite link: %q %q %v", base, got, err)
	}
	// An address typed beside it wins: the friend's Tailscale address.
	base, _, err = g.ParseInvite("http://192.168.1.20:3750/#/link/"+secret, "100.101.102.103:3750")
	if err != nil || base != "http://100.101.102.103:3750" {
		t.Fatalf("override: %q %v", base, err)
	}
	for _, bad := range []string{
		"http://192.168.1.20:3750/#/invite/" + secret, // a device invite
		"http://192.168.1.20:3750/#/link/short",
		"http://192.168.1.20:3750/#/link/" + secret + "/../admin",
		"http://192.168.1.20:3750/",
		secret,
	} {
		if _, _, err := g.ParseInvite(bad, ""); !errors.Is(err, ErrBadInvite) {
			t.Errorf("ParseInvite(%q): %v", bad, err)
		}
	}
	if _, _, err := g.ParseInvite("http://169.254.169.254/#/link/"+secret, ""); !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("metadata address in an invite: %v", err)
	}
}

// The guard runs again on the address each connection actually dials, after
// DNS: a name that resolves to loopback is refused even though no address in
// the URL says so.
func TestDialGuardChecksTheResolvedAddress(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	dial := func(g Guard) error {
		tr := NewClient(g, "test").JSON.Transport.(*http.Transport)
		c, err := tr.DialContext(context.Background(), "tcp", net.JoinHostPort("localhost", port))
		if err == nil {
			_ = c.Close()
		}
		return err
	}
	if err := dial(Guard{}); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("dialling a name that resolves to loopback: %v", err)
	}
	// Control: allowed, the same dial connects.
	if err := dial(Guard{AllowLoopback: true}); err != nil {
		t.Fatalf("control: %v", err)
	}
}

func TestClientRefusesOtherPathsAndRedirects(t *testing.T) {
	c := NewClient(Guard{AllowLoopback: true}, "test")
	for _, p := range []string{"/api/v1/../admin", "/api/v1/items/1?x=1", "/api/v1/items/%2e%2e", "/other", "/api/v1//items", "/api/v1/items/1#f"} {
		if _, err := c.URL("http://192.168.1.20:3750", p, nil); !errors.Is(err, ErrBadPath) {
			t.Errorf("URL(%q): %v", p, err)
		}
	}
	if _, err := c.URL("http://192.168.1.20:3750/api", "/api/v1/items/1", nil); !errors.Is(err, ErrBadAddress) {
		t.Errorf("a stored base with a path: %v", err)
	}
	u, err := c.URL("http://192.168.1.20:3750", "/api/v1/items/1", url.Values{"width": {"320"}})
	if err != nil || u != "http://192.168.1.20:3750/api/v1/items/1?width=320" {
		t.Errorf("a good URL: %q %v", u, err)
	}
	var hits atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer other.Close()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/x", http.StatusFound)
	}))
	defer peer.Close()
	err = c.GetJSON(context.Background(), Request{Base: peer.URL, Path: "/api/v1/federation/catalog"}, 1024, &struct{}{})
	var perr *Error
	if !errors.As(err, &perr) || perr.Status != http.StatusFound || hits.Load() != 0 {
		t.Fatalf("a redirect: %v, %d hits elsewhere", err, hits.Load())
	}
}

func TestReadLimitedAndErrors(t *testing.T) {
	if _, err := ReadLimited(strings.NewReader(strings.Repeat("x", 11)), 10); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	if b, err := ReadLimited(strings.NewReader("0123456789"), 10); err != nil || len(b) != 10 {
		t.Fatalf("at the limit: %v", err)
	}
	resp := &http.Response{StatusCode: 403, Body: http.NoBody}
	resp.Body = ioNop(`{"error":"playback_refused","message":"No converting\u0000 for you\u202e."}`)
	var e *Error
	if !errors.As(ReadError(resp), &e) || e.Code != "playback_refused" || e.Message != "No converting for you." {
		t.Fatalf("cleaned error: %+v", e)
	}
	resp.Body = ioNop(`{"error":"<script>","message":""}`)
	if !errors.As(ReadError(resp), &e) || e.Code != "peer_error" || e.Message == "" {
		t.Fatalf("a bad code: %+v", e)
	}
}

func TestItemValidation(t *testing.T) {
	year, bad := int64(1999), int64(-5)
	res := "1080P"
	it := Item{ID: 3, Kind: "movie", Title: "  A\tFilm\x00\u202e ", Year: &year, SeasonNumber: &bad, Resolution: &res}
	if !it.Valid() || it.Title != "A Film" || it.SeasonNumber != nil || *it.Resolution != "1080p" {
		t.Fatalf("cleaned item: %+v", it)
	}
	for _, it := range []Item{{ID: 0, Kind: "movie", Title: "x"}, {ID: 1, Kind: "folder", Title: "x"}, {ID: 1, Kind: "movie", Title: "\x00 "}} {
		if it.Valid() {
			t.Errorf("accepted %+v", it)
		}
	}
	c := Catalog{Items: []Item{{ID: 1, Kind: "movie", Title: "ok"}, {ID: 2, Kind: "episode", Title: "not top level"}, {ID: -1, Kind: "show", Title: "x"}}}
	if dropped := c.Valid(); dropped != 2 || len(c.Items) != 1 {
		t.Fatalf("catalog: %d dropped, %+v", dropped, c.Items)
	}
}

func TestCacheAnswersFastAndKeepsTrying(t *testing.T) {
	var calls atomic.Int64
	block := make(chan struct{})
	c := &Cache{TTL: time.Hour, Fetch: func(ctx context.Context, p Peer) (Catalog, error) {
		calls.Add(1)
		if p.ID == 2 {
			select {
			case <-block:
			case <-ctx.Done():
			}
			return Catalog{}, ctx.Err()
		}
		return Catalog{Items: []Item{{ID: 1, Kind: "movie", Title: "x"}}}, nil
	}}
	defer close(block)
	if st := c.Get(Peer{ID: 1}, time.Second); st.Err != nil || len(st.Catalog.Items) != 1 {
		t.Fatalf("first fetch: %+v", st)
	}
	c.Get(Peer{ID: 1}, time.Second)
	if calls.Load() != 1 {
		t.Fatalf("a fresh catalog was fetched again: %d", calls.Load())
	}
	began := time.Now()
	if st := c.Get(Peer{ID: 2}, 100*time.Millisecond); !st.Pending || time.Since(began) > time.Second {
		t.Fatalf("a hanging peer: %+v after %v", st, time.Since(began))
	}
}

func ioNop(s string) *nopCloser { return &nopCloser{strings.NewReader(s)} }

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }
