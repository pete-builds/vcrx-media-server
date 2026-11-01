package live

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ErrNotFound is a channel or source that does not exist, or that the caller
// may not see.
var ErrNotFound = errors.New("not found")

// Source is a playlist as admins see it. Its URL is never part of an answer:
// a provider's playlist URL usually carries the account's credentials.
type Source struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	RefreshedAt  *int64  `json:"refreshed_at"`
	LastError    *string `json:"last_error"`
	ChannelCount int     `json:"channel_count"`
	// HasGuide is an xmltv location set in vcrx.yaml; the rest is its last read.
	HasGuide         bool    `json:"has_guide"`
	GuideRefreshedAt *int64  `json:"guide_refreshed_at"`
	GuideError       *string `json:"guide_error"`
	ProgramCount     int     `json:"program_count"`
}

// Channel is a channel as clients see it: no stream or logo URL.
type Channel struct {
	ID      int64   `json:"id"`
	Number  *string `json:"number"`
	Name    string  `json:"name"`
	Group   *string `json:"group"`
	HasLogo bool    `json:"has_logo"`
	// Now and Next are from the guide, when the channel has one (NowNext).
	Now  *Program `json:"now,omitempty"`
	Next *Program `json:"next,omitempty"`
	// Admin-only fields, left out of the viewer's list.
	SourceID       int64   `json:"source_id,omitempty"`
	Enabled        *bool   `json:"enabled,omitempty"`
	Gone           *bool   `json:"gone,omitempty"`
	PlaylistNumber *string `json:"playlist_number,omitempty"`
}

// Store is the live_sources and channels tables.
type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s *Store) now() int64 {
	if s.Now != nil {
		return s.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

// EnsureSource adds a playlist named in vcrx.yaml, or updates its URL and
// its guide's (empty: none), and returns its id. Sources are matched by name.
func (s *Store) EnsureSource(ctx context.Context, name, url, guide string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `INSERT INTO live_sources (name, url, guide_url) VALUES (?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET url = excluded.url, guide_url = excluded.guide_url RETURNING id`, name, url, null(guide)).Scan(&id)
	return id, err
}

// SourceURL is a source's playlist location, for the refresher only.
func (s *Store) SourceURL(ctx context.Context, id int64) (string, error) {
	var url string
	err := s.DB.QueryRowContext(ctx, `SELECT url FROM live_sources WHERE id = ?`, id).Scan(&url)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return url, err
}

// Sources lists the playlists, by name.
func (s *Store) Sources(ctx context.Context) ([]Source, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, refreshed_at, last_error, channel_count, guide_url IS NOT NULL,
		guide_refreshed_at, guide_error, program_count FROM live_sources ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Source{}
	for rows.Next() {
		var src Source
		var at, gat sql.NullInt64
		var msg, gmsg sql.NullString
		if err := rows.Scan(&src.ID, &src.Name, &at, &msg, &src.ChannelCount, &src.HasGuide, &gat, &gmsg, &src.ProgramCount); err != nil {
			return nil, err
		}
		if gat.Valid {
			src.GuideRefreshedAt = &gat.Int64
		}
		src.GuideError = ptr(gmsg)
		if at.Valid {
			src.RefreshedAt = &at.Int64
		}
		if msg.Valid {
			src.LastError = &msg.String
		}
		out = append(out, src)
	}
	return out, rows.Err()
}

// Sync stores a fresh read of a source's playlist. New channels are added
// enabled; known ones get the playlist's name, number, group, stream and logo
// URL but keep the admin's number and enabled flag; channels the playlist no
// longer lists are marked gone rather than deleted, so an admin's choices
// survive a playlist that is briefly wrong.
func (s *Store) Sync(ctx context.Context, sourceID int64, entries []Entry) error {
	now := s.now()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE channels SET gone = 1 WHERE source_id = ?`, sourceID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, e := range entries {
		key := e.Key()
		if seen[key] {
			continue // the first of two entries with one tvg-id wins
		}
		seen[key] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO channels (source_id, source_key, name, playlist_number, tvg_id, group_title,
				stream_url, logo_url, added_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (source_id, source_key) DO UPDATE SET name = excluded.name, playlist_number = excluded.playlist_number,
				tvg_id = excluded.tvg_id, group_title = excluded.group_title, stream_url = excluded.stream_url,
				logo_file = CASE WHEN channels.logo_url IS excluded.logo_url THEN channels.logo_file END,
				logo_url = excluded.logo_url, gone = 0, updated_at = excluded.updated_at`,
			sourceID, key, e.Name, null(e.Number), null(e.TvgID), null(e.Group), e.URL, null(e.Logo), now, now); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE live_sources SET refreshed_at = ?, last_error = NULL, channel_count = ? WHERE id = ?`,
		now, len(seen), sourceID); err != nil {
		return err
	}
	return tx.Commit()
}

// Failed records why a refresh did not happen. The channels stay as they were.
func (s *Store) Failed(ctx context.Context, sourceID int64, msg string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE live_sources SET last_error = ? WHERE id = ?`, clip(msg), sourceID)
	return err
}

