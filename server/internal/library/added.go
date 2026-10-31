package library

import (
	"context"
	"database/sql"
	"fmt"
)

// AddedTitles names what a scan added to a library: new movies as "Title
// (Year)", and new episodes grouped by show as "Show (3 new episodes)", or
// "Show S01E04" for one. Items above idsAbove are the new ones.
func AddedTitles(ctx context.Context, db *sql.DB, libraryID, idsAbove int64) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT i.kind, i.title, i.year, i.season_number, i.episode_number, sh.id, sh.title
		FROM items i
		LEFT JOIN items se ON se.id = i.parent_id AND i.kind = 'episode'
		LEFT JOIN items sh ON sh.id = se.parent_id
		WHERE i.library_id = ? AND i.id > ? AND i.kind IN ('movie', 'episode') AND i.missing_since IS NULL
		ORDER BY i.id`, libraryID, idsAbove)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	episodes := map[int64][]string{}
	shows := map[int64]string{}
	var showOrder []int64
	for rows.Next() {
		var kind, title string
		var year, season, ep, showID sql.NullInt64
		var show sql.NullString
		if err := rows.Scan(&kind, &title, &year, &season, &ep, &showID, &show); err != nil {
			return nil, err
		}
		if kind == "movie" {
			if year.Valid {
				title = fmt.Sprintf("%s (%d)", title, year.Int64)
			}
			out = append(out, title)
			continue
		}
		if !showID.Valid {
			continue
		}
		if _, ok := shows[showID.Int64]; !ok {
			shows[showID.Int64] = show.String
			showOrder = append(showOrder, showID.Int64)
		}
		episodes[showID.Int64] = append(episodes[showID.Int64], fmt.Sprintf("S%02dE%02d", season.Int64, ep.Int64))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range showOrder {
		if eps := episodes[id]; len(eps) == 1 {
			out = append(out, shows[id]+" "+eps[0])
		} else {
			out = append(out, fmt.Sprintf("%s (%d new episodes)", shows[id], len(eps)))
		}
	}
	return out, nil
}
