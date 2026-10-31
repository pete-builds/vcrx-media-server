package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// homeFx is a small library built for Home's rows: titles that belong in
// every row, and three "Zebra" titles a friend may not see (a 4K-only film, a
// film and a show with a denied label) that also belong in every row.
type homeFx struct {
	t                  *testing.T
	db                 *sql.DB
	c                  *Catalog
	now                time.Time
	movies, tv         int64
	adminID, friendID  int64
	kidID              int64
	admin, friend, kid policy.Policy
	ids                map[string]int64
	hidden             []int64
	n                  int
}

type mo struct {
	year      int
	genres    []string
	directors []string
	imdb      float64
	votes     int
	res       string
	premiere  string
	added     time.Duration
	overview  string
	labels    []string
	coll      int64
	tmdb      int64
	rating    string
}

func newHomeFx(t *testing.T) *homeFx {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := db.Migrate(context.Background(), conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC)
	f := &homeFx{t: t, db: conn, now: now, ids: map[string]int64{}}
	f.c = &Catalog{DB: conn, Now: func() time.Time { return f.now }, Location: time.UTC}
	ms := now.UnixMilli()
	f.movies = f.exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('Movies', 'movie', '/m', ?, ?)`, ms, ms)
	f.tv = f.exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('TV', 'show', '/t', ?, ?)`, ms, ms)
	user := func(name string, admin bool) int64 {
		return f.exec(`INSERT INTO users (username, password_hash, is_admin, created_at, updated_at) VALUES (?, 'x', ?, ?, ?)`, name, admin, ms, ms)
	}
	f.adminID, f.friendID, f.kidID = user("admin", true), user("friend", false), user("kid", false)
	f.admin = policy.Policy{UserID: f.adminID, IsAdmin: true}
	f.friend = policy.Policy{UserID: f.friendID, LibraryIDs: []int64{f.movies, f.tv}, MaxResolution: "1080p", DenyLabels: []string{"secret"}}
	f.kid = policy.Policy{UserID: f.kidID, LibraryIDs: []int64{f.movies, f.tv}, MaxContentRating: "PG"}
	return f
}

func (f *homeFx) exec(q string, args ...any) int64 {
	f.t.Helper()
	res, err := f.db.Exec(q, args...)
	if err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (f *homeFx) tags(item int64, o mo) {
	for i, g := range o.genres {
		f.exec(`INSERT INTO item_tags (item_id, kind, value, position) VALUES (?, 'genre', ?, ?)`, item, g, i)
	}
	for i, d := range o.directors {
		f.exec(`INSERT INTO item_tags (item_id, kind, value, position) VALUES (?, 'director', ?, ?)`, item, d, i)
	}
	if o.votes == 0 {
		o.votes = 50000
	}
	if o.imdb > 0 {
		f.exec(`INSERT INTO item_ratings (item_id, source, value, votes) VALUES (?, 'imdb', ?, ?)`, item, o.imdb, o.votes)
	}
	for _, l := range o.labels {
		f.exec(`INSERT INTO labels (name) VALUES (?) ON CONFLICT (name) DO NOTHING`, l)
		f.exec(`INSERT INTO item_labels (item_id, label_id) SELECT ?, id FROM labels WHERE name = ?`, item, l)
	}
}

func (f *homeFx) version(item, lib int64, res string, added int64) {
	f.n++
	f.exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, resolution, duration_ms, probe_status, added_at, updated_at)
		VALUES (?, ?, ?, ?, 1, 0, ?, 6000000, 'ok', ?, ?)`, item, lib, fmt.Sprintf("/x/%d.mkv", f.n), fmt.Sprintf("%d.mkv", f.n), res, added, added)
}

func (f *homeFx) movie(title string, o mo) int64 {
	if o.res == "" {
		o.res = "1080p"
	}
	if o.year == 0 {
		o.year = 2015
	}
	added := f.now.Add(-o.added).UnixMilli()
	if o.added == 0 {
		added = f.now.Add(-30 * 24 * time.Hour).UnixMilli()
	}
	id := f.exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, year, overview, premiere_date, content_rating,
			tmdb_collection_id, tmdb_id, added_at, updated_at)
		VALUES (?, 'movie', ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, 0), NULLIF(?, 0), ?, ?)`,
		f.movies, title, title, title, o.year, o.overview, o.premiere, o.rating, o.coll, o.tmdb, added, added)
	f.version(id, f.movies, o.res, added)
	f.tags(id, o)
	f.ids[title] = id
	return id
}

