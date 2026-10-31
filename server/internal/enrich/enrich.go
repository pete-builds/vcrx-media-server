// Package enrich fills items from TMDb: plot, tagline, genres, cast, ratings,
// posters, backdrops and episode stills. It runs only with the operator's own
// API key, and it never overwrites what an NFO, a local image or an admin
// provided: a column or tag list is written only while it is empty or still
// holds the value enrichment itself wrote last time.
package enrich

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	_ "image/jpeg" // validate downloaded artwork
	_ "image/png"  // validate downloaded artwork
	"log/slog"
	"strconv"
	"strings"
	"time"

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
	// AfterPass, when set, runs after every pass (a Fix Match may have made a
	// pair since), e.g. to merge copies of a film that share a TMDb id. It
	// runs before Changed, so smart collections stamp the merged movie.
	AfterPass func(ctx context.Context)
	// Changed, when set, is called after a pass that matched anything and
	// after a Fix Match: genres and ratings just changed, so smart collections
	// and the labels they stamp rebuild at once instead of on their timer.
	Changed func()
	// Trailers also looks up each title's YouTube trailer (trailers_youtube).
	Trailers bool
	// Embeddable, when set, asks YouTube whether a trailer still plays in
	// another site's player (youtube.Client.Embeddable). The pass then skips
	// TMDb trailers that are gone or have embedding off, for the next best.
	Embeddable func(ctx context.Context, videoID string) (bool, error)
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
		// AfterPass merges copies before Changed rebuilds smart collections,
		// so the rebuild sees the merged movies.
		if e.AfterPass != nil && ctx.Err() == nil {
			e.AfterPass(ctx)
		}
		if e.Changed != nil && st.Matched+st.Episodes > 0 {
			e.Changed()
		}
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
		if e.Trailers && e.trailerPass(ctx) {
			return
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
