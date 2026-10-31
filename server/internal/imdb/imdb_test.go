package imdb

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
)

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return b.Bytes()
}

func TestSyncWritesRatingsAndHonoursNotModified(t *testing.T) {
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
	exec(`INSERT INTO libraries (id, name, kind, root_path, created_at, updated_at) VALUES (1, 'M', 'movie', '/m', 0, 0)`)
	exec(`INSERT INTO items (id, library_id, kind, group_key, title, sort_title, imdb_id, added_at, updated_at) VALUES
		(1, 1, 'movie', 'a', 'A', 'A', 'tt0000001', 0, 0), (2, 1, 'movie', 'b', 'B', 'B', 'tt0000002', 0, 0),
		(3, 1, 'movie', 'c', 'C', 'C', NULL, 0, 0), (4, 1, 'movie', 'd', 'D', 'D', 'tt0000009', 0, 0)`)
	// A stale rating for a title IMDb no longer lists is removed.
	exec(`INSERT INTO item_ratings (item_id, source, value, votes) VALUES (4, 'imdb', 5.0, 10)`)

	body := gz(t, "tconst\taverageRating\tnumVotes\ntt0000001\t8.4\t120345\ntt0000002\t6.1\t900\ntt9999999\t9.9\t5\n")
	var hits, notModified int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("If-Modified-Since") == "Wed, 28 Oct 2026 00:00:00 GMT" {
			notModified++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Last-Modified", "Wed, 28 Oct 2026 00:00:00 GMT")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	s := &Syncer{DB: conn, URL: srv.URL}
	res, err := s.Sync(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Wanted != 3 || res.Updated != 2 || res.Removed != 1 {
		t.Fatalf("result %+v", res)
	}
	var v float64
	var votes int64
	if err := conn.QueryRow(`SELECT value, votes FROM item_ratings WHERE item_id = 1 AND source = 'imdb'`).Scan(&v, &votes); err != nil || v != 8.4 || votes != 120345 {
		t.Fatalf("rating for item 1: %v %v %v", v, votes, err)
	}
	var n int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM item_ratings WHERE item_id = 4`).Scan(&n)
	if n != 0 {
		t.Fatal("stale rating kept")
	}
	res, err = s.Sync(context.Background(), false)
	if err != nil || !res.NotModified || notModified != 1 {
		t.Fatalf("second sync should send If-Modified-Since and stop at 304: %+v %v", res, err)
	}
	if _, err := s.Sync(context.Background(), true); err != nil || hits != 3 {
		t.Fatalf("forced sync: hits=%d err=%v", hits, err)
	}
}

func TestParseRejectsWrongFile(t *testing.T) {
	if _, err := parse(bytes.NewReader(gz(t, "<html>blocked</html>\n")), map[string][]int64{"tt1": {1}}); err == nil {
		t.Fatal("a file without the header must be refused")
	}
	if _, err := parse(bytes.NewReader([]byte("plain text")), nil); err == nil {
		t.Fatal("a non-gzip body must be refused")
	}
}
