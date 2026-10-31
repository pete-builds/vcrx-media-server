package api

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/activity"
)

type exportBody struct {
	GeneratedAt int64                 `json:"generated_at"`
	Items       []activity.TitleWatch `json:"items"`
	Count       int                   `json:"count"`
}

// play runs one session through start and stop at pos (of 6,000,000 ms).
func (f *fixture) play(tok string, item int64, pos int64) {
	f.t.Helper()
	r := f.do("POST", "/api/v1/playback/sessions", tok, map[string]any{"item_id": item, "capabilities": tclCaps()})
	if r.code != 201 {
		f.t.Fatalf("start: %d %s", r.code, r.body)
	}
	var s struct {
		SessionID string `json:"session_id"`
	}
	r.json(f.t, &s)
	for _, ev := range []struct {
		typ string
		pos int64
	}{{"start", 0}, {"progress", pos}, {"stop", pos}} {
		r := f.do("POST", "/api/v1/playback/sessions/"+s.SessionID+"/events", tok,
			map[string]any{"type": ev.typ, "position_ms": ev.pos, "duration_ms": 6_000_000})
		if r.code != 204 {
			f.t.Fatalf("event %s: %d %s", ev.typ, r.code, r.body)
		}
	}
}

func TestItemHistoryEndpoint(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.play(f.adminTok, f.movieID, 5_700_000) // finished
	f.play(f.friendTok, f.episodeID, 600_000)

	var h activity.ItemHistory
	r := f.do("GET", fmt.Sprintf("/api/v1/admin/items/%d/history", f.movieID), f.adminTok, nil)
	r.json(t, &h)
	if r.code != 200 || h.Kind != "movie" || h.Plays != 1 || h.Users != 1 || len(h.Sessions) != 1 || *h.Sessions[0].Username != "pete" ||
		!h.Sessions[0].Watched || *h.Sessions[0].DeviceName != "Test TV" || h.Sessions[0].Decision != "direct_play" {
		t.Fatalf("movie history: %d %s", r.code, r.body)
	}
	// A show's history is its episodes'.
	r = f.do("GET", fmt.Sprintf("/api/v1/admin/items/%d/history", f.showID), f.adminTok, nil)
	r.json(t, &h)
	if r.code != 200 || h.Kind != "show" || h.Plays != 1 || len(h.Sessions) != 1 || *h.Sessions[0].ItemID != f.episodeID ||
		*h.Sessions[0].Username != "friend" {
		t.Fatalf("show history: %d %s", r.code, r.body)
	}
	r = f.do("GET", fmt.Sprintf("/api/v1/admin/items/%d/history", f.episodeID), f.adminTok, nil)
	r.json(t, &h)
	if r.code != 200 || h.Kind != "episode" || h.Plays != 1 {
		t.Fatalf("episode history: %d %s", r.code, r.body)
	}

	for _, id := range []int64{f.seasonID, 9999} {
		if r := f.do("GET", fmt.Sprintf("/api/v1/admin/items/%d/history", id), f.adminTok, nil); r.code != 404 {
			t.Fatalf("history for item %d: %d, want 404", id, r.code)
		}
	}
	// Admin only, and LAN only like the rest of /admin.
	path := fmt.Sprintf("/api/v1/admin/items/%d/history", f.movieID)
	if r := f.do("GET", path, f.friendTok, nil); r.code != 403 {
		t.Fatalf("friend reading item history: %d, want 403", r.code)
	}
	if r := f.do("GET", path, f.adminTok, nil, "RemoteAddr", "8.8.8.8:5555"); r.code != 403 {
		t.Fatalf("item history from a public IP: %d, want 403", r.code)
	}
}

