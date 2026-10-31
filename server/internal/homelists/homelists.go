// Package homelists keeps Home's optional online rows, TMDb Trending and TMDb
// Popular, as saved lists of TMDb ids. They are off unless tmdb_home_lists is
// set, refreshed in the background, and never needed: with no network the
// last good list stays, and with none ever fetched the rows simply do not show.
package homelists

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/tmdb"
)

// Source is the TMDb client, or a fake in tests.
type Source interface {
	Trending(ctx context.Context) ([]tmdb.ListEntry, error)
	Popular(ctx context.Context) ([]tmdb.ListEntry, error)
}

type Refresher struct {
	DB   *sql.DB
	TMDb Source
	Log  *slog.Logger
	Now  func() time.Time
}

func (r *Refresher) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Refresh fetches both lists and saves each one that came back. A list that
// fails keeps its last good copy; the error is returned for the log.
func (r *Refresher) Refresh(ctx context.Context) error {
	var errs []error
	for kind, fetch := range map[string]func(context.Context) ([]tmdb.ListEntry, error){
		"tmdb_trending": r.TMDb.Trending,
		"tmdb_popular":  r.TMDb.Popular,
	} {
		entries, err := fetch(ctx)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(entries) == 0 {
			// An empty answer is more likely an outage than an empty list.
			continue
		}
		l := catalog.OnlineList{FetchedAt: r.now().UnixMilli()}
		for _, e := range entries {
			l.Items = append(l.Items, catalog.OnlineListItem{Kind: e.Kind, TMDbID: e.ID})
		}
		b, err := json.Marshal(l)
		if err != nil {
			return err
		}
		if _, err := r.DB.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			catalog.OnlineListKey(kind), string(b), l.FetchedAt); err != nil {
			return err
		}
	}
	return errors.Join(errs...)
}

// Run refreshes now and then every interval until ctx ends. Failures are
// logged at warn and retried on the next tick.
func (r *Refresher) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if err := r.Refresh(ctx); err != nil && ctx.Err() == nil {
			r.Log.Warn("TMDb Home lists not refreshed; keeping the last good copy", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
