package markers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
)

// Job finds markers for new episodes in the background. It is built to stay
// out of the way of people watching on the same box: one season at a time,
// one ffmpeg at a time, everything at nice 19, only inside Hours, and never
// in a library that is being scanned. An episode is due when it has no
// marker_scans row for its current file, so a restart carries on where the
// last run stopped, and fingerprints are cached on disk so a season is not
// decoded again when one more episode arrives.
type Job struct {
	DB      *sql.DB
	Decoder Decoder
	// CacheDir holds the fingerprint cache.
	CacheDir string
	Log      *slog.Logger
	Hours    Hours
	// IntroWindow and CreditsWindow are how much of the start and of the end
	// of each episode are compared (10 and 8 minutes by default).
	IntroWindow, CreditsWindow time.Duration
	// Busy reports a library being scanned; its seasons wait.
	Busy func(libraryID int64) bool
	Now  func() time.Time
	// InitialDelay is the wait before the first pass; Interval the wait
	// between passes. A scan that adds titles calls Wake.
	InitialDelay, Interval time.Duration

	once   sync.Once
	kick   chan struct{}
	tasks  chan func(context.Context)
	queued sync.Map // season id -> struct{}, for Rerun
}

// ErrQueueFull is returned when admins have queued more work than the job
// keeps; it is all still due and runs in the next window.
var ErrQueueFull = errors.New("marker detection queue is full")

// Rules for each kind of marker.
var (
	introRules   = Rules{MinMs: 15_000, MaxMs: 180_000, Partners: 4}
	creditsRules = Rules{MinMs: 10_000, MaxMs: 8 * 60_000, Partners: 4, Credits: true}
)

const (
	// A final stretch of black picture this long, or of silence this long,
	// counts as credits when nothing was matched. Silence needs longer: a
	// quiet last scene is still a scene.
	minBlackTailMs  = 5_000
	minSilentTailMs = 15_000
	// fallbackConfidence marks a credits marker from black or silence.
	fallbackConfidence = 0.3
	// Credits that end this close to the end of the file, or to the silence
	// that ends it, run to the end: too short for a scene to hide in.
	creditsGapMs = 2_000
	// sceneMarginMs is taken off credits that something follows.
	sceneMarginMs = 1_000
)

func (j *Job) init() {
	j.once.Do(func() {
		j.kick = make(chan struct{}, 1)
		j.tasks = make(chan func(context.Context), 64)
		if j.Log == nil {
			j.Log = slog.Default()
		}
		if j.IntroWindow <= 0 {
			j.IntroWindow = 10 * time.Minute
		}
		if j.CreditsWindow <= 0 {
			j.CreditsWindow = 8 * time.Minute
		}
		if j.Interval <= 0 {
			j.Interval = 10 * time.Minute
		}
	})
}

func (j *Job) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

// Wake asks for a pass now (still only inside Hours), after a scan added
// episodes.
func (j *Job) Wake() {
	j.init()
	select {
	case j.kick <- struct{}{}:
	default:
	}
}

// Rerun forgets what was worked out for the seasons of an item (a show, a
// season or an episode) and detects them again now, whatever the hour: an
// admin asked. Markers an admin set or cleared stay. It returns how many
// seasons were queued.
func (j *Job) Rerun(ctx context.Context, itemID int64) (int, error) {
	j.init()
	seasons, err := seasonsOf(ctx, j.DB, itemID)
	if err != nil {
		return 0, err
	}
	for _, s := range seasons {
		if _, err := j.DB.ExecContext(ctx, `DELETE FROM marker_scans WHERE item_id IN (SELECT id FROM items WHERE parent_id = ?)`, s); err != nil {
			return 0, err
		}
		if _, loaded := j.queued.LoadOrStore(s, struct{}{}); loaded {
			continue
		}
		err := j.enqueue(func(ctx context.Context) {
			j.queued.Delete(s)
			if err := j.Season(ctx, s); err != nil && ctx.Err() == nil {
				j.Log.Warn("marker detection did not complete", "season", s, "err", err)
			}
		})
		if err != nil {
			// Still due: the next pass inside Hours takes it.
			j.queued.Delete(s)
		}
	}
	return len(seasons), nil
}

