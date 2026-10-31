package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

var ErrNotFound = errors.New("not found")

type Catalog struct {
	DB *sql.DB
}

// each calls fn for every row and always closes rows.
func each(rows *sql.Rows, fn func() error) error {
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := fn(); err != nil {
			return err
		}
	}
	return rows.Err()
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func int64Args(ids []int64) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}

// Libraries lists the libraries the caller may see, with visible top-level counts.
func (c *Catalog) Libraries(ctx context.Context, pol policy.Policy) ([]Library, error) {
	rows, err := c.DB.QueryContext(ctx, `SELECT id, name, kind FROM libraries ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	libs := []Library{}
	err = each(rows, func() error {
		var l Library
		if err := rows.Scan(&l.ID, &l.Name, &l.Kind); err != nil {
			return err
		}
		if pol.CanSeeLibrary(l.ID) {
			libs = append(libs, l)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	cond, args := pol.ItemCond("i")
	for idx := range libs {
		q := `SELECT COUNT(*) FROM items i WHERE i.library_id = ? AND i.parent_id IS NULL AND ` + cond
		if err := c.DB.QueryRowContext(ctx, q, append([]any{libs[idx].ID}, args...)...).Scan(&libs[idx].ItemCount); err != nil {
			return nil, err
		}
	}
	return libs, nil
}

// LibraryItems pages through a library's top-level items.
func (c *Catalog) LibraryItems(ctx context.Context, pol policy.Policy, userID, libraryID int64, q ListQuery) ([]ItemSummary, int, error) {
	if !pol.CanSeeLibrary(libraryID) {
		return nil, 0, ErrNotFound
	}
	var exists int
	if err := c.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries WHERE id = ?`, libraryID).Scan(&exists); err != nil {
		return nil, 0, err
	}
	if exists == 0 {
		return nil, 0, ErrNotFound
	}
	cond, args := pol.ItemCond("i")
	where := []string{"i.library_id = ?", "i.parent_id IS NULL", cond}
	wargs := append([]any{libraryID}, args...)
	if q.Genre != "" {
		where = append(where, `EXISTS (SELECT 1 FROM item_tags t WHERE t.item_id = i.id AND t.kind = 'genre' AND t.value = ?)`)
		wargs = append(wargs, q.Genre)
	}
	if q.Year > 0 {
		where = append(where, "i.year = ?")
		wargs = append(wargs, q.Year)
	}
	if q.Decade > 0 {
		where = append(where, "i.year BETWEEN ? AND ?")
		wargs = append(wargs, q.Decade, q.Decade+9)
	}
	if q.Resolution != "" {
		vc, vargs := pol.VersionCond("rv")
		where = append(where, `EXISTS (SELECT 1 FROM media_versions rv WHERE rv.item_id = i.id AND rv.resolution = ? AND `+vc+`)`)
		wargs = append(wargs, q.Resolution)
		wargs = append(wargs, vargs...)
	}
	if q.Unwatched {
		where = append(where, `NOT EXISTS (SELECT 1 FROM watch_state w WHERE w.item_id = i.id AND w.user_id = ? AND w.played = 1)`)
		wargs = append(wargs, userID)
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := c.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM items i WHERE `+whereSQL, wargs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "i.sort_title COLLATE NOCASE ASC, i.year ASC"
	desc := strings.EqualFold(q.Order, "desc")
	switch q.Sort {
	case "added":
		order = "i.added_at DESC, i.id DESC"
		if strings.EqualFold(q.Order, "asc") {
			order = "i.added_at ASC, i.id ASC"
		}
	case "year":
		order = "i.year IS NULL, i.year ASC, i.sort_title COLLATE NOCASE ASC"
		if desc {
			order = "i.year IS NULL, i.year DESC, i.sort_title COLLATE NOCASE ASC"
		}
	default:
		if desc {
			order = "i.sort_title COLLATE NOCASE DESC, i.year DESC"
		}
	}
	limit, offset := clampPage(q.Limit, q.Offset, 100, 500)
	ids, err := c.queryIDs(ctx, `SELECT i.id FROM items i WHERE `+whereSQL+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
		append(wargs, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	items, err := c.Summaries(ctx, pol, userID, ids)
	return items, total, err
}

func clampPage(limit, offset, def, max int) (int, int) {
	if limit <= 0 {
		limit = def
	}
	if limit > max {
		limit = max
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func (c *Catalog) queryIDs(ctx context.Context, q string, args ...any) ([]int64, error) {
	rows, err := c.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	err = each(rows, func() error {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
		return nil
	})
	return ids, err
}

// visible reports whether the caller may see one item.
func (c *Catalog) visible(ctx context.Context, pol policy.Policy, id int64) (bool, error) {
	cond, args := pol.ItemCond("i")
	var n int
	err := c.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM items i WHERE i.id = ? AND `+cond, append([]any{id}, args...)...).Scan(&n)
	return n > 0, err
}

