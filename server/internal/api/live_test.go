package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/live"
)

// liveStreamHost is in every stream URL the fixture's playlist names; no
// answer to a client may ever contain it.
const liveStreamHost = "streams.example"

// withLive gives the fixture a playlist with two channels, the first with a
// cached logo, and returns that channel's id.
func withLive(t *testing.T, f *fixture) int64 {
	t.Helper()
	ctx := context.Background()
	store := &live.Store{DB: f.db}
	logos := t.TempDir()
	src, err := store.EnsureSource(ctx, "Walk", "https://playlists.example/walk.m3u?token=s3cret", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Sync(ctx, src, []live.Entry{
		{Name: "Walk News", TvgID: "walk.news", Number: "3", URL: "https://" + liveStreamHost + "/news.m3u8", Logo: "https://img.example/news.png"},
		{Name: "Walk Weather", Number: "4", URL: "https://" + liveStreamHost + "/wx.m3u8"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logos, "news.png"), []byte("\x89PNG\r\n\x1a\nlogo"), 0o600); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := f.db.QueryRow(`UPDATE channels SET logo_file = 'news.png' WHERE name = 'Walk News' RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	// On now, for the guide and the channel list's now.
	now := time.Now()
	if _, err := f.db.Exec(`INSERT INTO programs (channel_id, start_ms, stop_ms, title) VALUES (?, ?, ?, 'Walk Bulletin')`,
		id, now.Add(-10*time.Minute).UnixMilli(), now.Add(50*time.Minute).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	f.srv.Live = &Live{Store: store, LogoDir: logos}
	return id
}

func TestLiveChannelsFollowTheGrant(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	news := withLive(t, f)
	friend := f.userID("friend")

	// No grant: nothing listed, no logo.
	if r := f.do("GET", "/api/v1/live/channels", f.friendTok, nil); r.code != 200 || strings.Contains(string(r.body), "Walk") {
		t.Fatalf("ungranted list: %d %s", r.code, r.body)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/live/channels/%d/logo", news), f.friendTok, nil); r.code != 404 {
		t.Fatalf("ungranted logo: %d", r.code)
	}
	// The admin sees them without a grant, numbered, and without a stream URL.
	r := f.do("GET", "/api/v1/live/channels", f.adminTok, nil)
	if r.code != 200 || !strings.Contains(string(r.body), `"name":"Walk News"`) || !strings.Contains(string(r.body), `"number":"3"`) {
		t.Fatalf("admin list: %d %s", r.code, r.body)
	}
	if strings.Contains(string(r.body), "source_id") {
		t.Errorf("the viewer's list carries admin fields: %s", r.body)
	}

	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/users/%d/policy", friend), f.adminTok, map[string]any{"allow_transcode": true, "allow_live_tv": true}); r.code != 204 {
		t.Fatalf("grant: %d %s", r.code, r.body)
	}
	if r := f.do("GET", "/api/v1/admin/users", f.adminTok, nil); !strings.Contains(string(r.body), `"allow_live_tv":true`) {
		t.Errorf("the users list does not show the grant: %.300s", r.body)
	}
	if r := f.do("GET", "/api/v1/live/channels", f.friendTok, nil); r.code != 200 || !strings.Contains(string(r.body), "Walk Weather") {
		t.Fatalf("granted list: %d %s", r.code, r.body)
	}
	r = f.do("GET", fmt.Sprintf("/api/v1/live/channels/%d/logo", news), f.friendTok, nil)
	if r.code != 200 || r.hdr.Get("Content-Type") != "image/png" || !strings.HasPrefix(string(r.body), "\x89PNG") {
		t.Fatalf("granted logo: %d %q", r.code, r.hdr.Get("Content-Type"))
	}

	// The admin turns News off and renumbers Weather.
	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/live/channels/%d", news), f.adminTok, map[string]any{"enabled": false}); r.code != 204 {
		t.Fatalf("disable: %d %s", r.code, r.body)
	}
	r = f.do("GET", "/api/v1/live/channels", f.friendTok, nil)
	if strings.Contains(string(r.body), "Walk News") {
		t.Errorf("a disabled channel is still listed: %s", r.body)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/live/channels/%d/logo", news), f.friendTok, nil); r.code != 404 {
		t.Errorf("a disabled channel's logo: %d", r.code)
	}
	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/live/channels/%d", news), f.adminTok, map[string]any{"number": strings.Repeat("9", 17)}); r.code != 400 {
		t.Errorf("a 17-character number: %d", r.code)
	}
	if r := f.do("PUT", "/api/v1/admin/live/channels/99999", f.adminTok, map[string]any{"enabled": true}); r.code != 404 {
		t.Errorf("a missing channel: %d", r.code)
	}

	// The admin lists: disabled channels too, with the admin's fields; the
	// source without its URL or token.
	r = f.do("GET", "/api/v1/admin/live/channels", f.adminTok, nil)
	if !strings.Contains(string(r.body), "Walk News") || !strings.Contains(string(r.body), `"enabled":false`) {
		t.Errorf("admin channels: %s", r.body)
	}
	all := string(r.body)
	r = f.do("GET", "/api/v1/admin/live/sources", f.adminTok, nil)
	if !strings.Contains(string(r.body), `"name":"Walk"`) || strings.Contains(string(r.body), "s3cret") || strings.Contains(string(r.body), "playlists.example") {
		t.Errorf("admin sources: %s", r.body)
	}
	all += string(r.body)
	// Not from any answer: no stream URL.
	if strings.Contains(all, liveStreamHost) {
		t.Errorf("a stream URL reached a client: %s", all)
	}
	// The friend may not use the admin routes.
	if r := f.do("GET", "/api/v1/admin/live/channels", f.friendTok, nil); r.code != 403 {
		t.Errorf("friend on the admin list: %d", r.code)
	}
}

func TestLiveGuide(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	withLive(t, f)
	friend := f.userID("friend")
	if r := f.do("GET", "/api/v1/live/guide", f.friendTok, nil); r.code != 200 || strings.Contains(string(r.body), "Walk") {
		t.Fatalf("ungranted guide: %d %s", r.code, r.body)
	}
	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/users/%d/policy", friend), f.adminTok, map[string]any{"allow_transcode": true, "allow_live_tv": true}); r.code != 204 {
		t.Fatalf("grant: %d %s", r.code, r.body)
	}
	r := f.do("GET", "/api/v1/live/guide", f.friendTok, nil)
	var g struct {
		From, To int64
		Channels []struct {
			Name     string
			Programs []struct{ Title string }
		}
	}
	r.json(t, &g)
	if r.code != 200 || g.To-g.From != int64(3*time.Hour/time.Millisecond) || len(g.Channels) != 2 ||
		len(g.Channels[0].Programs) != 1 || g.Channels[0].Programs[0].Title != "Walk Bulletin" || len(g.Channels[1].Programs) != 0 {
		t.Fatalf("guide: %d %s", r.code, r.body)
	}
	// The channel list carries what is on now.
	if r := f.do("GET", "/api/v1/live/channels", f.friendTok, nil); !strings.Contains(string(r.body), `"now":{`) || !strings.Contains(string(r.body), "Walk Bulletin") {
		t.Errorf("channels without now: %s", r.body)
	}
	// An hour from now, the bulletin has ended.
	later := time.Now().Add(time.Hour).UnixMilli()
	if r := f.do("GET", fmt.Sprintf("/api/v1/live/guide?from=%d", later), f.friendTok, nil); strings.Contains(string(r.body), "Walk Bulletin") {
		t.Errorf("guide an hour on: %s", r.body)
	}
	for _, q := range []string{"from=soon", "to=later", fmt.Sprintf("from=%d&to=%d", later, later), fmt.Sprintf("from=%d&to=%d", later, later+int64(25*time.Hour/time.Millisecond))} {
		if r := f.do("GET", "/api/v1/live/guide?"+q, f.friendTok, nil); r.code != 400 {
			t.Errorf("guide?%s: %d, want 400", q, r.code)
		}
	}
}

// Without live_tv in vcrx.yaml (Live unset), the viewer's list is empty and
// the rest answer 404, rather than failing.
func TestLiveOffAnswersEmpty(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if r := f.do("GET", "/api/v1/live/channels", f.adminTok, nil); r.code != 200 || string(r.body) != `{"channels":[]}`+"\n" {
		t.Errorf("list: %d %q", r.code, r.body)
	}
	if r := f.do("GET", "/api/v1/live/channels/1/logo", f.adminTok, nil); r.code != 404 {
		t.Errorf("logo: %d", r.code)
	}
	if r := f.do("POST", "/api/v1/admin/live/sources/1/refresh", f.adminTok, nil); r.code != 404 {
		t.Errorf("refresh: %d", r.code)
	}
}
