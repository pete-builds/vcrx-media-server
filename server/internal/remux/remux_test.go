package remux

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func tools(t *testing.T) (string, string) {
	t.Helper()
	ff, err1 := exec.LookPath("ffmpeg")
	fp, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg or ffprobe not on PATH")
	}
	return ff, fp
}

// fixture writes a 30 s MKV with a keyframe every 2 s and AC-3 audio, which
// Chrome cannot decode: the case this package exists for.
func fixture(t *testing.T, ff string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "in.mkv")
	cmd := exec.Command(ff, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24:duration=30",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=30", "-c:v", "libx264", "-g", "48", "-keyint_min", "48", "-sc_threshold", "0",
		"-pix_fmt", "yuv420p", "-c:a", "ac3", "-y", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build fixture (needs libx264 and ac3): %v %s", err, msg)
	}
	return out
}

func probe(t *testing.T, fp, file, entries string) string {
	t.Helper()
	out, err := exec.Command(fp, "-v", "error", "-show_entries", entries, "-of", "csv=p=0", file).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// MPEG-TS from a broadcast or a Blu-ray remux starts at a large timestamp, and
// its demuxer seeks by byte: KeyframeAt must still find the keyframe at or
// before the position, counted from the file's own start, or every seek and
// resume in a .ts restarts at 0.
func TestKeyframeAtTransportStream(t *testing.T) {
	ff, fp := tools(t)
	in := filepath.Join(t.TempDir(), "in.ts")
	cmd := exec.Command(ff, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24:duration=30",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=30", "-c:v", "libx264", "-g", "48", "-keyint_min", "48", "-sc_threshold", "0",
		"-pix_fmt", "yuv420p", "-c:a", "aac", "-output_ts_offset", "600", "-y", in)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build fixture (needs libx264): %v %s", err, msg)
	}
	s := New(ff, fp, 2, nil)
	for _, at := range []int64{10050, 12700, 21000, 29900} {
		k, err := s.KeyframeAt(context.Background(), in, at)
		if err != nil {
			t.Fatal(err)
		}
		// Keyframes every 2 s from the start, give or take the muxer's delay.
		if k > at || at-k > 2300 {
			t.Errorf("KeyframeAt(%d) = %d; want the keyframe at or up to 2 s before it", at, k)
		}
	}
	// The stream ffmpeg makes from that keyframe runs from there to the end:
	// -ss counts from the file's start, the same clock KeyframeAt answers in.
	k, err := s.KeyframeAt(context.Background(), in, 21000)
	if err != nil {
		t.Fatal(err)
	}
	// Its AAC is ADTS, which the MP4 muxer refuses unless it is converted on the way.
	streamLasts(t, s, fp, Options{Path: in, StartMs: k, AudioIndex: 1, AudioCodec: "aac"}, 30-float64(k)/1000)
	// The same filter leaves AAC that is already MP4-style (Matroska) as it is.
	mkv := filepath.Join(t.TempDir(), "in.mkv")
	if msg, err := exec.Command(ff, "-nostdin", "-loglevel", "error", "-i", in, "-c", "copy", "-y", mkv).CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, msg)
	}
	streamLasts(t, s, fp, Options{Path: mkv, AudioIndex: 1, AudioCodec: "aac"}, 30)
}

// streamLasts streams o and checks the output carries video and audio for
// about want seconds: a stream ffmpeg gave up on still has a valid header.
func streamLasts(t *testing.T, s *Service, fp string, o Options, want float64) {
	t.Helper()
	var buf bytes.Buffer
	if err := s.Stream(context.Background(), &buf, o); err != nil {
		t.Fatalf("stream %s: %v", filepath.Base(o.Path), err)
	}
	out := filepath.Join(t.TempDir(), "out.mp4")
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, sel := range []string{"v:0", "a:0"} {
		got, err := exec.Command(fp, "-v", "error", "-select_streams", sel, "-show_entries", "packet=pts_time", "-of", "csv=p=0", out).Output()
		if err != nil {
			t.Fatal(err)
		}
		times := strings.Fields(string(got))
		if len(times) == 0 {
			t.Fatalf("stream %s from %d ms has no %s packets", filepath.Base(o.Path), o.StartMs, sel)
		}
		last, _ := strconv.ParseFloat(times[len(times)-1], 64)
		if math.Abs(last-want) > 0.7 {
			t.Errorf("stream %s from %d ms: %s runs to %.2f s; want about %.2f s", filepath.Base(o.Path), o.StartMs, sel, last, want)
		}
	}
}

