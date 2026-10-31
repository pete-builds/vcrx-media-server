package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"

	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// Extra is a local extra (trailer, featurette, deleted scene...) in an item's details.
type Extra struct {
	ID         int64   `json:"id"`
	Kind       string  `json:"kind"`
	Title      string  `json:"title"`
	DurationMs *int64  `json:"duration_ms"`
	Container  *string `json:"container"`
	Resolution *string `json:"resolution"`
}

// Trailer is the one a Trailer button plays: a local file first, else the
// YouTube trailer TMDb lists, when the admin has turned that on.
type Trailer struct {
	Source string `json:"source"` // "local" or "youtube"
	Title  string `json:"title"`
	// ExtraID is set for a local trailer; play it like any extra.
	ExtraID    *int64 `json:"extra_id"`
	DurationMs *int64 `json:"duration_ms"`
	// YouTubeKey is set for a YouTube trailer: the video id, always 11 characters
	// from YouTube's URL alphabet, so clients can build a URL or intent from it.
	YouTubeKey *string `json:"youtube_key"`
}

// ThemeMusic is a show's theme.mp3, for clients to play quietly on its page.
type ThemeMusic struct {
	ID  int64  `json:"id"`
	URL string `json:"url"`
}

// YouTubeKeyRe is the only shape of key VCRX stores or serves. Anything else
// from TMDb is dropped, so a key can never smuggle a path or a query into a
// URL, an iframe or an Android intent.
var YouTubeKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// extraOrder lists trailers first, then the rest the way Plex orders them.
const extraOrder = `CASE x.kind WHEN 'trailer' THEN 0 WHEN 'featurette' THEN 1 WHEN 'behind_the_scenes' THEN 2
	WHEN 'deleted_scene' THEN 3 WHEN 'interview' THEN 4 WHEN 'scene' THEN 5 WHEN 'short' THEN 6 ELSE 7 END`

// extraCond is a condition on an item_extras alias: present, and within the
// user's resolution ceiling like a version. Theme music has no picture, so the
// ceiling does not apply to it. The item's own visibility is checked separately.
func extraCond(pol policy.Policy, x string) (string, []any) {
	vc, args := pol.VersionCond(x)
	return "(" + x + ".missing_since IS NULL AND (" + x + ".kind = 'theme' OR " + vc + "))", args
}

