package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/admin"
	"github.com/pete-builds/vcrx-media-server/server/internal/artwork"
	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
)

type fixture struct {
	t            *testing.T
	db           *sql.DB
	srv          *Server
	h            http.Handler
	root         string
	movieID      int64
	v4k, v1080   int64
	fileSize     int64
	adminTok     string
	friendTok    string
	strangerTok  string
	outsidePath  string
	showID       int64
	seasonID     int64
	episodeID    int64
	episodeVerID int64
}

type fakeScans struct{ started []int64 }

func (f *fakeScans) StartScan(ctx context.Context, libraryID int64, subPath, trigger string) (int64, error) {
	f.started = append(f.started, libraryID)
	return 0, admin.ErrScanRunning
}

// content is a deterministic byte pattern so range responses can be checked.
func content(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := db.Migrate(context.Background(), conn, "", log); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, db: conn}
	mediaRoot := filepath.Join(dir, "media")
	f.root = filepath.Join(mediaRoot, "Movies")
	tvRoot := filepath.Join(mediaRoot, "TV")
	for _, d := range []string{filepath.Join(f.root, "Film 2001 2160p"), filepath.Join(f.root, "Film 2001 1080p"), filepath.Join(tvRoot, "Show", "Season 1")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	data := content(10_000)
	f.fileSize = int64(len(data))
	p4k := filepath.Join(f.root, "Film 2001 2160p", "film.2160p.mkv")
	p1080 := filepath.Join(f.root, "Film 2001 1080p", "film.1080p.mkv")
	pep := filepath.Join(tvRoot, "Show", "Season 1", "Show.S01E01.mkv")
	f.outsidePath = filepath.Join(dir, "secret.txt")
	for _, p := range []string{p4k, p1080, pep, f.outsidePath} {
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(f.root, "Film 2001 1080p", "film.1080p.en.srt"), []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := func(q string, args ...any) int64 {
		t.Helper()
		res, err := conn.Exec(q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	now := time.Now().UnixMilli()
	movies := exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('Movies', 'movie', ?, ?, ?)`, f.root, now, now)
	tv := exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('TV', 'show', ?, ?, ?)`, tvRoot, now, now)
	f.movieID = exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, year, overview, added_at, updated_at)
		VALUES (?, 'movie', 'film|2001', 'The Film', 'Film', 2001, 'A film.', ?, ?)`, movies, now, now)
	exec(`INSERT INTO item_tags (item_id, kind, value) VALUES (?, 'genre', 'Drama')`, f.movieID)
	version := func(item, lib int64, path, res string, w, h int64) int64 {
		return exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, container, duration_ms, video_codec,
				video_profile, bit_depth, width, height, resolution, probe_status, added_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 0, 'mkv', 6000000, 'hevc', 'main 10', 10, ?, ?, ?, 'ok', ?, ?)`,
			item, lib, path, filepath.Base(path), f.fileSize, w, h, res, now, now)
	}
	f.v4k = version(f.movieID, movies, p4k, "4K", 3840, 2160)
	f.v1080 = version(f.movieID, movies, p1080, "1080p", 1920, 1080)
	exec(`INSERT INTO media_streams (version_id, stream_index, kind, codec, channels, is_default) VALUES (?, 1, 'audio', 'eac3', 6, 1)`, f.v1080)
	exec(`INSERT INTO media_streams (version_id, kind, codec, language, is_external, external_path) VALUES (?, 'subtitle', 'subrip', 'eng', 1, ?)`,
		f.v1080, filepath.Join(f.root, "Film 2001 1080p", "film.1080p.en.srt"))
	f.showID = exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (?, 'show', 'show', 'Show', 'Show', ?, ?)`, tv, now, now)
	f.seasonID = exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, added_at, updated_at)
		VALUES (?, 'season', ?, '1', 'Season 1', 'Season 1', 1, ?, ?)`, tv, f.showID, now, now)
	f.episodeID = exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, episode_number, added_at, updated_at)
		VALUES (?, 'episode', ?, '1', 'Pilot', 'Pilot', 1, 1, ?, ?)`, tv, f.seasonID, now, now)
	f.episodeVerID = version(f.episodeID, tv, pep, "1080p", 1920, 1080)

	store := &auth.Store{DB: conn, TTL: 24 * time.Hour}
	ctx := context.Background()
	if _, err := store.CreateUser(ctx, "pete", "adminpass1", "", true); err != nil {
		t.Fatal(err)
	}
	friend, _ := store.CreateUser(ctx, "friend", "friendpass1", "", false)
	if _, err := store.CreateUser(ctx, "stranger", "strangerpass1", "", false); err != nil {
		t.Fatal(err)
	}
	adm := &admin.Admin{DB: conn, MediaRoots: []string{mediaRoot}}
	if err := adm.SetGrants(ctx, friend.ID, []int64{movies, tv}); err != nil {
		t.Fatal(err)
	}
	res := "1080p"
	if err := adm.SetPolicy(ctx, friend.ID, admin.UserPolicy{MaxResolution: &res, AllowTranscode: true}); err != nil {
		t.Fatal(err)
	}
	cat := &catalog.Catalog{DB: conn}
	f.srv = &Server{DB: conn, Log: log, Version: "test", ServerName: "Test", Auth: store, Limiter: auth.NewLimiter(10, time.Minute),
		Catalog: cat, Playback: &playback.Service{DB: conn, Catalog: cat}, Artwork: artwork.New(conn, filepath.Join(dir, "cache"), "ffmpeg"),
		Admin: adm, Scans: &fakeScans{}}
	f.h = f.srv.Handler()
	f.adminTok = f.login("pete", "adminpass1")
	f.friendTok = f.login("friend", "friendpass1")
	f.strangerTok = f.login("stranger", "strangerpass1")
	return f
}

type resp struct {
	code int
	hdr  http.Header
	body []byte
}

func (r resp) json(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("decode %s: %v", r.body, err)
	}
}

func (f *fixture) do(method, path, token string, body any, hdr ...string) resp {
	f.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.RemoteAddr = "192.168.86.50:40000"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i] == "RemoteAddr" {
			req.RemoteAddr = hdr[i+1]
			continue
		}
		req.Header.Set(hdr[i], hdr[i+1])
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return resp{code: rec.Code, hdr: rec.Header(), body: rec.Body.Bytes()}
}

func (f *fixture) login(user, pass string) string {
	f.t.Helper()
	r := f.do("POST", "/api/v1/auth/login", "", map[string]any{"username": user, "password": pass,
		"device": map[string]string{"id": "dev-" + user, "name": "Test TV", "platform": "android_tv", "client": "vcrx-tv", "client_version": "0.1.0"}})
	if r.code != 200 {
		f.t.Fatalf("login %s: %d %s", user, r.code, r.body)
	}
	var out struct{ Token string }
	r.json(f.t, &out)
	return out.Token
}

func TestPublicAndAuth(t *testing.T) {
	f := newFixture(t)
	if r := f.do("GET", "/healthz", "", nil); r.code != 200 {
		t.Fatalf("healthz %d", r.code)
	}
	r := f.do("GET", "/api/v1/server", "", nil)
	if r.code != 200 || !strings.Contains(string(r.body), `"product":"vcrx-media-server"`) {
		t.Fatalf("server info %d %s", r.code, r.body)
	}
	for _, p := range []string{"/api/v1/libraries", "/api/v1/home", fmt.Sprintf("/api/v1/versions/%d/file", f.v1080)} {
		if r := f.do("GET", p, "", nil); r.code != 401 {
			t.Errorf("%s without a token: %d", p, r.code)
		}
		if r := f.do("GET", p, "not-a-token", nil); r.code != 401 {
			t.Errorf("%s with a bad token: %d", p, r.code)
		}
	}
	if r := f.do("GET", "/api/v1/nope", f.adminTok, nil); r.code != 404 {
		t.Errorf("unknown route: %d", r.code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	f := newFixture(t)
	for i := range 10 {
		r := f.do("POST", "/api/v1/auth/login", "", map[string]any{"username": "friend", "password": "wrong-wrong", "device": map[string]string{}})
		if r.code != 401 {
			t.Fatalf("attempt %d: %d", i, r.code)
		}
	}
	r := f.do("POST", "/api/v1/auth/login", "", map[string]any{"username": "friend", "password": "friendpass1", "device": map[string]string{}})
	if r.code != 429 || r.hdr.Get("Retry-After") == "" {
		t.Fatalf("after 10 failures: %d %s", r.code, r.body)
	}
}

func TestUngrantedUserSeesNothing(t *testing.T) {
	f := newFixture(t)
	var libs struct{ Libraries []catalog.Library }
	f.do("GET", "/api/v1/libraries", f.strangerTok, nil).json(t, &libs)
	if len(libs.Libraries) != 0 {
		t.Fatalf("ungranted user sees %v", libs.Libraries)
	}
	for _, p := range []string{
		fmt.Sprintf("/api/v1/items/%d", f.movieID),
		fmt.Sprintf("/api/v1/versions/%d/file", f.v1080),
		fmt.Sprintf("/api/v1/items/%d/children", f.showID),
		"/api/v1/libraries/1/items",
	} {
		if r := f.do("GET", p, f.strangerTok, nil); r.code != 404 {
			t.Errorf("%s: %d", p, r.code)
		}
	}
	var s struct{ Items []catalog.ItemSummary }
	f.do("GET", "/api/v1/search?q=film", f.strangerTok, nil).json(t, &s)
	if len(s.Items) != 0 {
		t.Fatalf("search leaked %v", s.Items)
	}
}

// The reference's headline access test: a friend capped at 1080p can neither list
// nor stream the 4K version of a title that has both.
func TestFriendCannotSeeOrStream4K(t *testing.T) {
	f := newFixture(t)
	var d catalog.ItemDetail
	r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), f.friendTok, nil)
	if r.code != 200 {
		t.Fatalf("friend item: %d %s", r.code, r.body)
	}
	r.json(t, &d)
	if len(d.Versions) != 1 || d.Versions[0].ID != f.v1080 || *d.Resolution != "1080p" {
		t.Fatalf("friend versions: %+v resolution %v", d.Versions, d.Resolution)
	}
	if bytes.Contains(r.body, []byte(`"path"`)) || bytes.Contains(r.body, []byte(f.root)) {
		t.Fatalf("item detail leaks file paths: %s", r.body)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/versions/%d/file", f.v4k), f.friendTok, nil); r.code != 404 {
		t.Fatalf("friend streamed 4K: %d", r.code)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/versions/%d/file", f.v1080), f.friendTok, nil); r.code != 200 {
		t.Fatalf("friend 1080p: %d", r.code)
	}
	var page struct {
		Items []catalog.ItemSummary
		Total int
	}
	f.do("GET", "/api/v1/libraries/1/items?resolution=4K", f.friendTok, nil).json(t, &page)
	if page.Total != 0 {
		t.Fatalf("4K filter shows %d items to the friend", page.Total)
	}
	var sess map[string]any
	r = f.do("POST", "/api/v1/playback/sessions", f.friendTok, map[string]any{"item_id": f.movieID, "version_id": f.v4k, "capabilities": tclCaps()})
	if r.code != 404 {
		t.Fatalf("friend started a 4K session: %d %s", r.code, r.body)
	}
	r = f.do("POST", "/api/v1/playback/sessions", f.friendTok, map[string]any{"item_id": f.movieID, "capabilities": tclCaps()})
	r.json(t, &sess)
	if r.code != 201 || sess["version"].(map[string]any)["id"].(float64) != float64(f.v1080) {
		t.Fatalf("friend auto session: %d %s", r.code, r.body)
	}
	// The admin sees both, best first.
	f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), f.adminTok, nil).json(t, &d)
	if len(d.Versions) != 2 || d.Versions[0].ID != f.v4k {
		t.Fatalf("admin versions: %+v", d.Versions)
	}
}

func TestDenyLabelHidesShowAndEpisodes(t *testing.T) {
	f := newFixture(t)
	lid, _ := f.db.Exec(`INSERT INTO labels (name, hidden) VALUES ('kids-no', 1)`)
	labelID, _ := lid.LastInsertId()
	if _, err := f.db.Exec(`INSERT INTO item_labels (item_id, label_id) VALUES (?, ?)`, f.showID, labelID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE user_policies SET deny_labels = '["kids-no"]' WHERE user_id = (SELECT id FROM users WHERE username = 'friend')`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{f.showID, f.seasonID, f.episodeID} {
		if r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", id), f.friendTok, nil); r.code != 404 {
			t.Errorf("item %d visible despite a deny label on its show: %d", id, r.code)
		}
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/versions/%d/file", f.episodeVerID), f.friendTok, nil); r.code != 404 {
		t.Errorf("episode stream despite deny label: %d", r.code)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.showID), f.adminTok, nil); r.code != 200 {
		t.Errorf("admin lost the show: %d", r.code)
	}
}

