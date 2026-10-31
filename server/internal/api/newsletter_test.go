package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/newsletter"
	"github.com/pete-builds/vcrx-media-server/server/internal/notify"
)

func TestNewsletterSettingsAndPreview(t *testing.T) {
	f := newFixture(t)
	var mu sync.Mutex
	var posts []map[string]any
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		posts = append(posts, m)
		mu.Unlock()
	}))
	defer recv.Close()
	f.srv.Newsletter = &newsletter.Service{DB: f.db, Log: f.srv.Log}

	if r := f.do("GET", "/api/v1/admin/newsletter", f.friendTok, nil); r.code != 403 {
		t.Fatalf("friend reading newsletter settings: %d", r.code)
	}
	var st newsletter.Status
	r := f.do("GET", "/api/v1/admin/newsletter", f.adminTok, nil)
	r.json(t, &st)
	if r.code != 200 || st.Enabled || st.Webhooks != 0 || st.NextSend != nil {
		t.Fatalf("default: %d %s", r.code, r.body)
	}
	if r := f.do("PUT", "/api/v1/admin/newsletter", f.adminTok, map[string]any{"enabled": true, "weekday": 9, "hour": 8}); r.code != 400 {
		t.Fatalf("bad weekday: %d %s", r.code, r.body)
	}
	r = f.do("PUT", "/api/v1/admin/newsletter", f.adminTok, map[string]any{"enabled": true, "weekday": 1, "hour": 8})
	r.json(t, &st)
	if r.code != 200 || !st.Enabled || st.Weekday != 1 || st.Hour != 8 || st.NextSend == nil || st.LastSent == nil {
		t.Fatalf("save: %d %s", r.code, r.body)
	}

	// No webhook set up: the preview says so instead of pretending to send.
	if r := f.do("POST", "/api/v1/admin/newsletter/preview", f.adminTok, nil); r.code != 409 {
		t.Fatalf("preview without webhooks: %d %s", r.code, r.body)
	}
	f.srv.Webhooks = notify.New([]string{recv.URL}, nil, "Test", nil)
	f.srv.Newsletter.Hooks = f.srv.Webhooks
	r = f.do("POST", "/api/v1/admin/newsletter/preview", f.adminTok, nil)
	var out struct{ Sent, Movies, Shows, Episodes int }
	r.json(t, &out)
	// The fixture added one film and one episode just now.
	if r.code != 200 || out.Sent != 1 || out.Movies != 1 || out.Shows != 1 || out.Episodes != 1 {
		t.Fatalf("preview: %d %s", r.code, r.body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 || posts[0]["event"] != "newsletter" || posts[0]["preview"] != true {
		t.Fatalf("posted: %v", posts)
	}
}
