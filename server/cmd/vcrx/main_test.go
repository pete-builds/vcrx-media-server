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
