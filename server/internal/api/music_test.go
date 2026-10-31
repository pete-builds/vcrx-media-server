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
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/admin"
	"github.com/pete-builds/vcrx-media-server/server/internal/library"
	"github.com/pete-builds/vcrx-media-server/server/internal/music"
	mediaprobe "github.com/pete-builds/vcrx-media-server/server/internal/probe"
	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
)

type musicIDs struct {
	library, artist, album, flac, alac int64
}

// withMusic adds a scanned music library to the fixture: a two-track FLAC
// album with a cover.jpg, and an ALAC track Chrome cannot decode. The friend
// is not granted it.
func withMusic(t *testing.T, f *fixture) musicIDs {
	t.Helper()
	ff, err1 := osexec.LookPath("ffmpeg")
	_, err2 := osexec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("ffmpeg and ffprobe must be installed in CI")
		}
		t.Skip("ffmpeg or ffprobe not on PATH")
	}
	root := filepath.Join(filepath.Dir(f.root), "Music")
	mk := func(rel, codec string, freq int, tags ...string) {
		t.Helper()
		out := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatal(err)
		}
		args := []string{"-loglevel", "error", "-y", "-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=%d:duration=3", freq)}
		for i := 0; i+1 < len(tags); i += 2 {
			args = append(args, "-metadata", tags[i]+"="+tags[i+1])
		}
		if b, err := osexec.Command(ff, append(args, "-c:a", codec, out)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v %s", err, b)
		}
	}
	mk("Glass Harbor/Signal Fires/01 Low Tide.flac", "flac", 330, "title", "Low Tide", "artist", "Glass Harbor", "album", "Signal Fires", "track", "1")
	mk("Glass Harbor/Signal Fires/02 Breakwater.flac", "flac", 440, "title", "Breakwater", "artist", "Glass Harbor", "album", "Signal Fires", "track", "2")
	mk("Cedar Static/Low Orbit/01 Apogee.m4a", "alac", 550, "title", "Apogee", "artist", "Cedar Static", "album", "Low Orbit")
	if b, err := osexec.Command(ff, "-loglevel", "error", "-f", "lavfi", "-i", "color=c=0x1b3a6b:s=300x300", "-frames:v", "1",
		filepath.Join(root, "Glass Harbor", "Signal Fires", "cover.jpg")).CombinedOutput(); err != nil {
		t.Fatalf("cover: %v %s", err, b)
	}
	now := time.Now().UnixMilli()
	res, err := f.db.Exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('Music', 'music', ?, ?, ?)`, root, now, now)
	if err != nil {
		t.Fatal(err)
	}
	var ids musicIDs
	ids.library, _ = res.LastInsertId()
	sc := &library.Scanner{DB: f.db, Prober: &mediaprobe.FFprobe{}}
	if _, err := sc.Scan(context.Background(), library.Library{ID: ids.library, Name: "Music", Kind: library.KindMusic, RootPath: root}, library.Options{Trigger: "cli"}); err != nil {
		t.Fatal(err)
	}
	q := func(sql string, dst *int64) {
		t.Helper()
		if err := f.db.QueryRow(sql).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	q(`SELECT id FROM music_albums WHERE title = 'Signal Fires'`, &ids.album)
	q(`SELECT artist_id FROM music_albums WHERE title = 'Signal Fires'`, &ids.artist)
	q(`SELECT id FROM music_tracks WHERE title = 'Low Tide'`, &ids.flac)
	q(`SELECT id FROM music_tracks WHERE title = 'Apogee'`, &ids.alac)
	rmx := f.srv.Remux
	if rmx == nil {
		rmx = remux.New("ffmpeg", "ffprobe", 2, nil)
		f.srv.Remux = rmx
	}
	_ = rmx.DetectAudio(context.Background())
	mcat := &music.Catalog{DB: f.db}
	f.srv.Music = &Music{Catalog: mcat, Player: &music.Player{DB: f.db, Catalog: mcat, Remux: rmx},
		Covers: &music.Covers{Catalog: mcat, FFmpeg: ff, CacheDir: filepath.Join(t.TempDir(), "covers")}}
	return ids
}

// musicBrowserCaps is a Chrome-like music client: FLAC, MP3, AAC, Opus, no ALAC.
func musicBrowserCaps() map[string]any {
	return map[string]any{
		"containers":   []string{"flac", "mp3", "m4a", "mp4", "ogg", "opus", "wav", "webm"},
		"audio_codecs": []map[string]any{{"codec": "flac"}, {"codec": "mp3"}, {"codec": "aac"}, {"codec": "opus"}, {"codec": "pcm"}},
	}
}

// A friend without the Music grant gets 404 from every music route, exactly
// as for a video library they are not granted, and search finds nothing.
// With the grant, the same routes answer.
func TestMusicFollowsLibraryGrants(t *testing.T) {
	f := newFixture(t)
	ids := withMusic(t, f)
	routes := []struct{ method, path string }{
		{"GET", fmt.Sprintf("/api/v1/music/libraries/%d/artists", ids.library)},
		{"GET", fmt.Sprintf("/api/v1/music/libraries/%d/albums", ids.library)},
		{"GET", fmt.Sprintf("/api/v1/music/artists/%d", ids.artist)},
		{"GET", fmt.Sprintf("/api/v1/music/artists/%d/image", ids.artist)},
		{"GET", fmt.Sprintf("/api/v1/music/albums/%d", ids.album)},
		{"GET", fmt.Sprintf("/api/v1/music/albums/%d/cover", ids.album)},
		{"GET", fmt.Sprintf("/api/v1/music/tracks/%d/file", ids.flac)},
		{"HEAD", fmt.Sprintf("/api/v1/music/tracks/%d/file", ids.flac)},
		{"HEAD", fmt.Sprintf("/api/v1/music/tracks/%d/stream", ids.alac)},
		{"POST", fmt.Sprintf("/api/v1/music/tracks/%d/sessions", ids.flac)},
		{"POST", "/api/v1/music/queue"},
	}
	body := func(method, path string) any {
		switch {
		case strings.HasSuffix(path, "/queue"):
			return map[string]any{"album_id": ids.album, "capabilities": musicBrowserCaps()}
		case method == "POST":
			return map[string]any{"capabilities": musicBrowserCaps()}
		}
		return nil
	}
	for _, tok := range []struct{ who, tok string }{{"friend", f.friendTok}, {"stranger", f.strangerTok}} {
		for _, rt := range routes {
			if r := f.do(rt.method, rt.path, tok.tok, body(rt.method, rt.path)); r.code != 404 {
				t.Errorf("%s without the grant: %s %s = %d %s", tok.who, rt.method, rt.path, r.code, r.body)
			}
		}
		var sr music.SearchResult
		r := f.do("GET", "/api/v1/music/search?q=a", tok.tok, nil)
		r.json(t, &sr)
		if r.code != 200 || len(sr.Artists)+len(sr.Albums)+len(sr.Tracks) != 0 {
			t.Errorf("%s without the grant searched music: %d %s", tok.who, r.code, r.body)
		}
	}
	// A video library id is not a music library, even to the admin.
	if r := f.do("GET", "/api/v1/music/libraries/1/artists", f.adminTok, nil); r.code != 404 {
		t.Errorf("a movie library listed as music: %d", r.code)
	}
	var friendID int64
	if err := f.db.QueryRow(`SELECT id FROM users WHERE username = 'friend'`).Scan(&friendID); err != nil {
		t.Fatal(err)
	}
	var all string
	if err := f.db.QueryRow(`SELECT group_concat(id) FROM libraries`).Scan(&all); err != nil {
		t.Fatal(err)
	}
	var libs []int64
	for _, id := range strings.Split(all, ",") {
		n, _ := strconv.ParseInt(id, 10, 64)
		libs = append(libs, n)
	}
	if err := f.srv.Admin.SetGrants(context.Background(), friendID, libs); err != nil {
		t.Fatal(err)
	}
	for _, rt := range routes {
		r := f.do(rt.method, rt.path, f.friendTok, body(rt.method, rt.path))
		if r.code != 200 && r.code != 201 {
			t.Errorf("friend with the grant: %s %s = %d %s", rt.method, rt.path, r.code, r.body)
		}
	}
	var sr music.SearchResult
	f.do("GET", "/api/v1/music/search?q=tide", f.friendTok, nil).json(t, &sr)
	if len(sr.Tracks) != 1 || sr.Tracks[0].Title != "Low Tide" {
		t.Errorf("search with the grant: %+v", sr)
	}
}

// Direct play with Range, a listen recorded in Activity with its events, and
// a stop from Activity cutting the stream off.
func TestMusicDirectPlayAndListens(t *testing.T) {
	f := newFixture(t)
	ids := withMusic(t, f)
	var album music.AlbumDetail
	f.do("GET", fmt.Sprintf("/api/v1/music/albums/%d", ids.album), f.adminTok, nil).json(t, &album)
	if len(album.Tracks) != 2 || album.Tracks[0].Title != "Low Tide" || album.Tracks[1].Title != "Breakwater" || !album.HasCover {
		t.Fatalf("album: %+v", album)
	}
	r := f.do("GET", fmt.Sprintf("/api/v1/music/albums/%d/cover?width=100", ids.album), f.adminTok, nil)
	if r.code != 200 || r.hdr.Get("Content-Type") != "image/jpeg" || len(r.body) < 100 {
		t.Fatalf("cover: %d %s %d bytes", r.code, r.hdr.Get("Content-Type"), len(r.body))
	}

	var sess music.Session
	r = f.do("POST", fmt.Sprintf("/api/v1/music/tracks/%d/sessions", ids.flac), f.adminTok, map[string]any{"capabilities": musicBrowserCaps()})
	r.json(t, &sess)
	if r.code != 201 || sess.Decision != "direct_play" || sess.Stream.Protocol != music.Progressive || !strings.Contains(sess.Stream.URL, "session="+sess.SessionID) {
		t.Fatalf("listen: %d %s", r.code, r.body)
	}
	r = f.do("GET", sess.Stream.URL, f.adminTok, nil, "Range", "bytes=0-99")
	if r.code != 206 || len(r.body) != 100 || r.hdr.Get("Content-Type") != "audio/flac" || !strings.HasPrefix(r.hdr.Get("Content-Range"), "bytes 0-99/") {
		t.Fatalf("range: %d %q %q %d bytes", r.code, r.hdr.Get("Content-Type"), r.hdr.Get("Content-Range"), len(r.body))
	}
	if string(r.body[:4]) != "fLaC" {
		t.Errorf("not the FLAC file: %q", r.body[:4])
	}
	for _, ev := range []map[string]any{{"type": "start", "position_ms": 0}, {"type": "progress", "position_ms": 2000}} {
		if r := f.do("POST", "/api/v1/playback/sessions/"+sess.SessionID+"/events", f.adminTok, ev); r.code != 204 {
			t.Fatalf("event %v: %d %s", ev["type"], r.code, r.body)
		}
	}
	var kind, title, summary string
	var track int64
	if err := f.db.QueryRow(`SELECT item_kind, item_title, version_summary, track_id FROM playback_sessions WHERE id = ?`, sess.SessionID).
		Scan(&kind, &title, &summary, &track); err != nil {
		t.Fatal(err)
	}
	if kind != "track" || title != "Glass Harbor · Low Tide" || summary != "FLAC 44.1 kHz 16-bit" || track != ids.flac {
		t.Errorf("listen row: %q %q %q %d", kind, title, summary, track)
	}
	r = f.do("GET", "/api/v1/admin/activity/now", f.adminTok, nil)
	if r.code != 200 || !strings.Contains(string(r.body), "Low Tide") {
		t.Errorf("now playing does not show the listen: %d %s", r.code, r.body)
	}
	// Stopped from Activity: the file refuses that session from then on.
	if r := f.do("POST", "/api/v1/admin/activity/sessions/"+sess.SessionID+"/terminate", f.adminTok, map[string]string{"message": "Dinner"}); r.code != 200 {
		t.Fatalf("terminate: %d %s", r.code, r.body)
	}
	if r := f.do("GET", sess.Stream.URL, f.adminTok, nil); r.code != 410 {
		t.Errorf("a stopped listen still streams: %d", r.code)
	}
	r = f.do("POST", "/api/v1/playback/sessions/"+sess.SessionID+"/events", f.adminTok, map[string]any{"type": "progress", "position_ms": 2500})
	if r.code != 410 || !strings.Contains(string(r.body), "Dinner") {
		t.Errorf("events after a stop: %d %s", r.code, r.body)
	}
}

// A track the client cannot decode is converted with the real ffmpeg, and an
// account that may not have files converted is refused, as for video.
func TestMusicConvertsWhatTheClientCannotDecode(t *testing.T) {
	f := newFixture(t)
	ids := withMusic(t, f)
	var q struct {
		Tracks []music.QueueTrack `json:"tracks"`
	}
	r := f.do("POST", "/api/v1/music/queue", f.adminTok, map[string]any{"track_ids": []int64{ids.alac, ids.flac}, "capabilities": musicBrowserCaps()})
	r.json(t, &q)
	if r.code != 200 || len(q.Tracks) != 2 || q.Tracks[0].ID != ids.alac {
		t.Fatalf("queue: %d %s", r.code, r.body)
	}
	alac, flac := q.Tracks[0], q.Tracks[1]
	if alac.Stream == nil || alac.Stream.Protocol != music.Converted || alac.Stream.Decision != "audio_transcode" || alac.Stream.MimeType != "audio/aac" {
		t.Fatalf("ALAC for a browser: %+v", alac.Stream)
	}
	if flac.Stream == nil || flac.Stream.Protocol != music.Progressive {
		t.Fatalf("FLAC for a browser: %+v", flac.Stream)
	}
	r = f.do("GET", alac.Stream.URL, f.adminTok, nil)
	if r.code != 200 || r.hdr.Get("Content-Type") != "audio/aac" || len(r.body) < 1000 {
		t.Fatalf("converted: %d %q %d bytes %s", r.code, r.hdr.Get("Content-Type"), len(r.body), r.body[:min(len(r.body), 200)])
	}
	// ADTS frames start with a 12-bit sync word.
	if r.body[0] != 0xFF || r.body[1]&0xF0 != 0xF0 {
		t.Errorf("not ADTS AAC: % x", r.body[:4])
	}
	out := filepath.Join(t.TempDir(), "out.aac")
	if err := os.WriteFile(out, r.body, 0o644); err != nil {
		t.Fatal(err)
	}
	if b, err := osexec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_name,channels", "-of", "csv=p=0", out).Output(); err != nil || strings.TrimSpace(string(b)) != "aac,2" {
		t.Errorf("converted stream probes as %q %v", b, err)
	}
	// From a seek point the stream is shorter.
	r2 := f.do("GET", alac.Stream.URL+"&start_ms=2000", f.adminTok, nil)
	if r2.code != 200 || len(r2.body) >= len(r.body) {
		t.Errorf("start_ms: %d, %d bytes against %d", r2.code, len(r2.body), len(r.body))
	}

	// The friend, granted Music but not allowed to have files converted.
	var friendID int64
	if err := f.db.QueryRow(`SELECT id FROM users WHERE username = 'friend'`).Scan(&friendID); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := f.srv.Admin.SetGrants(ctx, friendID, []int64{ids.library}); err != nil {
		t.Fatal(err)
	}
	if err := f.srv.Admin.SetPolicy(ctx, friendID, admin.UserPolicy{AllowTranscode: false}); err != nil {
		t.Fatal(err)
	}
	r = f.do("POST", "/api/v1/music/queue", f.friendTok, map[string]any{"track_ids": []int64{ids.alac, ids.flac}, "capabilities": musicBrowserCaps()})
	q.Tracks = nil
	r.json(t, &q)
	if len(q.Tracks) != 2 || q.Tracks[0].Stream != nil || q.Tracks[0].Refusal == nil || q.Tracks[0].Refusal.Code != "playback_refused" || q.Tracks[1].Stream == nil {
		t.Fatalf("friend's queue: %s", r.body)
	}
	if r := f.do("GET", alac.Stream.URL, f.friendTok, nil); r.code != 403 {
		t.Errorf("friend fetched a converted stream: %d", r.code)
	}
	if r := f.do("POST", fmt.Sprintf("/api/v1/music/tracks/%d/sessions", ids.alac), f.friendTok, map[string]any{"capabilities": musicBrowserCaps()}); r.code != 403 {
		t.Errorf("friend's listen of a track that needs converting: %d %s", r.code, r.body)
	}
	var refused int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM playback_sessions WHERE track_id = ? AND decision = 'refused'`, ids.alac).Scan(&refused); err != nil || refused != 1 {
		t.Errorf("the refusal was not recorded: %d %v", refused, err)
	}
	// A device that decodes neither AAC nor Opus gets a clear refusal.
	r = f.do("POST", fmt.Sprintf("/api/v1/music/tracks/%d/sessions", ids.alac), f.adminTok,
		map[string]any{"capabilities": map[string]any{"audio_codecs": []map[string]any{{"codec": "mp3"}}}})
	if r.code != 422 || !strings.Contains(string(r.body), "neither AAC nor Opus") {
		t.Errorf("no codec to convert to: %d %s", r.code, r.body)
	}
}
