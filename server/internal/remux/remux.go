// Package remux streams a file in a container a browser can play: the video
// is copied untouched and the audio is copied or converted to AAC, written as
// fragmented MP4 to the response while ffmpeg reads the file. Converting audio
// costs a fraction of one core, so this needs no GPU.
//
// When the client cannot decode the video at all, the same stream carries the
// video re-encoded to H.264 with libx264 instead. That costs one or more whole
// cores per stream, so transcodes have their own, smaller limit.
//
// A stream like this cannot be seeked by byte range, so a client seeks by
// asking for a new stream at a position. Copied video can only start on a
// keyframe, so the server first finds the keyframe at or before the position
// and tells the client where the stream really starts. Re-encoded video starts
// exactly where it is asked to.
package remux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
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

// HLSAudio reports whether an audio codec can be copied into HLS fMP4
// segments. Apple's HLS spec lists AAC, AC-3 and E-AC-3 there, not MP3 (MP3
// in fMP4 segments makes Chrome's HLS player fail to parse the stream), so
// everything else is converted to AAC.
func HLSAudio(codec string) bool {
	switch strings.ToLower(codec) {
	case "aac", "ac3", "eac3":
		return true
	}
	return false
}

// DefaultMaxTranscodes is how many video transcodes run at once unless the
// operator says otherwise. Each takes one or more whole CPU cores.
const DefaultMaxTranscodes = 2

type Service struct {
	FFmpeg, FFprobe string
	Log             *slog.Logger
	sem             chan struct{}
	// tsem limits video transcodes, which do not count against sem.
	tsem chan struct{}
	// asem limits converted music (audio.go). A listener's player fetches the
	// next track while one plays, so music has its own slots rather than
	// taking the video streams'.
	asem chan struct{}
	// Opus is true when ffmpeg has libopus, so music can convert to Opus as
	// well as AAC. DetectAudio sets it.
	Opus bool
	// ToneMap is true when ffmpeg has the zscale and tonemap filters, so HDR
	// can be converted to SDR. Detect sets it.
	ToneMap bool
}

// New allows up to maxStreams concurrent converted streams that copy the
// video, and DefaultMaxTranscodes that re-encode it.
func New(ffmpeg, ffprobe string, maxStreams int, log *slog.Logger) *Service {
	if maxStreams < 1 {
		maxStreams = 1
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{FFmpeg: ffmpeg, FFprobe: ffprobe, Log: log, sem: make(chan struct{}, maxStreams),
		tsem: make(chan struct{}, DefaultMaxTranscodes), asem: make(chan struct{}, DefaultMaxAudio)}
}

// SetMaxTranscodes changes how many video transcodes may run at once. Call it
// before the first stream.
func (s *Service) SetMaxTranscodes(n int) {
	s.tsem = make(chan struct{}, max(1, n))
}

// Features is what the ffmpeg build can do for converted streams.
type Features struct {
	// X264: the libx264 encoder is there, so video can be transcoded.
	X264 bool
	// ToneMap: zscale and tonemap are there, so HDR becomes SDR properly.
	ToneMap bool
}

// Detect asks ffmpeg which encoders and filters it has, and remembers whether
// it can tone map.
func (s *Service) Detect(ctx context.Context) (Features, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var f Features
	enc, err := exec.CommandContext(ctx, s.FFmpeg, "-hide_banner", "-encoders").Output()
	if err != nil {
		return f, fmt.Errorf("list ffmpeg encoders: %w", err)
	}
	filt, err := exec.CommandContext(ctx, s.FFmpeg, "-hide_banner", "-filters").Output()
	if err != nil {
		return f, fmt.Errorf("list ffmpeg filters: %w", err)
	}
	encoders, filters := listed(string(enc)), listed(string(filt))
	f.X264 = encoders["libx264"]
	f.ToneMap = filters["zscale"] && filters["tonemap"]
	s.ToneMap = f.ToneMap
	return f, nil
}

// listed collects the names in ffmpeg's -encoders or -filters output, where
// each entry is a flags column followed by the name.
func listed(out string) map[string]bool {
	names := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) >= 2 {
			names[f[1]] = true
		}
	}
	return names
}

// keyframeWindows are how far before a seek position KeyframeAt reads packets,
// widening only when a window holds no keyframe: most files have one every few
// seconds, and every second read is real I/O on a high-bitrate file over a share.
var keyframeWindows = []float64{3, 10, 30}