// show adds a show with the given number of episodes per season; each
// season's episodes arrive at the matching entry of seasonAdded ago.
func (f *homeFx) show(title string, o mo, eps []int, seasonAdded []time.Duration) (int64, [][]int64) {
	first := f.now.Add(-seasonAdded[0]).UnixMilli()
	id := f.exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, year, added_at, updated_at) VALUES (?, 'show', ?, ?, ?, ?, ?, ?)`,
		f.tv, title, title, title, 2012, first, first)
	f.tags(id, o)
	var all [][]int64
	for s, n := range eps {
		at := f.now.Add(-seasonAdded[s]).UnixMilli()
		se := f.exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, added_at, updated_at)
			VALUES (?, 'season', ?, ?, ?, ?, ?, ?, ?)`, f.tv, id, fmt.Sprint(s+1), fmt.Sprintf("Season %d", s+1), fmt.Sprintf("Season %d", s+1), s+1, at, at)
		var epIDs []int64
		for e := 1; e <= n; e++ {
			ep := f.exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, episode_number, added_at, updated_at)
				VALUES (?, 'episode', ?, ?, ?, ?, ?, ?, ?, ?)`, f.tv, se, fmt.Sprint(e), fmt.Sprintf("Part %d", e), fmt.Sprintf("Part %d", e), s+1, e, at, at)
			f.version(ep, f.tv, "1080p", at)
			epIDs = append(epIDs, ep)
		}
		all = append(all, epIDs)
	}
	f.ids[title] = id
	return id, all
}

func (f *homeFx) play(user, item int64, ago time.Duration, n int) {
	for k := range n {
		f.n++
		at := f.now.Add(-ago - time.Duration(k)*time.Minute).UnixMilli()
		f.exec(`INSERT INTO playback_sessions (id, user_id, item_id, decision, decision_reason, state, played_ms, started_at, last_event_at, ended_at)
			VALUES (?, ?, ?, 'direct_play', 'test', 'stopped', 1800000, ?, ?, ?)`, fmt.Sprintf("s%d", f.n), user, item, at, at, at)
	}
}

func (f *homeFx) watched(user, item int64, ago time.Duration) {
	at := f.now.Add(-ago).UnixMilli()
	f.exec(`INSERT INTO watch_state (user_id, item_id, played, play_count, last_played_at, updated_at) VALUES (?, ?, 1, 1, ?, ?)
		ON CONFLICT (user_id, item_id) DO UPDATE SET played = 1, last_played_at = excluded.last_played_at`, user, item, at, at)
}

func (f *homeFx) home(pol policy.Policy) []HomeRow {
	f.t.Helper()
	rows, err := f.c.Home(context.Background(), pol, pol.UserID)
	if err != nil {
		f.t.Fatal(err)
	}
	return rows
}

func rowByID(rows []HomeRow, id string) *HomeRow {
	for i := range rows {
		if rows[i].ID == id {
			return &rows[i]
		}
	}
	return nil
}

func titles(r *HomeRow) []string {
	if r == nil {
		return nil
	}
	var out []string
	for _, it := range r.Items {
		out = append(out, it.Title)
	}
	return out
}

func rowIDs(rows []HomeRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

const day = 24 * time.Hour

// seed builds the full library: every row has at least four titles for the
// friend, and every row would also hold a Zebra title if the policy slipped.
func (f *homeFx) seed() {
	horror := "A night of terror on Halloween."
	for _, m := range []struct {
		title string
		o     mo
	}{
		{"Alpha Night", mo{genres: []string{"Horror"}, imdb: 7.5, overview: horror, premiere: "2026-10-01", tmdb: 103, added: 1 * day}},
		{"Bright Hollow", mo{genres: []string{"Horror"}, imdb: 6.8, added: 2 * day, premiere: "2026-09-12"}},
		{"Cold Signal", mo{genres: []string{"Thriller"}, imdb: 7.0, tmdb: 104, added: 3 * day, premiere: "2026-08-02"}},
		{"Dark Orchard", mo{genres: []string{"Horror", "Mystery"}, imdb: 7.2, added: 4 * day, premiere: "2026-07-20"}},
		{"Ember Lake", mo{genres: []string{"Thriller", "Drama"}, imdb: 8.1, tmdb: 102, added: 5 * day, premiere: "2026-06-11"}},
		{"Frost Bells", mo{genres: []string{"Comedy"}, imdb: 7.1, overview: "A Christmas story in a small town."}},
		{"Gift Wrap", mo{genres: []string{"Comedy"}, imdb: 6.5, overview: "Santa Claus takes a day off."}},
		{"Holly Lane", mo{genres: []string{"Romance", "Drama"}, imdb: 7.4, overview: "Two strangers meet on Christmas Eve."}},
		{"Silent Shift", mo{genres: []string{"Thriller"}, imdb: 6.6, overview: "A heist on Christmas Eve."}},
		{"Iron Coast", mo{genres: []string{"Action"}, imdb: 7.9, votes: 900000, tmdb: 101}},
		{"Jade River", mo{genres: []string{"Drama", "Mystery"}, directors: []string{"Ana Voss"}, imdb: 8.3, coll: 77}},
		{"Kite Hill", mo{genres: []string{"Drama", "Mystery"}, directors: []string{"Ana Voss"}, imdb: 7.7}},
		{"Lumen Park", mo{genres: []string{"Drama", "Mystery"}, imdb: 7.3}},
		{"Moss Town", mo{genres: []string{"Drama", "Mystery"}, imdb: 6.9}},
		{"North Wind", mo{genres: []string{"Drama"}, directors: []string{"Ana Voss"}, imdb: 8.0}},
		{"Quiet Harbor", mo{genres: []string{"Drama", "Mystery"}, imdb: 7.0}},
		{"Pumpkin Pals", mo{genres: []string{"Family", "Animation"}, imdb: 7.0, overview: "A Halloween costume party.", rating: "G"}},
		{"Ghost Buddies", mo{genres: []string{"Family", "Animation"}, imdb: 6.9, overview: "A friendly ghost moves in.", rating: "PG"}},
		{"Witchy Week", mo{genres: []string{"Family", "Comedy"}, imdb: 6.6, overview: "A young witch starts school.", rating: "PG"}},
		{"Monster Mash Up", mo{genres: []string{"Animation"}, imdb: 6.4, overview: "Monsters throw a party.", rating: "G"}},
		{"Iron Fog", mo{genres: []string{"Horror", "Family"}, imdb: 5.0, overview: "Halloween horror for the brave."}},
	} {
		f.movie(m.title, m.o)
	}
	// The Zebras: everything a row could want, all of it hidden from the friend.
	zebra := mo{genres: []string{"Horror", "Drama", "Mystery"}, directors: []string{"Ana Voss"}, imdb: 9.9, votes: 2000000,
		overview: "Christmas Eve and Halloween night in one.", premiere: "2026-10-30", added: time.Hour, coll: 77, tmdb: 900}
	z4k := zebra
	z4k.res = "4K"
	f.hidden = append(f.hidden, f.movie("Zebra Vault", z4k))
	zl := zebra
	zl.labels, zl.tmdb = []string{"secret"}, 901
	f.hidden = append(f.hidden, f.movie("Zebra Secret", zl))

	_, atlas := f.show("Atlas Bay", mo{imdb: 8.0}, []int{3, 3}, []time.Duration{40 * day, 2 * day})
	f.show("Birch Road", mo{imdb: 7.5}, []int{2}, []time.Duration{3 * day})
	f.show("Cedar Point", mo{imdb: 7.8}, []int{2}, []time.Duration{4 * day})
	f.show("Dune Line", mo{imdb: 8.6}, []int{2}, []time.Duration{50 * day})
	zs, zebraEps := f.show("Zebra Street", mo{imdb: 9.8, votes: 2000000, labels: []string{"secret"}}, []int{2, 2}, []time.Duration{60 * day, time.Hour})
	f.hidden = append(f.hidden, zs)

	// Plays across everyone this week, and long ago.
	for title, n := range map[string]int{"Iron Coast": 5, "Ember Lake": 3, "Alpha Night": 2, "Kite Hill": 2} {
		f.play(f.friendID, f.ids[title], 1*day, n)
	}
	f.play(f.adminID, f.ids["Cold Signal"], 2*day, 2)
	f.play(f.adminID, f.hidden[0], 1*day, 20)
	f.play(f.adminID, f.hidden[1], 1*day, 20)
	f.play(f.adminID, zebraEps[1][0], 1*day, 20)
	f.play(f.adminID, f.ids["Holly Lane"], 100*day, 8)

	// The friend: finished Jade River yesterday and Kite Hill the day before
	// (the anchors for Because You Watched), some favourites over a year ago,
	// and the first season of Atlas Bay before its second arrived.
	f.watched(f.friendID, f.ids["Jade River"], 1*day)
	f.watched(f.friendID, f.ids["Kite Hill"], 2*day)
	for _, t := range []string{"Holly Lane", "Iron Coast", "Ember Lake", "Cold Signal"} {
		f.watched(f.friendID, f.ids[t], 400*day)
	}
	f.watched(f.friendID, atlas[0][2], 10*day)
	f.watched(f.friendID, zebraEps[0][1], 10*day)

	// A collection shown on Home, one that is not, and one on Home for
	// admins only (visible: { home: true, shared: false }).
	col := func(name string, home, shared bool, members ...string) {
		id := f.exec(`INSERT INTO collections (library_id, slug, name, sort_title, source, visible_home, visible_shared, updated_at)
			VALUES (?, ?, ?, ?, 'yaml', ?, ?, 0)`, f.movies, strings.ToLower(strings.ReplaceAll(name, " ", "-")), name, name, home, shared)
		for i, m := range members {
			f.exec(`INSERT INTO collection_items (collection_id, item_id, position) VALUES (?, ?, ?)`, id, f.ids[m], i)
		}
	}
	col("Voss Films", true, true, "Zebra Secret", "Jade River", "Kite Hill", "North Wind", "Lumen Park", "Zebra Vault")
	col("Not On Home", false, true, "Alpha Night", "Bright Hollow", "Cold Signal", "Dark Orchard")
	col("Admins Only", true, false, "Iron Coast", "Ember Lake", "Holly Lane", "Gift Wrap")

	// A TMDb trending list as the refresher would have saved it.
	list, _ := json.Marshal(OnlineList{FetchedAt: 1, Items: []OnlineListItem{
		{"movie", 900}, {"movie", 101}, {"movie", 555}, {"movie", 102}, {"movie", 901}, {"movie", 103}, {"movie", 104},
	}})
	f.exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, 0)`, OnlineListKey("tmdb_trending"), string(list))
}

