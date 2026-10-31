package markers

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
)

type lib struct {
	t       *testing.T
	db      *sql.DB
	root    string
	library int64
	show    int64
	seasons int
}

func newLib(t *testing.T) *lib {
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
	l := &lib{t: t, db: conn, root: filepath.Join(dir, "TV")}
	l.library = l.exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('TV', 'show', ?, 0, 0)`, l.root)
	l.show = l.exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (?, 'show', 'show', 'Show', 'Show', 0, 0)`, l.library)
	return l
}

func (l *lib) exec(q string, args ...any) int64 {
	l.t.Helper()
	res, err := l.db.Exec(q, args...)
	if err != nil {
		l.t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

// season makes the episodes with ffmpeg and records them as a scan would.
// It returns episode ids and each episode's part start times.
func (l *lib) season(d Decoder, episodes ...[]part) ([]int64, [][]int64) {
	l.t.Helper()
	l.seasons++
	n := l.seasons
	sid := l.exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, added_at, updated_at)
		VALUES (?, 'season', ?, ?, 'Season', 'Season', ?, 0, 0)`, l.library, l.show, n, n)
	dir := filepath.Join(l.root, "Show", "Season "+string(rune('0'+n)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		l.t.Fatal(err)
	}
	var ids []int64
	var starts [][]int64
	for i, parts := range episodes {
		path := filepath.Join(dir, "Show - S0"+string(rune('0'+n))+"E0"+string(rune('1'+i))+".mp4")
		starts = append(starts, makeEpisode(l.t, path, parts...))
		dur, err := d.Duration(context.Background(), path)
		if err != nil {
			l.t.Fatal(err)
		}
		fi, _ := os.Stat(path)
		id := l.exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, episode_number, added_at, updated_at)
			VALUES (?, 'episode', ?, ?, 'Ep', 'Ep', ?, ?, ?, 0)`, l.library, sid, i+1, n, i+1, i)
		l.exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, duration_ms, probe_status, added_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'ok', 0, 0)`, id, l.library, path, filepath.Base(path), fi.Size(), fi.ModTime().UnixMilli(), dur)
		ids = append(ids, id)
	}
	return ids, starts
}

func (l *lib) markers(id int64) map[string]Marker {
	l.t.Helper()
	ms, err := ForItem(context.Background(), l.db, id)
	if err != nil {
		l.t.Fatal(err)
	}
	out := map[string]Marker{}
	for _, m := range ms {
		out[m.Kind] = m
	}
	return out
}

func (l *lib) duration(id int64) int64 {
	var d int64
	if err := l.db.QueryRow(`SELECT duration_ms FROM media_versions WHERE item_id = ?`, id).Scan(&d); err != nil {
		l.t.Fatal(err)
	}
	return d
}

func newJob(l *lib, d Decoder) *Job {
	return &Job{DB: l.db, Decoder: d, CacheDir: filepath.Join(l.t.TempDir(), "cache"), Hours: Hours{always: true},
		IntroWindow: 3 * time.Minute, CreditsWindow: 2 * time.Minute, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func near(t *testing.T, what string, got, want int64) {
	t.Helper()
	if diff := got - want; diff < -1000 || diff > 1000 {
		t.Errorf("%s: got %d ms, want %d ms (off by %d ms, more than 1 s)", what, got, want, diff)
	}
}

// The season the brief asks for: the theme after a cold open of a different
// length in each episode (none in the last), a recap in two episodes that
// repeats a scene of the episode before, the same end credits everywhere,
// a post-credits scene in two episodes and a silent tail in one.
func TestDetectsIntroAndCreditsInASyntheticSeason(t *testing.T) {
	d := needFFmpeg(t)
	l := newLib(t)
	theme := part{"theme", 30, 0}
	credits := part{"credits", 25, 0}
	ids, starts := l.season(d,
		[]part{{"unique", 8, 101}, theme, {"unique", 50, 102}, credits, {"unique", 12, 103}},
		[]part{{"unique", 20, 102}, {"unique", 6, 201}, theme, {"unique", 55, 202}, credits},
		[]part{{"unique", 25, 301}, theme, {"unique", 45, 302}, credits, {"silence", 4, 0}},
		[]part{{"unique", 20, 302}, theme, {"unique", 60, 402}, credits, {"unique", 15, 403}},
		[]part{theme, {"unique", 70, 502}, credits},
	)
	// Where the theme and credits parts sit in each episode.
	themeAt := []int{1, 2, 1, 1, 0}
	j := newJob(l, d)
	start := time.Now()
	st, err := j.Drain(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d episodes in %v: %+v", st.Episodes, time.Since(start).Round(time.Millisecond), st)
	for i, id := range ids {
		m := l.markers(id)
		intro, ok := m[Intro]
		if !ok {
			t.Errorf("episode %d: no intro", i+1)
		} else {
			at := starts[i][themeAt[i]]
			near(t, "intro start", intro.StartMs, at)
			near(t, "intro end", intro.EndMs, at+30_000)
			if intro.Source != SourceDetected || intro.Confidence < 0.5 {
				t.Errorf("episode %d intro: %+v", i+1, intro)
			}
		}
		c, ok := m[Credits]
		if !ok {
			t.Errorf("episode %d: no credits", i+1)
			continue
		}
		cAt := starts[i][themeAt[i]+2]
		near(t, "credits start", c.StartMs, cAt)
		switch i {
		case 0, 3:
			// A post-credits scene follows: the credits must end before it,
			// by no more than the deliberate one second margin and a little.
			scene := cAt + 25_000
			if c.EndMs > scene || c.EndMs < scene-2000 {
				t.Errorf("episode %d: credits end at %d ms; the post-credits scene starts at %d ms", i+1, c.EndMs, scene)
			}
		default:
			// Nothing but silence follows: the credits run to the end.
			if c.EndMs != l.duration(id) {
				t.Errorf("episode %d: credits end at %d ms, want the end of the file %d ms", i+1, c.EndMs, l.duration(id))
			}
		}
		t.Logf("episode %d: intro %+v credits %+v", i+1, intro, c)
	}
	if t.Failed() {
		return
	}
	ctx := context.Background()

	// An admin's own marker, and one they cleared, survive detection run
	// again; a detected one the new run does not find is removed.
	if err := SetManual(ctx, l.db, ids[0], Intro, 1000, 20_000); err != nil {
		t.Fatal(err)
	}
	if err := Clear(ctx, l.db, ids[1], Credits); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Rerun(ctx, l.show); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if m := l.markers(ids[0])[Intro]; m.Source != SourceManual || m.StartMs != 1000 || m.EndMs != 20_000 {
		t.Errorf("manual intro after a rerun: %+v", m)
	}
	if m, ok := l.markers(ids[1])[Credits]; ok {
		t.Errorf("cleared credits came back: %+v", m)
	}

	// Apply to the whole season: episode 3's intro, set by hand, is found in
	// the others by its audio. Episode 1's own manual marker is kept.
	l.exec(`DELETE FROM item_markers WHERE source = 'detected'`)
	at3 := starts[2][themeAt[2]]
	if err := SetManual(ctx, l.db, ids[2], Intro, at3, at3+30_000); err != nil {
		t.Fatal(err)
	}
	n, err := j.propagate(ctx, l.seasonID(ids[2]), ids[2], Intro, at3, at3+30_000)
	if err != nil || n != 3 {
		t.Fatalf("propagate: %d episodes, %v; want 3", n, err)
	}
	for i := 1; i < len(ids); i++ {
		if i == 2 {
			continue
		}
		m := l.markers(ids[i])[Intro]
		at := starts[i][themeAt[i]]
		near(t, "copied intro start", m.StartMs, at)
		near(t, "copied intro end", m.EndMs, at+30_000)
	}
	if m := l.markers(ids[0])[Intro]; m.StartMs != 1000 {
		t.Errorf("propagate replaced an admin's marker: %+v", m)
	}

	// The dry run prints the same season from the folder alone.
	var out strings.Builder
	dry := &Job{Decoder: d, IntroWindow: j.IntroWindow, CreditsWindow: j.CreditsWindow, Log: j.Log}
	if err := dry.DryRun(ctx, l.root, &out); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + out.String())
	if got := strings.Count(out.String(), " detected "); got != 2*len(ids) {
		t.Errorf("dry run printed %d detected markers, want %d", got, 2*len(ids))
	}
}

func (l *lib) seasonID(episode int64) int64 {
	var id int64
	if err := l.db.QueryRow(`SELECT parent_id FROM items WHERE id = ?`, episode).Scan(&id); err != nil {
		l.t.Fatal(err)
	}
	return id
}

// No shared audio but digital silence and a steady tone, the same in every
// episode: nothing may be detected. False positives are worse than misses.
func TestNoIntroWhereNothingIsShared(t *testing.T) {
	d := needFFmpeg(t)
	l := newLib(t)
	ids, _ := l.season(d,
		[]part{{"silence", 20, 0}, {"tone", 20, 0}, {"unique", 60, 11}},
		[]part{{"silence", 20, 0}, {"tone", 20, 0}, {"unique", 60, 12}},
		[]part{{"silence", 20, 0}, {"tone", 20, 0}, {"unique", 60, 13}},
		[]part{{"unique", 100, 14}},
	)
	j := newJob(l, d)
	if _, err := j.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		if m := l.markers(id); len(m) != 0 {
			t.Errorf("episode %d: found %+v in a season that shares nothing", i+1, m)
		}
	}
	var done int
	_ = l.db.QueryRow(`SELECT COUNT(*) FROM marker_scans WHERE status = 'done'`).Scan(&done)
	if done != len(ids) {
		t.Errorf("%d episodes recorded as done, want %d", done, len(ids))
	}
}
