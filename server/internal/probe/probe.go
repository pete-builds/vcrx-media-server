// Package probe reads container and stream facts from media files with ffprobe.
// It never writes to the file: ffprobe only opens its input for reading.
package probe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Prober reports what is inside one media file.
type Prober interface {
	Probe(ctx context.Context, path string) (*Result, error)
}

// Result is what the scanner stores for one file. The flat video fields describe
// the primary video stream, so callers need not search Streams for it.
type Result struct {
	Container  string
	FormatName string
	DurationMs int64
	Bitrate    int64
	Streams    []Stream

	VideoCodec   string
	VideoProfile string
	VideoLevel   int
	BitDepth     int
	Width        int
	Height       int
	FrameRate    float64
	Resolution   string
	HDRType      string
	DVProfile    int
	DVCompatID   int
}

// Stream kinds kept in Result.Streams. Attachments, data streams and embedded
// cover art are dropped because nothing can play them.
const (
	KindVideo    = "video"
	KindAudio    = "audio"
	KindSubtitle = "subtitle"
)

// HDR types, matching the media_versions.hdr_type column.
const (
	HDR10       = "hdr10"
	HDR10Plus   = "hdr10plus"
	HLG         = "hlg"
	DolbyVision = "dolby_vision"
)

// Stream is one video, audio or subtitle stream inside a file.
type Stream struct {
	Index           int
	Kind            string
	Codec           string
	Profile         string
	Level           int
	BitDepth        int
	Width           int
	Height          int
	FrameRate       float64
	Language        string
	Title           string
	Channels        int
	ChannelLayout   string
	SampleRate      int
	Bitrate         int64
	Default         bool
	Forced          bool
	HearingImpaired bool
	ImageBased      bool
}

// ErrNoVideo means the file holds no playable video stream.
var ErrNoVideo = errors.New("no video stream")

// DefaultTimeout bounds one ffprobe run. A file on a sleeping NAS disk can take
// seconds to answer; a hung SMB read must not stall the whole scan.
const DefaultTimeout = 60 * time.Second

// maxOutput caps what we read from ffprobe, so a pathological file cannot
// exhaust memory.
const maxOutput = 16 << 20

// FFprobe runs the ffprobe binary. The zero value uses "ffprobe" from PATH and
// DefaultTimeout.
type FFprobe struct {
	Path    string
	Timeout time.Duration
}

// Probe runs ffprobe on path, which must be absolute.
func (f *FFprobe) Probe(ctx context.Context, path string) (*Result, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("probe %q: path must be absolute", path)
	}
	timeout := f.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// The file: prefix stops ffprobe reading a name as a protocol or an option.
	cmd := exec.CommandContext(ctx, f.bin(), "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", "file:"+path)
	var stdout, stderr limitedBuffer
	stdout.max, stderr.max = maxOutput, 8<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("ffprobe timed out after %s", timeout)
	}
	if err != nil {
		msg := firstLine(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("ffprobe: %w", err)
		}
		return nil, fmt.Errorf("ffprobe: %s", msg)
	}
	if stdout.truncated {
		return nil, fmt.Errorf("ffprobe output exceeds %d bytes", maxOutput)
	}
	return Parse(stdout.Bytes(), path)
}

// Version returns ffprobe's first version line, so the server can fail loudly
// at startup instead of marking every file as a probe error.
func (f *FFprobe) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, f.bin(), "-version").Output()
	if err != nil {
		return "", fmt.Errorf("ffprobe %q: %w", f.bin(), err)
	}
	return firstLine(string(out)), nil
}

func (f *FFprobe) bin() string {
	if f.Path == "" {
		return "ffprobe"
	}
	return f.Path
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 500 {
		s = s[:500]
	}
	return strings.TrimSpace(s)
}

type limitedBuffer struct {
	bytes.Buffer
	max       int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.max - b.Len()
	if room <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		b.truncated = true
		b.Buffer.Write(p[:room])
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

var _ io.Writer = (*limitedBuffer)(nil)
