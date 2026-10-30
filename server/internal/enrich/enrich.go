// Package enrich fills items from TMDb: plot, tagline, genres, cast, ratings,
// posters, backdrops and episode stills. It runs only with the operator's own
// API key, and it never overwrites what an NFO, a local image or an admin
// provided: a column or tag list is written only while it is empty or still
// holds the value enrichment itself wrote last time.
package enrich

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // validate downloaded artwork
	_ "image/png"  // validate downloaded artwork
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/tmdb"
)

const (
	RefreshMatched  = 30 * 24 * time.Hour
	RetryUnmatched  = 7 * 24 * time.Hour
	RetryError      = time.Hour
	posterSize      = "w780"
	backdropSize    = "w1280"
	stillSize       = "w780"
	maxActors       = 15
	maxStudios      = 3
	maxWriters      = 5
	tagSep          = "\x1f"
	defaultBatch    = 100
	defaultInterval = 15 * time.Minute
)

type Enricher struct {
	DB   *sql.DB
	TMDb *tmdb.Client
	// Dir holds downloaded originals, one folder per item. It lives under the
	// config dir, not the cache, because re-downloading thousands of images is slow.
	Dir string
	// Country picks the content rating, e.g. US.
	Country string
	Log     *slog.Logger
	// Busy reports a library being scanned; its items wait for the next pass.
	Busy  func(libraryID int64) bool
	Batch int
	Now   func() time.Time
}

type Stats struct {
	Matched, Unmatched, Errors, Episodes int
}

func (s *Stats) add(o Stats) {
	s.Matched += o.Matched
	s.Unmatched += o.Unmatched
	s.Errors += o.Errors
	s.Episodes += o.Episodes
}

func (e *Enricher) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Enricher) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

// Run enriches after initial, then every interval, until ctx ends. A rejected
// key stops it: every later call would fail the same way.
func (e *Enricher) Run(ctx context.Context, initial, interval time.Duration) {
	if interval <= 0 {
		interval = defaultInterval
	}
	wait := initial
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = interval
		st, err := e.Drain(ctx)
		if st.Matched+st.Unmatched+st.Errors > 0 {
			e.log().Info("tmdb enrichment pass", "matched", st.Matched, "unmatched", st.Unmatched, "errors", st.Errors,
				"episodes", st.Episodes)
		}
		if errors.Is(err, tmdb.ErrUnauthorized) {
			e.log().Error("tmdb enrichment stopped: TMDb rejected the API key; check VCRX_TMDB_API_KEY")
			return
		}
		if err != nil && ctx.Err() == nil {
			e.log().Warn("tmdb enrichment pass did not complete", "err", err)
		}
	}
}

// Drain runs batches until nothing is due.
func (e *Enricher) Drain(ctx context.Context) (Stats, error) {
	var total Stats
	for {
		st, more, err := e.RunOnce(ctx)
		total.add(st)
		if err != nil || !more {
			return total, err
		}
	}
}

type candidate struct {
	id, library      int64
	kind, title      string
	year             int
	itemTMDb         int64
	imdb             string
	status           string
	stateTMDb        int64
	keySame          bool
	written          map[string]string
	how              string
	locked           map[string]bool
	cur              map[string]string
	source           string
	art              map[string]artRow
	tags             map[string]string
	seasonNumber     int
	episodeNumber    int
	hasSeason, hasEp bool
}

type artRow struct{ source, url, file string }

func matchKey(title string, year int) string { return title + "|" + strconv.Itoa(year) }