func TestHomeRowsNeverShowAFriendHiddenTitles(t *testing.T) {
	f := newHomeFx(t)
	f.seed()
	ctx := context.Background()
	hidden := map[int64]bool{}
	for _, id := range f.hidden {
		hidden[id] = true
	}

	// The control: an admin sees the Zebras in the rows they belong in, so
	// the friend's checks below test the policy and not an empty fixture.
	adminRows := f.home(f.admin)
	for _, id := range []string{"seasonal:halloween", "trending", "most_watched", "recently_released", "top_rated_movies",
		"top_rated_shows", "recently_added_movies:" + fmt.Sprint(f.movies), "recently_added_tv:" + fmt.Sprint(f.tv), "collection:1", "tmdb_trending"} {
		r := rowByID(adminRows, id)
		if r == nil {
			t.Fatalf("admin Home has no %s row: %v", id, rowIDs(adminRows))
		}
		if !slices.ContainsFunc(r.Items, func(s ItemSummary) bool { return hidden[s.ID] }) {
			t.Errorf("control: admin's %s row has no Zebra title: %v", id, titles(r))
		}
	}

	rows := f.home(f.friend)
	want := []string{"continue_watching", "seasonal:halloween", "seasonal:halloween:family", "because_you_watched:" + fmt.Sprint(f.ids["Jade River"]),
		"trending", "recently_released", "top_rated_movies", "top_rated_shows", "unwatched_gems", "genre:Drama", "collection:1",
		"most_watched", "most_watched_by_you", "rediscover", "best_of_decade:2010", "tmdb_trending",
		"recently_added_movies:" + fmt.Sprint(f.movies), "recently_added_tv:" + fmt.Sprint(f.tv)}
	for _, id := range want {
		if rowByID(rows, id) == nil {
			t.Errorf("friend's Home has no %s row: %v", id, rowIDs(rows))
		}
	}
	if r := rowByID(rows, "collection:2"); r != nil {
		t.Errorf("a collection not marked for Home is on Home: %v", titles(r))
	}
	if r := rowByID(rows, "collection:3"); r != nil {
		t.Errorf("a collection not shared with friends is on the friend's Home: %v", titles(r))
	}
	if rowByID(adminRows, "collection:3") == nil || rowByID(adminRows, "collection:2") != nil {
		t.Errorf("admin's collection rows: %v", rowIDs(adminRows))
	}
	check := func(where string, r HomeRow) {
		if strings.Contains(r.Title, "Zebra") {
			t.Errorf("%s: row title %q names a hidden title", where, r.Title)
		}
		for _, it := range r.Items {
			if hidden[it.ID] || strings.HasPrefix(it.Title, "Zebra") {
				t.Errorf("%s: row %s shows the hidden %q", where, r.ID, it.Title)
			}
		}
	}
	// New Seasons has one title for the friend, too few for Home; its See all
	// still must not offer the hidden show, which also had a season added.
	rows = append(rows, HomeRow{ID: "new_seasons", SeeAll: true})
	for _, r := range rows {
		check("Home", r)
		if !r.SeeAll {
			continue
		}
		all, err := f.c.HomeRowItems(ctx, f.friend, f.friendID, r.ID)
		if err != nil {
			t.Fatalf("See all %s: %v", r.ID, err)
		}
		check("See all", all)
		if len(all.Items) < len(r.Items) {
			t.Errorf("See all %s has %d titles, Home shows %d", r.ID, len(all.Items), len(r.Items))
		}
	}
	// Each row's own query applies the policy, before Summaries applies it
	// again: a row must never be built from hidden titles and then quietly
	// come up short.
	q := f.c.newHomeQuery(ctx, f.friend, f.friendID, SeeAllSize)
	for _, slot := range HomeSlots {
		built, err := q.slot(slot.Key)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range built {
			for _, id := range r.ids {
				if hidden[id] {
					t.Errorf("row %s's query picked hidden item %d", r.ID, id)
				}
			}
		}
	}
	// A row named after a hidden title is not there at all.
	if _, err := f.c.HomeRowItems(ctx, f.friend, f.friendID, "because_you_watched:"+fmt.Sprint(f.hidden[1])); !errors.Is(err, ErrNotFound) {
		t.Errorf("Because You Watched a hidden title: err = %v", err)
	}
	for _, id := range []string{"nonsense", "genre:", "collection:2", "collection:3", "collection:x", "seasonal:nope", "best_of_decade:1995", "recently_added_movies:999"} {
		if _, err := f.c.HomeRowItems(ctx, f.friend, f.friendID, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("See all %s: err = %v, want not found", id, err)
		}
	}
}

