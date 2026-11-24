package api

import (
	"net/netip"
	"strings"
	"sync"
	"testing"
)

// newEmptyFixture is a server as first started: libraries but no account.
func newEmptyFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	for _, q := range []string{`DELETE FROM auth_sessions`, `DELETE FROM users`} {
		if _, err := f.db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return f
}

func setupBody(user, pass string) map[string]any {
	return map[string]any{"username": user, "password": pass,
		"device": map[string]string{"id": "web-setup", "name": "Chrome on Linux", "platform": "web", "client": "VCRX Web", "client_version": "1.0.0"}}
}

func (f *fixture) countUsers() int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestSetupCreatesFirstAdminOnce(t *testing.T) {
	t.Parallel()
	f := newEmptyFixture(t)
	if r := f.do("GET", "/api/v1/server", "", nil); !strings.Contains(string(r.body), `"setup_needed":true`) {
		t.Fatalf("a new server on the LAN must offer setup: %s", r.body)
	}
	// Validation failures create nothing and leave setup open.
	if r := f.do("POST", "/api/v1/setup", "", setupBody("owner", "short")); r.code != 400 {
		t.Fatalf("short password: %d %s", r.code, r.body)
	}
	if n := f.countUsers(); n != 0 {
		t.Fatalf("a refused setup created %d accounts", n)
	}
	r := f.do("POST", "/api/v1/setup", "", setupBody("owner", "ownerpass1"))
	if r.code != 200 {
		t.Fatalf("setup: %d %s", r.code, r.body)
	}
	var out struct {
		Token string
		User  struct {
			Username string
			IsAdmin  bool `json:"is_admin"`
		}
	}
	r.json(t, &out)
	if out.Token == "" || out.User.Username != "owner" || !out.User.IsAdmin {
		t.Fatalf("setup response: %s", r.body)
	}
	// The token it returned is an admin session.
	if r := f.do("GET", "/api/v1/admin/users", out.Token, nil); r.code != 200 {
		t.Fatalf("new admin cannot reach admin routes: %d %s", r.code, r.body)
	}
	// The password works for a normal sign-in afterwards.
	f.login("owner", "ownerpass1")
	if r := f.do("GET", "/api/v1/server", "", nil); !strings.Contains(string(r.body), `"setup_needed":false`) {
		t.Fatalf("setup still offered after it ran: %s", r.body)
	}
	if r := f.do("POST", "/api/v1/setup", "", setupBody("intruder", "intruderpass1")); r.code != 409 {
		t.Fatalf("second setup: %d %s", r.code, r.body)
	}
	if n := f.countUsers(); n != 1 {
		t.Fatalf("want 1 account after setup, have %d", n)
	}
}

// A server that already has accounts never offers setup, even before this
// process has cached that, and refuses it.
func TestSetupClosedWhenAccountsExist(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	before := f.countUsers()
	if r := f.do("GET", "/api/v1/server", "", nil); !strings.Contains(string(r.body), `"setup_needed":false`) {
		t.Fatalf("setup offered on a server with accounts: %s", r.body)
	}
	f.srv.setupDone.Store(false) // as if freshly started
	if r := f.do("POST", "/api/v1/setup", "", setupBody("intruder", "intruderpass1")); r.code != 409 {
		t.Fatalf("setup on a server with accounts: %d %s", r.code, r.body)
	}
	if n := f.countUsers(); n != before {
		t.Fatalf("accounts went from %d to %d", before, n)
	}
}

// Only the local network may set a new server up: not the internet, not the
// internet through a trusted reverse proxy, and not through a proxy that is
// not trusted (whose own LAN address must not count as the client's).
func TestSetupRefusedOffTheLAN(t *testing.T) {
	t.Parallel()
	f := newEmptyFixture(t)
	f.srv.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.2/32")}
	cases := []struct {
		name string
		hdr  []string
	}{
		{"internet", []string{"RemoteAddr", "8.8.8.8:1"}},
		{"tailscale ipv4", []string{"RemoteAddr", "100.101.102.103:1"}},
		{"trusted proxy, internet client", []string{"RemoteAddr", "127.0.0.2:1", "X-Forwarded-For", "203.0.113.9"}},
		{"untrusted proxy on the LAN", []string{"RemoteAddr", "192.168.1.9:1", "X-Forwarded-For", "192.168.1.50"}},
	}
	for _, c := range cases {
		if r := f.do("GET", "/api/v1/server", "", nil, c.hdr...); !strings.Contains(string(r.body), `"setup_needed":false`) {
			t.Errorf("%s: setup offered: %s", c.name, r.body)
		}
		if r := f.do("POST", "/api/v1/setup", "", setupBody("intruder", "intruderpass1"), c.hdr...); r.code != 403 {
			t.Errorf("%s: setup answered %d %s", c.name, r.code, r.body)
		}
	}
	if n := f.countUsers(); n != 0 {
		t.Fatalf("off-LAN setup created %d accounts", n)
	}
	// Control: the same trusted proxy forwarding a LAN client may set up.
	if r := f.do("POST", "/api/v1/setup", "", setupBody("owner", "ownerpass1"), "RemoteAddr", "127.0.0.2:1", "X-Forwarded-For", "192.168.1.50"); r.code != 200 {
		t.Fatalf("LAN client through the trusted proxy: %d %s", r.code, r.body)
	}
}

// Two setup screens submitting at once make exactly one admin.
func TestSetupRace(t *testing.T) {
	t.Parallel()
	f := newEmptyFixture(t)
	const n = 8
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = f.do("POST", "/api/v1/setup", "", setupBody("owner"+string(rune('a'+i)), "ownerpass1")).code
		}()
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		switch c {
		case 200:
			ok++
		case 409:
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if ok != 1 || f.countUsers() != 1 {
		t.Fatalf("want exactly one admin, got %d successes and %d accounts (%v)", ok, f.countUsers(), codes)
	}
}

// A cookie session from setup needs the client header, as sign-in does, so a
// cross-site form cannot plant one.
func TestSetupCookieNeedsCSRFHeader(t *testing.T) {
	t.Parallel()
	f := newEmptyFixture(t)
	body := setupBody("owner", "ownerpass1")
	body["cookie"] = true
	if r := f.do("POST", "/api/v1/setup", "", body); r.code != 403 {
		t.Fatalf("cookie setup without the header: %d %s", r.code, r.body)
	}
	r := f.do("POST", "/api/v1/setup", "", body, csrfHeader, "web")
	if r.code != 200 || !strings.Contains(r.hdr.Get("Set-Cookie"), "=") {
		t.Fatalf("cookie setup with the header: %d %s %q", r.code, r.body, r.hdr.Get("Set-Cookie"))
	}
}
