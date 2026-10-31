package api

import (
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/household"
	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
)

// The access walk (REFERENCE.md section 10: "Policies filter every query and
// every stream URL server-side"). It calls every route a signed-in user can
// reach, as a friend, against things the friend's policy hides: a title in a
// library they are not granted, titles carrying a denied label, titles rated
// above their ceiling, and the 4K version of a title they may watch in 1080p.
//
// Each call runs twice with the same URL and the same user. First the friend's
// policy is wide open, and the call must succeed: that proves the URL really
// reaches the thing, so a 404 later cannot be a typo or a missing fixture. Then
// the policy is tightened, and the call must answer 404 (or, for a list, stop
// mentioning the hidden things).
//
// A route added to Routes() without a probe here fails the test by name. Add a
// probe that calls it for something hidden (or, if it reads no items at all,
// list it in noItemRoutes with the reason) and make the handler apply the
// caller's policy until the probe passes.

// publicRoutes answer without a session and carry no titles.
var publicRoutes = map[string]string{
	"GET /healthz":             "liveness only",
	"GET /readyz":              "database and mount health, no titles",
	"GET /api/v1/openapi.yaml": "the API description",
	"GET /api/v1/server":       "server name and version",
	"POST /api/v1/auth/login":  "sign-in",
	"POST /api/v1/auth/auto":   "trusted-network sign-in",
	"POST /api/v1/hooks/arr":   "Radarr and Sonarr webhooks, token-guarded, start scans and return no titles",
}

// noItemRoutes are signed-in routes that read nothing about titles.
var noItemRoutes = map[string]string{
	"POST /api/v1/auth/logout": "ends the caller's own session",
	"GET /api/v1/me":           "the caller's own account",
	// Skip Intro: the caller's own choice of button, auto or off.
	"GET /api/v1/me/playback-settings": "the caller's own playback settings",
	"PUT /api/v1/me/playback-settings": "the caller's own playback settings",
	"GET /api/v1/speedtest":            "random filler bytes for timing the connection; reads nothing from the library",
	// The Home layout is the caller's own list of row kinds ("trending",
	// "seasonal") with fixed names and descriptions; it names no titles.
	"GET /api/v1/layout/home":    "the caller's own Home row order, row kinds only",
	"PUT /api/v1/layout/home":    "saves the caller's own Home row order, row kinds only",
	"DELETE /api/v1/layout/home": "resets the caller's own Home row order, row kinds only",
}

// listParamRoutes take a path parameter that is not an item, version, session
// or other id the policy can hide, so a list probe (hides) may cover them
// instead of a 404. Keep this narrow: a route whose parameter names a title,
// file or stream must prove it answers 404 for a hidden one.
var listParamRoutes = map[string]string{
	"GET /api/v1/rows/{row_id}": "a Home row id (trending, genre:Drama); the row lists titles under the caller's policy",
}

type probe struct {
	route string // "METHOD /pattern" exactly as registered
	path  string // the concrete URL
	body  any
	// owner, when set, is the token the URL belongs to (another user's
	// playback session): the control call runs as the owner, and the friend
	// is refused in both phases.
	owner string
	// list probes: text the open friend sees and the restricted friend must not.
	list []string
	// pastPolicy, when set, is text in a 404 that only a handler step after
	// the policy check writes (an HLS run that has not started). The open
	// friend's 404 must carry it, proving the call got past the policy; the
	// restricted friend's 404 must not.
	pastPolicy string
}

var routeParam = regexp.MustCompile(`\{[a-z_]+\}`)

// at builds a probe for route with its path parameters filled in order.
func at(route string, args ...any) probe {
	_, pattern, _ := strings.Cut(route, " ")
	i := 0
	path := routeParam.ReplaceAllStringFunc(pattern, func(string) string {
		if i >= len(args) {
			return "{missing}"
		}
		i++
		return fmt.Sprint(args[i-1])
	})
	return probe{route: route, path: path}
}

func (p probe) with(body any) probe        { p.body = body; return p }
func (p probe) query(q string) probe       { p.path += "?" + q; return p }
func (p probe) ownedBy(tok string) probe   { p.owner = tok; return p }
func (p probe) hides(text ...string) probe { p.list = text; return p }
func (p probe) past(text string) probe     { p.pastPolicy = text; return p }

func (p probe) method() string { m, _, _ := strings.Cut(p.route, " "); return m }

// walkFixture adds the hidden things to the standard fixture.
type walkFixture struct {
	*fixture
	movies, tv, vault int64
	friendID          int64
	// Hidden from the restricted friend, each for a different reason.
	vaultMovie, horrorMovie, ratedMovie     int64 // ungranted library, denied label, rated R
	horrorShow, horrorSeason, horrorEpisode int64 // denied label on the show
	strictSeason, strictEpisode             int64 // TV-MA episode of a TV-14 show
	mixedCollection, hiddenCollection       int64
	hiddenVersions                          []int64 // one per hidden playable, plus the film's 4K
	hiddenSubs                              []int64
	posters                                 []int64
	// Extras hidden with their film (a denied label) or by the ceiling (4K).
	hiddenExtras []int64
}

