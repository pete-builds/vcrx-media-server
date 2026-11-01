package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
)

func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; _ = r.Close() })
}

// Run over ssh without -t, the CLI sees an empty stdin. It must say so instead of
// reporting a too-short password (seen on 2026-10-30).
func TestReadPasswordEmptyStdin(t *testing.T) {
	withStdin(t, "")
	if _, err := readPassword("Password: "); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadPasswordPiped(t *testing.T) {
	withStdin(t, "correct horse\r\n")
	pw, err := readPassword("Password: ")
	if err != nil || pw != "correct horse" {
		t.Fatalf("got %q, %v", pw, err)
	}
}

func TestWatchExportLines(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := db.Migrate(ctx, conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO libraries (id, name, kind, root_path, created_at, updated_at) VALUES (1, 'Movies', 'movie', '/media/Movies', 0, 0)`,
		`INSERT INTO libraries (id, name, kind, root_path, created_at, updated_at) VALUES (2, 'Docs', 'movie', '/media/Docs', 0, 0)`,
		`INSERT INTO items (library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (1, 'movie', 'a', 'Amber Road', 'Amber Road', 0, 0)`,
		`INSERT INTO items (library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (1, 'movie', 'b', 'Birch Lane', 'Birch Lane', 0, 0)`,
		`INSERT INTO items (library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (2, 'movie', 'c', 'Cold Spring', 'Cold Spring', 0, 0)`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := watchExport(ctx, conn, &out, "movies", 30); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines for Movies, got %q", out.String())
	}
	var row struct {
		Title        string
		LastPlayedAt *int64 `json:"last_played_at"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil || row.Title != "Amber Road" || row.LastPlayedAt != nil {
		t.Fatalf("first line %q: %v", lines[0], err)
	}
	if err := watchExport(ctx, conn, &out, "Nope", 0); err == nil || !strings.Contains(err.Error(), "no library named") {
		t.Fatalf("unknown library: %v", err)
	}
}

// The nightly backup goes by the calendar, not by uptime: a server restarted
// several times a day (deploys) must not fill the week of nightlies with
// copies from one day, and one up for weeks still gets one a day.
func TestNightlyDueByDate(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)
	if !nightlyDue(dir, now) {
		t.Fatal("no backups yet: not due")
	}
	for _, n := range []string{"vcrx-nightly-20261031T230000Z.db", "vcrx-pre-migrate-v18-20261101T040000Z.db"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !nightlyDue(dir, now) {
		t.Fatal("last nightly was yesterday (a pre-migrate copy from today does not count): not due")
	}
	if err := os.WriteFile(filepath.Join(dir, "vcrx-nightly-20261101T001000Z.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if nightlyDue(dir, now) {
		t.Fatal("already one for today: due again")
	}
	if !nightlyDue(dir, now.Add(20*time.Hour)) {
		t.Fatal("next day: not due")
	}
}

// Taking it twice on one day, as a restart would, leaves one copy.
func TestNightlyBackupOncePerDay(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	if _, err := db.Migrate(ctx, conn, "", log); err != nil {
		t.Fatal(err)
	}
	backups := filepath.Join(dir, "backups")
	for i := 0; i < 2; i++ {
		if i > 0 {
			time.Sleep(1100 * time.Millisecond) // backup names carry the second
		}
		maybeNightlyBackup(ctx, log, conn, backups, time.Now())
	}
	got, _ := filepath.Glob(filepath.Join(backups, "vcrx-nightly-*.db"))
	if len(got) != 1 {
		t.Fatalf("nightlies after two runs on one day = %v, want 1", got)
	}
}
