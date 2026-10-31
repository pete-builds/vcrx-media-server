package remux

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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

func TestKeyframeAndStreamStartTogether(t *testing.T) {
	ff, fp := tools(t)
	in := fixture(t, ff)
	s := New(ff, fp, 2, nil)
	ctx := context.Background()
	for want, at := range map[int64]int64{0: 0, 12000: 12700, 10000: 10050, 28000: 29900} {
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
