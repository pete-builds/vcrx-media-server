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
	"net/netip"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/activity"
	"github.com/pete-builds/vcrx-media-server/server/internal/admin"
	"github.com/pete-builds/vcrx-media-server/server/internal/artwork"
	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/collections"
	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/notify"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/subtitles"
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
		Admin: adm, Scans: &fakeScans{}, Subtitles: subtitles.New(filepath.Join(dir, "subs"), "ffmpeg", log),
		Activity: &activity.Service{DB: conn}}
	// httptest requests carry Host example.com; a real name must be allowed.
	f.srv.AllowedHosts = []string{"example.com"}
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
	req.RemoteAddr = "192.168.1.50:40000"
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
		*hist.Sessions[1].DeviceName != "Test TV" || hist.Sessions[1].IP == nil || *hist.Sessions[1].IP != "192.168.1.50" {
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

func TestTrustedNetworkSignIn(t *testing.T) {
	f := newFixture(t)
	dev := map[string]any{"device": map[string]string{"id": "web-1", "name": "Browser", "platform": "web", "client": "vcrx-web", "client_version": "1"}}
	// Off by default: no password-free sign-in from anywhere.
	if r := f.do("POST", "/api/v1/auth/auto", "", dev); r.code != 404 {
		t.Fatalf("off: %d %s", r.code, r.body)
	}
	if r := f.do("GET", "/api/v1/server", "", nil); !strings.Contains(string(r.body), `"auto_sign_in":false`) {
		t.Fatalf("server info when off: %s", r.body)
	}
	for _, n := range []string{"192.168.0.0/16", "100.64.0.0/10"} {
		p, _ := netip.ParsePrefix(n)
		f.srv.TrustedNetworks = append(f.srv.TrustedNetworks, p)
	}
	f.srv.AutoSignInUser = "pete"
	f.h = f.srv.Handler()

	for _, ip := range []string{"192.168.1.50:1", "100.101.102.103:1"} {
		r := f.do("POST", "/api/v1/auth/auto", "", dev, "RemoteAddr", ip)
		if r.code != 200 {
			t.Fatalf("%s: %d %s", ip, r.code, r.body)
		}
		var out struct {
			Token string
			User  struct{ Username string }
		}
		r.json(t, &out)
		if out.User.Username != "pete" || out.Token == "" {
			t.Fatalf("%s: %+v", ip, out)
		}
		if me := f.do("GET", "/api/v1/me", out.Token, nil, "RemoteAddr", ip); me.code != 200 {
			t.Fatalf("token from auto sign-in rejected: %d", me.code)
		}
	}
	if r := f.do("GET", "/api/v1/server", "", nil, "RemoteAddr", "100.101.102.103:1"); !strings.Contains(string(r.body), `"auto_sign_in":true`) {
		t.Fatalf("server info on Tailscale: %s", r.body)
	}
	// Outside the trusted networks, and through an untrusted proxy header, it is refused.
	if r := f.do("POST", "/api/v1/auth/auto", "", dev, "RemoteAddr", "8.8.8.8:1"); r.code != 403 {
		t.Fatalf("public address: %d", r.code)
	}
	if r := f.do("POST", "/api/v1/auth/auto", "", dev, "RemoteAddr", "8.8.8.8:1", "X-Forwarded-For", "192.168.1.2"); r.code != 403 {
		t.Fatalf("spoofed forwarded-for: %d", r.code)
	}
	if r := f.do("POST", "/api/v1/auth/auto", "", dev, "RemoteAddr", "10.0.0.5:1"); r.code != 403 {
		t.Fatalf("a private range not in the list: %d", r.code)
	}
	if r := f.do("GET", "/api/v1/server", "", nil, "RemoteAddr", "8.8.8.8:1"); !strings.Contains(string(r.body), `"auto_sign_in":false`) {
		t.Fatalf("server info from outside: %s", r.body)
	}
	// A disabled or missing user is not signed in.
	f.srv.AutoSignInUser = "nobody"
	f.h = f.srv.Handler()
	if r := f.do("POST", "/api/v1/auth/auto", "", dev); r.code != 403 {
		t.Fatalf("missing user: %d", r.code)
	}
}

func TestWebVTTSubtitles(t *testing.T) {
	ffmpeg, err := osexec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	f := newFixture(t)
	f.srv.Subtitles.FFmpeg = ffmpeg
	// Make the episode a real MKV with an embedded SubRip track at index 1, and
	// give the 4K movie an embedded PGS track, which cannot become text.
	srt := filepath.Join(t.TempDir(), "in.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,500 --> 00:00:01,500\nInside the file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var epPath string
	if err := f.db.QueryRow(`SELECT path FROM media_versions WHERE id = ?`, f.episodeVerID).Scan(&epPath); err != nil {
		t.Fatal(err)
	}
	if msg, err := osexec.Command(ffmpeg, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=64x64:d=2",
		"-i", srt, "-map", "0:v", "-map", "1", "-c:v", "mpeg4", "-c:s", "srt", "-y", epPath+".tmp.mkv").CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, msg)
	}
	if err := os.Rename(epPath+".tmp.mkv", epPath); err != nil {
		t.Fatal(err)
	}
	insert := func(q string, args ...any) int64 {
		t.Helper()
		res, err := f.db.Exec(q, args...)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	embedded := insert(`INSERT INTO media_streams (version_id, stream_index, kind, codec, language) VALUES (?, 1, 'subtitle', 'subrip', 'eng')`, f.episodeVerID)
	pgs := insert(`INSERT INTO media_streams (version_id, stream_index, kind, codec, is_image_based) VALUES (?, 2, 'subtitle', 'hdmv_pgs_subtitle', 1)`, f.v4k)

	// The play response lists both text tracks as WebVTT and marks the embedded one.
	var sess struct {
		Subtitles []struct{ URL string }
		WebVTT    []struct {
			URL      string
			Embedded bool
			MimeType string `json:"mime_type"`
		} `json:"webvtt_subtitles"`
	}
	r := f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.episodeID, "capabilities": tclCaps()})
	if r.code != 201 {
		t.Fatalf("start: %d %s", r.code, r.body)
	}
	r.json(t, &sess)
	if len(sess.Subtitles) != 0 {
		t.Fatalf("embedded tracks must stay out of the side-loaded list the TV reads: %+v", sess.Subtitles)
	}
	if len(sess.WebVTT) != 1 || !sess.WebVTT[0].Embedded || sess.WebVTT[0].MimeType != "text/vtt" {
		t.Fatalf("webvtt_subtitles: %+v", sess.WebVTT)
	}

	// First fetch starts the pass and answers 202; it then serves WebVTT.
	url := sess.WebVTT[0].URL
	r = f.do("GET", url, f.adminTok, nil)
	if r.code != 202 || r.hdr.Get("Retry-After") == "" {
		t.Fatalf("first embedded fetch: %d %s", r.code, r.body)
	}
	f.srv.Subtitles.Wait(epPath)
	r = f.do("GET", url, f.adminTok, nil)
	if r.code != 200 || !strings.HasPrefix(r.hdr.Get("Content-Type"), "text/vtt") || !strings.Contains(string(r.body), "Inside the file") {
		t.Fatalf("embedded fetch after the pass: %d %s %s", r.code, r.hdr.Get("Content-Type"), r.body)
	}
	// Without format=vtt an embedded track is not served: it is already in the stream.
	if r := f.do("GET", fmt.Sprintf("/api/v1/subtitles/%d", embedded), f.adminTok, nil); r.code != 404 {
		t.Fatalf("raw embedded fetch: %d, want 404", r.code)
	}

	// A sidecar SubRip converts on request.
	var sidecar int64
	if err := f.db.QueryRow(`SELECT id FROM media_streams WHERE version_id = ? AND is_external = 1`, f.v1080).Scan(&sidecar); err != nil {
		t.Fatal(err)
	}
	r = f.do("GET", fmt.Sprintf("/api/v1/subtitles/%d?format=vtt", sidecar), f.adminTok, nil)
	if r.code != 200 || !strings.HasPrefix(string(r.body), "WEBVTT") || !strings.Contains(string(r.body), "Hello") {
		t.Fatalf("sidecar as vtt: %d %s", r.code, r.body)
	}

	// Picture subtitles are refused with a reason, and policy still applies:
	// the friend's ceiling is 1080p, so the 4K version's track does not exist for them.
	if r := f.do("GET", fmt.Sprintf("/api/v1/subtitles/%d?format=vtt", pgs), f.adminTok, nil); r.code != 422 {
		t.Fatalf("pgs as vtt: %d %s, want 422", r.code, r.body)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/subtitles/%d?format=vtt", pgs), f.friendTok, nil); r.code != 404 {
		t.Fatalf("friend fetching a 4K track: %d, want 404", r.code)
	}
	if r := f.do("GET", url, f.strangerTok, nil); r.code != 404 {
		t.Fatalf("ungranted user fetching a subtitle: %d, want 404", r.code)
	}
}

