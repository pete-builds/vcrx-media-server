package playback

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/notify"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
	"github.com/pete-builds/vcrx-media-server/server/internal/subtitles"
)

var (
	ErrSessionNotFound = errors.New("playback session not found")
	ErrSessionEnded    = errors.New("playback session already ended")
)

// TerminatedError is returned for events on a session an admin stopped. Message
// is what the admin wrote for the viewer.
type TerminatedError struct{ Message string }

func (e *TerminatedError) Error() string { return "playback session terminated: " + e.Message }

// Refusal is a recorded "no": a policy block (403) or a file this server cannot
// adapt for the device yet (422). Message is safe to show on a TV.
type Refusal struct {
	Status  int
	Code    string
	Message string
}

// Client identifies who is asking, for the activity record.
type Client struct {
	UserID        int64
	Username      string
	IsAdmin       bool
	AuthSessionID int64
	DeviceID      string
	DeviceName    string
	DeviceModel   string
	Platform      string
	ClientName    string
	ClientVersion string
	IP            string
}

type StartRequest struct {
	ItemID          int64        `json:"item_id"`
	VersionID       *int64       `json:"version_id"`
	StartPositionMs *int64       `json:"start_position_ms"`
	Capabilities    Capabilities `json:"capabilities"`
}

type Stream struct {
	URL      string  `json:"url"`
	Protocol string  `json:"protocol"`
	MimeType *string `json:"mime_type"`
	// SeekURL, for protocols fmp4 and hls: GET it with position_ms to learn
	// where a stream from there starts and the URL to play. For hls, URL is
	// the seek URL too, since every stream starts at a position.
	SeekURL string `json:"seek_url,omitempty"`
}

type SubtitleSource struct {
	TrackID  int64   `json:"track_id"`
	URL      string  `json:"url"`
	Codec    string  `json:"codec"`
	MimeType string  `json:"mime_type"`
	Language *string `json:"language"`
	Title    *string `json:"title"`
	IsForced bool    `json:"is_forced"`
	// Embedded is true for a track inside the media file. Players that read the
	// container (Media3) already have it; browsers need the WebVTT copy.
	Embedded bool `json:"embedded"`
}

type Session struct {
	SessionID           string           `json:"session_id"`
	Decision            string           `json:"decision"`
	Reason              string           `json:"reason"`
	ItemID              int64            `json:"item_id"`
	Version             catalog.Version  `json:"version"`
	Stream              Stream           `json:"stream"`
	Subtitles           []SubtitleSource `json:"subtitles"`
	WebVTTSubtitles     []SubtitleSource `json:"webvtt_subtitles"`
	ResumePositionMs    int64            `json:"resume_position_ms"`
	HeartbeatIntervalMs int64            `json:"heartbeat_interval_ms"`
}

type Event struct {
	Type           string  `json:"type"`
	PositionMs     int64   `json:"position_ms"`
	FromPositionMs *int64  `json:"from_position_ms"`
	DurationMs     *int64  `json:"duration_ms"`
	Message        *string `json:"message"`
}

// Thresholds for watch state.
const (
	PlayedFraction  = 0.90
	MinResumeMs     = 10_000
	StaleAfter      = 10 * time.Minute
	DefaultInterval = 10 * time.Second
)

type Service struct {
	DB        *sql.DB
	Catalog   *catalog.Catalog
	Heartbeat time.Duration
	// CanTranscode turns on video transcoding to H.264 inside a converted
	// stream, for clients that report fmp4_stream (or hls_stream) and decode
	// H.264. It needs an ffmpeg with libx264. Every other client that needs its
	// video converted (the TV app today) is still refused with a clear message.
	CanTranscode bool
	// CanRemux turns on converted streams (video copied, audio copied or
	// converted) for clients that report fmp4_stream.
	CanRemux bool
	// CanHLS also offers converted streams, written as HLS, to clients that
	// report hls_stream.
	CanHLS bool
	Now    func() time.Time
	// Notify, when set, receives play, stop and refused events for webhooks.
	Notify func(notify.Notice)
}

