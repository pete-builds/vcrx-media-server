package live

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testStream serves a 20 s MPEG-TS test pattern (H.264 video, MP2 audio, so
// the audio must be converted) as a live source would.
func testStream(t *testing.T) (ffmpeg, url string) {
	t.Helper()
	ff, err := osexec.LookPath("ffmpeg")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("ffmpeg must be installed in CI")
		}
		t.Skip("ffmpeg not on PATH")
	}
	ts := filepath.Join(t.TempDir(), "live.ts")
	if b, err := osexec.Command(ff, "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=320x180:rate=25", "-f", "lavfi", "-i", "sine=frequency=440",
		"-t", "20", "-c:v", "libx264", "-preset", "ultrafast", "-g", "25", "-c:a", "mp2", "-f", "mpegts", ts).CombinedOutput(); err != nil {
		t.Fatalf("make the test stream: %v %s", err, b)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/live.ts" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, ts)
	}))
	t.Cleanup(srv.Close)
	return ff, srv.URL + "/live.ts"
}

func newStreams(t *testing.T, ff string) *Streams {
	t.Helper()
	s := &Streams{FFmpeg: ff, Root: t.TempDir(), Log: quiet, Ready: 20 * time.Second}
	t.Cleanup(s.Close)
	return s
}

func TestWatchSharesOnePullPerChannel(t *testing.T) {
	t.Parallel()
	ff, url := testStream(t)
	s := newStreams(t, ff)
	ctx := context.Background()
	req := WatchRequest{Channel: 7, Source: 1, URL: url, Playlist: "/config/channels.m3u"}
	run, err := s.Watch(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	pl, err := s.Playlist(ctx, 7, run, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pl), "#EXT-X-MAP:URI=\"init.mp4\"") || strings.Count(string(pl), "#EXTINF") < 3 || strings.Contains(string(pl), "#EXT-X-ENDLIST") {
		t.Fatalf("playlist:\n%s", pl)
	}
	again, err := s.Watch(ctx, req)
	if err != nil || again != run {
		t.Fatalf("a second viewer got run %q (%v), want the same pull %q", again, err, run)
	}
	for _, name := range []string{"init.mp4", "seg00000.m4s"} {
		p, err := s.Segment(7, run, "", name)
		if err != nil {
			t.Fatal(err)
		}
		if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Names the playlist never uses, and other channels' runs, reach nothing.
	for _, bad := range []struct {
		ch   int64
		run  string
		name string
	}{{7, run, "../../etc/passwd"}, {7, run, livePlaylist}, {7, "0000000000000000", "init.mp4"}, {8, run, "init.mp4"}} {
		if _, err := s.Segment(bad.ch, bad.run, "", bad.name); !errors.Is(err, ErrNoStream) {
			t.Errorf("Segment(%d, %s, %s) = %v", bad.ch, bad.run, bad.name, err)
		}
	}
	dir := filepath.Dir(func() string { p, _ := s.Segment(7, run, "", "init.mp4"); return p }())
	s.Stop(7)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("Stop left %s: %v", dir, err)
	}
	if _, err := s.Playlist(ctx, 7, run, ""); !errors.Is(err, ErrNoStream) {
		t.Errorf("playlist after Stop: %v", err)
	}
}

