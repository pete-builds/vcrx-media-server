package api

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestMergeAndSplitAPI(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UnixMilli()
	// The 4K file starts as a movie of its own, which someone watched.
	var lib int64
	if err := f.db.QueryRow(`SELECT library_id FROM items WHERE id = ?`, f.movieID).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	res, err := f.db.Exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, year, added_at, updated_at)
		VALUES (?, 'movie', 'film-4k|2001', 'The Film', 'Film', 2001, ?, ?)`, lib, now, now)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := res.LastInsertId()
	var pete int64
	if err := f.db.QueryRow(`SELECT id FROM users WHERE username = 'pete'`).Scan(&pete); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE media_versions SET item_id = :other WHERE id = :v4k`,
		`INSERT INTO watch_state (user_id, item_id, position_ms, played, play_count, last_version_id, last_played_at, updated_at)
			VALUES (:pete, :other, 0, 1, 1, :v4k, 1000, 1000)`,
		`INSERT INTO playback_sessions (id, user_id, item_id, version_id, decision, decision_reason, started_at, last_event_at)
			VALUES ('h1', :pete, :other, :v4k, 'direct_play', 'test', 1000, 1000)`,
	} {
		if _, err := f.db.Exec(q, sql.Named("other", other), sql.Named("v4k", f.v4k), sql.Named("pete", pete)); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	type detail struct {
		Watch    struct{ Played bool }
		Versions []struct {
			ID         int64
			Resolution string
		}
	}
	get := func(tok string, id int64) (int, detail) {
		t.Helper()
		var d detail
		r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", id), tok, nil)
		if r.code == 200 {
			r.json(t, &d)
		}
		return r.code, d
	}

	mergePath := fmt.Sprintf("/api/v1/admin/items/%d/merge", f.movieID)
	body := map[string]any{"item_ids": []int64{other}}
	if r := f.do("POST", mergePath, f.friendTok, body); r.code != 403 {
		t.Fatalf("a friend merging: %d", r.code)
	}
	if r := f.do("POST", mergePath, f.adminTok, map[string]any{"item_ids": []int64{f.episodeID}}); r.code != 400 {
		t.Fatalf("merging an episode: %d %s", r.code, r.body)
	}
	if r := f.do("POST", mergePath, f.adminTok, body); r.code != 200 {
		t.Fatalf("merge: %d %s", r.code, r.body)
	}
	if code, _ := get(f.adminTok, other); code != 404 {
		t.Fatalf("the merged-away movie still answers: %d", code)
	}
	code, d := get(f.adminTok, f.movieID)
	if code != 200 || len(d.Versions) != 2 || !d.Watch.Played {
		t.Fatalf("admin after merge: %d %+v", code, d)
	}
	var sessionItem int64
	if err := f.db.QueryRow(`SELECT item_id FROM playback_sessions WHERE id = 'h1'`).Scan(&sessionItem); err != nil || sessionItem != f.movieID {
		t.Fatalf("history after merge points at %d (%v)", sessionItem, err)
	}
	// The friend is capped at 1080p: one movie, and only its 1080p version.
	code, d = get(f.friendTok, f.movieID)
	if code != 200 || len(d.Versions) != 1 || d.Versions[0].ID != f.v1080 {
		t.Fatalf("friend after merge: %d %+v", code, d)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/versions/%d/file", f.v4k), f.friendTok, nil); r.code != 404 {
		t.Fatalf("friend streaming the 4K file after merge: %d", r.code)
	}

	// Split the 4K file back out.
	splitPath := fmt.Sprintf("/api/v1/admin/versions/%d/split", f.v4k)
	if r := f.do("POST", splitPath, f.friendTok, nil); r.code != 403 {
		t.Fatalf("a friend splitting: %d", r.code)
	}
	var split struct {
		ItemID int64 `json:"item_id"`
	}
	r := f.do("POST", splitPath, f.adminTok, nil)
	r.json(t, &split)
	if r.code != 200 || split.ItemID == 0 || split.ItemID == f.movieID {
		t.Fatalf("split: %d %s", r.code, r.body)
	}
	code, d = get(f.adminTok, split.ItemID)
	if code != 200 || len(d.Versions) != 1 || d.Versions[0].ID != f.v4k {
		t.Fatalf("admin, split movie: %d %+v", code, d)
	}
	if code, _ := get(f.friendTok, split.ItemID); code != 404 {
		t.Fatalf("friend sees a movie holding only a 4K file: %d", code)
	}
	if code, d := get(f.friendTok, f.movieID); code != 200 || len(d.Versions) != 1 || d.Versions[0].ID != f.v1080 {
		t.Fatalf("friend, original after split: %d %+v", code, d)
	}
	if r := f.do("POST", fmt.Sprintf("/api/v1/admin/versions/%d/split", f.v1080), f.adminTok, nil); r.code != 422 {
		t.Fatalf("splitting the only version: %d %s", r.code, r.body)
	}
	if r := f.do("POST", "/api/v1/admin/versions/99999/split", f.adminTok, nil); r.code != 404 {
		t.Fatalf("splitting no version: %d", r.code)
	}
}
