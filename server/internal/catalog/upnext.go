package catalog

import (
	"context"
	"sort"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// UpNextWindow is how long after finishing an episode its show keeps
// offering the next one on Home.
const UpNextWindow = 60 * 24 * time.Hour

type recent struct {
	id int64
	at int64
}

// continueWatching is Home's first row, like Plex's: films and episodes in
// progress, and for each show whose last episode the user finished recently,
// the episode after it. Most recent activity first.
func (c *Catalog) continueWatching(ctx context.Context, pol policy.Policy, userID int64, now time.Time) ([]int64, error) {
	cond, args := pol.ItemCond("i")
	var out []recent
	inProgressShows := map[int64]bool{}
	rows, err := c.DB.QueryContext(ctx, `SELECT i.id, w.last_played_at, COALESCE(sh.id, 0) FROM items i
		JOIN watch_state w ON w.item_id = i.id AND w.user_id = ?
		LEFT JOIN items se ON se.id = i.parent_id AND i.kind = 'episode' LEFT JOIN items sh ON sh.id = se.parent_id
		WHERE i.kind IN ('movie', 'episode') AND w.played = 0 AND w.position_ms > 0 AND `+cond+`
		ORDER BY w.last_played_at DESC LIMIT 20`, append([]any{userID}, args...)...)
	if err != nil {
		return nil, err
	}
	err = each(rows, func() error {
		var r recent
		var show int64
		if err := rows.Scan(&r.id, &r.at, &show); err != nil {
			return err
		}
		if show > 0 {
			inProgressShows[show] = true
		}
		out = append(out, r)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// The last finished episode per show, within the window.
	rows, err = c.DB.QueryContext(ctx, `SELECT i.id, w.last_played_at, sh.id FROM items i
		JOIN watch_state w ON w.item_id = i.id AND w.user_id = ?
		JOIN items se ON se.id = i.parent_id JOIN items sh ON sh.id = se.parent_id
		WHERE i.kind = 'episode' AND w.played = 1 AND w.last_played_at >= ? AND `+cond+`
		ORDER BY w.last_played_at DESC`, append([]any{userID, now.Add(-UpNextWindow).UnixMilli()}, args...)...)
	if err != nil {
		return nil, err
	}
	type finished struct{ ep, at, show int64 }
	var last []finished
	seen := map[int64]bool{}
	err = each(rows, func() error {
		var f finished
		if err := rows.Scan(&f.ep, &f.at, &f.show); err != nil {
			return err
		}
		if !seen[f.show] {
			seen[f.show] = true
			last = append(last, f)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, f := range last {
		if inProgressShows[f.show] || len(out) >= 40 {
			continue
		}
		next, err := c.FollowingEpisode(ctx, pol, userID, f.ep)
		if err != nil {
			return nil, err
		}
		if next == nil || next.Watch.Played || next.Watch.PositionMs > 0 {
			continue
		}
		out = append(out, recent{id: next.ID, at: f.at})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at > out[j].at })
	if len(out) > 20 {
		out = out[:20]
	}
	ids := make([]int64, len(out))
	for i, r := range out {
		ids[i] = r.id
	}
	return ids, nil
}
