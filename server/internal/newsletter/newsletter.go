// Package newsletter sends a weekly digest of what was added, the way
// Tautulli's newsletter does: new movies, and new episodes grouped by show,
// posted through the playback webhooks at a day and hour the admin picks.
// It is off until switched on in Settings.
package newsletter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/notify"
	"github.com/pete-builds/vcrx-media-server/server/internal/tmdb"
)

// Keys in the settings table. The last-sent time lives in the database, not in
// memory, so a restart never sends the same week twice.
const (
	settingsKey = "newsletter.settings"
	lastSentKey = "newsletter.last_sent"
)

// Window is how far back a digest looks.
const Window = 7 * 24 * time.Hour

// Settings are what the admin picks: on or off, and when, in the server's time zone.
type Settings struct {
	Enabled bool `json:"enabled"`
	Weekday int  `json:"weekday"` // 0 is Sunday
	Hour    int  `json:"hour"`    // 0 to 23
}

// Default is Friday at 17:00, ahead of the weekend.
var Default = Settings{Weekday: int(time.Friday), Hour: 17}

func (st Settings) Validate() error {
	if st.Weekday < 0 || st.Weekday > 6 {
		return errors.New("weekday must be 0 (Sunday) to 6 (Saturday)")
	}
	if st.Hour < 0 || st.Hour > 23 {
		return errors.New("hour must be 0 to 23")
	}
	return nil
}

type Service struct {
	DB    *sql.DB
	Hooks *notify.Webhooks
	Log   *slog.Logger
	// Now and Location exist for tests. Location is the server's time zone.
	Now      func() time.Time
	Location *time.Location
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) loc() *time.Location {
	if s.Location != nil {
		return s.Location
	}
	return time.Local
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Settings reads the saved settings, or the defaults (off) when none are saved.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	st := Default
	v, err := s.get(ctx, settingsKey)
	if err != nil || v == "" {
		return st, err
	}
	err = json.Unmarshal([]byte(v), &st)
	return st, err
}

// Save stores new settings. It also marks now as sent, so a change only takes
// effect from the next scheduled time: switching it on on a Wednesday for
// Mondays must not send last Monday's digest straight away.
func (s *Service) Save(ctx context.Context, st Settings) error {
	if err := st.Validate(); err != nil {
		return err
	}
	b, _ := json.Marshal(st)
	if err := s.set(ctx, settingsKey, string(b)); err != nil {
		return err
	}
	return s.set(ctx, lastSentKey, strconv.FormatInt(s.now().UnixMilli(), 10))
}

