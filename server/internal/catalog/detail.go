package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// Item returns full details for one item the caller may see.
func (c *Catalog) Item(ctx context.Context, pol policy.Policy, userID, id int64) (ItemDetail, error) {
	sums, err := c.Summaries(ctx, pol, userID, []int64{id})
	if err != nil {
		return ItemDetail{}, err
	}
	if len(sums) == 0 {
		return ItemDetail{}, ErrNotFound
	}
	d := ItemDetail{ItemSummary: sums[0], Genres: []string{}, Directors: []string{}, Studios: []string{}, Ratings: map[string]float64{}, Versions: []Version{}}
	var overview, tagline, rating sql.NullString
	if err := c.DB.QueryRowContext(ctx, `SELECT overview, tagline, content_rating FROM items WHERE id = ?`, id).Scan(&overview, &tagline, &rating); err != nil {
		return d, err
	}
	d.Overview, d.Tagline, d.ContentRating = strPtr(overview), strPtr(tagline), strPtr(rating)
	// Episodes and seasons show their show's genres and studios too.
	tagOwner := id
	if d.ShowID != nil && d.Kind != "show" {
		tagOwner = *d.ShowID
	}
	rows, err := c.DB.QueryContext(ctx, `SELECT kind, value FROM item_tags WHERE item_id IN (?, ?) AND kind IN ('genre', 'director', 'studio', 'network')
		ORDER BY item_id = ? DESC, position, value`, id, tagOwner, id)
	if err != nil {
		return d, err
	}
	seen := map[string]bool{}
	err = each(rows, func() error {
		var kind, value string
		if err := rows.Scan(&kind, &value); err != nil {
			return err
		}
		if seen[kind+"\x00"+value] {
			return nil
		}
		seen[kind+"\x00"+value] = true
		switch kind {
		case "genre":
			d.Genres = append(d.Genres, value)
		case "director":
			d.Directors = append(d.Directors, value)
		default:
			d.Studios = append(d.Studios, value)
		}
		return nil
	})
	if err != nil {
		return d, err
	}
	rows, err = c.DB.QueryContext(ctx, `SELECT source, value FROM item_ratings WHERE item_id = ?`, id)
	if err != nil {
		return d, err
	}
	err = each(rows, func() error {
		var src string
		var v float64
		if err := rows.Scan(&src, &v); err != nil {
			return err
		}
		d.Ratings[src] = v
		return nil
	})
	if err != nil {
		return d, err
	}
	if d.Collections, err = c.ItemCollections(ctx, id); err != nil {
		return d, err
	}
	if d.Kind == "movie" || d.Kind == "episode" {
		if d.Versions, err = c.Versions(ctx, pol, id); err != nil {
			return d, err
		}
		if d.Kind == "episode" {
			if d.NextEpisode, err = c.FollowingEpisode(ctx, pol, userID, id); err != nil {
				return d, err
			}
		}
	} else {
		if d.NextEpisode, err = c.NextEpisode(ctx, pol, userID, id, d.Kind); err != nil {
			return d, err
		}
	}
	return d, nil
}

// FollowingEpisode is the episode after this one in the show, for Up Next:
// the rest of the season, then the next season. Specials (season 0) follow
// only a special. Nil at the end of the show.
func (c *Catalog) FollowingEpisode(ctx context.Context, pol policy.Policy, userID, episodeID int64) (*ItemSummary, error) {
	var show int64
	var season, ep sql.NullInt64
	err := c.DB.QueryRowContext(ctx, `SELECT s.parent_id, s.season_number, e.episode_number FROM items e
		JOIN items s ON s.id = e.parent_id WHERE e.id = ? AND e.kind = 'episode'`, episodeID).Scan(&show, &season, &ep)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	cond, args := pol.ItemCond("e")
	sameSeasonOnly := " AND s.season_number > 0"
	if season.Int64 == 0 {
		sameSeasonOnly = " AND s.season_number = 0"
	}
	ids, err := c.queryIDs(ctx, `SELECT e.id FROM items e JOIN items s ON s.id = e.parent_id
		WHERE e.kind = 'episode' AND s.parent_id = ?`+sameSeasonOnly+`
		  AND (s.season_number > ? OR (s.season_number = ? AND COALESCE(e.episode_number, 0) > ?))
		  AND `+cond+`
		ORDER BY s.season_number, e.episode_number, e.id LIMIT 1`,
		append([]any{show, season.Int64, season.Int64, ep.Int64}, args...)...)
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	sums, err := c.Summaries(ctx, pol, userID, ids)
	if err != nil || len(sums) == 0 {
		return nil, err
	}
	return &sums[0], nil
}