func (s *Service) notify(n notify.Notice) {
	if s.Notify != nil {
		s.Notify(n)
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Start chooses a version and a decision, records the session, and returns it. A
// refusal is recorded too, and returned with a nil error.
func (s *Service) Start(ctx context.Context, pol policy.Policy, c Client, req StartRequest) (*Session, *Refusal, error) {
	// The item itself first: its library, labels and rating. Checking only the
	// versions would let a friend start a title they cannot see and read its
	// file name and tracks from the answer.
	visible, err := s.Catalog.Visible(ctx, pol, req.ItemID)
	if err != nil {
		return nil, nil, err
	}
	if !visible {
		return nil, nil, catalog.ErrNotFound
	}
	var kind, title string
	err = s.DB.QueryRowContext(ctx, `SELECT kind, title FROM items WHERE id = ?`, req.ItemID).Scan(&kind, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, catalog.ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if kind != "movie" && kind != "episode" {
		return nil, &Refusal{Status: 422, Code: "not_playable", Message: "Choose a movie or an episode to play."}, nil
	}
	versions, err := s.Catalog.Versions(ctx, pol, req.ItemID)
	if err != nil {
		return nil, nil, err
	}
	if len(versions) == 0 {
		// Either it does not exist for this user or every version is above their ceiling.
		return nil, nil, catalog.ErrNotFound
	}
	var v catalog.Version
	var d Decision
	if req.VersionID != nil {
		found := false
		for _, cand := range versions {
			if cand.ID == *req.VersionID {
				v, d, found = cand, Decide(cand, req.Capabilities), true
				break
			}
		}
		if !found {
			return nil, nil, catalog.ErrNotFound
		}
	} else {
		v, d = ChooseVersion(versions, req.Capabilities)
		if len(versions) > 1 {
			d.Reason += fmt.Sprintf(" (picked version %d of %d)", indexOf(versions, v.ID)+1, len(versions))
		}
	}
	if title, err = s.displayTitle(ctx, req.ItemID, kind, title); err != nil {
		return nil, nil, err
	}

	hls := s.CanHLS && req.Capabilities.HLSStream
	converted := s.CanRemux && (req.Capabilities.FMP4Stream || hls) && (((d.Kind == Remux || d.Kind == AudioTranscode) &&
		remux.MP4Video(s2(v.VideoCodec))) || (d.Kind == Transcode && s.CanTranscode && decodesH264(req.Capabilities)))
	var refusal *Refusal
	if d.Kind != DirectPlay {
		switch {
		case !pol.IsAdmin && !pol.AllowTranscode:
			refusal = &Refusal{Status: 403, Code: "playback_refused", Message: "This needs converting on the server, and your account is not allowed to do that. Reason: " + d.Reason + "."}
		case !pol.IsAdmin && !pol.Allow4KTranscode && policy.ResolutionRank(s2(v.Resolution)) >= policy.ResolutionRank("4K"):
			refusal = &Refusal{Status: 403, Code: "playback_refused", Message: "4K files cannot be converted for your account. Ask for a 1080p version. Reason: " + d.Reason + "."}
		case !converted:
			refusal = &Refusal{Status: 422, Code: "transcode_unavailable", Message: "This file needs " + describeKind(d.Kind) + " for this device, which VCRX cannot do yet. Reason: " + d.Reason + "."}
		}
	}

	resume := int64(0)
	if req.StartPositionMs != nil {
		resume = max(0, *req.StartPositionMs)
	} else {
		var pos int64
		var played bool
		err := s.DB.QueryRowContext(ctx, `SELECT position_ms, played FROM watch_state WHERE user_id = ? AND item_id = ?`,
			c.UserID, req.ItemID).Scan(&pos, &played)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, err
		}
		if !played {
			resume = pos
		}
	}

	capsJSON, _ := json.Marshal(req.Capabilities)
	id := newID()
	now := s.now().UnixMilli()
	decision, state, reason := d.Kind, "created", d.Reason
	if refusal != nil {
		decision, state, reason = Refused, "refused", refusal.Message
	}
	var endedAt any
	if refusal != nil {
		endedAt = now
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO playback_sessions (id, user_id, auth_session_id, item_id, version_id, username, item_title,
			item_kind, version_summary, device_id, device_name, device_model, platform, client, client_version, ip, decision,
			decision_reason, video_decision, audio_decision, bitrate, capabilities, state, start_position_ms, last_position_ms,
			duration_ms, started_at, last_event_at, ended_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, c.UserID, nullID(c.AuthSessionID), req.ItemID, v.ID, c.Username, title, kind, summarize(v),
		c.DeviceID, c.DeviceName, c.DeviceModel, c.Platform, c.ClientName, c.ClientVersion, c.IP,
		decision, reason, d.VideoDecision, d.AudioDecision, v.Bitrate, string(capsJSON), state, resume, resume,
		v.DurationMs, now, now, endedAt)
	if err != nil {
		return nil, nil, err
	}
	if refusal != nil {
		s.notify(notify.Notice{Event: notify.Refused, SessionID: id, Username: c.Username, Title: title, Version: summarize(v),
			Device: c.DeviceName, Client: c.ClientName, IP: c.IP, Decision: Refused, Reason: refusal.Message})
		return nil, refusal, nil
	}

	mime := mimeForContainer(s2(v.Container))
	stream := Stream{URL: fmt.Sprintf("/api/v1/versions/%d/file?session=%s", v.ID, id), Protocol: "progressive", MimeType: mime}
	seek := "/api/v1/playback/sessions/" + id + "/seek"
	switch {
	case converted && hls:
		m3u8 := "application/vnd.apple.mpegurl"
		stream = Stream{URL: seek, Protocol: "hls", MimeType: &m3u8, SeekURL: seek}
	case converted:
		mp4 := "video/mp4"
		stream = Stream{URL: "/api/v1/playback/sessions/" + id + "/stream.mp4", Protocol: "fmp4", MimeType: &mp4, SeekURL: seek}
	}
	out := &Session{
		SessionID: id, Decision: d.Kind, Reason: d.Reason, ItemID: req.ItemID, Version: v,
		Stream:              stream,
		Subtitles:           sidecars(v),
		WebVTTSubtitles:     webvtt(v),
		ResumePositionMs:    resume,
		HeartbeatIntervalMs: s.heartbeat().Milliseconds(),
	}
	return out, nil, nil
}

func (s *Service) heartbeat() time.Duration {
	if s.Heartbeat > 0 {
		return s.Heartbeat
	}
	return DefaultInterval
}

func indexOf(vs []catalog.Version, id int64) int {
	for i, v := range vs {
		if v.ID == id {
			return i
		}
	}
	return 0
}

func s2(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func describeKind(k string) string {
	switch k {
	case Remux:
		return "repackaging into another container"
	case AudioTranscode:
		return "audio conversion"
	default:
		return "video conversion"
	}
}

// displayTitle snapshots "Show S01E02 Title" for episodes so history reads well
// after the item is gone.
func (s *Service) displayTitle(ctx context.Context, itemID int64, kind, title string) (string, error) {
	if kind != "episode" {
		return title, nil
	}
	var show sql.NullString
	var season, ep sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT gp.title, p.season_number, i.episode_number FROM items i
		LEFT JOIN items p ON p.id = i.parent_id LEFT JOIN items gp ON gp.id = p.parent_id WHERE i.id = ?`, itemID).Scan(&show, &season, &ep)
	if err != nil {
		return title, err
	}
	return fmt.Sprintf("%s S%02dE%02d %s", show.String, season.Int64, ep.Int64, title), nil
}

func summarize(v catalog.Version) string {
	var parts []string
	for _, p := range []string{s2(v.Resolution), strings.ToUpper(s2(v.HDRType)), s2(v.VideoCodec), s2(v.Container)} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	if v.Label != nil && *v.Label != "" {
		parts = append(parts, "("+*v.Label+")")
	}
	return strings.Join(parts, " ")
}

func mimeForContainer(c string) *string {
	m := map[string]string{
		"mkv": "video/x-matroska", "webm": "video/webm", "mp4": "video/mp4", "mov": "video/quicktime",
		"avi": "video/x-msvideo", "ts": "video/mp2t", "m2ts": "video/mp2t", "flv": "video/x-flv",
		"ogg": "video/ogg", "wmv": "video/x-ms-wmv", "mpeg": "video/mpeg",
	}[strings.ToLower(c)]
	if m == "" {
		return nil
	}
	return &m
}

// SubtitleMime maps a sidecar file to the MIME type Media3 expects.
func SubtitleMime(path string) (codec, mime string, ok bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".srt":
		return "subrip", "application/x-subrip", true
	case ".ass":
		return "ass", "text/x-ssa", true
	case ".ssa":
		return "ssa", "text/x-ssa", true
	case ".vtt":
		return "webvtt", "text/vtt", true
	case ".ttml", ".dfxp", ".xml":
		return "ttml", "application/ttml+xml", true
	}
	return "", "", false
}

// sidecars lists the text subtitle files beside the media. Image-based sidecars
// (VobSub, PGS) are left out until burn-in exists.
func sidecars(v catalog.Version) []SubtitleSource {
	out := []SubtitleSource{}
	for _, t := range v.SubtitleTracks {
		if !t.IsExternal || t.IsImageBased {
			continue
		}
		codec := s2(t.Codec)
		mime := ""
		switch strings.ToLower(codec) {
		case "subrip", "srt":
			codec, mime = "subrip", "application/x-subrip"
		case "ass", "ssa":
			mime = "text/x-ssa"
		case "webvtt", "vtt":
			codec, mime = "webvtt", "text/vtt"
		case "ttml":
			mime = "application/ttml+xml"
		default:
			continue
		}
		out = append(out, SubtitleSource{TrackID: t.ID, URL: "/api/v1/subtitles/" + strconv.FormatInt(t.ID, 10), Codec: codec,
			MimeType: mime, Language: t.Language, Title: t.Title, IsForced: t.IsForced})
	}
	return out
}

// webvtt lists every text subtitle, sidecar or embedded, as a WebVTT URL for
// clients that read nothing else. Image-based tracks need burn-in and are left out.
func webvtt(v catalog.Version) []SubtitleSource {
	out := []SubtitleSource{}
	for _, t := range v.SubtitleTracks {
		if t.IsImageBased || !subtitles.TextCodec(s2(t.Codec)) {
			continue
		}
		out = append(out, SubtitleSource{TrackID: t.ID, URL: "/api/v1/subtitles/" + strconv.FormatInt(t.ID, 10) + "?format=vtt",
			Codec: "webvtt", MimeType: "text/vtt", Language: t.Language, Title: t.Title, IsForced: t.IsForced, Embedded: !t.IsExternal})
	}
	return out
}

var validEvents = map[string]string{
	"start": "playing", "resume": "playing", "progress": "playing", "seek": "", "pause": "paused",
	"buffering": "buffering", "stop": "stopped", "error": "error",
}

// RecordEvent appends an event, advances the session, and updates watch state. A
// repeated stop is accepted quietly so clients can retry it safely.
func (s *Service) RecordEvent(ctx context.Context, userID int64, sessionID string, ev Event) error {
	nextState, ok := validEvents[ev.Type]
	if !ok {
		return fmt.Errorf("unknown event type %q", ev.Type)
	}
	if ev.PositionMs < 0 {
		ev.PositionMs = 0
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var state string
	var itemID, versionID sql.NullInt64
	var lastEvent, playedMs int64
	var duration sql.NullInt64
	var terminated sql.NullString
	var n notify.Notice
	var uname, title, vsum, device, client, ip sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT state, item_id, version_id, last_event_at, played_ms, duration_ms, terminated_message,
			username, item_title, version_summary, device_name, client, ip, decision
		FROM playback_sessions WHERE id = ? AND user_id = ?`, sessionID, userID).Scan(&state, &itemID, &versionID, &lastEvent, &playedMs,
		&duration, &terminated, &uname, &title, &vsum, &device, &client, &ip, &n.Decision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSessionNotFound
	}
	if err != nil {
		return err
	}
	switch state {
	case "stopped", "error", "refused":
		if terminated.Valid {
			return &TerminatedError{Message: terminated.String}
		}
		if ev.Type == "stop" && state == "stopped" {
			return nil
		}
		return ErrSessionEnded
	}
	n.SessionID, n.Username, n.Title, n.Version = sessionID, uname.String, title.String, vsum.String
	n.Device, n.Client, n.IP = device.String, client.String, ip.String
	now := s.now().UnixMilli()
	if state == "playing" {
		// Credit wall time since the last event, capped so a lost heartbeat cannot
		// count an hour of idle as watching.
		playedMs += min(now-lastEvent, 2*s.heartbeat().Milliseconds()+5000)
	}
	if nextState == "" {
		nextState = state
	}
	if ev.DurationMs != nil && *ev.DurationMs > 0 {
		duration = sql.NullInt64{Int64: *ev.DurationMs, Valid: true}
	}
	var endedAt any
	if nextState == "stopped" || nextState == "error" {
		endedAt = now
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO playback_events (session_id, type, position_ms, from_position_ms, message, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, sessionID, ev.Type, ev.PositionMs, ev.FromPositionMs, ev.Message, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE playback_sessions SET state = ?, last_position_ms = ?, played_ms = ?, duration_ms = ?,
			last_event_at = ?, ended_at = COALESCE(ended_at, ?) WHERE id = ?`,
		nextState, ev.PositionMs, playedMs, duration, now, endedAt, sessionID); err != nil {
		return err
	}
	if itemID.Valid && ev.Type != "error" && ev.Type != "buffering" {
		if err := updateWatch(ctx, tx, userID, itemID.Int64, versionID, ev.PositionMs, duration, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	n.PositionMs, n.DurationMs, n.PlayedMs = ev.PositionMs, duration.Int64, playedMs
	switch {
	case state == "created" && nextState == "playing":
		n.Event = notify.Play
		s.notify(n)
	case nextState == "stopped":
		n.Event = notify.Stop
		s.notify(n)
	}
	return nil
}

func updateWatch(ctx context.Context, tx *sql.Tx, userID, itemID int64, versionID sql.NullInt64, pos int64, duration sql.NullInt64, now int64) error {
	finished := duration.Valid && duration.Int64 > 0 && float64(pos) >= PlayedFraction*float64(duration.Int64)
	if finished {
		_, err := tx.ExecContext(ctx, `INSERT INTO watch_state (user_id, item_id, position_ms, duration_ms, played, play_count, last_version_id, last_played_at, updated_at)
			VALUES (?, ?, 0, ?, 1, 1, ?, ?, ?)
			ON CONFLICT (user_id, item_id) DO UPDATE SET position_ms = 0, duration_ms = excluded.duration_ms,
				play_count = CASE WHEN played = 1 THEN play_count ELSE play_count + 1 END, played = 1,
				last_version_id = excluded.last_version_id, last_played_at = excluded.last_played_at, updated_at = excluded.updated_at`,
			userID, itemID, duration, versionID, now, now)
		return err
	}
	if pos < MinResumeMs {
		pos = 0
	}
	// Watching an item again from the start clears its played flag only once the
	// new position is worth resuming.
	_, err := tx.ExecContext(ctx, `INSERT INTO watch_state (user_id, item_id, position_ms, duration_ms, played, play_count, last_version_id, last_played_at, updated_at)
		VALUES (?, ?, ?, ?, 0, 0, ?, ?, ?)
		ON CONFLICT (user_id, item_id) DO UPDATE SET position_ms = excluded.position_ms, duration_ms = COALESCE(excluded.duration_ms, duration_ms),
			played = CASE WHEN excluded.position_ms > 0 THEN 0 ELSE played END,
			last_version_id = excluded.last_version_id, last_played_at = excluded.last_played_at, updated_at = excluded.updated_at`,
		userID, itemID, pos, duration, versionID, now, now)
	return err
}

// SweepStale closes sessions whose client vanished without sending stop.
func (s *Service) SweepStale(ctx context.Context) (int64, error) {
	now := s.now()
	res, err := s.DB.ExecContext(ctx, `UPDATE playback_sessions SET state = 'stopped', ended_at = last_event_at
		WHERE ended_at IS NULL AND last_event_at < ?`, now.Add(-StaleAfter).UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// StreamSession is what the converted-stream endpoints need from a session.
type StreamSession struct {
	VersionID  int64
	Decision   string
	Caps       Capabilities
	Ended      bool
	Terminated *string
}

// ForStream loads a session the user owns for its converted stream.
func (s *Service) ForStream(ctx context.Context, userID int64, sessionID string) (StreamSession, error) {
	var out StreamSession
	var vid sql.NullInt64
	var caps sql.NullString
	var ended sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT version_id, decision, capabilities, ended_at, terminated_message FROM playback_sessions
		WHERE id = ? AND user_id = ?`, sessionID, userID).Scan(&vid, &out.Decision, &caps, &ended, &out.Terminated)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !vid.Valid) {
		return out, ErrSessionNotFound
	}
	if err != nil {
		return out, err
	}
	out.VersionID, out.Ended = vid.Int64, ended.Valid
	if caps.Valid {
		_ = json.Unmarshal([]byte(caps.String), &out.Caps)
	}
	return out, nil
}

// Terminated returns the admin's message when a session was stopped from Activity.
func (s *Service) Terminated(ctx context.Context, sessionID string) (string, bool) {
	var msg sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT terminated_message FROM playback_sessions WHERE id = ?`, sessionID).Scan(&msg)
	return msg.String, err == nil && msg.Valid
}

// OwnsSession reports whether a session belongs to a user, for stream accounting.
func (s *Service) OwnsSession(ctx context.Context, userID int64, sessionID string) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM playback_sessions WHERE id = ? AND user_id = ?`, sessionID, userID).Scan(&n)
	return n > 0
}
