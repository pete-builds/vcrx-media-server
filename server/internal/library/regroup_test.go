package library

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/enrich"
	"github.com/pete-builds/vcrx-media-server/server/internal/tmdb"
)

// Every title here is invented: the repo is public.

// twoReleases is one film as two scene releases in their own folders, which
// folder grouping keeps apart, plus a show whose episodes must never merge.
var twoReleases = map[string]string{
	"Quorra.Vale.2011.2160p.UHD.BluRay.x265-VCRXTEST/Quorra.Vale.2011.2160p.UHD.BluRay.x265-VCRXTEST.mkv": "",
	"Quorra.Vale.2011.1080p.BluRay.x264-VCRXTEST/Quorra.Vale.2011.1080p.BluRay.x264-VCRXTEST.mkv":         "",
	"Tin Lantern (2016)/Season 1/Tin Lantern S01E01.mkv":                                                  "",
	"Tin Lantern (2016)/Season 1/Tin Lantern S01E02.mkv":                                                  "",
}

// oneFolder is one film with two versions side by side.
var oneFolder = map[string]string{
	"Quorra Vale (2011)/Quorra Vale (2011) - 1080p.mkv":     "",
	"Quorra Vale (2011)/Quorra Vale (2011) - 4K HDR.mkv":    "",
	"Quorra Vale (2011)/Quorra Vale (2011) - Workprint.mkv": "",
}

func (e *env) id(q string, args ...any) int64 {
	e.t.Helper()
	var id int64
	if err := e.db.QueryRow(q, args...).Scan(&id); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return id
}

