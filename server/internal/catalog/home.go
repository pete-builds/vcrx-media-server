package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/activity"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// Home's shape. A row with fewer than MinRowItems titles is left out (Continue
// Watching excepted), so Home never shows a stub of one or two posters.
const (
	HomeRowSize  = 20
	SeeAllSize   = 100
	MinRowItems  = 4
	imdbMinVotes = 5000
	tmdbMinVotes = 200
	// goodScore is "highly rated" for Unwatched Gems and Rediscover.
	goodScore         = 7.0
	trendingWindow    = 7 * 24 * time.Hour
	rediscoverAfter   = 365 * 24 * time.Hour
	newEpisodeWindow  = 14 * 24 * time.Hour
	maxGenreRows      = 3
	minGenreTitles    = 8
	maxCollectionRows = 6
	maxBecauseRows    = 2
	minDecadeTitles   = 10
)

// rated scores every title with a rating that has enough votes behind it:
// IMDb first, TMDb when IMDb has too few votes.
var ratedCTE = `rated AS (SELECT r.item_id AS id,
		COALESCE(MAX(CASE WHEN r.source = 'imdb' AND r.votes >= ` + strconv.Itoa(imdbMinVotes) + ` THEN r.value END),
			MAX(CASE WHEN r.source = 'tmdb' AND r.votes >= ` + strconv.Itoa(tmdbMinVotes) + ` THEN r.value END)) AS score,
		MAX(COALESCE(r.votes, 0)) AS votes
	FROM item_ratings r WHERE r.source IN ('imdb', 'tmdb') GROUP BY r.item_id)`

// scoreOf is one title's rating as rated scores it, for queries that rank
// only a few candidates and need not score the whole library.
var scoreOf = `(SELECT COALESCE(MAX(CASE WHEN r.source = 'imdb' AND r.votes >= ` + strconv.Itoa(imdbMinVotes) + ` THEN r.value END),
		MAX(CASE WHEN r.source = 'tmdb' AND r.votes >= ` + strconv.Itoa(tmdbMinVotes) + ` THEN r.value END))
	FROM item_ratings r WHERE r.item_id = i.id)`

// shuffle orders titles in a way that changes once a day and is the same for
// everyone that day, so a row does not reshuffle on every visit.
const shuffle = `((i.id * 2654435761 + ?) % 4294967291)`

// homeQuery carries one request's caller and clock through the row builders.
type homeQuery struct {
	c     *Catalog
	ctx   context.Context
	pol   policy.Policy
	user  int64
	now   time.Time
	today time.Time
	day   int64
	cond  string
	args  []any
	// libCond is the cheap part of the policy, granted libraries and present
	// titles, for ranking queries whose candidates ranked then checks in full.
	libCond string
	libArgs []any
	limit   int
}

// builtRow is a row before its items are loaded: ids in order, plus a note
// for some cards, such as "3 new episodes".
type builtRow struct {
	HomeRow
	ids   []int64
	notes map[int64]string
}

func (c *Catalog) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// homeDate is the local date seasons and daily rotation go by. Today, when
// set, overrides it (tests, and the dev server's fixed date).
func (c *Catalog) homeDate() time.Time {
	if c.Today != nil {
		return c.Today()
	}
	loc := c.Location
	if loc == nil {
		loc = time.Local
	}
	return c.now().In(loc)
}

func (c *Catalog) seasons() []Season {
	if c.Seasons != nil {
		return c.Seasons
	}
	return DefaultSeasons
}

func (c *Catalog) newHomeQuery(ctx context.Context, pol policy.Policy, userID int64, limit int) *homeQuery {
	cond, args := pol.ItemCond("i")
	today := c.homeDate()
	day := int64(today.Year())*1000 + int64(today.YearDay())
	libCond, libArgs := "i.missing_since IS NULL", []any(nil)
	if !pol.IsAdmin {
		libCond = "0"
		if len(pol.LibraryIDs) > 0 {
			libCond = "i.missing_since IS NULL AND i.library_id IN (" + placeholders(len(pol.LibraryIDs)) + ")"
			libArgs = int64Args(pol.LibraryIDs)
		}
	}
	return &homeQuery{c: c, ctx: ctx, pol: pol, user: userID, now: c.now(), today: today, day: day, cond: cond, args: args,
		libCond: libCond, libArgs: libArgs, limit: limit}
}

// Home builds the caller's Home rows in their layout's order: each enabled
// slot becomes zero or more rows, every query runs under the caller's policy,
// and rows too short to be worth a line are dropped.
func (c *Catalog) Home(ctx context.Context, pol policy.Policy, userID int64) ([]HomeRow, error) {
	layout, err := c.HomeLayout(ctx, userID)
	if err != nil {
		return nil, err
	}
	q := c.newHomeQuery(ctx, pol, userID, HomeRowSize)
	var built []builtRow
	for _, slot := range layout.Rows {
		if !slot.Enabled {
			continue
		}
		rows, err := q.slot(slot.Key)
		if err != nil {
			return nil, fmt.Errorf("home row %s: %w", slot.Key, err)
		}
		built = append(built, rows...)
	}
	return q.load(built, true)
}

