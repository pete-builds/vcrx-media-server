package subsearch

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/opensubtitles"
)

// The automatic fetch looks for each preferred language once per version,
// and only for versions that arrived after it was turned on: switching it on
// for a whole library would spend months of a free account's 20 a day.
// Nothing found is retried after 6 hours, a day, 4 days and 16 days, because
// subtitles for a new episode often appear a day or two after it airs; then
// it gives up for good.

const autoWatermark = "subtitles.auto_fetch_after_version_id"

// MaxAutoAttempts is how many searches a version gets before giving up.
const MaxAutoAttempts = 4

func backoff(attempts int) time.Duration {
	d := 6 * time.Hour
	for i := 1; i < attempts; i++ {
		d *= 4
	}
	return d
}

// InitAuto records where the automatic fetch starts: versions added from now
// on. It is idempotent; the first call wins.
func (s *Service) InitAuto(ctx context.Context) error {
	var maxID int64
	if err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM media_versions`).Scan(&maxID); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT (key) DO NOTHING`,
		autoWatermark, strconv.FormatInt(maxID, 10), s.now().UnixMilli())
	return err
}

// Kick asks the running loop for a pass soon, after a scan added files.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// RunAuto passes over new versions after every Kick and every interval until
// ctx ends. A pass that cannot reach the provider simply waits for the next.
func (s *Service) RunAuto(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
		case <-t.C:
		}
		if n, err := s.AutoPass(ctx); err != nil {
			s.Log.Info("automatic subtitle fetch paused", "fetched", n, "why", err.Error())
		} else if n > 0 {
			s.Log.Info("automatic subtitle fetch", "fetched", n)
		}
	}
}

type candidate struct {
	version int64
	lang    string
}

// AutoPass fetches the preferred languages for new versions that have no
// full (not forced-only) subtitle in them. It stops at the first sign that
// the provider cannot help right now (offline, rate limited, quota used up)
// and leaves those versions for the next pass without counting an attempt.
func (s *Service) AutoPass(ctx context.Context) (int, error) {
	if !s.CanDownload() {
		return 0, ErrOff
	}
	if q := s.Provider.LastQuota(); q.Known && q.Remaining <= 0 && q.ResetAt.After(s.now()) {
		return 0, &opensubtitles.QuotaError{Quota: q}
	}
	var after int64
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, autoWatermark).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errors.New("automatic fetch not initialised")
	}
	if err != nil {
		return 0, err
	}
	after, _ = strconv.ParseInt(v, 10, 64)

	var todo []candidate
	for _, lang := range s.Languages {
		ids, err := s.missing(ctx, after, lang)
		if err != nil {
			return 0, err
		}
		for _, id := range ids {
			todo = append(todo, candidate{id, lang})
		}
	}

	fetched := 0
	for _, c := range todo {
		if ctx.Err() != nil {
			return fetched, ctx.Err()
		}
		result, err := s.autoOne(ctx, c)
		var link *opensubtitles.LinkError
		if errors.As(err, &link) {
			// The download was counted but the file never came. Remember the
			// try, or every pass would spend another download on it.
			s.Log.Warn("automatic subtitle fetch: download link failed", "version", c.version, "language", c.lang, "err", err)
			if rerr := s.recordAttempt(ctx, c, "link_failed"); rerr != nil {
				return fetched, rerr
			}
			if link.Transient {
				return fetched, err
			}
			continue
		}
		if err != nil {
			if stopsPass(err) {
				return fetched, err
			}
			s.Log.Warn("automatic subtitle fetch failed", "version", c.version, "language", c.lang, "err", err)
			result = "error"
		}
		if result == "downloaded" {
			fetched++
		}
		if err := s.recordAttempt(ctx, c, result); err != nil {
			return fetched, err
		}
	}
	return fetched, nil
}

// missing lists up to 25 versions above the watermark with no full subtitle
// in lang that are due a try.
func (s *Service) missing(ctx context.Context, after int64, lang string) ([]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT v.id FROM media_versions v
		WHERE v.id > ? AND v.missing_since IS NULL AND v.probe_status = 'ok'
		AND NOT EXISTS (SELECT 1 FROM media_streams st WHERE st.version_id = v.id AND st.kind = 'subtitle'
			AND st.language = ? AND st.is_forced = 0)
		AND NOT EXISTS (SELECT 1 FROM subtitle_fetch_attempts a WHERE a.version_id = v.id AND a.language = ?
			AND (a.done = 1 OR a.next_attempt_at > ?))
		ORDER BY v.id LIMIT 25`, after, lang, lang, s.now().UnixMilli())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// stopsPass reports errors that say "not now" rather than "not this file".
func stopsPass(err error) bool {
	var rl *opensubtitles.RateLimitError
	var qe *opensubtitles.QuotaError
	return errors.As(err, &rl) || errors.As(err, &qe) || errors.Is(err, opensubtitles.ErrUnavailable) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// autoOne searches strictly (hash and ids only: a title match is too loose to
// accept unseen) and downloads the best result.
func (s *Service) autoOne(ctx context.Context, c candidate) (string, error) {
	t, err := s.target(ctx, c.version)
	if err != nil {
		return "", err
	}
	hits, err := s.search(ctx, t, s.providerLanguages([]string{c.lang}), false, true)
	if err != nil {
		return "", err
	}
	if len(hits) == 0 {
		return "none", nil
	}
	if _, err := s.download(ctx, c.version, hits[0], 0); err != nil {
		return "", err
	}
	return "downloaded", nil
}

func (s *Service) recordAttempt(ctx context.Context, c candidate, result string) error {
	now := s.now()
	var attempts int
	err := s.DB.QueryRowContext(ctx, `SELECT attempts FROM subtitle_fetch_attempts WHERE version_id = ? AND language = ?`,
		c.version, c.lang).Scan(&attempts)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	attempts++
	done := result == "downloaded" || attempts >= MaxAutoAttempts
	if done && result != "downloaded" {
		result = "gave_up"
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO subtitle_fetch_attempts (version_id, language, attempts, done, last_result, next_attempt_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (version_id, language) DO UPDATE SET attempts = excluded.attempts, done = excluded.done,
			last_result = excluded.last_result, next_attempt_at = excluded.next_attempt_at, updated_at = excluded.updated_at`,
		c.version, c.lang, attempts, done, result, now.Add(backoff(attempts)).UnixMilli(), now.UnixMilli())
	return err
}