// enqueue hands work to Run's goroutine, which does one thing at a time.
func (j *Job) enqueue(task func(context.Context)) error {
	j.init()
	select {
	case j.tasks <- task:
		return nil
	default:
		return ErrQueueFull
	}
}

// ErrNoSeasons is an item with no seasons to detect: a movie, or nothing.
var ErrNoSeasons = errors.New("no seasons")

func seasonsOf(ctx context.Context, db *sql.DB, itemID int64) ([]int64, error) {
	var kind string
	var parent sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT kind, parent_id FROM items WHERE id = ?`, itemID).Scan(&kind, &parent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoSeasons
	}
	if err != nil {
		return nil, err
	}
	switch kind {
	case "season":
		return []int64{itemID}, nil
	case "episode":
		return []int64{parent.Int64}, nil
	case "show":
		rows, err := db.QueryContext(ctx, `SELECT id FROM items WHERE parent_id = ? AND kind = 'season' ORDER BY season_number`, itemID)
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
	return nil, ErrNoSeasons
}

// Run works until ctx ends. Call it on its own goroutine: it lowers that
// goroutine's thread to nice 19 for good.
func (j *Job) Run(ctx context.Context) {
	j.init()
	lowerThreadPriority()
	timer := time.NewTimer(j.InitialDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case task := <-j.tasks:
			// An admin asked: run now, whatever the hour, still one at a time.
			task(ctx)
			continue
		case <-j.kick:
		case <-timer.C:
			timer.Reset(j.Interval)
		}
		if !j.Hours.Contains(j.now()) {
			continue
		}
		st, err := j.Drain(ctx)
		if st.Seasons+st.Errors > 0 {
			j.Log.Info("marker detection pass", "seasons", st.Seasons, "episodes", st.Episodes, "intros", st.Intros,
				"credits", st.Credits, "errors", st.Errors, "took", st.Took.Round(time.Second).String())
		}
		if err != nil && ctx.Err() == nil {
			j.Log.Warn("marker detection pass stopped", "err", err)
		}
	}
}

// Stats counts one pass.
type Stats struct {
	Seasons, Episodes, Intros, Credits, Errors int
	Took                                       time.Duration
}

func (s *Stats) add(o Stats) {
	s.Seasons += o.Seasons
	s.Episodes += o.Episodes
	s.Intros += o.Intros
	s.Credits += o.Credits
	s.Errors += o.Errors
}

// Drain handles due seasons, newest first, until none is left or Hours ends.
func (j *Job) Drain(ctx context.Context) (Stats, error) {
	j.init()
	start := time.Now()
	var total Stats
	tried := map[int64]bool{}
	for ctx.Err() == nil && j.Hours.Contains(j.now()) {
		season, lib, err := j.nextDue(ctx, tried)
		if err != nil || season == 0 {
			total.Took = time.Since(start)
			return total, err
		}
		tried[season] = true
		if j.Busy != nil && j.Busy(lib) {
			continue
		}
		st, err := j.season(ctx, season)
		total.add(st)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			total.Errors++
			j.Log.Warn("marker detection failed", "season", season, "err", err)
		}
	}
	total.Took = time.Since(start)
	return total, ctx.Err()
}

// dueCond is true for an episode whose current file has not been looked at.
// It expects the episode as e, its file as v and its marker_scans row as m.
const dueCond = `(m.item_id IS NULL OR m.version_id IS NOT v.id OR m.size != v.size OR m.mtime != v.mtime)`

// fileJoin picks each episode's file: the first that is present and probed.
const fileJoin = `JOIN media_versions v ON v.id = (SELECT v2.id FROM media_versions v2
	WHERE v2.item_id = e.id AND v2.missing_since IS NULL AND v2.probe_status = 'ok' ORDER BY v2.id LIMIT 1)
	LEFT JOIN marker_scans m ON m.item_id = e.id`

func (j *Job) nextDue(ctx context.Context, tried map[int64]bool) (season, library int64, err error) {
	rows, err := j.DB.QueryContext(ctx, `SELECT e.parent_id, e.library_id FROM items e `+fileJoin+`
		WHERE e.kind = 'episode' AND e.missing_since IS NULL AND e.parent_id IS NOT NULL AND `+dueCond+`
		GROUP BY e.parent_id ORDER BY MAX(e.added_at) DESC, e.parent_id`)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := rows.Scan(&season, &library); err != nil {
			return 0, 0, err
		}
		if !tried[season] {
			return season, library, rows.Close()
		}
	}
	return 0, 0, rows.Err()
}

// Season detects one season now.
func (j *Job) Season(ctx context.Context, seasonID int64) error {
	j.init()
	st, err := j.season(ctx, seasonID)
	if err == nil {
		j.Log.Info("markers detected", "season", seasonID, "episodes", st.Episodes, "intros", st.Intros, "credits", st.Credits,
			"took", st.Took.Round(time.Millisecond).String())
	}
	return err
}

type episode struct {
	id, version, size, mtime, duration int64
	root, path                         string
	due                                bool
	has                                map[string]bool // kinds with a marker row, cleared ones included
	intro, credits                     Clip
	explicit                           map[string]Marker
	failed                             error
}

func (j *Job) loadSeason(ctx context.Context, seasonID int64) ([]*episode, error) {
	rows, err := j.DB.QueryContext(ctx, `SELECT e.id, v.id, v.size, v.mtime, IFNULL(v.duration_ms, 0), l.root_path, v.path, `+dueCond+`
		FROM items e `+fileJoin+` JOIN libraries l ON l.id = e.library_id
		WHERE e.parent_id = ? AND e.kind = 'episode' AND e.missing_since IS NULL
		ORDER BY e.episode_number IS NULL, e.episode_number, e.id`, seasonID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var eps []*episode
	byID := map[int64]*episode{}
	for rows.Next() {
		e := &episode{has: map[string]bool{}}
		if err := rows.Scan(&e.id, &e.version, &e.size, &e.mtime, &e.duration, &e.root, &e.path, &e.due); err != nil {
			return nil, err
		}
		eps = append(eps, e)
		byID[e.id] = e
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	mrows, err := j.DB.QueryContext(ctx, `SELECT k.item_id, k.kind FROM item_markers k JOIN items e ON e.id = k.item_id WHERE e.parent_id = ?`, seasonID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = mrows.Close() }()
	for mrows.Next() {
		var id int64
		var kind string
		if err := mrows.Scan(&id, &kind); err != nil {
			return nil, err
		}
		if e := byID[id]; e != nil {
			e.has[kind] = true
		}
	}
	return eps, mrows.Err()
}

// season works out the markers of a season's due episodes, and of any of its
// episodes still without one (a new episode may be the partner they lacked).
func (j *Job) season(ctx context.Context, seasonID int64) (Stats, error) {
	start := time.Now()
	st := Stats{Seasons: 1}
	eps, err := j.loadSeason(ctx, seasonID)
	if err != nil || len(eps) == 0 {
		return st, err
	}
	targets := map[string][]int{}
	for i, e := range eps {
		for _, kind := range []string{Intro, Credits} {
			if e.due || !e.has[kind] {
				targets[kind] = append(targets[kind], i)
			}
		}
	}
	final, err := j.compute(ctx, eps, targets)
	if err != nil {
		return st, err
	}
	tx, err := j.DB.BeginTx(ctx, nil)
	if err != nil {
		return st, err
	}
	defer func() { _ = tx.Rollback() }()
	now := j.now().UnixMilli()
	for kind, list := range targets {
		for _, i := range list {
			e := eps[i]
			m := final[i][kind]
			if e.failed != nil && m == nil {
				continue // keep what an earlier pass found; this one could not read the file
			}
			if err := apply(ctx, tx, e.id, kind, m, now); err != nil {
				return st, err
			}
			switch {
			case m != nil && kind == Intro:
				st.Intros++
			case m != nil:
				st.Credits++
			}
		}
	}
	for _, e := range eps {
		if !e.due {
			continue
		}
		st.Episodes++
		status, msg := "done", sql.NullString{}
		if e.failed != nil {
			status, msg = "error", sql.NullString{String: e.failed.Error(), Valid: true}
			st.Errors++
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO marker_scans (item_id, version_id, size, mtime, status, error, checked_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (item_id) DO UPDATE SET version_id = excluded.version_id, size = excluded.size, mtime = excluded.mtime,
				status = excluded.status, error = excluded.error, checked_at = excluded.checked_at`,
			e.id, e.version, e.size, e.mtime, status, msg, now); err != nil {
			return st, err
		}
	}
	st.Took = time.Since(start)
	return st, tx.Commit()
}

