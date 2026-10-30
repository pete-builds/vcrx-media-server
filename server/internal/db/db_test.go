package db

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