// RunOnce enriches one batch of due movies and shows, and reports whether more are due.
func (e *Enricher) RunOnce(ctx context.Context) (Stats, bool, error) {
	var st Stats
	batch := e.Batch
	if batch <= 0 {
		batch = defaultBatch
	}
	now := e.now()
	ms := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	var cands []*candidate
	err := each(ctx, e.DB, func(rows *sql.Rows) error {
		c := &candidate{}
		if err := rows.Scan(&c.id, &c.library, &c.kind, &c.title, &c.year, &c.itemTMDb, &c.imdb, &c.status, &c.stateTMDb, &c.keySame); err != nil {
			return err
		}
		cands = append(cands, c)
		return nil
	}, `SELECT i.id, i.library_id, i.kind, i.title, IFNULL(i.year, 0), IFNULL(i.tmdb_id, 0),
		IFNULL(i.imdb_id, ''), IFNULL(s.status, ''), IFNULL(s.tmdb_id, 0), IFNULL(s.match_key, '') = i.title || '|' || IFNULL(i.year, 0)
		FROM items i LEFT JOIN tmdb_state s ON s.item_id = i.id
		WHERE i.kind IN ('movie', 'show') AND i.missing_since IS NULL AND (
			s.item_id IS NULL
			OR s.match_key != i.title || '|' || IFNULL(i.year, 0)
			OR (s.status = 'matched' AND s.checked_at < ?)
			OR (s.status = 'unmatched' AND s.checked_at < ?)
			OR (s.status = 'error' AND s.checked_at < ?)
			OR (s.status = 'matched' AND i.kind = 'show' AND EXISTS (
				SELECT 1 FROM items se JOIN items ep ON ep.parent_id = se.id
				WHERE se.parent_id = i.id AND ep.kind = 'episode' AND ep.missing_since IS NULL
				AND NOT EXISTS (SELECT 1 FROM tmdb_state es WHERE es.item_id = ep.id))))
		ORDER BY s.item_id IS NOT NULL, i.added_at DESC LIMIT ?`,
		ms(RefreshMatched), ms(RetryUnmatched), ms(RetryError), batch)
	if err != nil {
		return st, false, err
	}
	done := 0
	for _, c := range cands {
		if ctx.Err() != nil {
			return st, false, ctx.Err()
		}
		if e.Busy != nil && e.Busy(c.library) {
			continue
		}
		done++
		one, err := e.item(ctx, c)
		st.add(one)
		if errors.Is(err, tmdb.ErrUnauthorized) || errors.Is(err, context.Canceled) {
			return st, false, err
		}
		if err != nil {
			st.Errors++
			e.log().Warn("tmdb enrichment failed", "item", c.id, "err", err)
			if serr := e.saveState(ctx, nil, c.id, "error", 0, matchKey(c.title, c.year), nil, err.Error()); serr != nil {
				return st, false, serr
			}
		}
	}
	return st, len(cands) == batch && done > 0, nil
}

// resolve finds the TMDb id: an id from an NFO or an admin is trusted, a match
// enrichment made itself is reused while the title and year are unchanged, and
// otherwise the title is searched.
func (e *Enricher) resolve(ctx context.Context, c *candidate) (int64, string, error) {
	if c.itemTMDb > 0 && c.written["tmdb_id"] != strconv.FormatInt(c.itemTMDb, 10) {
		return c.itemTMDb, "", nil
	}
	if c.status == "matched" && c.keySame && c.stateTMDb > 0 {
		return c.stateTMDb, "", nil
	}
	if c.imdb != "" && c.written["imdb_id"] != c.imdb {
		id, err := e.TMDb.FindIMDb(ctx, c.kind, c.imdb)
		if err == nil {
			return id, "", nil
		}
		if !errors.Is(err, tmdb.ErrNotFound) {
			return 0, "", err
		}
	}
	return e.match(ctx, c)
}

// maxTieBreak caps how many tied candidates get their details fetched.
const maxTieBreak = 6

// match searches by title and year. A tie is settled by evidence from the files
// first (a movie's runtime, a show's seasons and episode counts), then by a
// clear popularity lead; otherwise the item stays unmatched.
func (e *Enricher) match(ctx context.Context, c *candidate) (int64, string, error) {
	title, country := tmdb.Qualifier(c.title)
	list, reason, err := e.TMDb.Candidates(ctx, c.kind, title, c.year, country)
	if err != nil || len(list) == 0 {
		return 0, reason, err
	}
	if len(list) == 1 {
		return list[0].ID, "", nil
	}
	if len(list) > maxTieBreak {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Popularity > list[j].Popularity })
		list = list[:maxTieBreak]
	}
	fits, evidence, err := e.fitting(ctx, c, list)
	if err != nil {
		return 0, "", err
	}
	if evidence != "" {
		if len(fits) == 1 {
			c.how = "picked by " + evidence
			return fits[0].ID, "", nil
		}
		if len(fits) == 0 {
			return 0, "no result with this title fits the " + evidence, nil
		}
		list = fits
	}
	if top, ok := tmdb.Standout(list); ok {
		c.how = "picked by popularity"
		if evidence != "" {
			c.how = "picked by " + evidence + " and popularity"
		}
		return top.ID, "", nil
	}
	if c.year > 0 {
		return 0, "several results share this title and year", nil
	}
	return 0, "several results share this title and there is no year to choose by", nil
}