// compute fingerprints what the targets need (each target and the
// neighbours it is compared with) and returns each target's markers by
// kind: from its NFO, else a chapter title, else detection. A kind with
// nothing found is absent.
func (j *Job) compute(ctx context.Context, eps []*episode, targets map[string][]int) (map[int]map[string]*Marker, error) {
	needed := map[int]bool{}
	for _, list := range targets {
		for _, i := range list {
			needed[i] = true
			for _, p := range neighbours(placeholder(len(eps)), i, 4) {
				needed[p] = true
			}
		}
	}
	for i := range eps {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if needed[i] {
			j.prepare(ctx, eps[i])
		}
	}
	intros := make([]Clip, len(eps))
	credits := make([]Clip, len(eps))
	for i, e := range eps {
		intros[i], credits[i] = e.intro, e.credits
	}
	found := map[string]map[int]Found{
		Intro:   Detect(intros, targets[Intro], introRules),
		Credits: Detect(credits, targets[Credits], creditsRules),
	}
	final := map[int]map[string]*Marker{}
	for kind, list := range targets {
		for _, i := range list {
			e := eps[i]
			if final[i] == nil {
				final[i] = map[string]*Marker{}
			}
			if m, ok := e.explicit[kind]; ok {
				final[i][kind] = &m
				continue
			}
			f, ok := found[kind][i]
			switch {
			case ok && kind == Credits:
				f = creditsEnd(e, f)
			case !ok && kind == Credits && e.failed == nil && e.due:
				// Only for a new file: it may decode video, so it runs once per file.
				f, ok = j.deadAir(ctx, e)
			}
			if ok {
				final[i][kind] = &Marker{Kind: kind, StartMs: f.StartMs, EndMs: f.EndMs, Confidence: f.Confidence, Source: SourceDetected}
			}
		}
	}
	return final, ctx.Err()
}

