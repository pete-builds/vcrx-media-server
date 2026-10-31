package api

import (
	"bytes"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
)

// safariCaps is what the web app reports in Safari: H.264 and HEVC, AAC but
// not AC-3 in a browser, no fragmented MP4 stream, HLS instead.
func safariCaps() map[string]any {
	c := browserCaps(false)
	c["hls_stream"] = true
	return c
}

// withHLS turns HLS on for the fixture, keeping streams under a temp dir.
func withHLS(t *testing.T, f *fixture, rmx *remux.Service) (*remux.HLS, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "hls")
	h, err := remux.NewHLS(rmx, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	f.srv.Remux, f.srv.HLS = rmx, h
	f.srv.Playback.CanRemux, f.srv.Playback.CanHLS = true, true
	return h, root
}

type hlsSession struct {
	SessionID string `json:"session_id"`
	Decision  string
	Stream    struct {
		URL, Protocol string
		MimeType      string `json:"mime_type"`
		SeekURL       string `json:"seek_url"`
	}
}

func TestHLSDecision(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.srv.Playback.CanRemux = true
	f.srv.Remux = remux.New("ffmpeg", "ffprobe", 2, nil)
	start := func(caps map[string]any) resp {
		return f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": caps})
	}
	// Before HLS, Safari (no fmp4_stream) is refused a file whose E-AC-3 audio it cannot decode.
	if r := start(safariCaps()); r.code != 422 || !strings.Contains(string(r.body), "transcode_unavailable") {
		t.Fatalf("Safari without HLS: %d %s", r.code, r.body)
	}
	withHLS(t, f, f.srv.Remux)
	var sess hlsSession
	r := start(safariCaps())
	r.json(t, &sess)
	if r.code != 201 || sess.Decision != "audio_transcode" || sess.Stream.Protocol != "hls" ||
		sess.Stream.MimeType != "application/vnd.apple.mpegurl" || sess.Stream.SeekURL == "" || sess.Stream.URL != sess.Stream.SeekURL {
		t.Fatalf("Safari with HLS: %d %s", r.code, r.body)
	}
	// Chrome still gets the fragmented MP4 stream, exactly as before.
	r = start(browserCaps(true))
	r.json(t, &sess)
	if sess.Stream.Protocol != "fmp4" || !strings.HasSuffix(sess.Stream.URL, "/stream.mp4") {
		t.Fatalf("Chrome: %s", r.body)
	}
	// A client that reports both gets HLS.
	both := safariCaps()
	both["fmp4_stream"] = true
	start(both).json(t, &sess)
	if sess.Stream.Protocol != "hls" {
		t.Fatalf("both: %+v", sess)
	}
	// The same policy as every conversion.
	var friendID int64
	if err := f.db.QueryRow(`SELECT id FROM users WHERE username = 'friend'`).Scan(&friendID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE user_policies SET allow_transcode = 0 WHERE user_id = ?`, friendID); err != nil {
		t.Fatal(err)
	}
	r = f.do("POST", "/api/v1/playback/sessions", f.friendTok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": safariCaps()})
	if r.code != 403 || !strings.Contains(string(r.body), "not allowed") {
		t.Fatalf("friend not allowed to convert, in Safari: %d %s", r.code, r.body)
	}
}

// seekHLS asks the seek endpoint for a stream and returns its start and URL.
func (f *fixture) seekHLS(t *testing.T, sess hlsSession, tok string, query string) (int64, string, resp) {
	t.Helper()
	var seek struct {
		StartMs int64 `json:"start_ms"`
		URL     string
	}
	r := f.do("GET", sess.Stream.SeekURL+"?"+query, tok, nil)
	if r.code == 200 {
		r.json(t, &seek)
	}
	return seek.StartMs, seek.URL, r
}

// fullPlaylist polls a playlist until ffmpeg has finished it.
func (f *fixture) fullPlaylist(t *testing.T, url string) string {
	t.Helper()
	for i := 0; i < 200; i++ {
		r := f.do("GET", url, f.adminTok, nil)
		if r.code != 200 || r.hdr.Get("Content-Type") != "application/vnd.apple.mpegurl" || r.hdr.Get("Cache-Control") != "no-store" {
			t.Fatalf("playlist: %d %v %s", r.code, r.hdr, r.body)
		}
		if strings.Contains(string(r.body), "#EXT-X-ENDLIST") {
			return string(r.body)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the playlist never ended")
	return ""
}

// segmentsOf lists the URIs a playlist names, init segment first, and the
// sum of its segment durations.
func segmentsOf(t *testing.T, pl string) ([]string, []float64) {
	t.Helper()
	var uris []string
	var durs []float64
	for _, l := range strings.Split(pl, "\n") {
		switch {
		case strings.HasPrefix(l, "#EXT-X-MAP:URI="):
			uris = append([]string{strings.Trim(strings.TrimPrefix(l, "#EXT-X-MAP:URI="), `"`)}, uris...)
		case strings.HasPrefix(l, "#EXTINF:"):
			d, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(l, "#EXTINF:"), ","), 64)
			if err != nil {
				t.Fatalf("EXTINF %q", l)
			}
			durs = append(durs, d)
		case l != "" && !strings.HasPrefix(l, "#"):
			uris = append(uris, l)
		}
	}
	return uris, durs
}

