package remux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const (
	sessA = "0123456789abcdef0123456789abcdef"
	sessB = "fedcba9876543210fedcba9876543210"
)

// sleeper is an "ffmpeg" that runs until killed and writes nothing, so a
// stream holds its slot for as long as a test needs.
func sleeper(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs /bin/sh")
	}
	p := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexec sleep 60\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func newHLS(t *testing.T, ffmpeg string, maxStreams int) (*HLS, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "hls")
	h, err := NewHLS(New(ffmpeg, "ffprobe", maxStreams, nil), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h, root
}

func TestHLSArgs(t *testing.T) {
	s := New("ffmpeg", "ffprobe", 1, nil)
	a := strings.Join(s.hlsArgs(Options{Path: "/m/f.mkv", StartMs: 12000, AudioIndex: 1, TranscodeAudio: true, HEVC: true}, "/c/hls/s/r"), " ")
	for _, want := range []string{
		"-noaccurate_seek -ss 12.500 -i /m/f.mkv", // the same seek as the fMP4 stream
		"-c:v copy -tag:v hvc1",                   // Safari needs hvc1 for HEVC
		"-c:a aac -ac 2 -b:a 192k",
		"-f hls -hls_time 4 -hls_playlist_type event -hls_segment_type fmp4 -hls_fmp4_init_filename init.mp4",
		"-hls_segment_filename /c/hls/s/r/seg%05d.m4s -hls_flags temp_file+independent_segments -start_number 0 /c/hls/s/r/index.m3u8",
	} {
		if !strings.Contains(a, filepath.FromSlash(want)) {
			t.Errorf("missing %q in\n%s", want, a)
		}
	}
	if strings.Contains(a, "pipe:1") || strings.Contains(a, "-movflags") {
		t.Errorf("HLS args carry the fMP4 output: %s", a)
	}
	// The fMP4 stream is unchanged by the split.
	if m := strings.Join(s.args(Options{Path: "/m/f.mkv", AudioIndex: -1}), " "); !strings.HasSuffix(m,
		"-f mp4 -movflags frag_keyframe+empty_moov+default_base_moof -frag_duration 2000000 pipe:1") {
		t.Errorf("fMP4 args: %s", m)
	}
}

// Only names the server writes are served, so no request reaches another file.
func TestHLSSegmentNames(t *testing.T) {
	h, root := newHLS(t, sleeper(t), 1)
	run, err := h.Start(context.Background(), sessA, Options{Path: "x", AudioIndex: -1})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"init.mp4", "seg00000.m4s", "seg00123.m4s", "seg123456789.m4s"} {
		p, err := h.Segment(sessA, run, name)
		if err != nil || p != filepath.Join(root, sessA, run, name) {
			t.Errorf("%s: %q %v", name, p, err)
		}
	}
	for _, name := range []string{"../secret.txt", "..", "../../" + sessB + "/x/init.mp4", "/etc/passwd", "index.m3u8",
		"index.m3u8.tmp", "seg00000.m4s.tmp", "seg1.m4s", "init.mp4/../../x", "", "seg00000.m4s\x00"} {
		if p, err := h.Segment(sessA, run, name); !errors.Is(err, ErrBadName) {
			t.Errorf("%q served as %q (%v)", name, p, err)
		}
	}
	// A stream belongs to its session and run; nothing else finds it.
	for _, c := range [][2]string{{sessB, run}, {sessA, "0000000000000000"}, {sessA, ".."}, {"../" + sessA, run}} {
		if _, err := h.Segment(c[0], c[1], "init.mp4"); !errors.Is(err, ErrNoStream) {
			t.Errorf("session %q run %q: %v", c[0], c[1], err)
		}
	}
	if _, err := h.Start(context.Background(), "../escape", Options{Path: "x", AudioIndex: -1}); !errors.Is(err, ErrBadName) {
		t.Errorf("a session id that is a path: %v", err)
	}
}

