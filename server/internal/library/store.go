package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pete-builds/vcrx-media-server/server/internal/metadata"
	"github.com/pete-builds/vcrx-media-server/server/internal/probe"
)

type verRow struct {
	id, itemID   int64
	path, rel    string
	size, mtime  int64
	label        string
	probeStatus  string
	probedAt     int64
	missingSince int64
	external     []string
}

type artRow struct{ source, path string }

type itemRow struct {
	id, parent   int64
	kind, key    string
	title        string
	year         int
	source       string
	locked       map[string]bool
	extra        map[string]any
	missingSince int64
	runtimeSet   bool
	art          map[string]artRow
}

// state is the library as the database holds it when the scan starts, kept in
// step with every write so unchanged files cost no queries.
type state struct {
	versions map[string]*verRow
	items    map[int64]*itemRow
	byKey    map[string]*itemRow
	// moved holds items that lost a version to another item this run, so
	// reconcile can prune them if they are now empty.
	moved map[int64]bool
	// alias maps keys merged into a movie (regroup.go) to that movie.
	alias map[string]*itemRow
}

func identity(kind string, parent int64, key string) string {
	return kind + "|" + strconv.FormatInt(parent, 10) + "|" + key
}

func loadState(ctx context.Context, db *sql.DB, lib Library) (*state, error) {
	st := &state{versions: map[string]*verRow{}, items: map[int64]*itemRow{}, byKey: map[string]*itemRow{}, moved: map[int64]bool{},
		alias: map[string]*itemRow{}}
	byID := map[int64]*verRow{}
	err := query(ctx, db, `SELECT id, item_id, path, rel_path, size, mtime, IFNULL(label, ''), probe_status,
		IFNULL(probed_at, 0), IFNULL(missing_since, 0) FROM media_versions WHERE library_id = ?`, lib.ID, func(rows *sql.Rows) error {
		v := &verRow{}
		if err := rows.Scan(&v.id, &v.itemID, &v.path, &v.rel, &v.size, &v.mtime, &v.label, &v.probeStatus, &v.probedAt, &v.missingSince); err != nil {
			return err
		}
		st.versions[v.path] = v
		byID[v.id] = v
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = query(ctx, db, `SELECT s.version_id, s.external_path FROM media_streams s JOIN media_versions v ON v.id = s.version_id
		WHERE v.library_id = ? AND s.is_external = 1 AND s.external_path IS NOT NULL AND s.downloaded_id IS NULL`, lib.ID, func(rows *sql.Rows) error {
		var id int64
		var p string
		if err := rows.Scan(&id, &p); err != nil {
			return err
		}
		if v := byID[id]; v != nil {
			v.external = append(v.external, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, v := range byID {
		sort.Strings(v.external)
	}
	err = query(ctx, db, `SELECT id, IFNULL(parent_id, 0), kind, group_key, title, IFNULL(year, 0), metadata_source,
		locked_fields, extra, IFNULL(missing_since, 0), runtime_ms IS NOT NULL FROM items WHERE library_id = ?`, lib.ID, func(rows *sql.Rows) error {
		it := &itemRow{art: map[string]artRow{}}
		var locked, extra string
		if err := rows.Scan(&it.id, &it.parent, &it.kind, &it.key, &it.title, &it.year, &it.source, &locked, &extra,
			&it.missingSince, &it.runtimeSet); err != nil {
			return err
		}
		it.locked = parseLocked(locked)
		if json.Unmarshal([]byte(extra), &it.extra) != nil || it.extra == nil {
			it.extra = map[string]any{}
		}
		st.items[it.id] = it
		st.byKey[identity(it.kind, it.parent, it.key)] = it
		if it.kind == "movie" {
			for _, k := range mergedKeys(it.extra) {
				st.alias[k] = it
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = query(ctx, db, `SELECT a.item_id, a.kind, a.source, IFNULL(a.source_path, '') FROM artwork a
		JOIN items i ON i.id = a.item_id WHERE i.library_id = ?`, lib.ID, func(rows *sql.Rows) error {
		var id int64
		var kind string
		var a artRow
		if err := rows.Scan(&id, &kind, &a.source, &a.path); err != nil {
			return err
		}
		if it := st.items[id]; it != nil {
			it.art[kind] = a
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return st, nil
}

// query runs q and calls each for every row.
func query(ctx context.Context, db *sql.DB, q string, arg any, each func(*sql.Rows) error) error {
	rows, err := db.QueryContext(ctx, q, arg)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := each(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// parseLocked reads items.locked_fields. Names are column names (title,
// overview, year, ...) plus genres, studios, directors, writers, actors,
// countries, networks, ratings, labels and artwork kinds (poster, backdrop,
// thumb, logo, or artwork for all of them); singular and plural both count.
func parseLocked(s string) map[string]bool {
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

// needsProbe decides whether a file must be (re)probed.
func needsProbe(v *verRow, f *videoFile, opt Options, nowMs int64) bool {
	switch {
	case v == nil, v.size != f.size, v.mtime != f.mtime, v.probeStatus == "pending":
		return true
	case v.probeStatus == "error":
		return opt.Force || opt.Trigger == "manual" || opt.Trigger == "cli" || nowMs-v.probedAt > ProbeRetryAfter.Milliseconds()
	}
	return false
}

type probeOutcome struct {
	p   *plan
	res *probe.Result
	err error
}

// process probes what needs probing, with bounded concurrency, and writes each
// file in its own transaction from this goroutine alone. No transaction is
// open while ffprobe runs.
func (s *Scanner) process(ctx context.Context, lib Library, opt Options, st *state, w *walker, plans []*plan, run *Run, log *slog.Logger) error {
	nowMs := s.now().UnixMilli()
	var toProbe []*plan
	for _, p := range plans {
		if needsProbe(st.versions[p.v.abs], p.v, opt, nowMs) {
			toProbe = append(toProbe, p)
		}
	}
	n := s.ProbeConcurrency
	if n <= 0 {
		n = DefaultProbeConcurrency
	}
	pctx, cancel := context.WithCancel(ctx)
	jobs := make(chan *plan)
	results := make(chan probeOutcome, len(toProbe))
	var wg sync.WaitGroup
	for range min(n, max(len(toProbe), 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				res, err := s.Prober.Probe(pctx, p.v.abs)
				results <- probeOutcome{p, res, err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, p := range toProbe {
			select {
			case jobs <- p:
			case <-pctx.Done():
				return
			}
		}
	}()
	// On an early return, stop the probes first so draining is quick.
	defer func() {
		cancel()
		wg.Wait()
	}()

	wr := &writer{s: s, lib: lib, st: st, w: w, log: log, run: run, showsDone: map[int64]bool{}}
	probing := map[*plan]bool{}
	for _, p := range toProbe {
		probing[p] = true
	}
	for _, p := range plans {
		if probing[p] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := wr.apply(ctx, p, nil, nil, false); err != nil {
			return err
		}
	}
	for range toProbe {
		var o probeOutcome
		select {
		case o = <-results:
		case <-ctx.Done():
			return ctx.Err()
		}
		if o.err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if err := wr.apply(ctx, o.p, o.res, o.err, true); err != nil {
			return err
		}
	}
	return nil
}

type writer struct {
	s   *Scanner
	lib Library
	st  *state
	w   *walker
	log *slog.Logger
	run *Run
	// shows whose tvshow.nfo and artwork this run has already looked at.
	showsDone map[int64]bool
}

// sidecars is everything a file's neighbours say, gathered before the write
// transaction opens so no file is read while it is held. The NFOs are set only
// when they need applying.
type sidecars struct {
	subs               []Sidecar
	nfo, showNFO       *nfoFile
	art                map[string]string // kind to absolute path, for the file's own item
	seasonArt, showArt map[string]string
}

type nfoFile struct {
	path, stamp string
	doc         *metadata.NFO
}

func (wr *writer) abs(rel string) string { return wr.w.absOf(rel) }

// nfo reads an NFO when the item should take it: the item has none yet, this
// is the one it came from and it changed, or the one it came from is gone. An
// item with versions in two folders keeps one NFO instead of alternating.
func (wr *writer) nfo(rel string, it *itemRow) *nfoFile {
	if rel == "" {
		return nil
	}
	p := wr.abs(rel)
	fi, err := os.Stat(p)
	if err != nil {
		return nil
	}
	stamp := fmt.Sprintf("%d|%d", fi.Size(), fi.ModTime().UnixMilli())
	if it != nil {
		recPath, _ := it.extra["nfo_path"].(string)
		recStamp, _ := it.extra["nfo_stamp"].(string)
		switch {
		case recPath == p && recStamp == stamp:
			return nil
		case recPath != "" && recPath != p && fileExists(recPath):
			return nil
		}
	}
	doc, err := metadata.Read(p)
	if err != nil {
		wr.log.Warn("nfo unreadable", "path", rel, "err", err)
		return nil
	}
	return &nfoFile{path: p, stamp: stamp, doc: doc}
}

func (wr *writer) gather(p *plan, item, show *itemRow) sidecars {
	var sc sidecars
	l := wr.w.listing(p.dir)
	for _, sub := range findSubtitles(p.v.rel, p.siblings, wr.w.listing) {
		sub.Path = wr.abs(sub.Path)
		sc.subs = append(sc.subs, sub)
	}
	absAll := func(m map[string]string) map[string]string {
		for k, v := range m {
			m[k] = wr.abs(v)
		}
		return m
	}
	if !p.episode {
		sc.nfo = wr.nfo(movieNFO(l, p.base, p.oneTitle), item)
		sc.art = absAll(movieArtwork(l, p.base, p.oneTitle))
		return sc
	}
	if n := l.lookup(p.base + ".nfo"); n != "" {
		sc.nfo = wr.nfo(joinRel(p.dir, n), item)
	}
	sc.art = absAll(episodeArtwork(l, p.base))
	var showL, seasonL *listing
	if p.ep.ShowDir != "" {
		showL = wr.w.listing(p.ep.ShowDir)
	}
	if p.ep.SeasonDir != "" {
		seasonL = wr.w.listing(p.ep.SeasonDir)
	}
	sc.seasonArt = absAll(seasonArtwork(showL, seasonL, p.ep.Season))
	// Show-level files are looked at once per show per run.
	if show == nil || !wr.showsDone[show.id] {
		sc.showArt = absAll(showArtwork(showL))
		if n := showL.lookup("tvshow.nfo"); n != "" {
			sc.showNFO = wr.nfo(joinRel(showL.rel, n), show)
		}
		if show != nil {
			wr.showsDone[show.id] = true
		}
	}
	return sc
}

// apply writes one file. probed says whether res and perr are fresh; when not,
// only rows whose inputs changed are touched, and an unchanged file causes no
// write at all.
func (wr *writer) apply(ctx context.Context, p *plan, res *probe.Result, perr error, probed bool) error {
	st := wr.st
	v := st.versions[p.v.abs]
	nowMs := wr.s.now().UnixMilli()

	// Look up the existing item chain so sidecar reads can skip unchanged NFOs.
	var item, season, show *itemRow
	if p.episode {
		show = st.byKey[identity("show", 0, p.showKey)]
		if show != nil {
			season = st.byKey[identity("season", show.id, strconv.Itoa(p.ep.Season))]
		}
		if season != nil {
			item = st.byKey[identity("episode", season.id, p.epKey)]
		}
	} else if item = st.routed(p); item == nil {
		item = st.byKey[identity("movie", 0, p.movieKey)]
	}
	sc := wr.gather(p, item, show)
	artGone := wr.staleArt(item, season, show, sc)

	if !probed && !wr.dirty(p, v, item, season, show, sc, artGone) {
		return nil
	}
	if v == nil {
		// Libraries with overlapping roots would otherwise fight over the file.
		var other string
		err := wr.s.DB.QueryRowContext(ctx, `SELECT l.name FROM media_versions v JOIN libraries l ON l.id = v.library_id
			WHERE v.path = ? AND v.library_id <> ?`, p.v.abs, wr.lib.ID).Scan(&other)
		if err == nil {
			wr.run.Failed++
			wr.log.Warn("file already belongs to another library; check for overlapping roots", "path", p.v.rel, "library", other)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}

	tx, err := wr.s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	t := &txw{tx: tx, ctx: ctx, lib: wr.lib, st: st, now: nowMs}

	var itemID int64
	var touched []*itemRow
	if p.episode {
		sh, err := t.ensure("show", 0, p.showKey, p.showRep.itemFields("show"))
		if err != nil {
			return err
		}
		se, err := t.ensure("season", sh.id, strconv.Itoa(p.ep.Season), p.itemFields("season"))
		if err != nil {
			return err
		}
		ep, err := t.ensure("episode", se.id, p.epKey, p.epRep.itemFields("episode"))
		if err != nil {
			return err
		}
		itemID, touched = ep.id, []*itemRow{ep, se, sh}
		if sc.showNFO != nil {
			if err := t.applyNFO(sh, sc.showNFO); err != nil {
				return err
			}
		}
		if sc.showArt != nil {
			if err := t.applyArt(sh, sc.showArt, artGone[sh.id]); err != nil {
				return err
			}
		}
		if err := t.applyArt(se, sc.seasonArt, artGone[se.id]); err != nil {
			return err
		}
		wr.showsDone[sh.id] = true
	} else {
		// A split or merged file goes where the admin or the TMDb merge put it,
		// and does not rename that movie after itself.
		mv := st.routed(p)
		if mv == nil {
			if mv, err = t.ensure("movie", 0, p.movieKey, p.movieRep.itemFields("movie")); err != nil {
				return err
			}
		}
		itemID, touched = mv.id, []*itemRow{mv}
	}
	it := touched[0]
	if sc.nfo != nil {
		if err := t.applyNFO(it, sc.nfo); err != nil {
			return err
		}
	}
	if err := t.applyArt(it, sc.art, artGone[it.id]); err != nil {
		return err
	}
	for _, x := range touched {
		if err := t.present(x); err != nil {
			return err
		}
	}

	tags, edition := p.tags()
	label := composeLabel(p.explicit, edition, tags, res)
	added := v == nil
	if probed {
		v, err = t.writeVersion(v, itemID, p, label, res, perr)
		if err != nil {
			return err
		}
		if perr != nil {
			wr.run.Failed++
			wr.log.Warn("probe failed", "path", p.v.rel, "err", perr)
		}
		if res != nil && !it.runtimeSet && res.DurationMs > 0 && !it.locked["runtime_ms"] {
			if _, err := tx.ExecContext(ctx, `UPDATE items SET runtime_ms = ? WHERE id = ?`, res.DurationMs, it.id); err != nil {
				return err
			}
			it.runtimeSet = true
		}
	} else if v.itemID != itemID || v.missingSince != 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE media_versions SET item_id = ?, missing_since = NULL, updated_at = ? WHERE id = ?`,
			itemID, nowMs, v.id); err != nil {
			return err
		}
		if v.itemID != itemID {
			st.moved[v.itemID] = true
		}
		v.itemID, v.missingSince = itemID, 0
	}
	if err := t.writeSidecars(v, sc.subs); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("write %s: %w", p.v.rel, err)
	}
	if added {
		wr.run.Added++
	} else {
		wr.run.Changed++
	}
	return nil
}

// dirty reports whether an unprobed file needs any write.
func (wr *writer) dirty(p *plan, v *verRow, item, season, show *itemRow, sc sidecars, gone map[int64]map[string]bool) bool {
	if v == nil || item == nil || v.itemID != item.id || v.missingSince != 0 || item.missingSince != 0 {
		return true
	}
	if season != nil && season.missingSince != 0 || show != nil && show.missingSince != 0 {
		return true
	}
	if !slices.Equal(v.external, subPaths(sc.subs)) {
		return true
	}
	if sc.nfo != nil || sc.showNFO != nil {
		return true
	}
	if len(gone) > 0 {
		return true
	}
	if artChanges(item, sc.art) || (season != nil && artChanges(season, sc.seasonArt)) || (show != nil && sc.showArt != nil && artChanges(show, sc.showArt)) {
		return true
	}
	return wr.titleDrift(p, item)
}

// titleDrift catches a filename-sourced item whose representative now parses
// differently, for example after the grouping mode changed.
func (wr *writer) titleDrift(p *plan, item *itemRow) bool {
	if item.source != "filename" || item.locked["title"] || (!p.episode && item.key != p.movieKey) {
		return false
	}
	f := p.movieRep
	if p.episode {
		f = p.epRep
	}
	if f != p {
		return false
	}
	want := f.itemFields(item.kind)
	return want.title != item.title || (want.year != item.year && !item.locked["year"])
}

func subPaths(subs []Sidecar) []string {
	out := make([]string, len(subs))
	for i, s := range subs {
		out[i] = s.Path
	}
	sort.Strings(out)
	return out
}

// artChanges reports whether local artwork found beside the file differs from
// what the item has. Art from another folder is kept while its file exists.
func artChanges(it *itemRow, found map[string]string) bool {
	for kind, p := range found {
		if it.locked[kind] || it.locked["artwork"] {
			continue
		}
		a, ok := it.art[kind]
		if !ok || a.source != "local" {
			return true
		}
		if a.path != p && !fileExists(a.path) {
			return true
		}
	}
	return false
}

// staleArt finds local artwork rows whose file has gone, per item and kind.
func (wr *writer) staleArt(item, season, show *itemRow, sc sidecars) map[int64]map[string]bool {
	out := map[int64]map[string]bool{}
	check := func(it *itemRow, found map[string]string) {
		if it == nil {
			return
		}
		for kind, a := range it.art {
			if a.source != "local" || found[kind] == a.path {
				continue
			}
			if fileGone(a.path) {
				if out[it.id] == nil {
					out[it.id] = map[string]bool{}
				}
				out[it.id][kind] = true
			}
		}
	}
	check(item, sc.art)
	check(season, sc.seasonArt)
	if sc.showArt != nil {
		check(show, sc.showArt)
	}
	return out
}

// statFile is os.Stat; tests replace it to simulate a share that fails a stat.
var statFile = os.Stat

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	fi, err := statFile(p)
	return err == nil && fi.Mode().IsRegular()
}

// fileGone reports whether p is known to be gone: it does not exist, or is no
// longer a regular file. Any other stat error (EIO, ESTALE, EACCES on a flaky
// share) says nothing either way, so the file is not treated as gone.
func fileGone(p string) bool {
	if p == "" {
		return true
	}
	fi, err := statFile(p)
	if err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	return !fi.Mode().IsRegular()
}

// fields are the filename-derived values an item is created with.
type fields struct {
	// added is the file's mtime: when it arrived on disk is what "recently added"
	// means, and a first scan must not stamp the whole library with one time.
	added                    int64
	title, sortTitle         string
	year                     int
	season, episode, epEnd   int
	airDate                  string
	hasSeason, hasEpisodeNum bool
}

func (p *plan) itemFields(kind string) fields {
	f := p.fields(kind)
	f.added = p.v.mtime
	return f
}

func (p *plan) fields(kind string) fields {
	switch kind {
	case "movie":
		t := p.movieTitle()
		return fields{title: t, sortTitle: SortTitle(t), year: p.movie.Year}
	case "show":
		t := p.showTitle()
		return fields{title: t, sortTitle: SortTitle(t), year: p.ep.ShowYear}
	case "season":
		t := seasonTitle(p.ep.Season)
		return fields{title: t, sortTitle: t, season: p.ep.Season, hasSeason: true}
	}
	t := p.episodeTitle()
	f := fields{title: t, sortTitle: SortTitle(t), season: p.ep.Season, hasSeason: true, airDate: p.ep.AirDate}
	if p.ep.Episode > 0 {
		f.episode, f.epEnd, f.hasEpisodeNum = p.ep.Episode, p.ep.EpisodeEnd, true
	}
	if p.ep.AirDate != "" {
		f.year, _ = strconv.Atoi(p.ep.AirDate[:4])
	}
	return f
}

// txw writes inside one file's transaction and keeps the state in step.
type txw struct {
	tx  *sql.Tx
	ctx context.Context
	lib Library
	st  *state
	now int64
}

func (t *txw) exec(q string, args ...any) error {
	_, err := t.tx.ExecContext(t.ctx, q, args...)
	return err
}

// ensure finds or creates an item. An existing item whose metadata still comes
// from file names takes the representative's title and year, unless locked.
func (t *txw) ensure(kind string, parent int64, key string, f fields) (*itemRow, error) {
	if it := t.st.byKey[identity(kind, parent, key)]; it != nil {
		if it.source == "filename" && !it.locked["title"] && (it.title != f.title || (it.year != f.year && !it.locked["year"])) {
			year := it.year
			if !it.locked["year"] {
				year = f.year
			}
			sortTitle := SortTitle(f.title)
			if kind == "season" {
				sortTitle = f.title
			}
			if err := t.exec(`UPDATE items SET title = ?, sort_title = CASE WHEN ? THEN sort_title ELSE ? END, year = ?, updated_at = ? WHERE id = ?`,
				f.title, it.locked["sort_title"], sortTitle, nullInt(int64(year)), t.now, it.id); err != nil {
				return nil, err
			}
			it.title, it.year = f.title, year
		}
		return it, nil
	}
	var par, season, episode, epEnd any
	if parent != 0 {
		par = parent
	}
	if f.hasSeason {
		season = f.season
	}
	if f.hasEpisodeNum {
		episode = f.episode
		if f.epEnd > f.episode {
			epEnd = f.epEnd
		}
	}
	res, err := t.tx.ExecContext(t.ctx, `INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, year,
		season_number, episode_number, episode_number_end, premiere_date, added_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.lib.ID, kind, par, key, f.title, f.sortTitle, nullInt(int64(f.year)), season, episode, epEnd, nullStr(f.airDate), t.addedAt(f.added), t.now)
	if err != nil {
		return nil, fmt.Errorf("create %s %q: %w", kind, f.title, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	it := &itemRow{id: id, parent: parent, kind: kind, key: key, title: f.title, year: f.year, source: "filename",
		locked: map[string]bool{}, extra: map[string]any{}, art: map[string]artRow{}}
	t.st.items[id] = it
	t.st.byKey[identity(kind, parent, key)] = it
	return it, nil
}

// addedAt is a file's mtime, never later than now (clocks on a NAS drift).
func (t *txw) addedAt(mtime int64) int64 {
	if mtime <= 0 || mtime > t.now {
		return t.now
	}
	return mtime
}

// present clears missing_since on an item that has a file again.
func (t *txw) present(it *itemRow) error {
	if it.missingSince == 0 {
		return nil
	}
	if err := t.exec(`UPDATE items SET missing_since = NULL, updated_at = ? WHERE id = ?`, t.now, it.id); err != nil {
		return err
	}
	it.missingSince = 0
	return nil
}

func (t *txw) writeVersion(v *verRow, itemID int64, p *plan, label string, res *probe.Result, perr error) (*verRow, error) {
	status, msg := "ok", ""
	if perr != nil || res == nil {
		status = "error"
		if perr != nil {
			msg = perr.Error()
		} else {
			msg = "no probe result"
		}
		// The extension still tells clients roughly what the file is.
		res = &probe.Result{Container: probe.Container("", p.v.abs)}
	}
	cols := []any{itemID, p.v.size, p.v.mtime, nullStr(label), nullStr(res.Container), nullInt(res.DurationMs), nullInt(res.Bitrate),
		nullStr(res.VideoCodec), nullStr(res.VideoProfile), nullInt(int64(res.VideoLevel)), nullInt(int64(res.BitDepth)),
		nullInt(int64(res.Width)), nullInt(int64(res.Height)), nullStr(res.Resolution), nullFloat(res.FrameRate),
		nullStr(res.HDRType), nullInt(int64(res.DVProfile)), nullInt(int64(res.DVCompatID)), status, nullStr(msg), t.now, t.now}
	if v == nil {
		r, err := t.tx.ExecContext(t.ctx, `INSERT INTO media_versions (item_id, size, mtime, label, container, duration_ms, bitrate,
			video_codec, video_profile, video_level, bit_depth, width, height, resolution, frame_rate, hdr_type, dv_profile,
			dv_compat_id, probe_status, probe_error, probed_at, updated_at, library_id, path, rel_path, added_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			append(cols, t.lib.ID, p.v.abs, p.v.rel, t.addedAt(p.v.mtime))...)
		if err != nil {
			return nil, fmt.Errorf("add %s: %w", p.v.rel, err)
		}
		id, err := r.LastInsertId()
		if err != nil {
			return nil, err
		}
		v = &verRow{id: id, path: p.v.abs, rel: p.v.rel}
		t.st.versions[v.path] = v
	} else {
		if v.itemID != itemID {
			t.st.moved[v.itemID] = true
		}
		if err := t.exec(`UPDATE media_versions SET item_id = ?, size = ?, mtime = ?, label = ?, container = ?, duration_ms = ?,
			bitrate = ?, video_codec = ?, video_profile = ?, video_level = ?, bit_depth = ?, width = ?, height = ?, resolution = ?,
			frame_rate = ?, hdr_type = ?, dv_profile = ?, dv_compat_id = ?, probe_status = ?, probe_error = ?, probed_at = ?,
			updated_at = ?, rel_path = ?, missing_since = NULL WHERE id = ?`, append(cols, p.v.rel, v.id)...); err != nil {
			return nil, err
		}
	}
	v.itemID, v.size, v.mtime, v.label, v.probeStatus, v.probedAt, v.missingSince = itemID, p.v.size, p.v.mtime, label, status, t.now, 0
	if err := t.exec(`DELETE FROM media_streams WHERE version_id = ? AND is_external = 0`, v.id); err != nil {
		return nil, err
	}
	for _, s := range res.Streams {
		if err := t.exec(`INSERT INTO media_streams (version_id, stream_index, kind, codec, profile, language, title, channels,
			channel_layout, sample_rate, bitrate, is_default, is_forced, is_hearing_impaired, is_image_based)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			v.id, s.Index, s.Kind, nullStr(s.Codec), nullStr(s.Profile), nullStr(s.Language), nullStr(s.Title),
			nullInt(int64(s.Channels)), nullStr(s.ChannelLayout), nullInt(int64(s.SampleRate)), nullInt(s.Bitrate),
			b2i(s.Default), b2i(s.Forced), b2i(s.HearingImpaired), b2i(s.ImageBased)); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func (t *txw) writeSidecars(v *verRow, subs []Sidecar) error {
	paths := subPaths(subs)
	if slices.Equal(v.external, paths) {
		return nil
	}
	// Downloaded subtitles are not beside the media, so a scan never removes them.
	if err := t.exec(`DELETE FROM media_streams WHERE version_id = ? AND is_external = 1 AND downloaded_id IS NULL`, v.id); err != nil {
		return err
	}
	for _, s := range subs {
		if err := t.exec(`INSERT INTO media_streams (version_id, kind, codec, language, title, is_default, is_forced,
			is_hearing_impaired, is_external, external_path, is_image_based) VALUES (?, 'subtitle', ?, ?, ?, ?, ?, ?, 1, ?, ?)`,
			v.id, s.Codec, nullStr(s.Language), nullStr(s.Title), b2i(s.Default), b2i(s.Forced), b2i(s.HearingImpaired),
			s.Path, b2i(s.ImageBased)); err != nil {
			return err
		}
	}
	v.external = paths
	return nil
}

// applyArt records local artwork. Local files beat TMDb and extracted frames;
// art already taken from another folder stays while its file exists; rows
// whose file vanished are dropped.
func (t *txw) applyArt(it *itemRow, found map[string]string, gone map[string]bool) error {
	if it.locked["artwork"] {
		return nil
	}
	for kind := range gone {
		if it.locked[kind] {
			continue
		}
		if _, ok := found[kind]; ok {
			continue
		}
		if err := t.exec(`DELETE FROM artwork WHERE item_id = ? AND kind = ? AND source = 'local'`, it.id, kind); err != nil {
			return err
		}
		delete(it.art, kind)
	}
	for _, kind := range []string{artPoster, artBackdrop, artThumb, artLogo} {
		p, ok := found[kind]
		if !ok || it.locked[kind] {
			continue
		}
		if a, ok := it.art[kind]; ok && a.source == "local" && (a.path == p || (!gone[kind] && fileExists(a.path))) {
			continue
		}
		if err := t.exec(`INSERT INTO artwork (item_id, kind, source, source_path, updated_at) VALUES (?, ?, 'local', ?, ?)
			ON CONFLICT (item_id, kind) DO UPDATE SET source = 'local', source_path = excluded.source_path, url = NULL,
			cache_path = NULL, width = NULL, height = NULL, updated_at = excluded.updated_at`, it.id, kind, p, t.now); err != nil {
			return err
		}
		it.art[kind] = artRow{source: "local", path: p}
	}
	return nil
}

// applyNFO copies NFO metadata onto an item. NFO beats file names; fields an
// admin locked are left alone. A scene NFO contributes only its ids.
func (t *txw) applyNFO(it *itemRow, f *nfoFile) error {
	n := f.doc
	set := map[string]any{}
	put := func(col string, v any, ok bool) {
		if ok && !it.locked[col] {
			set[col] = v
		}
	}
	put("imdb_id", n.IMDbID, n.IMDbID != "")
	put("tmdb_id", n.TMDbID, n.TMDbID > 0)
	if n.Structured() {
		put("title", n.Title, n.Title != "")
		switch {
		case n.SortTitle != "":
			put("sort_title", n.SortTitle, true)
		case n.Title != "" && it.kind != "season":
			put("sort_title", SortTitle(n.Title), !it.locked["title"])
		}
		put("original_title", n.OriginalTitle, n.OriginalTitle != "")
		put("year", n.Year, n.Year > 0 && it.kind != "episode")
		put("overview", n.Plot, n.Plot != "")
		put("tagline", n.Tagline, n.Tagline != "")
		put("content_rating", n.ContentRating, n.ContentRating != "")
		put("runtime_ms", n.RuntimeMs, n.RuntimeMs > 0)
		put("premiere_date", n.Premiered, n.Premiered != "")
		put("tmdb_collection_id", n.TMDbCollectionID, n.TMDbCollectionID > 0)
		if it.source != "manual" {
			set["metadata_source"] = "nfo"
		}
	}
	it.extra["nfo_path"], it.extra["nfo_stamp"] = f.path, f.stamp
	extra, err := json.Marshal(it.extra)
	if err != nil {
		return err
	}
	cols := make([]string, 0, len(set))
	for c := range set {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	q := "UPDATE items SET extra = ?, updated_at = ?"
	args := []any{string(extra), t.now}
	for _, c := range cols {
		q += ", " + c + " = ?"
		args = append(args, set[c])
	}
	if err := t.exec(q+" WHERE id = ?", append(args, it.id)...); err != nil {
		return err
	}
	if v, ok := set["title"].(string); ok {
		it.title = v
	}
	if v, ok := set["year"].(int); ok {
		it.year = v
	}
	if _, ok := set["runtime_ms"]; ok {
		it.runtimeSet = true
	}
	if s, ok := set["metadata_source"].(string); ok {
		it.source = s
	}
	if !n.Structured() {
		return nil
	}
	studioKind := "studio"
	if it.kind == "show" {
		studioKind = "network"
	}
	lists := []struct {
		kind   string
		values []string
	}{{"genre", n.Genres}, {studioKind, n.Studios}, {"director", n.Directors}, {"writer", n.Writers}, {"country", n.Countries}}
	for _, l := range lists {
		if len(l.values) == 0 || it.locked[l.kind] {
			continue
		}
		if err := t.exec(`DELETE FROM item_tags WHERE item_id = ? AND kind = ?`, it.id, l.kind); err != nil {
			return err
		}
		for i, v := range l.values {
			if err := t.exec(`INSERT OR IGNORE INTO item_tags (item_id, kind, value, position) VALUES (?, ?, ?, ?)`, it.id, l.kind, v, i); err != nil {
				return err
			}
		}
	}
	if len(n.Actors) > 0 && !it.locked["actor"] {
		if err := t.exec(`DELETE FROM item_tags WHERE item_id = ? AND kind = 'actor'`, it.id); err != nil {
			return err
		}
		for _, a := range n.Actors {
			if err := t.exec(`INSERT OR IGNORE INTO item_tags (item_id, kind, value, position, role) VALUES (?, 'actor', ?, ?, ?)`,
				it.id, a.Name, a.Order, nullStr(a.Role)); err != nil {
				return err
			}
		}
	}
	if len(n.Ratings) > 0 && !it.locked["rating"] {
		seen := map[string]bool{}
		for _, r := range n.Ratings {
			src := "nfo"
			switch r.Source {
			case "imdb":
				src = "imdb"
			case "tmdb", "themoviedb":
				src = "tmdb"
			}
			if seen[src] {
				continue
			}
			seen[src] = true
			if err := t.exec(`INSERT INTO item_ratings (item_id, source, value, votes) VALUES (?, ?, ?, ?)
				ON CONFLICT (item_id, source) DO UPDATE SET value = excluded.value, votes = excluded.votes`,
				it.id, src, r.Value, nullInt(int64(r.Votes))); err != nil {
				return err
			}
		}
	}
	if !it.locked["label"] {
		if err := t.exec(`DELETE FROM item_labels WHERE item_id = ? AND source = 'nfo'`, it.id); err != nil {
			return err
		}
		for _, tag := range n.Tags {
			if err := t.exec(`INSERT INTO labels (name) VALUES (?) ON CONFLICT (name) DO NOTHING`, tag); err != nil {
				return err
			}
			// An NFO label takes over the same label stamped by a smart
			// collection, so the collection never clears what the NFO set.
			if err := t.exec(`INSERT INTO item_labels (item_id, label_id, source)
				SELECT ?, id, 'nfo' FROM labels WHERE name = ?
				ON CONFLICT (item_id, label_id) DO UPDATE SET source = 'nfo' WHERE item_labels.source = 'collection'`, it.id, tag); err != nil {
				return err
			}
		}
	}
	return nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullFloat(f float64) any {
	if f == 0 {
		return nil
	}
	return f
}

// composeLabel names a version for the picker on the TV, e.g. "4K DV REMUX" or
// "Director's Cut 1080p BluRay". An explicit " - label" in the file name wins;
// probed resolution and HDR beat what the name claims.
func composeLabel(explicit, edition string, tags MediaTags, res *probe.Result) string {
	if explicit != "" {
		return explicit
	}
	resolution := tags.Resolution
	hdr := strings.Join(tags.HDR, " ")
	if res != nil {
		if res.Resolution != "" {
			resolution = res.Resolution
		}
		switch res.HDRType {
		case probe.DolbyVision:
			hdr = "DV"
		case probe.HDR10Plus:
			hdr = "HDR10+"
		case probe.HDR10:
			hdr = "HDR"
		case probe.HLG:
			hdr = "HLG"
		case "":
			hdr = ""
		}
	}
	var parts []string
	for _, s := range []string{edition, resolution, hdr, tags.Source} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if tags.ThreeD {
		parts = append(parts, "3D")
	}
	return strings.Join(parts, " ")
}

// reconcile marks unseen files missing, restores items whose files came back,
// and after MissingRetention deletes long-missing versions and empty items.
// A targeted scan touches only rows under its path.
func (s *Scanner) reconcile(ctx context.Context, st *state, w *walker, seen map[string]bool, sub string, isFile bool, run *Run) error {
	nowMs := s.now().UnixMilli()
	cutoff := nowMs - MissingRetention.Milliseconds()
	var missing, expired []int64
	scopeItems := map[int64]bool{}
	for p, v := range st.versions {
		if !inScope(v.rel, sub, isFile) {
			continue
		}
		scopeItems[v.itemID] = true
		switch {
		case v.missingSince == 0 && !seen[v.path] && !w.underFailed(v.rel):
			missing = append(missing, v.id)
			v.missingSince = nowMs
		case v.missingSince != 0 && v.missingSince < cutoff:
			expired = append(expired, v.id)
			delete(st.versions, p)
		}
	}
	for id := range st.moved {
		scopeItems[id] = true
	}
	if sub == "" {
		// A full scan also prunes items that lost every version some other way.
		for id := range st.items {
			scopeItems[id] = true
		}
	}
	run.Missing = len(missing)

	// Every item in scope plus the seasons and shows above it, by level.
	levels := map[string][]int64{}
	added := map[int64]bool{}
	for id := range scopeItems {
		for it := st.items[id]; it != nil && !added[it.id]; it = st.items[it.parent] {
			added[it.id] = true
			lv := it.kind
			if lv == "movie" {
				lv = "episode"
			}
			levels[lv] = append(levels[lv], it.id)
		}
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	exec := func(q string, ids []int64, lead ...any) error {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, chunk := range chunks(ids, 500) {
			args := append(append([]any{}, lead...), int64s(chunk)...)
			if _, err := tx.ExecContext(ctx, strings.Replace(q, "(?)", "("+placeholders(len(chunk))+")", 1), args...); err != nil {
				return err
			}
		}
		return nil
	}
	if err := exec(`UPDATE media_versions SET missing_since = ?, updated_at = ? WHERE id IN (?)`, missing, nowMs, nowMs); err != nil {
		return err
	}
	if err := exec(`DELETE FROM media_versions WHERE id IN (?)`, expired); err != nil {
		return err
	}
	// Leaves first, then seasons, then shows, so each level sees the one below.
	leaves := levels["episode"]
	if err := exec(`UPDATE items SET missing_since = NULL, updated_at = ? WHERE id IN (?) AND missing_since IS NOT NULL
		AND EXISTS (SELECT 1 FROM media_versions v WHERE v.item_id = items.id AND v.missing_since IS NULL)`, leaves, nowMs); err != nil {
		return err
	}
	if err := exec(`UPDATE items SET missing_since = ?, updated_at = ? WHERE id IN (?) AND missing_since IS NULL
		AND NOT EXISTS (SELECT 1 FROM media_versions v WHERE v.item_id = items.id AND v.missing_since IS NULL)`, leaves, nowMs, nowMs); err != nil {
		return err
	}
	if err := exec(`DELETE FROM items WHERE id IN (?) AND NOT EXISTS (SELECT 1 FROM media_versions v WHERE v.item_id = items.id)`, leaves); err != nil {
		return err
	}
	for _, lv := range []string{"season", "show"} {
		ids := levels[lv]
		if err := exec(`UPDATE items SET missing_since = NULL, updated_at = ? WHERE id IN (?) AND missing_since IS NOT NULL
			AND EXISTS (SELECT 1 FROM items c WHERE c.parent_id = items.id AND c.missing_since IS NULL)`, ids, nowMs); err != nil {
			return err
		}
		if err := exec(`UPDATE items SET missing_since = ?, updated_at = ? WHERE id IN (?) AND missing_since IS NULL
			AND NOT EXISTS (SELECT 1 FROM items c WHERE c.parent_id = items.id AND c.missing_since IS NULL)`, ids, nowMs, nowMs); err != nil {
			return err
		}
		if err := exec(`DELETE FROM items WHERE id IN (?) AND NOT EXISTS (SELECT 1 FROM items c WHERE c.parent_id = items.id)`, ids); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func chunks(ids []int64, n int) [][]int64 {
	var out [][]int64
	for len(ids) > 0 {
		k := min(n, len(ids))
		out = append(out, ids[:k])
		ids = ids[k:]
	}
	return out
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func int64s(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}