func TestActivityNowHistoryStatsAndTerminate(t *testing.T) {
	f := newFixture(t)
	var mu sync.Mutex
	var hooks []map[string]any
	recv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m map[string]any
		_ = json.NewDecoder(r.Body).Decode(&m)
		mu.Lock()
		hooks = append(hooks, m)
		mu.Unlock()
	}))
	defer recv.Close()
	f.srv.Webhooks = notify.New([]string{recv.URL}, nil, "Test", nil)
	f.srv.Playback.Notify = f.srv.Webhooks.Send

	start := func(tok string, item int64) string {
		t.Helper()
		r := f.do("POST", "/api/v1/playback/sessions", tok, map[string]any{"item_id": item, "capabilities": tclCaps()})
		if r.code != 201 {
			t.Fatalf("start: %d %s", r.code, r.body)
		}
		var s struct {
			SessionID string `json:"session_id"`
		}
		r.json(t, &s)
		return s.SessionID
	}
	event := func(tok, id, typ string, pos int64) resp {
		t.Helper()
		return f.do("POST", "/api/v1/playback/sessions/"+id+"/events", tok, map[string]any{"type": typ, "position_ms": pos, "duration_ms": 6_000_000})
	}

	// The admin watches the movie and stops; the friend is mid-episode.
	movie := start(f.adminTok, f.movieID)
	event(f.adminTok, movie, "start", 0)
	event(f.adminTok, movie, "progress", 5_500_000)
	event(f.adminTok, movie, "stop", 5_500_000)
	ep := start(f.friendTok, f.episodeID)
	event(f.friendTok, ep, "start", 0)
	event(f.friendTok, ep, "progress", 60_000)
	// Credit real watch time, as heartbeats would have over an hour.
	if _, err := f.db.Exec(`UPDATE playback_sessions SET played_ms = 3600000 WHERE id IN (?, ?)`, movie, ep); err != nil {
		t.Fatal(err)
	}
	// A misclick: opened and stopped at once. It is history, not a counted play.
	blip := start(f.adminTok, f.episodeID)
	event(f.adminTok, blip, "start", 0)
	event(f.adminTok, blip, "stop", 4_000)

	// Admin only, and LAN only like the rest of /admin.
	if r := f.do("GET", "/api/v1/admin/activity/now", f.friendTok, nil); r.code != 403 {
		t.Fatalf("friend reading activity: %d", r.code)
	}

	var now activity.NowPlaying
	r := f.do("GET", "/api/v1/admin/activity/now", f.adminTok, nil)
	r.json(t, &now)
	if r.code != 200 || now.StreamCount != 1 || now.DirectPlay != 1 || now.Sessions[0].ID != ep || *now.Sessions[0].Username != "friend" ||
		now.Sessions[0].ShowID == nil || *now.Sessions[0].ShowID != f.showID {
		t.Fatalf("now playing: %d %s", r.code, r.body)
	}

	var hist activity.History
	f.do("GET", "/api/v1/admin/activity/history", f.adminTok, nil).json(t, &hist)
	if hist.Total != 2 || hist.Sessions[0].ID != blip || !hist.Sessions[1].Watched {
		t.Fatalf("history: %+v", hist)
	}
	f.do("GET", fmt.Sprintf("/api/v1/admin/activity/history?item_id=%d", f.showID), f.adminTok, nil).json(t, &hist)
	if hist.Total != 1 || hist.Sessions[0].ID != blip {
		t.Fatalf("history for the show (its episodes): %+v", hist)
	}

	var st activity.Stats
	f.do("GET", "/api/v1/admin/activity/stats?days=7", f.adminTok, nil).json(t, &st)
	if st.Plays != 2 || st.Users != 2 || len(st.TopMovies) != 1 || st.TopMovies[0].Name != "The Film" ||
		len(st.TopShows) != 1 || st.TopShows[0].Name != "Show" || len(st.ByDay) != 7 || st.ByDay[6].Plays != 2 {
		t.Fatalf("stats: %+v", st)
	}

	var users struct{ Users []activity.UserStat }
	f.do("GET", "/api/v1/admin/activity/users", f.adminTok, nil).json(t, &users)
	byName := map[string]activity.UserStat{}
	for _, u := range users.Users {
		byName[u.Username] = u
	}
	if byName["pete"].Plays != 1 || byName["friend"].Plays != 1 || byName["stranger"].Plays != 0 || byName["friend"].LastSeen == nil {
		t.Fatalf("users: %+v", users.Users)
	}

	// Stopping the friend's stream: the file and further events answer 410 with the message.
	r = f.do("POST", "/api/v1/admin/activity/sessions/"+ep+"/terminate", f.adminTok, map[string]any{"message": "Bedtime."})
	if r.code != 200 {
		t.Fatalf("terminate: %d %s", r.code, r.body)
	}
	if r := f.do("POST", "/api/v1/admin/activity/sessions/"+ep+"/terminate", f.adminTok, nil); r.code != 404 {
		t.Fatalf("terminating an ended session: %d, want 404", r.code)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/versions/%d/file?session=%s", f.episodeVerID, ep), f.friendTok, nil); r.code != 410 ||
		!strings.Contains(string(r.body), "Bedtime.") {
		t.Fatalf("stream after terminate: %d %s", r.code, r.body)
	}
	if r := event(f.friendTok, ep, "progress", 70_000); r.code != 410 || !strings.Contains(string(r.body), "Bedtime.") {
		t.Fatalf("event after terminate: %d %s", r.code, r.body)
	}
	f.do("GET", "/api/v1/admin/activity/now", f.adminTok, nil).json(t, &now)
	if now.StreamCount != 0 {
		t.Fatalf("terminated session still listed: %+v", now)
	}

	// Webhooks: play and stop for each session, then the termination.
	deadline := time.Now().Add(3 * time.Second)
	want := map[string]int{"play": 3, "stop": 2, "terminated": 1}
	for {
		mu.Lock()
		got := map[string]int{}
		for _, h := range hooks {
			got[h["event"].(string)]++
		}
		mu.Unlock()
		if fmt.Sprint(got) == fmt.Sprint(want) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("webhook events %v, want %v", got, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTerminateCutsAnOpenStream(t *testing.T) {
	var g streamRegistry
	ctx, cancel := context.WithCancel(context.Background())
	g.add("s1", &cancel)
	rd := ctxReader{ctx: ctx, rs: strings.NewReader("abcdef")}
	buf := make([]byte, 2)
	if n, err := rd.Read(buf); n != 2 || err != nil {
		t.Fatalf("read before cancel: %d %v", n, err)
	}
	if n := g.cancel("s1"); n != 1 {
		t.Fatalf("cancelled %d streams, want 1", n)
	}
	if _, err := rd.Read(buf); err == nil {
		t.Fatal("read after terminate should fail so ServeContent stops")
	}
	g.remove("s1", &cancel)
	if g.cancel("s1") != 0 {
		t.Fatal("removed stream still registered")
	}
}

func TestEpisodeNextIsTheFollowingEpisode(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UnixMilli()
	var tv int64
	if err := f.db.QueryRow(`SELECT library_id FROM items WHERE id = ?`, f.showID).Scan(&tv); err != nil {
		t.Fatal(err)
	}
	add := func(kind string, parent int64, season, ep any, title string) int64 {
		t.Helper()
		res, err := f.db.Exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, episode_number, added_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, tv, kind, parent, title, title, title, season, ep, now, now)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		if kind == "episode" {
			// Episodes without a file are hidden; give each one.
			if _, err := f.db.Exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, container, probe_status, added_at, updated_at)
				VALUES (?, ?, ?, ?, 1, 0, 'mkv', 'ok', ?, ?)`, id, tv, "/x/"+title+".mkv", title+".mkv", now, now); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}
	e2 := add("episode", f.seasonID, 1, 2, "Second")
	s2 := add("season", f.showID, 2, nil, "Season 2")
	s2e1 := add("episode", s2, 2, 1, "Opener")
	sp := add("season", f.showID, 0, nil, "Specials")
	sp1 := add("episode", sp, 0, 1, "Special one")
	next := func(id int64) *int64 {
		t.Helper()
		var d struct {
			Next *struct{ ID int64 } `json:"next_episode"`
		}
		r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", id), f.adminTok, nil)
		if r.code != 200 {
			t.Fatalf("item %d: %d", id, r.code)
		}
		r.json(t, &d)
		if d.Next == nil {
			return nil
		}
		return &d.Next.ID
	}
	if n := next(f.episodeID); n == nil || *n != e2 {
		t.Fatalf("after S01E01: %v, want %d", n, e2)
	}
	if n := next(e2); n == nil || *n != s2e1 {
		t.Fatalf("after S01E02 the next season starts: %v, want %d (specials are skipped)", n, s2e1)
	}
	if n := next(s2e1); n != nil {
		t.Fatalf("end of the show: %v, want none", *n)
	}
	if n := next(sp1); n != nil {
		t.Fatalf("after the only special: %v, want none", *n)
	}
}

func TestHostCheckStopsDNSRebinding(t *testing.T) {
	f := newFixture(t)
	p, _ := netip.ParsePrefix("192.168.1.0/24")
	f.srv.AutoSignInUser, f.srv.TrustedNetworks = "pete", []netip.Prefix{p}
	f.h = f.srv.Handler()
	call := func(host, path string) int {
		t.Helper()
		req := httptest.NewRequest("POST", path, strings.NewReader(`{"device":{"id":"x","name":"x"}}`))
		req.Host = host
		req.RemoteAddr = "192.168.1.50:1"
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, req)
		return w.Code
	}
	// A page on another site that points its name at the server is refused,
	// though the request comes from a trusted LAN address.
	for _, host := range []string{"evil.example", "evil.example:3750", "rebind.attacker.test"} {
		if code := call(host, "/api/v1/auth/auto"); code != http.StatusMisdirectedRequest {
			t.Errorf("Host %s: %d, want 421", host, code)
		}
	}
	// IP addresses, localhost and configured names pass.
	for _, host := range []string{"192.168.1.20:3750", "192.168.1.20", "[fd7a:115c:a1e0::1]:3750", "localhost:3750", "example.com", "EXAMPLE.COM."} {
		if code := call(host, "/api/v1/auth/auto"); code != 200 {
			t.Errorf("Host %s: %d, want 200", host, code)
		}
	}
}

func TestClientIPBehindProxyFailsClosed(t *testing.T) {
	f := newFixture(t)
	proxy, _ := netip.ParsePrefix("127.0.0.1/32")
	lan, _ := netip.ParsePrefix("127.0.0.0/8")
	f.srv.TrustedProxies = []netip.Prefix{proxy}
	f.srv.AutoSignInUser, f.srv.TrustedNetworks = "pete", []netip.Prefix{lan}
	ip := func(xff ...string) string {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "127.0.0.1:9"
		for _, v := range xff {
			req.Header.Add("X-Forwarded-For", v)
		}
		return f.srv.clientIP(req)
	}
	// A proxy that sends no usable header must not make its clients look like
	// the proxy (loopback here, which the auto sign-in networks trust).
	for _, xff := range [][]string{nil, {"garbage"}, {"1.2.3.4:5678"}} {
		if got := ip(xff...); got != unknownClient || f.srv.autoSignIn(got) {
			t.Errorf("XFF %v: client %q", xff, got)
		}
	}
	if got := ip("8.8.8.8"); got != "8.8.8.8" {
		t.Errorf("one hop: %q", got)
	}
	// Several header lines are read as one list; the right-most untrusted hop wins.
	if got := ip("10.9.9.9", "8.8.4.4"); got != "8.8.4.4" {
		t.Errorf("two headers: %q", got)
	}
}

func TestCollectionsAndFilters(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UnixMilli()
	var movies int64
	if err := f.db.QueryRow(`SELECT library_id FROM items WHERE id = ?`, f.movieID).Scan(&movies); err != nil {
		t.Fatal(err)
	}
	res, err := f.db.Exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, year, tmdb_collection_id, added_at, updated_at)
		VALUES (?, 'movie', 'sequel|2005', 'The Sequel', 'Sequel', 2005, 77, ?, ?)`, movies, now, now)
	if err != nil {
		t.Fatal(err)
	}
	sequel, _ := res.LastInsertId()
	if _, err := f.db.Exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, container, resolution, probe_status, added_at, updated_at)
		VALUES (?, ?, '/x/s.mkv', 's.mkv', 1, 0, 'mkv', '1080p', 'ok', ?, ?)`, sequel, movies, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE items SET tmdb_collection_id = 77 WHERE id = ?`, f.movieID); err != nil {
		t.Fatal(err)
	}
	b := &collections.Builder{DB: f.db, Names: func(ctx context.Context, id int64) (string, error) { return "The Film Collection", nil }}
	if r, err := b.Build(context.Background()); err != nil || r.Collections != 1 {
		t.Fatalf("build: %+v %v", r, err)
	}

	var list struct{ Collections []catalog.Collection }
	f.do("GET", fmt.Sprintf("/api/v1/libraries/%d/collections", movies), f.adminTok, nil).json(t, &list)
	if len(list.Collections) != 1 || list.Collections[0].ItemCount != 2 || list.Collections[0].Name != "The Film Collection" {
		t.Fatalf("collections: %+v", list)
	}
	id := list.Collections[0].ID
	var one struct {
		Collection catalog.Collection
		Items      []catalog.ItemSummary
	}
	f.do("GET", fmt.Sprintf("/api/v1/collections/%d", id), f.adminTok, nil).json(t, &one)
	if len(one.Items) != 2 || one.Items[0].ID != f.movieID || one.Items[1].ID != sequel {
		t.Fatalf("members in release order: %+v", one.Items)
	}
	var detail struct{ Collections []catalog.CollectionRef }
	f.do("GET", fmt.Sprintf("/api/v1/items/%d", sequel), f.adminTok, nil).json(t, &detail)
	if len(detail.Collections) != 1 || detail.Collections[0].ID != id {
		t.Fatalf("item detail collections: %+v", detail.Collections)
	}
	// Someone without the library sees neither the collection nor its list.
	if r := f.do("GET", fmt.Sprintf("/api/v1/collections/%d", id), f.strangerTok, nil); r.code != 404 {
		t.Fatalf("stranger reading a collection: %d", r.code)
	}

	var filters catalog.Filters
	f.do("GET", fmt.Sprintf("/api/v1/libraries/%d/filters", movies), f.adminTok, nil).json(t, &filters)
	if len(filters.Genres) != 1 || filters.Genres[0] != (catalog.Count{Value: "Drama", Count: 1}) ||
		len(filters.Decades) != 1 || filters.Decades[0] != (catalog.Count{Value: "2000s", Count: 2}) {
		t.Fatalf("filters: %+v", filters)
	}
	var page struct{ Total int }
	f.do("GET", fmt.Sprintf("/api/v1/libraries/%d/items?decade=2000", movies), f.adminTok, nil).json(t, &page)
	if page.Total != 2 {
		t.Fatalf("decade 2000: %d", page.Total)
	}
	f.do("GET", fmt.Sprintf("/api/v1/libraries/%d/items?decade=1990", movies), f.adminTok, nil).json(t, &page)
	if page.Total != 0 {
		t.Fatalf("decade 1990: %d", page.Total)
	}
}

