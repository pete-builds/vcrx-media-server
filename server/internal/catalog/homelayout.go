package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// RowSlot is one kind of Home row, as the Customize Home editor lists it. Some
// slots produce several rows: one per library, per active season, per genre
// of the day or per collection.
type RowSlot struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// DefaultOn is whether the slot shows when nobody has chosen otherwise.
	DefaultOn bool `json:"-"`
}

// HomeSlots is the default order of Home, close to Plex's: what you are in the
// middle of, what is new, then what is popular, then ways in by taste.
var HomeSlots = []RowSlot{
	{"continue_watching", "Continue Watching", "Titles in progress and the next episode of shows you are watching.", true},
	{"recently_added_movies", "Recently Added Movies", "The newest films, one row per movie library.", true},
	{"recently_added_tv", "Recently Added TV", "Shows with new episodes, one row per TV library.", true},
	{"seasonal", "Holiday and Seasonal Picks", "Halloween, Thanksgiving, Christmas, Valentine's Day and summer, in their season.", true},
	{"new_seasons", "New Seasons", "Shows you watch that have a season added since you last watched.", true},
	{"because_you_watched", "Because You Watched", "Titles like the ones you finished most recently.", true},
	{"trending", "Trending on This Server", "Most played by everyone here over the last week.", true},
	{"recently_released", "Recently Released", "Films by release date, newest first.", true},
	{"top_rated_movies", "Top Rated Movies", "The best rated films, by IMDb or TMDb with enough votes.", true},
	{"top_rated_shows", "Top Rated Shows", "The best rated shows, by IMDb or TMDb with enough votes.", true},
	{"unwatched_gems", "Unwatched Gems", "Highly rated titles you have never played.", true},
	{"genres", "Genre Rows", "A few genres, changing every day.", true},
	{"collections", "Collections", "Collections set to show on Home.", true},
	{"most_watched", "Most Watched", "Most played by everyone here, all time.", true},
	{"most_watched_by_you", "Most Watched by You", "What you have played most, all time.", true},
	{"rediscover", "Rediscover", "Highly rated titles you watched more than a year ago.", true},
	{"best_of_decade", "Best of a Decade", "The best rated films of one decade, changing every day.", true},
	{"tmdb_trending", "Trending on TMDb", "TMDb's trending titles this week that are in your library. Needs tmdb_home_lists in vcrx.yaml.", true},
	{"tmdb_popular", "Popular on TMDb", "TMDb's popular titles that are in your library. Needs tmdb_home_lists in vcrx.yaml.", true},
}

func slotByKey(key string) (RowSlot, bool) {
	for _, s := range HomeSlots {
		if s.Key == key {
			return s, true
		}
	}
	return RowSlot{}, false
}

// LayoutEntry is one slot in a saved Home layout.
type LayoutEntry struct {
	Key     string `json:"key"`
	Enabled bool   `json:"enabled"`
}

// LayoutRow is a slot as the editor shows it.
type LayoutRow struct {
	RowSlot
	Enabled bool `json:"enabled"`
}

// Layout is the caller's Home layout: every slot in order with whether it
// shows. Source says where it came from: "user" (their own choice), "server"
// (the admin's default) or "default" (built in).
type Layout struct {
	Rows   []LayoutRow `json:"rows"`
	Source string      `json:"source"`
}

const serverLayoutKey = "home_layout"

func userLayoutKey(userID int64) string { return "home_layout:user:" + strconv.FormatInt(userID, 10) }

// ErrBadLayout is a layout naming a row that does not exist or naming one twice.
var ErrBadLayout = errors.New("bad layout")

// normalize puts every known slot into the layout exactly once: saved entries
// first in their order, unknown keys dropped, and slots added since (by a
// newer server) appended with their default.
func normalize(saved []LayoutEntry) []LayoutRow {
	out := make([]LayoutRow, 0, len(HomeSlots))
	seen := map[string]bool{}
	for _, e := range saved {
		s, ok := slotByKey(e.Key)
		if !ok || seen[e.Key] {
			continue
		}
		seen[e.Key] = true
		out = append(out, LayoutRow{RowSlot: s, Enabled: e.Enabled})
	}
	for _, s := range HomeSlots {
		if !seen[s.Key] {
			out = append(out, LayoutRow{RowSlot: s, Enabled: s.DefaultOn})
		}
	}
	return out
}

func (c *Catalog) readLayout(ctx context.Context, key string) ([]LayoutEntry, bool, error) {
	var raw string
	err := c.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var entries []LayoutEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		// A damaged value falls back to the next layout rather than breaking Home.
		return nil, false, nil //nolint:nilerr // see above
	}
	return entries, true, nil
}

// HomeLayout is the layout Home uses for a user: their own, else the server
// default, else the built-in order.
func (c *Catalog) HomeLayout(ctx context.Context, userID int64) (Layout, error) {
	if userID > 0 {
		if e, ok, err := c.readLayout(ctx, userLayoutKey(userID)); err != nil || ok {
			return Layout{Rows: normalize(e), Source: "user"}, err
		}
	}
	return c.ServerHomeLayout(ctx)
}

// ServerHomeLayout is the admin's default for everyone without their own.
func (c *Catalog) ServerHomeLayout(ctx context.Context) (Layout, error) {
	e, ok, err := c.readLayout(ctx, serverLayoutKey)
	if err != nil {
		return Layout{}, err
	}
	if ok {
		return Layout{Rows: normalize(e), Source: "server"}, nil
	}
	return Layout{Rows: normalize(nil), Source: "default"}, nil
}

// SetHomeLayout saves a user's layout; userID 0 saves the server default.
// Every entry must name a known slot, once.
func (c *Catalog) SetHomeLayout(ctx context.Context, userID int64, entries []LayoutEntry) error {
	seen := map[string]bool{}
	for _, e := range entries {
		if _, ok := slotByKey(e.Key); !ok {
			return fmt.Errorf("%w: no Home row %q", ErrBadLayout, e.Key)
		}
		if seen[e.Key] {
			return fmt.Errorf("%w: %q is listed twice", ErrBadLayout, e.Key)
		}
		seen[e.Key] = true
	}
	key := serverLayoutKey
	if userID > 0 {
		key = userLayoutKey(userID)
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	_, err = c.DB.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, string(b), time.Now().UnixMilli())
	return err
}

// ResetHomeLayout forgets a user's own layout, so the server default applies
// again; userID 0 forgets the server default, so the built-in order applies.
func (c *Catalog) ResetHomeLayout(ctx context.Context, userID int64) error {
	key := serverLayoutKey
	if userID > 0 {
		key = userLayoutKey(userID)
	}
	_, err := c.DB.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	return err
}