func newWalkFixture(t *testing.T) *walkFixture {
	t.Helper()
	w := &walkFixture{fixture: newFixture(t)}
	f := w.fixture
	withHLS(t, f, remux.New("ffmpeg", "ffprobe", 2, nil))
	now := time.Now().UnixMilli()
	exec := func(q string, args ...any) int64 {
		t.Helper()
		res, err := f.db.Exec(q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	row := func(q string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := f.db.QueryRow(q, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return id
	}
	w.movies = row(`SELECT id FROM libraries WHERE name = 'Movies'`)
	w.tv = row(`SELECT id FROM libraries WHERE name = 'TV'`)
	w.friendID = row(`SELECT id FROM users WHERE username = 'friend'`)
	tvRoot := filepath.Join(filepath.Dir(f.root), "TV")
	vaultRoot := filepath.Join(filepath.Dir(f.root), "Vault")
	w.vault = exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('Vault', 'movie', ?, ?, ?)`, vaultRoot, now, now)
	horror := exec(`INSERT INTO labels (name, hidden) VALUES ('horror', 1)`)

	// Every hidden playable gets a real file, a sidecar subtitle and an
	// eac3 track (so a browser gets a converted stream), and every hidden
	// title a poster, so the open friend's calls all succeed.
	jpg := func(path string) {
		t.Helper()
		fh, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := jpeg.Encode(fh, image.NewRGBA(image.Rect(0, 0, 20, 30)), nil); err != nil {
			t.Fatal(err)
		}
		_ = fh.Close()
	}
	art := func(item int64, kind, dir string) {
		p := filepath.Join(dir, kind+".jpg")
		jpg(p)
		exec(`INSERT INTO artwork (item_id, kind, source, source_path, updated_at) VALUES (?, ?, 'local', ?, ?)`, item, kind, p, now)
	}
	playable := func(item, lib int64, dir, name, res string) int64 {
		t.Helper()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, name+".mkv")
		if err := os.WriteFile(p, content(10_000), 0o644); err != nil {
			t.Fatal(err)
		}
		v := exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, container, duration_ms, video_codec,
				video_profile, bit_depth, width, height, resolution, probe_status, added_at, updated_at)
			VALUES (?, ?, ?, ?, 10000, 0, 'mkv', 6000000, 'hevc', 'main 10', 10, 1920, 1080, ?, 'ok', ?, ?)`,
			item, lib, p, filepath.Base(p), res, now, now)
		exec(`INSERT INTO media_streams (version_id, stream_index, kind, codec, channels, is_default) VALUES (?, 1, 'audio', 'eac3', 6, 1)`, v)
		srt := filepath.Join(dir, name+".en.srt")
		if err := os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:02,000\nHidden\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		w.hiddenSubs = append(w.hiddenSubs, exec(`INSERT INTO media_streams (version_id, kind, codec, language, is_external, external_path)
			VALUES (?, 'subtitle', 'subrip', 'eng', 1, ?)`, v, srt))
		w.hiddenVersions = append(w.hiddenVersions, v)
		return v
	}
	movie := func(lib int64, root, title, rating string) int64 {
		t.Helper()
		id := exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, year, content_rating, added_at, updated_at)
			VALUES (?, 'movie', ?, ?, ?, 2010, ?, ?, ?)`, lib, title, title, title, rating, now, now)
		exec(`INSERT INTO item_tags (item_id, kind, value) VALUES (?, 'genre', 'Slasher')`, id)
		dir := filepath.Join(root, title)
		playable(id, lib, dir, title, "1080p")
		art(id, "poster", dir)
		w.posters = append(w.posters, id)
		return id
	}

	// The film and the show stay visible: PG-13 and TV-14.
	exec(`UPDATE items SET content_rating = 'PG-13' WHERE id = ?`, f.movieID)
	exec(`UPDATE items SET content_rating = 'TV-14' WHERE id = ?`, f.showID)
	// Its 4K version is hidden from a 1080p friend, subtitles and all.
	w.hiddenVersions = append(w.hiddenVersions, f.v4k)
	exec(`INSERT INTO media_streams (version_id, stream_index, kind, codec, channels, is_default) VALUES (?, 1, 'audio', 'eac3', 6, 1)`, f.v4k)
	sub4k := filepath.Join(f.root, "Film 2001 2160p", "film.2160p.en.srt")
	if err := os.WriteFile(sub4k, []byte("1\n00:00:01,000 --> 00:00:02,000\n4K\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.hiddenSubs = append(w.hiddenSubs, exec(`INSERT INTO media_streams (version_id, kind, codec, language, is_external, external_path)
		VALUES (?, 'subtitle', 'subrip', 'eng', 1, ?)`, f.v4k, sub4k))

	w.vaultMovie = movie(w.vault, vaultRoot, "Zebra Vault", "PG")
	w.horrorMovie = movie(w.movies, f.root, "Zebra Night", "PG-13")
	exec(`INSERT INTO item_labels (item_id, label_id) VALUES (?, ?)`, w.horrorMovie, horror)
	w.ratedMovie = movie(w.movies, f.root, "Zebra Rated", "R")

	// A show with the denied label: its seasons and episodes inherit it.
	w.horrorShow = exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, content_rating, added_at, updated_at)
		VALUES (?, 'show', 'zebra street', 'Zebra Street', 'Zebra Street', 'TV-14', ?, ?)`, w.tv, now, now)
	exec(`INSERT INTO item_labels (item_id, label_id) VALUES (?, ?)`, w.horrorShow, horror)
	w.horrorSeason = exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, added_at, updated_at)
		VALUES (?, 'season', ?, '1', 'Season 1', 'Season 1', 1, ?, ?)`, w.tv, w.horrorShow, now, now)
	w.horrorEpisode = exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, episode_number, added_at, updated_at)
		VALUES (?, 'episode', ?, '1', 'Zebra Street Pilot', 'Zebra Street Pilot', 1, 1, ?, ?)`, w.tv, w.horrorSeason, now, now)
	streetDir := filepath.Join(tvRoot, "Zebra Street", "Season 1")
	playable(w.horrorEpisode, w.tv, streetDir, "Zebra.Street.S01E01", "1080p")
	art(w.horrorShow, "poster", filepath.Join(tvRoot, "Zebra Street"))
	art(w.horrorEpisode, "thumb", streetDir)
	w.posters = append(w.posters, w.horrorShow, w.horrorSeason)

	// A second season of the visible show whose one episode is rated TV-MA:
	// stricter than its show, so hidden on its own.
	w.strictSeason = exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, added_at, updated_at)
		VALUES (?, 'season', ?, '2', 'Season 2', 'Season 2', 2, ?, ?)`, w.tv, f.showID, now, now)
	w.strictEpisode = exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, episode_number,
			content_rating, added_at, updated_at)
		VALUES (?, 'episode', ?, '1', 'Zebra Special', 'Zebra Special', 2, 1, 'TV-MA', ?, ?)`, w.tv, w.strictSeason, now, now)
	strictDir := filepath.Join(tvRoot, "Show", "Season 2")
	playable(w.strictEpisode, w.tv, strictDir, "Show.S02E01", "1080p")
	art(w.strictEpisode, "thumb", strictDir)

	coll := func(name string, items ...int64) int64 {
		id := exec(`INSERT INTO collections (library_id, slug, name, sort_title, source, updated_at) VALUES (?, ?, ?, ?, 'auto', ?)`,
			w.movies, strings.ToLower(strings.ReplaceAll(name, " ", "-")), name, name, now)
		for i, it := range items {
			exec(`INSERT INTO collection_items (collection_id, item_id, position) VALUES (?, ?, ?)`, id, it, i)
		}
		return id
	}
	// A trailer of the labelled film, and a 4K featurette of the visible one:
	// extras follow their film's visibility and the resolution ceiling.
	extra := func(item, lib int64, dir, name, kind, res string) int64 {
		t.Helper()
		p := filepath.Join(dir, name+".mkv")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, content(10_000), 0o644); err != nil {
			t.Fatal(err)
		}
		return exec(`INSERT INTO item_extras (item_id, library_id, kind, title, path, rel_path, size, mtime, container, duration_ms,
				video_codec, video_profile, bit_depth, width, height, resolution, audio_streams, probe_status, added_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 10000, 0, 'mkv', 90000, 'hevc', 'main 10', 10, 1920, 1080, ?,
				'[{"index":1,"codec":"eac3","channels":6,"default":true}]', 'ok', ?, ?)`,
			item, lib, kind, name, p, filepath.Base(p), res, now, now)
	}
	w.hiddenExtras = append(w.hiddenExtras,
		extra(w.horrorMovie, w.movies, filepath.Join(f.root, "Zebra Night", "Trailers"), "Zebra Teaser", "trailer", "1080p"),
		extra(f.movieID, w.movies, filepath.Join(f.root, "Film 2001 2160p", "Featurettes"), "Making Of 4K", "featurette", "4K"))
	w.hiddenCollection = coll("Zebra Collection", w.horrorMovie, w.ratedMovie)
	w.mixedCollection = coll("Mixed Collection", f.movieID, w.horrorMovie)
	return w
}