// Children lists a show's seasons or a season's episodes in order.
func (c *Catalog) Children(ctx context.Context, pol policy.Policy, userID, id int64) ([]ItemSummary, error) {
	ok, err := c.visible(ctx, pol, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	cond, args := pol.ItemCond("i")
	ids, err := c.queryIDs(ctx, `SELECT i.id FROM items i WHERE i.parent_id = ? AND `+cond+`
		ORDER BY i.season_number IS NULL, i.season_number, i.episode_number IS NULL, i.episode_number, i.sort_title`,
		append([]any{id}, args...)...)
	if err != nil {
		return nil, err
	}
	return c.Summaries(ctx, pol, userID, ids)
}

type episodeState struct {
	id          int64
	played      bool
	pos, lastAt int64
}

// NextEpisode picks the episode Play should start for a show or season.
func (c *Catalog) NextEpisode(ctx context.Context, pol policy.Policy, userID, id int64, kind string) (*ItemSummary, error) {
	scope := "e.parent_id = ?"
	if kind == "show" {
		scope = "e.parent_id IN (SELECT id FROM items WHERE parent_id = ?)"
	}
	cond, args := pol.ItemCond("e")
	rows, err := c.DB.QueryContext(ctx, `SELECT e.id, COALESCE(w.played, 0), COALESCE(w.position_ms, 0), COALESCE(w.last_played_at, 0)
		FROM items e JOIN items s ON s.id = e.parent_id LEFT JOIN watch_state w ON w.item_id = e.id AND w.user_id = ?
		WHERE e.kind = 'episode' AND `+scope+` AND `+cond+`
		ORDER BY s.season_number = 0, s.season_number, e.episode_number, e.id`, append([]any{userID, id}, args...)...)
	if err != nil {
		return nil, err
	}
	var eps []episodeState
	err = each(rows, func() error {
		var e episodeState
		if err := rows.Scan(&e.id, &e.played, &e.pos, &e.lastAt); err != nil {
			return err
		}
		eps = append(eps, e)
		return nil
	})
	if err != nil || len(eps) == 0 {
		return nil, err
	}
	sums, err := c.Summaries(ctx, pol, userID, []int64{pickNext(eps)})
	if err != nil || len(sums) == 0 {
		return nil, err
	}
	return &sums[0], nil
}

// pickNext chooses from episodes in airing order: the most recently watched one
// still in progress; else the first unplayed after the most recently played;
// else the first unplayed; else the first.
func pickNext(eps []episodeState) int64 {
	best := -1
	for i, e := range eps {
		if !e.played && e.pos > 0 && (best < 0 || e.lastAt > eps[best].lastAt) {
			best = i
		}
	}
	if best >= 0 {
		return eps[best].id
	}
	last := -1
	for i, e := range eps {
		if e.played && (last < 0 || e.lastAt >= eps[last].lastAt) {
			last = i
		}
	}
	for i := last + 1; i < len(eps); i++ {
		if !eps[i].played {
			return eps[i].id
		}
	}
	for _, e := range eps {
		if !e.played {
			return e.id
		}
	}
	return eps[0].id
}
