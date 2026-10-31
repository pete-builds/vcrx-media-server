package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// AudioProber reads what a music scan needs from one audio file.
type AudioProber interface {
	ProbeAudio(ctx context.Context, path string) (*AudioResult, error)
}

// AudioResult describes one audio file: its first audio stream and its tags.
type AudioResult struct {
	// Container is the file's own kind (flac, mp3, m4a, ogg, wav, shn ...),
	// which is what a browser is asked about before direct play.
	Container  string
	FormatName string
	DurationMs int64
	Bitrate    int64
	Codec      string
	SampleRate int
	Channels   int
	BitDepth   int
	// Tags merges the container's tags with the audio stream's (Ogg and Opus
	// keep them on the stream). Keys are lowercased; values are trimmed.
	Tags map[string]string
	// EmbeddedCover is true when the file carries a picture (an attached_pic
	// stream), which serves as album art when the folder has none.
	EmbeddedCover bool
}

// ErrNoAudio means the file holds no audio stream.
var ErrNoAudio = errors.New("no audio stream")

// ProbeAudio runs ffprobe on an audio file, which must be an absolute path.
func (f *FFprobe) ProbeAudio(ctx context.Context, path string) (*AudioResult, error) {
	out, err := f.run(ctx, path)
	if err != nil {
		return nil, err
	}
	return ParseAudio(out, path)
}

// ParseAudio turns ffprobe's JSON for an audio file into an AudioResult.
func ParseAudio(data []byte, path string) (*AudioResult, error) {
	var out ffOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("ffprobe json: %w", err)
	}
	r := &AudioResult{
		FormatName: out.Format.FormatName,
		Container:  AudioContainer(out.Format.FormatName, path),
		DurationMs: secondsToMs(out.Format.Duration),
		Bitrate:    atoi64(out.Format.BitRate),
		Tags:       map[string]string{},
	}
	addTags(r.Tags, out.Format.Tags)
	var audio *ffStream
	for i := range out.Streams {
		s := &out.Streams[i]
		switch {
		case s.CodecType == KindVideo && s.Disposition["attached_pic"] == 1:
			r.EmbeddedCover = true
		case s.CodecType == KindAudio && audio == nil:
			audio = s
		}
	}
	if audio == nil {
		return nil, ErrNoAudio
	}
	addTags(r.Tags, audio.Tags)
	r.Codec = audio.CodecName
	r.SampleRate = int(atoi64(audio.SampleRate))
	r.Channels = audio.Channels
	r.BitDepth = int(atoi64(audio.BitsPerRawSample))
	if r.BitDepth == 0 {
		// FLAC and WAV report it here when bits_per_raw_sample is empty.
		r.BitDepth = audio.BitsPerSample
	}
	if r.DurationMs == 0 {
		r.DurationMs = streamDurationMs(audio)
	}
	if b := atoi64(audio.BitRate); b > 0 && r.Bitrate == 0 {
		r.Bitrate = b
	}
	if r.Bitrate == 0 && r.DurationMs > 0 {
		if size := atoi64(out.Format.Size); size > 0 {
			r.Bitrate = size * 8 * 1000 / r.DurationMs
		}
	}
	return r, nil
}

// addTags copies tags in, lowercasing keys and keeping the first value seen.
func addTags(dst, src map[string]string) {
	for k, v := range src {
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		if _, ok := dst[k]; !ok {
			dst[k] = v
		}
	}
}

// AudioContainer names an audio file's container the way clients are asked
// about it. MP4 audio is "m4a" whatever ffprobe calls the demuxer.
func AudioContainer(formatName, path string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	names := strings.Split(formatName, ",")
	switch {
	case containsName(names, "mov") || containsName(names, "mp4"):
		return "m4a"
	case containsName(names, "matroska") || containsName(names, "webm"):
		if ext == "webm" {
			return "webm"
		}
		return "mka"
	case containsName(names, "ogg"):
		if ext == "opus" {
			return "opus"
		}
		return "ogg"
	}
	if len(names) > 0 && names[0] != "" {
		return names[0]
	}
	return ext
}

func containsName(names []string, n string) bool {
	for _, x := range names {
		if x == n {
			return true
		}
	}
	return false
}