func TestHomeRowContents(t *testing.T) {
	f := newHomeFx(t)
	f.seed()
	rows := f.home(f.friend)

	// Trending weighs this week's plays; Iron Coast has the most.
	if got := titles(rowByID(rows, "trending")); len(got) == 0 || got[0] != "Iron Coast" {
		t.Errorf("trending = %v", got)
	}
	// Most watched, all time, counts Holly Lane's old plays.
	if got := titles(rowByID(rows, "most_watched")); !slices.Contains(got, "Holly Lane") {
		t.Errorf("most watched = %v", got)
	}
	// Most watched by you is the friend's own plays only.
	if got := titles(rowByID(rows, "most_watched_by_you")); slices.Contains(got, "Holly Lane") || slices.Contains(got, "Cold Signal") || got[0] != "Iron Coast" {
		t.Errorf("most watched by you = %v", got)
	}
	// Recently released runs newest premiere first.
	if got := titles(rowByID(rows, "recently_released")); len(got) < 4 || got[0] != "Alpha Night" || got[1] != "Bright Hollow" {
		t.Errorf("recently released = %v", got)
	}
	// Top rated by IMDb with enough votes.
	if got := titles(rowByID(rows, "top_rated_movies")); got[0] != "Jade River" || got[1] != "Ember Lake" {
		t.Errorf("top rated = %v", got)
	}
	if got := titles(rowByID(rows, "top_rated_shows")); got[0] != "Dune Line" {
		t.Errorf("top rated shows = %v", got)
	}
	// Rediscover: rated 7 or more and finished over a year ago.
	got := titles(rowByID(rows, "rediscover"))
	slices.Sort(got)
	if !slices.Equal(got, []string{"Cold Signal", "Ember Lake", "Holly Lane", "Iron Coast"}) {
		t.Errorf("rediscover = %v", got)
	}
	// Unwatched gems: rated 7 or more and never played.
	for _, title := range titles(rowByID(rows, "unwatched_gems")) {
		if slices.Contains([]string{"Jade River", "Kite Hill", "Iron Coast", "Ember Lake", "Alpha Night"}, title) {
			t.Errorf("unwatched gems lists %s, which the friend played", title)
		}
	}
	// New seasons: Atlas Bay's second season arrived after the friend's last episode.
	// (Its one title is too few for Home, so this reads its See all.)
	ns, err := f.c.HomeRowItems(context.Background(), f.friend, f.friendID, "new_seasons")
	if err != nil || len(ns.Items) != 1 || ns.Items[0].Title != "Atlas Bay" || ns.Items[0].Note == nil || *ns.Items[0].Note != "Season 2 added" {
		t.Errorf("new seasons = %+v, %v", ns, err)
	}
	if rowByID(rows, "new_seasons") != nil {
		t.Error("a one-title New Seasons row is on Home")
	}
	// Recently added TV: one card per show, with its new episodes counted.
	tv := rowByID(rows, "recently_added_tv:"+fmt.Sprint(f.tv))
	if got := titles(tv); !slices.Equal(got[:3], []string{"Atlas Bay", "Birch Road", "Cedar Point"}) {
		t.Errorf("recently added TV = %v", got)
	}
	if n := tv.Items[0].Note; n == nil || *n != "3 new episodes" {
		t.Errorf("Atlas Bay note = %v", n)
	}
	if n := tv.Items[3].Note; n != nil {
		t.Errorf("Dune Line, added weeks ago, has note %q", *n)
	}
	// The TMDb list in its own order, limited to the library.
	if got := titles(rowByID(rows, "tmdb_trending")); !slices.Equal(got, []string{"Iron Coast", "Ember Lake", "Alpha Night", "Cold Signal"}) {
		t.Errorf("tmdb trending = %v", got)
	}
	// The collection in its order.
	if got := titles(rowByID(rows, "collection:1")); !slices.Equal(got, []string{"Jade River", "Kite Hill", "North Wind", "Lumen Park"}) {
		t.Errorf("collection = %v", got)
	}
}

