package podcasts

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestCheckIPRefusesEveryNonPublicAddress(t *testing.T) {
	t.Parallel()
	refused := []string{
		"127.0.0.1", "127.8.9.10", "10.9.9.9", "172.16.0.1", "172.31.255.255", "192.168.1.1", "169.254.169.254", "100.64.0.1",
		"100.127.255.255", "224.0.0.1", "239.255.255.250", "255.255.255.255", "0.0.0.0", "198.18.0.1", "192.0.0.8",
		"::1", "::", "fe80::1", "fc00::1", "fd12:3456::1", "ff02::1", "::ffff:127.0.0.1", "::ffff:10.0.0.5", "64:ff9b::7f00:1",
		"2002:7f00:1::1", "fec0::1",
	}
	for _, s := range refused {
		if err := CheckIP(netip.MustParseAddr(s)); !Refused(err) {
			t.Errorf("%s: %v, want refused", s, err)
		}
	}
	for _, s := range []string{"203.0.113.10", "198.51.100.7", "2001:db8::1"} {
		if err := CheckIP(netip.MustParseAddr(s)); err != nil {
			t.Errorf("%s: %v, want allowed", s, err)
		}
	}
}

func TestParseURLRefusals(t *testing.T) {
	t.Parallel()
	g := Guard{}
	for _, raw := range []string{"ftp://feeds.example.com/x.xml", "file:///etc/passwd", "gopher://feeds.example.com/", "javascript:alert(1)",
		"https://user:pass@feeds.example.com/x.xml", "http://localhost/feed", "http://router/feed", "http://127.0.0.1/feed",
		"http://[::1]:8080/feed", "http://10.0.0.5/feed", "http://169.254.169.254/latest/meta-data/", "https://", "not a url",
		"http://app.localhost/feed", "https://feeds.example.com/" + strings.Repeat("a", 3000)} {
		if _, err := g.ParseURL(raw); !Refused(err) {
			t.Errorf("%q: %v, want refused", raw, err)
		}
	}
	u, err := g.ParseURL("  https://feeds.example.com/show.xml#top ")
	if err != nil || u.String() != "https://feeds.example.com/show.xml" {
		t.Fatalf("a public URL: %v %v", u, err)
	}
}

// A host name that passes the URL check can still resolve, or rebind, to
// the LAN: the dial-time check sees the address really dialed.
func TestDialCheckRefusesWhatANameResolvesTo(t *testing.T) {
	t.Parallel()
	g := Guard{}
	for _, dialed := range []string{"127.0.0.1:80", "10.9.9.9:443", "[::1]:443", "[fe80::1%en0]:80", "169.254.169.254:80"} {
		if err := g.dialCheck("feeds.example.com:443", dialed); !Refused(err) {
			t.Errorf("feeds.example.com resolving to %s: %v, want refused", dialed, err)
		}
	}
	if err := g.dialCheck("feeds.example.com:443", "203.0.113.10:443"); err != nil {
		t.Errorf("a public address: %v", err)
	}
}

func get(t *testing.T, c *http.Client, u string) (*http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c.Do(req)
}

func TestClientRefusesLoopbackAtDialTime(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "secret") }))
	defer srv.Close()
	// Straight to the loopback test server: refused by the client itself,
	// without the URL check that ParseURL would have done first.
	if resp, err := get(t, Guard{}.Client(time.Second), srv.URL); !Refused(err) {
		if resp != nil {
			_ = resp.Body.Close()
		}
		t.Fatalf("loopback fetch: %v, want refused", err)
	}
	// The test-only allowance lets it through, which shows the refusal is the guard's.
	host := strings.TrimPrefix(srv.URL, "http://")
	resp, err := get(t, Guard{AllowHosts: []string{host}}.Client(time.Second), srv.URL)
	if err != nil {
		t.Fatalf("allowed host: %v", err)
	}
	_ = resp.Body.Close()
}

func TestClientRefusesARedirectToLoopback(t *testing.T) {
	t.Parallel()
	inner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "lan secret") }))
	defer inner.Close()
	outer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/to-ip":
			http.Redirect(w, r, inner.URL+"/admin", http.StatusFound)
		case "/to-localhost":
			http.Redirect(w, r, "http://localhost:"+inner.URL[strings.LastIndex(inner.URL, ":")+1:]+"/admin", http.StatusFound)
		case "/to-file":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		}
	}))
	defer outer.Close()
	// The outer server stands in for a public host: allowed by name. The
	// inner one is not, so following the redirect must be refused.
	g := Guard{AllowHosts: []string{strings.TrimPrefix(outer.URL, "http://")}}
	c := g.Client(time.Second)
	for _, p := range []string{"/to-ip", "/to-localhost", "/to-file", "/loop"} {
		resp, err := get(t, c, outer.URL+p)
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err == nil {
			t.Errorf("%s: followed to %s", p, resp.Request.URL)
			continue
		}
		if !Refused(err) {
			t.Errorf("%s: %v, want refused", p, err)
		}
	}
}