// extras fills an item's extras, trailer and theme music. The caller has
// already checked that the item is visible.
func (c *Catalog) extras(ctx context.Context, pol policy.Policy, d *ItemDetail) error {
	d.Extras = []Extra{}
	if d.Kind != "movie" && d.Kind != "show" {
		return nil
	}
	cond, args := extraCond(pol, "x")
	// A lent movie's extras go with the tape: only its borrower sees them.
	lc, largs := pol.LoanCond("i")
	rows, err := c.DB.QueryContext(ctx, `SELECT x.id, x.kind, x.title, x.duration_ms, x.container, x.resolution
		FROM item_extras x JOIN items i ON i.id = x.item_id
		WHERE x.item_id = ? AND `+cond+` AND `+lc+` ORDER BY `+extraOrder+`, x.title = 'Trailer' DESC, x.title COLLATE NOCASE, x.id`,
		append(append([]any{d.ID}, args...), largs...)...)
	if err != nil {
		return err
	}
	err = each(rows, func() error {
		var x Extra
		var dur sql.NullInt64
		var container, res sql.NullString
		if err := rows.Scan(&x.ID, &x.Kind, &x.Title, &dur, &container, &res); err != nil {
			return err
		}
		x.DurationMs, x.Container, x.Resolution = ptr(dur), strPtr(container), strPtr(res)
		if x.Kind == "theme" {
			if d.ThemeMusic == nil {
				d.ThemeMusic = &ThemeMusic{ID: x.ID, URL: "/api/v1/extras/" + strconv.FormatInt(x.ID, 10) + "/file"}
			}
			return nil
		}
		d.Extras = append(d.Extras, x)
		return nil
	})
	if err != nil {
		return err
	}
	for i := range d.Extras {
		if x := d.Extras[i]; x.Kind == "trailer" {
			id := x.ID
			d.Trailer = &Trailer{Source: "local", Title: x.Title, ExtraID: &id, DurationMs: x.DurationMs}
			return nil
		}
	}
	if !c.TrailersYouTube {
		return nil
	}
	var key, name sql.NullString
	err = c.DB.QueryRowContext(ctx, `SELECT t.youtube_key, t.name FROM youtube_trailers t JOIN items i ON i.id = t.item_id
		WHERE t.item_id = ? AND t.youtube_key IS NOT NULL AND t.tmdb_id = i.tmdb_id`, d.ID).Scan(&key, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !YouTubeKeyRe.MatchString(key.String) {
		return nil
	}
	k := key.String
	title := name.String
	if title == "" {
		title = "Trailer"
	}
	d.Trailer = &Trailer{Source: "youtube", Title: title, YouTubeKey: &k}
	return nil
}

// ExtraForPlayback returns an extra as a Version (so the playback decision and
// converted streams treat it like any file), the item it belongs to and its
// library root, if the caller may see that item. Otherwise ErrNotFound.
func (c *Catalog) ExtraForPlayback(ctx context.Context, pol policy.Policy, extraID int64) (ExtraFile, error) {
	cond, cargs := pol.ItemCond("i")
	xc, xargs := extraCond(pol, "x")
	// One loan per copy covers the tape's extras too (policy.LoanCond).
	lc, largs := pol.LoanCond("i")
	cond, cargs = cond+" AND "+lc, append(cargs, largs...)
	var f ExtraFile
	var container, codec, profile, res, hdr sql.NullString
	var bitrate, dur, level, depth, w, h sql.NullInt64
	var fps sql.NullFloat64
	var audio string
	err := c.DB.QueryRowContext(ctx, `SELECT x.id, x.item_id, x.kind, x.title, i.title, x.path, l.root_path, x.size, x.container,
			x.bitrate, x.duration_ms, x.video_codec, x.video_profile, x.video_level, x.bit_depth, x.width, x.height, x.resolution,
			x.frame_rate, x.hdr_type, x.audio_streams, x.probe_status
		FROM item_extras x JOIN items i ON i.id = x.item_id JOIN libraries l ON l.id = x.library_id
		WHERE x.id = ? AND `+xc+` AND `+cond, append(append([]any{extraID}, xargs...), cargs...)...).
		Scan(&f.Version.ID, &f.ItemID, &f.Kind, &f.Title, &f.ItemTitle, &f.Version.Path, &f.Root, &f.Version.Size, &container,
			&bitrate, &dur, &codec, &profile, &level, &depth, &w, &h, &res, &fps, &hdr, &audio, &f.Version.ProbeStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	if err != nil {
		return f, err
	}
	v := &f.Version
	v.FileName, v.ItemID = filepath.Base(v.Path), f.ItemID
	v.Container, v.VideoCodec, v.VideoProfile, v.Resolution, v.HDRType = strPtr(container), strPtr(codec), strPtr(profile), strPtr(res), strPtr(hdr)
	v.Bitrate, v.DurationMs, v.VideoLevel, v.BitDepth, v.Width, v.Height = ptr(bitrate), ptr(dur), ptr(level), ptr(depth), ptr(w), ptr(h)
	if fps.Valid {
		x := fps.Float64
		v.FrameRate = &x
	}
	v.AudioTracks, v.SubtitleTracks, v.VideoTracks = []Track{}, []Track{}, []Track{}
	var streams []struct {
		Index    int64  `json:"index"`
		Codec    string `json:"codec"`
		Language string `json:"language"`
		Channels int64  `json:"channels"`
		Default  bool   `json:"default"`
	}
	_ = json.Unmarshal([]byte(audio), &streams)
	for _, s := range streams {
		t := Track{Kind: "audio", IsDefault: s.Default}
		idx, ch := s.Index, s.Channels
		t.Index = &idx
		if s.Channels > 0 {
			t.Channels = &ch
		}
		t.Codec, t.Language = nonEmpty(s.Codec), nonEmpty(s.Language)
		v.AudioTracks = append(v.AudioTracks, t)
	}
	return f, nil
}

// ExtraFile is an extra ready to play.
type ExtraFile struct {
	Version   Version
	ItemID    int64
	Kind      string
	Title     string
	ItemTitle string
	Root      string
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ExtraOnLoan reports whether an extra the caller could otherwise see belongs
// to a movie out on loan to someone else, and when it is due back, so starting
// it is refused as the film is ("Out on loan") rather than with a bare 404.
func (c *Catalog) ExtraOnLoan(ctx context.Context, pol policy.Policy, extraID int64) (bool, int64, error) {
	var item int64
	err := c.DB.QueryRowContext(ctx, `SELECT item_id FROM item_extras WHERE id = ?`, extraID).Scan(&item)
	if errors.Is(err, sql.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	if ok, err := c.Visible(ctx, pol, item); err != nil || !ok {
		return false, 0, err
	}
	return c.OnLoan(ctx, pol, item)
}