// KeyframeAt returns the time of the keyframe at or before ms in the file's
// first video stream, which is where a stream asked for at ms begins. Times
// count from the file's own start, as ffmpeg's -ss does; ffprobe reports
// packet times as stored, which in MPEG-TS start at an arbitrary value.
func (s *Service) KeyframeAt(ctx context.Context, path string, ms int64) (int64, error) {
	if ms <= 0 {
		return 0, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, s.FFprobe, "-v", "error", "-show_entries", "format=start_time", "-of", "csv=p=0", path).Output()
	if err != nil {
		return 0, fmt.Errorf("find keyframe: %w", err)
	}
	start, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		start = 0 // "N/A": the file's times already start at 0
	}
	at := start + float64(ms)/1000
	for _, w := range keyframeWindows {
		from := math.Max(start, at-w)
		interval := strconv.FormatFloat(from, 'f', 3, 64) + "%" + strconv.FormatFloat(at+0.001, 'f', 3, 64)
		out, err := exec.CommandContext(ctx, s.FFprobe, "-v", "error", "-select_streams", "v:0", "-show_entries", "packet=pts_time,flags",
			"-read_intervals", interval, "-of", "csv=p=0", path).Output()
		if err != nil {
			return 0, fmt.Errorf("find keyframe: %w", err)
		}
		if k, ok := lastKeyframe(string(out), start, ms); ok {
			return k, nil
		}
		if from <= start {
			break // read from the very start and found none
		}
	}
	return 0, nil
}

// lastKeyframe picks the latest packet flagged K at or before ms (counted from
// start) in ffprobe's "pts_time,flags" lines. Packets come in decode order,
// and a demuxer that seeks by byte (MPEG-TS) may hand over some from before
// the window, so it takes the maximum rather than the first.
func lastKeyframe(out string, start float64, ms int64) (int64, bool) {
	best, ok := int64(0), false
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(line, ",")
		if len(f) < 2 || !strings.HasPrefix(f[1], "K") {
			continue
		}
		t, err := strconv.ParseFloat(f[0], 64)
		if err != nil {
			continue
		}
		k := int64((t-start)*1000 + 0.5)
		if k < 0 {
			k = 0
		}
		if k <= ms && (!ok || k > best) {
			best, ok = k, true
		}
	}
	return best, ok
}

// Options describe one stream.
type Options struct {
	Path string
	// StartMs should be a time KeyframeAt returned when the video is copied.
	// When it is transcoded, the stream starts exactly at StartMs.
	StartMs int64
	// AudioIndex is the stream index of the audio track, or -1 for none.
	AudioIndex int
	// TranscodeAudio converts it to stereo AAC; otherwise it is copied.
	TranscodeAudio bool
	// AudioCodec is that track's FFprobe codec name. Copied AAC gets the
	// ADTS-to-MP4 bitstream filter: MPEG-TS carries AAC as ADTS, which the MP4
	// muxer refuses, and the filter passes AAC from MKV or MP4 through as is.
	AudioCodec string
	// HEVC tags the video hvc1, which Safari and Chrome require in MP4.
	HEVC bool
	// Video, when set, re-encodes the video to H.264 instead of copying it.
	Video *Video
	// Wait is how long to wait for a free slot before giving up with ErrBusy.
	Wait time.Duration
}

// Video is how to re-encode the video for a client that cannot decode it.
type Video struct {
	// SrcWidth and SrcHeight are the source's size, 0 when unknown.
	SrcWidth, SrcHeight int
	// MaxWidth and MaxHeight bound the output; 0 means no bound. The picture
	// is scaled down to fit, keeping its shape, and never scaled up.
	MaxWidth, MaxHeight int
	// FPS is the source frame rate (0 when unknown); MaxFPS caps the output.
	FPS, MaxFPS float64
	// MaxBitrate is the client's limit in bits per second, 0 for none.
	MaxBitrate int64
	// HDR is the source's HDR type (hdr10, hlg, ...), empty for SDR.
	HDR string
	// ToneMap converts HDR to SDR, for a display that reports no HDR.
	ToneMap bool
}

// Fit returns the output size for a source of srcW x srcH within maxW x maxH
// (0 means unbounded): scaled down to fit with its shape kept, never up, and
// rounded down to even numbers, which 4:2:0 H.264 needs. It returns 0, 0 when
// the source size is unknown.
func Fit(srcW, srcH, maxW, maxH int) (int, int) {
	if srcW <= 0 || srcH <= 0 {
		return 0, 0
	}
	f := 1.0
	if maxW > 0 {
		f = math.Min(f, float64(maxW)/float64(srcW))
	}
	if maxH > 0 {
		f = math.Min(f, float64(maxH)/float64(srcH))
	}
	even := func(x float64) int { return max(2, int(x)/2*2) }
	return even(float64(srcW) * f), even(float64(srcH) * f)
}

// videoBitrate is the cap for the encoder's rate control at an output height:
// generous for CRF 21, so it only bites on very busy scenes, and never above
// what the client said it takes (less room for the audio).
func videoBitrate(height int, clientMax int64) int64 {
	var b int64
	switch {
	case height > 0 && height <= 480:
		b = 2_500_000
	case height > 0 && height <= 720:
		b = 5_000_000
	case height > 0 && height <= 1080:
		b = 10_000_000
	default:
		b = 25_000_000
	}
	if clientMax > 0 {
		b = max(500_000, min(b, clientMax-256_000))
	}
	return b
}