func TestBecauseYouWatchedSkipsWhatYouWatched(t *testing.T) {
	f := newHomeFx(t)
	f.seed()
	id := "because_you_watched:" + fmt.Sprint(f.ids["Jade River"])
	r := rowByID(f.home(f.friend), id)
	if r == nil || r.Title != "Because You Watched Jade River" {
		t.Fatalf("row = %+v", r)
	}
	got := titles(r)
	if slices.Contains(got, "Kite Hill") || slices.Contains(got, "Jade River") {
		t.Errorf("lists a title the friend finished: %v", got)
	}
	// Shared director counts most, then shared genres; one genre is not enough.
	if got[0] != "North Wind" || slices.Contains(got, "Ember Lake") {
		t.Errorf("order = %v", got)
	}
	// The control: once Kite Hill is not finished, it is the closest match.
	f.exec(`UPDATE watch_state SET played = 0 WHERE user_id = ? AND item_id = ?`, f.friendID, f.ids["Kite Hill"])
	got = titles(rowByID(f.home(f.friend), id))
	if !slices.Contains(got, "Kite Hill") {
		t.Errorf("control: an unfinished Kite Hill is not suggested: %v", got)
	}
}

func TestSeasonalRowsFollowTheClock(t *testing.T) {
	f := newHomeFx(t)
	f.seed()
	at := func(date string) []string {
		d, _ := time.Parse("2006-01-02", date)
		f.c.Today = func() time.Time { return d.Add(12 * time.Hour) }
		var out []string
		for _, r := range f.home(f.friend) {
			if r.Kind == "seasonal" {
				out = append(out, r.ID)
			}
		}
		return out
	}
	if got := at("2026-10-31"); !slices.Equal(got, []string{"seasonal:halloween", "seasonal:halloween:family"}) {
		t.Errorf("Oct 31: %v", got)
	}
	if got := at("2026-12-20"); !slices.Equal(got, []string{"seasonal:christmas"}) {
		t.Errorf("Dec 20: %v", got)
	}
	if got := at("2026-03-03"); len(got) != 0 {
		t.Errorf("Mar 3: %v", got)
	}
	// Halloween runs horror and thriller, strongest match first.
	f.c.Today = func() time.Time { return time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC) }
	hw := titles(rowByID(f.home(f.friend), "seasonal:halloween"))
	if hw[0] != "Alpha Night" || !slices.Contains(hw, "Cold Signal") || slices.Contains(hw, "Pumpkin Pals") || slices.Contains(hw, "Iron Fog") {
		t.Errorf("Halloween = %v", hw)
	}
	// The family row: family films about Halloween, never horror.
	fam := titles(rowByID(f.home(f.friend), "seasonal:halloween:family"))
	slices.Sort(fam)
	if !slices.Equal(fam, []string{"Ghost Buddies", "Monster Mash Up", "Pumpkin Pals", "Witchy Week"}) {
		t.Errorf("family Halloween = %v", fam)
	}
	// A kids' profile gets only the family row.
	var kids []string
	for _, r := range f.home(f.kid) {
		if r.Kind == "seasonal" {
			kids = append(kids, r.ID)
		}
	}
	if !slices.Equal(kids, []string{"seasonal:halloween:family"}) {
		t.Errorf("kid's seasonal rows = %v", kids)
	}
}

