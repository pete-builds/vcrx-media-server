package db

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestMigrateFreshAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	conn, err := Open(filepath.Join(dir, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := context.Background()
	n, err := Migrate(ctx, conn, filepath.Join(dir, "backups"), quiet())
	if err != nil || n != LatestVersion() {
		t.Fatalf("first migrate: n=%d err=%v", n, err)
	}
	n, err = Migrate(ctx, conn, filepath.Join(dir, "backups"), quiet())
	if err != nil || n != 0 {
		t.Fatalf("second migrate: n=%d err=%v", n, err)
	}
	var mode string
	if err := conn.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
	var fk int
	if err := conn.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d, %v", fk, err)
	}
	// A fresh database needs no backup.
	if entries, _ := os.ReadDir(filepath.Join(dir, "backups")); len(entries) != 0 {
		t.Fatalf("unexpected backups: %v", entries)
	}
}

func TestRefusesNewerSchema(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := context.Background()
	if _, err := Migrate(ctx, conn, "", quiet()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (999, 'future.sql', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(ctx, conn, "", quiet()); err == nil || !strings.Contains(err.Error(), "newer database") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestBackup(t *testing.T) {
	dir := t.TempDir()
	conn, err := Open(filepath.Join(dir, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := Migrate(context.Background(), conn, "", quiet()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('k', 'v', 0)`); err != nil {
		t.Fatal(err)
	}
	dest, err := Backup(context.Background(), conn, filepath.Join(dir, "backups"), "test")
	if err != nil {
		t.Fatal(err)
	}
	copyConn, err := Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyConn.Close() }()
	var v string
	if err := copyConn.QueryRow(`SELECT value FROM settings WHERE key = 'k'`).Scan(&v); err != nil || v != "v" {
		t.Fatalf("backup content: %q, %v", v, err)
	}
}

func TestUniqueItemIdentityWithNullParent(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := Migrate(context.Background(), conn, "", quiet()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO libraries (id, name, kind, root_path, created_at, updated_at) VALUES (1, 'M', 'movie', '/m', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	ins := `INSERT INTO items (library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (1, 'movie', 'k', 't', 't', 0, 0)`
	if _, err := conn.Exec(ins); err != nil {
		t.Fatal(err)
	}
	// SQLite treats NULLs as distinct in UNIQUE; the expression index must not.
	if _, err := conn.Exec(ins); err == nil {
		t.Fatal("duplicate top-level item accepted")
	}
}

// Migration 2 backfills added_at from file mtimes, and a migration over existing
// data writes a backup first.
func TestMigration2BackfillsAddedAt(t *testing.T) {
	dir := t.TempDir()
	conn, err := Open(filepath.Join(dir, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := context.Background()
	if _, err := migrateTo(ctx, conn, "", quiet(), 1); err != nil {
		t.Fatal(err)
	}
	const scan, early, later = 5_000_000, 1_000_000, 2_000_000
	for _, q := range []string{
		`INSERT INTO libraries (id, name, kind, root_path, created_at, updated_at) VALUES (1, 'TV', 'show', '/tv', 0, 0)`,
		`INSERT INTO items (id, library_id, kind, parent_id, group_key, title, sort_title, added_at, updated_at) VALUES
			(1, 1, 'show', NULL, 's', 'Show', 'Show', 5000000, 0),
			(2, 1, 'season', 1, '1', 'Season 1', 'Season 1', 5000000, 0),
			(3, 1, 'episode', 2, '1', 'One', 'One', 5000000, 0),
			(4, 1, 'episode', 2, '2', 'Two', 'Two', 5000000, 0)`,
		`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, added_at, updated_at) VALUES
			(3, 1, '/tv/a.mkv', 'a.mkv', 1, 2000000, 5000000, 0),
			(3, 1, '/tv/b.mkv', 'b.mkv', 1, 9000000, 5000000, 0),
			(4, 1, '/tv/c.mkv', 'c.mkv', 1, 1000000, 5000000, 0)`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	n, err := Migrate(ctx, conn, filepath.Join(dir, "backups"), quiet())
	if err != nil || n != LatestVersion()-1 {
		t.Fatalf("migrate: n=%d err=%v", n, err)
	}
	want := map[int64]int64{1: early, 2: early, 3: later, 4: early}
	for id, w := range want {
		var got int64
		if err := conn.QueryRow(`SELECT added_at FROM items WHERE id = ?`, id).Scan(&got); err != nil || got != w {
			t.Errorf("item %d added_at = %d (%v), want %d", id, got, err, w)
		}
	}
	// A version whose mtime is later than its scan keeps the scan time.
	var b int64
	if err := conn.QueryRow(`SELECT added_at FROM media_versions WHERE rel_path = 'b.mkv'`).Scan(&b); err != nil || b != scan {
		t.Errorf("future mtime: added_at = %d, %v", b, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "backups"))
	if len(entries) != 1 || !strings.Contains(entries[0].Name(), "pre-migrate-v1") {
		t.Fatalf("backups = %v", entries)
	}
}

func TestPruneKeepsTheNewestNightlies(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"vcrx-nightly-20261020T010000Z.db", "vcrx-nightly-20261021T010000Z.db", "vcrx-nightly-20261022T010000Z.db",
		"vcrx-nightly-20261023T010000Z.db", "vcrx-pre-migrate-v4-20261001T010000Z.db",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := Prune(dir, "nightly", 2)
	if err != nil || removed != 2 {
		t.Fatalf("removed %d, %v", removed, err)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*.db"))
	want := []string{"vcrx-nightly-20261022T010000Z.db", "vcrx-nightly-20261023T010000Z.db", "vcrx-pre-migrate-v4-20261001T010000Z.db"}
	if len(left) != 3 {
		t.Fatalf("left %v", left)
	}
	for i, w := range want {
		if filepath.Base(left[i]) != w {
			t.Fatalf("left %v, want %v (the oldest nightlies go; pre-migrate backups stay)", left, want)
		}
	}
}

func TestBackupIsAReadableCopy(t *testing.T) {
	dir := t.TempDir()
	conn, err := Open(filepath.Join(dir, "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Exec(`CREATE TABLE t (v TEXT); INSERT INTO t VALUES ('kept')`); err != nil {
		t.Fatal(err)
	}
	path, err := Backup(context.Background(), conn, filepath.Join(dir, "backups"), "nightly")
	if err != nil {
		t.Fatal(err)
	}
	cp, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cp.Close() }()
	var v string
	if err := cp.QueryRow(`SELECT v FROM t`).Scan(&v); err != nil || v != "kept" {
		t.Fatalf("copy: %q %v", v, err)
	}
}

// A migration that fails on every start (a crash loop under restart:
// unless-stopped) must not leave one full copy of the database per attempt
// on the disk the live database shares; each attempt replaces the last copy
// for that schema version. Copies for other versions are kept.
func TestFailingMigrationKeepsOneBackupPerVersion(t *testing.T) {
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	conn, err := Open(filepath.Join(dir, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	ctx := context.Background()
	if _, err := migrateTo(ctx, conn, "", quiet(), 2); err != nil {
		t.Fatal(err)
	}
	// Migration 3 creates tmdb_state, so it fails from here on.
	if _, err := conn.Exec(`CREATE TABLE tmdb_state (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(backups, "vcrx-pre-migrate-v1-20261001T010000Z.db")
	if err := os.MkdirAll(backups, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if i > 0 {
			time.Sleep(1100 * time.Millisecond) // backup names carry the second
		}
		if _, err := Migrate(ctx, conn, backups, quiet()); err == nil || !strings.Contains(err.Error(), "tmdb_state") {
			t.Fatalf("attempt %d: err = %v, want the migration 3 failure", i+1, err)
		}
	}
	v2, _ := filepath.Glob(filepath.Join(backups, "vcrx-pre-migrate-v2-*.db"))
	if len(v2) != 1 {
		t.Fatalf("pre-migrate-v2 copies after 3 failed starts = %d (%v), want 1", len(v2), v2)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("the v1 copy was removed: %v", err)
	}
	copyConn, err := Open(v2[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyConn.Close() }()
	if v, err := CurrentVersion(ctx, copyConn); err != nil || v != 2 {
		t.Fatalf("kept copy is at version %d, %v; want 2", v, err)
	}
}

// A backup is only kept once SQLite has checked the copy: a damaged one must
// not take the place of good copies as the nightlies rotate.
func TestBackupChecksTheCopy(t *testing.T) {
	dir := t.TempDir()
	conn, err := Open(filepath.Join(dir, "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Exec(`CREATE TABLE t (v TEXT); CREATE INDEX t_v ON t (v);
		WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 5000)
		INSERT INTO t SELECT printf('row %d %s', i, hex(randomblob(16))) FROM n`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	backups := filepath.Join(dir, "backups")
	good, err := Backup(ctx, conn, backups, "nightly")
	if err != nil {
		t.Fatal(err)
	}
	if err := integrityCheck(ctx, good); err != nil {
		t.Fatalf("a healthy copy failed the check: %v", err)
	}
	// Control: the check can fail. Overwrite pages past the header of a copy.
	bad := filepath.Join(dir, "bad.db")
	raw, err := os.ReadFile(good)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 16*4096 {
		t.Fatalf("copy is only %d bytes", len(raw))
	}
	for i := 8 * 4096; i < 12*4096; i++ {
		raw[i] = 0xA5
	}
	if err := os.WriteFile(bad, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := integrityCheck(ctx, bad); err == nil {
		t.Fatal("a damaged copy passed the integrity check")
	}
	// A copy that fails is removed and reported, never left as a backup.
	defer func(f func(context.Context, string) error) { checkBackup = f }(checkBackup)
	checkBackup = func(context.Context, string) error { return errors.New("damaged") }
	if _, err := Backup(ctx, conn, filepath.Join(dir, "other"), "nightly"); err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("err = %v, want an integrity failure", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "other", "*.db")); len(left) != 0 {
		t.Fatalf("a copy that failed the check was kept: %v", left)
	}
}
