package api

import (
	"bytes"
	"testing"
)

func TestSpeedTest(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if r := f.do("GET", "/api/v1/speedtest", "", nil); r.code != 401 {
		t.Fatalf("speed test without a token: %d", r.code)
	}
	r := f.do("GET", "/api/v1/speedtest?bytes=200000", f.friendTok, nil)
	if r.code != 200 || len(r.body) != 200_000 || r.hdr.Get("Content-Length") != "200000" {
		t.Fatalf("speed test: %d, %d bytes, length %q", r.code, len(r.body), r.hdr.Get("Content-Length"))
	}
	if r.hdr.Get("Cache-Control") != "no-store" {
		t.Errorf("speed test may be cached: %q", r.hdr.Get("Cache-Control"))
	}
	// Random filler: a compressing proxy must not be able to shrink it.
	if bytes.Equal(r.body[:65536], make([]byte, 65536)) {
		t.Error("speed test sends zeros")
	}
	if r := f.do("GET", "/api/v1/speedtest", f.friendTok, nil); r.code != 200 || len(r.body) != speedTestDefault {
		t.Errorf("default size: %d, %d bytes", r.code, len(r.body))
	}
	for _, bad := range []string{"0", "-5", "nope", "64000001"} {
		if r := f.do("GET", "/api/v1/speedtest?bytes="+bad, f.friendTok, nil); r.code != 400 {
			t.Errorf("bytes=%s: %d, want 400", bad, r.code)
		}
	}
}
