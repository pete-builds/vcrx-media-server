package api

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
	"net"
	"net/http"
	"net/http/httptest"
	"time"
)

// browserCaps is what the web app reports in Chrome on a Mac: MP4 and
// Matroska, H.264 and HEVC, AAC but no AC-3 or E-AC-3, first audio track only.
func browserCaps(fmp4 bool) map[string]any {
	return map[string]any{
		"containers":   []string{"mp4", "mov", "webm"},
		"video_codecs": []map[string]any{{"codec": "h264"}, {"codec": "hevc", "max_bit_depth": 10}},
		"audio_codecs": []map[string]any{{"codec": "aac", "max_channels": 8}, {"codec": "opus"}},
		"fmp4_stream":  fmp4, "default_audio_only": true,
	}
}

func TestConvertedStreamDecision(t *testing.T) {
	f := newFixture(t)
	f.srv.Playback.CanRemux = true
	f.srv.Remux = remux.New("ffmpeg", "ffprobe", 2, nil)
	var sess struct {
		Decision string
		Stream   struct {
			URL, Protocol string
			SeekURL       string `json:"seek_url"`
		}
	}
	// The 1080p film is HEVC Matroska with E-AC-3: a browser needs the audio converted.
	r := f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": browserCaps(true)})
	if r.code != 201 {
		t.Fatalf("start: %d %s", r.code, r.body)
	}
	r.json(t, &sess)
	if sess.Decision != "audio_transcode" || sess.Stream.Protocol != "fmp4" || !strings.HasSuffix(sess.Stream.URL, "/stream.mp4") || sess.Stream.SeekURL == "" {
		t.Fatalf("converted session: %+v", sess)
	}
	// A client that does not offer fmp4_stream (the TV app today) is refused as before.
	r = f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": browserCaps(false)})
	if r.code != 422 || !strings.Contains(string(r.body), "transcode_unavailable") {
		t.Fatalf("without fmp4_stream: %d %s", r.code, r.body)
	}
	// default_audio_only: an AAC second track does not help a browser when the
	// default track is E-AC-3; a player that picks tracks would direct play.
	if _, err := f.db.Exec(`INSERT INTO media_streams (version_id, stream_index, kind, codec, channels) VALUES (?, 2, 'audio', 'aac', 2)`, f.v1080); err != nil {
		t.Fatal(err)
	}
	caps := browserCaps(true)
	caps["containers"] = []string{"mkv", "mp4"}
	r = f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": caps})
	r.json(t, &sess)
	if sess.Decision != "audio_transcode" {
		t.Fatalf("default-only browser with E-AC-3 first: %s", sess.Decision)
	}
	caps["default_audio_only"] = false
	r = f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": caps})
	r.json(t, &sess)
	if sess.Decision != "direct_play" {
		t.Fatalf("track-picking player with an AAC track: %s", sess.Decision)
	}
}

