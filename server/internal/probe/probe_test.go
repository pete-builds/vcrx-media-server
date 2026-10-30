package probe

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fixtures are real ffprobe 8.1 output for tiny files made with ffmpeg's
// testsrc2, except dovi_p8_pgs, hdr10plus and audio_only, which were edited by
// hand from those outputs: ffmpeg cannot synthesize Dolby Vision RPUs, PGS
// bitmaps or HDR10+ side data.
func load(t *testing.T, name string) *Result {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := Parse(data, "/media/x/"+name)
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return r
}

func TestParseHDR10Scope4K(t *testing.T) {
	r := load(t, "hevc_hdr10_scope_4k.mkv")
	check(t, "container", r.Container, "mkv")
	check(t, "codec", r.VideoCodec, "hevc")
	check(t, "profile", r.VideoProfile, "main 10")
	check(t, "level", r.VideoLevel, 150)
	check(t, "bit depth", r.BitDepth, 10)
	check(t, "size", [2]int{r.Width, r.Height}, [2]int{3840, 1600})
	check(t, "resolution", r.Resolution, "4K")
	check(t, "hdr", r.HDRType, HDR10)
	check(t, "fps", r.FrameRate, 23.976)
	check(t, "duration", r.DurationMs, int64(1000))
	check(t, "bitrate", r.Bitrate, int64(4933264))
	if len(r.Streams) != 6 {
		t.Fatalf("streams = %d, want 6", len(r.Streams))
	}
	a0, a1 := r.Streams[1], r.Streams[2]
	check(t, "a0", [4]any{a0.Codec, a0.Language, a0.Channels, a0.Default}, [4]any{"aac", "eng", 6, true})
	check(t, "a0 layout", a0.ChannelLayout, "5.1")
	check(t, "a0 rate", a0.SampleRate, 48000)
	check(t, "a1", [4]any{a1.Codec, a1.Language, a1.Title, a1.Default}, [4]any{"ac3", "fre", "Commentary", false})
	check(t, "a1 bitrate", a1.Bitrate, int64(192000))
	s0, s1, s2 := r.Streams[3], r.Streams[4], r.Streams[5]
	check(t, "s0", [4]bool{s0.Default, s0.Forced, s0.HearingImpaired, s0.ImageBased}, [4]bool{false, false, false, false})
	check(t, "s1 forced", [2]any{s1.Forced, s1.Language}, [2]any{true, "fre"})
	check(t, "s2 sdh", [2]any{s2.HearingImpaired, s2.Title}, [2]any{true, "SDH"})
	check(t, "s0 codec", s0.Codec, "subrip")
}

func TestParseDolbyVisionAndImageSubtitles(t *testing.T) {
	r := load(t, "dovi_p8_pgs.mkv")
	check(t, "hdr", r.HDRType, DolbyVision)
	check(t, "dv profile", r.DVProfile, 8)
	check(t, "dv compat", r.DVCompatID, 1)
	// Cover art and the font attachment are not streams anyone plays.
	if len(r.Streams) != 6 {
		t.Fatalf("streams = %d, want 6 (cover art and attachment dropped)", len(r.Streams))
	}
	for _, s := range r.Streams {
		if s.Codec == "mjpeg" || s.Codec == "ttf" {
			t.Fatalf("kept %s", s.Codec)
		}
	}
	pgs, forced, vob := r.Streams[3], r.Streams[4], r.Streams[5]
	check(t, "pgs", [3]any{pgs.Codec, pgs.ImageBased, pgs.Default}, [3]any{"hdmv_pgs_subtitle", true, true})
	check(t, "pgs forced", [2]any{forced.Forced, forced.Language}, [2]any{true, "ger"})
	check(t, "vobsub", [2]any{vob.ImageBased, vob.Language}, [2]any{true, ""})
}

func TestParseHDR10PlusAndDurationFallback(t *testing.T) {
	r := load(t, "hdr10plus.mkv")
	check(t, "hdr", r.HDRType, HDR10Plus)
	check(t, "duration from stream tag", r.DurationMs, int64(6130512))
	check(t, "bitrate from size", r.Bitrate, int64(12000000000*8*1000/6130512))
}

