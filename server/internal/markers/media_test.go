package markers

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Synthetic episodes built with ffmpeg: every part is generated audio, so the
// repo holds no real programme. The theme and the credits are fixed
// expressions that sound the same in every episode; "unique" parts are noise
// with a seed of their own, so they never repeat unless a test reuses a seed
// on purpose (a recap). Each episode is encoded to AAC on its own, so shared
// parts are not bit-identical, as in real files.

type part struct {
	kind string // theme, credits, unique, silence, tone
	secs float64
	seed int
}

const (
	themeExpr = "0.25*sin(2*PI*t*220*pow(2\\,mod(floor(t*3)*5\\,12)/12))*exp(-3*mod(t\\,1/3))" +
		"+0.15*sin(2*PI*(400*mod(t\\,2)+400*mod(t\\,2)*mod(t\\,2)))" +
		"+0.1*sin(2*PI*t*110*pow(2\\,mod(floor(t/2)*7\\,12)/12))*(0.6+0.4*sin(2*PI*5*t))"
	creditsExpr = "(0.2*sin(2*PI*t*660*pow(2\\,-mod(floor(t*4)*7+floor(t/1.1)*5\\,12)/12))+0.1*sin(2*PI*t*1320*pow(2\\,-mod(floor(t*4)*7+floor(t/1.1)*5\\,12)/12)))*exp(-5*mod(t\\,0.25))" +
		"+0.12*sin(2*PI*(1800*mod(t\\,1.7)-300*mod(t\\,1.7)*mod(t\\,1.7)))"
)

// rendered holds the theme and credits rendered once to WAV: evaluating the
// expressions is the slow part of making an episode. Tests run in parallel,
// so each key renders under its own Once and nobody reads a half-written file.
var (
	rendered   sync.Map // kind and length -> *renderJob
	renderDir  string
	renderOnce sync.Once
)

type renderJob struct {
	once sync.Once
	path string
	err  error
	out  []byte
}

func TestMain(m *testing.M) {
	code := m.Run()
	if renderDir != "" {
		_ = os.RemoveAll(renderDir)
	}
	os.Exit(code)
}

func render(t *testing.T, kind, expr string, secs float64) string {
	t.Helper()
	key := fmt.Sprintf("%s-%g", kind, secs)
	v, _ := rendered.LoadOrStore(key, &renderJob{})
	j := v.(*renderJob)
	j.once.Do(func() {
		renderOnce.Do(func() { renderDir, _ = os.MkdirTemp("", "vcrx-markers-test-") })
		j.path = filepath.Join(renderDir, key+".wav")
		cmd := exec.CommandContext(context.Background(), "ffmpeg", "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i",
			fmt.Sprintf("aevalsrc=exprs='%s':s=44100:d=%g", expr, secs), "-y", j.path)
		j.out, j.err = cmd.CombinedOutput()
	})
	if j.err != nil {
		t.Fatalf("render %s: %v\n%s", key, j.err, j.out)
	}
	return j.path
}

// input is the part's ffmpeg input arguments.
func (p part) input(t *testing.T) []string {
	switch p.kind {
	case "theme":
		return []string{"-i", render(t, p.kind, themeExpr, p.secs)}
	case "credits":
		return []string{"-i", render(t, p.kind, creditsExpr, p.secs)}
	}
	return []string{"-f", "lavfi", "-i", p.source()}
}

func (p part) source() string {
	switch p.kind {
	case "silence":
		return fmt.Sprintf("anullsrc=r=44100:cl=mono:d=%g", p.secs)
	case "tone":
		return fmt.Sprintf("sine=frequency=440:sample_rate=44100:duration=%g", p.secs)
	default:
		// Pink noise, different for every seed.
		return fmt.Sprintf("anoisesrc=color=pink:sample_rate=44100:amplitude=0.4:seed=%d:duration=%g", p.seed, p.secs)
	}
}

func needFFmpeg(t *testing.T) Decoder {
	t.Helper()
	ff, err1 := exec.LookPath("ffmpeg")
	fp, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg or ffprobe not on PATH")
	}
	return Decoder{FFmpeg: ff, FFprobe: fp}
}

// makeEpisode writes an MP4 of the parts in order, with a small test-pattern
// picture, and returns where each part starts in milliseconds.
func makeEpisode(t *testing.T, path string, parts ...part) []int64 {
	t.Helper()
	var args []string
	var graph strings.Builder
	var total float64
	starts := make([]int64, len(parts))
	for i, p := range parts {
		starts[i] = int64(total * 1000)
		total += p.secs
		args = append(args, p.input(t)...)
		fmt.Fprintf(&graph, "[%d:a]aformat=sample_rates=44100:channel_layouts=stereo[a%d];", i, i)
	}
	for i := range parts {
		fmt.Fprintf(&graph, "[a%d]", i)
	}
	fmt.Fprintf(&graph, "concat=n=%d:v=0:a=1[a]", len(parts))
	args = append(args, "-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=160x90:rate=10:duration=%g", total),
		"-filter_complex", graph.String(), "-map", fmt.Sprintf("%d:v", len(parts)), "-map", "[a]",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "128k", "-shortest", "-y", path)
	cmd := exec.CommandContext(context.Background(), "ffmpeg", append([]string{"-nostdin", "-loglevel", "error"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %s: %v\n%s", filepath.Base(path), err, out)
	}
	return starts
}
