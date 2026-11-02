package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The restore drill, as docs/INSTALL.md "Restoring a backup" describes it: a backup
// copied in as vcrx.db, with no -wal or -shm beside it, opens and migrates
// forward. The backup here is one taken before a migration, at an older
// schema, as after an upgrade that went wrong.
func TestRestoreDrill(t *testing.T) {
	ctx := context.Background()
	live := t.TempDir()
	conn, err := Open(filepath.Join(live, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := migrateTo(ctx, conn, "", quiet(), 5); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('kept', 'from the backup', 0)`); err != nil {
		t.Fatal(err)
	}
	backup, err := Backup(ctx, conn, filepath.Join(live, "backups"), "pre-migrate-v5")
	if err != nil {
		t.Fatal(err)
	}
	// Written after the backup, so the restore must not have it.
	if _, err := conn.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('later', 'after the backup', 0)`); err != nil {
		t.Fatal(err)
	}

	// Restore into a fresh /config: copy the backup in as vcrx.db.
	restored := t.TempDir()
	raw, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(restored, "vcrx.db"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	rc, err := Open(filepath.Join(restored, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	if v, err := CurrentVersion(ctx, rc); err != nil || v != 5 {
		t.Fatalf("restored copy at version %d, %v; want 5", v, err)
	}
	// Starting the server migrates it forward, copying it to backups first.
	if _, err := Migrate(ctx, rc, filepath.Join(restored, "backups"), quiet()); err != nil {
		t.Fatalf("migrating the restored copy: %v", err)
	}
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := CurrentVersion(ctx, rc); v != len(ms) {
		t.Fatalf("after migrating, version %d; want %d", v, len(ms))
	}
	var n int
	if err := rc.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'kept'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the backed-up row: %d, %v", n, err)
	}
	if err := rc.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'later'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a row written after the backup came back: %d, %v", n, err)
	}
	var check string
	if err := rc.QueryRow(`PRAGMA integrity_check`).Scan(&check); err != nil || check != "ok" {
		t.Fatalf("integrity_check: %q, %v", check, err)
	}
	if left, _ := filepath.Glob(filepath.Join(restored, "backups", "vcrx-pre-migrate-v5-*.db")); len(left) != 1 {
		t.Fatalf("no pre-migration copy of the restored database: %v", left)
	}
}