// fitting keeps the candidates consistent with the files on disk and names the
// evidence used, or returns no evidence when there is none to check.
func (e *Enricher) fitting(ctx context.Context, c *candidate, list []tmdb.Result) ([]tmdb.Result, string, error) {
	switch c.kind {
	case "movie":
		disk, _ := strconv.ParseInt(c.cur["runtime_ms"], 10, 64)
		if disk <= 0 || c.written["runtime_ms"] == c.cur["runtime_ms"] {
			return nil, "", nil
		}
		var fits []tmdb.Result
		for _, r := range list {
			m, err := e.TMDb.Movie(ctx, r.ID)
			if errors.Is(err, tmdb.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, "", err
			}
			if m.Runtime > 0 && absDiff(int64(m.Runtime)*60_000, disk) <= runtimeSlack.Milliseconds() {
				fits = append(fits, r)
			}
		}
		return fits, "runtime", nil
	case "show":
		episodes, err := e.diskSeasons(ctx, c.id)
		if err != nil || len(episodes) == 0 {
			return nil, "", err
		}
		var fits []tmdb.Result
		for _, r := range list {
			s, err := e.TMDb.Show(ctx, r.ID)
			if errors.Is(err, tmdb.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, "", err
			}
			counts := map[int]int{}
			for _, sr := range s.Seasons {
				counts[sr.SeasonNumber] = sr.EpisodeCount
			}
			ok := true
			for season, maxEp := range episodes {
				if n, found := counts[season]; !found || n < maxEp {
					ok = false
					break
				}
			}
			if ok {
				fits = append(fits, r)
			}
		}
		return fits, "seasons and episodes on disk", nil
	}
	return nil, "", nil
}

// runtimeSlack allows for credits, studio logos and cuts that differ a little.
const runtimeSlack = 10 * time.Minute

func absDiff(a, b int64) int64 {
	if a > b {
		return a - b
	}
	return b - a
}

// diskSeasons maps each regular season on disk (specials excluded) to its
// highest episode number.
func (e *Enricher) diskSeasons(ctx context.Context, show int64) (map[int]int, error) {
	out := map[int]int{}
	err := each(ctx, e.DB, func(rows *sql.Rows) error {
		var season, ep int
		if err := rows.Scan(&season, &ep); err != nil {
			return err
		}
		out[season] = ep
		return nil
	}, `SELECT se.season_number, MAX(IFNULL(ep.episode_number_end, ep.episode_number)) FROM items se
		JOIN items ep ON ep.parent_id = se.id AND ep.kind = 'episode' AND ep.missing_since IS NULL
		WHERE se.parent_id = ? AND se.kind = 'season' AND se.season_number > 0 AND ep.episode_number IS NOT NULL
		GROUP BY se.season_number`, show)
	return out, err
}

