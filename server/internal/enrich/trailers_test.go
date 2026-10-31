package enrich

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/tmdb"
)

// The /videos answers are the tmdb package's recorded fixtures, served by the
// local fake; nothing here reaches the internet.
func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../tmdb/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTrailersFoundRefreshedAndKeptOffline(t *testing.T) {
	v := setup(t)
	v.f.responses["/movie/100/videos"] = readFixture(t, "movie_videos.json")
	v.f.responses["/tv/300/videos"] = readFixture(t, "tv_videos.json")
	movie := v.item(v.library("movie"), "movie", nil, "lantern keeper|2011", "Lantern Keeper", 2011, nil, nil)
	show := v.item(v.library("show"), "show", nil, "glass orchard", "Glass Orchard", nil, nil, nil)
	unmatched := v.item(1, "movie", nil, "nobody|2000", "Nobody Knows", 2000, nil, nil)
	v.exec(`UPDATE items SET tmdb_id = 100 WHERE id = ?`, movie)
	v.exec(`UPDATE items SET tmdb_id = 300 WHERE id = ?`, show)
	ctx := context.Background()
	key := func(id int64) string {
		return v.str(`SELECT IFNULL(youtube_key, '') || '|' || status || '|' || tmdb_id FROM youtube_trailers WHERE item_id = ?`, id)
	}

	st, err := v.e.RefreshTrailers(ctx)
	if err != nil || st.Found != 2 || st.None != 0 || st.Errors != 0 {
		t.Fatalf("first pass: %+v, %v", st, err)
	}
	if got := key(movie); got != "OffTrlrEn01|found|100" {
		t.Fatalf("movie trailer = %q", got)
	}
	if got := key(show); got != "ShowTeaser1|found|300" {
		t.Fatalf("show trailer = %q", got)
	}
	if got := key(unmatched); got != "" {
		t.Fatalf("an item with no TMDb id was looked up: %q", got)
	}
	// Nothing is due again until TrailerRefresh.
	if st, _ := v.e.RefreshTrailers(ctx); st != (TrailerStats{}) {
		t.Fatalf("second pass looked again: %+v", st)
	}

	// Offline at the next refresh: the lookup fails, nothing else does, and the
	// key found earlier stays.
	base := v.e.TMDb.BaseURL
	v.e.TMDb.BaseURL = "http://127.0.0.1:1"
	v.now = v.now.Add(TrailerRefresh + time.Hour)
	st, err = v.e.RefreshTrailers(ctx)
	if err != nil || st.Errors != 2 {
		t.Fatalf("offline pass: %+v, %v", st, err)
	}
	if got := key(movie); got != "OffTrlrEn01|error|100" {
		t.Fatalf("offline lost the key: %q", got)
	}
	// Retried after TrailerRetry, not on every pass.
	if st, _ := v.e.RefreshTrailers(ctx); st != (TrailerStats{}) {
		t.Fatalf("retried at once: %+v", st)
	}
	v.e.TMDb.BaseURL = base
	v.now = v.now.Add(TrailerRetry + time.Minute)
	if st, err := v.e.RefreshTrailers(ctx); err != nil || st.Found != 2 {
		t.Fatalf("back online: %+v, %v", st, err)
	}

	// Fix Match points the film at another title, which has no videos on TMDb.
	v.exec(`UPDATE items SET tmdb_id = 200 WHERE id = ?`, movie)
	if st, err := v.e.RefreshTrailers(ctx); err != nil || st.None != 1 {
		t.Fatalf("after a re-match: %+v, %v", st, err)
	}
	if got := key(movie); got != "|none|200" {
		t.Fatalf("re-matched film kept the old trailer: %q", got)
	}

	// A rejected key stops the pass and says so.
	v.f.unauth = true
	v.now = v.now.Add(TrailerRefresh + time.Hour)
	if _, err := v.e.RefreshTrailers(ctx); !errors.Is(err, tmdb.ErrUnauthorized) {
		t.Fatalf("unauthorized: %v", err)
	}
}
