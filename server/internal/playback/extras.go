package playback

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/notify"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// startExtra plays a trailer or other extra through the same ladder, policy
// (gate) and activity record as a version. The session carries extra_id
// instead of an item and a version, so watching a trailer never moves the
// film's resume point or marks it played.
func (s *Service) startExtra(ctx context.Context, pol policy.Policy, c Client, req StartRequest) (*Session, *Refusal, error) {
	f, err := s.Catalog.ExtraForPlayback(ctx, pol, *req.ExtraID)
	if err != nil {
		return nil, nil, err
	}
	if req.ItemID != 0 && req.ItemID != f.ItemID {
		return nil, nil, catalog.ErrNotFound
	}
	if f.Kind == "theme" {
		return nil, &Refusal{Status: 422, Code: "not_playable", Message: "Theme music plays on the show's page, not in the player."}, nil
	}
	v := f.Version
	d := Decide(v, req.Capabilities)
	title := f.ItemTitle + ": " + f.Title

	converted, hls, refusal := s.gate(pol, v, d, req.Capabilities)
	start := int64(0)
	if req.StartPositionMs != nil {
		start = max(0, *req.StartPositionMs)
	}

	capsJSON, _ := json.Marshal(req.Capabilities)
	id := newID()
	now := s.now().UnixMilli()
	decision, state, reason := d.Kind, "created", d.Reason
	var endedAt any
	if refusal != nil {
		decision, state, reason, endedAt = Refused, "refused", refusal.Message, now
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO playback_sessions (id, user_id, auth_session_id, extra_id, username, item_title,
			item_kind, version_summary, device_id, device_name, device_model, platform, client, client_version, ip, decision,
			decision_reason, video_decision, audio_decision, bitrate, capabilities, state, start_position_ms, last_position_ms,
			duration_ms, started_at, last_event_at, ended_at, principal_kind, token_id)
		VALUES (?, ?, ?, ?, ?, ?, 'extra', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, c.UserID, nullID(c.AuthSessionID), v.ID, c.Username, title, summarize(v),
		c.DeviceID, c.DeviceName, c.DeviceModel, c.Platform, c.ClientName, c.ClientVersion, c.IP,
		decision, reason, d.VideoDecision, d.AudioDecision, v.Bitrate, string(capsJSON), state, start, start,
		v.DurationMs, now, now, endedAt, principalKind(c.PrincipalKind), nullID(c.TokenID))
	if err != nil {
		return nil, nil, err
	}
	if refusal != nil {
		s.notify(notify.Notice{Event: notify.Refused, SessionID: id, Username: c.Username, Title: title, Version: summarize(v),
			Device: c.DeviceName, Client: c.ClientName, IP: c.IP, Decision: Refused, Reason: refusal.Message})
		return nil, refusal, nil
	}

	stream := streamFor(id, fmt.Sprintf("/api/v1/extras/%d/file?session=%s", v.ID, id), v, converted, hls)
	extraID := v.ID
	return &Session{
		SessionID: id, Decision: d.Kind, Reason: d.Reason, ItemID: f.ItemID, ExtraID: &extraID, Version: v, Stream: stream,
		Subtitles: []SubtitleSource{}, WebVTTSubtitles: []SubtitleSource{}, ResumePositionMs: start,
		HeartbeatIntervalMs: s.heartbeat().Milliseconds(),
	}, nil, nil
}
