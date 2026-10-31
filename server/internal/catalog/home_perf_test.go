package catalog

import (
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"
)

func envOn(name string) bool { v := os.Getenv(name); return v != "" && v != "0" }

// seedLarge fills the fixture with 5,000 films, 500 shows of 20 episodes,
// 20,000 plays over two years (a fifth of them this week), tags, ratings,
// collections and the friend's watch history. Deterministic.
func seedLarge(t *testing.T, f *homeFx) {
	t.Helper()
	r := rand.New(rand.NewPCG(1, 2))
	tx, err := f.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	ex := func(q string, args ...any) int64 {
		res, err := tx.Exec(q, args...)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	genres := []string{"Drama", "Comedy", "Action", "Thriller", "Horror", "Romance", "Science Fiction", "Adventure", "Crime", "Mystery",
		"Family", "Animation", "Fantasy", "Documentary", "War", "Western", "Music", "History", "Sport", "Biography"}
	ex(`INSERT INTO labels (name) VALUES ('secret')`)
	words := []string{"a lighthouse", "the city", "a family", "Christmas Eve", "a haunted house", "the border", "a small town", "the sea", "an old friend", "the war"}
	tag := func(id int64) {
		for k, g := range pick(r, genres, 1+r.IntN(3)) {
			ex(`INSERT INTO item_tags (item_id, kind, value, position) VALUES (?, 'genre', ?, ?)`, id, g, k)
		}
		ex(`INSERT INTO item_tags (item_id, kind, value) VALUES (?, 'director', ?)`, id, fmt.Sprintf("Director %d", r.IntN(1500)))
		for k := range 6 {
			ex(`INSERT OR IGNORE INTO item_tags (item_id, kind, value, position) VALUES (?, 'actor', ?, ?)`, id, fmt.Sprintf("Actor %d", r.IntN(3000)), k)
		}
		if r.IntN(10) < 9 {
			ex(`INSERT INTO item_ratings (item_id, source, value, votes) VALUES (?, 'imdb', ?, ?)`, id, 4+r.Float64()*5, r.IntN(500000))
		}
		if r.IntN(2) == 0 {
			ex(`INSERT INTO item_ratings (item_id, source, value, votes) VALUES (?, 'tmdb', ?, ?)`, id, 4+r.Float64()*5, r.IntN(20000))
		}
		if r.IntN(50) == 0 {
			ex(`INSERT INTO item_labels (item_id, label_id) VALUES (?, 1)`, id)
		}
	}
	n := 0
	version := func(item, lib int64, res string, at int64) {
		n++
		ex(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, resolution, duration_ms, probe_status, added_at, updated_at)
			VALUES (?, ?, ?, ?, 1, 0, ?, 6000000, 'ok', ?, ?)`, item, lib, fmt.Sprintf("/p/%d.mkv", n), fmt.Sprintf("%d.mkv", n), res, at, at)
	}
	now := f.now
	var movieIDs, episodeIDs []int64
	for i := range 5000 {
		at := now.Add(-time.Duration(r.IntN(3*365*24)) * time.Hour).UnixMilli()
		year := 1950 + r.IntN(76)
		id := ex(`INSERT INTO items (library_id, kind, group_key, title, sort_title, year, overview, premiere_date, tmdb_id, tmdb_collection_id, added_at, updated_at)
			VALUES (?, 'movie', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, f.movies, fmt.Sprint(i), fmt.Sprintf("Film %d", i), fmt.Sprintf("Film %d", i), year,
			"A story about "+words[r.IntN(len(words))]+".", fmt.Sprintf("%d-%02d-%02d", year, 1+r.IntN(12), 1+r.IntN(28)), 10000+i,
			nullIf(r.IntN(10) != 0, int64(r.IntN(300))), at, at)
		res := "1080p"
		if r.IntN(10) == 0 {
			res = "4K"
		}
		version(id, f.movies, res, at)
		tag(id)
		movieIDs = append(movieIDs, id)
	}
	for i := range 500 {
		at := now.Add(-time.Duration(r.IntN(3*365*24)) * time.Hour).UnixMilli()
		sh := ex(`INSERT INTO items (library_id, kind, group_key, title, sort_title, year, tmdb_id, added_at, updated_at) VALUES (?, 'show', ?, ?, ?, ?, ?, ?, ?)`,
			f.tv, fmt.Sprint(i), fmt.Sprintf("Show %d", i), fmt.Sprintf("Show %d", i), 1980+r.IntN(46), 50000+i, at, at)
		tag(sh)
		for s := 1; s <= 2; s++ {
			sat := at + int64(s-1)*int64(r.IntN(400))*86400000
			if sat > now.UnixMilli() {
				sat = now.UnixMilli() - int64(r.IntN(30))*86400000
			}
			se := ex(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, added_at, updated_at)
				VALUES (?, 'season', ?, ?, ?, ?, ?, ?, ?)`, f.tv, sh, fmt.Sprint(s), fmt.Sprintf("Season %d", s), fmt.Sprintf("Season %d", s), s, sat, sat)
			for e := 1; e <= 10; e++ {
				ep := ex(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, episode_number, added_at, updated_at)
					VALUES (?, 'episode', ?, ?, ?, ?, ?, ?, ?, ?)`, f.tv, se, fmt.Sprint(e), fmt.Sprintf("Episode %d", e), fmt.Sprintf("Episode %d", e), s, e, sat, sat)
				version(ep, f.tv, "1080p", sat)
				episodeIDs = append(episodeIDs, ep)
			}
		}
	}
	users := []int64{f.adminID, f.friendID, f.kidID}
	for i := range 20000 {
		ago := time.Duration(r.IntN(2*365*24)) * time.Hour
		if i%5 == 0 {
			ago = time.Duration(r.IntN(7*24)) * time.Hour
		}
		item := movieIDs[r.IntN(len(movieIDs))]
		if r.IntN(2) == 0 {
			item = episodeIDs[r.IntN(len(episodeIDs))]
		}
		at := now.Add(-ago).UnixMilli()
		ex(`INSERT INTO playback_sessions (id, user_id, item_id, decision, decision_reason, state, played_ms, started_at, last_event_at, ended_at)
			VALUES (?, ?, ?, 'direct_play', 'perf', 'stopped', ?, ?, ?, ?)`, fmt.Sprintf("p%d", i), users[r.IntN(len(users))], item, r.IntN(7200000), at, at, at)
	}
	for _, id := range pick(r, append(append([]int64{}, movieIDs...), episodeIDs...), 1500) {
		at := now.Add(-time.Duration(r.IntN(2*365*24)) * time.Hour).UnixMilli()
		ex(`INSERT INTO watch_state (user_id, item_id, played, play_count, position_ms, last_played_at, updated_at) VALUES (?, ?, ?, 1, ?, ?, ?)`,
			f.friendID, id, r.IntN(4) > 0, r.IntN(2)*600000, at, at)
	}
	for c := range 100 {
		col := ex(`INSERT INTO collections (library_id, slug, name, sort_title, source, visible_home, updated_at) VALUES (?, ?, ?, ?, 'auto', ?, 0)`,
			f.movies, fmt.Sprintf("c%d", c), fmt.Sprintf("Collection %d", c), fmt.Sprintf("Collection %d", c), c < 10)
		for k, id := range pick(r, movieIDs, 4+r.IntN(8)) {
			ex(`INSERT INTO collection_items (collection_id, item_id, position) VALUES (?, ?, ?)`, col, id, k)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func pick[T any](r *rand.Rand, from []T, n int) []T {
	idx := r.Perm(len(from))[:min(n, len(from))]
	out := make([]T, len(idx))
	for i, k := range idx {
		out[i] = from[k]
	}
	return out
}

func nullIf(null bool, v int64) any {
	if null {
		return nil
	}
	return v
}