func TestConvertedStreamEndToEnd(t *testing.T) {
	ff, err1 := osexec.LookPath("ffmpeg")
	fp, err2 := osexec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg or ffprobe not on PATH")
	}
	f := newFixture(t)
	f.srv.Playback.CanRemux = true
	f.srv.Remux = remux.New(ff, fp, 2, nil)
	// The episode becomes a real 30 s H.264 Matroska with AC-3 audio and a
	// keyframe every 2 s.
	var epPath string
	if err := f.db.QueryRow(`SELECT path FROM media_versions WHERE id = ?`, f.episodeVerID).Scan(&epPath); err != nil {
		t.Fatal(err)
	}
	if msg, err := osexec.Command(ff, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24:duration=30",
		"-f", "lavfi", "-i", "sine=duration=30", "-c:v", "libx264", "-g", "48", "-sc_threshold", "0", "-pix_fmt", "yuv420p",
		"-c:a", "ac3", "-y", epPath+".tmp.mkv").CombinedOutput(); err != nil {
		t.Skipf("fixture needs libx264 and ac3: %v %s", err, msg)
	}
	if err := os.Rename(epPath+".tmp.mkv", epPath); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE media_versions SET video_codec = 'h264', video_profile = 'high', bit_depth = 8 WHERE id = ?`, f.episodeVerID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO media_streams (version_id, stream_index, kind, codec, channels, is_default) VALUES (?, 1, 'audio', 'ac3', 2, 1)`, f.episodeVerID); err != nil {
		t.Fatal(err)
	}
	var sess struct {
		SessionID string `json:"session_id"`
		Decision  string
		Stream    struct {
			SeekURL string `json:"seek_url"`
		}
	}
	r := f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.episodeID, "capabilities": browserCaps(true)})
	r.json(t, &sess)
	if sess.Decision != "audio_transcode" {
		t.Fatalf("decision %s: %s", sess.Decision, r.body)
	}
	var seek struct {
		StartMs int64 `json:"start_ms"`
		URL     string
	}
	r = f.do("GET", sess.Stream.SeekURL+"?position_ms=12700", f.adminTok, nil)
	r.json(t, &seek)
	if r.code != 200 || seek.StartMs != 12000 {
		t.Fatalf("seek: %d %s", r.code, r.body)
	}
	// Another user cannot use the session.
	if r := f.do("GET", seek.URL, f.friendTok, nil); r.code != 404 {
		t.Fatalf("friend using the admin's session: %d", r.code)
	}
	r = f.do("GET", seek.URL, f.adminTok, nil)
	if r.code != 200 || r.hdr.Get("Content-Type") != "video/mp4" || len(r.body) < 10000 {
		t.Fatalf("stream: %d %s %d bytes %.200s", r.code, r.hdr.Get("Content-Type"), len(r.body), r.body)
	}
	out := filepath.Join(t.TempDir(), "s.mp4")
	_ = os.WriteFile(out, r.body, 0o600)
	got, err := osexec.Command(fp, "-v", "error", "-show_entries", "stream=codec_name", "-of", "csv=p=0", out).Output()
	if err != nil || strings.Join(strings.Fields(string(got)), ",") != "h264,aac" {
		t.Fatalf("streams in the output: %q %v", got, err)
	}
	// Stopping the session from Activity closes the stream for good.
	if r := f.do("POST", "/api/v1/admin/activity/sessions/"+sess.SessionID+"/terminate", f.adminTok, nil); r.code != 200 {
		t.Fatalf("terminate: %d", r.code)
	}
	if r := f.do("GET", seek.URL, f.adminTok, nil); r.code != 410 {
		t.Fatalf("stream after terminate: %d", r.code)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/playback/sessions/%s/seek", "nope"), f.adminTok, nil); r.code != 404 {
		t.Fatalf("unknown session: %d", r.code)
	}
}

// firefoxCaps is a browser with H.264 but no HEVC decoder, as Firefox
// reports on most systems.
func firefoxCaps(fmp4 bool) map[string]any {
	c := browserCaps(fmp4)
	c["video_codecs"] = []map[string]any{{"codec": "h264", "max_width": 1920, "max_height": 1080}}
	return c
}