func TestRangeRequests(t *testing.T) {
	f := newFixture(t)
	url := fmt.Sprintf("/api/v1/versions/%d/file", f.v1080)
	data := content(int(f.fileSize))

	r := f.do("GET", url, f.adminTok, nil)
	if r.code != 200 || int64(len(r.body)) != f.fileSize || r.hdr.Get("Accept-Ranges") != "bytes" || r.hdr.Get("Content-Type") != "video/x-matroska" {
		t.Fatalf("full GET: %d len %d hdr %v", r.code, len(r.body), r.hdr)
	}
	cases := []struct {
		rng        string
		code       int
		start, end int // inclusive
	}{
		{"bytes=0-9", 206, 0, 9},
		{"bytes=9990-", 206, 9990, 9999},
		{"bytes=-5", 206, 9995, 9999},
		{"bytes=5000-99999", 206, 5000, 9999},
		{"bytes=10000-", 416, 0, 0},
		{"bytes=20-10", 416, 0, 0},
	}
	for _, c := range cases {
		r := f.do("GET", url, f.adminTok, nil, "Range", c.rng)
		if r.code != c.code {
			t.Errorf("%s: code %d, want %d", c.rng, r.code, c.code)
			continue
		}
		if c.code == 206 {
			want := data[c.start : c.end+1]
			if !bytes.Equal(r.body, want) {
				t.Errorf("%s: wrong bytes (len %d)", c.rng, len(r.body))
			}
			if cr := r.hdr.Get("Content-Range"); cr != fmt.Sprintf("bytes %d-%d/%d", c.start, c.end, f.fileSize) {
				t.Errorf("%s: Content-Range %q", c.rng, cr)
			}
		}
	}
	// HEAD carries the length and no body.
	r = f.do("HEAD", url, f.adminTok, nil)
	if r.code != 200 || len(r.body) != 0 || r.hdr.Get("Content-Length") != fmt.Sprint(f.fileSize) {
		t.Fatalf("HEAD: %d len %d cl %q", r.code, len(r.body), r.hdr.Get("Content-Length"))
	}
	// If-Range with a stale validator ignores the range and sends everything.
	r = f.do("GET", url, f.adminTok, nil, "Range", "bytes=0-9", "If-Range", "Mon, 02 Jan 2006 15:04:05 GMT")
	if r.code != 200 || int64(len(r.body)) != f.fileSize {
		t.Fatalf("stale If-Range: %d len %d", r.code, len(r.body))
	}
	lm := r.hdr.Get("Last-Modified")
	r = f.do("GET", url, f.adminTok, nil, "Range", "bytes=0-9", "If-Range", lm)
	if r.code != 206 || len(r.body) != 10 {
		t.Fatalf("current If-Range: %d len %d", r.code, len(r.body))
	}
}