// open lets the friend see everything; restrict applies the policy under test.
func (w *walkFixture) open() {
	w.setAccess([]int64{w.movies, w.tv, w.vault}, map[string]any{"allow_transcode": true, "allow_4k_transcode": true})
}

func (w *walkFixture) restrict() {
	w.setAccess([]int64{w.movies, w.tv}, map[string]any{"max_resolution": "1080p", "max_content_rating": "PG-13",
		"deny_labels": []string{"horror"}, "allow_transcode": true, "allow_4k_transcode": true})
}

func (w *walkFixture) setAccess(libs []int64, pol map[string]any) {
	w.t.Helper()
	if r := w.do("PUT", fmt.Sprintf("/api/v1/admin/users/%d/grants", w.friendID), w.adminTok, map[string]any{"library_ids": libs}); r.code != 204 {
		w.t.Fatalf("grants: %d %s", r.code, r.body)
	}
	if r := w.do("PUT", fmt.Sprintf("/api/v1/admin/users/%d/policy", w.friendID), w.adminTok, pol); r.code != 204 {
		w.t.Fatalf("policy: %d %s", r.code, r.body)
	}
}

// startSession opens a converted-stream session and returns its id.
func (w *walkFixture) startSession(tok string, item int64, version *int64) string {
	w.t.Helper()
	body := map[string]any{"item_id": item, "capabilities": browserCaps(true)}
	if version != nil {
		body["version_id"] = *version
	}
	r := w.do("POST", "/api/v1/playback/sessions", tok, body)
	var out struct {
		SessionID string `json:"session_id"`
		Stream    struct{ Protocol string }
	}
	r.json(w.t, &out)
	if r.code != 201 || out.Stream.Protocol != "fmp4" {
		w.t.Fatalf("session on item %d: %d %s", item, r.code, r.body)
	}
	return out.SessionID
}

