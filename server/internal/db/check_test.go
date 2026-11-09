package db

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The deploy's dry run: a copy taken from a database its server still has
// open (rows only in the WAL included) migrates to the latest schema and
// passes both checks, and the source is left at its own version.
func TestSnapshotThenCheckMigrations(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "live", "vcrx.db")
	conn, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	old := versionOf(t, "_audiobooks.sql") - 1
	if _, err := migrateTo(ctx, conn, "", quiet(), old); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO users (id, username, password_hash, created_at, updated_at) VALUES (1, 'owner', 'x', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src + "-wal"); err != nil {
		t.Fatalf("want the live database to have a WAL while open: %v", err)
	}

	copied := filepath.Join(dir, "work", "vcrx.db")
	if err := os.MkdirAll(filepath.Dir(copied), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := Snapshot(ctx, src, copied); err != nil {
		t.Fatalf("snapshot of an open database: %v", err)
	}
	from, to, err := CheckMigrations(ctx, copied, quiet())
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if from != old || to != LatestVersion() {
		t.Fatalf("versions %d -> %d, want %d -> %d", from, to, old, LatestVersion())
	}
	cp, err := Open(copied)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cp.Close() }()
	var name string
	if err := cp.QueryRow(`SELECT username FROM users WHERE id = 1`).Scan(&name); err != nil || name != "owner" {
		t.Fatalf("the copy lost a row that was only in the WAL: %q %v", name, err)
	}
	if v, err := CurrentVersion(ctx, conn); err != nil || v != old {
		t.Fatalf("the live database moved to version %d (%v), want it left at %d", v, err, old)
	}

	// A database whose server has stopped (no -wal file left) copies too:
	// the rollback procedure stops the container before deploying.
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src + "-wal"); !os.IsNotExist(err) {
		t.Fatalf("want no WAL after a clean close: %v", err)
	}
	again := filepath.Join(dir, "work", "again.db")
	if err := Snapshot(ctx, src, again); err != nil {
		t.Fatalf("snapshot of a closed database: %v", err)
	}
	if _, _, err := CheckMigrations(ctx, again, quiet()); err != nil {
		t.Fatalf("check of the second copy: %v", err)
	}
}

// The incident this guards against (2026-11-09): a migration that fails on
// real data. A dangling ON DELETE CASCADE reference makes the audiobooks step
// refuse, so the dry run fails and names the row, at the version it started.
func TestCheckMigrationsFailsWhereTheServerWould(t *testing.T) {
	conn, dir := orphanDB(t, func(exec func(string, ...any)) {
		exec(`DELETE FROM users WHERE id = 1`)
	})
	path := filepath.Join(dir, "vcrx.db")
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	from, to, err := CheckMigrations(context.Background(), path, quiet())
	if err == nil || !strings.Contains(err.Error(), "foreign key check failed: auth_sessions") {
		t.Fatalf("err = %v, want the auth_sessions orphan refused", err)
	}
	if want := versionOf(t, "_audiobooks.sql") - 1; from != want || to != want {
		t.Fatalf("versions %d -> %d, want both %d", from, to, want)
	}
}

// Rolling back to an older image without restoring its database would crash
// that server at start; the dry run refuses it first.
func TestCheckMigrationsRefusesANewerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vcrx.db")
	conn, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(context.Background(), conn, "", quiet()); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (9999, 'future.sql', 0)`); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if _, _, err := CheckMigrations(context.Background(), path, quiet()); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("err = %v, want a newer database refused", err)
	}
}

// A copy that never arrived is an error, not an empty database that migrates.
func TestCheckMigrationsNeedsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.db")
	if _, _, err := CheckMigrations(context.Background(), path, quiet()); err == nil {
		t.Fatal("a missing database passed the check")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the check created %s", path)
	}
	if err := Snapshot(context.Background(), path, path+".copy"); err == nil {
		t.Fatal("a missing source copied")
	}
}
