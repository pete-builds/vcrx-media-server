package api

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
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