// placeholder stands in for clips not decoded yet, so neighbours can say
// which episodes a target will be compared with.
func placeholder(n int) []Clip {
	cs := make([]Clip, n)
	for i := range cs {
		cs[i].FP = Fingerprint{Hashes: []uint32{0}}
	}
	return cs
}

// prepare reads an episode's NFO and chapter markers and fingerprints the
// start and end of its audio, from the cache when it can. A failure is kept
// on the episode, which then has no fingerprint.
func (j *Job) prepare(ctx context.Context, e *episode) {
	path, err := fsafe.ResolveUnder(e.root, e.path)
	if err != nil {
		e.failed = err
		return
	}
	e.explicit = map[string]Marker{}
	if cs, err := j.Decoder.Chapters(ctx, path); err == nil {
		for k, m := range fromChapters(cs) {
			e.explicit[k] = m
		}
	}
	if nfo, err := fromNFO(e.root, path); err == nil {
		for k, m := range nfo { // NFO beats chapters
			e.explicit[k] = m
		}
	}
	introLen := j.IntroWindow.Milliseconds()
	if e.duration > 0 {
		introLen = min(introLen, e.duration)
	}
	fp, err := j.fingerprint(ctx, e, Intro, 0, introLen, path)
	if err != nil {
		e.failed = err
		return
	}
	e.intro = Clip{FP: fp, DurationMs: e.duration}
	if e.duration <= 0 {
		return
	}
	creditsLen := min(j.CreditsWindow.Milliseconds(), e.duration)
	off := e.duration - creditsLen
	fp, err = j.fingerprint(ctx, e, Credits, off, creditsLen, path)
	if err != nil {
		e.failed = err
		return
	}
	e.credits = Clip{FP: fp, OffsetMs: off, DurationMs: e.duration}
}

