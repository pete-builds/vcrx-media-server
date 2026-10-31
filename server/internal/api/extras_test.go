package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
)

// extraFixture adds a trailer and a 4K featurette to the film, and theme music
// to the show, as the scanner would.
type extraFixture struct {
	*fixture
	trailer, featurette4K, theme int64
}

func newExtraFixture(t *testing.T) *extraFixture {
	f := &extraFixture{fixture: newFixture(t)}
	add := func(item int64, lib, rel, kind, title, res, container string, body []byte) int64 {
		t.Helper()
		root := f.root
		if lib == "TV" {
			root = filepath.Join(filepath.Dir(f.root), "TV")
		}
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UnixMilli()
		r, err := f.db.Exec(`INSERT INTO item_extras (item_id, library_id, kind, title, path, rel_path, size, mtime, container, duration_ms,
				video_codec, resolution, audio_streams, probe_status, added_at, updated_at)
			VALUES (?, (SELECT id FROM libraries WHERE name = ?), ?, ?, ?, ?, ?, 0, ?, 90000, ?, ?, '[{"index":1,"codec":"eac3","channels":2,"default":true}]', 'ok', ?, ?)`,
			item, lib, kind, title, p, rel, len(body), container, nullable(kind != "theme", "hevc"), nullable(res != "", res), now, now)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := r.LastInsertId()
		return id
	}
	f.trailer = add(f.movieID, "Movies", "Film 2001 1080p/Trailers/Teaser.mkv", "trailer", "Teaser", "1080p", "mkv", content(4000))
	f.featurette4K = add(f.movieID, "Movies", "Film 2001 2160p/Featurettes/Making Of.mkv", "featurette", "Making Of", "4K", "mkv", content(3000))
	f.theme = add(f.showID, "TV", "Show/theme.mp3", "theme", "Theme music", "", "mp3", content(2000))
	return f
}

func nullable(ok bool, v string) any {
	if !ok {
		return nil
	}
	return v
}

func (f *extraFixture) detail(tok string, id int64) (catalog.ItemDetail, int) {
	f.t.Helper()
	var d catalog.ItemDetail
	r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", id), tok, nil)
	if r.code == 200 {
		r.json(f.t, &d)
	}
	return d, r.code
}

