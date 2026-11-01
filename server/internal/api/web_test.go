package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestWebAppServing(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	get := func(path string, hdr ...string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "192.168.1.20:5555"
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, req)
		return w
	}
	root := get("/")
	if root.Code != 200 || !strings.Contains(root.Body.String(), `<script type="module" src="js/app.js">`) {
		t.Fatalf("/: %d %.200s", root.Code, root.Body.String())
	}
	csp := root.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe") || root.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security headers: %v", root.Header())
	}
	// A deep link falls back to the shell; an unknown API path stays a JSON 404.
	if w := get("/library/1"); w.Code != 200 || !strings.Contains(w.Body.String(), "js/app.js") {
		t.Fatalf("deep link: %d", w.Code)
	}
	// A missing asset is a 404, never the page: otherwise a broken build passes
	// every probe with a 200.
	for _, p := range []string{"/js/not-there.js", "/css/gone.css", "/img/none.png", "/fonts/x.ttf"} {
		if w := get(p); w.Code != 404 || strings.Contains(w.Body.String(), "js/app.js") {
			t.Fatalf("missing asset %s: %d %.80s", p, w.Code, w.Body.String())
		}
	}
	if w := get("/api/v1/nope"); w.Code != 404 || !strings.Contains(w.Header().Get("Content-Type"), "json") {
		t.Fatalf("unknown api path: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	// Assets revalidate by content hash.
	js := get("/js/app.js")
	tag := js.Header().Get("ETag")
	if js.Code != 200 || tag == "" || !strings.Contains(js.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("app.js: %d etag=%q type=%q", js.Code, tag, js.Header().Get("Content-Type"))
	}
	if w := get("/js/app.js", "If-None-Match", tag); w.Code != 304 {
		t.Fatalf("revalidation: %d, want 304", w.Code)
	}
}

func TestCookieSessionAndCSRF(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	r := f.do("POST", "/api/v1/auth/login", "", map[string]any{"username": "pete", "password": "adminpass1", "cookie": true}, csrfHeader, "web")
	if r.code != 200 {
		t.Fatalf("login: %d %s", r.code, r.body)
	}
	set := r.hdr.Get("Set-Cookie")
	if !strings.HasPrefix(set, cookieName+"=") || !strings.Contains(set, "HttpOnly") || !strings.Contains(set, "SameSite=Strict") {
		t.Fatalf("cookie: %q", set)
	}
	cookie := strings.SplitN(set, ";", 2)[0]
	// Without "cookie": true, no cookie is set (the TV keeps its bearer token).
	if r := f.do("POST", "/api/v1/auth/login", "", map[string]any{"username": "pete", "password": "adminpass1"}); r.hdr.Get("Set-Cookie") != "" {
		t.Fatal("login without cookie:true set a cookie")
	}
	call := func(method, path string, body string, hdr ...string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.RemoteAddr = "192.168.1.20:5555"
		req.Header.Set("Cookie", cookie)
		req.Header.Set("Content-Type", "application/json")
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, req)
		return w
	}
	if w := call("GET", "/api/v1/me", ""); w.Code != 200 {
		t.Fatalf("cookie GET: %d", w.Code)
	}
	path := fmt.Sprintf("/api/v1/items/%d/watched", f.movieID)
	if w := call("PUT", path, `{"played":true}`); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "csrf") {
		t.Fatalf("cookie PUT without the client header: %d %s, want 403 csrf", w.Code, w.Body.String())
	}
	if w := call("PUT", path, `{"played":true}`, csrfHeader, "web"); w.Code != 204 {
		t.Fatalf("cookie PUT with the client header: %d %s", w.Code, w.Body.String())
	}
	// Bearer clients are unaffected by the header rule.
	if r := f.do("PUT", path, f.adminTok, map[string]any{"played": false}); r.code != 204 {
		t.Fatalf("bearer PUT: %d", r.code)
	}
	w := call("POST", "/api/v1/auth/logout", "", csrfHeader, "web")
	if w.Code != 204 || !strings.Contains(w.Header().Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout: %d %q", w.Code, w.Header().Get("Set-Cookie"))
	}
	if w := call("GET", "/api/v1/me", ""); w.Code != 401 {
		t.Fatalf("cookie after logout: %d, want 401", w.Code)
	}
}

