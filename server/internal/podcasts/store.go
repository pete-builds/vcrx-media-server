package podcasts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is a show or episode that does not exist, or that the caller
// does not subscribe to or may not see.
var ErrNotFound = errors.New("not found")

// NewWindow is how recent an unplayed, unstarted episode must be to count as new.
const NewWindow = 30 * 24 * time.Hour

// Viewer is who is asking: an account user or a household profile. NoExplicit
// hides shows and episodes marked explicit (a profile under a content rating
// ceiling below R).
type Viewer struct {
	UserID     int64
	NoExplicit bool
}

// Podcast is a show as a subscriber sees it.
type Podcast struct {
	ID                int64   `json:"id"`
	Title             string  `json:"title"`
	Author            string  `json:"author"`
	Description       string  `json:"description"`
	Link              string  `json:"link"`
	FeedURL           string  `json:"feed_url"`
	Explicit          bool    `json:"explicit"`
	HasArtwork        bool    `json:"has_artwork"`
	ArtworkURL        string  `json:"artwork_url,omitempty"`
	EpisodeCount      int     `json:"episode_count"`
	UnplayedCount     int     `json:"unplayed_count"`
	NewCount          int     `json:"new_count"`
	LatestPublishedAt *int64  `json:"latest_published_at"`
	SubscribedAt      int64   `json:"subscribed_at"`
	RefreshedAt       *int64  `json:"refreshed_at"`
	LastError         *string `json:"last_error"`
}

// EpisodeView is an episode with the caller's own state. The enclosure URL
// never leaves the server: clients play stream_url.
type EpisodeView struct {
	ID           int64   `json:"id"`
	PodcastID    int64   `json:"podcast_id"`
	PodcastTitle string  `json:"podcast_title"`
	Title        string  `json:"title"`
	Description  string  `json:"description"`
	PublishedAt  *int64  `json:"published_at"`
	Duration     *int64  `json:"duration_seconds"`
	Season       *int64  `json:"season"`
	Episode      *int64  `json:"episode"`
	EpisodeType  string  `json:"episode_type"`
	Explicit     bool    `json:"explicit"`
	MimeType     string  `json:"mime_type"`
	Size         *int64  `json:"size_bytes"`
	StreamURL    string  `json:"stream_url"`
	ArtworkURL   string  `json:"artwork_url"`
	Position     float64 `json:"position_seconds"`
	Played       bool    `json:"played"`
	New          bool    `json:"new"`
}

// Progress is a caller's place in an episode.
type Progress struct {
	Position float64 `json:"position_seconds"`
	Played   bool    `json:"played"`
	Duration *int64  `json:"duration_seconds"`
}