// Channels lists what a viewer may watch: enabled, still in its playlist, by
// number then name. allowed is the caller's grant; without it the list is empty.
func (s *Store) Channels(ctx context.Context, allowed bool) ([]Channel, error) {
	if !allowed {
		return []Channel{}, nil
	}
	return s.list(ctx, `WHERE c.enabled = 1 AND c.gone = 0`, false)
}

// AllChannels lists every channel for the admin page, disabled and gone ones too.
func (s *Store) AllChannels(ctx context.Context) ([]Channel, error) {
	return s.list(ctx, ``, true)
}

func (s *Store) list(ctx context.Context, where string, admin bool) ([]Channel, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT c.id, COALESCE(c.number, c.playlist_number), c.name, c.group_title,
			c.logo_file IS NOT NULL, c.source_id, c.enabled, c.gone, c.playlist_number
		FROM channels c `+where+`
		ORDER BY CAST(COALESCE(c.number, c.playlist_number) AS REAL) IS NULL, CAST(COALESCE(c.number, c.playlist_number) AS REAL),
			c.name COLLATE NOCASE, c.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Channel{}
	for rows.Next() {
		var c Channel
		var number, group, playlistNumber sql.NullString
		var sourceID int64
		var enabled, gone bool
		if err := rows.Scan(&c.ID, &number, &c.Name, &group, &c.HasLogo, &sourceID, &enabled, &gone, &playlistNumber); err != nil {
			return nil, err
		}
		c.Number, c.Group = ptr(number), ptr(group)
		if admin {
			c.SourceID, c.Enabled, c.Gone, c.PlaylistNumber = sourceID, &enabled, &gone, ptr(playlistNumber)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ChannelUpdate is an admin's change to one channel. A nil field is left as
// it is; an empty Number goes back to the playlist's.
type ChannelUpdate struct {
	Enabled *bool   `json:"enabled"`
	Number  *string `json:"number"`
}

// Update applies an admin's change.
func (s *Store) Update(ctx context.Context, id int64, u ChannelUpdate) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE channels SET enabled = COALESCE(?, enabled),
			number = CASE WHEN ? THEN NULLIF(TRIM(?), '') ELSE number END, updated_at = ? WHERE id = ?`,
		u.Enabled, u.Number != nil, deref(u.Number), s.now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Logo is a channel's cached logo file name, for a viewer who may watch it.
func (s *Store) Logo(ctx context.Context, id int64, allowed bool) (string, error) {
	if !allowed {
		return "", ErrNotFound
	}
	var file sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT logo_file FROM channels WHERE id = ? AND enabled = 1 AND gone = 0`, id).Scan(&file)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !file.Valid) {
		return "", ErrNotFound
	}
	return file.String, err
}

// logoWork is a channel whose logo has not been fetched yet.
type logoWork struct {
	ID  int64
	URL string
}

func (s *Store) logosToFetch(ctx context.Context, sourceID int64) ([]logoWork, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, logo_url FROM channels
		WHERE source_id = ? AND gone = 0 AND logo_url IS NOT NULL AND logo_file IS NULL ORDER BY id`, sourceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []logoWork
	for rows.Next() {
		var w logoWork
		if err := rows.Scan(&w.ID, &w.URL); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) setLogo(ctx context.Context, id int64, url, file string) error {
	// Only if the playlist still names this logo: a refresh in between wins.
	_, err := s.DB.ExecContext(ctx, `UPDATE channels SET logo_file = ? WHERE id = ? AND logo_url = ?`, file, id, url)
	return err
}

func null(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func ptr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	return &n.String
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