// Signing in with "cookie": true plants a session cookie, so like redeeming
// an invite or switching profile it needs the client header: a page on
// another site must not be able to sign this browser in. Bearer sign-ins
// (the TV) are unaffected.
func TestSignInCookieNeedsTheClientHeader(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	lan, _ := netip.ParsePrefix("192.168.0.0/16")
	f.srv.AutoSignInUser, f.srv.TrustedNetworks = "pete", []netip.Prefix{lan}
	f.h = f.srv.Handler()
	home := []string{"RemoteAddr", "192.168.1.20:5555"}
	login := map[string]any{"username": "pete", "password": "adminpass1", "cookie": true}
	auto := map[string]any{"device": map[string]string{"id": "web-1"}, "cookie": true}
	for _, c := range []struct {
		path string
		body map[string]any
	}{{"/api/v1/auth/login", login}, {"/api/v1/auth/auto", auto}} {
		r := f.do("POST", c.path, "", c.body, home...)
		wantError(t, c.path+" with cookie:true and no header", r, 403, "csrf")
		if r.hdr.Get("Set-Cookie") != "" {
			t.Errorf("%s: a refused sign-in set a cookie", c.path)
		}
		r = f.do("POST", c.path, "", c.body, append(home, csrfHeader, "web")...)
		if r.code != 200 || !strings.HasPrefix(r.hdr.Get("Set-Cookie"), cookieName+"=") {
			t.Errorf("%s with the header: %d %s", c.path, r.code, r.body)
		}
	}
	if r := f.do("POST", "/api/v1/auth/login", "", map[string]any{"username": "pete", "password": "adminpass1"}, home...); r.code != 200 {
		t.Errorf("bearer login without the header: %d %s", r.code, r.body)
	}
	if r := f.do("POST", "/api/v1/auth/auto", "", map[string]any{"device": map[string]string{"id": "tv-1"}}, home...); r.code != 200 {
		t.Errorf("bearer auto sign-in without the header: %d %s", r.code, r.body)
	}
}

// Behind a TLS-terminating proxy the browser is on HTTPS, so the session
// cookie must be Secure; the proxy's X-Forwarded-Proto is believed only from a
// trusted proxy, as for invite links.
func TestSessionCookieSecureBehindTrustedProxy(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	proxy := netip.PrefixFrom(netip.MustParseAddr("10.9.9.9"), 32)
	f.srv.TrustedProxies = []netip.Prefix{proxy}
	f.h = f.srv.Handler()
	login := map[string]any{"username": "pete", "password": "adminpass1", "cookie": true}
	for _, c := range []struct {
		what   string
		hdr    []string
		secure bool
	}{
		{"trusted proxy, https", []string{"RemoteAddr", "10.9.9.9:4000", "X-Forwarded-For", "203.0.113.5", "X-Forwarded-Proto", "https"}, true},
		{"trusted proxy, http", []string{"RemoteAddr", "10.9.9.9:4000", "X-Forwarded-For", "203.0.113.5"}, false},
		{"untrusted peer claiming https", []string{"RemoteAddr", "192.168.1.20:5555", "X-Forwarded-Proto", "https"}, false},
	} {
		r := f.do("POST", "/api/v1/auth/login", "", login, append(c.hdr, csrfHeader, "web")...)
		set := r.hdr.Get("Set-Cookie")
		if r.code != 200 || !strings.HasPrefix(set, cookieName+"=") {
			t.Fatalf("%s: %d %s", c.what, r.code, r.body)
		}
		if got := strings.Contains(set, "; Secure"); got != c.secure {
			t.Errorf("%s: Secure = %v, want %v (%s)", c.what, got, c.secure, set)
		}
	}
}