func TestStreamRefusesPathOutsideRoot(t *testing.T) {
	f := newFixture(t)
	// A tampered row must not turn into a file read outside the library.
	if _, err := f.db.Exec(`UPDATE media_versions SET path = ? WHERE id = ?`, f.outsidePath, f.v1080); err != nil {
		t.Fatal(err)
	}
	r := f.do("GET", fmt.Sprintf("/api/v1/versions/%d/file", f.v1080), f.adminTok, nil)
	if r.code != 404 || bytes.Contains(r.body, content(16)) {
		t.Fatalf("served a file outside the root: %d", r.code)
	}
	traversal := filepath.Join(f.root, "..", "..", "secret.txt")
	if _, err := f.db.Exec(`UPDATE media_versions SET path = ? WHERE id = ?`, traversal, f.v1080); err != nil {
		t.Fatal(err)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/versions/%d/file", f.v1080), f.adminTok, nil); r.code != 404 {
		t.Fatalf("served a ../ path: %d", r.code)
	}
	if r := f.do("GET", "/api/v1/versions/..%2f..%2fetc/file", f.adminTok, nil); r.code != 404 {
		t.Fatalf("encoded traversal in the id: %d", r.code)
	}
}

func TestAdminIsLANOnly(t *testing.T) {
	f := newFixture(t)
	if r := f.do("GET", "/api/v1/admin/users", f.adminTok, nil); r.code != 200 {
		t.Fatalf("admin on LAN: %d %s", r.code, r.body)
	}
	if r := f.do("GET", "/api/v1/admin/users", f.adminTok, nil, "RemoteAddr", "8.8.8.8:5555"); r.code != 403 {
		t.Fatalf("admin from a public IP: %d", r.code)
	}
	// X-Forwarded-For is ignored unless the peer is a trusted proxy.
	if r := f.do("GET", "/api/v1/admin/users", f.adminTok, nil, "RemoteAddr", "8.8.8.8:5555", "X-Forwarded-For", "192.168.1.2"); r.code != 403 {
		t.Fatalf("spoofed forwarded-for accepted: %d", r.code)
	}
	if r := f.do("GET", "/api/v1/admin/users", f.friendTok, nil); r.code != 403 {
		t.Fatalf("non-admin on admin route: %d", r.code)
	}
	if r := f.do("POST", "/api/v1/admin/libraries/1/scan", f.adminTok, map[string]any{}); r.code != 409 {
		t.Fatalf("scan while running: %d %s", r.code, r.body)
	}
	r := f.do("POST", "/api/v1/admin/libraries", f.adminTok, map[string]any{"name": "Etc", "kind": "movie", "root_path": "/etc"})
	if r.code != 400 || !strings.Contains(string(r.body), "media root") {
		t.Fatalf("library outside the media roots: %d %s", r.code, r.body)
	}
}