func TestKeyframeAndStreamStartTogether(t *testing.T) {
	ff, fp := tools(t)
	in := fixture(t, ff)
	s := New(ff, fp, 2, nil)
	ctx := context.Background()
	// Keyframes are stored every 2 s; KeyframeAt answers from the file's start,
	// which some ffmpeg builds put a few ms before 0 (audio priming).
	start, err := strconv.ParseFloat(probe(t, fp, in, "format=start_time"), 64)
	if err != nil {
		t.Fatal(err)
	}
	shift := int64(math.Round(-start * 1000))
	for want, at := range map[int64]int64{0: 0, 12000: 12700, 10000: 10050, 28000: 29900} {
		if want > 0 {
			want += shift
		}
		k, err := s.KeyframeAt(ctx, in, at)
		if err != nil || k != want {
			t.Errorf("KeyframeAt(%d) = %d, %v; want %d", at, k, err, want)
		}
	}
	var buf bytes.Buffer
	if err := s.Stream(ctx, &buf, Options{Path: in, StartMs: 12000, AudioIndex: 1, TranscodeAudio: true}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.mp4")
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	// Both streams start at 0 and run the 18 s from the keyframe at 12 s, so the
	// client's offset (12 s) plus currentTime is the true position, in sync.
	got := probe(t, fp, out, "stream=codec_type,codec_name,duration")
	lines := strings.Split(got, "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "h264,video") || !strings.HasPrefix(lines[1], "aac,audio") {
		t.Fatalf("streams: %q", got)
	}
	for _, l := range lines {
		f := strings.Split(l, ",")
		d, _ := strconv.ParseFloat(f[len(f)-1], 64)
		if d < 17.5 || d > 18.6 {
			t.Errorf("%s runs %.2f s, want about 18 (stream started at the wrong place)", f[1], d)
		}
	}
}

func TestStreamLimit(t *testing.T) {
	s := New("ffmpeg", "ffprobe", 1, nil)
	s.sem <- struct{}{}
	if err := s.Stream(context.Background(), &bytes.Buffer{}, Options{Path: "x"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("err %v, want ErrBusy", err)
	}
}

func TestArgs(t *testing.T) {
	a := strings.Join(New("ffmpeg", "ffprobe", 1, nil).args(Options{Path: "/m/f.mkv", StartMs: 12000, AudioIndex: 2, HEVC: true}), " ")
	for _, want := range []string{"-noaccurate_seek -ss 12.500 -i /m/f.mkv", "-map 0:v:0 -map 0:2", "-c:v copy -tag:v hvc1", "-c:a copy", "frag_keyframe+empty_moov"} {
		if !strings.Contains(a, want) {
			t.Errorf("args lack %q:\n%s", want, a)
		}
	}
	if a := strings.Join(New("ffmpeg", "ffprobe", 1, nil).args(Options{Path: "f", AudioIndex: -1}), " "); strings.Contains(a, "-ss") || strings.Contains(a, "-c:a") {
		t.Errorf("a stream from the start with no audio: %s", a)
	}
}

func TestFitNeverScalesUp(t *testing.T) {
	for _, c := range []struct{ sw, sh, mw, mh, w, h int }{
		{3840, 2160, 1920, 1080, 1920, 1080}, // down to fit
		{1920, 1080, 3840, 2160, 1920, 1080}, // never up
		{1280, 720, 0, 0, 1280, 720},         // no limit
		{1920, 800, 1280, 720, 1280, 532},    // wide: width binds, shape kept, even
		{1440, 1080, 1280, 720, 960, 720},    // 4:3: height binds
		{721, 481, 0, 0, 720, 480},           // odd source rounded down to even
		{0, 0, 1280, 720, 0, 0},              // unknown size
	} {
		if w, h := Fit(c.sw, c.sh, c.mw, c.mh); w != c.w || h != c.h {
			t.Errorf("Fit(%d, %d, %d, %d) = %dx%d, want %dx%d", c.sw, c.sh, c.mw, c.mh, w, h, c.w, c.h)
		}
	}
}

func TestTranscodeArgs(t *testing.T) {
	s := New("ffmpeg", "ffprobe", 1, nil)
	a := strings.Join(s.args(Options{Path: "/m/f.avi", StartMs: 12700, AudioIndex: 1, HEVC: true,
		Video: &Video{SrcWidth: 1920, SrcHeight: 1080, MaxWidth: 1280, MaxHeight: 720, FPS: 23.976}}), " ")
	for _, want := range []string{
		"-accurate_seek -ss 12.700 -i /m/f.avi", // exactly the position, not a keyframe before it
		"-vf scale=1280:720,format=yuv420p",
		"-c:v libx264 -preset veryfast -crf 21 -profile:v high -pix_fmt yuv420p",
		"-maxrate 5000000 -bufsize 10000000",
		"-force_key_frames expr:gte(t,n_forced*2) -g 48",
		"-copypriorss 0 -c:a copy", // copied audio starts at the position too
	} {
		if !strings.Contains(a, want) {
			t.Errorf("args lack %q:\n%s", want, a)
		}
	}
	for _, bad := range []string{"-noaccurate_seek", "-c:v copy", "hvc1", "zscale"} {
		if strings.Contains(a, bad) {
			t.Errorf("args have %q:\n%s", bad, a)
		}
	}
	// A source smaller than the limit keeps its size; unknown sizes are left to
	// ffmpeg, still never larger than the source; the client's bitrate binds.
	a = strings.Join(s.args(Options{Path: "f", AudioIndex: -1, Video: &Video{SrcWidth: 640, SrcHeight: 360, MaxWidth: 1920, MaxHeight: 1080, MaxBitrate: 2_000_000}}), " ")
	if !strings.Contains(a, "scale=640:360,") || !strings.Contains(a, "-maxrate 1744000") || strings.Contains(a, "-ss") || strings.Contains(a, "copypriorss") {
		t.Errorf("small source: %s", a)
	}
	a = strings.Join(s.args(Options{Path: "f", AudioIndex: -1, Video: &Video{MaxWidth: 1280, MaxHeight: 720, FPS: 120, MaxFPS: 60}}), " ")
	if !strings.Contains(a, `scale=min(iw\,1280):min(ih\,720):force_original_aspect_ratio=decrease`) || !strings.Contains(a, "-fpsmax 60") || !strings.Contains(a, "-g 120") {
		t.Errorf("unknown size, fast source: %s", a)
	}
	// HDR to a display without it: tone mapped when the build can, tagged BT.709.
	s.ToneMap = true
	a = strings.Join(s.args(Options{Path: "f", AudioIndex: -1, Video: &Video{SrcWidth: 3840, SrcHeight: 2160, HDR: "hdr10", ToneMap: true}}), " ")
	if !strings.Contains(a, "zscale=tin=smpte2084") || !strings.Contains(a, "tonemap=tonemap=hable") || !strings.Contains(a, "-color_trc bt709") {
		t.Errorf("tone map: %s", a)
	}
	if a = strings.Join(s.args(Options{Path: "f", AudioIndex: -1, Video: &Video{HDR: "hdr10"}}), " "); strings.Contains(a, "tonemap") {
		t.Errorf("tone mapped for an HDR display: %s", a)
	}
}

func TestTranscodeLimitIsSeparate(t *testing.T) {
	s := New("ffmpeg-not-here", "ffprobe", 4, nil)
	s.SetMaxTranscodes(1)
	s.tsem <- struct{}{}
	if err := s.Stream(context.Background(), &bytes.Buffer{}, Options{Path: "x", Video: &Video{}}); !errors.Is(err, ErrBusy) {
		t.Fatalf("second transcode: %v, want ErrBusy", err)
	}
	// Copied-video streams have their own slots.
	if err := s.Stream(context.Background(), &bytes.Buffer{}, Options{Path: "x"}); errors.Is(err, ErrBusy) {
		t.Fatal("a full transcode limit blocked a copied stream")
	}
	// A seek's new stream waits for the old one's slot.
	go func() { time.Sleep(50 * time.Millisecond); <-s.tsem }()
	if err := s.Stream(context.Background(), &bytes.Buffer{}, Options{Path: "x", Video: &Video{}, Wait: 2 * time.Second}); errors.Is(err, ErrBusy) {
		t.Fatal("waited for a slot and still got ErrBusy")
	}
}

func TestDetect(t *testing.T) {
	ff, fp := tools(t)
	f, err := New(ff, fp, 1, nil).Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command(ff, "-hide_banner", "-encoders").Output()
	if want := strings.Contains(string(out), " libx264 "); f.X264 != want {
		t.Fatalf("X264 %v, ffmpeg -encoders says %v", f.X264, want)
	}
	got := listed(" V....D libx264              libx264 H.264\n .S tonemap           V->V       Conversion\n ------\n")
	if !got["libx264"] || !got["tonemap"] || got["H.264"] {
		t.Fatalf("listed: %v", got)
	}
}

// needX264 skips unless ffmpeg can encode H.264.
func needX264(t *testing.T, s *Service) {
	t.Helper()
	f, err := s.Detect(context.Background())
	if err != nil || !f.X264 {
		t.Skipf("ffmpeg has no libx264: %v", err)
	}
}

func TestTranscodeStartsExactly(t *testing.T) {
	ff, fp := tools(t)
	s := New(ff, fp, 2, nil)
	needX264(t, s)
	// MPEG-4 Part 2 video, which no browser decodes, with a keyframe only
	// every 10 s: a stream that started on a keyframe would start 2.7 s early.
	in := filepath.Join(t.TempDir(), "in.mkv")
	if msg, err := exec.Command(ff, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24:duration=30",
		"-f", "lavfi", "-i", "sine=duration=30", "-c:v", "mpeg4", "-g", "240", "-c:a", "aac", "-y", in).CombinedOutput(); err != nil {
		t.Skipf("cannot build fixture: %v %s", err, msg)
	}
	for _, transcodeAudio := range []bool{false, true} {
		var buf bytes.Buffer
		err := s.Stream(context.Background(), &buf, Options{Path: in, StartMs: 12700, AudioIndex: 1, TranscodeAudio: transcodeAudio,
			Video: &Video{SrcWidth: 640, SrcHeight: 360, MaxWidth: 320, MaxHeight: 240, FPS: 24}})
		if err != nil {
			t.Fatal(err)
		}
		out := filepath.Join(t.TempDir(), "out.mp4")
		if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		got := probe(t, fp, out, "stream=codec_name,width,height,duration")
		lines := strings.Split(got, "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "h264,320,180,") || !strings.HasPrefix(lines[1], "aac,") {
			t.Fatalf("audio converted %v: streams %q", transcodeAudio, got)
		}
		// Both run the 17.3 s from 12.7 s to the end: the stream starts where
		// it was asked to, so the client's offset plus currentTime is right.
		for _, l := range lines {
			f := strings.Split(l, ",")
			d, _ := strconv.ParseFloat(f[len(f)-1], 64)
			if d < 17.0 || d > 17.6 {
				t.Errorf("audio converted %v: %s runs %.2f s, want about 17.3", transcodeAudio, f[0], d)
			}
		}
	}
}

func TestToneMapHDR(t *testing.T) {
	ff, fp := tools(t)
	s := New(ff, fp, 2, nil)
	needX264(t, s)
	if !s.ToneMap {
		// CI installs Ubuntu's ffmpeg, built with zscale like the image's
		// Debian one, so there a missing filter is a failure, not a skip.
		if os.Getenv("CI") != "" {
			t.Fatal("ffmpeg in CI has no zscale and tonemap filters")
		}
		t.Skip("ffmpeg has no zscale filter: HDR is sent without tone mapping")
	}
	in := filepath.Join(t.TempDir(), "hdr.mkv")
	if msg, err := exec.Command(ff, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=24:duration=4",
		"-c:v", "libx265", "-pix_fmt", "yuv420p10le", "-color_primaries", "bt2020", "-color_trc", "smpte2084", "-colorspace", "bt2020nc",
		"-y", in).CombinedOutput(); err != nil {
		t.Skipf("cannot build an HDR10 fixture (needs libx265): %v %s", err, msg)
	}
	var buf bytes.Buffer
	if err := s.Stream(context.Background(), &buf, Options{Path: in, AudioIndex: -1,
		Video: &Video{SrcWidth: 640, SrcHeight: 360, HDR: "hdr10", ToneMap: true}}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out.mp4")
	if err := os.WriteFile(out, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := probe(t, fp, out, "stream=codec_name,pix_fmt,color_transfer,color_primaries"); got != "h264,yuv420p,bt709,bt709" {
		t.Fatalf("tone mapped stream: %q", got)
	}
}
