package youtube

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEmbeddable(t *testing.T) {
	t.Parallel()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oembed" || r.URL.Query().Get("format") != "json" {
			t.Errorf("asked %s", r.URL)
		}
		u := r.URL.Query().Get("url")
		asked = append(asked, u)
		switch u {
		case "https://www.youtube.com/watch?v=PlaysFine01":
			_, _ = w.Write([]byte(`{"title":"Trailer"}`))
		case "https://www.youtube.com/watch?v=EmbedOff001":
			w.WriteHeader(http.StatusUnauthorized)
		case "https://www.youtube.com/watch?v=GoneVideo01":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusTooManyRequests)
		}
	}))
	t.Cleanup(srv.Close)
	c := New()
	c.BaseURL = srv.URL
	ctx := context.Background()
	cases := []struct {
		id      string
		ok, err bool
	}{
		{"PlaysFine01", true, false},
		{"EmbedOff001", false, false},
		{"GoneVideo01", false, false},
		// Rate limited: unknown, so an error and the caller keeps its key.
		{"Throttled01", false, true},
		// Never sent: not a video id.
		{"x/../a?b=c", false, false},
	}
	for _, tc := range cases {
		ok, err := c.Embeddable(ctx, tc.id)
		if ok != tc.ok || (err != nil) != tc.err {
			t.Errorf("%s: ok %v err %v, want ok %v err %v", tc.id, ok, err, tc.ok, tc.err)
		}
	}
	if len(asked) != 4 {
		t.Errorf("asked oEmbed %d times, want 4 (the malformed id never): %v", len(asked), asked)
	}

	c.BaseURL = "http://127.0.0.1:1"
	if _, err := c.Embeddable(ctx, "PlaysFine01"); err == nil {
		t.Error("offline: want an error, not an answer")
	}
}