// startExtraSession opens a converted-stream session on an extra.
func (w *walkFixture) startExtraSession(tok string, extra int64) string {
	w.t.Helper()
	r := w.do("POST", "/api/v1/playback/sessions", tok, map[string]any{"extra_id": extra, "capabilities": browserCaps(true)})
	var out struct {
		SessionID string `json:"session_id"`
		Stream    struct{ Protocol string }
	}
	r.json(w.t, &out)
	if r.code != 201 || out.Stream.Protocol != "fmp4" {
		w.t.Fatalf("session on extra %d: %d %s", extra, r.code, r.body)
	}
	return out.SessionID
}

// probes lists every call the walk makes. sessions are the friend's, opened
// while the friend could still play the hidden things; adminSession is not the
// friend's at all.
func (w *walkFixture) probes(sessions []string, adminSession string) []probe {
	f := w.fixture
	hiddenItems := []int64{w.vaultMovie, w.horrorMovie, w.ratedMovie, w.horrorShow, w.horrorSeason, w.horrorEpisode, w.strictSeason, w.strictEpisode}
	hiddenPlayables := []int64{w.vaultMovie, w.horrorMovie, w.ratedMovie, w.horrorEpisode, w.strictEpisode}
	var ps []probe
	for _, id := range hiddenItems {
		ps = append(ps,
			at("GET /api/v1/items/{item_id}", id),
			at("PUT /api/v1/items/{item_id}/watched", id).with(map[string]any{"played": true}),
			at("DELETE /api/v1/items/{item_id}/progress", id))
	}
	for _, id := range []int64{w.horrorShow, w.horrorSeason, w.strictSeason} {
		ps = append(ps, at("GET /api/v1/items/{item_id}/children", id))
	}
	for _, id := range w.posters {
		ps = append(ps, at("GET /api/v1/items/{item_id}/artwork/{kind}", id, "poster"))
	}
	for _, id := range []int64{w.horrorEpisode, w.strictEpisode} {
		ps = append(ps, at("GET /api/v1/items/{item_id}/artwork/{kind}", id, "thumb"))
	}
	for _, id := range append(hiddenPlayables, w.horrorShow) {
		ps = append(ps, at("POST /api/v1/playback/sessions").with(map[string]any{"item_id": id, "capabilities": tclCaps()}))
	}
	ps = append(ps, at("POST /api/v1/playback/sessions").with(map[string]any{"item_id": f.movieID, "version_id": f.v4k, "capabilities": tclCaps()}))
	for _, v := range w.hiddenVersions {
		ps = append(ps, at("GET /api/v1/versions/{version_id}/file", v), at("HEAD /api/v1/versions/{version_id}/file", v))
		// Subtitle search is off in the fixture, so even the open friend gets a
		// 404, but only one written after the version passed the policy.
		const off = "Subtitle search is not set up"
		ps = append(ps,
			at("GET /api/v1/versions/{version_id}/subtitles/search", v).past(off),
			at("POST /api/v1/versions/{version_id}/subtitles/download", v).with(map[string]any{"file_id": 1}).past(off),
			at("PUT /api/v1/versions/{version_id}/subtitle-offset", v).with(map[string]any{"offset_ms": 250}))
	}
	for _, s := range w.hiddenSubs {
		ps = append(ps, at("GET /api/v1/subtitles/{stream_id}", s))
	}
	for _, x := range w.hiddenExtras {
		ps = append(ps, at("GET /api/v1/extras/{extra_id}/file", x), at("HEAD /api/v1/extras/{extra_id}/file", x),
			at("POST /api/v1/playback/sessions").with(map[string]any{"extra_id": x, "capabilities": tclCaps()}))
	}
	sessionProbes := func(id, owner string) {
		ps = append(ps,
			at("POST /api/v1/playback/sessions/{session_id}/events", id).with(map[string]any{"type": "progress", "position_ms": 60000}).ownedBy(owner),
			at("GET /api/v1/playback/sessions/{session_id}/seek", id).query("position_ms=1000").ownedBy(owner),
			at("GET /api/v1/playback/sessions/{session_id}/stream.mp4", id).ownedBy(owner),
			at("HEAD /api/v1/playback/sessions/{session_id}/stream.mp4", id).ownedBy(owner),
			// No HLS run exists for these sessions (their client asked for
			// fragmented MP4), so even the open friend gets a 404, but one
			// written after the policy check.
			at("GET /api/v1/playback/sessions/{session_id}/hls/{run}/index.m3u8", id, "1").ownedBy(owner).past("stream has ended"),
			at("HEAD /api/v1/playback/sessions/{session_id}/hls/{run}/index.m3u8", id, "1").ownedBy(owner).past("stream has ended"),
			at("GET /api/v1/playback/sessions/{session_id}/hls/{run}/{segment}", id, "1", "seg-00001.m4s").ownedBy(owner).past("No such segment"))
	}
	for _, s := range sessions {
		sessionProbes(s, "")
	}
	sessionProbes(adminSession, f.adminTok)
	for _, route := range []string{"GET /api/v1/libraries/{library_id}/items", "GET /api/v1/libraries/{library_id}/collections",
		"GET /api/v1/libraries/{library_id}/filters"} {
		ps = append(ps, at(route, w.vault))
	}
	ps = append(ps, at("GET /api/v1/collections/{collection_id}", w.hiddenCollection))

	// Lists and details of things the friend still sees must stop naming the
	// hidden ones.
	titles := []string{"Zebra Vault", "Zebra Night", "Zebra Rated", "Zebra Street", "Zebra Special"}
	ps = append(ps,
		at("GET /api/v1/home").hides("Zebra Vault", "Zebra Night", "Zebra Rated", "Zebra Street"),
		at("GET /api/v1/libraries").hides(`"name":"Vault"`),
		at("GET /api/v1/search").query("q=zebra").hides(titles...),
		at("GET /api/v1/libraries/{library_id}/items", w.movies).hides("Zebra Night", "Zebra Rated", `"resolution":"4K"`),
		at("GET /api/v1/libraries/{library_id}/items", w.tv).hides("Zebra Street", `"child_count":2`),
		at("GET /api/v1/libraries/{library_id}/collections", w.movies).hides("Zebra Collection"),
		at("GET /api/v1/libraries/{library_id}/filters", w.movies).hides("Slasher"),
		at("GET /api/v1/collections/{collection_id}", w.mixedCollection).hides("Zebra Night"),
		at("GET /api/v1/items/{item_id}", f.movieID).hides("film.2160p", `"4K"`),
		at("GET /api/v1/items/{item_id}", f.showID).hides(`"child_count":2`),
		at("GET /api/v1/items/{item_id}/children", f.showID).hides("Season 2"),
		// Home's See all: a library's row lists only what the friend may see,
		// a row in a library they lose is gone, and a row named after a
		// hidden title does not exist for them.
		at("GET /api/v1/rows/{row_id}", fmt.Sprintf("recently_added_movies:%d", w.movies)).hides("Zebra Night", "Zebra Rated"),
		at("GET /api/v1/rows/{row_id}", fmt.Sprintf("recently_added_movies:%d", w.vault)),
		at("GET /api/v1/rows/{row_id}", fmt.Sprintf("because_you_watched:%d", w.horrorMovie)),
	)
	return ps
}