func (e *Enricher) item(ctx context.Context, c *candidate) (Stats, error) {
	var st Stats
	if err := e.load(ctx, c); err != nil {
		return st, err
	}
	key := matchKey(c.title, c.year)
	id, reason, err := e.resolve(ctx, c)
	if err != nil {
		return st, err
	}
	if id == 0 {
		st.Unmatched++
		e.log().Info("tmdb: no confident match", "item", c.id, "kind", c.kind, "title", c.title, "year", c.year, "reason", reason)
		return st, e.saveState(ctx, nil, c.id, "unmatched", 0, key, c.written, reason)
	}
	switch c.kind {
	case "movie":
		m, err := e.TMDb.Movie(ctx, id)
		if errors.Is(err, tmdb.ErrNotFound) {
			st.Unmatched++
			return st, e.saveState(ctx, nil, c.id, "unmatched", 0, key, c.written, "TMDb id "+strconv.FormatInt(id, 10)+" not found")
		}
		if err != nil {
			return st, err
		}
		if err := e.applyMovie(ctx, c, m, key); err != nil {
			return st, err
		}
	case "show":
		s, err := e.TMDb.Show(ctx, id)
		if errors.Is(err, tmdb.ErrNotFound) {
			st.Unmatched++
			return st, e.saveState(ctx, nil, c.id, "unmatched", 0, key, c.written, "TMDb id "+strconv.FormatInt(id, 10)+" not found")
		}
		if err != nil {
			return st, err
		}
		n, err := e.applyShow(ctx, c, s, key)
		st.Episodes += n
		if err != nil {
			return st, err
		}
	}
	st.Matched++
	return st, nil
}

// load reads an item's current values, locks, tags, artwork and enrichment state.
func (e *Enricher) load(ctx context.Context, c *candidate) error {
	var locked, written, overview, tagline, rating, premiere, imdb, coll string
	var runtime, tmdbID sql.NullInt64
	var season, episode sql.NullInt64
	err := e.DB.QueryRowContext(ctx, `SELECT i.kind, i.title, IFNULL(i.year, 0), i.metadata_source, i.locked_fields, IFNULL(s.written, '{}'),
		IFNULL(i.overview, ''), IFNULL(i.tagline, ''), IFNULL(i.content_rating, ''), IFNULL(i.premiere_date, ''), IFNULL(i.imdb_id, ''),
		IFNULL(CAST(i.tmdb_collection_id AS TEXT), ''), i.runtime_ms, i.tmdb_id, i.season_number, i.episode_number
		FROM items i LEFT JOIN tmdb_state s ON s.item_id = i.id WHERE i.id = ?`, c.id).
		Scan(&c.kind, &c.title, &c.year, &c.source, &locked, &written, &overview, &tagline, &rating, &premiere, &imdb, &coll,
			&runtime, &tmdbID, &season, &episode)
	if err != nil {
		return err
	}
	c.locked = ParseLocked(locked)
	c.written = map[string]string{}
	_ = json.Unmarshal([]byte(written), &c.written)
	c.cur = map[string]string{"title": c.title, "overview": overview, "tagline": tagline, "content_rating": rating,
		"premiere_date": premiere, "imdb_id": imdb, "tmdb_collection_id": coll, "runtime_ms": "", "tmdb_id": ""}
	if runtime.Valid {
		c.cur["runtime_ms"] = strconv.FormatInt(runtime.Int64, 10)
	}
	if tmdbID.Valid {
		c.cur["tmdb_id"] = strconv.FormatInt(tmdbID.Int64, 10)
	}
	c.seasonNumber, c.hasSeason = int(season.Int64), season.Valid
	c.episodeNumber, c.hasEp = int(episode.Int64), episode.Valid

	c.tags = map[string]string{}
	err = each(ctx, e.DB, func(rows *sql.Rows) error {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		if c.tags[k] != "" {
			c.tags[k] += tagSep
		}
		c.tags[k] += v
		return nil
	}, `SELECT kind, value FROM item_tags WHERE item_id = ? ORDER BY kind, position, value`, c.id)
	if err != nil {
		return err
	}
	c.art = map[string]artRow{}
	return each(ctx, e.DB, func(rows *sql.Rows) error {
		var k string
		var a artRow
		if err := rows.Scan(&k, &a.source, &a.url, &a.file); err != nil {
			return err
		}
		c.art[k] = a
		return nil
	}, `SELECT kind, source, IFNULL(url, ''), IFNULL(cache_path, '') FROM artwork WHERE item_id = ?`, c.id)
}

// ParseLocked reads items.locked_fields: column names plus tag kinds, rating,
// artwork kinds or "artwork" for all of them; singular and plural both count.
func ParseLocked(s string) map[string]bool {
	var names []string
	_ = json.Unmarshal([]byte(s), &names)
	m := map[string]bool{}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		m[n] = true
		m[strings.TrimSuffix(n, "s")] = true
	}
	return m
}

