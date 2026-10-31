package remux

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Converted music: a track a client cannot decode (SHN in any browser, ALAC
// in Chrome) is decoded and written as AAC or Opus while ffmpeg reads the
// file, like the video's converted streams. Audio alone costs a few percent
// of a core.
const (
	// AudioAAC is AAC in ADTS frames, which every browser, Safari included,
	// and Media3 play as a plain progressive stream.
	AudioAAC = "aac"
	// AudioOpus is Opus in Ogg, smaller for the same quality, for clients
	// that decode it.
	AudioOpus = "opus"
)

// DefaultMaxAudio is how many converted music streams run at once.
const DefaultMaxAudio = 8

// AudioOptions describe one converted music stream.
type AudioOptions struct {
	Path string
	// StartMs starts the stream that far into the track, for a seek. Audio
	// has no keyframes, so the stream starts exactly there.
	StartMs int64
	// Codec is AudioAAC or AudioOpus.
	Codec string
	// Wait is how long to wait for a free slot before giving up with ErrBusy.
	Wait time.Duration
}

// AudioMime is the Content-Type of a converted music stream.
func AudioMime(codec string) string {
	if codec == AudioOpus {
		return "audio/ogg"
	}
	return "audio/aac"
}

// DetectAudio asks ffmpeg whether it has libopus.
func (s *Service) DetectAudio(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	enc, err := exec.CommandContext(ctx, s.FFmpeg, "-hide_banner", "-encoders").Output()
	if err != nil {
		return fmt.Errorf("list ffmpeg encoders: %w", err)
	}
	s.Opus = listed(string(enc))["libopus"]
	return nil
}

// audioArgs decode the first audio stream from StartMs and encode it, stereo,
// to stdout. Pictures, tags and chapters stay behind.
func audioArgs(o AudioOptions) ([]string, error) {
	a := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if o.StartMs > 0 {
		a = append(a, "-ss", strconv.FormatFloat(float64(o.StartMs)/1000, 'f', 3, 64))
	}
	a = append(a, "-i", o.Path, "-map", "0:a:0", "-vn", "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1", "-ac", "2")
	switch o.Codec {
	case AudioAAC:
		a = append(a, "-c:a", "aac", "-b:a", "256k", "-f", "adts")
	case AudioOpus:
		a = append(a, "-c:a", "libopus", "-b:a", "160k", "-f", "ogg")
	default:
		return nil, fmt.Errorf("unknown audio codec %q", o.Codec)
	}
	return append(a, "pipe:1"), nil
}

// StreamAudio writes the converted track to w until it ends, ctx ends, or the
// client stops reading.
func (s *Service) StreamAudio(ctx context.Context, w io.Writer, o AudioOptions) error {
	args, err := audioArgs(o)
	if err != nil {
		return err
	}
	if o.Codec == AudioOpus && !s.Opus {
		return fmt.Errorf("this ffmpeg has no libopus")
	}
	sem := s.asem
	if sem == nil {
		sem = s.sem
	}
	if !acquire(ctx, sem, o.Wait) {
		return ErrBusy
	}
	defer func() { <-sem }()
	cmd := exec.CommandContext(ctx, s.FFmpeg, args...)
	cmd.Stdout = w
	var stderr strings.Builder
	cmd.Stderr = &limited{w: &stderr, n: 2000}
	started := time.Now()
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("ffmpeg after %s: %w: %s", time.Since(started).Round(time.Second), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
