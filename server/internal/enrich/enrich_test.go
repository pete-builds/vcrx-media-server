package enrich

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/tmdb"
)

// Every title here is invented: the repo is public.

type fakeTMDb struct {
	mu        sync.Mutex
	overview  string
	images    atomic.Int32
	fetched   map[string]int // image path -> downloads
	unauth    bool
	responses map[string]string
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 12))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (f *fakeTMDb) handler(t *testing.T) http.HandlerFunc {
	img := pngBytes(t)
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.unauth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/img/") {
			f.images.Add(1)
			if f.fetched == nil {
				f.fetched = map[string]int{}
			}
			f.fetched[r.URL.Path]++
			_, _ = w.Write(img)
			return
		}
		key := r.URL.Path
		if q := r.URL.Query().Get("query"); q != "" {
			key += "?" + q
		}
		body, ok := f.responses[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(strings.ReplaceAll(body, "{{overview}}", f.overview)))
	}
}

func newFake() *fakeTMDb {
	return &fakeTMDb{overview: "A keeper tends the last lantern.", responses: map[string]string{
		"/search/movie?Lantern Keeper": `{"results":[{"id":100,"title":"The Lantern Keeper","release_date":"2011-03-04"}]}`,
		"/search/movie?Paper Harbor": `{"results":[{"id":1,"title":"Paper Harbor","release_date":"1998-05-01"},
			{"id":2,"title":"Paper Harbor","release_date":"2019-10-11"}]}`,
		"/movie/100": `{"id":100,"title":"The Lantern Keeper","overview":"{{overview}}","tagline":"Keep it lit.",
			"release_date":"2011-03-04","runtime":101,"imdb_id":"tt0000100","poster_path":"/lk.jpg","backdrop_path":"/lkb.jpg",
			"vote_average":7.4,"vote_count":321,"genres":[{"name":"Drama"},{"name":"Mystery"}],
			"production_companies":[{"name":"Harbor Light Films"}],"production_countries":[{"name":"Iceland"}],
			"belongs_to_collection":{"id":555},
			"credits":{"cast":[{"id":11,"name":"Ada Fenwick","character":"The Keeper","order":0,"profile_path":"/ada.jpg"},{"id":12,"name":"Rui Sato","character":"Ferryman","order":1}],
			"crew":[{"id":31,"name":"Mira Holt","job":"Director","department":"Directing","profile_path":"/mira.jpg"},{"id":32,"name":"Jon Vale","job":"Screenplay","department":"Writing","profile_path":"/jon.jpg"}]},
			"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"certification":""},{"certification":"PG-13"}]}]}}`,
		"/movie/200":               `{"id":200,"title":"Night Ferry","overview":"Crossing at midnight.","poster_path":"/nf.jpg","vote_count":0}`,
		"/search/tv?Glass Orchard": `{"results":[{"id":300,"name":"Glass Orchard","first_air_date":"2016-09-01"}]}`,
		"/tv/300": `{"id":300,"name":"Glass Orchard","overview":"Orchards made of glass.","first_air_date":"2016-09-01",
			"poster_path":"/go.jpg","backdrop_path":"/gob.jpg","vote_average":8.1,"vote_count":50,
			"genres":[{"name":"Drama"}],"networks":[{"name":"North Channel"}],
			"seasons":[{"season_number":1,"poster_path":"/gos1.jpg"},{"season_number":2,"poster_path":"/gos2.jpg"}],
			"credits":{"cast":[{"id":21,"name":"Lena Ok","character":"Gardener","order":0}]},
			"external_ids":{"imdb_id":"tt0000300"},"content_ratings":{"results":[{"iso_3166_1":"US","rating":"TV-14"}]}}`,
		// Ties: two films called Ember Vale in 2024, one a short; a feature runs 101 minutes.
		"/search/movie?Ember Vale": `{"results":[{"id":401,"title":"Ember Vale","release_date":"2024-02-01","popularity":5,"vote_count":40},
			{"id":402,"title":"Ember Vale","release_date":"2024-06-01","popularity":4,"vote_count":30}]}`,
		"/movie/401": `{"id":401,"title":"Ember Vale","overview":"The feature.","runtime":101}`,
		"/movie/402": `{"id":402,"title":"Ember Vale","overview":"The short.","runtime":12}`,
		// Two shows called Copper Sky: one ran a single season, the other three of ten episodes.
		"/search/tv?Copper Sky": `{"results":[{"id":501,"name":"Copper Sky","first_air_date":"2005-01-01","origin_country":["GB"],"popularity":30,"vote_count":200},
			{"id":502,"name":"Copper Sky","first_air_date":"2011-01-01","origin_country":["US"],"popularity":25,"vote_count":150}]}`,
		"/tv/501": `{"id":501,"name":"Copper Sky","overview":"The British one.","seasons":[{"season_number":1,"episode_count":6}]}`,
		"/tv/502": `{"id":502,"name":"Copper Sky","overview":"The American one.","seasons":[{"season_number":0,"episode_count":2},
			{"season_number":1,"episode_count":10},{"season_number":2,"episode_count":10},{"season_number":3,"episode_count":10}]}`,
		// A popular film and an obscure namesake, with no runtime on disk to compare.
		"/search/movie?Iron Lantern": `{"results":[{"id":601,"title":"Iron Lantern","release_date":"2019-01-01","popularity":80,"vote_count":5000},
			{"id":602,"title":"Iron Lantern","release_date":"2019-03-01","popularity":2,"vote_count":3}]}`,
		"/movie/601": `{"id":601,"title":"Iron Lantern","overview":"The famous one."}`,
		// Every season's cast, which the show's own credits (latest season only) lack:
		// a regular from earlier seasons with most episodes first, under the part
		// played most, and an actor the movie above shares.
		"/tv/300/aggregate_credits": `{"cast":[
			{"id":21,"name":"Lena Ok","profile_path":"/lena.jpg","order":0,"total_episode_count":2,"roles":[{"character":"Gardener","episode_count":2}]},
			{"id":22,"name":"Ivo Marsh","profile_path":"/ivo.jpg","order":1,"total_episode_count":7,
				"roles":[{"character":"Young Ivo","episode_count":1},{"character":"Ivo","episode_count":6}]},
			{"id":11,"name":"Ada Fenwick","profile_path":"/ada.jpg","order":2,"total_episode_count":1,"roles":[{"character":"","episode_count":1}]}]}`,
		"/tv/300/season/1": `{"season_number":1,"overview":"The first harvest.","poster_path":"/gos1.jpg","episodes":[
			{"episode_number":1,"name":"Seedlings","overview":"It begins.","air_date":"2016-09-01","still_path":"/e1.jpg","vote_average":7.9,"vote_count":10,
				"crew":[{"id":33,"name":"Odile Brand","job":"Director","department":"Directing","profile_path":"/odile.jpg"},{"id":34,"name":"Sam Reed","job":"Writer","department":"Writing"}]},
			{"episode_number":2,"name":"First Frost","overview":"It gets cold.","air_date":"2016-09-08","still_path":"/e2.jpg"},
			{"episode_number":3,"name":"Thaw","overview":"It warms.","air_date":"2016-09-15","still_path":"/e3.jpg"}]}`,
	}}
}