// Summaries loads summaries for ids, preserving their order and dropping any the
// caller may not see.
func (c *Catalog) Summaries(ctx context.Context, pol policy.Policy, userID int64, ids []int64) ([]ItemSummary, error) {
	out := []ItemSummary{}
	if len(ids) == 0 {
		return out, nil
	}
	cond, cargs := pol.ItemCond("i")
	q := `SELECT i.id, i.kind, i.library_id, i.title, i.year, i.parent_id, i.season_number, i.episode_number,
			i.episode_number_end, i.runtime_ms, i.added_at,
			w.position_ms, w.duration_ms, w.played, w.play_count, w.last_played_at
		FROM items i LEFT JOIN watch_state w ON w.item_id = i.id AND w.user_id = ?
		WHERE i.id IN (` + placeholders(len(ids)) + `) AND ` + cond
	args := append([]any{userID}, int64Args(ids)...)
	rows, err := c.DB.QueryContext(ctx, q, append(args, cargs...)...)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*ItemSummary{}
	err = each(rows, func() error {
		var s ItemSummary
		var year, parent, season, ep, epEnd, runtime, pos, dur, lastPlayed, playCount sql.NullInt64
		var played sql.NullBool
		if err := rows.Scan(&s.ID, &s.Kind, &s.LibraryID, &s.Title, &year, &parent, &season, &ep, &epEnd, &runtime, &s.AddedAt,
			&pos, &dur, &played, &playCount, &lastPlayed); err != nil {
			return err
		}
		s.Year, s.ParentID, s.SeasonNumber, s.EpisodeNumber = ptr(year), ptr(parent), ptr(season), ptr(ep)
		s.EpisodeNumberEnd, s.RuntimeMs = ptr(epEnd), ptr(runtime)
		s.Watch = WatchState{PositionMs: pos.Int64, DurationMs: ptr(dur), Played: played.Bool, PlayCount: int(playCount.Int64), LastPlayedAt: ptr(lastPlayed)}
		byID[s.ID] = &s
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(byID) == 0 {
		return out, nil
	}
	if err := c.enrich(ctx, pol, userID, byID); err != nil {
		return nil, err
	}
	for _, id := range ids {
		if s, ok := byID[id]; ok {
			out = append(out, *s)
		}
	}
	return out, nil
}

func ptr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

func strPtr(s sql.NullString) *string {
	if !s.Valid || s.String == "" {
		return nil
	}
	v := s.String
	return &v
}

// enrich fills show titles, artwork flags, best resolution, child counts and
// unplayed counts in a few batched queries.
func (c *Catalog) enrich(ctx context.Context, pol policy.Policy, userID int64, byID map[int64]*ItemSummary) error {
	ids := make([]int64, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	in := placeholders(len(ids))
	idArgs := int64Args(ids)

	// Show lineage: a season's parent is its show, an episode's grandparent is.
	rows, err := c.DB.QueryContext(ctx, `SELECT i.id,
			CASE i.kind WHEN 'season' THEN p.id WHEN 'episode' THEN gp.id END,
			CASE i.kind WHEN 'season' THEN p.title WHEN 'episode' THEN gp.title END
		FROM items i LEFT JOIN items p ON p.id = i.parent_id LEFT JOIN items gp ON gp.id = p.parent_id
		WHERE i.id IN (`+in+`) AND i.kind IN ('season', 'episode')`, idArgs...)
	if err != nil {
		return err
	}
	err = each(rows, func() error {
		var id int64
		var showID sql.NullInt64
		var showTitle sql.NullString
		if err := rows.Scan(&id, &showID, &showTitle); err != nil {
			return err
		}
		byID[id].ShowID = ptr(showID)
		byID[id].ShowTitle = strPtr(showTitle)
		return nil
	})
	if err != nil {
		return err
	}

	// Artwork, with seasons falling back to their show's poster.
	lookup := append([]int64{}, ids...)
	for _, s := range byID {
		if s.Kind == "season" && s.ShowID != nil {
			lookup = append(lookup, *s.ShowID)
		}
	}
	art := map[int64]map[string]bool{}
	rows, err = c.DB.QueryContext(ctx, `SELECT item_id, kind FROM artwork WHERE item_id IN (`+placeholders(len(lookup))+`)`, int64Args(lookup)...)
	if err != nil {
		return err
	}
	err = each(rows, func() error {
		var id int64
		var kind string
		if err := rows.Scan(&id, &kind); err != nil {
			return err
		}
		if art[id] == nil {
			art[id] = map[string]bool{}
		}
		art[id][kind] = true
		return nil
	})
	if err != nil {
		return err
	}
	for id, s := range byID {
		s.HasPoster = art[id]["poster"]
		s.HasBackdrop = art[id]["backdrop"]
		s.HasThumb = art[id]["thumb"]
		if s.Kind == "season" && !s.HasPoster && s.ShowID != nil {
			s.HasPoster = art[*s.ShowID]["poster"]
		}
		if s.Kind == "episode" {
			// Episodes without a thumbnail file get a frame extracted on request.
			s.HasThumb = true
		}
	}

	// Best playable resolution and runtime for movies and episodes.
	vc, vargs := pol.VersionCond("v")
	rows, err = c.DB.QueryContext(ctx, `SELECT v.item_id, v.resolution, v.duration_ms FROM media_versions v
		WHERE v.item_id IN (`+in+`) AND `+vc, append(idArgs, vargs...)...)
	if err != nil {
		return err
	}
	err = each(rows, func() error {
		var id int64
		var res sql.NullString
		var dur sql.NullInt64
		if err := rows.Scan(&id, &res, &dur); err != nil {
			return err
		}
		s := byID[id]
		if res.Valid && (s.Resolution == nil || policy.ResolutionRank(res.String) > policy.ResolutionRank(*s.Resolution)) {
			r := res.String
			s.Resolution = &r
		}
		if s.RuntimeMs == nil && dur.Valid {
			d := dur.Int64
			s.RuntimeMs = &d
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Children and unplayed episodes for shows and seasons.
	cond, cargs := pol.ItemCond("e")
	for id, s := range byID {
		if s.Kind != "show" && s.Kind != "season" {
			continue
		}
		var children, episodes, unplayed int
		if err := c.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM items ch WHERE ch.parent_id = ? AND ch.missing_since IS NULL`, id).Scan(&children); err != nil {
			return err
		}
		epScope := "e.parent_id = ?"
		if s.Kind == "show" {
			epScope = "e.parent_id IN (SELECT id FROM items WHERE parent_id = ?)"
		}
		q := `SELECT COUNT(*), COALESCE(SUM(CASE WHEN w.played = 1 THEN 0 ELSE 1 END), 0)
			FROM items e LEFT JOIN watch_state w ON w.item_id = e.id AND w.user_id = ?
			WHERE e.kind = 'episode' AND ` + epScope + ` AND ` + cond
		if err := c.DB.QueryRowContext(ctx, q, append([]any{userID, id}, cargs...)...).Scan(&episodes, &unplayed); err != nil {
			return err
		}
		s.ChildCount = &children
		s.Watch.UnplayedCount = &unplayed
		s.Watch.Played = episodes > 0 && unplayed == 0
	}
	return nil
}

// Search matches titles of movies, shows and episodes. Prefix matches rank first.
func (c *Catalog) Search(ctx context.Context, pol policy.Policy, userID int64, query string, limit int) ([]ItemSummary, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []ItemSummary{}, nil
	}
	limit, _ = clampPage(limit, 0, 50, 200)
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
	cond, args := pol.ItemCond("i")
	q := `SELECT i.id FROM items i
		WHERE i.kind IN ('movie', 'show', 'episode') AND (i.title LIKE ? ESCAPE '\' OR i.sort_title LIKE ? ESCAPE '\'
			OR i.original_title LIKE ? ESCAPE '\') AND ` + cond + `
		ORDER BY (i.title LIKE ? ESCAPE '\' OR i.sort_title LIKE ? ESCAPE '\') DESC,
			CASE i.kind WHEN 'episode' THEN 1 ELSE 0 END, i.sort_title COLLATE NOCASE
		LIMIT ?`
	contains, prefix := "%"+esc+"%", esc+"%"
	all := append([]any{contains, contains, contains}, args...)
	all = append(all, prefix, prefix, limit)
	ids, err := c.queryIDs(ctx, q, all...)
	if err != nil {
		return nil, err
	}
	return c.Summaries(ctx, pol, userID, ids)
}

// Home builds the TV home rows: Continue Watching, then recently added per library.
func (c *Catalog) Home(ctx context.Context, pol policy.Policy, userID int64) ([]HomeRow, error) {
	rows := []HomeRow{}
	cond, args := pol.ItemCond("i")
	ids, err := c.queryIDs(ctx, `SELECT i.id FROM items i JOIN watch_state w ON w.item_id = i.id AND w.user_id = ?
		WHERE i.kind IN ('movie', 'episode') AND w.played = 0 AND w.position_ms > 0 AND `+cond+`
		ORDER BY w.last_played_at DESC LIMIT 20`, append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	if len(ids) > 0 {
		items, err := c.Summaries(ctx, pol, userID, ids)
		if err != nil {
			return nil, err
		}
		rows = append(rows, HomeRow{ID: "continue_watching", Title: "Continue Watching", Items: items})
	}
	libs, err := c.Libraries(ctx, pol)
	if err != nil {
		return nil, err
	}
	for _, l := range libs {
		var q string
		if l.Kind == "movie" {
			q = `SELECT i.id FROM items i WHERE i.library_id = ? AND i.kind = 'movie' AND ` + cond + ` ORDER BY i.added_at DESC, i.id DESC LIMIT 20`
		} else {
			q = `SELECT i.id FROM items i WHERE i.library_id = ? AND i.kind = 'show' AND ` + cond + `
				ORDER BY (SELECT MAX(e.added_at) FROM items s JOIN items e ON e.parent_id = s.id WHERE s.parent_id = i.id) DESC LIMIT 20`
		}
		ids, err := c.queryIDs(ctx, q, append([]any{l.ID}, args...)...)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			continue
		}
		items, err := c.Summaries(ctx, pol, userID, ids)
		if err != nil {
			return nil, err
		}
		id := l.ID
		rows = append(rows, HomeRow{ID: "recently_added:" + strconv.FormatInt(l.ID, 10), Title: "Recently Added in " + l.Name, LibraryID: &id, Items: items})
	}
	return rows, nil
}

// SetWatched marks a movie or episode, or every episode of a show or season.
func (c *Catalog) SetWatched(ctx context.Context, pol policy.Policy, userID, itemID int64, played bool, now int64) error {
	ok, err := c.visible(ctx, pol, itemID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	var kind string
	if err := c.DB.QueryRowContext(ctx, `SELECT kind FROM items WHERE id = ?`, itemID).Scan(&kind); err != nil {
		return err
	}
	targets := []int64{itemID}
	switch kind {
	case "season":
		targets, err = c.queryIDs(ctx, `SELECT id FROM items WHERE parent_id = ? AND kind = 'episode'`, itemID)
	case "show":
		targets, err = c.queryIDs(ctx, `SELECT e.id FROM items s JOIN items e ON e.parent_id = s.id WHERE s.parent_id = ? AND e.kind = 'episode'`, itemID)
	}
	if err != nil {
		return err
	}
	tx, err := c.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range targets {
		if played {
			_, err = tx.ExecContext(ctx, `INSERT INTO watch_state (user_id, item_id, position_ms, played, play_count, last_played_at, updated_at)
				VALUES (?, ?, 0, 1, 1, ?, ?)
				ON CONFLICT (user_id, item_id) DO UPDATE SET position_ms = 0, played = 1,
					play_count = CASE WHEN played = 1 THEN play_count ELSE play_count + 1 END, updated_at = excluded.updated_at`,
				userID, id, now, now)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE watch_state SET played = 0, position_ms = 0, updated_at = ? WHERE user_id = ? AND item_id = ?`,
				now, userID, id)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearProgress forgets the caller's resume position on a movie or episode, so
// it leaves Continue Watching, without marking it played or touching the play
// count. On a show or season it clears every episode. Only the caller's own
// watch state changes; an item the caller cannot see is ErrNotFound.
func (c *Catalog) ClearProgress(ctx context.Context, pol policy.Policy, userID, itemID int64, now int64) error {
	ok, err := c.visible(ctx, pol, itemID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	_, err = c.DB.ExecContext(ctx, `UPDATE watch_state SET position_ms = 0, updated_at = ?
		WHERE user_id = ? AND position_ms > 0 AND item_id IN (
			SELECT ? UNION ALL
			SELECT id FROM items WHERE parent_id = ? AND kind = 'episode' UNION ALL
			SELECT e.id FROM items s JOIN items e ON e.parent_id = s.id WHERE s.parent_id = ? AND e.kind = 'episode')`,
		now, userID, itemID, itemID, itemID)
	return err
}
