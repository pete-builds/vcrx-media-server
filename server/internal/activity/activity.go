// Package activity reads the playback log the way Tautulli reads Plex: what is
// playing now, who watched what, and totals by title, user, device and time.
// Every row comes from playback_sessions, which the playback service writes
// for each play request, refusals included.
package activity

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"
)

// CountedPlayMs is how much of a session must actually play before it counts
// as a play in stats. A session opened and closed in a few seconds is a misclick.
const CountedPlayMs = 30_000

// WatchedFraction matches the playback service: past 90% is watched.
const WatchedFraction = 0.90

var ErrNotFound = errors.New("session not found")

type Service struct {
	DB *sql.DB
	// Now and Location exist for tests. Location sets day and hour buckets.
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

// Session is one playback session as Activity shows it.
type Session struct {
	ID                string   `json:"id"`
	State             string   `json:"state"`
	Decision          string   `json:"decision"`
	DecisionReason    string   `json:"decision_reason"`
	UserID            *int64   `json:"user_id"`
	Username          *string  `json:"username"`
	ItemID            *int64   `json:"item_id"`
	ItemKind          *string  `json:"item_kind"`
	ItemTitle         *string  `json:"item_title"`
	ShowID            *int64   `json:"show_id"`
	VersionSummary    *string  `json:"version_summary"`
	Bitrate           *int64   `json:"bitrate"`
	DeviceName        *string  `json:"device_name"`
	DeviceModel       *string  `json:"device_model"`
	Platform          *string  `json:"platform"`
	Client            *string  `json:"client"`
	ClientVersion     *string  `json:"client_version"`
	IP                *string  `json:"ip"`
	StartPositionMs   int64    `json:"start_position_ms"`
	PositionMs        int64    `json:"position_ms"`
	DurationMs        *int64   `json:"duration_ms"`
	Progress          *float64 `json:"progress"`
	PlayedMs          int64    `json:"played_ms"`
	Watched           bool     `json:"watched"`
	StartedAt         int64    `json:"started_at"`
	LastEventAt       int64    `json:"last_event_at"`
	EndedAt           *int64   `json:"ended_at"`
	TerminatedMessage *string  `json:"terminated_message"`
}

const sessionColumns = `ps.id, ps.state, ps.decision, ps.decision_reason, ps.user_id, ps.username, ps.item_id, ps.item_kind,
	ps.item_title, CASE WHEN ps.item_kind = 'episode' THEN sh.id END, ps.version_summary, ps.bitrate, ps.device_name,
	ps.device_model, ps.platform, ps.client, ps.client_version, ps.ip, ps.start_position_ms, ps.last_position_ms,
	ps.duration_ms, ps.played_ms, ps.started_at, ps.last_event_at, ps.ended_at, ps.terminated_message`

// sessionJoins reach an episode's show for its poster and for show totals.
const sessionJoins = ` FROM playback_sessions ps
	LEFT JOIN items ep ON ep.id = ps.item_id
	LEFT JOIN items se ON se.id = ep.parent_id
	LEFT JOIN items sh ON sh.id = se.parent_id`

func scanSessions(rows *sql.Rows) ([]Session, error) {
	defer func() { _ = rows.Close() }()
	out := []Session{}
	for rows.Next() {
		var x Session
		var uid, iid, show, bitrate, dur, ended sql.NullInt64
		var uname, kind, title, vsum, dname, dmodel, platform, client, cver, ip, term sql.NullString
		if err := rows.Scan(&x.ID, &x.State, &x.Decision, &x.DecisionReason, &uid, &uname, &iid, &kind, &title, &show, &vsum,
			&bitrate, &dname, &dmodel, &platform, &client, &cver, &ip, &x.StartPositionMs, &x.PositionMs, &dur, &x.PlayedMs,
			&x.StartedAt, &x.LastEventAt, &ended, &term); err != nil {
			return nil, err
		}
		x.UserID, x.ItemID, x.ShowID, x.Bitrate, x.DurationMs, x.EndedAt = i64(uid), i64(iid), i64(show), i64(bitrate), i64(dur), i64(ended)
		x.Username, x.ItemKind, x.ItemTitle, x.VersionSummary = str(uname), str(kind), str(title), str(vsum)
		x.DeviceName, x.DeviceModel, x.Platform, x.Client = str(dname), str(dmodel), str(platform), str(client)
		x.ClientVersion, x.IP, x.TerminatedMessage = str(cver), str(ip), str(term)
		if x.DurationMs != nil && *x.DurationMs > 0 {
			p := min(1, float64(x.PositionMs)/float64(*x.DurationMs))
			x.Progress = &p
			x.Watched = p >= WatchedFraction
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// NowPlaying is every open session, with totals for the header.
type NowPlaying struct {
	Sessions     []Session `json:"sessions"`
	StreamCount  int       `json:"stream_count"`
	DirectPlay   int       `json:"direct_play"`
	Converting   int       `json:"converting"`
	BandwidthBps int64     `json:"bandwidth_bps"`
}

// Now lists sessions that have not ended. Sessions that never got past
// "created" are left out after a minute: the client asked and never played.
func (s *Service) NowPlaying(ctx context.Context) (NowPlaying, error) {
	cutoff := s.now().Add(-time.Minute).UnixMilli()
	rows, err := s.DB.QueryContext(ctx, `SELECT `+sessionColumns+sessionJoins+`
		WHERE ps.ended_at IS NULL AND ps.decision != 'refused' AND (ps.state != 'created' OR ps.started_at >= ?)
		ORDER BY ps.started_at`, cutoff)
	if err != nil {
		return NowPlaying{}, err
	}
	list, err := scanSessions(rows)
	if err != nil {
		return NowPlaying{}, err
	}
	out := NowPlaying{Sessions: list, StreamCount: len(list)}
	for _, x := range list {
		if x.Decision == "direct_play" {
			out.DirectPlay++
		} else {
			out.Converting++
		}
		if x.Bitrate != nil && x.State != "paused" {
			out.BandwidthBps += *x.Bitrate
		}
	}
	return out, nil
}

// HistoryFilter narrows History. Zero values mean no filter.
type HistoryFilter struct {
	UserID         int64
	ItemID         int64 // a movie, an episode, or a show (all its episodes)
	IncludeRefused bool
	Limit, Offset  int
}

type History struct {
	Sessions []Session `json:"sessions"`
	Total    int       `json:"total"`
}

// History lists ended sessions, newest first.
func (s *Service) History(ctx context.Context, f HistoryFilter) (History, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 50
	}
	f.Offset = max(0, f.Offset)
	where := []string{"ps.ended_at IS NOT NULL"}
	var args []any
	if !f.IncludeRefused {
		where = append(where, "ps.decision != 'refused'", "(ps.played_ms > 0 OR ps.last_position_ms != ps.start_position_ms)")
	}
	if f.UserID > 0 {
		where = append(where, "ps.user_id = ?")
		args = append(args, f.UserID)
	}
	if f.ItemID > 0 {
		where = append(where, "(ps.item_id = ? OR sh.id = ?)")
		args = append(args, f.ItemID, f.ItemID)
	}
	cond := " WHERE " + strings.Join(where, " AND ")
	var out History
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*)`+sessionJoins+cond, args...).Scan(&out.Total); err != nil {
		return out, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+sessionColumns+sessionJoins+cond+` ORDER BY ps.started_at DESC LIMIT ? OFFSET ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return out, err
	}
	out.Sessions, err = scanSessions(rows)
	return out, err
}

// Ranked is one title or user in a top list.
type Ranked struct {
	ID      *int64 `json:"id"`
	Name    string `json:"name"`
	Plays   int    `json:"plays"`
	WatchMs int64  `json:"watch_ms"`
	Users   int    `json:"users"`
}

type DayCount struct {
	Date    string `json:"date"`
	Plays   int    `json:"plays"`
	WatchMs int64  `json:"watch_ms"`
}

type Stats struct {
	Days      int        `json:"days"`
	Since     int64      `json:"since"`
	Plays     int        `json:"plays"`
	WatchMs   int64      `json:"watch_ms"`
	Users     int        `json:"users"`
	Refused   int        `json:"refused"`
	TopMovies []Ranked   `json:"top_movies"`
	TopShows  []Ranked   `json:"top_shows"`
	TopUsers  []Ranked   `json:"top_users"`
	Platforms []Ranked   `json:"platforms"`
	Devices   []Ranked   `json:"devices"`
	Decisions []Ranked   `json:"decisions"`
	ByDay     []DayCount `json:"by_day"`
	ByHour    [24]int    `json:"by_hour"`
	ByWeekday [7]int     `json:"by_weekday"`
}

type statRow struct {
	userID                sql.NullInt64
	username, title, kind sql.NullString
	itemID, showID        sql.NullInt64
	showTitle             sql.NullString
	platform, device      sql.NullString
	decision              string
	playedMs, startedAt   int64
}

// Stats totals the last days days (1 to 3650) of counted plays.
func (s *Service) Stats(ctx context.Context, days int) (Stats, error) {
	if days <= 0 || days > 3650 {
		days = 30
	}
	loc := s.loc()
	now := s.now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(days - 1))
	st := Stats{Days: days, Since: start.UnixMilli()}
	rows, err := s.DB.QueryContext(ctx, `SELECT ps.user_id, ps.username, ps.item_title, ps.item_kind, ps.item_id, sh.id, sh.title,
			COALESCE(NULLIF(ps.client, ''), ps.platform), ps.device_name, ps.decision, ps.played_ms, ps.started_at`+sessionJoins+`
		WHERE ps.started_at >= ?`, st.Since)
	if err != nil {
		return st, err
	}
	defer func() { _ = rows.Close() }()

	type acc struct {
		r     Ranked
		users map[int64]bool
	}
	movies, shows, users, platforms, devices, decisions := map[string]*acc{}, map[string]*acc{}, map[string]*acc{}, map[string]*acc{}, map[string]*acc{}, map[string]*acc{}
	add := func(m map[string]*acc, key string, id *int64, name string, uid int64, ms int64) {
		a := m[key]
		if a == nil {
			a = &acc{r: Ranked{ID: id, Name: name}, users: map[int64]bool{}}
			m[key] = a
		}
		a.r.Plays++
		a.r.WatchMs += ms
		a.users[uid] = true
	}
	byDay := map[string]*DayCount{}
	allUsers := map[int64]bool{}
	for rows.Next() {
		var r statRow
		if err := rows.Scan(&r.userID, &r.username, &r.title, &r.kind, &r.itemID, &r.showID, &r.showTitle, &r.platform, &r.device,
			&r.decision, &r.playedMs, &r.startedAt); err != nil {
			return st, err
		}
		add(decisions, r.decision, nil, r.decision, r.userID.Int64, 0)
		if r.decision == "refused" {
			st.Refused++
			continue
		}
		if r.playedMs < CountedPlayMs {
			continue
		}
		uid := r.userID.Int64
		st.Plays++
		st.WatchMs += r.playedMs
		allUsers[uid] = true
		add(users, "u"+r.username.String, i64(r.userID), orUnknown(r.username), uid, r.playedMs)
		add(platforms, r.platform.String, nil, orUnknown(r.platform), uid, r.playedMs)
		add(devices, r.device.String, nil, orUnknown(r.device), uid, r.playedMs)
		switch {
		case r.kind.String == "episode" && r.showID.Valid:
			add(shows, "s"+r.showTitle.String, i64(r.showID), r.showTitle.String, uid, r.playedMs)
		case r.kind.String == "movie":
			key := "m" + r.title.String
			if r.itemID.Valid {
				key = "i" + strconv.FormatInt(r.itemID.Int64, 10)
			}
			add(movies, key, i64(r.itemID), orUnknown(r.title), uid, r.playedMs)
		}
		t := time.UnixMilli(r.startedAt).In(loc)
		d := t.Format("2006-01-02")
		if byDay[d] == nil {
			byDay[d] = &DayCount{Date: d}
		}
		byDay[d].Plays++
		byDay[d].WatchMs += r.playedMs
		st.ByHour[t.Hour()]++
		st.ByWeekday[int(t.Weekday())]++
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	st.Users = len(allUsers)
	top := func(m map[string]*acc, n int) []Ranked {
		out := make([]Ranked, 0, len(m))
		for _, a := range m {
			a.r.Users = len(a.users)
			out = append(out, a.r)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Plays != out[j].Plays {
				return out[i].Plays > out[j].Plays
			}
			if out[i].WatchMs != out[j].WatchMs {
				return out[i].WatchMs > out[j].WatchMs
			}
			return out[i].Name < out[j].Name
		})
		if n > 0 && len(out) > n {
			out = out[:n]
		}
		return out
	}
	st.TopMovies, st.TopShows, st.TopUsers = top(movies, 10), top(shows, 10), top(users, 10)
	st.Platforms, st.Devices, st.Decisions = top(platforms, 10), top(devices, 10), top(decisions, 0)
	// Every day in the window, zeros included, so a chart has no gaps.
	for d := start; !d.After(now); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		if c := byDay[k]; c != nil {
			st.ByDay = append(st.ByDay, *c)
		} else {
			st.ByDay = append(st.ByDay, DayCount{Date: k})
		}
	}
	return st, nil
}