// assemble fetches the init segment and every media segment into one file,
// which is what a player decodes, for ffprobe.
func (f *fixture) assemble(t *testing.T, base string, uris []string) string {
	t.Helper()
	var all bytes.Buffer
	for _, u := range uris {
		r := f.do("GET", base+u, f.adminTok, nil)
		if r.code != 200 || r.hdr.Get("Content-Type") != "video/mp4" {
			t.Fatalf("segment %s: %d %s", u, r.code, r.body)
		}
		all.Write(r.body)
	}
	out := filepath.Join(t.TempDir(), "all.mp4")
	if err := os.WriteFile(out, all.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return out
}

func sum(ds []float64) float64 {
	s := 0.0
	for _, d := range ds {
		s += d
	}
	return s
}

// The common case on an iPhone: H.264 in Matroska with AC-3 audio, played
// from the start and after a seek, as HLS with the video copied and the audio
// converted to AAC; owner-only, traversal-proof, and cleaned up at the end.
func TestHLSEndToEnd(t *testing.T) {
	t.Parallel()
	ff, err1 := osexec.LookPath("ffmpeg")
	fp, err2 := osexec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg or ffprobe not on PATH")
	}
	f := newFixture(t)
	_, root := withHLS(t, f, remux.New(ff, fp, 2, nil))
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
	if _, err := f.db.Exec(`UPDATE media_versions SET video_codec = 'h264', video_profile = 'high', bit_depth = 8, duration_ms = 30000 WHERE id = ?`, f.episodeVerID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO media_streams (version_id, stream_index, kind, codec, channels, is_default) VALUES (?, 1, 'audio', 'ac3', 2, 1)`, f.episodeVerID); err != nil {
		t.Fatal(err)
	}
	var sess hlsSession
	r := f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.episodeID, "capabilities": safariCaps()})
	r.json(t, &sess)
	if r.code != 201 || sess.Decision != "audio_transcode" || sess.Stream.Protocol != "hls" {
		t.Fatalf("start: %d %s", r.code, r.body)
	}

	// From the start: the playlist covers the whole 30 s and starts at its beginning.
	start, url, r := f.seekHLS(t, sess, f.adminTok, "position_ms=0")
	if r.code != 200 || start != 0 || !strings.HasSuffix(url, "/index.m3u8") {
		t.Fatalf("seek: %d %s", r.code, r.body)
	}
	// A player is given a growing playlist only once it lists three segments.
	if first := f.do("GET", url, f.adminTok, nil); !strings.Contains(string(first.body), "#EXT-X-ENDLIST") &&
		strings.Count(string(first.body), "#EXTINF") < 3 {
		t.Fatalf("the first playlist lists fewer than three segments:\n%s", first.body)
	}
	pl := f.fullPlaylist(t, url)
	if !strings.HasPrefix(pl, "#EXTM3U\n#EXT-X-START:TIME-OFFSET=0\n") || !strings.Contains(pl, "#EXT-X-PLAYLIST-TYPE:EVENT") {
		t.Fatalf("playlist header:\n%s", pl)
	}
	uris, durs := segmentsOf(t, pl)
	if total := sum(durs); total < 29.5 || total > 30.5 {
		t.Fatalf("the playlist covers %.2f s of a 30 s file:\n%s", total, pl)
	}
	base := strings.TrimSuffix(url, "index.m3u8")
	out := f.assemble(t, base, uris)
	got, err := osexec.Command(fp, "-v", "error", "-show_entries", "stream=codec_name:format=duration", "-of", "csv=p=0", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(got))
	if len(lines) != 3 || lines[0] != "h264" || lines[1] != "aac" {
		t.Fatalf("segments carry %q, want copied h264 and aac", got)
	}
	if d, _ := strconv.ParseFloat(lines[2], 64); d < 29.5 || d > 30.5 {
		t.Fatalf("segments run %.2f s, want 30", d)
	}

	// A seek starts a new stream on the keyframe before the position and
	// replaces the old one, whose URLs then find nothing.
	start, url2, r := f.seekHLS(t, sess, f.adminTok, "position_ms=12700")
	if r.code != 200 || start != 12000 || url2 == url {
		t.Fatalf("seek to 12.7 s: %d %s", r.code, r.body)
	}
	if r := f.do("GET", url, f.adminTok, nil); r.code != 404 {
		t.Fatalf("the replaced stream's playlist: %d", r.code)
	}
	pl = f.fullPlaylist(t, url2)
	if _, durs := segmentsOf(t, pl); sum(durs) < 17.5 || sum(durs) > 18.5 {
		t.Fatalf("from 12 s the playlist covers %.2f s, want 18:\n%s", sum(durs), pl)
	}
	base = strings.TrimSuffix(url2, "index.m3u8")
	if r := f.do("GET", base+"seg00000.m4s", f.adminTok, nil); r.code != 200 {
		t.Fatalf("segment: %d", r.code)
	}
	// Ranges work on segments.
	if r := f.do("GET", base+"init.mp4", f.adminTok, nil, "Range", "bytes=0-7"); r.code != 206 || len(r.body) != 8 {
		t.Fatalf("range on the init segment: %d %d bytes", r.code, len(r.body))
	}
	// An audio track the file does not have.
	if _, _, r := f.seekHLS(t, sess, f.adminTok, "position_ms=0&audio=9"); r.code != 400 {
		t.Fatalf("missing audio track: %d", r.code)
	}

	// Only the owner: the friend and a stranger see no such session.
	run := strings.Split(strings.TrimPrefix(url2, "/api/v1/playback/sessions/"+sess.SessionID+"/hls/"), "/")[0]
	for _, tok := range []string{f.friendTok, f.strangerTok, ""} {
		for _, u := range []string{url2, base + "init.mp4", base + "seg00000.m4s"} {
			if r := f.do("GET", u, tok, nil); r.code != 404 && r.code != 401 {
				t.Errorf("another user fetching %s: %d", u, r.code)
			}
		}
		if _, _, r := f.seekHLS(t, sess, tok, "position_ms=0"); r.code != 404 && r.code != 401 {
			t.Errorf("another user seeking: %d", r.code)
		}
	}

	// Only files the stream wrote: a path, the playlist's temp file, another
	// session's files. A file planted beside the stream proves the check.
	sessDir := filepath.Join(root, sess.SessionID)
	if err := os.WriteFile(filepath.Join(sessDir, "secret.txt"), []byte("not for you"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"..%2Fsecret.txt", "..%2F..%2F" + sess.SessionID + "%2Fsecret.txt", "%2Fetc%2Fpasswd",
		"index.m3u8.tmp", "seg00099.m4s.tmp", "seg00099.m4s", "secret.txt"} {
		r := f.do("GET", base+name, f.adminTok, nil)
		if r.code != 404 || strings.Contains(string(r.body), "not for you") {
			t.Errorf("%s: %d %s", name, r.code, r.body)
		}
	}
	for _, u := range []string{
		"/api/v1/playback/sessions/" + sess.SessionID + "/hls/..%2F" + run + "/init.mp4",
		"/api/v1/playback/sessions/" + sess.SessionID + "/hls/0000000000000000/init.mp4",
		"/api/v1/playback/sessions/" + sess.SessionID + "/hls/" + run + "%2Finit.mp4/index.m3u8",
	} {
		if r := f.do("GET", u, f.adminTok, nil); r.code != 404 {
			t.Errorf("%s: %d", u, r.code)
		}
	}
	_ = os.Remove(filepath.Join(sessDir, "secret.txt"))

	// The session ends: the stream stops, its files go, its URLs answer 409.
	if r := f.do("POST", "/api/v1/playback/sessions/"+sess.SessionID+"/events", f.adminTok, map[string]any{"type": "stop", "position_ms": 15000}); r.code != 204 {
		t.Fatalf("stop: %d %s", r.code, r.body)
	}
	if _, err := os.Stat(sessDir); !os.IsNotExist(err) {
		t.Fatalf("HLS files survived the session's end: %v", err)
	}
	if r := f.do("GET", url2, f.adminTok, nil); r.code != 409 {
		t.Fatalf("playlist after stop: %d", r.code)
	}

	// Stopped from Activity: the same, and 410 for the player to recognise.
	var sess2 hlsSession
	f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.episodeID, "capabilities": safariCaps()}).json(t, &sess2)
	_, url3, r := f.seekHLS(t, sess2, f.adminTok, "position_ms=0")
	if r.code != 200 {
		t.Fatalf("seek: %d %s", r.code, r.body)
	}
	if r := f.do("POST", "/api/v1/admin/activity/sessions/"+sess2.SessionID+"/terminate", f.adminTok, nil); r.code != 200 {
		t.Fatalf("terminate: %d", r.code)
	}
	if _, err := os.Stat(filepath.Join(root, sess2.SessionID)); !os.IsNotExist(err) {
		t.Fatalf("HLS files survived terminate: %v", err)
	}
	if r := f.do("HEAD", url3, f.adminTok, nil); r.code != 410 {
		t.Fatalf("playlist after terminate: %d", r.code)
	}
}

// A file no browser decodes (MPEG-4 Part 2) plays in Safari as HLS with the
// video transcoded to H.264 in 4 s segments. Its MP3 audio, which the
// browser decodes and an MP4 stream would copy, is converted to AAC, since
// HLS fMP4 segments cannot carry MP3.
func TestHLSTranscodeEndToEnd(t *testing.T) {
	t.Parallel()
	ff, err1 := osexec.LookPath("ffmpeg")
	fp, err2 := osexec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg or ffprobe not on PATH")
	}
	rmx := remux.New(ff, fp, 2, nil)
	if feat, err := rmx.Detect(t.Context()); err != nil || !feat.X264 {
		t.Skipf("ffmpeg has no libx264: %v", err)
	}
	f := newFixture(t)
	withHLS(t, f, rmx)
	f.srv.Playback.CanTranscode = true
	var epPath string
	if err := f.db.QueryRow(`SELECT path FROM media_versions WHERE id = ?`, f.episodeVerID).Scan(&epPath); err != nil {
		t.Fatal(err)
	}
	if msg, err := osexec.Command(ff, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24:duration=20",
		"-f", "lavfi", "-i", "sine=duration=20", "-c:v", "mpeg4", "-g", "240", "-c:a", "libmp3lame", "-y", epPath+".tmp.mkv").CombinedOutput(); err != nil {
		t.Skipf("fixture needs the mpeg4 and libmp3lame encoders: %v %s", err, msg)
	}
	if err := os.Rename(epPath+".tmp.mkv", epPath); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE media_versions SET video_codec = 'mpeg4', video_profile = 'simple profile', bit_depth = 8,
			width = 320, height = 180, frame_rate = 24, duration_ms = 20000 WHERE id = ?`, f.episodeVerID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO media_streams (version_id, stream_index, kind, codec, channels, is_default) VALUES (?, 1, 'audio', 'mp3', 2, 1)`, f.episodeVerID); err != nil {
		t.Fatal(err)
	}
	caps := safariCaps()
	caps["audio_codecs"] = []map[string]any{{"codec": "aac"}, {"codec": "mp3", "max_channels": 2}}
	var sess hlsSession
	r := f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.episodeID, "capabilities": caps})
	r.json(t, &sess)
	if r.code != 201 || sess.Decision != "transcode" || sess.Stream.Protocol != "hls" {
		t.Fatalf("start: %d %s", r.code, r.body)
	}
	_, url, r := f.seekHLS(t, sess, f.adminTok, "position_ms=0")
	if r.code != 200 {
		t.Fatalf("seek: %d %s", r.code, r.body)
	}
	pl := f.fullPlaylist(t, url)
	uris, durs := segmentsOf(t, pl)
	if total := sum(durs); total < 19.5 || total > 20.5 {
		t.Fatalf("the playlist covers %.2f s of a 20 s file:\n%s", total, pl)
	}
	for _, d := range durs[:len(durs)-1] {
		if d < 3.9 || d > 4.1 {
			t.Fatalf("a transcoded segment runs %.2f s, want 4:\n%s", d, pl)
		}
	}
	out := f.assemble(t, strings.TrimSuffix(url, "index.m3u8"), uris)
	got, err := osexec.Command(fp, "-v", "error", "-show_entries", "stream=codec_name", "-of", "csv=p=0", out).Output()
	if err != nil || strings.Join(strings.Fields(string(got)), ",") != "h264,aac" {
		t.Fatalf("segments carry %q (%v), want h264 and aac", got, err)
	}
}

// HLS streams take the same conversion slots as fMP4 streams.
func TestHLSSlotLimit(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh")
	}
	// An "ffmpeg" that runs until it is stopped, so each stream holds its slot.
	ff := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(ff, []byte("#!/bin/sh\nexec sleep 60\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	withHLS(t, f, remux.New(ff, "ffprobe", 1, nil))
	newSession := func() hlsSession {
		var s hlsSession
		r := f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": safariCaps()})
		r.json(t, &s)
		if s.Stream.Protocol != "hls" {
			t.Fatalf("start: %s", r.body)
		}
		return s
	}
	a, b := newSession(), newSession()
	if _, _, r := f.seekHLS(t, a, f.adminTok, "position_ms=0"); r.code != 200 {
		t.Fatalf("first stream: %d %s", r.code, r.body)
	}
	if _, _, r := f.seekHLS(t, b, f.adminTok, "position_ms=0"); r.code != 503 || !strings.Contains(string(r.body), "busy") {
		t.Fatalf("second stream with one slot: %d %s", r.code, r.body)
	}
	// A seek replaces the session's own stream, so it needs no second slot.
	if _, _, r := f.seekHLS(t, a, f.adminTok, "position_ms=0"); r.code != 200 {
		t.Fatalf("seek in the first session: %d %s", r.code, r.body)
	}
	// The first session ends, which frees its slot.
	if r := f.do("POST", "/api/v1/playback/sessions/"+a.SessionID+"/events", f.adminTok, map[string]any{"type": "stop", "position_ms": 0}); r.code != 204 {
		t.Fatalf("stop: %d", r.code)
	}
	if _, _, r := f.seekHLS(t, b, f.adminTok, "position_ms=0"); r.code != 200 {
		t.Fatalf("second stream after the first ended: %d %s", r.code, r.body)
	}
}
