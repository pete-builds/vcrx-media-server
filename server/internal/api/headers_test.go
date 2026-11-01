package api

import "testing"

// Every response, JSON, the web app's files, errors, says nosniff, so a
// browser never second-guesses a declared type (an artwork or subtitle body
// read as HTML or script).
func TestEveryResponseSaysNoSniff(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, c := range []struct{ method, path, token string }{
		{"GET", "/api/v1/server", ""},
		{"GET", "/api/v1/libraries", f.adminTok},
		{"GET", "/api/v1/no-such-route", ""},
		{"GET", "/api/v1/libraries", ""},
		{"GET", "/", ""},
		{"GET", "/healthz", ""},
	} {
		r := f.do(c.method, c.path, c.token, nil)
		if got := r.hdr.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("%s %s (%d): X-Content-Type-Options = %q", c.method, c.path, r.code, got)
		}
	}
}
