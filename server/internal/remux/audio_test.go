package remux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// alac writes a 6 s ALAC track in MP4, which Chrome cannot decode: the case
// converted music exists for (SHN is the other, but ffmpeg cannot write it).
func alac(t *testing.T, ff string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "in.m4a")
	cmd := exec.Command(ff, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=6:sample_rate=96000",
		"-ac", "1", "-c:a", "alac", "-metadata", "title=Low Tide", "-y", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cannot build fixture: %v %s", err, msg)
	}
	return out
}

// Converted music with the real ffmpeg: AAC in ADTS from the start and from a
// seek point, Opus in Ogg when ffmpeg has it, stereo, and nothing but audio.
func TestStreamAudioConverts(t *testing.T) {
	ff, fp := tools(t)
	in := alac(t, ff)
	s := New(ff, fp, 2, nil)
	if err := s.DetectAudio(context.Background()); err != nil {
		t.Fatal(err)
	}
	type tc struct {
		codec, ext, wantCodec string
		start                 int64
		wantSec               float64
	}
	cases := []tc{{AudioAAC, "aac", "aac", 0, 6}, {AudioAAC, "aac", "aac", 4000, 2}}
	if s.Opus {
		cases = append(cases, tc{AudioOpus, "ogg", "opus", 0, 6})
	} else {
		t.Log("ffmpeg has no libopus; Opus not tested")
	}
	for _, c := range cases {
		out := filepath.Join(t.TempDir(), "out."+c.ext)
		f, err := os.Create(out)
		if err != nil {
			t.Fatal(err)
		}
		err = s.StreamAudio(context.Background(), f, AudioOptions{Path: in, Codec: c.codec, StartMs: c.start})
		_ = f.Close()
		if err != nil {
			t.Fatalf("%s from %d: %v", c.codec, c.start, err)
		}
		if got := probe(t, fp, out, "stream=codec_type,codec_name,channels"); got != "audio,"+c.wantCodec+",2" && got != c.wantCodec+",audio,2" {
			t.Errorf("%s from %d: streams %q, want one stereo %s stream", c.codec, c.start, got, c.wantCodec)
		}
		// ADTS carries no duration, so count the decoded audio.
		dec, err := exec.Command(fp, "-v", "error", "-count_packets", "-select_streams", "a:0", "-show_entries", "format=duration",
			"-of", "csv=p=0", out).Output()
		if err != nil {
			t.Fatal(err)
		}
		sec, _ := strconv.ParseFloat(string(trimNL(dec)), 64)
		if sec < c.wantSec-0.3 || sec > c.wantSec+0.3 {
			t.Errorf("%s from %d ms: %.2f s of audio, want about %.0f", c.codec, c.start, sec, c.wantSec)
		}
	}
	if err := s.StreamAudio(context.Background(), os.Stdout, AudioOptions{Path: in, Codec: "mp3"}); err == nil {
		t.Error("an unknown codec was accepted")
	}
}

func trimNL(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

// Converted music has its own slots: a full set refuses the next stream with
// ErrBusy and leaves the video streams' slots alone.
func TestStreamAudioHasItsOwnSlots(t *testing.T) {
	s := New("ffmpeg", "ffprobe", 1, nil)
	for range DefaultMaxAudio {
		s.asem <- struct{}{}
	}
	err := s.StreamAudio(context.Background(), os.Stdout, AudioOptions{Path: "/nonexistent.flac", Codec: AudioAAC})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if len(s.sem) != 0 {
		t.Fatal("music took a video stream slot")
	}
}