// Store reads and writes podcasts in the database.
type Store struct {
	DB *sql.DB
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (v Viewer) explicitCond(col string) string {
	if v.NoExplicit {
		return " AND " + col + " = 0"
	}
	return ""
}

// FeedByURL is the stored feed with this URL, if any.
func (s *Store) FeedByURL(ctx context.Context, feedURL string) (int64, bool, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM podcasts WHERE feed_url = ?`, feedURL).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// Explicit reports whether a stored feed is marked explicit.
func (s *Store) Explicit(ctx context.Context, id int64) (bool, error) {
	var e bool
	err := s.DB.QueryRowContext(ctx, `SELECT explicit FROM podcasts WHERE id = ?`, id).Scan(&e)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return e, err
}

// Save stores a freshly read feed, inserting it if it is new, and syncs its
// episodes by guid: new ones are added, known ones updated, and any beyond
// the newest maxEpisodes deleted with every user's place in them.
func (s *Store) Save(ctx context.Context, feedURL string, f *Feed, etag, lastModified string, maxEpisodes int) (int64, error) {
	now := s.now().UnixMilli()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	err = tx.QueryRowContext(ctx, `INSERT INTO podcasts (feed_url, title, author, description, link, language, explicit, image_url,
			new_feed_url, etag, last_modified, refreshed_at, checked_at, last_error, added_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)
		ON CONFLICT (feed_url) DO UPDATE SET title = excluded.title, author = excluded.author, description = excluded.description,
			link = excluded.link, language = excluded.language, explicit = excluded.explicit,
			image_file = CASE WHEN podcasts.image_url = excluded.image_url THEN podcasts.image_file ELSE '' END,
			image_url = excluded.image_url, new_feed_url = excluded.new_feed_url, etag = excluded.etag,
			last_modified = excluded.last_modified, refreshed_at = excluded.refreshed_at, checked_at = excluded.checked_at, last_error = NULL
		RETURNING id`,
		feedURL, f.Title, f.Author, f.Description, f.Link, f.Language, f.Explicit, f.Image, f.NewURL, etag, lastModified, now, now, now).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("save podcast: %w", err)
	}
	for _, e := range f.Episodes {
		var pub, dur, length, season, number any
		if !e.Published.IsZero() {
			pub = e.Published.UnixMilli()
		}
		if e.Duration > 0 {
			dur = e.Duration
		}
		if e.Length > 0 {
			length = e.Length
		}
		if e.Season > 0 {
			season = e.Season
		}
		if e.Number > 0 {
			number = e.Number
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO podcast_episodes (podcast_id, guid, title, description, published_at, duration_seconds,
				enclosure_url, enclosure_type, enclosure_length, image_url, season, episode, episode_type, chapters_url, explicit, added_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (podcast_id, guid) DO UPDATE SET title = excluded.title, description = excluded.description,
				published_at = excluded.published_at, duration_seconds = COALESCE(excluded.duration_seconds, podcast_episodes.duration_seconds),
				enclosure_url = excluded.enclosure_url, enclosure_type = excluded.enclosure_type, enclosure_length = excluded.enclosure_length,
				image_file = CASE WHEN podcast_episodes.image_url = excluded.image_url THEN podcast_episodes.image_file ELSE '' END,
				image_url = excluded.image_url, season = excluded.season, episode = excluded.episode, episode_type = excluded.episode_type,
				chapters_url = excluded.chapters_url, explicit = excluded.explicit`,
			id, e.GUID, e.Title, e.Description, pub, dur, e.URL, e.Type, length, e.Image, season, number, e.EpisodeType, e.ChaptersURL,
			e.Explicit, now); err != nil {
			return 0, fmt.Errorf("save episode: %w", err)
		}
	}
	if maxEpisodes > 0 {
		if _, err := tx.ExecContext(ctx, `DELETE FROM podcast_episodes WHERE podcast_id = ? AND id NOT IN (
				SELECT id FROM podcast_episodes WHERE podcast_id = ? ORDER BY COALESCE(published_at, 0) DESC, id DESC LIMIT ?)`,
			id, id, maxEpisodes); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}

// NotModified records a conditional read the host answered 304.
func (s *Store) NotModified(ctx context.Context, id int64) error {
	now := s.now().UnixMilli()
	_, err := s.DB.ExecContext(ctx, `UPDATE podcasts SET refreshed_at = ?, checked_at = ?, last_error = NULL WHERE id = ?`, now, now, id)
	return err
}

// Failed records why a read failed; the episodes stay as they were.
func (s *Store) Failed(ctx context.Context, id int64, reason string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE podcasts SET checked_at = ?, last_error = ? WHERE id = ?`, s.now().UnixMilli(), clip(reason, 500), id)
	return err
}

// Subscribe adds the subscription; created is false when it was there already.
func (s *Store) Subscribe(ctx context.Context, userID, podcastID int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO podcast_subscriptions (user_id, podcast_id, subscribed_at) VALUES (?, ?, ?)
		ON CONFLICT DO NOTHING`, userID, podcastID, s.now().UnixMilli())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// Unsubscribe removes the subscription and the user's places in its
