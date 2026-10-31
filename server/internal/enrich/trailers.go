package enrich

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/tmdb"
)

const (
	// TrailerRefresh is how long a found (or confirmed missing) YouTube trailer
	// is kept before TMDb is asked again.
	TrailerRefresh = 30 * 24 * time.Hour
	// TrailerRetry spaces out retries after a failure, such as no internet.
	TrailerRetry = 6 * time.Hour
)

// TrailerStats counts one trailer pass. Skipped counts trailers YouTube said
// no longer play embedded, passed over for the next best.
type TrailerStats struct {
	Found, None, Errors, Skipped int
}

// trailerPass runs RefreshTrailers for Run and logs it. It reports true when
// TMDb rejected the key, which stops enrichment.
func (e *Enricher) trailerPass(ctx context.Context) bool {
	st, err := e.RefreshTrailers(ctx)
	if st.Found+st.None+st.Errors > 0 {
		e.log().Info("tmdb trailer pass", "found", st.Found, "none", st.None, "errors", st.Errors, "skipped", st.Skipped)
	}
	if errors.Is(err, tmdb.ErrUnauthorized) {
		e.log().Error("tmdb enrichment stopped: TMDb rejected the API key; check VCRX_TMDB_API_KEY")
		return true
	}
	if err != nil && ctx.Err() == nil {
		e.log().Warn("tmdb trailer pass did not complete", "err", err)
	}
	return false
}

type trailerCand struct {
	id, tmdbID int64
	kind       string
}

// RefreshTrailers looks up the best official YouTube trailer for matched
// movies and shows that have none on record, are due, or were matched to
// another TMDb title since. It never fails anything when offline: a failed
// lookup is recorded and retried later, and the last good key is kept. Only a
// rejected key or a cancelled context stop it; both are returned.
func (e *Enricher) RefreshTrailers(ctx context.Context) (TrailerStats, error) {
	var st TrailerStats
	batch := e.Batch
	if batch <= 0 {
		batch = defaultBatch
	}
	for {
		now := e.now()
		var cands []trailerCand
		err := each(ctx, e.DB, func(rows *sql.Rows) error {
			var c trailerCand
			if err := rows.Scan(&c.id, &c.kind, &c.tmdbID); err != nil {
				return err
			}
			cands = append(cands, c)
			return nil
		}, `SELECT i.id, i.kind, i.tmdb_id FROM items i LEFT JOIN youtube_trailers t ON t.item_id = i.id
			WHERE i.kind IN ('movie', 'show') AND i.missing_since IS NULL AND i.tmdb_id IS NOT NULL AND (
				t.item_id IS NULL
				OR (t.status = 'error' AND t.checked_at < ?)
				OR (t.status != 'error' AND (t.tmdb_id != i.tmdb_id OR t.checked_at < ?)))
			ORDER BY t.item_id IS NOT NULL, i.added_at DESC LIMIT ?`,
			now.Add(-TrailerRetry).UnixMilli(), now.Add(-TrailerRefresh).UnixMilli(), batch)
		if err != nil {
			return st, err
		}
		for _, c := range cands {
			if err := e.trailer(ctx, c, &st); err != nil {
				return st, err
			}
		}
		if len(cands) < batch {
			return st, nil
		}
	}
}

func (e *Enricher) trailer(ctx context.Context, c trailerCand, st *TrailerStats) error {
	now := e.now().UnixMilli()
	videos, err := e.TMDb.Videos(ctx, c.kind, c.tmdbID)
	switch {
	case errors.Is(err, tmdb.ErrUnauthorized), ctx.Err() != nil:
		if err == nil {
			err = ctx.Err()
		}
		return err
	case errors.Is(err, tmdb.ErrNotFound):
		videos, err = nil, nil
	case err != nil:
		st.Errors++
		e.log().Warn("tmdb trailer lookup failed; keeping what was there", "item", c.id, "err", err)
		// Only the status moves; a key found earlier stays and keeps playing.
		_, werr := e.DB.ExecContext(ctx, `INSERT INTO youtube_trailers (item_id, tmdb_id, status, error, checked_at)
			VALUES (?, ?, 'error', ?, ?)
			ON CONFLICT (item_id) DO UPDATE SET status = 'error', error = excluded.error, checked_at = excluded.checked_at`,
			c.id, c.tmdbID, err.Error(), now)
		return werr
	}
	v, ok, err := e.playableTrailer(ctx, tmdb.Trailers(videos, e.TMDb.Language), st)
	if err != nil {
		return err
	}
	if !ok {
		st.None++
		_, err := e.DB.ExecContext(ctx, `INSERT INTO youtube_trailers (item_id, tmdb_id, status, checked_at) VALUES (?, ?, 'none', ?)
			ON CONFLICT (item_id) DO UPDATE SET tmdb_id = excluded.tmdb_id, youtube_key = NULL, name = NULL, language = NULL,
				published_at = NULL, status = 'none', error = NULL, checked_at = excluded.checked_at`, c.id, c.tmdbID, now)
		return err
	}
	st.Found++
	_, err = e.DB.ExecContext(ctx, `INSERT INTO youtube_trailers (item_id, tmdb_id, youtube_key, name, language, published_at, status, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, 'found', ?)
		ON CONFLICT (item_id) DO UPDATE SET tmdb_id = excluded.tmdb_id, youtube_key = excluded.youtube_key, name = excluded.name,
			language = excluded.language, published_at = excluded.published_at, status = 'found', error = NULL,
			checked_at = excluded.checked_at`, c.id, c.tmdbID, v.Key, v.Name, v.Language, v.PublishedAt, now)
	return err
}

// playableTrailer is the first of ranked that YouTube still embeds. When
// YouTube cannot be asked (no internet, rate limited), the one in hand is
// kept rather than dropped; the next refresh checks it again.
func (e *Enricher) playableTrailer(ctx context.Context, ranked []tmdb.Video, st *TrailerStats) (tmdb.Video, bool, error) {
	for _, v := range ranked {
		if e.Embeddable == nil {
			return v, true, nil
		}
		ok, err := e.Embeddable(ctx, v.Key)
		if ctx.Err() != nil {
			return tmdb.Video{}, false, ctx.Err()
		}
		if err != nil {
			e.log().Warn("youtube check failed; keeping the trailer unchecked", "key", v.Key, "err", err)
			return v, true, nil
		}
		if ok {
			return v, true, nil
		}
		st.Skipped++
	}
	return tmdb.Video{}, false, nil
}
