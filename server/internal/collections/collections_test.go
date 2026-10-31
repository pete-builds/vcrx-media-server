package collections

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
)

func TestFranchiseCollections(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := db.Migrate(context.Background(), conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO libraries (id, name, kind, root_path, created_at, updated_at) VALUES (1, 'Movies', 'movie', '/m', 0, 0), (2, 'TV', 'show', '/t', 0, 0)`)
	movie := func(id int64, title string, year int, coll any) {
		exec(`INSERT INTO items (id, library_id, kind, group_key, title, sort_title, year, tmdb_collection_id, added_at, updated_at)
			VALUES (?, 1, 'movie', ?, ?, ?, ?, ?, 0, 0)`, id, title, title, title, year, coll)
	}
	movie(1, "Part Two", 2004, 10)
	movie(2, "Part One", 2001, 10)
	movie(3, "Lonely Sequel", 2010, 20) // only one of its series here
	movie(4, "Standalone", 2012, nil)
	movie(5, "Other A", 1990, 30)
	movie(6, "Other B", 1992, 30)

	names := map[int64]string{10: "The Series Collection", 30: "Other Collection"}
	calls := 0
	b := &Builder{DB: conn, Names: func(ctx context.Context, id int64) (string, error) {
		calls++
		if n, ok := names[id]; ok {
			return n, nil
		}
		return "", errors.New("unknown")
	}}
	res, err := b.Build(context.Background())
	if err != nil || res.Collections != 2 || res.Named != 2 {
		t.Fatalf("first build: %+v %v", res, err)
	}
	var name, sortT string
	if err := conn.QueryRow(`SELECT name, sort_title FROM collections WHERE slug = 'tmdb-10'`).Scan(&name, &sortT); err != nil || name != "The Series Collection" || sortT != "Series Collection" {
		t.Fatalf("collection 10: %q %q %v", name, sortT, err)
	}
	// Members in release order.
	got, err := ids(context.Background(), conn, `SELECT ci.item_id FROM collection_items ci JOIN collections c ON c.id = ci.collection_id
		WHERE c.slug = 'tmdb-10' ORDER BY ci.position`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 2 || got[1] != 1 {
		t.Fatalf("members %v, want [2 1] (2001 before 2004)", got)
	}
	// A second build reuses the names and asks TMDb nothing.
	if _, err := b.Build(context.Background()); err != nil || calls != 2 {
		t.Fatalf("second build asked TMDb again: %d calls, %v", calls, err)
	}
	// A film leaving drops its series below two: the collection goes.
	exec(`UPDATE items SET missing_since = 1 WHERE id = 6`)
	res, err = b.Build(context.Background())
	if err != nil || res.Removed != 1 || res.Collections != 1 {
		t.Fatalf("after a removal: %+v %v", res, err)
	}
	// Without a TMDb key, an unnamed franchise waits instead of getting a made-up name.
	movie(7, "Third", 2020, 40)
	movie(8, "Fourth", 2021, 40)
	res, err = (&Builder{DB: conn}).Build(context.Background())
	if err != nil || res.Waiting != 1 || res.Collections != 1 {
		t.Fatalf("no namer: %+v %v", res, err)
	}
}