// episodes. A feed nobody subscribes to any more is deleted; its image files
// are returned for the caller to remove.
func (s *Store) Unsubscribe(ctx context.Context, userID, podcastID int64) ([]string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `DELETE FROM podcast_subscriptions WHERE user_id = ? AND podcast_id = ?`, userID, podcastID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM podcast_episode_state WHERE user_id = ? AND episode_id IN (
			SELECT id FROM podcast_episodes WHERE podcast_id = ?)`, userID, podcastID); err != nil {
		return nil, err
	}
	var left int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM podcast_subscriptions WHERE podcast_id = ?`, podcastID).Scan(&left); err != nil {
		return nil, err
	}
	var files []string
	if left == 0 {
		rows, err := tx.QueryContext(ctx, `SELECT image_file FROM podcasts WHERE id = ? AND image_file != ''
			UNION SELECT image_file FROM podcast_episodes WHERE podcast_id = ? AND image_file != ''`, podcastID, podcastID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var f string
			if err := rows.Scan(&f); err != nil {
				_ = rows.Close()
				return nil, err
			}
			files = append(files, f)
		}
		_ = rows.Close()
		if _, err := tx.ExecContext(ctx, `DELETE FROM podcasts WHERE id = ?`, podcastID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// A file another show still uses (the same image URL) stays.
	var orphans []string
	for _, f := range files {
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM podcasts WHERE image_file = ?) +
				(SELECT COUNT(*) FROM podcast_episodes WHERE image_file = ?)`, f, f).Scan(&n); err == nil && n == 0 {
			orphans = append(orphans, f)
		}
	}
	return orphans, nil
}

const podcastCols = `p.id, p.title, p.author, p.description, p.link, p.feed_url, p.explicit, p.image_file != '', p.refreshed_at, p.last_error,
	sub.subscribed_at`

func (s *Store) podcastCounts(v Viewer, newSince int64) (string, []any) {
	ex := v.explicitCond("e.explicit")
	q := `(SELECT COUNT(*) FROM podcast_episodes e WHERE e.podcast_id = p.id` + ex + `),
		(SELECT COUNT(*) FROM podcast_episodes e LEFT JOIN podcast_episode_state st ON st.episode_id = e.id AND st.user_id = sub.user_id
			WHERE e.podcast_id = p.id AND COALESCE(st.played, 0) = 0` + ex + `),
		(SELECT COUNT(*) FROM podcast_episodes e LEFT JOIN podcast_episode_state st ON st.episode_id = e.id AND st.user_id = sub.user_id
			WHERE e.podcast_id = p.id AND COALESCE(st.played, 0) = 0 AND COALESCE(st.position_seconds, 0) = 0 AND e.published_at >= ?` + ex + `),
		(SELECT MAX(e.published_at) FROM podcast_episodes e WHERE e.podcast_id = p.id` + ex + `)`
	return q, []any{newSince}
}

func scanPodcast(sc interface{ Scan(...any) error }) (Podcast, error) {
	var p Podcast
	var refreshed, latest sql.NullInt64
	var lastErr sql.NullString
	err := sc.Scan(&p.ID, &p.Title, &p.Author, &p.Description, &p.Link, &p.FeedURL, &p.Explicit, &p.HasArtwork, &refreshed, &lastErr,
		&p.SubscribedAt, &p.EpisodeCount, &p.UnplayedCount, &p.NewCount, &latest)
	if err != nil {
		return p, err
	}
	if refreshed.Valid {
		p.RefreshedAt = &refreshed.Int64
	}
	if latest.Valid {
		p.LatestPublishedAt = &latest.Int64
	}
	if lastErr.Valid {
		p.LastError = &lastErr.String
	}
	if p.HasArtwork {
		p.ArtworkURL = fmt.Sprintf("/api/v1/podcasts/shows/%d/artwork", p.ID)
	}
	return p, nil
}

// Subscriptions are the viewer's shows, by title.
func (s *Store) Subscriptions(ctx context.Context, v Viewer) ([]Podcast, error) {
	counts, args := s.podcastCounts(v, s.now().Add(-NewWindow).UnixMilli())
	rows, err := s.DB.QueryContext(ctx, `SELECT `+podcastCols+`, `+counts+`
		FROM podcast_subscriptions sub JOIN podcasts p ON p.id = sub.podcast_id
		WHERE sub.user_id = ?`+v.explicitCond("p.explicit")+` ORDER BY p.title COLLATE NOCASE, p.id`, append(args, v.UserID)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Podcast{}
	for rows.Next() {
		p, err := scanPodcast(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Show is one of the viewer's shows.
func (s *Store) Show(ctx context.Context, v Viewer, id int64) (Podcast, error) {
	counts, args := s.podcastCounts(v, s.now().Add(-NewWindow).UnixMilli())
	p, err := scanPodcast(s.DB.QueryRowContext(ctx, `SELECT `+podcastCols+`, `+counts+`
		FROM podcast_subscriptions sub JOIN podcasts p ON p.id = sub.podcast_id
		WHERE sub.user_id = ? AND p.id = ?`+v.explicitCond("p.explicit"), append(args, v.UserID, id)...))
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

const episodeCols = `e.id, e.podcast_id, p.title, e.title, e.description, e.published_at, e.duration_seconds, e.season, e.episode,
	e.episode_type, e.explicit, e.enclosure_type, e.enclosure_length, COALESCE(st.position_seconds, 0), COALESCE(st.played, 0)`

func (s *Store) episodeFrom(v Viewer) string {
	return ` FROM podcast_episodes e JOIN podcasts p ON p.id = e.podcast_id
		JOIN podcast_subscriptions sub ON sub.podcast_id = p.id AND sub.user_id = ?
		LEFT JOIN podcast_episode_state st ON st.episode_id = e.id AND st.user_id = sub.user_id
		WHERE 1 = 1` + v.explicitCond("p.explicit") + v.explicitCond("e.explicit")
}

func (s *Store) scanEpisodes(rows *sql.Rows) ([]EpisodeView, error) {
	defer func() { _ = rows.Close() }()
	newSince := s.now().Add(-NewWindow).UnixMilli()
	out := []EpisodeView{}
	for rows.Next() {
		var e EpisodeView
		var pub, dur, season, number, size sql.NullInt64
		if err := rows.Scan(&e.ID, &e.PodcastID, &e.PodcastTitle, &e.Title, &e.Description, &pub, &dur, &season, &number, &e.EpisodeType,
			&e.Explicit, &e.MimeType, &size, &e.Position, &e.Played); err != nil {
			return nil, err
		}
		e.PublishedAt, e.Duration, e.Season, e.Episode, e.Size = ptr(pub), ptr(dur), ptr(season), ptr(number), ptr(size)
		e.StreamURL = fmt.Sprintf("/api/v1/podcasts/episodes/%d/stream", e.ID)
		e.ArtworkURL = fmt.Sprintf("/api/v1/podcasts/episodes/%d/artwork", e.ID)
		e.New = !e.Played && e.Position == 0 && pub.Valid && pub.Int64 >= newSince
		out = append(out, e)
	}
	return out, rows.Err()
}

func ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// Episodes is a page of a show's episodes, newest first, and how many the
// viewer may see in all.
func (s *Store) Episodes(ctx context.Context, v Viewer, podcastID int64, limit, offset int) ([]EpisodeView, int, error) {
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*)`+s.episodeFrom(v)+` AND e.podcast_id = ?`, v.UserID, podcastID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+episodeCols+s.episodeFrom(v)+` AND e.podcast_id = ?
		ORDER BY COALESCE(e.published_at, 0) DESC, e.id DESC LIMIT ? OFFSET ?`, v.UserID, podcastID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	eps, err := s.scanEpisodes(rows)
	return eps, total, err
}

// NewEpisodes are the viewer's new episodes across their shows, newest first.
func (s *Store) NewEpisodes(ctx context.Context, v Viewer, limit int) ([]EpisodeView, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+episodeCols+s.episodeFrom(v)+`
		AND COALESCE(st.played, 0) = 0 AND COALESCE(st.position_seconds, 0) = 0 AND e.published_at >= ?
		ORDER BY e.published_at DESC, e.id DESC LIMIT ?`, v.UserID, s.now().Add(-NewWindow).UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	return s.scanEpisodes(rows)
}

// Episode is one episode the viewer may see.
func (s *Store) Episode(ctx context.Context, v Viewer, id int64) (EpisodeView, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+episodeCols+s.episodeFrom(v)+` AND e.id = ?`, v.UserID, id)
	if err != nil {
		return EpisodeView{}, err
	}
	eps, err := s.scanEpisodes(rows)
	if err != nil {
		return EpisodeView{}, err
	}
	if len(eps) == 0 {
		return EpisodeView{}, ErrNotFound
	}
	return eps[0], nil
}