func TestParseOtherContainers(t *testing.T) {
	cases := []struct {
		name, container, codec, profile, res, hdr string
		depth, w, h                               int
		fps                                       float64
	}{
		{"hevc_hlg_1080.mkv", "mkv", "hevc", "main 10", "1080p", HLG, 10, 1920, 1080, 50},
		{"h264_scope_1080.mp4", "mp4", "h264", "constrained baseline", "1080p", "", 8, 1920, 800, 24},
		{"h264_scope_1080.mov", "mov", "h264", "constrained baseline", "1080p", "", 8, 1920, 800, 24},
		{"vp9_opus_720.webm", "webm", "vp9", "profile 0", "720p", "", 8, 1280, 720, 30},
		{"mpeg2_ac3_480.ts", "ts", "mpeg2video", "main", "480p", "", 8, 720, 480, 29.97},
		{"mpeg4_ac3.avi", "avi", "mpeg4", "simple profile", "SD", "", 8, 640, 272, 25},
		{"h264_flac_1080.mkv", "mkv", "h264", "constrained baseline", "1080p", "", 8, 1920, 1080, 24},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := load(t, c.name)
			check(t, "container", r.Container, c.container)
			check(t, "codec", r.VideoCodec, c.codec)
			check(t, "profile", r.VideoProfile, c.profile)
			check(t, "resolution", r.Resolution, c.res)
			check(t, "hdr", r.HDRType, c.hdr)
			check(t, "depth", r.BitDepth, c.depth)
			check(t, "size", [2]int{r.Width, r.Height}, [2]int{c.w, c.h})
			check(t, "fps", r.FrameRate, c.fps)
		})
	}
	// VP9 reports level -99, meaning none.
	if r := load(t, "vp9_opus_720.webm"); r.VideoLevel != 0 {
		t.Errorf("vp9 level = %d, want 0", r.VideoLevel)
	}
}

func TestParseNoVideo(t *testing.T) {
	data, err := os.ReadFile("testdata/audio_only.m4v.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(data, "/m/a.m4v"); !errors.Is(err, ErrNoVideo) {
		t.Fatalf("err = %v, want ErrNoVideo", err)
	}
	if _, err := Parse([]byte("not json"), "/m/a.mkv"); err == nil {
		t.Fatal("garbage parsed")
	}
}

func TestContainer(t *testing.T) {
	cases := []struct{ format, path, want string }{
		{"matroska,webm", "/a/x.mkv", "mkv"},
		{"matroska,webm", "/a/x.WEBM", "webm"},
		{"matroska,webm", "/a/x.mk3d", "mkv"},
		{"mov,mp4,m4a,3gp,3g2,mj2", "/a/x.mp4", "mp4"},
		{"mov,mp4,m4a,3gp,3g2,mj2", "/a/x.m4v", "mp4"},
		{"mov,mp4,m4a,3gp,3g2,mj2", "/a/x.MOV", "mov"},
		{"mpegts", "/a/x.ts", "ts"},
		{"mpegts", "/a/x.m2ts", "m2ts"},
		{"mpegts", "/a/x.mts", "m2ts"},
		{"avi", "/a/x.avi", "avi"},
		{"asf", "/a/x.wmv", "wmv"},
		{"flv", "/a/x.flv", "flv"},
		{"mpeg", "/a/x.mpg", "mpeg"},
		{"ogg", "/a/x.ogv", "ogg"},
		{"rm", "/a/x.rm", "rm"},
		{"", "/a/x.mkv", "mkv"},
	}
	for _, c := range cases {
		if got := Container(c.format, c.path); got != c.want {
			t.Errorf("Container(%q, %q) = %q, want %q", c.format, c.path, got, c.want)
		}
	}
}

func TestBucket(t *testing.T) {
	cases := []struct {
		w, h int
		want string
	}{
		{7680, 4320, "8K"}, {6144, 2560, "8K"},
		{3840, 2160, "4K"}, {3840, 1600, "4K"}, {3996, 2160, "4K"}, {3200, 1350, "4K"},
		{1920, 1080, "1080p"}, {1920, 800, "1080p"}, {1800, 1012, "1080p"}, {1440, 1080, "1080p"},
		{1280, 720, "720p"}, {1280, 536, "720p"}, {960, 720, "720p"},
		{720, 576, "576p"}, {1024, 576, "576p"},
		{720, 480, "480p"}, {854, 480, "480p"}, {640, 480, "480p"},
		{640, 272, "SD"}, {320, 240, "SD"},
		{0, 0, ""},
	}
	for _, c := range cases {
		if got := Bucket(c.w, c.h); got != c.want {
			t.Errorf("Bucket(%d, %d) = %q, want %q", c.w, c.h, got, c.want)
		}
	}
}