// checkCoverage fails for any registered route the walk does not account for.
func checkCoverage(t *testing.T, routes []route, ps []probe) {
	t.Helper()
	hidden, listed := map[string]bool{}, map[string]bool{}
	registered := map[string]bool{}
	for _, rt := range routes {
		registered[rt.Method+" "+rt.Path] = true
	}
	for _, p := range ps {
		if !registered[p.route] {
			t.Errorf("probe for %s, which is not a registered route", p.route)
		}
		if strings.Contains(p.path, "{") {
			t.Errorf("probe for %s has an unfilled parameter: %s", p.route, p.path)
		}
		if p.list != nil {
			listed[p.route] = true
		} else {
			hidden[p.route] = true
		}
	}
	for _, rt := range routes {
		key := rt.Method + " " + rt.Path
		switch rt.access {
		case adminOnly:
			continue // every admin route is called below and must refuse the friend
		case householdOnly:
			continue // profiles, not titles; called below and must refuse the friend outside the household
		case public:
			if publicRoutes[key] == "" {
				t.Errorf("%s is public: if it really must answer without a session, add it to publicRoutes with the reason it carries no titles", key)
			}
			continue
		}
		if _, ok := noItemRoutes[key]; ok {
			if strings.Contains(rt.Path, "{") {
				t.Errorf("%s takes an id, so it cannot be in noItemRoutes", key)
			}
			continue
		}
		switch {
		case strings.Contains(rt.Path, "{") && listParamRoutes[key] != "" && listed[key]:
			// Covered by a list probe; see listParamRoutes.
		case strings.Contains(rt.Path, "{") && !hidden[key]:
			t.Errorf("%s is not in the access walk: add a probe that calls it as the friend for something their policy hides (want 404), and make the handler apply info(r).Policy", key)
		case !hidden[key] && !listed[key]:
			t.Errorf("%s is not in the access walk: add a probe that shows it stops listing hidden titles, or list it in noItemRoutes if it reads no titles", key)
		}
	}
}