// Enclosure is where an episode the viewer may see is fetched from.
func (s *Store) Enclosure(ctx context.Context, v Viewer, id int64) (string, error) {
	var u string
	err := s.DB.QueryRowContext(ctx, `SELECT e.enclosure_url`+s.episodeFrom(v)+` AND e.id = ?`, v.UserID, id).Scan(&u)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return u, err
}

// ShowArtwork is the image file of a show the viewer subscribes to.
func (s *Store) ShowArtwork(ctx context.Context, v Viewer, id int64) (string, error) {
	var f string
	err := s.DB.QueryRowContext(ctx, `SELECT p.image_file FROM podcasts p JOIN podcast_subscriptions sub ON sub.podcast_id = p.id AND sub.user_id = ?
		WHERE p.id = ?`+v.explicitCond("p.explicit"), v.UserID, id).Scan(&f)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && f == "") {
		return "", ErrNotFound
	}
	return f, err
}

// EpisodeArtwork is an episode's own image file, else its show's.
func (s *Store) EpisodeArtwork(ctx context.Context, v Viewer, id int64) (string, error) {
	var own, show string
	err := s.DB.QueryRowContext(ctx, `SELECT e.image_file, p.image_file`+s.episodeFrom(v)+` AND e.id = ?`, v.UserID, id).Scan(&own, &show)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", ErrNotFound
	case err != nil:
		return "", err
	case own != "":
		return own, nil
	case show != "":
		return show, nil
	}
	return "", ErrNotFound
}

