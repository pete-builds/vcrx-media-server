package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
)

// Snapshot writes a consistent copy of the database at src to dest with
// VACUUM INTO, the way the nightly backup does. src is opened read-only, so a
// running server's database is only read: SQLite's locking gives the copy a
// single point in time while that server goes on writing. dest must not exist.
func Snapshot(ctx context.Context, src, dest string) error {
	enableOFDLocks()
	// mode=ro would refuse a missing file too, but less plainly.
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("copy the database: %w", err)
	}
	conn, err := sql.Open("sqlite", "file:"+src+"?mode=ro&_pragma=busy_timeout(10000)")
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("copy %s: %w", src, err)
	}
	return nil
}

// CheckMigrations is the deploy's dry run. It migrates the database at path,
// which must be a throwaway copy (it is changed in place and no backup is
// taken), then checks the result with PRAGMA integrity_check and PRAGMA
// foreign_key_check. It returns the schema version before and after.
func CheckMigrations(ctx context.Context, path string, log *slog.Logger) (from, to int, err error) {
	// Open creates a missing file, and an empty database migrates cleanly:
	// a copy that never arrived must not pass as a fresh install.
	if _, err := os.Stat(path); err != nil {
		return 0, 0, err
	}
	conn, err := Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = conn.Close() }()
	if from, err = CurrentVersion(ctx, conn); err != nil {
		return 0, 0, err
	}
	n, err := Migrate(ctx, conn, "", log)
	to = from + n
	if err != nil {
		return from, to, err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return from, to, err
	}
	err = foreignKeyCheck(ctx, tx)
	_ = tx.Rollback()
	if err != nil {
		return from, to, err
	}
	if err := conn.Close(); err != nil {
		return from, to, err
	}
	return from, to, integrityCheck(ctx, path)
}