func TestAccessWalkEveryRouteAppliesThePolicy(t *testing.T) {
	w := newWalkFixture(t)
	w.open()
	// The friend's own sessions on everything about to be hidden, opened while
	// allowed: a policy tightened mid-play must cut them off.
	var sessions []string
	for _, id := range []int64{w.vaultMovie, w.horrorMovie, w.ratedMovie, w.horrorEpisode, w.strictEpisode} {
		sessions = append(sessions, w.startSession(w.friendTok, id, nil))
	}
	sessions = append(sessions, w.startSession(w.friendTok, w.movieID, &w.v4k))
	// And on the extras about to be hidden: a trailer session has no version,
	// so its events and streams must check the extra itself.
	for _, x := range w.hiddenExtras {
		sessions = append(sessions, w.startExtraSession(w.friendTok, x))
	}
	adminSession := w.startSession(w.adminTok, w.movieID, &w.v1080)
	ps := w.probes(sessions, adminSession)
	checkCoverage(t, w.srv.Routes(), ps)

	call := func(p probe, tok string) resp {
		return w.do(p.method(), p.path, tok, p.body)
	}
	label := func(p probe) string {
		if p.method()+" "+strings.SplitN(p.path, "?", 2)[0] == p.route {
			return p.route
		}
		return p.route + " as " + p.method() + " " + p.path
	}

	// Control: with the friend's policy open, every call reaches its target.
	controlOK := true
	for _, p := range ps {
		tok := w.friendTok
		if p.owner != "" {
			tok = p.owner
		}
		r := call(p, tok)
		switch {
		case p.pastPolicy != "":
			if r.code != 404 || !strings.Contains(string(r.body), p.pastPolicy) {
				t.Errorf("control: %s answered %d %.120s, not the %q that shows it got past the policy", label(p), r.code, r.body, p.pastPolicy)
				controlOK = false
			}
		case r.code == 401 || r.code == 403 || r.code == 404 || r.code >= 500:
			t.Errorf("control: %s answered %d %.120s, so a 404 later would prove nothing", label(p), r.code, r.body)
			controlOK = false
		case p.list != nil:
			for _, text := range p.list {
				if !strings.Contains(string(r.body), text) {
					t.Errorf("control: %s does not mention %q even unrestricted, so its absence later would prove nothing", label(p), text)
					controlOK = false
				}
			}
		}
	}
	if !controlOK {
		t.FailNow()
	}

	w.restrict()
	byRoute := map[string][]probe{}
	for _, p := range ps {
		byRoute[p.route] = append(byRoute[p.route], p)
	}
	keys := make([]string, 0, len(byRoute))
	for k := range byRoute {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			for _, p := range byRoute[key] {
				r := call(p, w.friendTok)
				if p.list == nil {
					if r.code != http.StatusNotFound || (p.pastPolicy != "" && strings.Contains(string(r.body), p.pastPolicy)) {
						t.Errorf("%s: the restricted friend got %d %.200s, want the policy's 404", label(p), r.code, r.body)
					}
					continue
				}
				if r.code != 200 {
					t.Errorf("%s: %d %.200s", label(p), r.code, r.body)
				}
				for _, text := range p.list {
					if strings.Contains(string(r.body), text) {
						t.Errorf("%s: still shows %q to the restricted friend", label(p), text)
					}
				}
			}
		})
	}
	// Every admin route (item history, watch export, merge and split, smart
	// collections, the newsletter...) refuses the friend outright, and an
	// admin from off the LAN too.
	for _, rt := range w.srv.Routes() {
		if rt.access != adminOnly {
			continue
		}
		path := routeParam.ReplaceAllString(rt.Path, "1")
		if r := w.do(rt.Method, path, w.friendTok, nil); r.code != http.StatusForbidden {
			t.Errorf("%s %s: the friend got %d, want 403", rt.Method, rt.Path, r.code)
		}
		if r := w.do(rt.Method, path, w.adminTok, nil, "RemoteAddr", "203.0.113.7:5000"); r.code != http.StatusForbidden || !strings.Contains(string(r.body), "lan_only") {
			t.Errorf("%s %s: the admin from the internet got %d, want 403 lan_only", rt.Method, rt.Path, r.code)
		}
	}
	// Every household route (the profile picker, its avatars, switching)
	// refuses the friend, who is not in the household: no profile list, no
	// pictures, no way into a household member's session.
	w.srv.Household = &household.Store{DB: w.db, AvatarDir: t.TempDir()}
	w.srv.PINs = household.NewLimiter(household.PINWindow)
	for _, rt := range w.srv.Routes() {
		if rt.access != householdOnly {
			continue
		}
		path := routeParam.ReplaceAllString(rt.Path, "1")
		if r := w.do(rt.Method, path, w.friendTok, map[string]any{"user_id": 1}); r.code != http.StatusForbidden || !strings.Contains(string(r.body), "not_household") {
			t.Errorf("%s %s: the friend got %d %.120s, want 403 not_household", rt.Method, rt.Path, r.code, r.body)
		}
	}
}

