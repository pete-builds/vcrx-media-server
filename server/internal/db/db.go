// Package db opens the SQLite database and applies the embedded, forward-only
// migrations. It is the only package that knows about the driver.
package db

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// MaxOpenConns bounds the pool. The driver's default pool is unbounded, and SQLite
// allows one writer at a time anyway.
const MaxOpenConns = 8

// Open opens (creating if needed) the database at path with WAL, a busy timeout and
// foreign keys on. It does not migrate; call Migrate.
func Open(path string) (*sql.DB, error) {
	enableOFDLocks()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create database dir: %w", err)
	}
	dsn := "file:" + path + "?" + strings.Join([]string{
		"_pragma=journal_mode(WAL)",
		"_pragma=busy_timeout(10000)",
		"_pragma=foreign_keys(1)",
		"_pragma=synchronous(NORMAL)",
		"_txlock=immediate",
	}, "&")
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(MaxOpenConns)
	conn.SetMaxIdleConns(MaxOpenConns)
	conn.SetConnMaxIdleTime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := conn.PingContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}
	return conn, nil
}

// enableOFDLocks turns on Linux OFD locks, so the scanner opening and closing
// files never strips SQLite's transaction locks. It must run before the first
// connection; later calls with the same value are harmless.
func enableOFDLocks() {
	if runtime.GOOS != "linux" {
		return
	}
	if _, err := sqlite.OFDLocking(true); err != nil && !errors.Is(err, sqlite.ErrOFDLockingTooLate) {
		slog.Warn("sqlite OFD locking unavailable, using POSIX locks", "err", err)
	}
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		name := e.Name()
		prefix, _, ok := strings.Cut(name, "_")
		if !ok || !strings.HasSuffix(name, ".sql") {
			return nil, fmt.Errorf("migration %q: want NNNN_name.sql", name)
		}
		v, err := strconv.Atoi(prefix)
		if err != nil {
			return nil, fmt.Errorf("migration %q: %w", name, err)
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: name, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("migrations must be numbered 1..n without gaps, found %s at position %d", m.name, i+1)
		}
	}
	return out, nil
}

// LatestVersion is the schema version this binary expects.
func LatestVersion() int {
	ms, err := loadMigrations()
	if err != nil {
		return 0
	}
	return len(ms)
}

// CurrentVersion returns the applied schema version, 0 for a fresh database.
func CurrentVersion(ctx context.Context, conn *sql.DB) (int, error) {
	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		return 0, err
	}
	var v sql.NullInt64
	if err := conn.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

// Migrate applies pending migrations, each in its own transaction, so an
// interrupted run resumes where it stopped. When the database already holds data
// it is copied to backupDir first. It refuses a database newer than this binary.
func Migrate(ctx context.Context, conn *sql.DB, backupDir string, log *slog.Logger) (applied int, err error) {
	return migrateTo(ctx, conn, backupDir, log, 0)
}

// migrateTo stops at version target (0 means the latest), so tests can build a
// database at an older schema and exercise a data migration.
func migrateTo(ctx context.Context, conn *sql.DB, backupDir string, log *slog.Logger, target int) (applied int, err error) {
	ms, err := loadMigrations()
	if err != nil {
		return 0, err
	}
	if target > 0 && target < len(ms) {
		ms = ms[:target]
	}
	current, err := CurrentVersion(ctx, conn)
	if err != nil {
		return 0, err
	}
	if current > len(ms) {
		return 0, fmt.Errorf("database schema is version %d but this binary only knows %d: refusing to run an older VCRX against a newer database", current, len(ms))
	}
	if current == len(ms) {
		return 0, nil
	}
	if current > 0 && backupDir != "" {
		dest, err := Backup(ctx, conn, backupDir, fmt.Sprintf("pre-migrate-v%d", current))
		if err != nil {
			return 0, fmt.Errorf("backup before migration: %w", err)
		}
		log.Info("database backed up before migration", "path", dest)
	}
	for _, m := range ms[current:] {
		start := time.Now()
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return applied, err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			_ = tx.Rollback()
			return applied, fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			m.version, m.name, time.Now().UnixMilli()); err != nil {
			_ = tx.Rollback()
			return applied, err
		}
		if err := tx.Commit(); err != nil {
			return applied, fmt.Errorf("migration %s: commit: %w", m.name, err)
		}
		applied++
		log.Info("migration applied", "version", m.version, "name", m.name, "took", time.Since(start).String())
	}
	return applied, nil
}

// Backup writes a consistent copy of the live database into dir with VACUUM INTO
// and returns its path.
func Backup(ctx context.Context, conn *sql.DB, dir, tag string) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, fmt.Sprintf("vcrx-%s-%s.db", tag, time.Now().UTC().Format("20060102T150405Z")))
	if _, err := conn.ExecContext(ctx, `VACUUM INTO ?`, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// NowMs is the timestamp convention used in every table.
func NowMs() int64 { return time.Now().UnixMilli() }