func TestExtrasInDetailsAndPlayback(t *testing.T) {
	f := newExtraFixture(t)
	d, _ := f.detail(f.adminTok, f.movieID)
	if len(d.Extras) != 2 || d.Extras[0].ID != f.trailer || d.Extras[1].Kind != "featurette" || *d.Extras[0].DurationMs != 90000 {
		t.Fatalf("admin extras: %+v", d.Extras)
	}
	if d.Trailer == nil || d.Trailer.Source != "local" || *d.Trailer.ExtraID != f.trailer || d.Trailer.YouTubeKey != nil {
		t.Fatalf("admin trailer: %+v", d.Trailer)
	}
	// The friend's 1080p ceiling applies to extras as it does to versions.
	d, _ = f.detail(f.friendTok, f.movieID)
	if len(d.Extras) != 1 || d.Extras[0].ID != f.trailer {
		t.Fatalf("friend extras: %+v", d.Extras)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/extras/%d/file", f.featurette4K), f.friendTok, nil); r.code != 404 {
		t.Fatalf("friend got the 4K featurette: %d", r.code)
	}

	// Playing the trailer: a session on the same ladder, a file URL for it,
	// and no change to the film's watch state.
	r := f.do("POST", "/api/v1/playback/sessions", f.friendTok, map[string]any{"extra_id": f.trailer, "item_id": f.movieID, "capabilities": tclCaps()})
	if r.code != 201 {
		t.Fatalf("trailer session: %d %s", r.code, r.body)
	}
	var sess struct {
		SessionID string `json:"session_id"`
		Decision  string
		ItemID    int64 `json:"item_id"`
		ExtraID   int64 `json:"extra_id"`
		Stream    struct{ URL string }
	}
	r.json(t, &sess)
	if sess.Decision != "direct_play" || sess.ExtraID != f.trailer || sess.ItemID != f.movieID ||
		!strings.HasPrefix(sess.Stream.URL, fmt.Sprintf("/api/v1/extras/%d/file?session=", f.trailer)) {
		t.Fatalf("session: %s", r.body)
	}
	if r := f.do("GET", sess.Stream.URL, f.friendTok, nil, "Range", "bytes=0-9"); r.code != 206 || len(r.body) != 10 {
		t.Fatalf("trailer file: %d (%d bytes)", r.code, len(r.body))
	}
	for _, ev := range []map[string]any{{"type": "start", "position_ms": 0}, {"type": "progress", "position_ms": 60000, "duration_ms": 90000},
		{"type": "stop", "position_ms": 88000, "duration_ms": 90000}} {
		if r := f.do("POST", "/api/v1/playback/sessions/"+sess.SessionID+"/events", f.friendTok, ev); r.code != 204 {
			t.Fatalf("event %v: %d %s", ev["type"], r.code, r.body)
		}
	}
	var n int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM watch_state`).Scan(&n)
	if n != 0 {
		t.Fatalf("a trailer wrote %d watch state rows", n)
	}
	var title, kind string
	_ = f.db.QueryRow(`SELECT item_title, item_kind FROM playback_sessions WHERE id = ?`, sess.SessionID).Scan(&title, &kind)
	if title != "The Film: Teaser" || kind != "extra" {
		t.Fatalf("activity shows %q (%s)", title, kind)
	}
	// An extra asked for under another item is not found.
	if r := f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"extra_id": f.trailer, "item_id": f.showID, "capabilities": tclCaps()}); r.code != 404 {
		t.Fatalf("extra under the wrong item: %d", r.code)
	}

	// Theme music: in details, not in the Extras row, served as audio, not a player session.
	d, _ = f.detail(f.friendTok, f.showID)
	if d.ThemeMusic == nil || d.ThemeMusic.ID != f.theme || len(d.Extras) != 0 || d.Trailer != nil {
		t.Fatalf("show: theme %+v extras %+v trailer %+v", d.ThemeMusic, d.Extras, d.Trailer)
	}
	if r := f.do("GET", d.ThemeMusic.URL, f.friendTok, nil); r.code != 200 || r.hdr.Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("theme: %d %s", r.code, r.hdr.Get("Content-Type"))
	}
	if r := f.do("POST", "/api/v1/playback/sessions", f.friendTok, map[string]any{"extra_id": f.theme, "capabilities": tclCaps()}); r.code != 422 {
		t.Fatalf("theme as a session: %d", r.code)
	}

	// A missing extra is neither listed nor served.
	if _, err := f.db.Exec(`UPDATE item_extras SET missing_since = 1 WHERE id = ?`, f.trailer); err != nil {
		t.Fatal(err)
	}
	if d, _ = f.detail(f.adminTok, f.movieID); len(d.Extras) != 1 || d.Trailer != nil {
		t.Fatalf("missing extra listed: %+v %+v", d.Extras, d.Trailer)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/extras/%d/file", f.trailer), f.adminTok, nil); r.code != 404 {
		t.Fatalf("missing extra served: %d", r.code)
	}
}

// A user who cannot see the film gets 404 for its extras and its trailer, by
// every route: no library grant, or a deny label.
func TestExtrasFollowTheItemsPolicy(t *testing.T) {
	f := newExtraFixture(t)
	f.srv.TrailersYouTube, f.srv.Catalog.TrailersYouTube = true, true
	if _, err := f.db.Exec(`UPDATE items SET tmdb_id = 77 WHERE id = ?`, f.showID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO youtube_trailers (item_id, tmdb_id, youtube_key, name, status, checked_at) VALUES (?, 77, 'abcDEF12345', 'Official Trailer', 'found', 1)`, f.showID); err != nil {
		t.Fatal(err)
	}
	if d, _ := f.detail(f.friendTok, f.showID); d.Trailer == nil || d.Trailer.Source != "youtube" {
		t.Fatalf("friend should see the show's YouTube trailer: %+v", d.Trailer)
	}
	blocked := func(who, tok string) {
		t.Helper()
		for _, id := range []int64{f.movieID, f.showID} {
			if _, code := f.detail(tok, id); code != 404 {
				t.Errorf("%s: item %d (with its extras and trailer) answered %d", who, id, code)
			}
		}
		for _, id := range []int64{f.trailer, f.theme} {
			if r := f.do("GET", fmt.Sprintf("/api/v1/extras/%d/file", id), tok, nil); r.code != 404 {
				t.Errorf("%s: extra %d file answered %d", who, id, r.code)
			}
			if r := f.do("HEAD", fmt.Sprintf("/api/v1/extras/%d/file", id), tok, nil); r.code != 404 {
				t.Errorf("%s: extra %d HEAD answered %d", who, id, r.code)
			}
		}
		if r := f.do("POST", "/api/v1/playback/sessions", tok, map[string]any{"extra_id": f.trailer, "capabilities": tclCaps()}); r.code != 404 {
			t.Errorf("%s: trailer session answered %d %s", who, r.code, r.body)
		}
	}
	blocked("stranger without grants", f.strangerTok)

	lid, _ := f.db.Exec(`INSERT INTO labels (name, hidden) VALUES ('not-for-friends', 1)`)
	labelID, _ := lid.LastInsertId()
	for _, id := range []int64{f.movieID, f.showID} {
		if _, err := f.db.Exec(`INSERT INTO item_labels (item_id, label_id) VALUES (?, ?)`, id, labelID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec(`UPDATE user_policies SET deny_labels = '["not-for-friends"]' WHERE user_id = (SELECT id FROM users WHERE username = 'friend')`); err != nil {
		t.Fatal(err)
	}
	blocked("friend with a deny label", f.friendTok)
	if r := f.do("GET", fmt.Sprintf("/api/v1/extras/%d/file", f.trailer), f.adminTok, nil); r.code != 200 {
		t.Fatalf("admin lost the trailer: %d", r.code)
	}
}

func TestYouTubeTrailerInDetails(t *testing.T) {
	f := newExtraFixture(t)
	if _, err := f.db.Exec(`UPDATE items SET tmdb_id = 42 WHERE id = ?`, f.showID); err != nil {
		t.Fatal(err)
	}
	set := func(tmdbID int64, key string) {
		t.Helper()
		if _, err := f.db.Exec(`INSERT INTO youtube_trailers (item_id, tmdb_id, youtube_key, name, status, checked_at) VALUES (?, ?, ?, 'Official Trailer', 'found', 1)
			ON CONFLICT (item_id) DO UPDATE SET tmdb_id = excluded.tmdb_id, youtube_key = excluded.youtube_key`, f.showID, tmdbID, key); err != nil {
			t.Fatal(err)
		}
	}
	trailer := func() *catalog.Trailer {
		t.Helper()
		d, code := f.detail(f.adminTok, f.showID)
		if code != 200 {
			t.Fatalf("show: %d", code)
		}
		return d.Trailer
	}
	set(42, "abcDEF12345")
	if tr := trailer(); tr != nil {
		t.Fatalf("YouTube trailer offered with the feature off: %+v", tr)
	}
	f.srv.TrailersYouTube, f.srv.Catalog.TrailersYouTube = true, true
	if tr := trailer(); tr == nil || tr.Source != "youtube" || *tr.YouTubeKey != "abcDEF12345" || tr.Title != "Official Trailer" || tr.ExtraID != nil {
		t.Fatalf("YouTube trailer: %+v", tr)
	}
	// Fix Match to another title: the stored key is for the old one.
	set(43, "abcDEF12345")
	if tr := trailer(); tr != nil {
		t.Fatalf("a key for another TMDb id was offered: %+v", tr)
	}
	// A key that is not a YouTube id is never served.
	set(42, "x\"onload=1")
	if tr := trailer(); tr != nil {
		t.Fatalf("a malformed key was offered: %+v", tr)
	}
	// A local trailer beats YouTube.
	set(42, "abcDEF12345")
	if d, _ := f.detail(f.adminTok, f.movieID); d.Trailer == nil || d.Trailer.Source != "local" {
		t.Fatalf("movie trailer: %+v", d.Trailer)
	}
	var info map[string]any
	f.do("GET", "/api/v1/server", "", nil).json(t, &info)
	if info["trailers_youtube"] != true {
		t.Fatalf("server info: %v", info)
	}
}

// The YouTube player is the only frame the web app may load, and only when
// the feature is on; nothing else in the policy changes.
func TestCSPAllowsYouTubeOnlyWhenOn(t *testing.T) {
	f := newFixture(t)
	csp := func() string {
		t.Helper()
		return f.do("GET", "/", "", nil).hdr.Get("Content-Security-Policy")
	}
	off := csp()
	if strings.Contains(off, "youtube") || strings.Contains(off, "frame-src") || !strings.Contains(off, "default-src 'self'") {
		t.Fatalf("CSP with trailers off: %s", off)
	}
	f.srv.TrailersYouTube = true
	on := csp()
	const frame = "frame-src https://www.youtube-nocookie.com; "
	if !strings.Contains(on, frame) || strings.Count(on, "https://") != 1 {
		t.Fatalf("CSP with trailers on: %s", on)
	}
	if strings.Replace(on, frame, "", 1) != off {
		t.Fatalf("turning trailers on changed more than the frame source:\noff: %s\non:  %s", off, on)
	}
	var info map[string]any
	f.do("GET", "/api/v1/server", "", nil).json(t, &info)
	if info["trailers_youtube"] != true {
		t.Fatalf("server info: %v", info)
	}
}

// A trailer goes through the same gate as a version: for each client, the same
// decision, the same kind of stream or the same refusal. Its converted stream
// finds the extra's file through the session.
func TestExtraPlaysLikeAVersion(t *testing.T) {
	f := newExtraFixture(t)
	f.srv.Playback.CanRemux = true
	f.srv.Remux = remux.New("ffmpeg", "ffprobe", 2, nil)
	// The trailer is shaped like the 1080p version: HEVC Matroska with E-AC-3.
	if _, err := f.db.Exec(`UPDATE item_extras SET video_profile = 'main 10', bit_depth = 10, width = 1920, height = 1080 WHERE id = ?`, f.trailer); err != nil {
		t.Fatal(err)
	}
	type out struct {
		code     int
		decision string
		protocol string
	}
	start := func(tok string, body map[string]any) (out, string) {
		t.Helper()
		r := f.do("POST", "/api/v1/playback/sessions", tok, body)
		var sess struct {
			SessionID string `json:"session_id"`
			Decision  string
			Stream    struct{ Protocol string }
		}
		if r.code == 201 {
			r.json(t, &sess)
		}
		return out{r.code, sess.Decision, sess.Stream.Protocol}, sess.SessionID
	}
	for name, caps := range map[string]map[string]any{"tv": tclCaps(), "browser": browserCaps(true), "browser without fmp4": browserCaps(false)} {
		for _, tok := range []string{f.adminTok, f.friendTok} {
			film, _ := start(tok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": caps})
			trailer, id := start(tok, map[string]any{"extra_id": f.trailer, "capabilities": caps})
			if film != trailer {
				t.Errorf("%s: film %+v, trailer %+v", name, film, trailer)
			}
			if trailer.protocol == "fmp4" {
				r := f.do("GET", "/api/v1/playback/sessions/"+id+"/seek?position_ms=0", tok, nil)
				if r.code != 200 || !strings.Contains(string(r.body), "/stream.mp4") {
					t.Errorf("%s: the trailer's converted stream: %d %s", name, r.code, r.body)
				}
			}
		}
	}
	// A friend not allowed conversions is refused a trailer that needs one, as a film.
	if _, err := f.db.Exec(`UPDATE user_policies SET allow_transcode = 0 WHERE user_id = (SELECT id FROM users WHERE username = 'friend')`); err != nil {
		t.Fatal(err)
	}
	if got, _ := start(f.friendTok, map[string]any{"extra_id": f.trailer, "capabilities": browserCaps(true)}); got.code != 403 {
		t.Fatalf("friend without conversions: %+v", got)
	}
}