// Remove from Continue Watching: the resume position goes, the item stays
// unplayed, and only the caller's own watch state changes.
func TestClearProgressLeavesContinueWatching(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UnixMilli()
	for _, u := range []string{"pete", "friend"} {
		if _, err := f.db.Exec(`INSERT INTO watch_state (user_id, item_id, position_ms, duration_ms, played, play_count, last_played_at, updated_at)
			VALUES ((SELECT id FROM users WHERE username = ?), ?, 1_200_000, 6_000_000, 0, 0, ?, ?)`, u, f.movieID, now, now); err != nil {
			t.Fatal(err)
		}
	}
	continuing := func(tok string) bool {
		t.Helper()
		var home struct{ Rows []catalog.HomeRow }
		f.do("GET", "/api/v1/home", tok, nil).json(t, &home)
		for _, row := range home.Rows {
			if row.ID != "continue_watching" {
				continue
			}
			for _, it := range row.Items {
				if it.ID == f.movieID {
					return true
				}
			}
		}
		return false
	}
	if !continuing(f.adminTok) || !continuing(f.friendTok) {
		t.Fatal("setup: the movie should be in both users' Continue Watching")
	}
	url := fmt.Sprintf("/api/v1/items/%d/progress", f.movieID)

	// A user who cannot see the item gets 404, and nobody's state moves.
	if r := f.do("DELETE", url, f.strangerTok, nil); r.code != 404 {
		t.Fatalf("stranger cleared an item it cannot see: %d %s", r.code, r.body)
	}
	if r := f.do("DELETE", "/api/v1/items/999999/progress", f.adminTok, nil); r.code != 404 {
		t.Fatalf("missing item: %d %s", r.code, r.body)
	}
	if !continuing(f.adminTok) || !continuing(f.friendTok) {
		t.Fatal("a refused clear changed someone's Continue Watching")
	}

	// The friend clears their own: it leaves their row, not the admin's.
	if r := f.do("DELETE", url, f.friendTok, nil); r.code != 204 {
		t.Fatalf("clear: %d %s", r.code, r.body)
	}
	if continuing(f.friendTok) {
		t.Fatal("the movie is still in the friend's Continue Watching")
	}
	if !continuing(f.adminTok) {
		t.Fatal("clearing the friend's progress removed it from the admin's Continue Watching")
	}
	var d catalog.ItemDetail
	f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), f.friendTok, nil).json(t, &d)
	if d.Watch.Played || d.Watch.PositionMs != 0 || d.Watch.PlayCount != 0 {
		t.Fatalf("after clearing, friend watch state: %+v", d.Watch)
	}
	f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), f.adminTok, nil).json(t, &d)
	if d.Watch.PositionMs != 1_200_000 {
		t.Fatalf("admin position changed to %d", d.Watch.PositionMs)
	}
	// Clearing again, or an item never started, is still fine.
	if r := f.do("DELETE", url, f.friendTok, nil); r.code != 204 {
		t.Fatalf("second clear: %d", r.code)
	}
	if r := f.do("DELETE", fmt.Sprintf("/api/v1/items/%d/progress", f.episodeID), f.friendTok, nil); r.code != 204 {
		t.Fatalf("clear an unstarted episode: %d", r.code)
	}
}