func TestTranscodeDecision(t *testing.T) {
	f := newFixture(t)
	f.srv.Playback.CanRemux = true
	f.srv.Remux = remux.New("ffmpeg", "ffprobe", 2, nil)
	start := func(tok string, version int64, caps map[string]any) resp {
		t.Helper()
		return f.do("POST", "/api/v1/playback/sessions", tok, map[string]any{"item_id": f.movieID, "version_id": version, "capabilities": caps})
	}
	// The 1080p film is HEVC: a browser without an HEVC decoder needs the video
	// transcoded. Before transcoding existed (and with an ffmpeg that lacks
	// libx264) that is refused, for every client.
	before := map[bool]resp{}
	for _, fmp4 := range []bool{false, true} {
		before[fmp4] = start(f.adminTok, f.v1080, firefoxCaps(fmp4))
		if before[fmp4].code != 422 || !strings.Contains(string(before[fmp4].body), "transcode_unavailable") {
			t.Fatalf("no libx264, fmp4 %v: %d %s", fmp4, before[fmp4].code, before[fmp4].body)
		}
	}
	f.srv.Playback.CanTranscode = true
	var sess struct {
		SessionID string `json:"session_id"`
		Decision  string
		Stream    struct {
			URL, Protocol string
			SeekURL       string `json:"seek_url"`
		}
	}
	r := start(f.adminTok, f.v1080, firefoxCaps(true))
	if r.code != 201 {
		t.Fatalf("start: %d %s", r.code, r.body)
	}
	r.json(t, &sess)
	if sess.Decision != "transcode" || sess.Stream.Protocol != "fmp4" || !strings.HasSuffix(sess.Stream.URL, "/stream.mp4") || sess.Stream.SeekURL == "" {
		t.Fatalf("transcode session: %+v", sess)
	}
	// A transcoded stream starts exactly where it is asked to, not on a keyframe.
	var seek struct {
		StartMs int64 `json:"start_ms"`
		URL     string
	}
	r = f.do("GET", sess.Stream.SeekURL+"?position_ms=12700", f.adminTok, nil)
	r.json(t, &seek)
	if r.code != 200 || seek.StartMs != 12700 || !strings.HasSuffix(seek.URL, "start_ms=12700") {
		t.Fatalf("seek: %d %s", r.code, r.body)
	}
	// The TV app does not send fmp4_stream: refused exactly as before.
	r = start(f.adminTok, f.v1080, firefoxCaps(false))
	if r.code != before[false].code || string(r.body) != string(before[false].body) {
		t.Fatalf("without fmp4_stream: %d %s\nbefore: %d %s", r.code, r.body, before[false].code, before[false].body)
	}
	// A transcode makes H.264, so a client without it gains nothing.
	noH264 := firefoxCaps(true)
	noH264["video_codecs"] = []map[string]any{{"codec": "vp9"}}
	if r := start(f.adminTok, f.v1080, noH264); r.code != 422 || !strings.Contains(string(r.body), "transcode_unavailable") {
		t.Fatalf("client without H.264: %d %s", r.code, r.body)
	}
	// Policy applies as to every conversion: allow_transcode, then 4K.
	var friendID int64
	if err := f.db.QueryRow(`SELECT id FROM users WHERE username = 'friend'`).Scan(&friendID); err != nil {
		t.Fatal(err)
	}
	if r := start(f.friendTok, f.v1080, firefoxCaps(true)); r.code != 201 || !strings.Contains(string(r.body), `"decision":"transcode"`) {
		t.Fatalf("friend allowed to transcode: %d %s", r.code, r.body)
	}
	if _, err := f.db.Exec(`UPDATE user_policies SET allow_transcode = 0 WHERE user_id = ?`, friendID); err != nil {
		t.Fatal(err)
	}
	if r := start(f.friendTok, f.v1080, firefoxCaps(true)); r.code != 403 || !strings.Contains(string(r.body), "not allowed") {
		t.Fatalf("friend not allowed to transcode: %d %s", r.code, r.body)
	}
	if _, err := f.db.Exec(`UPDATE user_policies SET allow_transcode = 1, max_resolution = '4K', allow_4k_transcode = 0 WHERE user_id = ?`, friendID); err != nil {
		t.Fatal(err)
	}
	if r := start(f.friendTok, f.v4k, firefoxCaps(true)); r.code != 403 || !strings.Contains(string(r.body), "4K files cannot be converted") {
		t.Fatalf("friend transcoding 4K: %d %s", r.code, r.body)
	}
}