func TestSeasonDates(t *testing.T) {
	byID := map[string]Season{}
	for _, s := range DefaultSeasons {
		byID[s.ID] = s
	}
	cases := []struct {
		date   string
		active []string
	}{
		{"2026-09-30", nil}, {"2026-10-01", []string{"halloween"}}, {"2026-10-31", []string{"halloween"}},
		{"2026-11-01", nil}, {"2026-11-22", nil}, {"2026-11-23", []string{"thanksgiving"}}, {"2026-11-26", []string{"thanksgiving", "christmas"}},
		{"2026-11-29", []string{"thanksgiving", "christmas"}}, {"2026-11-30", []string{"christmas"}}, {"2026-12-31", []string{"christmas"}},
		{"2027-01-01", nil}, {"2027-02-07", []string{"valentines"}}, {"2027-02-15", nil}, {"2027-07-04", []string{"summer"}},
		{"2027-11-25", []string{"thanksgiving", "christmas"}}, {"2027-11-28", []string{"thanksgiving", "christmas"}}, {"2027-11-29", []string{"christmas"}},
	}
	for _, c := range cases {
		d, _ := time.Parse("2006-01-02", c.date)
		var got []string
		for _, s := range ActiveSeasons(DefaultSeasons, d) {
			got = append(got, s.ID)
		}
		if !slices.Equal(got, c.active) {
			t.Errorf("%s: active %v, want %v", c.date, got, c.active)
		}
	}
	if tg := thanksgiving(2026); tg.Day() != 26 {
		t.Errorf("Thanksgiving 2026 = %v", tg)
	}
	// A season may wrap the new year.
	wrap := Season{ID: "w", Title: "W", Start: "12-26", End: "01-05", SeasonRule: SeasonRule{Keywords: []string{"x"}}}
	for date, want := range map[string]bool{"2026-12-25": false, "2026-12-30": true, "2027-01-03": true, "2027-01-06": false} {
		d, _ := time.Parse("2006-01-02", date)
		if wrap.Active(d) != want {
			t.Errorf("wrapping season on %s: %v", date, !want)
		}
	}
}

