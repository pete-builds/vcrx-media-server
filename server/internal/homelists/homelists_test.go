package homelists

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/tmdb"
)

type fakeTMDb struct {
	trending, popular []tmdb.ListEntry
	err               error
}

func (f *fakeTMDb) Trending(context.Context) ([]tmdb.ListEntry, error) { return f.trending, f.err }
func (f *fakeTMDb) Popular(context.Context) ([]tmdb.ListEntry, error)  { return f.popular, f.err }

// Lists are saved when TMDb answers, and an outage or an empty answer keeps
// the last good copy, so Home's TMDb rows survive the network going away.
func TestRefreshKeepsTheLastGoodList(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := db.Migrate(context.Background(), conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	src := &fakeTMDb{trending: []tmdb.ListEntry{{Kind: "movie", ID: 7}, {Kind: "show", ID: 9}}, popular: []tmdb.ListEntry{{Kind: "movie", ID: 3}}}
	r := &Refresher{DB: conn, TMDb: src, Now: func() time.Time { return time.UnixMilli(1000) }}
	ctx := context.Background()
	if err := r.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	read := func(kind string) catalog.OnlineList {
		var raw string
		if err := conn.QueryRow(`SELECT value FROM settings WHERE key = ?`, catalog.OnlineListKey(kind)).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var l catalog.OnlineList
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			t.Fatal(err)
		}
		return l
	}
	if l := read("tmdb_trending"); len(l.Items) != 2 || l.Items[1] != (catalog.OnlineListItem{Kind: "show", TMDbID: 9}) || l.FetchedAt != 1000 {
		t.Fatalf("trending = %+v", l)
	}
	src.err = errors.New("network unreachable")
	if err := r.Refresh(ctx); err == nil {
		t.Fatal("an outage was not reported")
	}
	if l := read("tmdb_trending"); len(l.Items) != 2 {
		t.Fatalf("an outage replaced the list: %+v", l)
	}
	src.err, src.popular = nil, nil
	if err := r.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if l := read("tmdb_popular"); len(l.Items) != 1 {
		t.Fatalf("an empty answer replaced the list: %+v", l)
	}
}
