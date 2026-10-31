package activity

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"
)

// ErrNoItem means there is no movie, show or episode with that id.
var ErrNoItem = errors.New("no movie, show or episode with that id")

// ErrNoLibrary means the export named a library that does not exist.
var ErrNoLibrary = errors.New("no library with that id")

// ItemHistory is one title's plays, Tautulli's item page: the newest sessions
// and totals over all of them. A show includes its episodes.
type ItemHistory struct {
	ItemID   int64     `json:"item_id"`
	Kind     string    `json:"kind"`
	Sessions []Session `json:"sessions"`
	// Plays counts every session History lists for the title, not only the page.
	Plays   int   `json:"plays"`
	Users   int   `json:"users"`
	WatchMs int64 `json:"watch_ms"`
}

// ItemHistory lists a movie's, show's or episode's plays, newest first, with
// totals. It returns ErrNoItem for an unknown id or a season.
func (s *Service) ItemHistory(ctx context.Context, itemID int64, limit, offset int) (ItemHistory, error) {
	out := ItemHistory{ItemID: itemID}
	err := s.DB.QueryRowContext(ctx, `SELECT kind FROM items WHERE id = ? AND kind IN ('movie', 'show', 'episode')`, itemID).Scan(&out.Kind)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNoItem
	}
	if err != nil {
		return out, err
	}
	f := HistoryFilter{ItemID: itemID, Limit: limit, Offset: offset}
	h, err := s.History(ctx, f)
	if err != nil {
		return out, err
	}
	out.Sessions, out.Plays = h.Sessions, h.Total
	cond, args := f.where()
	// A session whose account was deleted keeps its username, so count people by
	// id when there is one and by name otherwise.
	err = s.DB.QueryRowContext(ctx, `SELECT COUNT(DISTINCT COALESCE('u' || ps.user_id, 'n' || LOWER(ps.username))),
		COALESCE(SUM(ps.played_ms), 0)`+sessionJoins+cond, args...).Scan(&out.Users, &out.WatchMs)
	return out, err
}

// ExportFilter narrows WatchExport. Zero values mean no filter.
type ExportFilter struct {
	LibraryID int64
	// UnplayedDays keeps only titles nobody has played in that many days,
	// titles never played included.
	UnplayedDays int
}

// TitleWatch is one movie or show as a cleanup tool sees it: where its files
// are, how big, and whether anyone watched it. A show is rolled up from its
// episodes.
type TitleWatch struct {
	ItemID    int64    `json:"item_id"`
	Kind      string   `json:"kind"`
	Title     string   `json:"title"`
	Year      *int64   `json:"year"`
	LibraryID int64    `json:"library_id"`
	Library   string   `json:"library"`
	Paths     []string `json:"paths"`
	SizeBytes int64    `json:"size_bytes"`
	AddedAt   int64    `json:"added_at"`
	// LastPlayedAt is the latest play by anyone, nil when nobody ever played it.
	LastPlayedAt *int64 `json:"last_played_at"`
	// PlayCount is how many times it was finished, summed over users (and over
	// episodes for a show).
	PlayCount int `json:"play_count"`
	// FinishedBy lists the users who have it marked watched; for a show, every
	// episode of it.
	FinishedBy      []string `json:"finished_by"`
	EpisodesTotal   *int     `json:"episodes_total,omitempty"`
	EpisodesWatched *int     `json:"episodes_watched,omitempty"`
}

// leaves maps each playable item (a movie, or an episode) that is still on disk
// to its title: the movie itself, or the episode's show.
const leaves = `WITH leaf AS (
	SELECT i.id AS leaf_id, i.kind AS kind, CASE WHEN i.kind = 'movie' THEN i.id ELSE se.parent_id END AS title_id
	FROM items i LEFT JOIN items se ON se.id = i.parent_id
	WHERE i.kind IN ('movie', 'episode') AND i.missing_since IS NULL)`