// GetProgress is the viewer's place in an episode they may see.
func (s *Store) GetProgress(ctx context.Context, v Viewer, id int64) (Progress, error) {
	var p Progress
	var dur sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT COALESCE(st.position_seconds, 0), COALESCE(st.played, 0), e.duration_seconds`+s.episodeFrom(v)+
		` AND e.id = ?`, v.UserID, id).Scan(&p.Position, &p.Played, &dur)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.Duration = ptr(dur)
	return p, err
}

// playedNearEnd is how close to the end a saved place marks an episode played.
const playedNearEnd = 30

// SetProgress saves the viewer's place. measured is the duration the player
// found, kept when the feed gave none. Within 30 seconds of the end, the
// episode is played.
func (s *Store) SetProgress(ctx context.Context, v Viewer, id int64, position, measured float64) (Progress, error) {
	cur, err := s.GetProgress(ctx, v, id)
	if err != nil {
		return cur, err
	}
	if cur.Duration == nil && measured >= 1 && measured < 7*24*3600 {
		d := int64(measured)
		if _, err := s.DB.ExecContext(ctx, `UPDATE podcast_episodes SET duration_seconds = ? WHERE id = ? AND duration_seconds IS NULL`, d, id); err != nil {
			return cur, err
		}
		cur.Duration = &d
	}
	played := cur.Played
	if cur.Duration != nil && *cur.Duration > playedNearEnd && position >= float64(*cur.Duration-playedNearEnd) {
		played, position = true, 0
	}
	return s.writeState(ctx, v.UserID, id, position, played, cur.Duration)
}

// SetPlayed marks an episode played or not; either way its place resets.
func (s *Store) SetPlayed(ctx context.Context, v Viewer, id int64, played bool) (Progress, error) {
	cur, err := s.GetProgress(ctx, v, id)
	if err != nil {
		return cur, err
	}
	return s.writeState(ctx, v.UserID, id, 0, played, cur.Duration)
}

func (s *Store) writeState(ctx context.Context, userID, id int64, position float64, played bool, dur *int64) (Progress, error) {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO podcast_episode_state (user_id, episode_id, position_seconds, played, updated_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (user_id, episode_id) DO UPDATE SET position_seconds = excluded.position_seconds,
		played = excluded.played, updated_at = excluded.updated_at`, userID, id, position, played, s.now().UnixMilli())
	return Progress{Position: position, Played: played, Duration: dur}, err
}

