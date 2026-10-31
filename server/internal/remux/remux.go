// Package remux streams a file in a container a browser can play: the video
// is copied untouched and the audio is copied or converted to AAC, written as
// fragmented MP4 to the response while ffmpeg reads the file. Converting audio
// costs a fraction of one core, so this needs no GPU.
//
// A stream like this cannot be seeked by byte range, so a client seeks by
// asking for a new stream at a position. Copied video can only start on a
// keyframe, so the server first finds the keyframe at or before the position
// and tells the client where the stream really starts.
package remux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ErrBusy means the server is already running as many streams as it allows.
var ErrBusy = errors.New("too many converted streams at once")

// MP4Video reports whether a video codec can be copied into MP4 for a browser.
func MP4Video(codec string) bool {
	switch strings.ToLower(codec) {
	case "h264", "hevc", "av1", "vp9":
		return true
	}
	return false
}

// MP4Audio reports whether an audio codec can be copied into MP4 as is.
func MP4Audio(codec string) bool {
	switch strings.ToLower(codec) {
	case "aac", "mp3", "ac3", "eac3", "opus", "flac", "alac":
		return true
	}
	return false
}

type Service struct {
	FFmpeg, FFprobe string
	Log             *slog.Logger
	sem             chan struct{}
}

// New allows up to max concurrent streams.
func New(ffmpeg, ffprobe string, maxStreams int, log *slog.Logger) *Service {
	if maxStreams < 1 {
		maxStreams = 1
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{FFmpeg: ffmpeg, FFprobe: ffprobe, Log: log, sem: make(chan struct{}, maxStreams)}
}

// KeyframeAt returns the time of the keyframe at or before ms in the file's
// first video stream, which is where a stream asked for at ms begins.
func (s *Service) KeyframeAt(ctx context.Context, path string, ms int64) (int64, error) {
	if ms <= 0 {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	sec := strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64)
	out, err := exec.CommandContext(ctx, s.FFprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time,flags",
		"-read_intervals", sec+"%+#8", "-of", "csv=p=0", path).Output()
	if err != nil {
		return 0, fmt.Errorf("find keyframe: %w", err)
	}
	// The read starts at the seek point, which is a keyframe; take the first
	// packet flagged K in case the demuxer hands over a non-key packet first.
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, ",")
		if len(f) < 2 || !strings.HasPrefix(f[1], "K") {
			continue
		}
		t, err := strconv.ParseFloat(f[0], 64)
		if err != nil || t < 0 {
			continue
		}
		if k := int64(t*1000 + 0.5); k <= ms {
			return k, nil
		}
		return 0, nil
	}
	return 0, nil
}

// Options describe one stream.
type Options struct {
	Path string
	// StartMs should be a time KeyframeAt returned.
	StartMs int64
	// AudioIndex is the stream index of the audio track, or -1 for none.
	AudioIndex int
	// TranscodeAudio converts it to stereo AAC; otherwise it is copied.
	TranscodeAudio bool
	// HEVC tags the video hvc1, which Safari and Chrome require in MP4.
	HEVC bool
}

func (s *Service) args(o Options) []string {
	a := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	if o.StartMs > 0 {
		// Without accurate seek, ffmpeg steps back 3/23 s before seeking, so
		// asking for exactly the keyframe lands on the one before it. Half a
		// second past the keyframe lands on it, and video and audio start
		// together there.
		a = append(a, "-noaccurate_seek", "-ss", strconv.FormatFloat(float64(o.StartMs)/1000+0.5, 'f', 3, 64))
	}
	a = append(a, "-i", o.Path, "-map", "0:v:0")
	if o.AudioIndex >= 0 {
		a = append(a, "-map", "0:"+strconv.Itoa(o.AudioIndex))
	}
	a = append(a, "-c:v", "copy")
	if o.HEVC {
		a = append(a, "-tag:v", "hvc1")
	}
	if o.AudioIndex >= 0 {
		if o.TranscodeAudio {
			a = append(a, "-c:a", "aac", "-ac", "2", "-b:a", "192k")
		} else {
			a = append(a, "-c:a", "copy")
		}
	}
	return append(a, "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1", "-avoid_negative_ts", "make_zero",
		"-f", "mp4", "-movflags", "frag_keyframe+empty_moov+default_base_moof", "-frag_duration", "2000000", "pipe:1")
}

// Stream writes the converted file to w until it ends, ctx ends, or the
// client stops reading. ffmpeg only reads as fast as w accepts.
func (s *Service) Stream(ctx context.Context, w io.Writer, o Options) error {
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	default:
		return ErrBusy
	}
	cmd := exec.CommandContext(ctx, s.FFmpeg, s.args(o)...)
	cmd.Stdout = w
	var stderr strings.Builder
	cmd.Stderr = &limited{w: &stderr, n: 2000}
	started := time.Now()
	err := cmd.Run()
	if ctx.Err() != nil {
		// The client left or the stream was stopped: not a failure.
		return nil
	}
	if err != nil {
		return fmt.Errorf("ffmpeg after %s: %w: %s", time.Since(started).Round(time.Second), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// limited keeps the first n bytes of ffmpeg's error output.
type limited struct {
	w io.Writer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		_, _ = l.w.Write(p[:k])
		l.n -= k
	}
	return len(p), nil
}