func TestWatchExportEndpoint(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	now := time.Now().UnixMilli()
	var moviesLib int64
	if err := f.db.QueryRow(`SELECT library_id FROM items WHERE id = ?`, f.movieID).Scan(&moviesLib); err != nil {
		t.Fatal(err)
	}
	res, err := f.db.Exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, year, added_at, updated_at)
		VALUES (?, 'movie', 'meadow|2010', 'Quiet Meadow', 'Quiet Meadow', 2010, ?, ?)`, moviesLib, now, now)
	if err != nil {
		t.Fatal(err)
	}
	meadow, _ := res.LastInsertId()
	if _, err := f.db.Exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, added_at, updated_at)
		VALUES (?, ?, '/media/Movies/Quiet Meadow/meadow.mkv', 'meadow.mkv', 4096, 0, ?, ?)`, meadow, moviesLib, now, now); err != nil {
		t.Fatal(err)
	}
	f.play(f.adminTok, f.movieID, 5_700_000)
	f.play(f.friendTok, f.episodeID, 600_000)

	export := func(query string) (resp, exportBody) {
		t.Helper()
		r := f.do("GET", "/api/v1/admin/watch/export"+query, f.adminTok, nil)
		var b exportBody
		if r.code == 200 {
			r.json(t, &b)
		}
		return r, b
	}
	r, all := export("")
	byID := map[int64]activity.TitleWatch{}
	for _, x := range all.Items {
		byID[x.ItemID] = x
	}
	if r.code != 200 || !strings.HasPrefix(r.hdr.Get("Content-Type"), "application/json") || all.Count != 3 || len(all.Items) != 3 || all.GeneratedAt == 0 {
		t.Fatalf("export: %d %s", r.code, r.body)
	}
	film := byID[f.movieID]
	if film.Kind != "movie" || film.Library != "Movies" || len(film.Paths) != 2 || film.SizeBytes != 2*f.fileSize || film.LastPlayedAt == nil ||
		film.PlayCount != 1 || len(film.FinishedBy) != 1 || film.FinishedBy[0] != "pete" {
		t.Fatalf("The Film: %+v", film)
	}
	if m := byID[meadow]; m.Title != "Quiet Meadow" || m.LastPlayedAt != nil || m.PlayCount != 0 || m.FinishedBy == nil || len(m.FinishedBy) != 0 {
		t.Fatalf("never played: %+v", m)
	}
	// The show is rolled up from its one episode, which the friend has not finished.
	show := byID[f.showID]
	if show.Kind != "show" || show.EpisodesTotal == nil || *show.EpisodesTotal != 1 || *show.EpisodesWatched != 0 || show.LastPlayedAt == nil ||
		len(show.FinishedBy) != 0 || len(show.Paths) != 1 || show.SizeBytes != f.fileSize {
		t.Fatalf("show: %+v", show)
	}
	// The raw JSON carries null for a title never played, not a missing key or 0.
	if !strings.Contains(string(r.body), `"title":"Quiet Meadow","year":2010,`) || !strings.Contains(string(r.body), `"last_played_at":null`) {
		t.Fatalf("raw export: %s", r.body)
	}

	// Everything else was played just now; only the never-played title is stale.
	if _, b := export("?unplayed_days=1"); b.Count != 1 || b.Items[0].ItemID != meadow {
		t.Fatalf("unplayed_days=1: %+v", b)
	}
	if _, b := export("?unplayed_days=0"); b.Count != 3 {
		t.Fatalf("unplayed_days=0 is no filter: %+v", b)
	}
	var tvLib int64
	if err := f.db.QueryRow(`SELECT library_id FROM items WHERE id = ?`, f.showID).Scan(&tvLib); err != nil {
		t.Fatal(err)
	}
	if _, b := export(fmt.Sprintf("?library_id=%d", tvLib)); b.Count != 1 || b.Items[0].ItemID != f.showID {
		t.Fatalf("library_id: %+v", b)
	}
	if _, b := export(fmt.Sprintf("?library_id=%d&unplayed_days=1", tvLib)); b.Count != 0 || b.Items == nil {
		t.Fatalf("an empty export is an empty list: %+v", b)
	}
	for _, q := range []string{"?unplayed_days=-1", "?unplayed_days=soon", "?library_id=abc", "?library_id=0"} {
		if r, _ := export(q); r.code != 400 {
			t.Fatalf("%s: %d, want 400", q, r.code)
		}
	}
	if r, _ := export("?library_id=9999"); r.code != 404 {
		t.Fatalf("unknown library: %d, want 404", r.code)
	}

	if r := f.do("GET", "/api/v1/admin/watch/export", f.friendTok, nil); r.code != 403 {
		t.Fatalf("friend exporting: %d, want 403", r.code)
	}
	if r := f.do("GET", "/api/v1/admin/watch/export", f.adminTok, nil, "RemoteAddr", "8.8.8.8:5555"); r.code != 403 {
		t.Fatalf("export from a public IP: %d, want 403", r.code)
	}
	if r := f.do("GET", "/api/v1/admin/watch/export", "", nil); r.code != 401 {
		t.Fatalf("export signed out: %d, want 401", r.code)
	}
}