// UserStat is one account's all-time activity.
type UserStat struct {
	UserID     int64   `json:"user_id"`
	Username   string  `json:"username"`
	IsAdmin    bool    `json:"is_admin"`
	Plays      int     `json:"plays"`
	WatchMs    int64   `json:"watch_ms"`
	LastSeen   *int64  `json:"last_seen"`
	LastTitle  *string `json:"last_title"`
	LastDevice *string `json:"last_device"`
	LastIP     *string `json:"last_ip"`
}

// Users lists every account with its play totals and its latest session.
func (s *Service) Users(ctx context.Context) ([]UserStat, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT u.id, u.username, u.is_admin,
			(SELECT COUNT(*) FROM playback_sessions p WHERE p.user_id = u.id AND p.decision != 'refused' AND p.played_ms >= ?),
			(SELECT COALESCE(SUM(p.played_ms), 0) FROM playback_sessions p WHERE p.user_id = u.id AND p.decision != 'refused' AND p.played_ms >= ?),
			l.last_event_at, l.item_title, l.device_name, l.ip
		FROM users u
		LEFT JOIN playback_sessions l ON l.id = (SELECT id FROM playback_sessions p WHERE p.user_id = u.id ORDER BY p.last_event_at DESC LIMIT 1)
		ORDER BY u.username`, CountedPlayMs, CountedPlayMs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []UserStat{}
	for rows.Next() {
		var u UserStat
		var seen sql.NullInt64
		var title, device, ip sql.NullString
		if err := rows.Scan(&u.UserID, &u.Username, &u.IsAdmin, &u.Plays, &u.WatchMs, &seen, &title, &device, &ip); err != nil {
			return nil, err
		}
		u.LastSeen, u.LastTitle, u.LastDevice, u.LastIP = i64(seen), str(title), str(device), str(ip)
		out = append(out, u)
	}
	return out, rows.Err()
}

// Terminate ends an open session with a message for the viewer. It returns
// ErrNotFound when there is no open session with that id.
func (s *Service) Terminate(ctx context.Context, sessionID, by, message string) (Session, error) {
	if strings.TrimSpace(message) == "" {
		message = "An administrator stopped this stream."
	}
	now := s.now().UnixMilli()
	res, err := s.DB.ExecContext(ctx, `UPDATE playback_sessions SET state = 'stopped', ended_at = ?, last_event_at = ?,
		terminated_message = ?, terminated_by = ? WHERE id = ? AND ended_at IS NULL`, now, now, message, by, sessionID)
	if err != nil {
		return Session{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Session{}, ErrNotFound
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+sessionColumns+sessionJoins+` WHERE ps.id = ?`, sessionID)
	if err != nil {
		return Session{}, err
	}
	list, err := scanSessions(rows)
	if err != nil || len(list) == 0 {
		return Session{}, err
	}
	return list[0], nil
}

func i64(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

func str(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

func orUnknown(s sql.NullString) string {
	if !s.Valid || strings.TrimSpace(s.String) == "" {
		return "Unknown"
	}
	return s.String
}