// HomeRowItems is a row's "See all": the same row with up to SeeAllSize
// titles. An unknown row, or one the caller may not see, is ErrNotFound.
func (c *Catalog) HomeRowItems(ctx context.Context, pol policy.Policy, userID int64, rowID string) (HomeRow, error) {
	q := c.newHomeQuery(ctx, pol, userID, SeeAllSize)
	kind, arg, _ := strings.Cut(rowID, ":")
	r, err := q.build(kind, arg, false)
	if err != nil {
		return HomeRow{}, err
	}
	if r == nil {
		return HomeRow{}, ErrNotFound
	}
	rows, err := q.load([]builtRow{*r}, false)
	if err != nil {
		return HomeRow{}, err
	}
	return rows[0], nil
}

// load fills every row's items with one batched summary query, so a title in
// several rows is loaded once.
func (q *homeQuery) load(built []builtRow, dropShort bool) ([]HomeRow, error) {
	var all []int64
	seen := map[int64]bool{}
	for _, r := range built {
		for _, id := range r.ids {
			if !seen[id] {
				seen[id] = true
				all = append(all, id)
			}
		}
	}
	sums, err := q.c.Summaries(q.ctx, q.pol, q.user, all)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]ItemSummary, len(sums))
	for _, s := range sums {
		byID[s.ID] = s
	}
	out := []HomeRow{}
	for _, r := range built {
		row := r.HomeRow
		row.Items = []ItemSummary{}
		for _, id := range r.ids {
			s, ok := byID[id]
			if !ok {
				continue
			}
			if n := r.notes[id]; n != "" {
				s.Note = &n
			}
			row.Items = append(row.Items, s)
		}
		if dropShort && len(row.Items) < MinRowItems && row.ID != "continue_watching" {
			continue
		}
		if len(row.Items) == 0 {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

// slot expands one layout slot into its rows.
func (q *homeQuery) slot(key string) ([]builtRow, error) {
	var out []builtRow
	add := func(kind, arg string) error {
		r, err := q.build(kind, arg, true)
		if err == nil && r != nil {
			out = append(out, *r)
		}
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	switch key {
	case "recently_added_movies", "recently_added_tv":
		libs, err := q.c.Libraries(q.ctx, q.pol)
		if err != nil {
			return nil, err
		}
		want := "movie"
		if key == "recently_added_tv" {
			want = "show"
		}
		for _, l := range libs {
			if l.Kind == want {
				if err := add(key, strconv.FormatInt(l.ID, 10)); err != nil {
					return nil, err
				}
			}
		}
	case "seasonal":
		kids := kidsRatings[strings.ToUpper(q.pol.MaxContentRating)] && !q.pol.IsAdmin
		for _, s := range ActiveSeasons(q.c.seasons(), q.today) {
			if !kids {
				if err := add("seasonal", s.ID); err != nil {
					return nil, err
				}
			}
			if s.Family != nil {
				if err := add("seasonal", s.ID+":family"); err != nil {
					return nil, err
				}
			}
		}
	case "because_you_watched":
		anchors, err := q.recentAnchors()
		if err != nil {
			return nil, err
		}
		n := 0
		for _, a := range anchors {
			r, err := q.build("because_you_watched", strconv.FormatInt(a, 10), true)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return nil, err
			}
			// Only anchors with enough like them count toward the rows shown.
			if r != nil && len(r.ids) >= MinRowItems {
				out = append(out, *r)
				if n++; n == maxBecauseRows {
					break
				}
			}
		}
	case "genres":
		genres, err := q.genresOfTheDay()
		if err != nil {
			return nil, err
		}
		for _, g := range genres {
			if err := add("genre", g); err != nil {
				return nil, err
			}
		}
	case "collections":
		ids, err := q.homeCollections()
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if err := add("collection", strconv.FormatInt(id, 10)); err != nil {
				return nil, err
			}
		}
	case "best_of_decade":
		d, err := q.decadeOfTheDay()
		if err != nil || d == 0 {
			return nil, err
		}
		if err := add("best_of_decade", strconv.Itoa(d)); err != nil {
			return nil, err
		}
	default:
		if err := add(key, ""); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// build makes one row from its kind and argument, the two halves of a row id
// such as "genre:Comedy". Nil means the row has nothing for this caller;
// ErrNotFound means there is no such row for them at all.
func (q *homeQuery) build(kind, arg string, forHome bool) (*builtRow, error) {
	row := func(title string, ids []int64, err error) (*builtRow, error) {
		if err != nil {
			return nil, err
		}
		id := kind
		if arg != "" {
			id += ":" + arg
		}
		return &builtRow{HomeRow: HomeRow{ID: id, Kind: kind, Title: title, SeeAll: kind != "continue_watching"}, ids: ids}, nil
	}
	switch kind {
	case "continue_watching":
		if forHome {
			ids, err := q.c.continueWatching(q.ctx, q.pol, q.user, q.now)
			return row("Continue Watching", ids, err)
		}
		return nil, ErrNotFound
	case "recently_added_movies", "recently_added_tv":
		return q.recentlyAdded(kind, arg)
	case "seasonal":
		return q.seasonal(arg, forHome)
	case "new_seasons":
		r, err := q.newSeasons()
		if r != nil {
			r.HomeRow = HomeRow{ID: kind, Kind: kind, Title: "New Seasons", SeeAll: true}
		}
		return r, err
	case "because_you_watched":
		return q.becauseYouWatched(arg)
	case "trending":
		ids, err := q.plays(q.now.Add(-trendingWindow), 0)
		return row("Trending on This Server", ids, err)
	case "most_watched":
		ids, err := q.plays(time.Time{}, 0)
		return row("Most Watched", ids, err)
	case "most_watched_by_you":
		ids, err := q.plays(time.Time{}, q.user)
		return row("Most Watched by You", ids, err)
	case "recently_released":
		ids, err := q.ranked(`SELECT i.id FROM items i WHERE i.kind = 'movie' AND i.parent_id IS NULL AND `+q.libCond+`
			AND i.premiere_date IS NOT NULL AND i.premiere_date <> '' AND i.premiere_date <= ?
			ORDER BY i.premiere_date DESC, i.id DESC LIMIT ? OFFSET ?`, sqlArgs(q.libArgs), q.today.Format("2006-01-02"))
		return row("Recently Released", ids, err)
	case "top_rated_movies", "top_rated_shows":
		k, title := "movie", "Top Rated Movies"
		if kind == "top_rated_shows" {
			k, title = "show", "Top Rated Shows"
		}
		ids, err := q.ranked(`WITH `+ratedCTE+` SELECT i.id FROM rated JOIN items i ON i.id = rated.id
			WHERE rated.score IS NOT NULL AND i.kind = ? AND i.parent_id IS NULL AND `+q.libCond+`
			ORDER BY rated.score DESC, rated.votes DESC, i.id LIMIT ? OFFSET ?`, k, sqlArgs(q.libArgs))
		return row(title, ids, err)
	case "unwatched_gems":
		ids, err := q.ranked(`WITH `+ratedCTE+`, `+touchedCTE+` SELECT i.id FROM rated JOIN items i ON i.id = rated.id
			WHERE rated.score >= ? AND i.kind IN ('movie', 'show') AND i.parent_id IS NULL AND `+q.libCond+`
			AND i.id NOT IN (SELECT top FROM touched) ORDER BY `+shuffle+` LIMIT ? OFFSET ?`,
			q.user, q.user, goodScore, sqlArgs(q.libArgs), q.day)
		return row("Unwatched Gems", ids, err)
	case "rediscover":
		ids, err := q.ids(`WITH `+ratedCTE+`, `+finishedCTE+`
			SELECT i.id FROM finished JOIN items i ON i.id = finished.top JOIN rated ON rated.id = i.id
			WHERE finished.last < ? AND rated.score >= ? AND `+q.cond+` ORDER BY `+shuffle+` LIMIT ?`,
			q.user, q.now.Add(-rediscoverAfter).UnixMilli(), goodScore, sqlArgs(q.args), q.day)
		return row("Rediscover", ids, err)
	case "genre":
		if arg == "" {
			return nil, ErrNotFound
		}
		ids, err := q.ranked(`SELECT i.id FROM item_tags t JOIN items i ON i.id = t.item_id
			WHERE t.kind = 'genre' AND t.value = ? AND i.kind = 'movie' AND i.parent_id IS NULL AND `+q.libCond+`
			ORDER BY `+shuffle+` LIMIT ? OFFSET ?`, arg, sqlArgs(q.libArgs), q.day)
		return row(arg+" Movies", ids, err)
	case "best_of_decade":
		d, err := strconv.Atoi(arg)
		if err != nil || d < 1900 || d%10 != 0 {
			return nil, ErrNotFound
		}
		ids, err := q.ranked(`WITH `+ratedCTE+` SELECT i.id FROM rated JOIN items i ON i.id = rated.id
			WHERE rated.score IS NOT NULL AND i.kind = 'movie' AND i.parent_id IS NULL AND i.year BETWEEN ? AND ? AND `+q.libCond+`
			ORDER BY rated.score DESC, rated.votes DESC, i.id LIMIT ? OFFSET ?`, d, d+9, sqlArgs(q.libArgs))
		return row("Best of the "+decadeName(d), ids, err)
	case "collection":
		return q.collection(arg)
	case "tmdb_trending", "tmdb_popular":
		title := "Trending on TMDb"
		if kind == "tmdb_popular" {
			title = "Popular on TMDb"
		}
		ids, err := q.onlineList(kind)
		if err == nil && ids == nil {
			return nil, ErrNotFound
		}
		return row(title, ids, err)
	}
	return nil, ErrNotFound
}

// ids runs a row query. The query's own arguments come first, then the
// policy's, then the limit; pass sqlArgs(q.args) explicitly where the policy
// condition is not last.
func (q *homeQuery) ids(query string, args ...any) ([]int64, error) {
	var all []any
	explicit := false
	for _, a := range args {
		if s, ok := a.(sqlArgs); ok {
			all = append(all, []any(s)...)
			explicit = true
			continue
		}
		all = append(all, a)
	}
	if !explicit {
		all = append(all, q.args...)
	}
	all = append(all, q.limit)
	return q.c.queryIDs(q.ctx, query, all...)
}

// sqlArgs marks where the policy's arguments go in a row query.
type sqlArgs []any

// touchedCTE is every movie or show the user has played at all: finished,
// started, or a playback session that played (an episode counts for its
// show). Built once per query from the user's own rows, so excluding these is
// one lookup per title. Takes the user id twice.
const touchedCTE = `touched AS (
	SELECT COALESCE(sh.id, ti.id) AS top FROM watch_state w JOIN items ti ON ti.id = w.item_id
		LEFT JOIN items se ON se.id = ti.parent_id AND ti.kind = 'episode' LEFT JOIN items sh ON sh.id = se.parent_id
		WHERE w.user_id = ? AND (w.played = 1 OR w.position_ms > 0 OR w.play_count > 0)
	UNION
	SELECT COALESCE(sh.id, ti.id) FROM playback_sessions p JOIN items ti ON ti.id = p.item_id
		LEFT JOIN items se ON se.id = ti.parent_id AND ti.kind = 'episode' LEFT JOIN items sh ON sh.id = se.parent_id
		WHERE p.user_id = ? AND p.played_ms > 0)`

// finishedCTE is each movie or show the user has finished (a show counts once
// they finish any episode) with when they last did. Takes the user id.
const finishedCTE = `finished AS (SELECT COALESCE(sh.id, fi.id) AS top, MAX(w.last_played_at) AS last
	FROM watch_state w JOIN items fi ON fi.id = w.item_id
	LEFT JOIN items se ON se.id = fi.parent_id AND fi.kind = 'episode' LEFT JOIN items sh ON sh.id = se.parent_id
	WHERE w.user_id = ? AND w.played = 1 AND w.last_played_at IS NOT NULL AND fi.kind IN ('movie', 'episode')
	GROUP BY COALESCE(sh.id, fi.id))`

// rankedPage is how many candidates ranked reads at a time.
const rankedPage = 200

// ranked runs a row's ranking query, which leaves the policy out and ends in
// LIMIT ? OFFSET ?, and keeps the titles the caller may see, in order, page by
// page until the row is full. Ranking without the policy and checking it only
// on the few candidates a row needs is what keeps Home fast on a large
// library; the check itself is the same ItemCond every other query uses.
// Arguments as for ids.
func (q *homeQuery) ranked(query string, args ...any) ([]int64, error) {
	var base []any
	for _, a := range args {
		if s, ok := a.(sqlArgs); ok {
			base = append(base, []any(s)...)
			continue
		}
		base = append(base, a)
	}
	out := []int64{}
	for offset := 0; len(out) < q.limit; offset += rankedPage {
		cands, err := q.c.queryIDs(q.ctx, query, append(append([]any{}, base...), rankedPage, offset)...)
		if err != nil {
			return nil, err
		}
		more, err := q.visibleTop(cands)
		if err != nil {
			return nil, err
		}
		for _, id := range more {
			if len(out) < q.limit {
				out = append(out, id)
			}
		}
		if len(cands) < rankedPage {
			break
		}
	}
	return out, nil
}

// visibleSet reports which of ids, of any kind, the caller may see.
func (q *homeQuery) visibleSet(ids []int64) (map[int64]bool, error) {
	ok := make(map[int64]bool, len(ids))
	for start := 0; start < len(ids); start += 400 {
		chunk := ids[start:min(start+400, len(ids))]
		rows, err := q.c.queryIDs(q.ctx, `SELECT i.id FROM items i WHERE i.id IN (`+placeholders(len(chunk))+`) AND `+q.cond,
			append(int64Args(chunk), q.args...)...)
		if err != nil {
			return nil, err
		}
		for _, id := range rows {
			ok[id] = true
		}
	}
	return ok, nil
}

// visibleTop keeps the ids the caller may see that are movies or shows, in
// order, up to the limit. Candidates come from queries that cannot carry the
// policy themselves, such as play counts grouped across users.
func (q *homeQuery) visibleTop(ids []int64) ([]int64, error) {
	out := []int64{}
	for start := 0; start < len(ids) && len(out) < q.limit; start += 400 {
		chunk := ids[start:min(start+400, len(ids))]
		rows, err := q.c.queryIDs(q.ctx, `SELECT i.id FROM items i WHERE i.id IN (`+placeholders(len(chunk))+`)
			AND i.parent_id IS NULL AND i.kind IN ('movie', 'show') AND `+q.cond, append(int64Args(chunk), q.args...)...)
		if err != nil {
			return nil, err
		}
		ok := make(map[int64]bool, len(rows))
		for _, id := range rows {
			ok[id] = true
		}
		for _, id := range chunk {
			if ok[id] && len(out) < q.limit {
				out = append(out, id)
			}
		}
	}
	return out, nil
}

func (q *homeQuery) recentlyAdded(kind, arg string) (*builtRow, error) {
	lib, err := strconv.ParseInt(arg, 10, 64)
	if err != nil || !q.pol.CanSeeLibrary(lib) {
		return nil, ErrNotFound
	}
	libs, err := q.c.Libraries(q.ctx, q.pol)
	if err != nil {
		return nil, err
	}
	var name string
	same := 0
	want := map[string]string{"recently_added_movies": "movie", "recently_added_tv": "show"}[kind]
	found := false
	for _, l := range libs {
		if l.Kind == want {
			same++
		}
		if l.ID == lib && l.Kind == want {
			name, found = l.Name, true
		}
	}
	if !found {
		return nil, ErrNotFound
	}
	title := "Recently Added Movies"
	if want == "show" {
		title = "Recently Added TV"
	}
	if same > 1 {
		title = "Recently Added in " + name
	}
	r := &builtRow{HomeRow: HomeRow{ID: kind + ":" + arg, Kind: kind, Title: title, LibraryID: &lib, SeeAll: true}}
	if want == "movie" {
		r.ids, err = q.ranked(`SELECT i.id FROM items i WHERE i.library_id = ? AND i.kind = 'movie' AND i.missing_since IS NULL
			ORDER BY i.added_at DESC, i.id DESC LIMIT ? OFFSET ?`, lib)
		return r, err
	}
	// Shows in the order their newest episode arrived, counting only episodes
	// the caller may see, so a hidden episode never moves a show up. Several
	// new episodes of one show are one card: "3 new episodes". Episodes are
	// read newest first and only as far as needed: every one inside the
	// counting window, then on until enough shows are found.
	cutoff := q.now.Add(-newEpisodeWindow).UnixMilli()
	fresh := map[int64]int{}
	var order []int64
	const page = 1000
	for offset := 0; ; offset += page {
		rows, err := q.c.DB.QueryContext(q.ctx, `SELECT e.id, se.parent_id, e.added_at FROM items e JOIN items se ON se.id = e.parent_id
			WHERE e.library_id = ? AND e.kind = 'episode' AND e.missing_since IS NULL
			ORDER BY e.added_at DESC, e.id DESC LIMIT ? OFFSET ?`, lib, page, offset)
		if err != nil {
			return nil, err
		}
		type ep struct{ id, show, added int64 }
		var eps []ep
		err = each(rows, func() error {
			var e ep
			if err := rows.Scan(&e.id, &e.show, &e.added); err != nil {
				return err
			}
			eps = append(eps, e)
			return nil
		})
		if err != nil {
			return nil, err
		}
		ids := make([]int64, len(eps))
		for k, e := range eps {
			ids[k] = e.id
		}
		ok, err := q.visibleSet(ids)
		if err != nil {
			return nil, err
		}
		done := len(eps) < page
		for _, e := range eps {
			if !ok[e.id] {
				continue
			}
			// A few spare shows, in case the show-level check drops some.
			if e.added < cutoff && len(order) >= q.limit+10 {
				done = true
				break
			}
			if _, seen := fresh[e.show]; !seen {
				fresh[e.show] = 0
				order = append(order, e.show)
			}
			if e.added >= cutoff {
				fresh[e.show]++
			}
		}
		if done {
			break
		}
	}
	if r.ids, err = q.visibleTop(order); err != nil {
		return nil, err
	}
	r.notes = map[int64]string{}
	for _, id := range r.ids {
		switch n := fresh[id]; {
		case n == 1:
			r.notes[id] = "1 new episode"
		case n > 1:
			r.notes[id] = strconv.Itoa(n) + " new episodes"
		}
	}
	return r, nil
}

// newSeasons: shows the caller has watched where a season they may see
// arrived after the last time they watched any episode.
func (q *homeQuery) newSeasons() (*builtRow, error) {
	// From the user's own watch rows outward (CROSS JOIN fixes that order),
	// then the policy on the few seasons and shows found.
	rows, err := q.c.DB.QueryContext(q.ctx, `WITH watched AS (
			SELECT se.parent_id AS show_id, MAX(w.last_played_at) AS last
			FROM watch_state w JOIN items e ON e.id = w.item_id AND e.kind = 'episode' JOIN items se ON se.id = e.parent_id
			WHERE w.user_id = ? AND w.last_played_at IS NOT NULL AND (w.played = 1 OR w.position_ms > 0)
			GROUP BY se.parent_id)
		SELECT s.id, watched.show_id, s.season_number, s.added_at FROM watched CROSS JOIN items s ON s.parent_id = watched.show_id
		WHERE s.kind = 'season' AND s.added_at > watched.last AND s.season_number > 0 AND s.missing_since IS NULL
		ORDER BY s.added_at DESC, s.id DESC`, q.user)
	if err != nil {
		return nil, err
	}
	type season struct{ id, show, num, added int64 }
	var found []season
	err = each(rows, func() error {
		var x season
		if err := rows.Scan(&x.id, &x.show, &x.num, &x.added); err != nil {
			return err
		}
		found = append(found, x)
		return nil
	})
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(found))
	for k, x := range found {
		ids[k] = x.id
	}
	ok, err := q.visibleSet(ids)
	if err != nil {
		return nil, err
	}
	r := &builtRow{notes: map[int64]string{}}
	newest := map[int64]int64{}
	var shows []int64
	for _, x := range found {
		if !ok[x.id] {
			continue
		}
		if _, seen := newest[x.show]; !seen {
			shows = append(shows, x.show)
		}
		newest[x.show] = max(newest[x.show], x.num)
	}
	if r.ids, err = q.visibleTop(shows); err != nil {
		return nil, err
	}
	for _, id := range r.ids {
		r.notes[id] = "Season " + strconv.FormatInt(newest[id], 10) + " added"
	}
	return r, nil
}

// recentAnchors are the movies and shows the caller finished most recently,
// newest first, for Because You Watched.
func (q *homeQuery) recentAnchors() ([]int64, error) {
	return q.c.queryIDs(q.ctx, `WITH `+finishedCTE+` SELECT i.id FROM finished JOIN items i ON i.id = finished.top
		WHERE i.parent_id IS NULL AND `+q.cond+` ORDER BY finished.last DESC LIMIT 6`, append([]any{q.user}, q.args...)...)
}

// becauseYouWatched: titles of the same kind sharing the most with the anchor
// (a director counts three, a franchise four, each shared genre or lead actor
// one), none of them already finished. The anchor itself must be visible, so
// its title never leaks through the row's name.
func (q *homeQuery) becauseYouWatched(arg string) (*builtRow, error) {
	anchor, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return nil, ErrNotFound
	}
	var title, kind string
	err = q.c.DB.QueryRowContext(q.ctx, `SELECT i.title, i.kind FROM items i WHERE i.id = ? AND i.parent_id IS NULL AND `+q.cond,
		append([]any{anchor}, q.args...)...).Scan(&title, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	ids, err := q.ranked(`WITH `+finishedCTE+`,
		a AS (SELECT kind, value FROM item_tags WHERE item_id = ? AND (kind IN ('genre', 'director') OR (kind = 'actor' AND position < 8))),
		m AS (
			SELECT t.item_id AS id, CASE t.kind WHEN 'director' THEN 3 ELSE 1 END AS pts
			FROM a JOIN item_tags t ON t.kind = a.kind AND t.value = a.value
			WHERE t.item_id <> ? AND (t.kind <> 'actor' OR t.position < 8)
			UNION ALL
			SELECT c.id, 4 FROM items an JOIN items c ON c.tmdb_collection_id = an.tmdb_collection_id AND c.id <> an.id
			WHERE an.id = ? AND an.tmdb_collection_id IS NOT NULL),
		s AS (SELECT id, SUM(pts) AS score FROM m GROUP BY id)
		SELECT i.id FROM s JOIN items i ON i.id = s.id
		WHERE s.score >= 2 AND i.parent_id IS NULL AND i.kind = ? AND `+q.libCond+` AND i.id NOT IN (SELECT top FROM finished)
		ORDER BY s.score DESC, `+scoreOf+` IS NULL, `+scoreOf+` DESC, i.sort_title COLLATE NOCASE, i.id LIMIT ? OFFSET ?`,
		q.user, anchor, anchor, anchor, kind, sqlArgs(q.libArgs))
	if err != nil {
		return nil, err
	}
	return &builtRow{HomeRow: HomeRow{ID: "because_you_watched:" + arg, Kind: "because_you_watched",
		Title: "Because You Watched " + title, SeeAll: true}, ids: ids}, nil
}

// plays ranks movies and shows (an episode counts for its show) by plays
// that actually played, since a time (zero for all time) and for one user
// (zero for everyone). Recent plays weigh more inside a window: a play today
// counts 1, one at the window's start a quarter.
func (q *homeQuery) plays(since time.Time, user int64) ([]int64, error) {
	weight := "1.0"
	where := []string{"ps.item_id IS NOT NULL", "ps.decision <> 'refused'", "ps.played_ms >= ?"}
	var wargs []any
	args := []any{int64(activity.CountedPlayMs)}
	if !since.IsZero() {
		s := since.UnixMilli()
		weight = "0.25 + 0.75 * (ps.started_at - ?) / ?"
		wargs = []any{s, float64(q.now.UnixMilli() - s)}
		where = append(where, "ps.started_at >= ?")
		args = append(args, s)
	}
	if user > 0 {
		where = append(where, "ps.user_id = ?")
		args = append(args, user)
	}
	key := fmt.Sprintf("%d:%d", since.UnixMilli()/int64(playsCacheTTL/time.Millisecond), user)
	if since.IsZero() {
		key = fmt.Sprintf("all:%d", user)
	}
	if ids, ok := q.c.playsCache.get(key, q.now); ok {
		return q.visibleTop(ids)
	}
	cands, err := q.c.queryIDs(q.ctx, `SELECT COALESCE(sh.id, ps.item_id) AS top FROM playback_sessions ps
		LEFT JOIN items ep ON ep.id = ps.item_id AND ep.kind = 'episode'
		LEFT JOIN items se ON se.id = ep.parent_id LEFT JOIN items sh ON sh.id = se.parent_id
		WHERE `+strings.Join(where, " AND ")+`
		GROUP BY COALESCE(sh.id, ps.item_id) ORDER BY SUM(`+weight+`) DESC, MAX(ps.started_at) DESC LIMIT 500`,
		append(args, wargs...)...)
	if err != nil {
		return nil, err
	}
	q.c.playsCache.put(key, cands, q.now)
	return q.visibleTop(cands)
}

// playsCacheTTL is how long a play ranking is reused. Ranking reads every
// play ever recorded, and the answer is the same for everyone (or, for Most
// Watched by You, for that user) until more plays finish; a few minutes
// behind is fine for these rows. Each caller's policy is still applied fresh.
const playsCacheTTL = 5 * time.Minute

type idCache struct {
	mu sync.Mutex
	m  map[string]idCacheEntry
}

type idCacheEntry struct {
	at  time.Time
	ids []int64
}

func (c *idCache) get(key string, now time.Time) ([]int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || now.Sub(e.at) > playsCacheTTL || now.Before(e.at) {
		return nil, false
	}
	return e.ids, true
}

func (c *idCache) put(key string, ids []int64, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) > 1000 {
		c.m = map[string]idCacheEntry{}
	}
	c.m[key] = idCacheEntry{at: now, ids: ids}
}

// genresOfTheDay picks a few movie genres with enough visible titles, a
// different few each day.
func (q *homeQuery) genresOfTheDay() ([]string, error) {
	// Counted over the caller's libraries without the per-title policy, which
	// would cost a check of every film. A genre whose titles the caller mostly
	// cannot see makes a short row, and short rows are dropped.
	rows, err := q.c.DB.QueryContext(q.ctx, `SELECT t.value FROM item_tags t JOIN items i ON i.id = t.item_id
		WHERE t.kind = 'genre' AND i.kind = 'movie' AND i.parent_id IS NULL AND `+q.libCond+`
		GROUP BY t.value HAVING COUNT(*) >= ? ORDER BY t.value`, append(append([]any{}, q.libArgs...), minGenreTitles)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var all []string
	err = each(rows, func() error {
		var g string
		if err := rows.Scan(&g); err != nil {
			return err
		}
		all = append(all, g)
		return nil
	})
	if err != nil || len(all) == 0 {
		return nil, err
	}
	var out []string
	for k := range min(maxGenreRows, len(all)) {
		out = append(out, all[int((q.day*maxGenreRows+int64(k))%int64(len(all)))])
	}
	return out, nil
}

// decadeOfTheDay picks a decade with enough well-rated visible films, a
// different one each day; 0 when none has enough.
func (q *homeQuery) decadeOfTheDay() (int, error) {
	rows, err := q.c.DB.QueryContext(q.ctx, `WITH `+ratedCTE+` SELECT (i.year / 10) * 10 AS d FROM rated JOIN items i ON i.id = rated.id
		WHERE rated.score IS NOT NULL AND i.kind = 'movie' AND i.parent_id IS NULL AND i.year IS NOT NULL AND `+q.libCond+`
		GROUP BY d HAVING COUNT(*) >= ? ORDER BY d`, append(append([]any{}, q.libArgs...), minDecadeTitles)...)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	var all []int
	err = each(rows, func() error {
		var d int
		if err := rows.Scan(&d); err != nil {
			return err
		}
		all = append(all, d)
		return nil
	})
	if err != nil || len(all) == 0 {
		return 0, err
	}
	return all[int(q.day%int64(len(all)))], nil
}

func decadeName(d int) string {
	if d >= 1900 && d < 2000 {
		return strconv.Itoa(d%100) + "s"
	}
	return strconv.Itoa(d) + "s"
}

// homeCollections are collections marked to show on Home in libraries the
// caller may see; when there are more than fit, a different set each day.
//
// A YAML collection's visible: { home, shared } sets this, as in Kometa:
// home puts it on Home, and shared lets it reach the Home of users other than
// admins too. Shared is on by default, so a friend sees a collection row only
// when it is on Home and not withheld from them. Franchise collections leave
// home off.
func (q *homeQuery) homeCollections() ([]int64, error) {
	rows, err := q.c.DB.QueryContext(q.ctx, `SELECT id, library_id FROM collections WHERE `+q.collectionVisible()+`
		ORDER BY sort_title COLLATE NOCASE, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	err = each(rows, func() error {
		var id, lib int64
		if err := rows.Scan(&id, &lib); err != nil {
			return err
		}
		if q.pol.CanSeeLibrary(lib) {
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil || len(ids) <= maxCollectionRows {
		return ids, err
	}
	start := int(q.day % int64(len(ids)))
	out := make([]int64, 0, maxCollectionRows)
	for k := range maxCollectionRows {
		out = append(out, ids[(start+k)%len(ids)])
	}
	return out, nil
}

// collectionVisible is the condition for a collection on the caller's Home.
func (q *homeQuery) collectionVisible() string {
	if q.pol.IsAdmin {
		return "visible_home = 1"
	}
	return "visible_home = 1 AND visible_shared = 1"
}

func (q *homeQuery) collection(arg string) (*builtRow, error) {
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		return nil, ErrNotFound
	}
	var name string
	var lib int64
	err = q.c.DB.QueryRowContext(q.ctx, `SELECT name, library_id FROM collections WHERE id = ? AND `+q.collectionVisible(), id).Scan(&name, &lib)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !q.pol.CanSeeLibrary(lib)) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	ids, err := q.ids(`SELECT i.id FROM collection_items ci JOIN items i ON i.id = ci.item_id
		WHERE ci.collection_id = ? AND `+q.cond+` ORDER BY ci.position LIMIT ?`, id)
	if err != nil {
		return nil, err
	}
	// A collection whose members are all hidden from the caller is not there
	// for them, name included.
	if len(ids) == 0 {
		return nil, ErrNotFound
	}
	return &builtRow{HomeRow: HomeRow{ID: "collection:" + arg, Kind: "collection", Title: name, SeeAll: true}, ids: ids}, nil
}

// OnlineList is a TMDb list as the background refresher last saved it, in
// the settings table so the last good copy survives restarts and outages.
type OnlineList struct {
	FetchedAt int64            `json:"fetched_at"`
	Items     []OnlineListItem `json:"items"`
}

// OnlineListItem is one entry: "movie" or "show" and its TMDb id.
type OnlineListItem struct {
	Kind   string `json:"kind"`
	TMDbID int64  `json:"tmdb_id"`
}

// OnlineListKey is the settings key a list is saved under.
func OnlineListKey(kind string) string { return "home_list:" + kind }

// onlineList maps a saved TMDb list onto the library, in the list's order.
// Nil (not empty) when no list has ever been fetched, which hides the row.
func (q *homeQuery) onlineList(kind string) ([]int64, error) {
	var raw string
	err := q.c.DB.QueryRowContext(q.ctx, `SELECT value FROM settings WHERE key = ?`, OnlineListKey(kind)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// A damaged list counts as no list: the row hides rather than breaking Home.
	var l OnlineList
	if jerr := json.Unmarshal([]byte(raw), &l); jerr != nil || len(l.Items) == 0 {
		return nil, nil //nolint:nilerr // see above
	}
	pos := map[string]int{}
	var tmdbIDs []any
	for i, it := range l.Items {
		key := it.Kind + ":" + strconv.FormatInt(it.TMDbID, 10)
		if _, dup := pos[key]; !dup {
			pos[key] = i
			tmdbIDs = append(tmdbIDs, it.TMDbID)
		}
	}
	rows, err := q.c.DB.QueryContext(q.ctx, `SELECT i.id, i.kind, i.tmdb_id FROM items i WHERE i.parent_id IS NULL
		AND i.kind IN ('movie', 'show') AND i.tmdb_id IN (`+placeholders(len(tmdbIDs))+`) AND `+q.cond, append(tmdbIDs, q.args...)...)
	if err != nil {
		return nil, err
	}
	type hit struct {
		id  int64
		pos int
	}
	var hits []hit
	err = each(rows, func() error {
		var id, tid int64
		var kind string
		if err := rows.Scan(&id, &kind, &tid); err != nil {
			return err
		}
		if p, ok := pos[kind+":"+strconv.FormatInt(tid, 10)]; ok {
			hits = append(hits, hit{id, p})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].pos < hits[j].pos })
	ids := []int64{}
	for _, h := range hits {
		if len(ids) < q.limit {
			ids = append(ids, h.id)
		}
	}
	return ids, nil
}