func TestPixFmtDepth(t *testing.T) {
	cases := map[string]int{
		"yuv420p": 8, "yuvj420p": 8, "nv12": 8, "yuv420p10le": 10, "yuv420p10be": 10, "p010le": 10,
		"yuv422p12le": 12, "gray10le": 10, "yuv444p16le": 16, "gbrp10le": 10, "rgb24": 8, "": 0,
	}
	for pf, want := range cases {
		if got := pixFmtDepth(pf); got != want {
			t.Errorf("pixFmtDepth(%q) = %d, want %d", pf, got, want)
		}
	}
}

// fakeFFprobe writes a shell script standing in for ffprobe.
func fakeFFprobe(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ffprobe")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFFprobeTimeoutAndErrors(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	slow := &FFprobe{Path: fakeFFprobe(t, "exec sleep 10"), Timeout: 200 * time.Millisecond}
	start := time.Now()
	_, err := slow.Probe(context.Background(), "/media/a.mkv")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
	if time.Since(start) > 7*time.Second {
		t.Fatalf("timeout took %s", time.Since(start))
	}

	failing := &FFprobe{Path: fakeFFprobe(t, `echo "file:/media/a.mkv: Invalid data found when processing input" >&2; exit 1`)}
	_, err = failing.Probe(context.Background(), "/media/a.mkv")
	if err == nil || !strings.Contains(err.Error(), "Invalid data") {
		t.Fatalf("err = %v, want stderr message", err)
	}

	argv := filepath.Join(t.TempDir(), "argv")
	recorder := &FFprobe{Path: fakeFFprobe(t, `printf '%s\n' "$@" > `+argv+`; cat `+mustAbs(t, "testdata/h264_scope_1080.mp4.json"))}
	r, err := recorder.Probe(context.Background(), "/media/-rf Movie.mp4")
	if err != nil {
		t.Fatal(err)
	}
	check(t, "container", r.Container, "mp4")
	got, _ := os.ReadFile(argv)
	if !strings.HasSuffix(strings.TrimSpace(string(got)), "file:/media/-rf Movie.mp4") {
		t.Fatalf("argv = %q, want the path last with a file: prefix", got)
	}

	if _, err := (&FFprobe{}).Probe(context.Background(), "relative.mkv"); err == nil {
		t.Fatal("relative path accepted")
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// TestFFprobeReal runs the real binary on a file made by ffmpeg, when both exist.
func TestFFprobeReal(t *testing.T) {
	ffmpeg, err1 := exec.LookPath("ffmpeg")
	_, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg or ffprobe not on PATH")
	}
	p := filepath.Join(t.TempDir(), "clip.mkv")
	out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i",
		"testsrc2=size=1280x536:rate=24:duration=0.2,setparams=color_primaries=bt2020:color_trc=smpte2084:colorspace=bt2020nc",
		"-f", "lavfi", "-i", "sine=duration=0.2", "-c:v", "mpeg4", "-c:a", "ac3", "-metadata:s:a:0", "language=eng", p).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
	ff := &FFprobe{}
	if v, err := ff.Version(context.Background()); err != nil || !strings.Contains(v, "ffprobe") {
		t.Fatalf("Version = %q, %v", v, err)
	}
	r, err := ff.Probe(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "container", r.Container, "mkv")
	check(t, "resolution", r.Resolution, "720p")
	check(t, "hdr", r.HDRType, HDR10)
	if len(r.Streams) != 2 || r.Streams[1].Language != "eng" {
		t.Fatalf("streams = %+v", r.Streams)
	}
	if _, err := ff.Probe(context.Background(), filepath.Join(t.TempDir(), "missing.mkv")); err == nil {
		t.Fatal("missing file probed")
	}
}

func check[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}