// HLS streams take the same slots as fMP4 streams: a seek replaces the
// session's stream without needing a second slot, and another session waits
// for a free one.
func TestHLSSlotLimit(t *testing.T) {
	h, _ := newHLS(t, sleeper(t), 1)
	ctx := context.Background()
	copyOpts := Options{Path: "x", AudioIndex: -1}
	if _, err := h.Start(ctx, sessA, copyOpts); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Start(ctx, sessB, copyOpts); !errors.Is(err, ErrBusy) {
		t.Fatalf("second session with one slot: %v", err)
	}
	if _, err := h.Start(ctx, sessA, copyOpts); err != nil {
		t.Fatalf("seek within the session: %v", err)
	}
	// The fMP4 stream is held off by the same slot.
	if err := h.svc.Stream(ctx, &strings.Builder{}, copyOpts); !errors.Is(err, ErrBusy) {
		t.Fatalf("fMP4 stream while HLS holds the slot: %v", err)
	}
	// Video transcodes have their own limit.
	h.svc.SetMaxTranscodes(1)
	tx := Options{Path: "x", AudioIndex: -1, Video: &Video{}}
	if _, err := h.Start(ctx, sessB, tx); err != nil {
		t.Fatalf("transcode beside a copy stream: %v", err)
	}
	if _, err := h.Start(ctx, "11111111111111111111111111111111", tx); !errors.Is(err, ErrBusy) {
		t.Fatalf("second transcode with one transcode slot: %v", err)
	}
	h.Stop(sessA)
	if _, err := h.Start(ctx, "22222222222222222222222222222222", copyOpts); err != nil {
		t.Fatalf("after the session ended: %v", err)
	}
}

func TestHLSCleanup(t *testing.T) {
	ff := sleeper(t)
	root := filepath.Join(t.TempDir(), "hls")
	// Leftovers from a previous process go at startup; other files stay.
	for _, d := range []string{filepath.Join(root, sessA, "0123456789abcdef"), filepath.Join(root, "keep-me")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, sessA, "0123456789abcdef", "seg00000.m4s"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := NewHLS(New(ff, "ffprobe", 4, nil), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, sessA)); !os.IsNotExist(err) {
		t.Errorf("leftover session directory survived startup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "keep-me")); err != nil {
		t.Errorf("startup removed a directory it did not make: %v", err)
	}

	ctx := context.Background()
	opts := Options{Path: "x", AudioIndex: -1}
	run1, _ := h.Start(ctx, sessA, opts)
	run2, err := h.Start(ctx, sessA, opts) // a seek
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, sessA, run1)); !os.IsNotExist(err) {
		t.Errorf("a replaced stream's files survived: %v", err)
	}
	if _, err := h.Segment(sessA, run1, "init.mp4"); !errors.Is(err, ErrNoStream) {
		t.Errorf("the replaced stream still answers: %v", err)
	}
	// The session ends.
	h.mu.Lock()
	j := h.jobs[sessA]
	h.mu.Unlock()
	if !h.Stop(sessA) {
		t.Fatal("Stop found no stream")
	}
	select {
	case <-j.done:
	default:
		t.Error("ffmpeg still running after Stop")
	}
	if _, err := os.Stat(filepath.Join(root, sessA)); !os.IsNotExist(err) {
		t.Errorf("files survived the session's end: %v", err)
	}
	if _, err := h.Segment(sessA, run2, "init.mp4"); !errors.Is(err, ErrNoStream) {
		t.Errorf("the stream answers after Stop: %v", err)
	}

	// A stream nobody asks anything of is stopped.
	if _, err := h.Start(ctx, sessB, opts); err != nil {
		t.Fatal(err)
	}
	h.Idle = 50 * time.Millisecond
	time.Sleep(100 * time.Millisecond)
	h.tick()
	if _, err := os.Stat(filepath.Join(root, sessB)); !os.IsNotExist(err) {
		t.Errorf("an idle stream's files survived: %v", err)
	}

	// Shutdown stops everything and takes no new streams.
	h.Idle = time.Hour
	if _, err := h.Start(ctx, sessA, opts); err != nil {
		t.Fatal(err)
	}
	h.Close()
	if _, err := os.Stat(filepath.Join(root, sessA)); !os.IsNotExist(err) {
		t.Errorf("files survived shutdown: %v", err)
	}
	if _, err := h.Start(ctx, sessB, opts); err == nil {
		t.Error("started a stream after Close")
	}
}