func tclCaps() map[string]any {
	return map[string]any{
		"containers":   []string{"mkv", "mp4"},
		"video_codecs": []map[string]any{{"codec": "hevc", "profiles": []string{"main", "main 10"}, "max_bit_depth": 10, "max_width": 3840, "max_height": 2160}},
		"audio_codecs": []map[string]any{{"codec": "eac3"}, {"codec": "aac"}},
		"hdr_types":    []string{"hdr10"},
	}
}

// The acceptance-test loop: start, progress, seek, stop, then resume from the
// saved position, with every step in the activity log.
func TestPlaybackResumeLoop(t *testing.T) {
	f := newFixture(t)
	var sess struct {
		SessionID        string `json:"session_id"`
		Decision         string
		Reason           string
		ResumePositionMs int64 `json:"resume_position_ms"`
		Stream           struct{ URL string }
		Subtitles        []struct {
			URL      string
			MimeType string `json:"mime_type"`
		}
	}
	r := f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": tclCaps()})
	if r.code != 201 {
		t.Fatalf("start: %d %s", r.code, r.body)
	}
	r.json(t, &sess)
	if sess.Decision != "direct_play" || sess.ResumePositionMs != 0 || !strings.HasPrefix(sess.Stream.URL, fmt.Sprintf("/api/v1/versions/%d/file", f.v1080)) {
		t.Fatalf("session: %+v", sess)
	}
	if len(sess.Subtitles) != 1 || sess.Subtitles[0].MimeType != "application/x-subrip" {
		t.Fatalf("subtitles: %+v", sess.Subtitles)
	}
	if r := f.do("GET", sess.Subtitles[0].URL, f.adminTok, nil); r.code != 200 || !strings.Contains(string(r.body), "Hello") {
		t.Fatalf("subtitle fetch: %d %s", r.code, r.body)
	}
	ev := func(typ string, pos int64, extra map[string]any) {
		t.Helper()
		body := map[string]any{"type": typ, "position_ms": pos}
		for k, v := range extra {
			body[k] = v
		}
		if r := f.do("POST", "/api/v1/playback/sessions/"+sess.SessionID+"/events", f.adminTok, body); r.code != 204 {
			t.Fatalf("event %s: %d %s", typ, r.code, r.body)
		}
	}
	ev("start", 0, nil)
	ev("progress", 30_000, nil)
	ev("seek", 1_800_000, map[string]any{"from_position_ms": 30_000})
	ev("progress", 1_810_000, nil)
	ev("stop", 1_812_000, nil)
	ev("stop", 1_812_000, nil) // retried stop is fine
	if r := f.do("POST", "/api/v1/playback/sessions/"+sess.SessionID+"/events", f.adminTok, map[string]any{"type": "progress", "position_ms": 1}); r.code != 409 {
		t.Fatalf("event after stop: %d", r.code)
	}
	if r := f.do("POST", "/api/v1/playback/sessions/"+sess.SessionID+"/events", f.friendTok, map[string]any{"type": "pause", "position_ms": 1}); r.code != 404 {
		t.Fatalf("another user's session: %d", r.code)
	}

	var home struct {
		Rows []catalog.HomeRow
	}
	f.do("GET", "/api/v1/home", f.adminTok, nil).json(t, &home)
	if len(home.Rows) == 0 || home.Rows[0].ID != "continue_watching" || home.Rows[0].Items[0].Watch.PositionMs != 1_812_000 {
		t.Fatalf("home: %+v", home.Rows)
	}
	r = f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": tclCaps()})
	r.json(t, &sess)
	if sess.ResumePositionMs != 1_812_000 {
		t.Fatalf("resume position %d", sess.ResumePositionMs)
	}
	ev("start", 1_812_000, nil)
	ev("stop", 5_700_000, nil) // past 90% of 6,000,000 ms
	var d catalog.ItemDetail
	f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), f.adminTok, nil).json(t, &d)
	if !d.Watch.Played || d.Watch.PositionMs != 0 || d.Watch.PlayCount != 1 {
		t.Fatalf("after finishing: %+v", d.Watch)
	}

	var hist struct {
		Sessions []admin.ActivitySession
	}
	f.do("GET", "/api/v1/admin/activity/sessions", f.adminTok, nil).json(t, &hist)
	if len(hist.Sessions) != 2 || hist.Sessions[1].Decision != "direct_play" || hist.Sessions[1].DeviceName == nil ||
		*hist.Sessions[1].DeviceName != "Test TV" || hist.Sessions[1].IP == nil || *hist.Sessions[1].IP != "192.168.86.50" {
		t.Fatalf("history: %+v", hist.Sessions)
	}
	var events int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM playback_events`).Scan(&events); err != nil || events != 7 {
		t.Fatalf("events recorded: %d %v", events, err)
	}
}

func TestRefusalIsRecorded(t *testing.T) {
	f := newFixture(t)
	caps := map[string]any{"containers": []string{"mkv"}, "video_codecs": []map[string]any{{"codec": "h264"}}, "audio_codecs": []map[string]any{{"codec": "aac"}}}
	r := f.do("POST", "/api/v1/playback/sessions", f.friendTok, map[string]any{"item_id": f.movieID, "capabilities": caps})
	if r.code != 422 || !strings.Contains(string(r.body), "transcode_unavailable") {
		t.Fatalf("hevc to an h264-only device: %d %s", r.code, r.body)
	}
	var decision, reason string
	if err := f.db.QueryRow(`SELECT decision, decision_reason FROM playback_sessions`).Scan(&decision, &reason); err != nil {
		t.Fatal(err)
	}
	if decision != "refused" || !strings.Contains(reason, "hevc") {
		t.Fatalf("recorded %s: %s", decision, reason)
	}
}

func TestShowNavigationAndWatched(t *testing.T) {
	f := newFixture(t)
	var kids struct{ Items []catalog.ItemSummary }
	f.do("GET", fmt.Sprintf("/api/v1/items/%d/children", f.showID), f.friendTok, nil).json(t, &kids)
	if len(kids.Items) != 1 || kids.Items[0].ID != f.seasonID || *kids.Items[0].ChildCount != 1 {
		t.Fatalf("seasons: %+v", kids.Items)
	}
	f.do("GET", fmt.Sprintf("/api/v1/items/%d/children", f.seasonID), f.friendTok, nil).json(t, &kids)
	if len(kids.Items) != 1 || kids.Items[0].ID != f.episodeID || *kids.Items[0].ShowTitle != "Show" {
		t.Fatalf("episodes: %+v", kids.Items)
	}
	var d catalog.ItemDetail
	f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.showID), f.friendTok, nil).json(t, &d)
	if d.NextEpisode == nil || d.NextEpisode.ID != f.episodeID || *d.Watch.UnplayedCount != 1 {
		t.Fatalf("show detail: next %+v watch %+v", d.NextEpisode, d.Watch)
	}
	if r := f.do("PUT", fmt.Sprintf("/api/v1/items/%d/watched", f.showID), f.friendTok, map[string]any{"played": true}); r.code != 204 {
		t.Fatalf("mark show watched: %d %s", r.code, r.body)
	}
	f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.showID), f.friendTok, nil).json(t, &d)
	if !d.Watch.Played || *d.Watch.UnplayedCount != 0 {
		t.Fatalf("after marking: %+v", d.Watch)
	}
	var s struct{ Items []catalog.ItemSummary }
	f.do("GET", "/api/v1/search?q=pil", f.friendTok, nil).json(t, &s)
	if len(s.Items) != 1 || s.Items[0].ID != f.episodeID {
		t.Fatalf("search: %+v", s.Items)
	}
}
