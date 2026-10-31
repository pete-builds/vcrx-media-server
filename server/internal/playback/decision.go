// Package playback decides how a version reaches a device, records every playback
// session and its events, and keeps each user's resume position.
package playback

import (
	"fmt"
	"strings"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
)

// Capabilities is what the client reports it can decode, built on the device.
type Capabilities struct {
	Containers     []string        `json:"containers"`
	VideoCodecs    []VideoCodecCap `json:"video_codecs"`
	AudioCodecs    []AudioCodecCap `json:"audio_codecs"`
	HDRTypes       []string        `json:"hdr_types"`
	SubtitleCodecs []string        `json:"subtitle_codecs"`
	MaxWidth       int             `json:"max_width"`
	MaxHeight      int             `json:"max_height"`
	MaxBitrate     int64           `json:"max_bitrate"`
	// FMP4Stream: the client plays a fragmented MP4 stream that cannot be
	// seeked by byte range and seeks by asking for a new one. Only then are
	// files it cannot play directly converted (video copied, audio converted).
	FMP4Stream bool `json:"fmp4_stream"`
	// HLSStream: the client plays HLS with fragmented MP4 segments natively
	// (Safari, which cannot play a stream that does not answer byte ranges).
	// Files it cannot play directly are converted the same way and written as
	// HLS. A client that reports both gets HLS.
	HLSStream bool `json:"hls_stream"`
	// DefaultAudioOnly: the client plays only the file's default audio track
	// (a browser <video>), so that is the one that has to decode.
	DefaultAudioOnly bool `json:"default_audio_only"`
}

type VideoCodecCap struct {
	Codec       string   `json:"codec"`
	Profiles    []string `json:"profiles"`
	MaxBitDepth int      `json:"max_bit_depth"`
	MaxWidth    int      `json:"max_width"`
	MaxHeight   int      `json:"max_height"`
	MaxFPS      float64  `json:"max_fps"`
}

type AudioCodecCap struct {
	Codec       string `json:"codec"`
	MaxChannels int    `json:"max_channels"`
	Decoder     string `json:"decoder"`
}

// Decision kinds, in the order the ladder tries them.
const (
	DirectPlay     = "direct_play"
	Remux          = "remux"
	AudioTranscode = "audio_transcode"
	Transcode      = "transcode"
	Refused        = "refused"
)

type Decision struct {
	Kind          string
	Reason        string
	VideoDecision string
	AudioDecision string
	// Warnings are soft mismatches that still allow direct play, such as HDR sent
	// to a display that reports none.
	Warnings []string
}

func normContainer(c string) string {
	switch strings.ToLower(c) {
	case "matroska", "mkv":
		return "mkv"
	case "mp4", "m4v", "mov", "mp4,m4a,3gp,3g2,mj2":
		return "mp4"
	case "mpegts", "ts", "m2ts", "mts":
		return "ts"
	default:
		return strings.ToLower(c)
	}
}

func containerOK(container string, caps Capabilities) bool {
	want := normContainer(container)
	for _, c := range caps.Containers {
		if normContainer(c) == want {
			return true
		}
	}
	return false
}

