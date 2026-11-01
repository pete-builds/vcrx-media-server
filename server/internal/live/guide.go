package live

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Program is a guide entry as clients see it. Times are Unix milliseconds.
type Program struct {
	Start       int64    `json:"start"`
	Stop        int64    `json:"stop"`
	Title       string   `json:"title"`
	SubTitle    *string  `json:"sub_title"`
	Description *string  `json:"description"`
	Categories  []string `json:"categories"`
	Episode     *string  `json:"episode"`
}

// ChannelGuide is one channel's programmes in a window.
type ChannelGuide struct {
	Channel
	Programs []Program `json:"programs"`
}

// The guide keeps programmes from a day ago to 14 days ahead, and at most
// maxPrograms from one read.
const (
	guideKeepBehind = 24 * time.Hour
	guideKeepAhead  = 14 * 24 * time.Hour
	maxPrograms     = 200000
	// MaxGuideWindow is the longest span one guide request may ask for.
	MaxGuideWindow = 24 * time.Hour
)

// DefaultGuideInterval is how often guides are read when vcrx.yaml does not say.
const DefaultGuideInterval = 6 * time.Hour

type guideRow struct {
	channelID int64
	p         Programme
}

// GuideURL is a source's guide location, or "" when it has none.
func (s *Store) GuideURL(ctx context.Context, sourceID int64) (string, error) {
	var url sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT guide_url FROM live_sources WHERE id = ?`, sourceID).Scan(&url)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return url.String, err
}

// guideTargets are a source's channels by lowercased tvg-id and by
// lowercased name, for matching a guide to them.
func (s *Store) guideTargets(ctx context.Context, sourceID int64) (byTvg, byName map[string]int64, err error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, tvg_id, name FROM channels WHERE source_id = ? AND gone = 0`, sourceID)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	byTvg, byName = map[string]int64{}, map[string]int64{}
	for rows.Next() {
		var id int64
		var tvg sql.NullString
		var name string
		if err := rows.Scan(&id, &tvg, &name); err != nil {
			return nil, nil, err
		}
		if tvg.Valid && tvg.String != "" {
			byTvg[strings.ToLower(tvg.String)] = id
		}
		if _, dup := byName[strings.ToLower(name)]; !dup {
			byName[strings.ToLower(name)] = id
		}
	}
	return byTvg, byName, rows.Err()
}