// LastSent is when the last scheduled digest was handled (sent, or skipped as
// empty), in unix milliseconds; zero means never.
func (s *Service) LastSent(ctx context.Context) (int64, error) {
	v, err := s.get(ctx, lastSentKey)
	if err != nil || v == "" {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

// Status is what Settings shows.
type Status struct {
	Settings
	TimeZone string `json:"time_zone"`
	Webhooks int    `json:"webhooks"`
	LastSent *int64 `json:"last_sent"`
	NextSend *int64 `json:"next_send"`
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	st, err := s.Settings(ctx)
	if err != nil {
		return Status{}, err
	}
	out := Status{Settings: st, TimeZone: s.zoneName()}
	if s.Hooks != nil {
		out.Webhooks = len(s.Hooks.URLs)
	}
	last, err := s.LastSent(ctx)
	if err != nil {
		return out, err
	}
	if last > 0 {
		out.LastSent = &last
	}
	if st.Enabled {
		next := LastSlot(s.now(), st, s.loc()).AddDate(0, 0, 7)
		if prev := LastSlot(s.now(), st, s.loc()); prev.UnixMilli() > last {
			next = prev // due now; the next tick sends it
		}
		n := next.UnixMilli()
		out.NextSend = &n
	}
	return out, nil
}

// zoneName is the TZ setting when there is one, else the zone's abbreviation.
func (s *Service) zoneName() string {
	if s.Location != nil {
		return s.Location.String()
	}
	if tz := os.Getenv("TZ"); tz != "" {
		return tz
	}
	name, _ := s.now().In(s.loc()).Zone()
	return name
}

// LastSlot is the most recent scheduled time at or before now.
func LastSlot(now time.Time, st Settings, loc *time.Location) time.Time {
	n := now.In(loc)
	slot := time.Date(n.Year(), n.Month(), n.Day(), st.Hour, 0, 0, 0, loc)
	slot = slot.AddDate(0, 0, -((int(n.Weekday()) - st.Weekday + 7) % 7))
	if slot.After(n) {
		slot = slot.AddDate(0, 0, -7)
	}
	return slot
}

// Tick sends this week's digest if it is due and has not gone out yet. It
// reports whether a digest was sent.
func (s *Service) Tick(ctx context.Context) (bool, error) {
	st, err := s.Settings(ctx)
	if err != nil || !st.Enabled || s.Hooks == nil || len(s.Hooks.URLs) == 0 {
		return false, err
	}
	last, err := s.LastSent(ctx)
	if err != nil {
		return false, err
	}
	due := LastSlot(s.now(), st, s.loc())
	if last >= due.UnixMilli() {
		return false, nil
	}
	// Claim the week before sending. If the process dies mid-send the week is
	// missed rather than sent twice; if every webhook fails, the claim is
	// undone so the next tick tries again.
	if err := s.set(ctx, lastSentKey, strconv.FormatInt(due.UnixMilli(), 10)); err != nil {
		return false, err
	}
	d, err := Build(ctx, s.DB, due.Add(-Window), due)
	if err != nil {
		return false, s.undo(ctx, last, err)
	}
	if d.Empty() {
		s.log().Info("newsletter: nothing was added this week, so nothing was sent")
		return false, nil
	}
	if n, err := s.Hooks.SendDigest(ctx, d); n == 0 {
		return false, s.undo(ctx, last, err)
	}
	s.log().Info("newsletter sent", "movies", len(d.Movies), "episodes", d.Episodes(), "shows", len(d.Shows))
	return true, nil
}

func (s *Service) undo(ctx context.Context, last int64, cause error) error {
	if err := s.set(ctx, lastSentKey, strconv.FormatInt(last, 10)); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

// Run checks once a minute until ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if _, err := s.Tick(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("newsletter", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ErrNothingAdded is Preview's answer when the last week added nothing.
var ErrNothingAdded = errors.New("nothing was added in the last 7 days")

// Preview sends the last seven days' digest now, marked as a preview. It works
// while the newsletter is off and never counts as this week's send.
func (s *Service) Preview(ctx context.Context) (notify.Digest, int, error) {
	now := s.now()
	d, err := Build(ctx, s.DB, now.Add(-Window), now)
	if err != nil {
		return d, 0, err
	}
	if d.Empty() {
		return d, 0, ErrNothingAdded
	}
	d.Preview = true
	n, err := s.Hooks.SendDigest(ctx, d)
	return d, n, err
}

// Build collects the movies and episodes added in [since, until). Shows are
// ordered by their newest episode, and their episodes by season and number.
func Build(ctx context.Context, db *sql.DB, since, until time.Time) (notify.Digest, error) {
	d := notify.Digest{Since: since.UnixMilli(), Until: until.UnixMilli(), Movies: []notify.DigestMovie{}, Shows: []notify.DigestShow{}}
	rows, err := db.QueryContext(ctx, `SELECT i.id, i.kind, i.title, i.year, i.season_number, i.episode_number, i.episode_number_end,
			i.added_at, sh.id, sh.title,
			(SELECT a.url FROM artwork a WHERE a.item_id = COALESCE(sh.id, i.id) AND a.kind = 'poster' AND a.source = 'tmdb')
		FROM items i
		LEFT JOIN items se ON se.id = i.parent_id AND i.kind = 'episode'
		LEFT JOIN items sh ON sh.id = se.parent_id
		WHERE i.kind IN ('movie', 'episode') AND i.missing_since IS NULL AND i.added_at >= ? AND i.added_at < ?
			AND (i.kind = 'movie' OR (sh.id IS NOT NULL AND sh.missing_since IS NULL))
		ORDER BY i.added_at DESC, i.id`, since.UnixMilli(), until.UnixMilli())
	if err != nil {
		return d, err
	}
	defer func() { _ = rows.Close() }()
	shows := map[int64]int{} // show id to its index in d.Shows
	for rows.Next() {
		var id, addedAt int64
		var kind, title string
		var year, season, ep, epEnd, showID sql.NullInt64
		var showTitle, poster sql.NullString
		if err := rows.Scan(&id, &kind, &title, &year, &season, &ep, &epEnd, &addedAt, &showID, &showTitle, &poster); err != nil {
			return d, err
		}
		if kind == "movie" {
			d.Movies = append(d.Movies, notify.DigestMovie{ID: id, Title: title, Year: int(year.Int64), PosterURL: PosterURL(poster.String), AddedAt: addedAt})
			continue
		}
		i, ok := shows[showID.Int64]
		if !ok {
			i = len(d.Shows)
			shows[showID.Int64] = i
			d.Shows = append(d.Shows, notify.DigestShow{ID: showID.Int64, Title: showTitle.String, PosterURL: PosterURL(poster.String)})
		}
		d.Shows[i].Episodes = append(d.Shows[i].Episodes, notify.DigestEpisode{ID: id, Season: int(season.Int64), Episode: int(ep.Int64),
			EpisodeEnd: int(epEnd.Int64), Title: episodeTitle(title), AddedAt: addedAt})
	}
	if err := rows.Err(); err != nil {
		return d, err
	}
	for _, sh := range d.Shows {
		sort.SliceStable(sh.Episodes, func(i, j int) bool {
			a, b := sh.Episodes[i], sh.Episodes[j]
			if a.Season != b.Season {
				return a.Season < b.Season
			}
			return a.Episode < b.Episode
		})
	}
	return d, nil
}

// numberOnly matches the title a scan gives an episode without metadata
// ("Episode 3", "Episodes 3-4"), which only repeats the code beside it.
var numberOnly = regexp.MustCompile(`^Episodes? \d+(-\d+)?$`)

func episodeTitle(title string) string {
	if numberOnly.MatchString(strings.TrimSpace(title)) {
		return ""
	}
	return title
}

// PosterURL turns a stored TMDb image path into a link a chat service can
// load. The server's own poster route needs a sign-in, so local-only posters
// have no link.
func PosterURL(path string) string {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return ""
	}
	return tmdb.DefaultImageURL + "/w342" + path
}

func (s *Service) get(ctx context.Context, key string) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Service) set(ctx context.Context, key, value string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, key, value, s.now().UnixMilli())
	return err
}