// A real ffmpeg, paused when it is far enough ahead of the player, resumed
// as the player catches up, with segments far behind the player deleted.
func TestHLSBoundsDisk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pausing ffmpeg needs SIGSTOP")
	}
	ff, _ := tools(t)
	in := filepath.Join(t.TempDir(), "in.mkv")
	// 120 s with a keyframe every second: thirty 4 s segments.
	if msg, err := exec.Command(ff, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=24:duration=120",
		"-f", "lavfi", "-i", "sine=duration=120", "-c:v", "libx264", "-g", "24", "-sc_threshold", "0", "-pix_fmt", "yuv420p",
		"-c:a", "ac3", "-y", in).CombinedOutput(); err != nil {
		t.Skipf("fixture needs libx264 and ac3: %v %s", err, msg)
	}
	h, root := newHLS(t, ff, 1)
	h.Ahead, h.Behind = 4, 2
	// A transcode, slower than a copy, so the pause lands well before ffmpeg
	// could finish the file.
	run, err := h.Start(context.Background(), sessA, Options{Path: in, AudioIndex: 1, TranscodeAudio: true,
		Video: &Video{SrcWidth: 160, SrcHeight: 90}})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, sessA, run)
	segs := func() []string {
		m, _ := filepath.Glob(filepath.Join(dir, "seg*.m4s"))
		return m
	}
	paused := func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.jobs[sessA].paused
	}
	wait := func(what string, cond func() bool) {
		t.Helper()
		for i := 0; i < 300; i++ {
			h.tick()
			if cond() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s; segments %d, paused %v", what, len(segs()), paused())
	}
	wait("ffmpeg to pause ahead of the player", paused)
	// Let a paused ffmpeg prove it stays put.
	n := len(segs())
	time.Sleep(500 * time.Millisecond)
	h.tick()
	if got := len(segs()); got != n || got > h.Ahead+2 || !paused() {
		t.Fatalf("paused at %d segments, now %d (ahead limit %d), paused %v", n, got, h.Ahead, paused())
	}
	// The player catches up: ffmpeg resumes and the segments it left behind go.
	if _, err := h.Segment(sessA, run, "seg00005.m4s"); err != nil {
		t.Fatal(err)
	}
	wait("ffmpeg to resume", func() bool { return !paused() || len(segs()) > n })
	wait("segments behind the player to go", func() bool {
		_, err := os.Stat(filepath.Join(dir, "seg00002.m4s"))
		return os.IsNotExist(err)
	})
	if _, err := os.Stat(filepath.Join(dir, "seg00003.m4s")); err != nil {
		t.Errorf("deleted a segment within the behind window: %v", err)
	}
	// Over the byte cap, ffmpeg pauses even when it is not far ahead, and
	// only two segments are kept behind the player.
	h.Ahead, h.MaxBytes = 1000, 1
	if _, err := h.Segment(sessA, run, "seg00020.m4s"); err != nil {
		t.Fatal(err)
	}
	wait("ffmpeg to resume for the player", func() bool { return !paused() })
	wait("ffmpeg to pause at the byte cap", paused)
	if _, err := os.Stat(filepath.Join(dir, "seg00017.m4s")); !os.IsNotExist(err) {
		t.Errorf("over the byte cap, kept a segment three behind the player: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "seg00018.m4s")); err != nil {
		t.Errorf("over the byte cap, deleted a segment two behind the player: %v", err)
	}
}

// A growing playlist goes to a player only with three segments in it: Chrome
// refuses one with fewer (DEMUXER_ERROR_COULD_NOT_PARSE), and the HLS spec
// starts a live playlist three target durations from its end.
func TestHLSPlaylistReady(t *testing.T) {
	head := "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:4\n#EXT-X-PLAYLIST-TYPE:EVENT\n#EXT-X-MAP:URI=\"init.mp4\"\n"
	seg := func(n int) string { return strings.Repeat("#EXTINF:4.000000,\nseg00000.m4s\n", n) }
	for _, c := range []struct {
		pl   string
		want bool
	}{
		{head, false},
		{head + seg(1), false},
		{head + seg(2), false},
		{head + seg(3), true},
		{head + seg(1) + "#EXT-X-ENDLIST\n", true}, // a file shorter than three segments
		{head + "#EXT-X-ENDLIST\n", false},
	} {
		if got := playable(c.pl); got != c.want {
			t.Errorf("playable(%d segments, ended %v) = %v", strings.Count(c.pl, "#EXTINF"), strings.Contains(c.pl, "ENDLIST"), got)
		}
	}
	if got := string(withStart([]byte(head + seg(3)))); !strings.HasPrefix(got, "#EXTM3U\n#EXT-X-START:TIME-OFFSET=0\n#EXT-X-VERSION:7\n") {
		t.Errorf("withStart:\n%s", got)
	}
}
