package db

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func musicVersion(t *testing.T) int {
	t.Helper()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if strings.HasSuffix(m.name, "_music_library.sql") {
			return m.version
		}
	}
	t.Fatal("no music library migration")
	return 0
}

// The music migration rebuilds libraries to widen its kind CHECK. Everything
// that pointed at a library must survive it: items, grants and sessions keep
// their rows (dropping the old table with foreign keys on would have cascaded
// them away), the foreign keys still cascade afterwards, and a column or index
// another migration added to libraries is kept, since the rebuild copies the
// table's own definition rather than a column list written here.
func TestMusicMigrationRebuildsLibrariesSafely(t *testing.T) {
	dir := t.TempDir()
	conn, err := Open(filepath.Join(dir, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := context.Background()
	mv := musicVersion(t)
	if _, err := migrateTo(ctx, conn, "", quiet(), mv-1); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := conn.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO libraries (id, name, kind, root_path, created_at, updated_at) VALUES (1, 'Movies', 'movie', '/m', 0, 0), (2, 'TV', 'show', '/t', 0, 0)`)
	exec(`INSERT INTO items (id, library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (10, 1, 'movie', 'k', 'Film', 'Film', 0, 0)`)
	exec(`INSERT INTO media_versions (id, item_id, library_id, path, rel_path, size, mtime, added_at, updated_at) VALUES (20, 10, 1, '/m/f.mkv', 'f.mkv', 1, 1, 0, 0)`)
	exec(`INSERT INTO users (id, username, password_hash, created_at, updated_at) VALUES (5, 'friend', 'x', 0, 0)`)
	exec(`INSERT INTO library_grants (user_id, library_id) VALUES (5, 1), (5, 2)`)
	exec(`INSERT INTO scan_runs (library_id, trigger, started_at) VALUES (1, 'cli', 0)`)
	// Stand-ins for what another pull request might add to libraries first.
	exec(`ALTER TABLE libraries ADD COLUMN added_elsewhere TEXT`)
	exec(`UPDATE libraries SET added_elsewhere = 'kept' WHERE id = 1`)
	exec(`CREATE INDEX libraries_added_elsewhere ON libraries (added_elsewhere)`)

	if _, err := Migrate(ctx, conn, filepath.Join(dir, "backups"), quiet()); err != nil {
		t.Fatal(err)
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var n int
		if err := conn.QueryRow(q, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM libraries`:                                2,
		`SELECT COUNT(*) FROM items`:                                    1,
		`SELECT COUNT(*) FROM media_versions`:                           1,
		`SELECT COUNT(*) FROM library_grants`:                           2,
		`SELECT COUNT(*) FROM scan_runs`:                                1,
		`SELECT COUNT(*) FROM libraries WHERE added_elsewhere = 'kept'`: 1,
		`SELECT COUNT(*) FROM sqlite_schema WHERE type = 'index' AND name = 'libraries_added_elsewhere'`: 1,
		`SELECT COUNT(*) FROM sqlite_schema WHERE name LIKE '%__rebuild%'`:                               0,
	} {
		if got := count(q); got != want {
			t.Errorf("%s = %d, want %d", q, got, want)
		}
	}
	var fk int
	if err := conn.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign keys after the migration = %d, %v", fk, err)
	}
	// The new kind is accepted, an unknown one still refused, names still unique.
	exec(`INSERT INTO libraries (id, name, kind, root_path, created_at, updated_at) VALUES (3, 'Music', 'music', '/a', 0, 0)`)
	if _, err := conn.Exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('Books', 'book', '/b', 0, 0)`); err == nil {
		t.Error("kind 'book' accepted")
	}
	if _, err := conn.Exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('movies', 'movie', '/x', 0, 0)`); err == nil {
		t.Error("a duplicate library name accepted")
	}
	// The references still point at the rebuilt table: deleting a library
	// cascades to its items, versions and grants.
	exec(`DELETE FROM libraries WHERE id = 1`)
	for q, want := range map[string]int{
		`SELECT COUNT(*) FROM items`:          0,
		`SELECT COUNT(*) FROM media_versions`: 0,
		`SELECT COUNT(*) FROM library_grants`: 1,
	} {
		if got := count(q); got != want {
			t.Errorf("after deleting the library: %s = %d, want %d", q, got, want)
		}
	}
	if _, err := conn.Exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (99, 'movie', 'k', 't', 't', 0, 0)`); err == nil {
		t.Error("an item in a library that does not exist was accepted")
	}
}

func TestUnknownStepRefused(t *testing.T) {
	m := migration{name: "0099_x.sql", sql: "-- vcrx:step no_such_step\nSELECT 1;"}
	if _, err := m.step(); err == nil || !strings.Contains(err.Error(), "unknown step") {
		t.Fatalf("err = %v", err)
	}
	if fn, err := (migration{name: "0098_y.sql", sql: "SELECT 1;"}).step(); fn != nil || err != nil {
		t.Fatalf("plain migration: %v %v", fn != nil, err)
	}
}

func TestRebuildRefusesAnUnexpectedDefinition(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Exec(`CREATE TABLE t (kind TEXT CHECK (kind IN ('a')))`); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	err = rebuildTable(context.Background(), tx, "t", func(ddl string) (string, error) {
		return replaceOnce(ddl, "CHECK (kind IN ('b'))", "x")
	})
	if err == nil {
		t.Fatal("an edit that matches nothing was accepted")
	}
}
