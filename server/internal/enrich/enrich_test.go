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
			"credits":{"cast":[{"name":"Ada Fenwick","character":"The Keeper","order":0},{"name":"Rui Sato","character":"Ferryman","order":1}],
			"crew":[{"name":"Mira Holt","job":"Director","department":"Directing"},{"name":"Jon Vale","job":"Screenplay","department":"Writing"}]},
			"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"certification":""},{"certification":"PG-13"}]}]}}`,
		"/movie/200":               `{"id":200,"title":"Night Ferry","overview":"Crossing at midnight.","poster_path":"/nf.jpg","vote_count":0}`,
		"/search/tv?Glass Orchard": `{"results":[{"id":300,"name":"Glass Orchard","first_air_date":"2016-09-01"}]}`,
		"/tv/300": `{"id":300,"name":"Glass Orchard","overview":"Orchards made of glass.","first_air_date":"2016-09-01",
			"poster_path":"/go.jpg","backdrop_path":"/gob.jpg","vote_average":8.1,"vote_count":50,
			"genres":[{"name":"Drama"}],"networks":[{"name":"North Channel"}],
			"seasons":[{"season_number":1,"poster_path":"/gos1.jpg"},{"season_number":2,"poster_path":"/gos2.jpg"}],
			"credits":{"cast":[{"name":"Lena Ok","character":"Gardener","order":0}]},
			"external_ids":{"imdb_id":"tt0000300"},"content_ratings":{"results":[{"iso_3166_1":"US","rating":"TV-14"}]}}`,
		"/tv/300/season/1": `{"season_number":1,"overview":"The first harvest.","poster_path":"/gos1.jpg","episodes":[
			{"episode_number":1,"name":"Seedlings","overview":"It begins.","air_date":"2016-09-01","still_path":"/e1.jpg","vote_average":7.9,"vote_count":10},
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