// A content-rating ceiling admits the TV ratings for the same audience, hides
// titles with no known rating, and judges an episode by its own rating too.
func TestContentRatingCeiling(t *testing.T) {
	w := newWalkFixture(t)
	w.open()
	set := func(item int64, rating any) {
		if _, err := w.db.Exec(`UPDATE items SET content_rating = ? WHERE id = ?`, rating, item); err != nil {
			t.Fatal(err)
		}
	}
	sees := func(item int64) bool {
		return w.do("GET", fmt.Sprintf("/api/v1/items/%d", item), w.friendTok, nil).code == 200
	}
	w.setAccess([]int64{w.movies, w.tv, w.vault}, map[string]any{"max_content_rating": "pg-13", "allow_transcode": true})
	for _, c := range []struct {
		rating any
		want   bool
	}{{"G", true}, {"TV-PG", true}, {"PG-13", true}, {"TV-14", true}, {"Rated R", false}, {"R", false}, {"TV-MA", false},
		{"NC-17", false}, {"NR", false}, {"Unrated", false}, {"", false}, {nil, false}} {
		set(w.vaultMovie, c.rating)
		if got := sees(w.vaultMovie); got != c.want {
			t.Errorf("PG-13 ceiling, a title rated %v: visible %v, want %v", c.rating, got, c.want)
		}
	}
	// The show's TV-14 covers its unrated pilot; the TV-MA special is hidden
	// on its own rating, and its season with it.
	if !sees(w.episodeID) || sees(w.strictEpisode) || sees(w.strictSeason) || !sees(w.showID) {
		t.Errorf("episodes: pilot %v, TV-MA special %v, its season %v, show %v", sees(w.episodeID), sees(w.strictEpisode), sees(w.strictSeason), sees(w.showID))
	}
	// R admits TV-MA.
	w.setAccess([]int64{w.movies, w.tv, w.vault}, map[string]any{"max_content_rating": "R", "allow_transcode": true})
	if !sees(w.strictEpisode) {
		t.Error("an R ceiling hides a TV-MA episode")
	}
	// An unknown ceiling is refused when saved, never silently ignored.
	if r := w.do("PUT", fmt.Sprintf("/api/v1/admin/users/%d/policy", w.friendID), w.adminTok, map[string]any{"max_content_rating": "PG13"}); r.code != 400 {
		t.Errorf("unknown ceiling saved: %d %s", r.code, r.body)
	}
	// Allow labels: only titles carrying one, on the title or its show.
	w.setAccess([]int64{w.movies, w.tv, w.vault}, map[string]any{"allow_labels": []string{"HORROR"}, "allow_transcode": true})
	if !sees(w.horrorMovie) || !sees(w.horrorEpisode) || sees(w.ratedMovie) || sees(w.movieID) {
		t.Errorf("allow labels: horror film %v, horror episode %v, rated film %v, the film %v",
			sees(w.horrorMovie), sees(w.horrorEpisode), sees(w.ratedMovie), sees(w.movieID))
	}
}

// Marking a show watched marks only the episodes the caller can see.
func TestWatchedShowSkipsHiddenEpisodes(t *testing.T) {
	w := newWalkFixture(t)
	w.restrict()
	if r := w.do("PUT", fmt.Sprintf("/api/v1/items/%d/watched", w.showID), w.friendTok, map[string]any{"played": true}); r.code != 204 {
		t.Fatalf("watched: %d %s", r.code, r.body)
	}
	var pilot, special int
	_ = w.db.QueryRow(`SELECT COUNT(*) FROM watch_state WHERE user_id = ? AND item_id = ?`, w.friendID, w.episodeID).Scan(&pilot)
	_ = w.db.QueryRow(`SELECT COUNT(*) FROM watch_state WHERE user_id = ? AND item_id = ?`, w.friendID, w.strictEpisode).Scan(&special)
	if pilot != 1 || special != 0 {
		t.Fatalf("watch rows: visible pilot %d (want 1), hidden TV-MA special %d (want 0)", pilot, special)
	}
}

// The labels the policy editor offers.
func TestAdminLabelsAndSummary(t *testing.T) {
	w := newWalkFixture(t)
	w.restrict()
	var labels struct {
		Labels []struct {
			Name      string
			ItemCount int `json:"item_count"`
		}
	}
	w.do("GET", "/api/v1/admin/labels", w.adminTok, nil).json(t, &labels)
	if len(labels.Labels) != 1 || labels.Labels[0].Name != "horror" || labels.Labels[0].ItemCount != 2 {
		t.Fatalf("labels: %+v", labels)
	}
	var users struct {
		Users []struct {
			Username string
			Summary  string
		}
	}
	w.do("GET", "/api/v1/admin/users", w.adminTok, nil).json(t, &users)
	for _, u := range users.Users {
		if u.Username == "friend" && u.Summary != "Sees Movies and TV, up to 1080p and PG-13, never titles labelled horror, may convert 4K." {
			t.Errorf("friend summary: %q", u.Summary)
		}
	}
}