func s(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func n(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// videoOK checks the primary video stream against the device. It returns a hard
// problem (cannot play correctly) or soft warnings.
func videoOK(v catalog.Version, caps Capabilities) (problem string, warnings []string) {
	codec := strings.ToLower(s(v.VideoCodec))
	if codec == "" {
		return "", nil
	}
	var match *VideoCodecCap
	for i := range caps.VideoCodecs {
		if strings.EqualFold(caps.VideoCodecs[i].Codec, codec) {
			match = &caps.VideoCodecs[i]
			break
		}
	}
	if match == nil {
		return fmt.Sprintf("the device has no %s video decoder", codec), nil
	}
	if prof := strings.ToLower(s(v.VideoProfile)); prof != "" && len(match.Profiles) > 0 {
		found := false
		for _, p := range match.Profiles {
			if strings.EqualFold(p, prof) {
				found = true
				break
			}
		}
		if !found {
			return fmt.Sprintf("the device's %s decoder lacks the %s profile", codec, prof), nil
		}
	}
	if depth := n(v.BitDepth); depth > 0 && match.MaxBitDepth > 0 && depth > int64(match.MaxBitDepth) {
		return fmt.Sprintf("%d-bit %s exceeds the decoder's %d-bit limit", depth, codec, match.MaxBitDepth), nil
	}
	maxW, maxH := match.MaxWidth, match.MaxHeight
	if maxW == 0 {
		maxW = caps.MaxWidth
	}
	if maxH == 0 {
		maxH = caps.MaxHeight
	}
	w, h := n(v.Width), n(v.Height)
	if (maxW > 0 && w > int64(maxW)) || (maxH > 0 && h > int64(maxH)) {
		return fmt.Sprintf("%dx%d exceeds the decoder's %dx%d", w, h, maxW, maxH), nil
	}
	if fps := v.FrameRate; fps != nil && match.MaxFPS > 0 && *fps > match.MaxFPS+0.5 {
		return fmt.Sprintf("%.0f fps exceeds the decoder's %.0f fps", *fps, match.MaxFPS), nil
	}
	if hdr := s(v.HDRType); hdr != "" {
		has := func(t string) bool {
			for _, x := range caps.HDRTypes {
				if strings.EqualFold(x, t) {
					return true
				}
			}
			return false
		}
		switch {
		case has(hdr):
		case hdr == "hdr10plus" && has("hdr10"):
			warnings = append(warnings, "HDR10+ plays as HDR10 on this display")
		case hdr == "dolby_vision":
			// Profiles 8 and 7 carry an HDR10, SDR or HLG base layer the player can fall
			// back to; profile 5 has none, so without Dolby Vision its colours are wrong.
			switch n(v.DVCompatID) {
			case 1, 6:
				if has("hdr10") {
					warnings = append(warnings, "Dolby Vision plays as its HDR10 base layer")
				} else {
					warnings = append(warnings, "Dolby Vision plays as HDR10 on a display that reports no HDR; colours may look washed out")
				}
			case 2:
				warnings = append(warnings, "Dolby Vision plays as its SDR base layer")
			case 4:
				warnings = append(warnings, "Dolby Vision plays as its HLG base layer")
			default:
				return "Dolby Vision without a compatible base layer needs a Dolby Vision display", nil
			}
		default:
			warnings = append(warnings, fmt.Sprintf("%s video on a display that reports no %s; colours may look washed out", strings.ToUpper(hdr), strings.ToUpper(hdr)))
		}
	}
	return "", warnings
}

// audioOK passes when at least one audio track decodes: the player picks it.
func audioOK(v catalog.Version, caps Capabilities) (ok bool, chosen string) {
	if len(v.AudioTracks) == 0 {
		return true, ""
	}
	tracks := v.AudioTracks
	if caps.DefaultAudioOnly {
		tracks = []catalog.Track{DefaultAudio(v)}
	}
	for _, t := range tracks {
		codec := strings.ToLower(s(t.Codec))
		for _, c := range caps.AudioCodecs {
			if strings.EqualFold(c.Codec, codec) {
				return true, codec
			}
		}
	}
	return false, ""
}

// DefaultAudio is the track a player without track selection plays: the one
// flagged default, else the first. The caller checks there is one.
func DefaultAudio(v catalog.Version) catalog.Track {
	for _, t := range v.AudioTracks {
		if t.IsDefault {
			return t
		}
	}
	return v.AudioTracks[0]
}

// ChooseAudio picks the audio for a converted stream: the first track the
// device decodes that MP4 can carry, copied; else the default track,
// converted to AAC. index is -1 when the file has no audio.
func ChooseAudio(v catalog.Version, caps Capabilities) (index int, transcode bool) {
	if len(v.AudioTracks) == 0 {
		return -1, false
	}
	for _, t := range v.AudioTracks {
		codec := strings.ToLower(s(t.Codec))
		if t.Index == nil || !remux.MP4Audio(codec) {
			continue
		}
		for _, c := range caps.AudioCodecs {
			if strings.EqualFold(c.Codec, codec) && (c.MaxChannels == 0 || t.Channels == nil || *t.Channels <= int64(c.MaxChannels)) {
				return int(*t.Index), false
			}
		}
	}
	d := DefaultAudio(v)
	if d.Index == nil {
		return -1, false
	}
	return int(*d.Index), true
}

// decodesH264 reports whether the client plays H.264, which is what a video
// transcode produces.
func decodesH264(caps Capabilities) bool {
	return h264Cap(caps) != nil
}

func h264Cap(caps Capabilities) *VideoCodecCap {
	for i := range caps.VideoCodecs {
		if strings.EqualFold(caps.VideoCodecs[i].Codec, "h264") {
			return &caps.VideoCodecs[i]
		}
	}
	return nil
}

// TranscodeVideo is how a transcode session re-encodes v for the client:
// within the client's H.264 size and frame rate limits (or its overall size
// limit), under its bitrate limit, and tone mapped to SDR when the source is
// HDR and the client reports no HDR at all.
func TranscodeVideo(v catalog.Version, caps Capabilities) remux.Video {
	out := remux.Video{SrcWidth: int(n(v.Width)), SrcHeight: int(n(v.Height)), MaxWidth: caps.MaxWidth, MaxHeight: caps.MaxHeight,
		MaxBitrate: caps.MaxBitrate}
	if c := h264Cap(caps); c != nil {
		if c.MaxWidth > 0 {
			out.MaxWidth = c.MaxWidth
		}
		if c.MaxHeight > 0 {
			out.MaxHeight = c.MaxHeight
		}
		out.MaxFPS = c.MaxFPS
	}
	if v.FrameRate != nil {
		out.FPS = *v.FrameRate
	}
	out.HDR = strings.ToLower(s(v.HDRType))
	if out.HDR == "dolby_vision" {
		// The decoder outputs the base layer: SDR, HLG, or PQ like HDR10.
		switch n(v.DVCompatID) {
		case 2:
			out.HDR = ""
		case 4:
			out.HDR = "hlg"
		}
	}
	out.ToneMap = out.HDR != "" && len(caps.HDRTypes) == 0
	return out
}

// Decide walks the ladder for one version: direct play, remux, audio transcode,
// full transcode.
func Decide(v catalog.Version, caps Capabilities) Decision {
	if v.ProbeStatus != "ok" {
		return Decision{Kind: DirectPlay, VideoDecision: "direct", AudioDecision: "direct",
			Reason: "the file has not been inspected yet, so it is sent as is"}
	}
	videoProblem, warnings := videoOK(v, caps)
	audioGood, audioCodec := audioOK(v, caps)
	cont := normContainer(s(v.Container))
	contGood := containerOK(cont, caps)
	if caps.MaxBitrate > 0 && n(v.Bitrate) > caps.MaxBitrate && videoProblem == "" {
		videoProblem = fmt.Sprintf("%d kbps exceeds the device's %d kbps limit", n(v.Bitrate)/1000, caps.MaxBitrate/1000)
	}
	d := Decision{Warnings: warnings, VideoDecision: "direct", AudioDecision: "direct"}
	var audioDesc string
	if audioCodec != "" {
		audioDesc = " and " + audioCodec
	}
	switch {
	case videoProblem == "" && audioGood && contGood:
		d.Kind = DirectPlay
		d.Reason = fmt.Sprintf("the device plays %s with %s%s directly", cont, describeVideo(v), audioDesc)
	case videoProblem == "" && audioGood:
		d.Kind, d.VideoDecision, d.AudioDecision = Remux, "copy", "copy"
		d.Reason = fmt.Sprintf("the device cannot read the %s container; the streams themselves are fine", cont)
	case videoProblem == "":
		d.Kind, d.VideoDecision, d.AudioDecision = AudioTranscode, "copy", "transcode"
		d.Reason = "no audio track in the file is one the device decodes: " + audioCodecs(v)
	default:
		d.Kind, d.VideoDecision = Transcode, "transcode"
		if !audioGood {
			d.AudioDecision = "transcode"
		}
		d.Reason = videoProblem
	}
	if len(warnings) > 0 && d.Kind == DirectPlay {
		d.Reason += "; " + strings.Join(warnings, "; ")
	}
	return d
}

func describeVideo(v catalog.Version) string {
	parts := []string{s(v.VideoCodec)}
	if p := s(v.VideoProfile); p != "" {
		parts = append(parts, p)
	}
	if r := s(v.Resolution); r != "" {
		parts = append(parts, r)
	}
	return strings.Join(parts, " ")
}

func audioCodecs(v catalog.Version) string {
	var cs []string
	for _, t := range v.AudioTracks {
		cs = append(cs, s(t.Codec))
	}
	return strings.Join(cs, ", ")
}

// ChooseVersion picks the best version the device can direct play, else the best
// version overall. Versions arrive best first.
func ChooseVersion(vs []catalog.Version, caps Capabilities) (catalog.Version, Decision) {
	for _, v := range vs {
		if d := Decide(v, caps); d.Kind == DirectPlay {
			return v, d
		}
	}
	return vs[0], Decide(vs[0], caps)
}
