package enrich

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// ErrNotMatchable is returned for an item that is not a movie or a show.
var ErrNotMatchable = errors.New("only movies and shows are matched")

// Choice is one TMDb search result offered for Fix Match.
type Choice struct {
	TMDbID     int64   `json:"tmdb_id"`
	Title      string  `json:"title"`
	Year       int     `json:"year"`
	Overview   string  `json:"overview"`
	Popularity float64 `json:"popularity"`
}

// Search lists TMDb results for an item's kind, for an admin to pick from when
// automatic matching failed or chose wrong. An empty query searches the item's
// own title.
func (e *Enricher) Search(ctx context.Context, itemID int64, query string, year int) ([]Choice, error) {
	var kind, title string
	err := e.DB.QueryRowContext(ctx, `SELECT kind, title FROM items WHERE id = ?`, itemID).Scan(&kind, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	if kind != "movie" && kind != "show" {
		return nil, ErrNotMatchable
	}
	if strings.TrimSpace(query) == "" {
		query = title
	}
	results, err := e.TMDb.Search(ctx, kind, strings.TrimSpace(query), year)
	if err != nil {
		return nil, err
	}
	out := make([]Choice, 0, len(results))
	for _, r := range results {
		out = append(out, Choice{TMDbID: r.ID, Title: r.DisplayTitle(), Year: r.Year(), Overview: r.Overview, Popularity: r.Popularity})
	}
	return out, nil
}

// Fix sets an item's TMDb id by hand and enriches it from that id now.
// Enrichment trusts an id it did not write itself (as it trusts one from an
// NFO), so later passes keep the admin's choice instead of matching again.
func (e *Enricher) Fix(ctx context.Context, itemID, tmdbID int64) (Stats, error) {
	if tmdbID <= 0 {
		return Stats{}, errors.New("tmdb_id is required")
	}
	res, err := e.DB.ExecContext(ctx, `UPDATE items SET tmdb_id = ? WHERE id = ? AND kind IN ('movie', 'show')`, tmdbID, itemID)
	if err != nil {
		return Stats{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Stats{}, ErrNotMatchable
	}
	c := &candidate{}
	err = e.DB.QueryRowContext(ctx, `SELECT i.id, i.library_id, i.kind, i.title, IFNULL(i.year, 0), IFNULL(i.tmdb_id, 0),
		IFNULL(i.imdb_id, ''), IFNULL(s.status, ''), IFNULL(s.tmdb_id, 0), IFNULL(s.match_key, '') = i.title || '|' || IFNULL(i.year, 0)
		FROM items i LEFT JOIN tmdb_state s ON s.item_id = i.id WHERE i.id = ?`, itemID).
		Scan(&c.id, &c.library, &c.kind, &c.title, &c.year, &c.itemTMDb, &c.imdb, &c.status, &c.stateTMDb, &c.keySame)
	if err != nil {
		return Stats{}, err
	}
	return e.item(ctx, c)
}