// syncGuide replaces a source's programmes with a fresh read.
func (s *Store) syncGuide(ctx context.Context, sourceID int64, rows []guideRow, truncated bool) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM programs WHERE channel_id IN (SELECT id FROM channels WHERE source_id = ?)`, sourceID); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO programs (channel_id, start_ms, stop_ms, title, sub_title, description, categories, episode)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, r := range rows {
		cats, _ := json.Marshal(nonNilStrings(r.p.Categories))
		if _, err := stmt.ExecContext(ctx, r.channelID, r.p.Start.UnixMilli(), r.p.Stop.UnixMilli(), r.p.Title, null(r.p.SubTitle),
			null(r.p.Desc), string(cats), null(r.p.Episode)); err != nil {
			return err
		}
	}
	var msg any
	if truncated {
		msg = fmt.Sprintf("the guide lists more than %d programmes for these channels; only the first %d were kept", maxPrograms, maxPrograms)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE live_sources SET guide_refreshed_at = ?, guide_error = ?, program_count = ? WHERE id = ?`,
		s.now(), msg, len(rows), sourceID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) guideFailed(ctx context.Context, sourceID int64, msg string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE live_sources SET guide_error = ? WHERE id = ?`, clip(msg), sourceID)
	return err
}

// errTooManyPrograms stops a read that has kept maxPrograms already.
var errTooManyPrograms = errors.New("too many programmes")

// RefreshGuide reads one source's guide, if it has one, into the programs
// table. A failure is recorded on the source and leaves the guide as it was.
func (r *Refresher) RefreshGuide(ctx context.Context, sourceID int64) error {
	loc, err := r.Store.GuideURL(ctx, sourceID)
	if err != nil || loc == "" {
		return err
	}
	byTvg, byName, err := r.Store.guideTargets(ctx, sourceID)
	if err != nil {
		return err
	}
	body, err := r.open(ctx, loc, "guide")
	if err != nil {
		_ = r.Store.guideFailed(ctx, sourceID, err.Error())
		return err
	}
	defer func() { _ = body.Close() }()

	now := time.Now()
	if r.Store.Now != nil {
		now = r.Store.Now()
	}
	from, to := now.Add(-guideKeepBehind), now.Add(guideKeepAhead)
	// A guide channel maps to one of ours by its id (our tvg-id), else by one
	// of its display names (our channel name).
	guideTo := map[string]int64{}
	onChannel := func(gc GuideChannel) {
		if id, ok := byTvg[strings.ToLower(gc.ID)]; ok {
			guideTo[gc.ID] = id
			return
		}
		for _, n := range gc.Names {
			if id, ok := byName[strings.ToLower(n)]; ok {
				guideTo[gc.ID] = id
				return
			}
		}
	}
	var rows []guideRow
	truncated := false
	onProgramme := func(p Programme) error {
		id, ok := guideTo[p.Channel]
		if !ok {
			// A guide without <channel> entries: its programmes name tvg-ids directly.
			if id, ok = byTvg[strings.ToLower(p.Channel)]; !ok {
				return nil
			}
		}
		if !p.Stop.After(from) || !p.Start.Before(to) {
			return nil
		}
		if len(rows) == maxPrograms {
			truncated = true
			return errTooManyPrograms
		}
		rows = append(rows, guideRow{channelID: id, p: p})
		return nil
	}
	if err := ParseXMLTV(body, onChannel, onProgramme); err != nil && !errors.Is(err, errTooManyPrograms) {
		_ = r.Store.guideFailed(ctx, sourceID, err.Error())
		return err
	}
	return r.Store.syncGuide(ctx, sourceID, rows, truncated)
}

// open reads a playlist or guide location: a file the admin named, or a URL
// fetched with a bare GET (no cookies, no credentials beyond what the URL
// itself carries). what names it in errors, which never repeat the URL.
func (r *Refresher) open(ctx context.Context, loc, what string) (io.ReadCloser, error) {
	if !strings.HasPrefix(loc, "http://") && !strings.HasPrefix(loc, "https://") {
		f, err := os.Open(loc) //nolint:gosec // the admin names this file in vcrx.yaml
		if err != nil {
			return nil, fmt.Errorf("open the %s file: %w", what, err)
		}
		return f, nil
	}
	client := r.Playlists
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
	if err != nil {
		return nil, fmt.Errorf("the %s URL is not valid", what)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not download the %s: %s", what, redact(err))
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("the %s server answered %s", what, resp.Status)
	}
	return resp.Body, nil
}

// NowNext adds each channel's programme on now and the one after it.
func (s *Store) NowNext(ctx context.Context, channels []Channel) error {
	if len(channels) == 0 {
		return nil
	}
	byID := map[int64]*Channel{}
	for i := range channels {
		byID[channels[i].ID] = &channels[i]
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT channel_id, rn, start_ms, stop_ms, title, sub_title, description, categories, episode FROM (
			SELECT p.*, ROW_NUMBER() OVER (PARTITION BY p.channel_id ORDER BY p.start_ms) AS rn
			FROM programs p JOIN channels c ON c.id = p.channel_id
			WHERE c.enabled = 1 AND c.gone = 0 AND p.stop_ms > ?)
		WHERE rn <= 2 ORDER BY channel_id, rn`, s.now())
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	now := s.now()
	for rows.Next() {
		var chID int64
		var rn int
		p, err := scanProgram(rows, &chID, &rn)
		if err != nil {
			return err
		}
		c := byID[chID]
		if c == nil {
			continue
		}
		switch {
		case rn == 1 && p.Start <= now:
			c.Now = &p
		case c.Now != nil || rn == 1:
			// The first programme still ahead (nothing on now) is next.
			if c.Next == nil {
				c.Next = &p
			}
		}
	}
	return rows.Err()
}

// Guide lists, for each channel a viewer may watch, the programmes that
// overlap [from, to). Without the grant it is empty.
func (s *Store) Guide(ctx context.Context, allowed bool, from, to time.Time) ([]ChannelGuide, error) {
	channels, err := s.Channels(ctx, allowed)
	if err != nil || len(channels) == 0 {
		return []ChannelGuide{}, err
	}
	out := make([]ChannelGuide, len(channels))
	idx := map[int64]int{}
	for i, c := range channels {
		out[i] = ChannelGuide{Channel: c, Programs: []Program{}}
		idx[c.ID] = i
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT p.channel_id, 0, p.start_ms, p.stop_ms, p.title, p.sub_title, p.description, p.categories, p.episode
		FROM programs p JOIN channels c ON c.id = p.channel_id
		WHERE c.enabled = 1 AND c.gone = 0 AND p.stop_ms > ? AND p.start_ms < ?
		ORDER BY p.channel_id, p.start_ms`, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var chID int64
		var rn int
		p, err := scanProgram(rows, &chID, &rn)
		if err != nil {
			return nil, err
		}
		if i, ok := idx[chID]; ok {
			out[i].Programs = append(out[i].Programs, p)
		}
	}
	return out, rows.Err()
}

func scanProgram(rows *sql.Rows, chID *int64, rn *int) (Program, error) {
	var p Program
	var sub, desc, ep sql.NullString
	var cats string
	if err := rows.Scan(chID, rn, &p.Start, &p.Stop, &p.Title, &sub, &desc, &cats, &ep); err != nil {
		return p, err
	}
	p.SubTitle, p.Description, p.Episode = ptr(sub), ptr(desc), ptr(ep)
	if err := json.Unmarshal([]byte(cats), &p.Categories); err != nil || p.Categories == nil {
		p.Categories = []string{}
	}
	return p, nil
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