func (j *Job) fingerprint(ctx context.Context, e *episode, kind string, offMs, lenMs int64, path string) (Fingerprint, error) {
	dir := filepath.Join(j.CacheDir, "markers")
	name := cacheName(e.version, e.size, e.mtime, kind, offMs, lenMs)
	if j.CacheDir != "" {
		if fp, ok := readCache(filepath.Join(dir, name)); ok {
			return fp, nil
		}
	}
	pcm, err := j.Decoder.PCM(ctx, path, offMs, lenMs)
	if err != nil {
		return Fingerprint{}, fmt.Errorf("decode audio: %w", err)
	}
	fp := Compute(pcm)
	if j.CacheDir != "" {
		if err := writeCache(dir, e.version, kind, name, fp); err != nil {
			j.Log.Warn("fingerprint cache", "err", err)
		}
	}
	return fp, nil
}

// creditsEnd decides where matched credits end. The match itself ends where
// the shared credits audio stops; whatever follows is either dead air
// (silence to the end of the file), in which case the credits run to the end
// and a player can go straight to the next episode, or something else: a
// post-credits scene, an epilogue, a different song. That is kept: the marker
// ends where the shared audio did, so Skip Credits lands on the scene.
func creditsEnd(e *episode, f Found) Found {
	c := e.credits
	if c.FP.TrailingSilent > 0 {
		silentFrom := c.OffsetMs + FrameMs(c.FP.Len()-c.FP.TrailingSilent)
		if silentFrom <= f.EndMs+creditsGapMs {
			f.EndMs = e.duration
			return f
		}
	}
	// The window's last frame stops short of the end of the file by under a
	// frame and a half; nothing can hide in that.
	if e.duration-f.EndMs <= creditsGapMs {
		f.EndMs = e.duration
		return f
	}
	// Something follows. Measured edges land within a few hundred
	// milliseconds, either side; stopping a second early means Skip Credits
	// shows a moment of credits rather than missing the start of a scene.
	f.EndMs = max(f.StartMs+1000, f.EndMs-sceneMarginMs)
	return f
}

// deadAir is the fallback when no credits were matched: a final stretch of
// silence, or of black picture, running to the end of the file.
func (j *Job) deadAir(ctx context.Context, e *episode) (Found, bool) {
	c := e.credits
	if c.FP.Len() == 0 || e.duration <= 0 {
		return Found{}, false
	}
	if c.FP.TrailingSilent > 0 {
		from := c.OffsetMs + FrameMs(c.FP.Len()-c.FP.TrailingSilent)
		if e.duration-from >= minSilentTailMs && from > e.duration/2 {
			return Found{StartMs: from, EndMs: e.duration, Confidence: fallbackConfidence}, true
		}
	}
	path, err := fsafe.ResolveUnder(e.root, e.path)
	if err != nil {
		return Found{}, false
	}
	look := min(int64(180_000), e.duration)
	from, err := j.Decoder.BlackFrom(ctx, path, e.duration-look, e.duration)
	if err != nil || from < 0 {
		return Found{}, false
	}
	if e.duration-from >= minBlackTailMs && from > e.duration/2 {
		return Found{StartMs: from, EndMs: e.duration, Confidence: fallbackConfidence}, true
	}
	return Found{}, false
}