// plan collects one item's writes. Network work (images) happens before the
// transaction; the transaction only writes rows.
type plan struct {
	c      *candidate
	set    map[string]any
	next   map[string]string
	tags   map[string][]tag
	rating *[2]float64
	art    []artPlan
	source string
	status string
	tmdbID int64
	key    string
	reason string
}

type tag struct{ value, role string }

type artPlan struct{ kind, url, file string }

func newPlan(c *candidate, tmdbID int64, key string) *plan {
	return &plan{c: c, set: map[string]any{}, next: map[string]string{}, tags: map[string][]tag{}, status: "matched", tmdbID: tmdbID, key: key}
}

// col writes a column when TMDb has a value and the column is empty or still
// holds what enrichment wrote last time.
func (p *plan) col(name, value string, v any) {
	if value == "" || p.c.locked[name] {
		return
	}
	cur := p.c.cur[name]
	if cur != "" && cur != p.c.written[name] {
		return
	}
	p.next[name] = value
	if cur != value {
		p.set[name] = v
	}
}

// title replaces an episode title that still comes from the file name.
func (p *plan) title(value string) {
	if value == "" || p.c.locked["title"] {
		return
	}
	cur := p.c.cur["title"]
	if p.c.source != "filename" && cur != p.c.written["title"] {
		return
	}
	p.next["title"] = value
	if cur != value {
		p.set["title"] = value
	}
	if p.c.source == "filename" {
		p.source = "tmdb"
	}
}

func (p *plan) tagList(kind string, values []tag) {
	if len(values) == 0 || p.c.locked[kind] {
		return
	}
	seen := map[string]bool{}
	var clean []tag
	var names []string
	for _, t := range values {
		t.value = strings.TrimSpace(t.value)
		k := strings.ToLower(t.value)
		if t.value == "" || seen[k] {
			continue
		}
		seen[k] = true
		clean = append(clean, t)
		names = append(names, t.value)
	}
	if len(clean) == 0 {
		return
	}
	joined := strings.Join(names, tagSep)
	cur, key := p.c.tags[kind], "tag:"+kind
	if cur != "" && !strings.EqualFold(cur, p.c.written[key]) && !strings.EqualFold(cur, joined) {
		return
	}
	p.next[key] = joined
	if cur != joined {
		p.tags[kind] = clean
	}
}

func (p *plan) setRating(avg float64, votes int) {
	if votes > 0 && avg > 0 && !p.c.locked["rating"] {
		p.rating = &[2]float64{avg, float64(votes)}
	}
}

func names(ns []tmdb.Named, limit int) []tag {
	var out []tag
	for _, n := range ns {
		if limit > 0 && len(out) == limit {
			break
		}
		out = append(out, tag{value: n.Name})
	}
	return out
}

func actors(cast []tmdb.Cast) []tag {
	var out []tag
	for _, a := range cast {
		if len(out) == maxActors {
			break
		}
		out = append(out, tag{value: a.Name, role: a.Character})
	}
	return out
}

func crew(cs []tmdb.Crew, keep func(tmdb.Crew) bool, limit int) []tag {
	var out []tag
	for _, c := range cs {
		if len(out) == limit {
			break
		}
		if keep(c) {
			out = append(out, tag{value: c.Name})
		}
	}
	return out
}

