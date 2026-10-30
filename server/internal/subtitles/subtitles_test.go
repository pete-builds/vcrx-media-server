package subtitles

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const srt = "1\n00:00:00,500 --> 00:00:01,500\nFirst line here\n\n2\n00:00:01,600 --> 00:00:02,500\nSecond line\n"

func needFFmpeg(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	return bin
}

// mkvWithSubs writes a three-second MKV holding two SubRip tracks, at stream
// indexes 1 and 2 after the video.
func mkvWithSubs(t *testing.T, bin, dir string) string {
	t.Helper()
	a := filepath.Join(dir, "a.srt")
	b := filepath.Join(dir, "b.srt")
	if err := os.WriteFile(a, []byte(srt), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte(strings.ReplaceAll(srt, "First line here", "Zweite Sprache")), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "film.mkv")
	cmd := exec.Command(bin, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=64x64:d=3",
		"-i", a, "-i", b, "-map", "0:v", "-map", "1", "-map", "2", "-c:v", "mpeg4", "-c:s", "srt", "-y", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make fixture: %v: %s", err, msg)
	}
	return out
}

func TestEmbeddedExtractsEveryTrackInOnePass(t *testing.T) {
	bin := needFFmpeg(t)
	dir := t.TempDir()
	src := mkvWithSubs(t, bin, dir)
	s := New(filepath.Join(dir, "cache"), bin, nil)
	all := []Track{{StreamID: 11, Index: 1}, {StreamID: 12, Index: 2}}

	_, ready, err := s.Embedded(src, all[0], all)
	if err != nil || ready {
		t.Fatalf("first request: ready=%v err=%v, want a pass started", ready, err)
	}
	s.Wait(src)

	p1, ready, err := s.Embedded(src, all[0], all)
	if err != nil || !ready {
		t.Fatalf("after the pass: ready=%v err=%v", ready, err)
	}
	body, _ := os.ReadFile(p1)
	if !strings.HasPrefix(string(body), "WEBVTT") || !strings.Contains(string(body), "First line here") {
		t.Fatalf("track 1 is not WebVTT with the cue:\n%s", body)
	}
	// The second language came out of the same pass without being asked for.
	p2, ready, err := s.Embedded(src, all[1], all)
	if err != nil || !ready {
		t.Fatalf("track 2 not ready from the shared pass: ready=%v err=%v", ready, err)
	}
	body, _ = os.ReadFile(p2)
	if !strings.Contains(string(body), "Zweite Sprache") {
		t.Fatalf("track 2 holds the wrong cues:\n%s", body)
	}
}

func TestEmbeddedRemembersFailure(t *testing.T) {
	bin := needFFmpeg(t)
	dir := t.TempDir()
	src := mkvWithSubs(t, bin, dir)
	s := New(filepath.Join(dir, "cache"), bin, nil)
	bad := Track{StreamID: 99, Index: 7} // no such stream
	if _, ready, err := s.Embedded(src, bad, []Track{bad}); err != nil || ready {
		t.Fatalf("first request: ready=%v err=%v", ready, err)
	}
	s.Wait(src)
	if _, _, err := s.Embedded(src, bad, []Track{bad}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("after a failed pass err=%v, want ErrUnavailable rather than another pass", err)
	}
}

func TestEmbeddedReextractsReplacedFile(t *testing.T) {
	bin := needFFmpeg(t)
	dir := t.TempDir()
	src := mkvWithSubs(t, bin, dir)
	s := New(filepath.Join(dir, "cache"), bin, nil)
	tr := Track{StreamID: 11, Index: 1}
	s.Embedded(src, tr, []Track{tr})
	s.Wait(src)
	first, ready, _ := s.Embedded(src, tr, []Track{tr})
	if !ready {
		t.Fatal("not ready after the pass")
	}
	// Replace the file: the cache key changes, so the old cues are not served.
	if err := os.WriteFile(src, append(mustRead(t, src), 0), 0o600); err != nil {
		t.Fatal(err)
	}
	second, ready, _ := s.Embedded(src, tr, []Track{tr})
	if ready || second == first {
		t.Fatalf("replaced file served from the old cache: %s", second)
	}
	s.Wait(src)
}

func TestSidecarConvertsSubRip(t *testing.T) {
	bin := needFFmpeg(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "film.en.srt")
	if err := os.WriteFile(src, []byte(srt), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(filepath.Join(dir, "cache"), bin, nil)
	out, err := s.Sidecar(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	body := string(mustRead(t, out))
	if !strings.HasPrefix(body, "WEBVTT") || !strings.Contains(body, "00.500 --> 00:01.500") {
		t.Fatalf("not converted to WebVTT timing:\n%s", body)
	}
	vtt := filepath.Join(dir, "film.en.vtt")
	_ = os.WriteFile(vtt, []byte("WEBVTT\n"), 0o600)
	if got, _ := s.Sidecar(context.Background(), vtt); got != vtt {
		t.Fatalf("a .vtt sidecar should be served as is, got %s", got)
	}
}

func TestTextCodec(t *testing.T) {
	for _, c := range []string{"subrip", "ASS", "mov_text", "webvtt"} {
		if !TextCodec(c) {
			t.Errorf("%s should convert", c)
		}
	}
	for _, c := range []string{"hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", ""} {
		if TextCodec(c) {
			t.Errorf("%s is image based or unknown", c)
		}
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
