package api

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/enrich"
	"github.com/pete-builds/vcrx-media-server/server/internal/tmdb"
)

func TestFixMatchAPI(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	// TMDb is off: the routes say so.
	if r := f.do("GET", "/api/v1/admin/metadata/unmatched", f.adminTok, nil); r.code != 404 || !strings.Contains(string(r.body), "tmdb_off") {
		t.Fatalf("without TMDb: %d %s", r.code, r.body)
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/movie":
			_, _ = io.WriteString(w, `{"results":[{"id":11,"title":"The Film","release_date":"2001-01-01","overview":"Right one."},
				{"id":12,"title":"The Film","release_date":"1980-01-01","overview":"Older one."}]}`)
		case "/movie/11":
			_, _ = io.WriteString(w, `{"id":11,"title":"The Film","overview":"Picked by hand.","release_date":"2001-01-01"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer fake.Close()
	c := tmdb.New("v3key", "en-US")
	c.BaseURL, c.ImageURL, c.Interval = fake.URL, fake.URL+"/img", 0
	f.srv.Metadata = &enrich.Enricher{DB: f.db, TMDb: c, Dir: filepath.Join(t.TempDir(), "meta"), Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	// An unmatched file has nothing but its name; values an NFO or an admin
	// set are never replaced, by enrichment or by Fix Match.
	if _, err := f.db.Exec(`UPDATE items SET overview = NULL WHERE id = ?`, f.movieID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/admin/items/%d/match", f.movieID)
	if r := f.do("GET", path, f.friendTok, nil); r.code != 403 {
		t.Fatalf("non-admin search: %d", r.code)
	}
	var found struct {
		Choices []enrich.Choice
	}
	r := f.do("GET", path+"?query=The%20Film", f.adminTok, nil)
	r.json(t, &found)
	if r.code != 200 || len(found.Choices) != 2 || found.Choices[0].TMDbID != 11 || found.Choices[1].Year != 1980 {
		t.Fatalf("search: %d %s", r.code, r.body)
	}
	if r := f.do("PUT", path, f.adminTok, map[string]any{"tmdb_id": 99}); r.code != 422 {
		t.Fatalf("an id TMDb does not have: %d %s", r.code, r.body)
	}
	if r := f.do("PUT", path, f.adminTok, map[string]any{"tmdb_id": 11}); r.code != 200 {
		t.Fatalf("fix: %d %s", r.code, r.body)
	}
	var d struct{ Overview string }
	f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), f.adminTok, nil).json(t, &d)
	if d.Overview != "Picked by hand." {
		t.Fatalf("item after fix: %q", d.Overview)
	}
	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/items/%d/match", f.episodeID), f.adminTok, map[string]any{"tmdb_id": 11}); r.code != 400 {
		t.Fatalf("fixing an episode: %d", r.code)
	}
}
