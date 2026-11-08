package audiobook

import (
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/music"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
)

func nowMs() int64 { return time.Now().UnixMilli() }

// Player turns a book's files into streams and listens into Activity. It
// decides the way music does: the file itself when the device decodes it,
// else converted to AAC or Opus, under the same allow_transcode rule.
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

// StreamFor is how this caller's client plays one file, or why it cannot.
func (p *Player) StreamFor(f *FileSource, pol policy.Policy, caps playback.Capabilities) (*music.Stream, *music.Refusal) {
	decision, reason, codec := music.Decide(&music.File{Codec: f.Codec, Container: f.Container}, caps, p.Remux != nil && p.Remux.Opus)
	base := "/api/v1/audiobooks/files/" + strconv.FormatInt(f.FileID, 10)
	switch {
	case decision == playback.DirectPlay:
		return &music.Stream{URL: base + "/file", Protocol: music.Progressive, MimeType: music.MimeFor(f.Container, f.Codec),
			Decision: decision, Reason: reason}, nil
	case decision == playback.Refused || p.Remux == nil:
		return nil, &music.Refusal{Status: 422, Code: "transcode_unavailable", Message: "This book cannot play on this device: " + reason + "."}
	case !pol.IsAdmin && !pol.AllowTranscode:
		return nil, &music.Refusal{Status: 403, Code: "playback_refused",
			Message: "This book needs converting on the server, and your account is not allowed to do that. Reason: " + reason + "."}
	}
	return &music.Stream{URL: base + "/stream?codec=" + codec, Protocol: music.Converted, MimeType: remux.AudioMime(codec),
		Decision: decision, Reason: reason}, nil
}

// QueueFile is a book's file with how this client plays it.
type QueueFile struct {
	File
	Stream  *music.Stream  `json:"stream"`
	Refusal *music.Refusal `json:"refusal"`
}

// Queue is everything a player needs to play a book: its files in order,
// each with a stream or a refusal, its chapters, and where the caller left
// off.
type Queue struct {
	BookID    int64       `json:"book_id"`
	Title     string      `json:"title"`
	Author    string      `json:"author"`
	HasCover  bool        `json:"has_cover"`
	DurationS float64     `json:"duration_s"`
	Files     []QueueFile `json:"files"`
	Chapters  []Chapter   `json:"chapters"`
	Progress  Progress    `json:"progress"`
}

// Queue builds the play queue for a book the caller may see.
func (p *Player) Queue(ctx context.Context, pol policy.Policy, userID, bookID int64, caps playback.Capabilities) (*Queue, error) {
	d, err := p.Catalog.Book(ctx, pol, userID, bookID)
	if err != nil {
		return nil, err
	}
	q := &Queue{BookID: d.ID, Title: d.Title, Author: d.Author, HasCover: d.HasCover, DurationS: d.DurationS, Chapters: d.Chapters,
		Files: make([]QueueFile, 0, len(d.Files))}
	if d.Progress != nil {
		q.Progress = *d.Progress
	}
	for _, f := range d.Files {
		src, err := p.Catalog.Source(ctx, pol, f.ID)
		if err != nil {
			return nil, err
		}
		qf := QueueFile{File: f}
		qf.Stream, qf.Refusal = p.StreamFor(src, pol, caps)
		q.Files = append(q.Files, qf)
	}
	return q, nil
}

// Client identifies who is listening, for Activity.
type Client = music.Client

// Session is a listen to one file of a book. The client reports start,
// progress, pause and stop through the same events endpoint as video; its
// place in the book goes to the progress route.
type Session struct {
	SessionID           string        `json:"session_id"`
	FileID              int64         `json:"file_id"`
	BookID              int64         `json:"book_id"`
	Decision            string        `json:"decision"`
	Reason              string        `json:"reason"`
	Stream              *music.Stream `json:"stream"`
	HeartbeatIntervalMs int64         `json:"heartbeat_interval_ms"`
}

// Listen records that a file starts playing and returns its session. The
// decision is the server's own, from the capabilities; a refusal is recorded
// too, as for music.
func (p *Player) Listen(ctx context.Context, pol policy.Policy, c Client, fileID int64, caps playback.Capabilities) (*Session, *music.Refusal, error) {
	f, err := p.Catalog.Source(ctx, pol, fileID)
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
	title := f.Author + " · " + f.BookTitle
	summary := strings.ToUpper(f.Codec)
	if f.Bitrate > 0 {
		summary += " " + strconv.FormatInt(f.Bitrate/1000, 10) + " kbps"
	}
	_, err = p.DB.ExecContext(ctx, `INSERT INTO playback_sessions (id, user_id, auth_session_id, book_file_id, username, item_title, item_kind,
			version_summary, device_id, device_name, device_model, platform, client, client_version, ip, decision, decision_reason,
			audio_decision, bitrate, capabilities, state, duration_ms, started_at, last_event_at, ended_at)
		VALUES (?, ?, ?, ?, ?, ?, 'audiobook', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, c.UserID, nullID(c.AuthSessionID), f.FileID, c.Username, title, summary, c.DeviceID, c.DeviceName, c.DeviceModel,
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
	return &Session{SessionID: id, FileID: f.FileID, BookID: f.BookID, Decision: decision, Reason: reason, Stream: &s,
		HeartbeatIntervalMs: hb.Milliseconds()}, nil, nil
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