type env struct {
	t    *testing.T
	conn *sql.DB
	e    *Enricher
	f    *fakeTMDb
	now  time.Time
}

func setup(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := db.Migrate(context.Background(), conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	f := newFake()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	c := tmdb.New("v3key", "en-US")
	c.BaseURL, c.ImageURL, c.Interval = srv.URL, srv.URL+"/img", 0
	v := &env{t: t, conn: conn, f: f, now: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}
	v.e = &Enricher{DB: conn, TMDb: c, Dir: filepath.Join(dir, "metadata"), Country: "US",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return v.now }}
	return v
}

func (v *env) exec(q string, args ...any) int64 {
	v.t.Helper()
	res, err := v.conn.Exec(q, args...)
	if err != nil {
		v.t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (v *env) library(kind string) int64 {
	return v.exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES (?, ?, '/media/x', 0, 0)`, kind+"s", kind)
}

func (v *env) item(lib int64, kind string, parent any, key, title string, year any, season, episode any) int64 {
	return v.exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, year, season_number, episode_number,
		added_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 1)`, lib, kind, parent, key, title, title, year, season, episode)
}

func (v *env) str(q string, args ...any) string {
	v.t.Helper()
	var s sql.NullString
	if err := v.conn.QueryRow(q, args...).Scan(&s); err != nil && !errors.Is(err, sql.ErrNoRows) {
		v.t.Fatalf("%s: %v", q, err)
	}
	return s.String
}

func (v *env) run() Stats {
	v.t.Helper()
	st, err := v.e.Drain(context.Background())
	if err != nil {
		v.t.Fatal(err)
	}
	return st
}

func TestMovieIsFilled(t *testing.T) {
	v := setup(t)
	lib := v.library("movie")
	id := v.item(lib, "movie", nil, "lantern keeper|2011", "Lantern Keeper", 2011, nil, nil)
	if st := v.run(); st.Matched != 1 {
		t.Fatalf("stats %+v", st)
	}
	for col, want := range map[string]string{
		"overview": v.f.overview, "tagline": "Keep it lit.", "content_rating": "PG-13", "premiere_date": "2011-03-04",
		"CAST(tmdb_id AS TEXT)": "100", "imdb_id": "tt0000100", "CAST(runtime_ms AS TEXT)": "6060000",
		"CAST(tmdb_collection_id AS TEXT)": "555", "title": "Lantern Keeper", "metadata_source": "filename",
	} {
		if got := v.str(`SELECT `+col+` FROM items WHERE id = ?`, id); got != want {
			t.Errorf("%s = %q, want %q", col, got, want)
		}
	}
	for kind, want := range map[string]string{"genre": "Drama,Mystery", "actor": "Ada Fenwick,Rui Sato", "director": "Mira Holt",
		"writer": "Jon Vale", "studio": "Harbor Light Films", "country": "Iceland"} {
		if got := v.str(`SELECT group_concat(value, ',') FROM (SELECT value FROM item_tags WHERE item_id = ? AND kind = ? ORDER BY position)`, id, kind); got != want {
			t.Errorf("%s tags = %q, want %q", kind, got, want)
		}
	}
	if got := v.str(`SELECT role FROM item_tags WHERE item_id = ? AND value = 'Ada Fenwick'`, id); got != "The Keeper" {
		t.Errorf("role = %q", got)
	}
	if got := v.str(`SELECT CAST(value AS TEXT) || '/' || votes FROM item_ratings WHERE item_id = ? AND source = 'tmdb'`, id); got != "7.4/321" {
		t.Errorf("rating = %q", got)
	}
	for _, kind := range []string{"poster", "backdrop"} {
		p := v.str(`SELECT cache_path FROM artwork WHERE item_id = ? AND kind = ? AND source = 'tmdb'`, id, kind)
		if p == "" || !strings.HasPrefix(p, v.e.Dir) {
			t.Fatalf("%s artwork path %q", kind, p)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s file: %v", kind, err)
		}
	}
	if got := v.str(`SELECT status || ':' || tmdb_id FROM tmdb_state WHERE item_id = ?`, id); got != "matched:100" {
		t.Errorf("state = %q", got)
	}
}

func TestNeverOverwritesOthers(t *testing.T) {
	v := setup(t)
	lib := v.library("movie")
	id := v.item(lib, "movie", nil, "k", "Lantern Keeper", 2011, nil, nil)
	v.exec(`UPDATE items SET overview = 'Written in my NFO.', locked_fields = '["genres", "backdrop"]' WHERE id = ?`, id)
	v.exec(`INSERT INTO item_tags (item_id, kind, value, position) VALUES (?, 'genre', 'Mine', 0)`, id)
	v.exec(`INSERT INTO artwork (item_id, kind, source, source_path, updated_at) VALUES (?, 'poster', 'local', '/media/x/poster.jpg', 0)`, id)
	v.run()
	if got := v.str(`SELECT overview FROM items WHERE id = ?`, id); got != "Written in my NFO." {
		t.Errorf("NFO overview replaced: %q", got)
	}
	if got := v.str(`SELECT group_concat(value) FROM item_tags WHERE item_id = ? AND kind = 'genre'`, id); got != "Mine" {
		t.Errorf("locked genres replaced: %q", got)
	}
	if got := v.str(`SELECT source FROM artwork WHERE item_id = ? AND kind = 'poster'`, id); got != "local" {
		t.Errorf("local poster replaced by %q", got)
	}
	if got := v.str(`SELECT count(*) FROM artwork WHERE item_id = ? AND kind = 'backdrop'`, id); got != "0" {
		t.Errorf("locked backdrop downloaded")
	}
	if got := v.str(`SELECT tagline FROM items WHERE id = ?`, id); got != "Keep it lit." {
		t.Errorf("empty tagline not filled: %q", got)
	}
}

func TestRefreshReplacesOnlyItsOwnValues(t *testing.T) {
	v := setup(t)
	lib := v.library("movie")
	id := v.item(lib, "movie", nil, "k", "Lantern Keeper", 2011, nil, nil)
	v.run()
	images := v.f.images.Load()

	// Nothing is due until the refresh interval passes.
	if st := v.run(); st.Matched != 0 {
		t.Fatalf("re-ran too soon: %+v", st)
	}
	v.f.overview = "A revised plot."
	v.now = v.now.Add(RefreshMatched + time.Hour)
	v.run()
	if got := v.str(`SELECT overview FROM items WHERE id = ?`, id); got != "A revised plot." {
		t.Errorf("own overview not refreshed: %q", got)
	}
	if v.f.images.Load() != images {
		t.Errorf("unchanged images downloaded again: %d -> %d", images, v.f.images.Load())
	}

	// An NFO arriving later wins, and the next refresh leaves it alone.
	v.exec(`UPDATE items SET overview = 'From an NFO.' WHERE id = ?`, id)
	v.f.overview = "Yet another plot."
	v.now = v.now.Add(RefreshMatched + time.Hour)
	v.run()
	if got := v.str(`SELECT overview FROM items WHERE id = ?`, id); got != "From an NFO." {
		t.Errorf("NFO overview overwritten: %q", got)
	}
}

func TestNFOIdIsTrusted(t *testing.T) {
	v := setup(t)
	lib := v.library("movie")
	// A file name TMDb search would never find, with the id an NFO supplied.
	id := v.item(lib, "movie", nil, "k", "nf rip final", nil, nil, nil)
	v.exec(`UPDATE items SET tmdb_id = 200 WHERE id = ?`, id)
	v.run()
	if got := v.str(`SELECT overview FROM items WHERE id = ?`, id); got != "Crossing at midnight." {
		t.Errorf("overview = %q", got)
	}
}

func TestAmbiguousIsUnmatchedAndRetriedLater(t *testing.T) {
	v := setup(t)
	lib := v.library("movie")
	id := v.item(lib, "movie", nil, "k", "Paper Harbor", nil, nil, nil)
	if st := v.run(); st.Unmatched != 1 || st.Matched != 0 {
		t.Fatalf("stats %+v", st)
	}
	if got := v.str(`SELECT overview FROM items WHERE id = ?`, id); got != "" {
		t.Errorf("an ambiguous title was filled: %q", got)
	}
	if got := v.str(`SELECT reason FROM tmdb_state WHERE item_id = ?`, id); !strings.Contains(got, "several") {
		t.Errorf("reason = %q", got)
	}
	if st := v.run(); st.Unmatched != 0 {
		t.Errorf("retried at once: %+v", st)
	}
	// Adding the year (a rename) triggers a new match straight away.
	v.exec(`UPDATE items SET year = 2019 WHERE id = ?`, id)
	if st := v.run(); st.Unmatched != 1 {
		// 2019 search hits the same fake results; Pick now chooses id 2, which the fake has no details for.
		t.Errorf("rename not re-matched: %+v", st)
	}
	if got := v.str(`SELECT reason FROM tmdb_state WHERE item_id = ?`, id); !strings.Contains(got, "not found") {
		t.Errorf("reason after rename = %q", got)
	}
}

func TestShowSeasonsAndEpisodes(t *testing.T) {
	v := setup(t)
	lib := v.library("show")
	show := v.item(lib, "show", nil, "glass orchard", "Glass Orchard", nil, nil, nil)
	s1 := v.item(lib, "season", show, "1", "Season 1", nil, 1, nil)
	s7 := v.item(lib, "season", show, "7", "Season 7", nil, 7, nil)
	e1 := v.item(lib, "episode", s1, "1", "Episode 1", nil, 1, 1)
	e2 := v.item(lib, "episode", s1, "2", "My Own Title", nil, 1, 2)
	v.exec(`UPDATE items SET metadata_source = 'nfo' WHERE id = ?`, e2)
	e71 := v.item(lib, "episode", s7, "1", "Episode 1", nil, 7, 1)

	st := v.run()
	if st.Matched != 1 || st.Episodes != 2 {
		t.Fatalf("stats %+v", st)
	}
	if got := v.str(`SELECT title || '|' || metadata_source || '|' || overview FROM items WHERE id = ?`, e1); got != "Seedlings|tmdb|It begins." {
		t.Errorf("episode 1 = %q", got)
	}
	if got := v.str(`SELECT title FROM items WHERE id = ?`, e2); got != "My Own Title" {
		t.Errorf("NFO episode title replaced: %q", got)
	}
	if got := v.str(`SELECT source FROM artwork WHERE item_id = ? AND kind = 'thumb'`, e1); got != "tmdb" {
		t.Errorf("episode still = %q", got)
	}
	if got := v.str(`SELECT overview FROM items WHERE id = ?`, s1); got != "The first harvest." {
		t.Errorf("season overview = %q", got)
	}
	if got := v.str(`SELECT source FROM artwork WHERE item_id = ? AND kind = 'poster'`, s1); got != "tmdb" {
		t.Errorf("season poster = %q", got)
	}
	if got := v.str(`SELECT group_concat(value) FROM item_tags WHERE item_id = ? AND kind = 'network'`, show); got != "North Channel" {
		t.Errorf("network = %q", got)
	}
	if got := v.str(`SELECT status FROM tmdb_state WHERE item_id = ?`, e71); got != "unmatched" {
		t.Errorf("episode of a season TMDb lacks = %q", got)
	}
	if st := v.run(); st.Matched != 0 {
		t.Fatalf("show re-ran with nothing new: %+v", st)
	}
	// A new episode on disk makes the show due again.
	e3 := v.item(lib, "episode", s1, "3", "Episode 3", nil, 1, 3)
	if st := v.run(); st.Matched != 1 {
		t.Fatalf("new episode did not trigger: %+v", st)
	}
	if got := v.str(`SELECT title FROM items WHERE id = ?`, e3); got != "Thaw" {
		t.Errorf("new episode = %q", got)
	}
}

func TestRejectedKeyStops(t *testing.T) {
	v := setup(t)
	lib := v.library("movie")
	v.item(lib, "movie", nil, "k", "Lantern Keeper", 2011, nil, nil)
	v.f.unauth = true
	if _, err := v.e.Drain(context.Background()); !errors.Is(err, tmdb.ErrUnauthorized) {
		t.Fatalf("err = %v", err)
	}
	if got := v.str(`SELECT count(*) FROM tmdb_state`); got != "0" {
		t.Errorf("a rejected key recorded state")
	}
}

func TestBusyLibraryWaits(t *testing.T) {
	v := setup(t)
	lib := v.library("movie")
	id := v.item(lib, "movie", nil, "k", "Lantern Keeper", 2011, nil, nil)
	v.e.Busy = func(int64) bool { return true }
	v.run()
	if got := v.str(`SELECT count(*) FROM tmdb_state WHERE item_id = ?`, id); got != "0" {
		t.Errorf("enriched a library mid-scan")
	}
	v.e.Busy = nil
	v.run()
	if got := v.str(`SELECT status FROM tmdb_state WHERE item_id = ?`, id); got != "matched" {
		t.Errorf("status = %q", got)
	}
}

func TestTieSettledByRuntime(t *testing.T) {
	v := setup(t)
	lib := v.library("movie")
	id := v.item(lib, "movie", nil, "k", "Ember Vale", 2024, nil, nil)
	v.exec(`UPDATE items SET runtime_ms = ? WHERE id = ?`, (99 * time.Minute).Milliseconds(), id)
	v.run()
	if got := v.str(`SELECT overview FROM items WHERE id = ?`, id); got != "The feature." {
		t.Errorf("overview = %q", got)
	}
	if got := v.str(`SELECT reason FROM tmdb_state WHERE item_id = ?`, id); got != "picked by runtime" {
		t.Errorf("reason = %q", got)
	}
	// With a runtime that fits neither, it stays unmatched.
	other := v.item(lib, "movie", nil, "k2", "Ember Vale", 2024, nil, nil)
	v.exec(`UPDATE items SET runtime_ms = ? WHERE id = ?`, (55 * time.Minute).Milliseconds(), other)
	v.run()
	if got := v.str(`SELECT status || '|' || reason FROM tmdb_state WHERE item_id = ?`, other); !strings.HasPrefix(got, "unmatched|no result with this title fits the runtime") {
		t.Errorf("no fit = %q", got)
	}
}

func TestTieSettledBySeasons(t *testing.T) {
	v := setup(t)
	lib := v.library("show")
	show := v.item(lib, "show", nil, "copper sky", "Copper Sky", nil, nil, nil)
	for season := 1; season <= 3; season++ {
		sid := v.item(lib, "season", show, strconv.Itoa(season), "Season", nil, season, nil)
		v.item(lib, "episode", sid, "8", "Episode 8", nil, season, 8)
	}
	v.run()
	if got := v.str(`SELECT overview FROM items WHERE id = ?`, show); got != "The American one." {
		t.Errorf("overview = %q", got)
	}
	if got := v.str(`SELECT reason FROM tmdb_state WHERE item_id = ?`, show); got != "picked by seasons and episodes on disk" {
		t.Errorf("reason = %q", got)
	}
}

func TestCountryTagPicksTheShow(t *testing.T) {
	v := setup(t)
	lib := v.library("show")
	show := v.item(lib, "show", nil, "copper sky uk", "Copper Sky (UK)", nil, nil, nil)
	v.run()
	if got := v.str(`SELECT overview FROM items WHERE id = ?`, show); got != "The British one." {
		t.Errorf("overview = %q", got)
	}
}

func TestTieSettledByPopularity(t *testing.T) {
	v := setup(t)
	lib := v.library("movie")
	id := v.item(lib, "movie", nil, "k", "Iron Lantern", 2019, nil, nil)
	v.run()
	if got := v.str(`SELECT overview || '|' || (SELECT reason FROM tmdb_state WHERE item_id = items.id) FROM items WHERE id = ?`, id); got != "The famous one.|picked by popularity" {
		t.Errorf("got %q", got)
	}
}

// Run calls AfterPass after a pass, where copies of one film that now share a
// TMDb id are merged, and before Changed, so the smart collection rebuild
// stamps the merged movie rather than a copy about to disappear.
func TestRunMergesBeforeRebuild(t *testing.T) {
	v := setup(t)
	v.item(v.library("movie"), "movie", nil, "k", "Lantern Keeper", 2011, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var order []string
	done := make(chan struct{})
	note := func(what string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, what)
		if len(order) == 2 {
			close(done)
			cancel()
		}
	}
	v.e.AfterPass = func(context.Context) { note("merge") }
	v.e.Changed = func() { note("rebuild") }
	go v.e.Run(ctx, 0, time.Hour)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a pass that matched did not call both AfterPass and Changed")
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(order, []string{"merge", "rebuild"}) {
		t.Fatalf("order = %q, want the merge before the rebuild", order)
	}
}

// A show takes its cast from every season, most episodes first, each actor
// under the part they played most, and every actor with a TMDb id gets it on
// their tag. Headshots download once per person, however many titles share
// them, and not again on a refresh.
func TestCastAcrossSeasonsWithPhotos(t *testing.T) {
	v := setup(t)
	movies, shows := v.library("movie"), v.library("show")
	movie := v.item(movies, "movie", nil, "k", "Lantern Keeper", 2011, nil, nil)
	show := v.item(shows, "show", nil, "glass orchard", "Glass Orchard", nil, nil, nil)
	v.run()

	cast := func(id int64) string {
		t.Helper()
		return v.str(`SELECT group_concat(value || '/' || IFNULL(role, '') || '/' || IFNULL(person_id, ''), ', ')
			FROM (SELECT * FROM item_tags WHERE item_id = ? AND kind = 'actor' ORDER BY position)`, id)
	}
	if got, want := cast(show), "Ivo Marsh/Ivo/22, Lena Ok/Gardener/21, Ada Fenwick//11"; got != want {
		t.Errorf("show cast = %q, want %q", got, want)
	}
	if got, want := cast(movie), "Ada Fenwick/The Keeper/11, Rui Sato/Ferryman/12"; got != want {
		t.Errorf("movie cast = %q, want %q", got, want)
	}
	for id, want := range map[int64]bool{11: true, 21: true, 22: true, 12: false} {
		file := v.str(`SELECT cache_path FROM people WHERE tmdb_id = ?`, id)
		if (file != "") != want {
			t.Errorf("person %d photo = %q, want one: %v", id, file, want)
			continue
		}
		if want {
			if _, err := os.Stat(file); err != nil || !strings.HasPrefix(file, filepath.Join(v.e.Dir, "people")+string(filepath.Separator)) {
				t.Errorf("person %d photo at %q: %v", id, file, err)
			}
		}
	}
	if n := v.f.fetched["/img/w185/ada.jpg"]; n != 1 {
		t.Errorf("shared actor's photo downloaded %d times, want 1", n)
	}

	// A refresh downloads nothing it already has; a new photo replaces the old file.
	v.f.mu.Lock()
	v.f.responses["/tv/300/aggregate_credits"] = strings.Replace(v.f.responses["/tv/300/aggregate_credits"], "/lena.jpg", "/lena2.jpg", 1)
	v.f.mu.Unlock()
	old := v.str(`SELECT cache_path FROM people WHERE tmdb_id = 21`)
	v.now = v.now.Add(RefreshMatched + time.Hour)
	v.run()
	if n := v.f.fetched["/img/w185/ada.jpg"]; n != 1 {
		t.Errorf("unchanged photo downloaded again: %d", n)
	}
	cur := v.str(`SELECT cache_path FROM people WHERE tmdb_id = 21`)
	if cur == old || !strings.Contains(cur, "lena2") {
		t.Errorf("new photo not taken: %q -> %q", old, cur)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old photo left behind: %v", err)
	}
}

// Directors carry their TMDb person and headshot like actors, a film's from
// its credits and an episode's from its season's crew; writers stay names.
func TestDirectorsWithPhotos(t *testing.T) {
	v := setup(t)
	movies, shows := v.library("movie"), v.library("show")
	movie := v.item(movies, "movie", nil, "k", "Lantern Keeper", 2011, nil, nil)
	show := v.item(shows, "show", nil, "glass orchard", "Glass Orchard", nil, nil, nil)
	s1 := v.item(shows, "season", show, "1", "Season 1", nil, 1, nil)
	e1 := v.item(shows, "episode", s1, "1", "Episode 1", nil, 1, 1)
	v.run()

	tags := func(id int64, kind string) string {
		t.Helper()
		return v.str(`SELECT IFNULL(group_concat(value || '/' || IFNULL(person_id, ''), ', '), '')
			FROM (SELECT * FROM item_tags WHERE item_id = ? AND kind = ? ORDER BY position)`, id, kind)
	}
	if got, want := tags(movie, "director"), "Mira Holt/31"; got != want {
		t.Errorf("movie directors = %q, want %q", got, want)
	}
	if got, want := tags(movie, "writer"), "Jon Vale/"; got != want {
		t.Errorf("movie writers = %q, want %q (no person: writers have no photo)", got, want)
	}
	if got, want := tags(e1, "director"), "Odile Brand/33"; got != want {
		t.Errorf("episode directors = %q, want %q", got, want)
	}
	for _, id := range []int64{31, 33} {
		file := v.str(`SELECT IFNULL(cache_path, '') FROM people WHERE tmdb_id = ?`, id)
		if _, err := os.Stat(file); file == "" || err != nil {
			t.Errorf("director %d photo at %q: %v", id, file, err)
		}
	}
	if n := v.f.fetched["/img/w185/jon.jpg"]; n != 0 {
		t.Errorf("writer photo downloaded %d times, want 0", n)
	}
}

// When TMDb cannot give a show's cast across seasons, the latest season's
// regulars from its plain credits still fill it.
func TestShowCastFallsBackToCredits(t *testing.T) {
	v := setup(t)
	delete(v.f.responses, "/tv/300/aggregate_credits")
	show := v.item(v.library("show"), "show", nil, "glass orchard", "Glass Orchard", nil, nil, nil)
	if st := v.run(); st.Matched != 1 {
		t.Fatalf("stats %+v", st)
	}
	if got := v.str(`SELECT group_concat(value || '/' || IFNULL(person_id, '')) FROM item_tags WHERE item_id = ? AND kind = 'actor'`, show); got != "Lena Ok/21" {
		t.Errorf("cast = %q", got)
	}
}