// Target is a feed to read again.
type Target struct {
	ID                 int64
	URL                string
	ETag, LastModified string
	CheckedAt          int64
}

// Due are the feeds someone subscribes to that were not checked since before.
func (s *Store) Due(ctx context.Context, before time.Time) ([]Target, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, feed_url, etag, last_modified, COALESCE(checked_at, 0) FROM podcasts p
		WHERE COALESCE(checked_at, 0) < ? AND EXISTS (SELECT 1 FROM podcast_subscriptions sub WHERE sub.podcast_id = p.id)
		ORDER BY COALESCE(checked_at, 0)`, before.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Target
	for rows.Next() {
		var t Target
		if err := rows.Scan(&t.ID, &t.URL, &t.ETag, &t.LastModified, &t.CheckedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Target is one stored feed's refresh details.
func (s *Store) Target(ctx context.Context, id int64) (Target, error) {
	var t Target
	err := s.DB.QueryRowContext(ctx, `SELECT id, feed_url, etag, last_modified, COALESCE(checked_at, 0) FROM podcasts WHERE id = ?`, id).
		Scan(&t.ID, &t.URL, &t.ETag, &t.LastModified, &t.CheckedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// imageWork is an image URL still to download, and where it goes.
type imageWork struct {
	table string
	id    int64
	url   string
}

// imagesToFetch are the show image and the newest episodes' own images not
// downloaded yet. An episode image the same as its show's is not fetched.
func (s *Store) imagesToFetch(ctx context.Context, podcastID int64, episodes int) ([]imageWork, error) {
	var out []imageWork
	var u string
	err := s.DB.QueryRowContext(ctx, `SELECT image_url FROM podcasts WHERE id = ? AND image_url != '' AND image_file = ''`, podcastID).Scan(&u)
	if err == nil {
		out = append(out, imageWork{"podcasts", podcastID, u})
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT e.id, e.image_url FROM podcast_episodes e JOIN podcasts p ON p.id = e.podcast_id
		WHERE e.podcast_id = ? AND e.image_url != '' AND e.image_file = '' AND e.image_url != p.image_url
		ORDER BY COALESCE(e.published_at, 0) DESC LIMIT ?`, podcastID, episodes)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		w := imageWork{table: "podcast_episodes"}
		if err := rows.Scan(&w.id, &w.url); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) setImage(ctx context.Context, w imageWork, file string) error {
	table := "podcasts"
	if w.table == "podcast_episodes" {
		table = "podcast_episodes"
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE `+table+` SET image_file = ? WHERE id = ? AND image_url = ?`, file, w.id, w.url)
	return err
}

// OPML is the viewer's subscriptions as title and feed URL pairs.
func (s *Store) OPML(ctx context.Context, v Viewer) ([][2]string, error) {
	subs, err := s.Subscriptions(ctx, v)
	if err != nil {
		return nil, err
	}
	out := make([][2]string, 0, len(subs))
	for _, p := range subs {
		out = append(out, [2]string{p.Title, p.FeedURL})
	}
	return out, nil
}