// Sign-in lockout: per address and per account, but the account lock only
// counts and only blocks clients off the LAN, so nobody on the internet can
// keep Pete from signing in at home.
func TestLoginLockoutSparesTheLAN(t *testing.T) {
	f := newFixture(t)
	login := func(addr, user, pass string) int {
		return f.do("POST", "/api/v1/auth/login", "", map[string]any{"username": user, "password": pass}, "RemoteAddr", addr).code
	}
	// Ten wrong guesses at "pete" from the internet lock that address, and the
	// account for every other internet address...
	for i := range 10 {
		if c := login("203.0.113.7:5000", "pete", "wrong-guess"); c != 401 {
			t.Fatalf("guess %d: %d", i, c)
		}
	}
	if c := login("203.0.113.7:5000", "pete", "adminpass1"); c != 429 {
		t.Errorf("the guessing address after 10 failures: %d, want 429", c)
	}
	if c := login("198.51.100.9:5000", "pete", "adminpass1"); c != 429 {
		t.Errorf("another internet address on the locked account: %d, want 429", c)
	}
	// ...but not for the LAN, and not for a different account.
	if c := login("192.168.1.20:5000", "pete", "adminpass1"); c != 200 {
		t.Errorf("Pete on the LAN during an internet lockout: %d, want 200", c)
	}
	if c := login("198.51.100.9:5000", "friend", "friendpass1"); c != 200 {
		t.Errorf("another account from another address: %d, want 200", c)
	}
	// Pete signing in at home leaves the internet count standing.
	if c := login("198.51.100.10:5000", "pete", "adminpass1"); c != 429 {
		t.Errorf("a LAN sign-in reset the internet lockout: %d, want 429", c)
	}
	// On the LAN, guessing is limited per address.
	for range 10 {
		login("192.168.1.30:5000", "stranger", "wrong-guess")
	}
	if c := login("192.168.1.30:5000", "stranger", "strangerpass1"); c != 429 {
		t.Errorf("the guessing LAN address: %d, want 429", c)
	}
	if c := login("192.168.1.31:5000", "stranger", "strangerpass1"); c != 200 {
		t.Errorf("another LAN address on the same account: %d, want 200", c)
	}
	// An IPv6 client is one address per /64, however many it rotates through.
	for i := range 10 {
		login(fmt.Sprintf("[2001:db8:1:2::%x]:5000", i+1), fmt.Sprintf("nobody%d", i), "wrong-guess")
	}
	if c := login("[2001:db8:1:2::ffff]:5000", "friend", "friendpass1"); c != 429 {
		t.Errorf("a new address in the guessing /64: %d, want 429", c)
	}
	if c := login("[2001:db8:1:3::1]:5000", "friend", "friendpass1"); c != 200 {
		t.Errorf("a different /64: %d, want 200", c)
	}
}

// A device on the LAN rotating through addresses is still limited per
// account (50 per window), apart from the internet's count; Tailscale peers
// count as off the LAN for sign-in limits and each is its own address.
func TestLoginLANAccountBucketAndTailscale(t *testing.T) {
	f := newFixture(t)
	login := func(addr, user, pass string) int {
		return f.do("POST", "/api/v1/auth/login", "", map[string]any{"username": user, "password": pass}, "RemoteAddr", addr).code
	}
	// Five failures from each of ten guest Wi-Fi addresses: none hits its
	// own limit of 10, but together they fill the account's LAN bucket.
	for host := 10; host < 20; host++ {
		for range 5 {
			login(fmt.Sprintf("192.168.1.%d:5000", host), "pete", "wrong-guess")
		}
	}
	if c := login("192.168.1.50:5000", "pete", "adminpass1"); c != 429 {
		t.Errorf("a fresh LAN address after 50 LAN failures on the account: %d, want 429", c)
	}
	// The internet scope is separate: the LAN guessing did not touch it.
	if c := login("203.0.113.7:5000", "pete", "adminpass1"); c != 200 {
		t.Errorf("the internet scope after LAN-only failures: %d, want 200", c)
	}
	// 49 LAN failures are not a lockout: the LAN limit is generous.
	for host := 10; host < 17; host++ {
		for range 7 {
			login(fmt.Sprintf("192.168.1.%d:5000", host), "friend", "wrong-guess")
		}
	}
	if c := login("192.168.1.51:5000", "friend", "friendpass1"); c != 200 {
		t.Errorf("49 LAN failures on an account: %d, want 200", c)
	}

	// One bad Tailscale peer locks only itself, not the tailnet.
	for i := range 10 {
		login("[fd7a:115c:a1e0::bad]:5000", fmt.Sprintf("nobody%d", i), "wrong-guess")
	}
	if c := login("[fd7a:115c:a1e0::bad]:5000", "stranger", "strangerpass1"); c != 429 {
		t.Errorf("the guessing Tailscale peer: %d, want 429", c)
	}
	if c := login("[fd7a:115c:a1e0::2]:5000", "stranger", "strangerpass1"); c != 200 {
		t.Errorf("another Tailscale peer: %d, want 200", c)
	}
	// Tailscale failures count in the internet scope, like any remote client.
	for range 10 {
		login("[fd7a:115c:a1e0::3]:5000", "stranger", "wrong-guess")
	}
	if c := login("198.51.100.20:5000", "stranger", "strangerpass1"); c != 429 {
		t.Errorf("internet sign-in after 10 Tailscale failures on the account: %d, want 429", c)
	}
	if c := login("192.168.1.60:5000", "stranger", "strangerpass1"); c != 200 {
		t.Errorf("LAN sign-in after Tailscale failures: %d, want 200", c)
	}

	// Admin gating is unchanged: Tailscale IPv6 reaches admin routes, as
	// Pete does from away; Tailscale IPv4 never did.
	for addr, want := range map[string]int{"[fd7a:115c:a1e0::2]:5000": 200, "100.101.102.103:5000": 403, "192.168.1.20:5000": 200} {
		if r := f.do("GET", "/api/v1/admin/users", f.adminTok, nil, "RemoteAddr", addr); r.code != want {
			t.Errorf("admin from %s: %d, want %d", addr, r.code, want)
		}
	}
}
