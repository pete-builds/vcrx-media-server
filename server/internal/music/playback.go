package music

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
)

// Stream is how a client plays one track: the file itself, with byte ranges,
// or a stream converted on the fly, which seeks by asking for a new stream
// from a position (start_ms) rather than by range.
type Stream struct {
	URL      string `json:"url"`
	Protocol string `json:"protocol"`
	MimeType string `json:"mime_type"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// Protocols of a Stream.
const (
	Progressive = "progressive"
	Converted   = "converted"
)

// Refusal says why a track will not play for this caller. Message is safe to
// show on a TV.
type Refusal struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// QueueTrack is a track with how this client plays it: Stream, or Refusal.
type QueueTrack struct {
	Track
	Stream  *Stream  `json:"stream"`
	Refusal *Refusal `json:"refusal"`
}

// codecNames maps ffprobe's audio codec names onto the names clients report.
// Every PCM layout is "pcm"; the container decides whether it plays (WAV
// does in browsers, AIFF does not).
func codecName(c string) string {
	c = strings.ToLower(c)
	if strings.HasPrefix(c, "pcm_") {
		return "pcm"
	}
	return c
}

// containerMatches is whether a client that lists containers plays this one.
// MP4 audio is listed as m4a or mp4, Ogg Opus as ogg or opus.
func containerMatches(caps playback.Capabilities, container string) bool {
	if len(caps.Containers) == 0 {
		return true
	}
	alias := map[string][]string{"m4a": {"m4a", "mp4"}, "opus": {"opus", "ogg"}, "oga": {"ogg"}, "mka": {"mka", "mkv"}}
	names := alias[container]
	if names == nil {
		names = []string{container}
	}
	for _, have := range caps.Containers {
		for _, n := range names {
			if strings.EqualFold(have, n) {
				return true
			}
		}
	}
	return false
}

func decodes(caps playback.Capabilities, codec string) bool {
	for _, a := range caps.AudioCodecs {
		if strings.EqualFold(a.Codec, codec) {
			return true
		}
	}
	return false
}

// Decide picks direct play when the client decodes the codec in this
// container, else a converted stream in a codec it does decode: AAC, or Opus
// when it lists Opus and not AAC. canOpus is whether the server can write Opus.
func Decide(f *File, caps playback.Capabilities, canOpus bool) (decision, reason, codec string) {
	c := codecName(f.Codec)
	switch {
	case c == "":
		reason = "the file could not be read when it was scanned"
	case !decodes(caps, c):
		reason = "this device does not decode " + c
	case !containerMatches(caps, f.Container):
		reason = "this device does not play " + f.Container + " files"
	default:
		return playback.DirectPlay, "the device plays " + c + " in " + f.Container, ""
	}
	switch {
	case decodes(caps, "aac"):
		return playback.AudioTranscode, reason + "; converting to AAC", remux.AudioAAC
	case decodes(caps, "opus") && canOpus:
		return playback.AudioTranscode, reason + "; converting to Opus", remux.AudioOpus
	}
	return playback.Refused, reason + ", and it decodes neither AAC nor Opus", ""
}

// Player turns tracks into streams and listens into Activity.
type Player struct {
	DB      *sql.DB
	Catalog *Catalog
	// Remux converts what a client cannot decode; nil means direct play only.
	Remux     *remux.Service
	Heartbeat time.Duration
	Now       func() time.Time
}

func (p *Player) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *Player) canOpus() bool { return p.Remux != nil && p.Remux.Opus }

// StreamFor is how this caller's client plays one track, or why it cannot:
// conversion follows the same policy as video (allow_transcode).
func (p *Player) StreamFor(f *File, pol policy.Policy, caps playback.Capabilities) (*Stream, *Refusal) {
	decision, reason, codec := Decide(f, caps, p.canOpus())
	switch {
	case decision == playback.DirectPlay:
		return &Stream{URL: "/api/v1/music/tracks/" + strconv.FormatInt(f.TrackID, 10) + "/file", Protocol: Progressive,
			MimeType: MimeFor(f.Container, f.Codec), Decision: decision, Reason: reason}, nil
	case decision == playback.Refused || p.Remux == nil:
		return nil, &Refusal{Status: 422, Code: "transcode_unavailable", Message: "This track cannot play on this device: " + reason + "."}
	case !pol.IsAdmin && !pol.AllowTranscode:
		return nil, &Refusal{Status: 403, Code: "playback_refused",
			Message: "This track needs converting on the server, and your account is not allowed to do that. Reason: " + reason + "."}
	}
	return &Stream{URL: "/api/v1/music/tracks/" + strconv.FormatInt(f.TrackID, 10) + "/stream?codec=" + codec, Protocol: Converted,
		MimeType: remux.AudioMime(codec), Decision: decision, Reason: reason}, nil
}

// MimeFor is the Content-Type a track's file is served with.
func MimeFor(container, codec string) string {
	switch container {
	case "flac":
		return "audio/flac"
	case "mp3":
		return "audio/mpeg"
	case "m4a":
		return "audio/mp4"
	case "ogg", "opus", "oga":
		return "audio/ogg"
	case "wav":
		return "audio/wav"
	case "aac":
		return "audio/aac"
	case "webm":
		return "audio/webm"
	case "aiff":
		return "audio/aiff"
	}
	if codec == "mp3" {
		return "audio/mpeg"
	}
	return "application/octet-stream"
}

// QueueRequest asks for a play queue: an album in order, an artist's tracks
// (shuffled by default), or a list of tracks.
type QueueRequest struct {
	AlbumID      int64                 `json:"album_id"`
	ArtistID     int64                 `json:"artist_id"`
	TrackIDs     []int64               `json:"track_ids"`
	Shuffle      *bool                 `json:"shuffle"`
	Capabilities playback.Capabilities `json:"capabilities"`
}

// Queue builds a play queue for the caller. ErrNotFound when the album or
// artist is not one they may see, or the list holds nothing playable.
func (p *Player) Queue(ctx context.Context, pol policy.Policy, req QueueRequest) ([]QueueTrack, error) {
	var tracks []Track
	var err error
	shuffle := req.Shuffle != nil && *req.Shuffle
	switch {
	case req.AlbumID > 0:
		tracks, err = p.Catalog.AlbumTracks(ctx, pol, req.AlbumID)
	case req.ArtistID > 0:
		tracks, err = p.Catalog.ArtistTracks(ctx, pol, req.ArtistID)
		// Playing an artist means shuffling them, unless asked not to.
		shuffle = req.Shuffle == nil || *req.Shuffle
	default:
		tracks, err = p.Catalog.Tracks(ctx, pol, req.TrackIDs)
	}
	if err != nil {
		return nil, err
	}
	if len(tracks) == 0 {
		return nil, ErrNotFound
	}
	if shuffle {
		rand.Shuffle(len(tracks), func(i, j int) { tracks[i], tracks[j] = tracks[j], tracks[i] })
	}
	out := make([]QueueTrack, 0, len(tracks))
	for _, t := range tracks {
		f := &File{TrackID: t.ID, Codec: deref(t.Codec), Container: deref(t.Container)}
		qt := QueueTrack{Track: t}
		qt.Stream, qt.Refusal = p.StreamFor(f, pol, req.Capabilities)
		out = append(out, qt)
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Client identifies who is listening, for Activity.
type Client struct {
	UserID        int64
	Username      string
	AuthSessionID int64
	DeviceID      string
	DeviceName    string
	DeviceModel   string
	Platform      string
	ClientName    string
	ClientVersion string
	IP            string
}

// Session is a listen: a playback session for one track. The client reports
// start, progress, pause and stop through the same events endpoint as video.
type Session struct {
	SessionID           string  `json:"session_id"`
	TrackID             int64   `json:"track_id"`
	Decision            string  `json:"decision"`
	Reason              string  `json:"reason"`
	Stream              *Stream `json:"stream"`
	HeartbeatIntervalMs int64   `json:"heartbeat_interval_ms"`
}

// Listen records that a track starts playing and returns its session. The
// decision is the server's own, from the capabilities, never the client's
// word. A refusal is recorded too, as for video.
func (p *Player) Listen(ctx context.Context, pol policy.Policy, c Client, trackID int64, caps playback.Capabilities) (*Session, *Refusal, error) {
	f, err := p.Catalog.File(ctx, pol, trackID)
	if err != nil {
		return nil, nil, err
	}
	stream, refusal := p.StreamFor(f, pol, caps)
	id := newID()
	now := p.now().UnixMilli()
	decision, reason, state := playback.Refused, "", "refused"
	var ended any = now
	if refusal != nil {
		reason = refusal.Message
	} else {
		decision, reason, state, ended = stream.Decision, stream.Reason, "created", nil
	}
	capsJSON, _ := json.Marshal(caps)
	audioDecision := "direct play"
	if decision == playback.AudioTranscode {
		audioDecision = "transcode"
	}
	title := f.Artist + " · " + f.Title
	if f.Artist == "" {
		title = f.Title
	}
	_, err = p.DB.ExecContext(ctx, `INSERT INTO playback_sessions (id, user_id, auth_session_id, track_id, username, item_title, item_kind,
			version_summary, device_id, device_name, device_model, platform, client, client_version, ip, decision, decision_reason,
			audio_decision, bitrate, capabilities, state, duration_ms, started_at, last_event_at, ended_at)
		VALUES (?, ?, ?, ?, ?, ?, 'track', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, c.UserID, nullID(c.AuthSessionID), f.TrackID, c.Username, title, summary(f), c.DeviceID, c.DeviceName, c.DeviceModel,
		c.Platform, c.ClientName, c.ClientVersion, c.IP, decision, reason, audioDecision, nullID(f.Bitrate), string(capsJSON), state,
		nullID(f.DurationMs), now, now, ended)
	if err != nil {
		return nil, nil, err
	}
	if refusal != nil {
		return nil, refusal, nil
	}
	s := *stream
	sep := "?"
	if strings.Contains(s.URL, "?") {
		sep = "&"
	}
	s.URL += sep + "session=" + id
	hb := p.Heartbeat
	if hb <= 0 {
		hb = playback.DefaultInterval
	}
	return &Session{SessionID: id, TrackID: f.TrackID, Decision: decision, Reason: reason, Stream: &s, HeartbeatIntervalMs: hb.Milliseconds()}, nil, nil
}

// summary describes a track's file for Activity: "FLAC 44.1 kHz 16-bit".
func summary(f *File) string {
	parts := []string{strings.ToUpper(codecName(f.Codec))}
	if f.SampleRate > 0 {
		parts = append(parts, strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.1f", float64(f.SampleRate)/1000), "0"), ".")+" kHz")
	}
	if f.BitDepth > 0 {
		parts = append(parts, strconv.FormatInt(f.BitDepth, 10)+"-bit")
	} else if f.Bitrate > 0 {
		parts = append(parts, strconv.FormatInt(f.Bitrate/1000, 10)+" kbps")
	}
	return strings.Join(parts, " ")
}

func nullID(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func newID() string {
	b := make([]byte, 16)
	_, _ = crand.Read(b)
	return hex.EncodeToString(b)
}