func TestFollowWritesOneFragmentedMP4(t *testing.T) {
	t.Parallel()
	ff, url := testStream(t)
	s := newStreams(t, ff)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run, err := s.Watch(ctx, WatchRequest{Channel: 7, Source: 1, URL: url, Playlist: "/config/a.m3u"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	flushes := 0
	// The test stream is a file, so the pull ends after it; Follow ends with it.
	err = s.Follow(ctx, 7, run, "", &out, func() { flushes++ }, func() bool { return true })
	if !errors.Is(err, ErrNoStream) {
		t.Fatalf("Follow ended with %v, want ErrNoStream when the pull ended", err)
	}
	b := out.Bytes()
	if len(b) < 8 || string(b[4:8]) != "ftyp" || !bytes.Contains(b, []byte("moof")) || flushes < 2 {
		t.Fatalf("%d bytes, %d flushes: want the init segment (ftyp) then media segments (moof)", len(b), flushes)
	}
	// Taking the grant away ends a stream within a few segments.
	run, err = s.Watch(ctx, WatchRequest{Channel: 8, Source: 1, URL: url, Playlist: "/config/a.m3u"})
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := s.Follow(ctx, 8, run, "", &out, func() {}, func() bool { return false }); !errors.Is(err, ErrNoStream) {
		t.Errorf("revoked: %v", err)
	}
}

func TestThePullStopsWhenTheLastViewerLeaves(t *testing.T) {
	t.Parallel()
	ff, url := testStream(t)
	s := newStreams(t, ff)
	ctx := context.Background()
	req := WatchRequest{Channel: 7, Source: 1, URL: url, Playlist: "/config/a.m3u", Viewer: "tv"}
	run, err := s.Watch(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	req.Viewer = "phone"
	if again, err := s.Watch(ctx, req); err != nil || again != run {
		t.Fatalf("second viewer: %q %v", again, err)
	}
	if err := s.Leave(7, "0000000000000000", "tv"); !errors.Is(err, ErrNoStream) {
		t.Errorf("leaving a run that is not running: %v", err)
	}
	if err := s.Leave(7, run, "tv"); err != nil || s.Running() != 1 {
		t.Fatalf("the TV left (%v): %d pulls, want the phone's still running", err, s.Running())
	}
	if err := s.Leave(7, run, "phone"); err != nil || s.Running() != 0 {
		t.Fatalf("the last viewer left (%v): %d pulls, want none", err, s.Running())
	}
	// Surfing past the stream cap: each channel left frees its slot at once.
	s.MaxStreams = 1
	for ch := int64(10); ch < 14; ch++ {
		run, err := s.Watch(ctx, WatchRequest{Channel: ch, Source: 1, URL: url, Playlist: "/config/a.m3u", Viewer: "tv"})
		if err != nil {
			t.Fatalf("channel %d with max_streams 1: %v", ch, err)
		}
		if err := s.Leave(ch, run, "tv"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheSweepDropsAViewerWhoStoppedAsking(t *testing.T) {
	t.Parallel()
	ff, url := testStream(t)
	s := newStreams(t, ff)
	s.Idle = time.Hour
	ctx := context.Background()
	if _, err := s.Watch(ctx, WatchRequest{Channel: 7, Source: 1, URL: url, Playlist: "/config/a.m3u", Viewer: "tv"}); err != nil {
		t.Fatal(err)
	}
	// The TV lost power an hour and a minute ago.
	s.mu.Lock()
	s.jobs[7].viewers["tv"] = time.Now().Add(-61 * time.Minute)
	s.mu.Unlock()
	s.sweep()
	if s.Running() != 0 {
		t.Errorf("%d pulls after the only viewer went quiet", s.Running())
	}
}

func TestNewestSegment(t *testing.T) {
	t.Parallel()
	pl := []byte("#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:4.0,\nseg00011.m4s\n#EXTINF:4.0,\nseg00012.m4s\n")
	if n := newestSegment(pl); n != 12 {
		t.Errorf("newest %d, want 12", n)
	}
	if n := newestSegment([]byte("#EXTM3U\n")); n != -1 {
		t.Errorf("empty: %d", n)
	}
}

func TestWatchLimits(t *testing.T) {
	t.Parallel()
	ff, url := testStream(t)
	s := newStreams(t, ff)
	s.MaxStreams = 2
	ctx := context.Background()
	watch := func(ch, src int64, max int) error {
		_, err := s.Watch(ctx, WatchRequest{Channel: ch, Source: src, SourceMax: max, URL: url, Playlist: "/config/a.m3u"})
		return err
	}
	if err := watch(1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := watch(2, 1, 1); !errors.Is(err, ErrBusy) {
		t.Errorf("second channel on a one-stream source: %v, want ErrBusy", err)
	}
	if err := watch(1, 1, 1); err != nil {
		t.Errorf("joining the running channel needs no new slot: %v", err)
	}
	if err := watch(3, 2, 0); err != nil {
		t.Fatal(err)
	}
	if err := watch(4, 3, 0); !errors.Is(err, ErrBusy) {
		t.Errorf("a third pull with max_streams 2: %v, want ErrBusy", err)
	}
	if n := s.Running(); n != 2 {
		t.Errorf("%d pulls running, want 2", n)
	}
}

func TestWatchChecksTheStreamAddress(t *testing.T) {
	t.Parallel()
	s := &Streams{FFmpeg: "ffmpeg-is-never-started", Root: t.TempDir(), Log: quiet,
		Resolve: func(_ context.Context, host string) ([]netip.Addr, error) {
			switch host {
			case "lan-playlist.example", "nas.example":
				return []netip.Addr{netip.MustParseAddr("192.168.1.20")}, nil
			case "rebind.example":
				return []netip.Addr{netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.0.0.5")}, nil
			default:
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}
		}}
	ctx := context.Background()
	refused := []WatchRequest{
		// A playlist from the internet naming the LAN, by address or by name.
		{Channel: 1, URL: "http://192.168.1.1/admin", Playlist: "https://playlists.example/free.m3u"},
		{Channel: 1, URL: "http://nas.example/stream.ts", Playlist: "https://playlists.example/free.m3u"},
		{Channel: 1, URL: "http://rebind.example/stream.ts", Playlist: "https://playlists.example/free.m3u"},
		{Channel: 1, URL: "http://169.254.169.254/latest/meta-data", Playlist: "https://playlists.example/free.m3u"},
		// Never anything but http(s), whatever the playlist.
		{Channel: 1, URL: "file:///etc/passwd", Playlist: "/config/a.m3u"},
		{Channel: 1, URL: "rtmp://cam.example/live", Playlist: "/config/a.m3u"},
	}
	for _, w := range refused {
		if _, err := s.Watch(ctx, w); !errors.Is(err, ErrRefusedAddress) {
			t.Errorf("%s from %s: %v, want ErrRefusedAddress", w.URL, w.Playlist, err)
		}
	}
	// The admin's own file, or a playlist served from the LAN, may name the LAN.
	for _, pl := range []string{"/config/a.m3u", "http://lan-playlist.example/list.m3u", "http://192.168.1.30:34400/lineup.m3u"} {
		if !s.lanPlaylist(ctx, pl) {
			t.Errorf("%s is not counted as the LAN's", pl)
		}
	}
	if s.lanPlaylist(ctx, "https://playlists.example/free.m3u") {
		t.Error("an internet playlist is counted as the LAN's")
	}
}

func TestLiveArgsAllowOnlyNetworkProtocols(t *testing.T) {
	t.Parallel()
	args := strings.Join(liveArgs("https://s.example/live.m3u8", "/tmp/x", 4), " ")
	if !strings.Contains(args, "-protocol_whitelist http,https,tcp,tls,crypto -user_agent VCRX") ||
		strings.Index(args, "-protocol_whitelist") > strings.Index(args, "-i ") {
		t.Errorf("the whitelist must come before -i: %s", args)
	}
}
