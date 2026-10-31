package catalog

import (
	"context"
	"database/sql"

	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// BeforeTrailer is a local trailer to play before a movie: another title's,
// one the caller may see, may play and has not watched.
type BeforeTrailer struct {
	ExtraID    int64  `json:"extra_id"`
	ItemID     int64  `json:"item_id"`
	ItemTitle  string `json:"item_title"`
	Year       *int64 `json:"year"`
	DurationMs *int64 `json:"duration_ms"`
}

// BeforeTrailers picks up to n local trailers, at random, for movies other
// than movieID. Every condition the details page and the extra's file route
// apply holds here too: the movie is visible under the caller's policy, the
// trailer's file is one the policy lets them play, and a movie out on loan to
// someone else is left out. So a trailer for a title the profile cannot see
// (a kids' profile, a denied label, an ungranted library) is never offered.
func (c *Catalog) BeforeTrailers(ctx context.Context, pol policy.Policy, userID, movieID int64, n int) ([]BeforeTrailer, error) {
	out := []BeforeTrailer{}
	if n <= 0 {
		return out, nil
	}
	ic, iargs := pol.ItemCond("i")
	xc, xargs := extraCond(pol, "x")
	lc, largs := pol.LoanCond("i")
	args := append([]any{movieID, userID}, iargs...)
	args = append(append(append(args, xargs...), largs...), n)
	// One trailer per movie: the first by the details page's order.
	rows, err := c.DB.QueryContext(ctx, `SELECT x.id, i.id, i.title, i.year, x.duration_ms FROM items i
		JOIN item_extras x ON x.id = (SELECT x2.id FROM item_extras x2 WHERE x2.item_id = i.id AND x2.kind = 'trailer'
			AND x2.missing_since IS NULL ORDER BY x2.title = 'Trailer' DESC, x2.title COLLATE NOCASE, x2.id LIMIT 1)
		WHERE i.kind = 'movie' AND i.id != ?
			AND NOT EXISTS (SELECT 1 FROM watch_state w WHERE w.item_id = i.id AND w.user_id = ? AND w.played = 1)
			AND `+ic+` AND `+xc+` AND `+lc+`
		ORDER BY random() LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	err = each(rows, func() error {
		var t BeforeTrailer
		var year, dur sql.NullInt64
		if err := rows.Scan(&t.ExtraID, &t.ItemID, &t.ItemTitle, &year, &dur); err != nil {
			return err
		}
		t.Year, t.DurationMs = ptr(year), ptr(dur)
		out = append(out, t)
		return nil
	})
	return out, err
}