func (e *env) exec(q string, args ...any) int64 {
	e.t.Helper()
	res, err := e.db.Exec(q, args...)
	if err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (e *env) user(name string) int64 {
	return e.exec(`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES (?, 'x', 0, 0)`, name)
}

// everything renders the rows a merge or split may touch, user data included,
// so a failed one can be shown to have changed nothing.
func (e *env) everything() string {
	e.t.Helper()
	var b strings.Builder
	b.WriteString(e.digest())
	for _, q := range []string{
		`SELECT user_id, item_id, position_ms, played, play_count, IFNULL(last_version_id, 0), updated_at FROM watch_state ORDER BY user_id, item_id`,
		`SELECT id, IFNULL(item_id, 0), IFNULL(version_id, 0) FROM playback_sessions ORDER BY id`,
		`SELECT collection_id, item_id FROM collection_items ORDER BY collection_id, item_id`,
		`SELECT item_id, status, IFNULL(tmdb_id, 0) FROM tmdb_state ORDER BY item_id`,
	} {
		e.dump(&b, q)
		b.WriteString("--\n")
	}
	return b.String()
}

func TestTMDbMergeFoldsCopiesAndSticks(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", twoReleases)
	e.scan(Options{})
	ids := e.strs(`SELECT id FROM items WHERE kind = 'movie' ORDER BY id`)
	if len(ids) != 2 {
		t.Fatalf("folder grouping should keep the two releases apart, got %d movies", len(ids))
	}
	a := e.id(`SELECT MIN(id) FROM items WHERE kind = 'movie'`)
	b := e.id(`SELECT MAX(id) FROM items WHERE kind = 'movie'`)
	vb := e.id(`SELECT id FROM media_versions WHERE item_id = ?`, b)
	bKey := e.str(`SELECT group_key FROM items WHERE id = ?`, b)
	// Enrichment found the same film for both, and the same show id for two
	// episodes, which must not matter.
	e.exec(`UPDATE items SET tmdb_id = 501 WHERE kind = 'movie'`)
	e.exec(`UPDATE items SET tmdb_id = 900 WHERE kind = 'episode'`)
	shapeOfShows := e.strs(`SELECT kind || ':' || id FROM items WHERE kind != 'movie' ORDER BY id`)

	// What people did with each copy.
	pete, kim, ana := e.user("pete"), e.user("kim"), e.user("ana")
	e.exec(`INSERT INTO watch_state (user_id, item_id, position_ms, played, play_count, last_played_at, updated_at) VALUES (?, ?, 600000, 0, 1, 1000, 1000)`, pete, a)
	e.exec(`INSERT INTO watch_state (user_id, item_id, position_ms, played, play_count, last_version_id, last_played_at, updated_at) VALUES (?, ?, 0, 1, 1, ?, 2000, 2000)`, pete, b, vb)
	e.exec(`INSERT INTO watch_state (user_id, item_id, position_ms, played, play_count, updated_at) VALUES (?, ?, 0, 1, 2, 500)`, kim, b)
	e.exec(`INSERT INTO watch_state (user_id, item_id, position_ms, played, play_count, updated_at) VALUES (?, ?, 300, 0, 0, 700)`, ana, a)
	e.exec(`INSERT INTO playback_sessions (id, user_id, item_id, version_id, decision, decision_reason, started_at, last_event_at)
		VALUES ('s1', ?, ?, ?, 'direct_play', 'test', 1, 1)`, pete, b, vb)
	hidden := e.exec(`INSERT INTO labels (name) VALUES ('Hidden From Kids')`)
	// Both copies carry the label (only copies that agree are merged); a
	// collection stamp on one copy does not count against the merge.
	e.exec(`INSERT INTO item_labels (item_id, label_id, source) VALUES (?, ?, 'nfo'), (?, ?, 'manual')`, a, hidden, b, hidden)
	stamp := e.exec(`INSERT INTO labels (name) VALUES ('Stamped')`)
	e.exec(`INSERT INTO item_labels (item_id, label_id, source) VALUES (?, ?, 'collection')`, b, stamp)
	e.exec(`INSERT INTO item_ratings (item_id, source, value) VALUES (?, 'tmdb', 7.1)`, a)
	e.exec(`INSERT INTO item_ratings (item_id, source, value) VALUES (?, 'tmdb', 6.0), (?, 'user', 9)`, b, b)
	coll := e.exec(`INSERT INTO collections (library_id, slug, name, sort_title, source, updated_at) VALUES (?, 'vale', 'Vale', 'Vale', 'auto', 0)`, e.lib.ID)
	e.exec(`INSERT INTO collection_items (collection_id, item_id, position) VALUES (?, ?, 1)`, coll, b)

	rep, err := MergeByTMDb(context.Background(), e.db, 0)
	if err != nil || rep.Merged != 1 {
		t.Fatalf("MergeByTMDb = %+v, %v; want 1 merged", rep, err)
	}
	if got := e.strs(`SELECT id FROM items WHERE kind = 'movie'`); len(got) != 1 || got[0] != ids[0] {
		t.Fatalf("movies after merge = %q, want only the oldest, %s", got, ids[0])
	}
	if n := e.count(`SELECT COUNT(*) FROM media_versions WHERE item_id = ?`, a); n != 2 {
		t.Fatalf("versions on the merged movie = %d", n)
	}
	if got := e.strs(`SELECT kind || ':' || id FROM items WHERE kind != 'movie' ORDER BY id`); !slices.Equal(got, shapeOfShows) {
		t.Fatalf("shows, seasons or episodes changed: %q, was %q", got, shapeOfShows)
	}
	// Watch state: played if either copy was, counts added, the position from
	// the copy watched last; nobody's row is lost.
	if got := e.str(`SELECT played || ',' || play_count || ',' || position_ms || ',' || IFNULL(last_version_id, 0) || ',' || last_played_at
		FROM watch_state WHERE user_id = ? AND item_id = ?`, pete, a); got != "1,2,0,"+itoa(vb)+",2000" {
		t.Fatalf("pete's watch state = %s", got)
	}
	if got := e.str(`SELECT played || ',' || play_count FROM watch_state WHERE user_id = ? AND item_id = ?`, kim, a); got != "1,2" {
		t.Fatalf("kim's watch state = %q", got)
	}
	if got := e.str(`SELECT position_ms FROM watch_state WHERE user_id = ? AND item_id = ?`, ana, a); got != "300" {
		t.Fatalf("ana's watch state = %q", got)
	}
	if got := e.id(`SELECT item_id FROM playback_sessions WHERE id = 's1'`); got != a {
		t.Fatalf("history points at %d, want %d", got, a)
	}
	// The shared label is now held by hand, so a rewrite of the survivor's NFO
	// cannot drop it; the stamp comes along as a stamp, for its rule to judge.
	if got := e.strs(`SELECT l.name || ':' || il.source FROM item_labels il JOIN labels l ON l.id = il.label_id WHERE il.item_id = ? ORDER BY l.name`, a); !slices.Equal(got, []string{"Hidden From Kids:manual", "Stamped:collection"}) {
		t.Fatalf("labels on the merged movie = %q", got)
	}
	if got := e.strs(`SELECT source || '=' || value FROM item_ratings WHERE item_id = ? ORDER BY source`, a); !slices.Equal(got, []string{"tmdb=7.1", "user=9.0"}) {
		t.Fatalf("ratings = %q: the survivor keeps its own, gains what it lacked", got)
	}
	if n := e.count(`SELECT COUNT(*) FROM collection_items WHERE item_id = ?`, a); n != 1 {
		t.Fatal("collection membership was lost")
	}
	if !strings.Contains(e.str(`SELECT extra FROM items WHERE id = ?`, a), bKey) {
		t.Fatal("the merged key is not recorded on the survivor")
	}

	// A rescan finds both folders again and changes nothing.
	before := e.digest()
	e.scan(Options{})
	if after := e.digest(); after != before {
		t.Fatalf("rescan undid or changed the merge:\n%s\nwas\n%s", after, before)
	}
	// Nor does a changed file, which is re-probed and rewritten.
	e.write(map[string]string{"Quorra.Vale.2011.1080p.BluRay.x264-VCRXTEST/Quorra.Vale.2011.1080p.BluRay.x264-VCRXTEST.mkv": "a re-encoded file"})
	e.scan(Options{})
	if n := e.count(`SELECT COUNT(*) FROM items WHERE kind = 'movie'`); n != 1 {
		t.Fatalf("movies after a changed file = %d", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM media_versions WHERE item_id = ?`, a); n != 2 {
		t.Fatalf("versions after a changed file = %d", n)
	}
}

// fakeTMDbServer answers a search and a details call for the invented film.
func fakeTMDbServer(t *testing.T) *tmdb.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/movie":
			_, _ = w.Write([]byte(`{"results":[{"id":501,"title":"Quorra Vale","release_date":"2011-05-06"}]}`))
		case "/movie/501":
			_, _ = w.Write([]byte(`{"id":501,"title":"Quorra Vale","overview":"A valley keeps its secret.","release_date":"2011-05-06","runtime":98}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	c := tmdb.New("v3key", "en-US")
	c.BaseURL, c.ImageURL, c.Interval = srv.URL, srv.URL+"/img", 0
	return c
}

func TestSplitSticksThroughRescanAndEnrichment(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", oneFolder)
	e.scan(Options{})
	enr := &enrich.Enricher{DB: e.db, TMDb: fakeTMDbServer(t), Dir: filepath.Join(t.TempDir(), "meta"), Log: quiet}
	ctx := context.Background()
	if st, err := enr.Drain(ctx); err != nil || st.Matched != 1 {
		t.Fatalf("enrich: %+v %v", st, err)
	}
	a := e.id(`SELECT id FROM items WHERE kind = 'movie'`)
	v4k := e.id(`SELECT id FROM media_versions WHERE label = '4K HDR'`)
	pete := e.user("pete")
	e.exec(`INSERT INTO watch_state (user_id, item_id, position_ms, played, play_count, last_version_id, updated_at) VALUES (?, ?, 4000, 0, 0, ?, 1)`, pete, a, v4k)
	e.exec(`INSERT INTO playback_sessions (id, user_id, item_id, version_id, decision, decision_reason, started_at, last_event_at)
		VALUES ('s4k', ?, ?, ?, 'direct_play', 'test', 1, 1)`, pete, a, v4k)

	s, err := Split(ctx, e.db, v4k)
	if err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		if got := e.strs(`SELECT IFNULL(label, '') FROM media_versions WHERE item_id = ? ORDER BY label`, s); !slices.Equal(got, []string{"4K HDR"}) {
			t.Fatalf("%s: split movie holds %q", when, got)
		}
		if got := e.strs(`SELECT IFNULL(label, '') FROM media_versions WHERE item_id = ? ORDER BY label`, a); !slices.Equal(got, []string{"1080p", "Workprint"}) {
			t.Fatalf("%s: original movie holds %q", when, got)
		}
		if n := e.count(`SELECT COUNT(*) FROM items WHERE kind = 'movie'`); n != 2 {
			t.Fatalf("%s: %d movies", when, n)
		}
	}
	check("after the split")
	if got := e.str(`SELECT title || ',' || IFNULL(tmdb_id, 0) || ',' || group_key FROM items WHERE id = ?`, s); got != "Quorra Vale,501,p:Quorra Vale (2011)/Quorra Vale (2011) - 4K HDR.mkv" {
		t.Fatalf("split movie = %q", got)
	}
	if got := e.str(`SELECT item_id FROM playback_sessions WHERE id = 's4k'`); got != itoa(s) {
		t.Fatalf("history of the split file stayed on %s", got)
	}
	if got := e.str(`SELECT position_ms FROM watch_state WHERE user_id = ? AND item_id = ?`, pete, s); got != "4000" {
		t.Fatalf("resume point on the split file = %q", got)
	}

	// Enrichment runs again from scratch and matches both to the same film;
	// the automatic merge must leave the split alone.
	if err := enr.ResetChecks(ctx); err != nil {
		t.Fatal(err)
	}
	if st, err := enr.Drain(ctx); err != nil || st.Matched != 2 {
		t.Fatalf("re-enrich: %+v %v", st, err)
	}
	if rep, err := MergeByTMDb(ctx, e.db, 0); err != nil || rep.Merged != 0 {
		t.Fatalf("MergeByTMDb after a split = %+v, %v; want 0", rep, err)
	}
	check("after re-enrichment")
	e.scan(Options{})
	check("after a rescan")
	e.write(map[string]string{"Quorra Vale (2011)/Quorra Vale (2011) - 4K HDR.mkv": "the 4K file, remuxed"})
	e.scan(Options{Trigger: "manual"})
	check("after the file changed")

	// The original's last two versions can still be split, but not its last one.
	wp := e.id(`SELECT id FROM media_versions WHERE label = 'Workprint'`)
	if _, err := Split(ctx, e.db, wp); err != nil {
		t.Fatal(err)
	}
	if _, err := Split(ctx, e.db, e.id(`SELECT id FROM media_versions WHERE label = '1080p'`)); !errors.Is(err, ErrOnlyVersion) {
		t.Fatalf("splitting a movie's only version: %v", err)
	}
	// Merged back by hand, the split file stays merged through a rescan too.
	if err := Merge(ctx, e.db, a, []int64{s}); err != nil {
		t.Fatal(err)
	}
	e.scan(Options{})
	if got := e.strs(`SELECT IFNULL(label, '') FROM media_versions WHERE item_id = ? ORDER BY label`, a); !slices.Equal(got, []string{"1080p", "4K HDR"}) {
		t.Fatalf("after merging the split back and a rescan, original holds %q", got)
	}
}

func TestMergeAndSplitRollBack(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", twoReleases)
	e.scan(Options{})
	ctx := context.Background()
	a := e.id(`SELECT MIN(id) FROM items WHERE kind = 'movie'`)
	b := e.id(`SELECT MAX(id) FROM items WHERE kind = 'movie'`)
	pete := e.user("pete")
	e.exec(`INSERT INTO watch_state (user_id, item_id, position_ms, played, play_count, updated_at) VALUES (?, ?, 10, 1, 1, 5)`, pete, b)
	e.exec(`INSERT INTO playback_sessions (id, user_id, item_id, decision, decision_reason, started_at, last_event_at)
		VALUES ('s1', ?, ?, 'direct_play', 'test', 1, 1)`, pete, b)

	// The last step of a merge fails after every other step has run.
	e.exec(`CREATE TRIGGER fail_merge BEFORE DELETE ON items WHEN old.id = ` + itoa(b) + ` BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
	before := e.everything()
	if err := Merge(ctx, e.db, a, []int64{b}); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("merge with a failing step: %v", err)
	}
	if after := e.everything(); after != before {
		t.Fatalf("a failed merge changed the library:\n%s\nwas\n%s", after, before)
	}
	e.exec(`DROP TRIGGER fail_merge`)
	if err := Merge(ctx, e.db, a, []int64{b}); err != nil {
		t.Fatal(err)
	}

	// The last step of a split fails after the new movie was made and filled.
	e.exec(`CREATE TRIGGER fail_split BEFORE UPDATE OF item_id ON media_versions BEGIN SELECT RAISE(ABORT, 'disk full'); END`)
	v := e.id(`SELECT MAX(id) FROM media_versions WHERE item_id = ?`, a)
	before = e.everything()
	if _, err := Split(ctx, e.db, v); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("split with a failing step: %v", err)
	}
	if after := e.everything(); after != before {
		t.Fatalf("a failed split changed the library:\n%s\nwas\n%s", after, before)
	}
}

func TestMergeAndSplitRefuse(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", twoReleases)
	e.scan(Options{})
	ctx := context.Background()
	a := e.id(`SELECT MIN(id) FROM items WHERE kind = 'movie'`)
	b := e.id(`SELECT MAX(id) FROM items WHERE kind = 'movie'`)
	ep := e.id(`SELECT MIN(id) FROM items WHERE kind = 'episode'`)
	other := addLibrary(t, e.db, "Other", KindMovie, t.TempDir(), "folder")
	stranger := e.exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (?, 'movie', 'k', 'X', 'X', 0, 0)`, other.ID)
	before := e.everything()
	for _, c := range []struct {
		into int64
		from []int64
		want error
	}{
		{a, nil, ErrNothingToMerge},
		{a, []int64{a}, ErrNothingToMerge},
		{a, []int64{ep}, ErrNotMovie},
		{ep, []int64{a}, ErrNotMovie},
		{a, []int64{stranger}, ErrOtherLibrary},
		{a, []int64{b, 99999}, ErrItemNotFound},
	} {
		if err := Merge(ctx, e.db, c.into, c.from); !errors.Is(err, c.want) {
			t.Errorf("Merge(%d, %v) = %v, want %v", c.into, c.from, err, c.want)
		}
	}
	if _, err := Split(ctx, e.db, 99999); !errors.Is(err, ErrItemNotFound) {
		t.Errorf("split of no version: %v", err)
	}
	if _, err := Split(ctx, e.db, e.id(`SELECT id FROM media_versions WHERE item_id = ?`, ep)); !errors.Is(err, ErrNotMovie) {
		t.Errorf("split of an episode: %v", err)
	}
	// A scan in progress holds the library.
	e.exec(`INSERT INTO scan_runs (library_id, trigger, status, started_at) VALUES (?, 'manual', 'running', ?)`, e.lib.ID, e.clock.now().UnixMilli())
	if err := Merge(ctx, e.db, a, []int64{b}); !errors.Is(err, ErrScanRunning) {
		t.Errorf("merge during a scan: %v", err)
	}
	e.exec(`UPDATE items SET tmdb_id = 501 WHERE id IN (?, ?)`, a, b)
	if rep, err := MergeByTMDb(ctx, e.db, 0); rep.Merged != 0 || err != nil {
		t.Errorf("automatic merge during a scan = %+v, %v; want it skipped", rep, err)
	}
	e.exec(`UPDATE items SET tmdb_id = NULL WHERE id IN (?, ?)`, a, b)
	if after := e.everything(); after != before {
		t.Fatalf("a refused merge or split changed the library:\n%s\nwas\n%s", after, before)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// A merged-in file whose name gives another title neither renames the movie
// nor counts as changed on every scan.
func TestMergedFileDoesNotRenameOrChurn(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", map[string]string{
		"Amber Coast (2004)/Amber Coast (2004).mkv":             "",
		"Amber Coast Redux (2004)/Amber Coast Redux (2004).mkv": "",
	})
	e.scan(Options{})
	a := e.id(`SELECT id FROM items WHERE title = 'Amber Coast'`)
	b := e.id(`SELECT id FROM items WHERE title = 'Amber Coast Redux'`)
	if err := Merge(context.Background(), e.db, a, []int64{b}); err != nil {
		t.Fatal(err)
	}
	before := e.digest()
	run := e.scan(Options{})
	if run.Changed != 0 || run.Added != 0 {
		t.Fatalf("rescan after a merge: %+v, want nothing changed", run)
	}
	if after := e.digest(); after != before {
		t.Fatalf("rescan changed the merged movie:\n%s\nwas\n%s", after, before)
	}
	if got := titles(e, "movie"); !slices.Equal(got, []string{"Amber Coast (2004)"}) {
		t.Fatalf("movies = %q", got)
	}
}

// A scan merges copies an NFO already identifies, without waiting for
// enrichment.
func TestScanMergesCopiesWithOneNFOId(t *testing.T) {
	files := map[string]string{}
	for k, v := range twoReleases {
		files[k] = v
	}
	files["Quorra.Vale.2011.2160p.UHD.BluRay.x265-VCRXTEST/Quorra.Vale.2011.2160p.UHD.BluRay.x265-VCRXTEST.nfo"] = "https://www.themoviedb.org/movie/501"
	files["Quorra.Vale.2011.1080p.BluRay.x264-VCRXTEST/Quorra.Vale.2011.1080p.BluRay.x264-VCRXTEST.nfo"] = "https://www.themoviedb.org/movie/501"
	e := newEnv(t, KindMovie, "folder", files)
	e.scan(Options{})
	if n := e.count(`SELECT COUNT(*) FROM items WHERE kind = 'movie'`); n != 1 {
		t.Fatalf("movies after the scan = %d, want the two copies as one", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM media_versions v JOIN items i ON i.id = v.item_id WHERE i.kind = 'movie'`); n != 2 {
		t.Fatalf("versions = %d", n)
	}
}

const (
	nfo2160 = "Quorra.Vale.2011.2160p.UHD.BluRay.x265-VCRXTEST/Quorra.Vale.2011.2160p.UHD.BluRay.x265-VCRXTEST.nfo"
	nfo1080 = "Quorra.Vale.2011.1080p.BluRay.x264-VCRXTEST/Quorra.Vale.2011.1080p.BluRay.x264-VCRXTEST.nfo"
)

func movieNFODoc(rating string, tags ...string) string {
	doc := `<movie><title>Quorra Vale</title><year>2011</year><tmdbid>501</tmdbid><mpaa>` + rating + `</mpaa>`
	for _, t := range tags {
		doc += `<tag>` + t + `</tag>`
	}
	return doc + `</movie>`
}

// Access rules judge a movie by its content rating and labels, so copies that
// differ in either are never merged: merged, the stricter copy's files would
// be judged by the milder copy's rating. Once merged, a label both copies
// carry survives a rewrite of the survivor's NFO.
func TestMergeKeepsDifferentAccessApart(t *testing.T) {
	files := map[string]string{}
	for k, v := range twoReleases {
		files[k] = v
	}
	files[nfo1080] = movieNFODoc("PG-13")
	files[nfo2160] = movieNFODoc("NC-17")
	e := newEnv(t, KindMovie, "folder", files)
	ctx := context.Background()
	movies := func() int { return e.count(`SELECT COUNT(*) FROM items WHERE kind = 'movie'`) }

	e.scan(Options{})
	if n := movies(); n != 2 {
		t.Fatalf("copies rated PG-13 and NC-17 were merged: %d movies", n)
	}
	rep, err := MergeByTMDb(ctx, e.db, 0)
	if err != nil || rep.Merged != 0 || len(rep.Apart) != 1 || !strings.Contains(rep.Apart[0], "content rating NC-17 against PG-13") && !strings.Contains(rep.Apart[0], "content rating PG-13 against NC-17") {
		t.Fatalf("automatic merge of differently rated copies: %+v, %v", rep, err)
	}
	a := e.id(`SELECT MIN(id) FROM items WHERE kind = 'movie'`)
	b := e.id(`SELECT MAX(id) FROM items WHERE kind = 'movie'`)
	if err := Merge(ctx, e.db, a, []int64{b}); !errors.Is(err, ErrAccessDiffers) {
		t.Fatalf("merge by hand of differently rated copies: %v", err)
	}

	// Same rating, but only one copy carries a label someone may be denied.
	e.write(map[string]string{nfo1080: movieNFODoc("PG-13"), nfo2160: movieNFODoc("PG-13", "adult")})
	e.scan(Options{})
	if n := movies(); n != 2 {
		t.Fatalf("copies with different labels were merged: %d movies", n)
	}
	if err := Merge(ctx, e.db, a, []int64{b}); !errors.Is(err, ErrAccessDiffers) || !strings.Contains(err.Error(), "labels") {
		t.Fatalf("merge by hand of differently labelled copies: %v", err)
	}

	// Once both agree, the scan merges them.
	e.write(map[string]string{nfo1080: movieNFODoc("PG-13", "adult")})
	e.scan(Options{})
	if n := movies(); n != 1 {
		t.Fatalf("copies that agree stayed apart: %d movies", n)
	}
	survivor := e.id(`SELECT id FROM items WHERE kind = 'movie'`)
	adult := func() string {
		return e.str(`SELECT IFNULL(MAX(il.source), '') FROM item_labels il JOIN labels l ON l.id = il.label_id WHERE il.item_id = ? AND l.name = 'adult'`, survivor)
	}
	if got := adult(); got != "manual" {
		t.Fatalf("the shared label after the merge: %q", got)
	}
	// Radarr rewrites the survivor's NFO without the tag. The other copy's
	// file still declares it, so the merged movie keeps it.
	own := nfo1080
	if strings.HasSuffix(e.str(`SELECT json_extract(extra, '$.nfo_path') FROM items WHERE id = ?`, survivor), filepath.FromSlash(nfo2160)) {
		own = nfo2160
	}
	e.write(map[string]string{own: movieNFODoc("PG-13", "remastered")})
	e.scan(Options{})
	if n := e.count(`SELECT COUNT(*) FROM item_labels il JOIN labels l ON l.id = il.label_id
		WHERE il.item_id = ? AND l.name = 'remastered' AND il.source = 'nfo'`, survivor); n != 1 {
		t.Fatal("the rewritten NFO was not read, so this proves nothing")
	}
	if got := adult(); got != "manual" {
		t.Fatalf("the label vanished when the survivor's NFO was rewritten: %q", got)
	}
}