// WatchExport calls emit once per movie and show, ordered by library and
// title. Every query runs before the first emit, so a slow reader never holds a
// database connection, and an error from the database comes back before any
// row was emitted.
func (s *Service) WatchExport(ctx context.Context, f ExportFilter, emit func(TitleWatch) error) error {
	if f.LibraryID > 0 {
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries WHERE id = ?`, f.LibraryID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNoLibrary
		}
	}
	titles, err := s.exportTitles(ctx, f.LibraryID)
	if err != nil {
		return err
	}
	byID := make(map[int64]*TitleWatch, len(titles))
	for i := range titles {
		byID[titles[i].ItemID] = &titles[i]
	}
	if err := s.exportFiles(ctx, byID); err != nil {
		return err
	}
	if err := s.exportPlays(ctx, byID); err != nil {
		return err
	}
	if err := s.exportFinished(ctx, byID); err != nil {
		return err
	}
	var cutoff int64
	if f.UnplayedDays > 0 {
		cutoff = s.now().Add(-time.Duration(f.UnplayedDays) * 24 * time.Hour).UnixMilli()
	}
	for _, t := range titles {
		if cutoff > 0 && t.LastPlayedAt != nil && *t.LastPlayedAt >= cutoff {
			continue
		}
		if err := emit(t); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) exportTitles(ctx context.Context, libraryID int64) ([]TitleWatch, error) {
	q := `SELECT i.id, i.kind, i.title, i.year, l.id, l.name, i.added_at FROM items i JOIN libraries l ON l.id = i.library_id
		WHERE i.kind IN ('movie', 'show') AND i.missing_since IS NULL`
	var args []any
	if libraryID > 0 {
		q += ` AND l.id = ?`
		args = append(args, libraryID)
	}
	rows, err := s.DB.QueryContext(ctx, q+` ORDER BY l.name, i.sort_title, i.id`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []TitleWatch{}
	for rows.Next() {
		var t TitleWatch
		var year sql.NullInt64
		if err := rows.Scan(&t.ItemID, &t.Kind, &t.Title, &year, &t.LibraryID, &t.Library, &t.AddedAt); err != nil {
			return nil, err
		}
		t.Year = i64(year)
		t.Paths, t.FinishedBy = []string{}, []string{}
		if t.Kind == "show" {
			t.EpisodesTotal, t.EpisodesWatched = new(int), new(int)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// exportFiles adds each title's files that are still on disk and their sizes.
func (s *Service) exportFiles(ctx context.Context, byID map[int64]*TitleWatch) error {
	rows, err := s.DB.QueryContext(ctx, leaves+` SELECT leaf.title_id, v.path, v.size FROM leaf
		JOIN media_versions v ON v.item_id = leaf.leaf_id WHERE v.missing_since IS NULL ORDER BY leaf.title_id, v.path`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id sql.NullInt64
		var path string
		var size int64
		if err := rows.Scan(&id, &path, &size); err != nil {
			return err
		}
		if t := byID[id.Int64]; t != nil {
			t.Paths = append(t.Paths, path)
			t.SizeBytes += size
		}
	}
	return rows.Err()
}

// exportPlays sets the latest play and the finished count. The latest play is
// the newer of the watch state and the activity log, so a deleted account's
// plays still count and a title is never reported older than it is.
func (s *Service) exportPlays(ctx context.Context, byID map[int64]*TitleWatch) error {
	rows, err := s.DB.QueryContext(ctx, leaves+` SELECT title_id, MAX(last), SUM(plays) FROM (
			SELECT leaf.title_id, w.last_played_at AS last, w.play_count AS plays FROM leaf JOIN watch_state w ON w.item_id = leaf.leaf_id
			UNION ALL
			SELECT leaf.title_id, ps.last_event_at, 0 FROM leaf JOIN playback_sessions ps ON ps.item_id = leaf.leaf_id
			WHERE ps.decision != 'refused' AND (ps.played_ms > 0 OR ps.last_position_ms != ps.start_position_ms)
		) GROUP BY title_id`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, last sql.NullInt64
		var plays int
		if err := rows.Scan(&id, &last, &plays); err != nil {
			return err
		}
		if t := byID[id.Int64]; t != nil {
			t.LastPlayedAt, t.PlayCount = i64(last), plays
		}
	}
	return rows.Err()
}

// exportFinished fills who finished each title and, for shows, the episode
// counts. A user finished a show when every episode on disk is watched.
func (s *Service) exportFinished(ctx context.Context, byID map[int64]*TitleWatch) error {
	rows, err := s.DB.QueryContext(ctx, leaves+` SELECT title_id, COUNT(*),
			SUM(EXISTS (SELECT 1 FROM watch_state w WHERE w.item_id = leaf.leaf_id AND w.played = 1))
		FROM leaf WHERE kind = 'episode' GROUP BY title_id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id sql.NullInt64
		var total, watched int
		if err := rows.Scan(&id, &total, &watched); err != nil {
			_ = rows.Close()
			return err
		}
		if t := byID[id.Int64]; t != nil && t.EpisodesTotal != nil {
			*t.EpisodesTotal, *t.EpisodesWatched = total, watched
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	rows, err = s.DB.QueryContext(ctx, leaves+` SELECT leaf.title_id, u.username, COUNT(*) FROM leaf
		JOIN watch_state w ON w.item_id = leaf.leaf_id AND w.played = 1 JOIN users u ON u.id = w.user_id
		GROUP BY leaf.title_id, u.id`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id sql.NullInt64
		var name string
		var n int
		if err := rows.Scan(&id, &name, &n); err != nil {
			return err
		}
		t := byID[id.Int64]
		if t == nil {
			continue
		}
		if t.Kind == "movie" || (t.EpisodesTotal != nil && n == *t.EpisodesTotal) {
			t.FinishedBy = append(t.FinishedBy, name)
		}
	}
	for _, t := range byID {
		sort.Strings(t.FinishedBy)
	}
	return rows.Err()
}