// videoArgs are the filter and encoder arguments for a transcoded video.
func (s *Service) videoArgs(v Video) []string {
	var filters []string
	w, h := Fit(v.SrcWidth, v.SrcHeight, v.MaxWidth, v.MaxHeight)
	switch {
	case w > 0:
		filters = append(filters, fmt.Sprintf("scale=%d:%d", w, h))
	default:
		// Size unknown: let ffmpeg fit it, still never larger than the source.
		mw, mh := "iw", "ih"
		if v.MaxWidth > 0 {
			mw = fmt.Sprintf("min(iw\\,%d)", v.MaxWidth)
		}
		if v.MaxHeight > 0 {
			mh = fmt.Sprintf("min(ih\\,%d)", v.MaxHeight)
		}
		filters = append(filters, "scale="+mw+":"+mh+":force_original_aspect_ratio=decrease:force_divisible_by=2")
	}
	toneMapped := false
	if v.HDR != "" && v.ToneMap {
		if s.ToneMap {
			tin := "smpte2084"
			if v.HDR == "hlg" {
				tin = "arib-std-b67"
			}
			// Linear light, BT.709 primaries, Hable's curve, then back to BT.709.
			filters = append(filters, "zscale=tin="+tin+":min=bt2020nc:pin=bt2020:rin=tv:t=linear:npl=100",
				"format=gbrpf32le", "zscale=p=bt709", "tonemap=tonemap=hable:desat=0", "zscale=t=bt709:m=bt709:r=tv")
			toneMapped = true
		}
		// Without zscale the HDR picture goes out as 8-bit with its HDR tags
		// kept, so a browser that tone maps by itself still can. Detect's
		// caller logs this once at startup.
	}
	filters = append(filters, "format=yuv420p")
	a := []string{"-vf", strings.Join(filters, ","),
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "21", "-profile:v", "high", "-pix_fmt", "yuv420p"}
	if toneMapped {
		a = append(a, "-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709")
	}
	outH := h
	if outH == 0 {
		outH = v.MaxHeight
	}
	rate := videoBitrate(outH, v.MaxBitrate)
	a = append(a, "-maxrate", strconv.FormatInt(rate, 10), "-bufsize", strconv.FormatInt(2*rate, 10))
	fps := v.FPS
	if v.MaxFPS > 0 && (fps == 0 || fps > v.MaxFPS+0.5) {
		a = append(a, "-fpsmax", strconv.FormatFloat(v.MaxFPS, 'f', -1, 64))
		if fps > 0 {
			fps = v.MaxFPS
		}
	}
	// A keyframe every 2 s, so a fragment ends there and a seek starts on one.
	a = append(a, "-force_key_frames", "expr:gte(t,n_forced*2)")
	if fps > 0 {
		a = append(a, "-g", strconv.Itoa(int(math.Round(2*fps))))
	}
	return a
}

// args runs the conversion into fragmented MP4 on stdout.
func (s *Service) args(o Options) []string {
	return append(s.convertArgs(o), "-f", "mp4", "-movflags", "frag_keyframe+empty_moov+default_base_moof", "-frag_duration", "2000000", "pipe:1")
}

// convertArgs are the input, mapping and codec arguments every converted
// stream shares, whatever it is written as.
func (s *Service) convertArgs(o Options) []string {
	a := []string{"-nostdin", "-hide_banner", "-loglevel", "error"}
	switch {
	case o.StartMs > 0 && o.Video != nil:
		// Decoding from the keyframe before StartMs and dropping frames up to
		// it, so the stream starts exactly at StartMs.
		a = append(a, "-accurate_seek", "-ss", strconv.FormatFloat(float64(o.StartMs)/1000, 'f', 3, 64))
	case o.StartMs > 0:
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
	switch {
	case o.Video != nil:
		a = append(a, s.videoArgs(*o.Video)...)
		if o.StartMs > 0 && o.AudioIndex >= 0 && !o.TranscodeAudio {
			// Copied audio would otherwise start at the demuxer's seek point,
			// seconds early, and drag the whole stream's time 0 back with it.
			a = append(a, "-copypriorss", "0")
		}
	default:
		a = append(a, "-c:v", "copy")
		if o.HEVC {
			a = append(a, "-tag:v", "hvc1")
		}
	}
	if o.AudioIndex >= 0 {
		if o.TranscodeAudio {
			a = append(a, "-c:a", "aac", "-ac", "2", "-b:a", "192k")
		} else {
			a = append(a, "-c:a", "copy")
			if o.AudioCodec == "aac" {
				a = append(a, "-bsf:a", "aac_adtstoasc")
			}
		}
	}
	return append(a, "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1", "-avoid_negative_ts", "make_zero")
}

// Stream writes the converted file to w until it ends, ctx ends, or the
// client stops reading. ffmpeg only reads as fast as w accepts.
func (s *Service) Stream(ctx context.Context, w io.Writer, o Options) error {
	sem := s.sem
	if o.Video != nil {
		sem = s.tsem
	}
	if !acquire(ctx, sem, o.Wait) {
		return ErrBusy
	}
	defer func() { <-sem }()
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

// acquire takes a slot, waiting up to wait for one to free up.
func acquire(ctx context.Context, sem chan struct{}, wait time.Duration) bool {
	select {
	case sem <- struct{}{}:
		return true
	default:
	}
	if wait <= 0 {
		return false
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case sem <- struct{}{}:
		return true
	case <-t.C:
		return false
	case <-ctx.Done():
		return false
	}
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