func TestSeasonsYAMLOverride(t *testing.T) {
	merged, err := mergeSeasons(DefaultSeasons, []byte(`
seasons:
  - id: halloween
    enabled: false
  - id: christmas
    title: Holiday Classics
    start: "12-01"
    end: "12-25"
    keywords: [christmas]
  - id: hockey
    title: Playoff Hockey
    start: "04-15"
    end: "06-15"
    genres: [Sport]
    keywords: [hockey]
`))
	if err != nil {
		t.Fatal(err)
	}
	ids := func(date string) []string {
		d, _ := time.Parse("2006-01-02", date)
		var out []string
		for _, s := range ActiveSeasons(merged, d) {
			out = append(out, s.ID+"="+s.Title)
		}
		return out
	}
	if got := ids("2026-10-15"); len(got) != 0 {
		t.Errorf("halloween turned off, yet Oct 15 has %v", got)
	}
	if got := ids("2026-11-28"); !slices.Equal(got, []string{"thanksgiving=Thanksgiving Week"}) {
		t.Errorf("Nov 28: %v", got)
	}
	if got := ids("2026-12-10"); !slices.Equal(got, []string{"christmas=Holiday Classics"}) {
		t.Errorf("Dec 10: %v", got)
	}
	if got := ids("2026-05-01"); !slices.Equal(got, []string{"hockey=Playoff Hockey"}) {
		t.Errorf("May 1: %v", got)
	}
	for _, bad := range []string{
		"seasons:\n  - id: x\n    title: X\n    start: 13-01\n    end: 12-01\n    keywords: [a]\n",
		"seasons:\n  - id: x\n    title: X\n    start: 01-01\n    end: 02-01\n",
		"seasons:\n  - title: X\n",
		"seasons:\n  - id: x\n    title: X\n    start: 01-01\n    end: 02-01\n    keywords: [a]\n    colour: red\n",
	} {
		if _, err := mergeSeasons(DefaultSeasons, []byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestShortRowsAreHidden(t *testing.T) {
	f := newHomeFx(t)
	for i, title := range []string{"One", "Two", "Three"} {
		f.movie(title, mo{genres: []string{"Western"}, imdb: 7, added: time.Duration(i+1) * day})
	}
	movies := "recently_added_movies:" + fmt.Sprint(f.movies)
	if r := rowByID(f.home(f.admin), movies); r != nil {
		t.Errorf("a three-title row shows: %v", titles(r))
	}
	// Continue Watching shows with a single title.
	f.exec(`INSERT INTO watch_state (user_id, item_id, position_ms, last_played_at, updated_at) VALUES (?, ?, 600000, ?, ?)`,
		f.adminID, f.ids["One"], f.now.UnixMilli(), f.now.UnixMilli())
	f.movie("Four", mo{genres: []string{"Western"}, imdb: 7})
	rows := f.home(f.admin)
	if r := rowByID(rows, movies); r == nil || len(r.Items) != 4 {
		t.Errorf("a four-title row does not show: %v", rowIDs(rows))
	}
	if r := rowByID(rows, "continue_watching"); r == nil || len(r.Items) != 1 {
		t.Errorf("Continue Watching with one title is hidden: %v", rowIDs(rows))
	}
	// See all of a row with nothing in it is an empty row, not a crash (it
	// once indexed an empty result): nothing is like a lone Western yet.
	if r, err := f.c.HomeRowItems(context.Background(), f.admin, f.adminID, "because_you_watched:"+fmt.Sprint(f.ids["One"])); err != nil || len(r.Items) != 0 || r.Title != "Because You Watched One" {
		t.Errorf("empty See all: %+v, %v", r, err)
	}
	// See all has no minimum.
	f.exec(`UPDATE items SET missing_since = 1 WHERE title IN ('Two', 'Three')`)
	if r, err := f.c.HomeRowItems(context.Background(), f.admin, f.adminID, movies); err != nil || len(r.Items) != 2 {
		t.Errorf("See all: %v, %v", titles(&r), err)
	}
}

func TestHomeLayoutOrderAndHide(t *testing.T) {
	f := newHomeFx(t)
	f.seed()
	ctx := context.Background()
	kinds := func(rows []HomeRow) []string {
		var out []string
		for _, r := range rows {
			if len(out) == 0 || out[len(out)-1] != r.Kind {
				out = append(out, r.Kind)
			}
		}
		return out
	}
	before := kinds(f.home(f.friend))
	if !slices.Equal(before[:3], []string{"continue_watching", "recently_added_movies", "recently_added_tv"}) || !slices.Contains(before, "trending") {
		t.Fatalf("default order = %v", before)
	}
	// The friend puts Trending first and hides Recently Added Movies.
	if err := f.c.SetHomeLayout(ctx, f.friendID, []LayoutEntry{{"trending", true}, {"recently_added_movies", false}}); err != nil {
		t.Fatal(err)
	}
	after := kinds(f.home(f.friend))
	if after[0] != "trending" || slices.Contains(after, "recently_added_movies") {
		t.Errorf("friend's order = %v", after)
	}
	// Slots the layout leaves out keep their default, after the listed ones.
	if !slices.Equal(after[1:3], []string{"continue_watching", "recently_added_tv"}) {
		t.Errorf("unlisted slots: %v", after)
	}
	l, err := f.c.HomeLayout(ctx, f.friendID)
	if err != nil || l.Source != "user" || len(l.Rows) != len(HomeSlots) || l.Rows[0].Key != "trending" || l.Rows[1].Enabled {
		t.Errorf("layout = %+v, %v", l, err)
	}
	// Nobody else is affected.
	if got := kinds(f.home(f.kid)); got[0] == "trending" {
		t.Errorf("the kid got the friend's layout: %v", got)
	}
	// The server default applies to everyone without their own.
	if err := f.c.SetHomeLayout(ctx, 0, []LayoutEntry{{"top_rated_movies", true}, {"seasonal", false}}); err != nil {
		t.Fatal(err)
	}
	kid := kinds(f.home(f.kid))
	if kid[0] != "top_rated_movies" || slices.Contains(kid, "seasonal") {
		t.Errorf("kid under the server default = %v", kid)
	}
	if got := kinds(f.home(f.friend)); got[0] != "trending" || !slices.Contains(got, "seasonal") {
		t.Errorf("the friend's own layout lost to the server default: %v", got)
	}
	// Resetting the friend's layout brings the server default back.
	if err := f.c.ResetHomeLayout(ctx, f.friendID); err != nil {
		t.Fatal(err)
	}
	if l, _ := f.c.HomeLayout(ctx, f.friendID); l.Source != "server" || l.Rows[0].Key != "top_rated_movies" {
		t.Errorf("after reset: %+v", l)
	}
	// Unknown and repeated slots are refused.
	for _, bad := range [][]LayoutEntry{{{"nope", true}}, {{"trending", true}, {"trending", false}}} {
		if err := f.c.SetHomeLayout(ctx, f.friendID, bad); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

// The Home endpoint's cost on a library of 5,000 films and 500 shows with
// 20,000 plays. Seeding takes a while, so it runs only with VCRX_HOME_PERF=1:
//
//	VCRX_HOME_PERF=1 go test ./internal/catalog -run HomePerformance -v
func TestHomePerformance(t *testing.T) {
	if testing.Short() || !envOn("VCRX_HOME_PERF") {
		t.Skip("set VCRX_HOME_PERF=1 to measure")
	}
	f := newHomeFx(t)
	start := time.Now()
	seedLarge(t, f)
	t.Logf("seeded in %v", time.Since(start).Round(time.Millisecond))
	ctx := context.Background()
	for _, who := range []struct {
		name string
		pol  policy.Policy
	}{{"admin", f.admin}, {"friend", f.friend}} {
		var times []time.Duration
		var n, size int
		for range 6 {
			t0 := time.Now()
			rows, err := f.c.Home(ctx, who.pol, who.pol.UserID)
			if err != nil {
				t.Fatal(err)
			}
			// The handler's other cost: encoding the answer.
			b, err := json.Marshal(map[string]any{"rows": rows})
			if err != nil {
				t.Fatal(err)
			}
			times = append(times, time.Since(t0))
			n, size = len(rows), len(b)
		}
		slices.Sort(times)
		t.Logf("%s: %d rows, %d KB, fastest %v, median %v, slowest %v", who.name, n, size/1024, times[0], times[len(times)/2], times[len(times)-1])
		if times[len(times)/2] > 2*time.Second {
			t.Errorf("%s Home took %v", who.name, times[len(times)/2])
		}
	}
	// Per row, to see where the time goes.
	q := f.c.newHomeQuery(ctx, f.friend, f.friendID, HomeRowSize)
	for _, s := range HomeSlots {
		t0 := time.Now()
		rows, err := q.slot(s.Key)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("  %-22s %3d rows %v", s.Key, len(rows), time.Since(t0).Round(100*time.Microsecond))
	}
}