func TestTranscodeEndToEnd(t *testing.T) {
	ff, err1 := osexec.LookPath("ffmpeg")
	fp, err2 := osexec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg or ffprobe not on PATH")
	}
	rmx := remux.New(ff, fp, 2, nil)
	if feat, err := rmx.Detect(context.Background()); err != nil || !feat.X264 {
		t.Skipf("ffmpeg has no libx264: %v", err)
	}
	f := newFixture(t)
	f.srv.Playback.CanRemux, f.srv.Playback.CanTranscode = true, true
	f.srv.Remux = rmx
	// The episode becomes a real 30 s MPEG-4 Part 2 file, which no browser
	// decodes, with AAC audio and a keyframe only every 10 s.
	var epPath string
	if err := f.db.QueryRow(`SELECT path FROM media_versions WHERE id = ?`, f.episodeVerID).Scan(&epPath); err != nil {
		t.Fatal(err)
	}
	if msg, err := osexec.Command(ff, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24:duration=30",
		"-f", "lavfi", "-i", "sine=duration=30", "-c:v", "mpeg4", "-g", "240", "-c:a", "aac", "-y", epPath+".tmp.mkv").CombinedOutput(); err != nil {
		t.Skipf("fixture needs the mpeg4 and aac encoders: %v %s", err, msg)
	}
	if err := os.Rename(epPath+".tmp.mkv", epPath); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE media_versions SET video_codec = 'mpeg4', video_profile = 'simple profile', bit_depth = 8,
			width = 640, height = 360, resolution = '480p', frame_rate = 24, duration_ms = 30000 WHERE id = ?`, f.episodeVerID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO media_streams (version_id, stream_index, kind, codec, channels, is_default) VALUES (?, 1, 'audio', 'aac', 1, 1)`, f.episodeVerID); err != nil {
		t.Fatal(err)
	}
	// A small screen, so the picture is scaled down too.
	caps := browserCaps(true)
	caps["max_width"], caps["max_height"] = 320, 240
	var sess struct {
		SessionID string `json:"session_id"`
		Decision  string
		Stream    struct {
			Protocol string
			SeekURL  string `json:"seek_url"`
		}
	}
	r := f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.episodeID, "capabilities": caps})
	r.json(t, &sess)
	if r.code != 201 || sess.Decision != "transcode" || sess.Stream.Protocol != "fmp4" {
		t.Fatalf("start: %d %s", r.code, r.body)
	}
	var seek struct {
		StartMs int64 `json:"start_ms"`
		URL     string
	}
	r = f.do("GET", sess.Stream.SeekURL+"?position_ms=12700", f.adminTok, nil)
	r.json(t, &seek)
	if r.code != 200 || seek.StartMs != 12700 {
		t.Fatalf("seek: %d %s", r.code, r.body)
	}
	r = f.do("GET", seek.URL, f.adminTok, nil)
	if r.code != 200 || r.hdr.Get("Content-Type") != "video/mp4" || len(r.body) < 10000 {
		t.Fatalf("stream: %d %s %d bytes %.200s", r.code, r.hdr.Get("Content-Type"), len(r.body), r.body)
	}
	out := filepath.Join(t.TempDir(), "s.mp4")
	if err := os.WriteFile(out, r.body, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := osexec.Command(fp, "-v", "error", "-show_entries", "stream=codec_name,width,height,duration", "-of", "csv=p=0", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "h264,320,180,") || !strings.HasPrefix(lines[1], "aac,") {
		t.Fatalf("streams in the output: %q", got)
	}
	// Both streams run the 17.3 s from 12.7 s to the end: output time 0 is
	// start_ms, so the player's start_ms plus currentTime is the true position.
	for _, l := range lines {
		fs := strings.Split(l, ",")
		d, _ := strconv.ParseFloat(fs[len(fs)-1], 64)
		if d < 17.0 || d > 17.6 {
			t.Errorf("%s runs %.2f s, want about 17.3 (stream started at the wrong place)", fs[0], d)
		}
	}
	// Activity shows it as converting, with the transcode decision.
	if r := f.do("POST", "/api/v1/playback/sessions/"+sess.SessionID+"/events", f.adminTok, map[string]any{"type": "start", "position_ms": 12700}); r.code != 204 {
		t.Fatalf("start event: %d %s", r.code, r.body)
	}
	var now struct {
		Converting int
		Sessions   []struct{ Decision string }
	}
	f.do("GET", "/api/v1/admin/activity/now", f.adminTok, nil).json(t, &now)
	if now.Converting != 1 || len(now.Sessions) != 1 || now.Sessions[0].Decision != "transcode" {
		t.Fatalf("now playing: %+v", now)
	}
}

// Stop and error events end the session's open streams, so a client that keeps
// reading after it stopped does not hold a conversion slot. Other events leave
// them alone.
func TestStopEventCutsOpenStreams(t *testing.T) {
	f := newFixture(t)
	f.srv.Playback.CanRemux = true
	f.srv.Remux = remux.New("ffmpeg", "ffprobe", 2, nil)
	start := func() string {
		t.Helper()
		var sess struct {
			SessionID string `json:"session_id"`
		}
		r := f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": browserCaps(true)})
		if r.code != 201 {
			t.Fatalf("start: %d %s", r.code, r.body)
		}
		r.json(t, &sess)
		return sess.SessionID
	}
	open := func(id string) context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		f.srv.streams.add(id, &cancel)
		t.Cleanup(func() { f.srv.streams.remove(id, &cancel); cancel() })
		return ctx
	}
	event := func(id, typ string) {
		t.Helper()
		if r := f.do("POST", "/api/v1/playback/sessions/"+id+"/events", f.adminTok, map[string]any{"type": typ, "position_ms": 1000}); r.code != 204 {
			t.Fatalf("%s event: %d %s", typ, r.code, r.body)
		}
	}
	for _, typ := range []string{"stop", "error"} {
		id := start()
		ctx := open(id)
		event(id, "progress")
		if ctx.Err() != nil {
			t.Fatalf("a progress event cut the stream")
		}
		event(id, typ)
		if ctx.Err() == nil {
			t.Fatalf("after a %s event the stream is still open", typ)
		}
	}
}

// A cancelled stream must not wait for a client that has stopped reading: the
// pending write fails at once, so the conversion slot frees.
func TestCancelledStreamDoesNotWaitForASlowReader(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan time.Duration, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = streamUntilCancelled(ctx, w, func() error {
			chunk := make([]byte, 64<<10)
			for {
				if _, err := w.Write(chunk); err != nil {
					return err
				}
			}
		})
		returned <- 0
	}))
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// Ask for the stream and never read it, so the server's writes fill the
	// socket buffers and block.
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	cancel()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream kept writing to a reader that had stopped, after it was cancelled")
	}
}
