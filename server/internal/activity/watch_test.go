package activity

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
)

const day = 24 * time.Hour

type watchFixture struct {
	svc                            *Service
	conn                           *sql.DB
	now                            time.Time
	docs, movies, tv               int64
	lantern, orchard, gone, salt   int64
	show, season, ep1, ep2, epGone int64
}

// newWatchFixture builds three libraries: a movie played by two people, a
// movie nobody played, a movie gone from disk, an old documentary, and a show
// with two episodes on disk and one gone.
func newWatchFixture(t *testing.T) *watchFixture {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := db.Migrate(context.Background(), conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 30, 20, 0, 0, 0, time.UTC)
	f := &watchFixture{conn: conn, now: now, svc: &Service{DB: conn, Now: func() time.Time { return now }, Location: time.UTC}}
	ago := func(d time.Duration) int64 { return now.Add(-d).UnixMilli() }
	exec := func(q string, args ...any) int64 {
		t.Helper()
		res, err := conn.Exec(q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	lib := func(name, kind string) int64 {
		return exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES (?, ?, ?, 0, 0)`, name, kind, "/media/"+name)
	}
	f.movies, f.tv, f.docs = lib("Movies", "movie"), lib("TV", "show"), lib("Docs", "movie")
	item := func(libID int64, kind string, parent any, title string, year any, missing any) int64 {
		return exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, year, missing_since, added_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`, libID, kind, parent, title, title, title, year, missing, ago(400*day))
	}
	file := func(itemID, libID int64, path string, size int64, missing any) {
		exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, missing_since, added_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 0, ?, 0, 0)`, itemID, libID, path, filepath.Base(path), size, missing)
	}
	f.lantern = item(f.movies, "movie", nil, "Lantern Hill", 2004, nil)
	file(f.lantern, f.movies, "/media/Movies/Lantern Hill (2004)/Lantern Hill 2160p.mkv", 200, nil)
	file(f.lantern, f.movies, "/media/Movies/Lantern Hill (2004)/Lantern Hill 1080p.mkv", 100, nil)
	file(f.lantern, f.movies, "/media/Movies/Lantern Hill (2004)/Lantern Hill old.avi", 999, ago(day))
	f.orchard = item(f.movies, "movie", nil, "Glass Orchard", nil, nil)
	file(f.orchard, f.movies, "/media/Movies/Glass Orchard/Glass Orchard.mkv", 50, nil)
	f.gone = item(f.movies, "movie", nil, "Vanished Pier", 1999, ago(day))
	f.salt = item(f.docs, "movie", nil, "Salt Flats", 2012, nil)
	file(f.salt, f.docs, "/media/Docs/Salt Flats.mkv", 70, nil)
	f.show = item(f.tv, "show", nil, "Tidewater", 2019, nil)
	f.season = item(f.tv, "season", f.show, "Season 1", nil, nil)
	f.ep1 = item(f.tv, "episode", f.season, "Arrival", nil, nil)
	f.ep2 = item(f.tv, "episode", f.season, "Departure", nil, nil)
	f.epGone = item(f.tv, "episode", f.season, "Lost Reel", nil, ago(day))
	file(f.ep1, f.tv, "/media/TV/Tidewater/Season 01/Tidewater S01E01.mkv", 10, nil)
	file(f.ep2, f.tv, "/media/TV/Tidewater/Season 01/Tidewater S01E02.mkv", 20, nil)
	file(f.epGone, f.tv, "/media/TV/Tidewater/Season 01/Tidewater S01E03.mkv", 30, ago(day))

	user := func(name string) int64 {
		return exec(`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES (?, 'x', 0, 0)`, name)
	}
	ana, ben := user("ana"), user("ben")
	watch := func(u, itemID int64, played, count int, last time.Duration) {
		exec(`INSERT INTO watch_state (user_id, item_id, played, play_count, last_played_at, updated_at) VALUES (?, ?, ?, ?, ?, 0)`,
			u, itemID, played, count, ago(last), ago(last))
	}
	watch(ana, f.lantern, 1, 2, 40*day)
	watch(ben, f.lantern, 0, 0, 10*day)
	watch(ana, f.ep1, 1, 1, 6*day)
	watch(ana, f.ep2, 1, 1, 5*day)
	watch(ben, f.ep1, 1, 1, 7*day)
	watch(ana, f.salt, 1, 1, 100*day)

	n := 0
	session := func(u int64, name string, itemID int64, kind, decision string, played int64, start time.Duration, ended bool) {
		n++
		var endedAt any
		if ended {
			endedAt = ago(start) + played
		}
		exec(`INSERT INTO playback_sessions (id, user_id, username, item_id, item_title, item_kind, device_name, decision, decision_reason,
				state, last_position_ms, duration_ms, played_ms, started_at, last_event_at, ended_at)
			VALUES (?, ?, ?, ?, 'snapshot', ?, 'Den TV', ?, 'test', 'stopped', ?, 6000000, ?, ?, ?, ?)`,
			"s"+string(rune('a'+n)), u, name, itemID, kind, decision, played, played, ago(start), ago(start)+played, endedAt)
	}
	session(ana, "ana", f.lantern, "movie", "direct_play", 5_800_000, 40*day, true)
	session(ben, "ben", f.lantern, "movie", "remux", 1_200_000, 10*day, true)
	session(ben, "ben", f.lantern, "movie", "refused", 0, 9*day, true)
	session(ana, "ana", f.lantern, "movie", "direct_play", 60_000, time.Hour, false) // still open
	session(ana, "ana", f.ep1, "episode", "direct_play", 2_400_000, 6*day, true)
	session(ana, "ana", f.ep2, "episode", "direct_play", 2_400_000, 5*day, true)
	session(ben, "ben", f.ep1, "episode", "direct_play", 2_400_000, 7*day, true)
	return f
}

func TestItemHistory(t *testing.T) {
	f := newWatchFixture(t)
	ctx := context.Background()

	m, err := f.svc.ItemHistory(ctx, f.lantern, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Two ended plays: the refusal and the open session are not history.
	if m.Kind != "movie" || m.Plays != 2 || m.Users != 2 || m.WatchMs != 5_800_000+1_200_000 || len(m.Sessions) != 2 ||
		*m.Sessions[0].Username != "ben" || *m.Sessions[1].Username != "ana" || !m.Sessions[1].Watched {
		t.Fatalf("movie history: %+v", m)
	}

	s, err := f.svc.ItemHistory(ctx, f.show, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != "show" || s.Plays != 3 || s.Users != 2 || s.WatchMs != 3*2_400_000 || len(s.Sessions) != 3 ||
		*s.Sessions[0].ItemID != f.ep2 || *s.Sessions[0].ShowID != f.show {
		t.Fatalf("show history (its episodes): %+v", s)
	}

	e, err := f.svc.ItemHistory(ctx, f.ep1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The page holds one session; the totals still cover both.
	if e.Plays != 2 || e.Users != 2 || len(e.Sessions) != 1 {
		t.Fatalf("episode history: %+v", e)
	}

	for _, id := range []int64{f.season, 9999} {
		if _, err := f.svc.ItemHistory(ctx, id, 20, 0); !errors.Is(err, ErrNoItem) {
			t.Fatalf("item %d: err = %v, want ErrNoItem", id, err)
		}
	}
}

// export returns the exported ids in order, and the rows by id.
func (f *watchFixture) export(t *testing.T, filter ExportFilter) ([]int64, map[int64]TitleWatch) {
	t.Helper()
	byID := map[int64]TitleWatch{}
	var order []int64
	if err := f.svc.WatchExport(context.Background(), filter, func(x TitleWatch) error {
		byID[x.ItemID] = x
		order = append(order, x.ItemID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return order, byID
}

func TestWatchExport(t *testing.T) {
	f := newWatchFixture(t)
	order, all := f.export(t, ExportFilter{})
	// By library name, then title: Docs, Movies, TV.
	if want := []int64{f.salt, f.orchard, f.lantern, f.show}; !reflect.DeepEqual(order, want) {
		t.Fatalf("exported %v, want %v", order, want)
	}
	if _, ok := all[f.gone]; ok {
		t.Fatal("a title gone from disk was exported")
	}

	ms := func(d time.Duration) *int64 { v := f.now.Add(-d).UnixMilli(); return &v }
	lh := all[f.lantern]
	// The latest play is ana's, still open an hour ago: the activity log counts
	// a session in progress, so a title being watched is never reported as stale.
	wantLast := *ms(time.Hour) + 60_000
	if lh.Kind != "movie" || lh.Library != "Movies" || lh.Year == nil || *lh.Year != 2004 || lh.SizeBytes != 300 ||
		!reflect.DeepEqual(lh.Paths, []string{"/media/Movies/Lantern Hill (2004)/Lantern Hill 1080p.mkv", "/media/Movies/Lantern Hill (2004)/Lantern Hill 2160p.mkv"}) ||
		lh.LastPlayedAt == nil || *lh.LastPlayedAt != wantLast || lh.PlayCount != 2 || !reflect.DeepEqual(lh.FinishedBy, []string{"ana"}) ||
		lh.EpisodesTotal != nil {
		t.Fatalf("Lantern Hill: %+v", lh)
	}

	g := all[f.orchard]
	if g.LastPlayedAt != nil || g.PlayCount != 0 || len(g.FinishedBy) != 0 || g.FinishedBy == nil || g.Year != nil || g.SizeBytes != 50 {
		t.Fatalf("never played: %+v", g)
	}

	sh := all[f.show]
	// Rolled up from the two episodes on disk: ana watched both, ben only one.
	if sh.Kind != "show" || sh.Library != "TV" || sh.EpisodesTotal == nil || *sh.EpisodesTotal != 2 || *sh.EpisodesWatched != 2 ||
		sh.SizeBytes != 30 || len(sh.Paths) != 2 || sh.PlayCount != 3 || !reflect.DeepEqual(sh.FinishedBy, []string{"ana"}) ||
		sh.LastPlayedAt == nil || *sh.LastPlayedAt != *ms(5 * day)+2_400_000 {
		t.Fatalf("show: %+v", sh)
	}

	// Not played in 30 days: the unwatched movie and the old documentary, not
	// the movie or show played this month.
	if order, _ := f.export(t, ExportFilter{UnplayedDays: 30}); !reflect.DeepEqual(order, []int64{f.salt, f.orchard}) {
		t.Fatalf("unplayed 30 days: %v, want Salt Flats and Glass Orchard", order)
	}
	// The show was last played five days ago: a four day window keeps it, a
	// six day window drops it. The movie being watched now is never stale.
	if order, _ := f.export(t, ExportFilter{UnplayedDays: 4}); !reflect.DeepEqual(order, []int64{f.salt, f.orchard, f.show}) {
		t.Fatalf("unplayed 4 days: %v, want all but Lantern Hill", order)
	}
	if order, _ := f.export(t, ExportFilter{UnplayedDays: 6}); !reflect.DeepEqual(order, []int64{f.salt, f.orchard}) {
		t.Fatalf("unplayed 6 days: %v, want Salt Flats and Glass Orchard", order)
	}

	if order, _ := f.export(t, ExportFilter{LibraryID: f.docs}); !reflect.DeepEqual(order, []int64{f.salt}) {
		t.Fatalf("library filter: %v, want only Salt Flats", order)
	}
	if err := f.svc.WatchExport(context.Background(), ExportFilter{LibraryID: 9999}, func(TitleWatch) error { return nil }); !errors.Is(err, ErrNoLibrary) {
		t.Fatalf("unknown library: %v", err)
	}
}