func TestContinueWatchingOffersTheNextEpisode(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UnixMilli()
	var tv int64
	if err := f.db.QueryRow(`SELECT library_id FROM items WHERE id = ?`, f.showID).Scan(&tv); err != nil {
		t.Fatal(err)
	}
	res, err := f.db.Exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, episode_number, added_at, updated_at)
		VALUES (?, 'episode', ?, '2', 'Second', 'Second', 1, 2, ?, ?)`, tv, f.seasonID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	e2, _ := res.LastInsertId()
	if _, err := f.db.Exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, container, probe_status, added_at, updated_at)
		VALUES (?, ?, '/x/e2.mkv', 'e2.mkv', 1, 0, 'mkv', 'ok', ?, ?)`, e2, tv, now, now); err != nil {
		t.Fatal(err)
	}
	cont := func() []int64 {
		t.Helper()
		var home struct {
			Rows []struct {
				ID    string
				Items []struct{ ID int64 }
			}
		}
		f.do("GET", "/api/v1/home", f.adminTok, nil).json(t, &home)
		var ids []int64
		for _, r := range home.Rows {
			if r.ID == "continue_watching" {
				for _, i := range r.Items {
					ids = append(ids, i.ID)
				}
			}
		}
		return ids
	}
	if got := cont(); len(got) != 0 {
		t.Fatalf("nothing watched yet: %v", got)
	}
	// Finishing S01E01 offers S01E02.
	if r := f.do("PUT", fmt.Sprintf("/api/v1/items/%d/watched", f.episodeID), f.adminTok, map[string]any{"played": true}); r.code != 204 {
		t.Fatalf("mark watched: %d", r.code)
	}
	if got := cont(); len(got) != 1 || got[0] != e2 {
		t.Fatalf("after S01E01: %v, want [%d]", got, e2)
	}
	// Once S01E02 is under way it shows once, as in progress, not twice.
	if _, err := f.db.Exec(`INSERT INTO watch_state (user_id, item_id, position_ms, played, play_count, last_played_at, updated_at)
		SELECT id, ?, 60000, 0, 0, ?, ? FROM users WHERE username = 'pete'`, e2, now+1, now+1); err != nil {
		t.Fatal(err)
	}
	if got := cont(); len(got) != 1 || got[0] != e2 {
		t.Fatalf("with S01E02 in progress: %v", got)
	}
	// An episode finished long ago no longer offers anything.
	if _, err := f.db.Exec(`DELETE FROM watch_state WHERE item_id = ?`, e2); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE watch_state SET last_played_at = ? WHERE item_id = ?`, now-90*24*3600*1000, f.episodeID); err != nil {
		t.Fatal(err)
	}
	if got := cont(); len(got) != 0 {
		t.Fatalf("after 90 days: %v", got)
	}
}

// Remove on a next-episode card keeps it away, including a removed episode
// that was in progress, until another episode of the show is finished.
func TestRemoveNextEpisodeStaysRemoved(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UnixMilli()
	var tv int64
	if err := f.db.QueryRow(`SELECT library_id FROM items WHERE id = ?`, f.showID).Scan(&tv); err != nil {
		t.Fatal(err)
	}
	var eps []int64
	for n := 2; n <= 3; n++ {
		res, err := f.db.Exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, episode_number, added_at, updated_at)
			VALUES (?, 'episode', ?, ?, ?, ?, 1, ?, ?, ?)`, tv, f.seasonID, fmt.Sprint(n), fmt.Sprint("Ep ", n), fmt.Sprint("Ep ", n), n, now, now)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		if _, err := f.db.Exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, container, probe_status, added_at, updated_at)
			VALUES (?, ?, ?, ?, 1, 0, 'mkv', 'ok', ?, ?)`, id, tv, fmt.Sprintf("/x/e%d.mkv", n), fmt.Sprintf("e%d.mkv", n), now, now); err != nil {
			t.Fatal(err)
		}
		eps = append(eps, id)
	}
	e2, e3 := eps[0], eps[1]
	cont := func() []int64 {
		t.Helper()
		var home struct {
			Rows []struct {
				ID    string
				Items []struct{ ID int64 }
			}
		}
		f.do("GET", "/api/v1/home", f.adminTok, nil).json(t, &home)
		var ids []int64
		for _, r := range home.Rows {
			if r.ID == "continue_watching" {
				for _, i := range r.Items {
					ids = append(ids, i.ID)
				}
			}
		}
		return ids
	}
	remove := func(id int64) {
		t.Helper()
		if r := f.do("DELETE", fmt.Sprintf("/api/v1/items/%d/progress", id), f.adminTok, nil); r.code != 204 {
			t.Fatalf("remove %d: %d %s", id, r.code, r.body)
		}
	}
	finish := func(id, at int64) {
		t.Helper()
		if r := f.do("PUT", fmt.Sprintf("/api/v1/items/%d/watched", id), f.adminTok, map[string]any{"played": true}); r.code != 204 {
			t.Fatalf("mark %d watched: %d", id, r.code)
		}
		// Server clocks tie at millisecond resolution; place the finish clearly
		// before or after the removal.
		if _, err := f.db.Exec(`UPDATE watch_state SET last_played_at = ? WHERE item_id = ?`, at, id); err != nil {
			t.Fatal(err)
		}
	}

	finish(f.episodeID, now-60_000)
	if got := cont(); len(got) != 1 || got[0] != e2 {
		t.Fatalf("after S01E01: %v, want [%d]", got, e2)
	}
	remove(e2)
	if got := cont(); len(got) != 0 {
		t.Fatalf("the removed next-episode card came back: %v", got)
	}

	// Started, then removed: it must not return as the show's next episode.
	if _, err := f.db.Exec(`INSERT INTO watch_state (user_id, item_id, position_ms, played, play_count, last_played_at, updated_at)
		SELECT id, ?, 60000, 0, 0, ?, ? FROM users WHERE username = 'pete'`, e2, now, now); err != nil {
		t.Fatal(err)
	}
	if got := cont(); len(got) != 1 || got[0] != e2 {
		t.Fatalf("with S01E02 in progress: %v", got)
	}
	remove(e2)
	if got := cont(); len(got) != 0 {
		t.Fatalf("a removed in-progress episode came back as next up: %v", got)
	}

	// Finishing another episode afterwards offers the one after it again.
	finish(e2, now+60_000)
	if got := cont(); len(got) != 1 || got[0] != e3 {
		t.Fatalf("after finishing S01E02: %v, want [%d]", got, e3)
	}
}

// The details page lists the cast in billing order, an episode's guests before
// its show's regulars, each actor once.
func TestItemDetailCast(t *testing.T) {
	f := newFixture(t)
	add := func(item int64, name, role string, pos int) {
		t.Helper()
		var r any
		if role != "" {
			r = role
		}
		if _, err := f.db.Exec(`INSERT INTO item_tags (item_id, kind, value, position, role) VALUES (?, 'actor', ?, ?, ?)`, item, name, pos, r); err != nil {
			t.Fatal(err)
		}
	}
	add(f.movieID, "Second Billed", "", 1)
	add(f.movieID, "Top Billed", "The Lead", 0)
	add(f.showID, "Series Regular", "Neighbour", 0)
	add(f.showID, "Shared Actor", "Regular Part", 1)
	add(f.episodeID, "Shared Actor", "Guest Part", 0)
	add(f.episodeID, "One-Off Guest", "", 1)
	type member struct {
		Name string
		Role *string
	}
	cast := func(id int64) []string {
		t.Helper()
		var d struct{ Cast []member }
		f.do("GET", fmt.Sprintf("/api/v1/items/%d", id), f.adminTok, nil).json(t, &d)
		var out []string
		for _, c := range d.Cast {
			s := c.Name
			if c.Role != nil {
				s += "/" + *c.Role
			}
			out = append(out, s)
		}
		return out
	}
	if got, want := strings.Join(cast(f.movieID), ", "), "Top Billed/The Lead, Second Billed"; got != want {
		t.Fatalf("movie cast: %s, want %s", got, want)
	}
	if got, want := strings.Join(cast(f.episodeID), ", "), "Shared Actor/Guest Part, One-Off Guest, Series Regular/Neighbour"; got != want {
		t.Fatalf("episode cast: %s, want %s", got, want)
	}
	var d struct{ Cast []member }
	f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.seasonID), f.adminTok, nil).json(t, &d)
	if d.Cast == nil {
		t.Fatal("cast should be an empty list, never null")
	}
}
