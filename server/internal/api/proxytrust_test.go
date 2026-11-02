package api

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

// A reverse proxy that is not listed in trusted_proxies must not lend its own
// address to the clients behind it. The case that matters: Caddy on the host,
// proxying to the host's LAN address, connects from an address inside
// trusted_networks. Read as the client, that address would hand everyone on
// the internet the profile picker (sign-in without a password) and the
// LAN-only admin routes. A request that carries any forwarding header from an
// unlisted peer has an unknown client instead, which is never LAN and never
// trusted.
func TestUnlistedProxyClientIsUnknown(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	lan, _ := netip.ParsePrefix("192.168.0.0/16")
	f.srv.AutoSignInUser, f.srv.TrustedNetworks = "pete", []netip.Prefix{lan}
	f.h = f.srv.Handler()
	dev := map[string]any{"device": map[string]string{"id": "web-1", "name": "Browser", "platform": "web", "client": "vcrx-web", "client_version": "1"}}
	const proxy = "192.168.1.20:41000"

	// Control: the same peer with no forwarding header is a LAN client.
	if r := f.do("POST", "/api/v1/auth/auto", "", dev, "RemoteAddr", proxy); r.code != 200 {
		t.Fatalf("control, direct LAN client: %d %s", r.code, r.body)
	}
	if r := f.do("GET", "/api/v1/admin/users", f.adminTok, nil, "RemoteAddr", proxy); r.code != 200 {
		t.Fatalf("control, admin on the LAN: %d %s", r.code, r.body)
	}

	headers := [][2]string{
		{"X-Forwarded-For", "8.8.8.8"},
		{"X-Forwarded-For", "192.168.1.77"}, // a spoofed LAN hop is no better
		{"X-Real-IP", "8.8.8.8"},
		{"Forwarded", "for=8.8.8.8;proto=https"},
		{"X-Forwarded-Proto", "https"},
		{"X-Forwarded-Host", "watch.example.com"},
	}
	for _, peer := range []string{proxy, "[::ffff:192.168.1.20]:41000"} {
		for _, h := range headers {
			name := peer + " " + h[0] + ": " + h[1]
			if r := f.do("POST", "/api/v1/auth/auto", "", dev, "RemoteAddr", peer, h[0], h[1]); r.code != 403 {
				t.Errorf("%s: auto sign-in %d, want 403", name, r.code)
			}
			if r := f.do("GET", "/api/v1/server", "", nil, "RemoteAddr", peer, h[0], h[1]); !strings.Contains(string(r.body), `"auto_sign_in":false`) ||
				!strings.Contains(string(r.body), `"profile_picker":false`) {
				t.Errorf("%s: server info %s", name, r.body)
			}
			if r := f.do("GET", "/api/v1/profiles", "", nil, "RemoteAddr", peer, h[0], h[1]); r.code != 401 {
				t.Errorf("%s: profile picker %d, want 401", name, r.code)
			}
			if r := f.do("GET", "/api/v1/admin/users", f.adminTok, nil, "RemoteAddr", peer, h[0], h[1]); r.code != 403 {
				t.Errorf("%s: admin route %d, want 403", name, r.code)
			}
		}
	}

	// Listed in trusted_proxies, the same proxy is believed: the internet
	// client is refused, a LAN client behind it keeps its LAN rights.
	p, _ := netip.ParsePrefix("192.168.1.20/32")
	f.srv.TrustedProxies = []netip.Prefix{p}
	f.h = f.srv.Handler()
	if r := f.do("POST", "/api/v1/auth/auto", "", dev, "RemoteAddr", proxy, "X-Forwarded-For", "8.8.8.8"); r.code != 403 {
		t.Errorf("listed proxy, internet client: %d, want 403", r.code)
	}
	if r := f.do("GET", "/api/v1/admin/users", f.adminTok, nil, "RemoteAddr", proxy, "X-Forwarded-For", "8.8.8.8"); r.code != 403 {
		t.Errorf("listed proxy, internet admin: %d, want 403", r.code)
	}
	// A client that prepends a LAN hop of its own is still the internet hop
	// the proxy appended.
	if r := f.do("POST", "/api/v1/auth/auto", "", dev, "RemoteAddr", proxy, "X-Forwarded-For", "192.168.1.77, 8.8.8.8"); r.code != 403 {
		t.Errorf("listed proxy, prepended LAN hop: %d, want 403", r.code)
	}
	if r := f.do("POST", "/api/v1/auth/auto", "", dev, "RemoteAddr", proxy, "X-Forwarded-For", "192.168.1.77"); r.code != 200 {
		t.Errorf("listed proxy, LAN client: %d %s", r.code, r.body)
	}
	// X-Real-IP and Forwarded are never read, even from a listed proxy: only
	// X-Forwarded-For names the client.
	if r := f.do("POST", "/api/v1/auth/auto", "", dev, "RemoteAddr", proxy, "X-Forwarded-For", "8.8.8.8", "X-Real-IP", "192.168.1.77"); r.code != 403 {
		t.Errorf("listed proxy, X-Real-IP override: %d, want 403", r.code)
	}
}

// /readyz is public so a health checker needs no credential, but off the LAN it
// says only up or down: its checks name every library, granted or not, and
// carry raw mount errors.
func TestReadyzDetailIsLANOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.srv.LibraryRoots = func(context.Context) (map[string]string, error) {
		return map[string]string{"Private Library": "unreachable: open /mnt/secret: no such file"}, nil
	}
	f.h = f.srv.Handler()
	lan := f.do("GET", "/readyz", "", nil)
	if !strings.Contains(string(lan.body), "Private Library") {
		t.Fatalf("control, LAN sees the checks: %d %s", lan.code, lan.body)
	}
	for _, peer := range []string{"8.8.8.8:1", "192.168.1.20:1"} {
		r := f.do("GET", "/readyz", "", nil, "RemoteAddr", peer, "X-Forwarded-For", "8.8.8.8")
		if strings.Contains(string(r.body), "Private") || strings.Contains(string(r.body), "/mnt") || !strings.Contains(string(r.body), `"status":"degraded"`) {
			t.Errorf("%s: %d %s", peer, r.code, r.body)
		}
		if r.code != lan.code {
			t.Errorf("%s: status %d, LAN got %d", peer, r.code, lan.code)
		}
	}
}