func idStr(id int64) string {
	if id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

func (e *Enricher) country() string {
	if e.Country != "" {
		return e.Country
	}
	return "US"
}

func (e *Enricher) applyMovie(ctx context.Context, c *candidate, m *tmdb.Movie, key string) error {
	p := newPlan(c, m.ID, key)
	p.reason = c.how
	p.col("tmdb_id", idStr(m.ID), m.ID)
	p.col("imdb_id", m.IMDbID, m.IMDbID)
	p.col("overview", m.Overview, m.Overview)
	p.col("tagline", m.Tagline, m.Tagline)
	cert := m.Certification(e.country())
	p.col("content_rating", cert, cert)
	p.col("premiere_date", m.ReleaseDate, m.ReleaseDate)
	if m.Runtime > 0 {
		ms := int64(m.Runtime) * 60_000
		p.col("runtime_ms", strconv.FormatInt(ms, 10), ms)
	}
	if m.Collection != nil && m.Collection.ID > 0 {
		p.col("tmdb_collection_id", idStr(m.Collection.ID), m.Collection.ID)
	}
	p.tagList("genre", names(m.Genres, 0))
	p.tagList("studio", names(m.Companies, maxStudios))
	p.tagList("country", names(m.Countries, 0))
	p.tagList("director", crew(m.Credits.Crew, func(x tmdb.Crew) bool { return x.Job == "Director" }, maxWriters))
	p.tagList("writer", crew(m.Credits.Crew, func(x tmdb.Crew) bool { return x.Department == "Writing" }, maxWriters))
	p.tagList("actor", actors(m.Credits.Cast))
	p.setRating(m.VoteAverage, m.VoteCount)
	if err := e.planArt(ctx, p, "poster", posterSize, m.PosterPath); err != nil {
		return err
	}
	if err := e.planArt(ctx, p, "backdrop", backdropSize, m.BackdropPath); err != nil {
		return err
	}
	return e.commit(ctx, []*plan{p})
}

func (e *Enricher) applyShow(ctx context.Context, c *candidate, s *tmdb.Show, key string) (int, error) {
	p := newPlan(c, s.ID, key)
	p.reason = c.how
	p.col("tmdb_id", idStr(s.ID), s.ID)
	p.col("imdb_id", s.ExternalIDs.IMDbID, s.ExternalIDs.IMDbID)
	p.col("overview", s.Overview, s.Overview)
	p.col("tagline", s.Tagline, s.Tagline)
	cert := s.Certification(e.country())
	p.col("content_rating", cert, cert)
	p.col("premiere_date", s.FirstAirDate, s.FirstAirDate)
	p.tagList("genre", names(s.Genres, 0))
	p.tagList("network", names(s.Networks, maxStudios))
	p.tagList("actor", actors(s.Credits.Cast))
	p.setRating(s.VoteAverage, s.VoteCount)
	if err := e.planArt(ctx, p, "poster", posterSize, s.PosterPath); err != nil {
		return 0, err
	}
	if err := e.planArt(ctx, p, "backdrop", backdropSize, s.BackdropPath); err != nil {
		return 0, err
	}
	plans := []*plan{p}

	seasons, err := e.children(ctx, c.id, "season")
	if err != nil {
		return 0, err
	}
	onTMDb := map[int]bool{}
	for _, r := range s.Seasons {
		onTMDb[r.SeasonNumber] = true
	}
	episodes := 0
	for _, sid := range seasons {
		sc := &candidate{id: sid}
		if err := e.load(ctx, sc); err != nil {
			return 0, err
		}
		eps, err := e.children(ctx, sid, "episode")
		if err != nil {
			return 0, err
		}
		var season *tmdb.Season
		if sc.hasSeason && onTMDb[sc.seasonNumber] {
			season, err = e.TMDb.Season(ctx, s.ID, sc.seasonNumber)
			if err != nil && !errors.Is(err, tmdb.ErrNotFound) {
				return 0, err
			}
		}
		if season != nil {
			sp := newPlan(sc, 0, "")
			sp.col("overview", season.Overview, season.Overview)
			if err := e.planArt(ctx, sp, "poster", posterSize, season.PosterPath); err != nil {
				return 0, err
			}
			plans = append(plans, sp)
		}
		byNum := map[int]*tmdb.Episode{}
		if season != nil {
			for i := range season.Episodes {
				byNum[season.Episodes[i].EpisodeNumber] = &season.Episodes[i]
			}
		}
		for _, eid := range eps {
			ec := &candidate{id: eid}
			if err := e.load(ctx, ec); err != nil {
				return 0, err
			}
			ep := byNum[ec.episodeNumber]
			if !ec.hasEp || ep == nil {
				up := newPlan(ec, 0, "")
				up.status, up.reason = "unmatched", "episode not on TMDb"
				up.next = ec.written
				plans = append(plans, up)
				continue
			}
			episodes++
			epl := newPlan(ec, 0, "")
			epl.title(ep.Name)
			epl.col("overview", ep.Overview, ep.Overview)
			epl.col("premiere_date", ep.AirDate, ep.AirDate)
			epl.setRating(ep.VoteAverage, ep.VoteCount)
			if err := e.planArt(ctx, epl, "thumb", stillSize, ep.StillPath); err != nil {
				return 0, err
			}
			plans = append(plans, epl)
		}
	}
	return episodes, e.commit(ctx, plans)
}

func (e *Enricher) children(ctx context.Context, parent int64, kind string) ([]int64, error) {
	rows, err := e.DB.QueryContext(ctx, `SELECT id FROM items WHERE parent_id = ? AND kind = ? AND missing_since IS NULL ORDER BY id`, parent, kind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// planArt downloads an image unless local art, a lock or an up-to-date copy
// makes it unnecessary.
func (e *Enricher) planArt(ctx context.Context, p *plan, kind, size, path string) error {
	if path == "" || !tmdb.ValidImagePath(path) || p.c.locked[kind] || p.c.locked["artwork"] {
		return nil
	}
	if a, ok := p.c.art[kind]; ok {
		if a.source == "local" {
			return nil
		}
		if a.source == "tmdb" && a.url == path && a.file != "" && fsafe.Within(e.Dir, a.file) {
			if _, err := os.Stat(a.file); err == nil {
				return nil
			}
		}
	}
	b, err := e.TMDb.Image(ctx, size, path)
	if errors.Is(err, tmdb.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("download %s: %w", kind, err)
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || (format != "jpeg" && format != "png") {
		return fmt.Errorf("TMDb %s for item %d is not a usable image", kind, p.c.id)
	}
	dir := filepath.Join(e.Dir, strconv.FormatInt(p.c.id, 10))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	ext := ".jpg"
	if format == "png" {
		ext = ".png"
	}
	base := strings.TrimSuffix(strings.TrimPrefix(path, "/"), filepath.Ext(path))
	out := filepath.Join(dir, kind+"-"+base+ext)
	tmp, err := os.CreateTemp(dir, ".dl-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if a, ok := p.c.art[kind]; ok && a.source == "tmdb" && a.file != "" && a.file != out && fsafe.Within(e.Dir, a.file) {
		_ = os.Remove(a.file)
	}
	p.art = append(p.art, artPlan{kind: kind, url: path, file: out})
	return nil
}

func (e *Enricher) commit(ctx context.Context, plans []*plan) error {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := e.now().UnixMilli()
	for _, p := range plans {
		if err := writePlan(ctx, tx, p, now); err != nil {
			return err
		}
		if err := e.saveState(ctx, tx, p.c.id, p.status, p.tmdbID, p.key, p.next, p.reason); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func writePlan(ctx context.Context, tx *sql.Tx, p *plan, now int64) error {
	if p.source != "" {
		p.set["metadata_source"] = p.source
	}
	if len(p.set) > 0 {
		cols := make([]string, 0, len(p.set))
		for c := range p.set {
			cols = append(cols, c)
		}
		// Fixed order keeps the statement stable; every name comes from this package.
		sort.Strings(cols)
		q := "UPDATE items SET updated_at = ?"
		args := []any{now}
		for _, c := range cols {
			q += ", " + c + " = ?"
			args = append(args, p.set[c])
		}
		if _, err := tx.ExecContext(ctx, q+" WHERE id = ?", append(args, p.c.id)...); err != nil {
			return err
		}
	}
	for kind, values := range p.tags {
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_tags WHERE item_id = ? AND kind = ?`, p.c.id, kind); err != nil {
			return err
		}
		for i, t := range values {
			var role any
			if t.role != "" {
				role = t.role
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO item_tags (item_id, kind, value, position, role) VALUES (?, ?, ?, ?, ?)`,
				p.c.id, kind, t.value, i, role); err != nil {
				return err
			}
		}
	}
	if p.rating != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_ratings (item_id, source, value, votes) VALUES (?, 'tmdb', ?, ?)
			ON CONFLICT (item_id, source) DO UPDATE SET value = excluded.value, votes = excluded.votes`,
			p.c.id, p.rating[0], int64(p.rating[1])); err != nil {
			return err
		}
	}
	for _, a := range p.art {
		// The WHERE keeps a local image a scan recorded in the meantime.
		if _, err := tx.ExecContext(ctx, `INSERT INTO artwork (item_id, kind, source, url, cache_path, updated_at) VALUES (?, ?, 'tmdb', ?, ?, ?)
			ON CONFLICT (item_id, kind) DO UPDATE SET source = 'tmdb', source_path = NULL, url = excluded.url,
			cache_path = excluded.cache_path, width = NULL, height = NULL, updated_at = excluded.updated_at
			WHERE artwork.source = 'tmdb'`, p.c.id, a.kind, a.url, a.file, now); err != nil {
			return err
		}
	}
	return nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (e *Enricher) saveState(ctx context.Context, tx *sql.Tx, id int64, status string, tmdbID int64, key string, written map[string]string, reason string) error {
	var x execer = e.DB
	if tx != nil {
		x = tx
	}
	if written == nil {
		written = map[string]string{}
	}
	w, err := json.Marshal(written)
	if err != nil {
		return err
	}
	var tid, why any
	if tmdbID > 0 {
		tid = tmdbID
	}
	if reason != "" {
		why = reason
	}
	_, err = x.ExecContext(ctx, `INSERT INTO tmdb_state (item_id, status, tmdb_id, match_key, written, reason, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (item_id) DO UPDATE SET status = excluded.status, tmdb_id = excluded.tmdb_id, match_key = excluded.match_key,
		written = excluded.written, reason = excluded.reason, checked_at = excluded.checked_at`,
		id, status, tid, key, string(w), why, e.now().UnixMilli())
	return err
}

// Status summarises enrichment for the CLI.
type Status struct {
	Counts    map[string]int
	Unmatched []Unmatched
}

type Unmatched struct {
	ID     int64
	Kind   string
	Title  string
	Year   int
	Reason string
}

func (e *Enricher) Status(ctx context.Context) (Status, error) {
	st := Status{Counts: map[string]int{}}
	err := each(ctx, e.DB, func(rows *sql.Rows) error {
		var kind, status string
		var n int
		if err := rows.Scan(&kind, &status, &n); err != nil {
			return err
		}
		st.Counts[kind+" "+status] = n
		return nil
	}, `SELECT i.kind, IFNULL(s.status, 'pending'), COUNT(*) FROM items i
		LEFT JOIN tmdb_state s ON s.item_id = i.id WHERE i.kind IN ('movie', 'show', 'episode') AND i.missing_since IS NULL
		GROUP BY 1, 2`)
	if err != nil {
		return st, err
	}
	urows, err := e.DB.QueryContext(ctx, `SELECT i.id, i.kind, i.title, IFNULL(i.year, 0), IFNULL(s.reason, '') FROM items i
		JOIN tmdb_state s ON s.item_id = i.id WHERE i.kind IN ('movie', 'show') AND s.status != 'matched' AND i.missing_since IS NULL
		ORDER BY i.kind, i.sort_title`)
	if err != nil {
		return st, err
	}
	defer func() { _ = urows.Close() }()
	for urows.Next() {
		var u Unmatched
		if err := urows.Scan(&u.ID, &u.Kind, &u.Title, &u.Year, &u.Reason); err != nil {
			return st, err
		}
		st.Unmatched = append(st.Unmatched, u)
	}
	return st, urows.Err()
}

// ResetChecks makes every item due on the next pass.
func (e *Enricher) ResetChecks(ctx context.Context) error {
	_, err := e.DB.ExecContext(ctx, `UPDATE tmdb_state SET checked_at = 0`)
	return err
}

// each runs q and calls fn for every row.
func each(ctx context.Context, conn *sql.DB, fn func(*sql.Rows) error, q string, args ...any) error {
	rows, err := conn.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
