package probe

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
)

type ffOutput struct {
	Streams []ffStream `json:"streams"`
	Format  ffFormat   `json:"format"`
}

type ffFormat struct {
	FormatName string            `json:"format_name"`
	Duration   string            `json:"duration"`
	BitRate    string            `json:"bit_rate"`
	Size       string            `json:"size"`
	Tags       map[string]string `json:"tags"`
}

type ffStream struct {
	Index            int               `json:"index"`
	CodecName        string            `json:"codec_name"`
	CodecType        string            `json:"codec_type"`
	Profile          string            `json:"profile"`
	Level            int               `json:"level"`
	Width            int               `json:"width"`
	Height           int               `json:"height"`
	PixFmt           string            `json:"pix_fmt"`
	BitsPerRawSample string            `json:"bits_per_raw_sample"`
	BitsPerSample    int               `json:"bits_per_sample"`
	ColorTransfer    string            `json:"color_transfer"`
	AvgFrameRate     string            `json:"avg_frame_rate"`
	RFrameRate       string            `json:"r_frame_rate"`
	SampleRate       string            `json:"sample_rate"`
	Channels         int               `json:"channels"`
	ChannelLayout    string            `json:"channel_layout"`
	BitRate          string            `json:"bit_rate"`
	Duration         string            `json:"duration"`
	Disposition      map[string]int    `json:"disposition"`
	Tags             map[string]string `json:"tags"`
	SideData         []ffSideData      `json:"side_data_list"`
}

type ffSideData struct {
	Type       string `json:"side_data_type"`
	DVProfile  int    `json:"dv_profile"`
	DVCompatID int    `json:"dv_bl_signal_compatibility_id"`
}

// Parse turns ffprobe's JSON into a Result. path is used only for its
// extension, which settles mkv against webm and mp4 against mov.
func Parse(data []byte, path string) (*Result, error) {
	var out ffOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("ffprobe json: %w", err)
	}
	r := &Result{
		FormatName: out.Format.FormatName,
		Container:  Container(out.Format.FormatName, path),
		DurationMs: secondsToMs(out.Format.Duration),
		Bitrate:    atoi64(out.Format.BitRate),
	}
	var primary *ffStream
	for i := range out.Streams {
		s := &out.Streams[i]
		st, ok := convertStream(s)
		if !ok {
			continue
		}
		r.Streams = append(r.Streams, st)
		if st.Kind == KindVideo && (primary == nil || (s.Disposition["default"] == 1 && primary.Disposition["default"] != 1)) {
			primary = s
		}
	}
	if primary == nil {
		return nil, ErrNoVideo
	}
	if r.DurationMs == 0 {
		r.DurationMs = streamDurationMs(primary)
	}
	if r.Bitrate == 0 && r.DurationMs > 0 {
		if size := atoi64(out.Format.Size); size > 0 {
			r.Bitrate = size * 8 * 1000 / r.DurationMs
		}
	}
	r.VideoCodec = primary.CodecName
	r.VideoProfile = strings.ToLower(primary.Profile)
	r.VideoLevel = max(primary.Level, 0)
	r.BitDepth = bitDepth(primary)
	r.Width, r.Height = primary.Width, primary.Height
	r.FrameRate = frameRate(primary)
	r.Resolution = Bucket(primary.Width, primary.Height)
	r.HDRType, r.DVProfile, r.DVCompatID = hdr(primary)
	return r, nil
}

func convertStream(s *ffStream) (Stream, bool) {
	switch s.CodecType {
	case KindVideo, KindAudio, KindSubtitle:
	default:
		return Stream{}, false
	}
	// Cover art rides along as a one-frame "video" stream; it is not a picture
	// anyone can play.
	if s.CodecType == KindVideo && s.Disposition["attached_pic"] == 1 {
		return Stream{}, false
	}
	st := Stream{
		Index:           s.Index,
		Kind:            s.CodecType,
		Codec:           s.CodecName,
		Profile:         strings.ToLower(s.Profile),
		Language:        tag(s.Tags, "language"),
		Title:           tag(s.Tags, "title"),
		Bitrate:         atoi64(s.BitRate),
		Default:         s.Disposition["default"] == 1,
		Forced:          s.Disposition["forced"] == 1,
		HearingImpaired: s.Disposition["hearing_impaired"] == 1 || s.Disposition["captions"] == 1,
	}
	if st.Language == "und" {
		st.Language = ""
	}
	switch s.CodecType {
	case KindVideo:
		// ffprobe reports -99 when the codec has no level.
		st.Level = max(s.Level, 0)
		st.BitDepth = bitDepth(s)
		st.Width, st.Height = s.Width, s.Height
		st.FrameRate = frameRate(s)
	case KindAudio:
		st.Channels = s.Channels
		st.ChannelLayout = s.ChannelLayout
		st.SampleRate = int(atoi64(s.SampleRate))
	case KindSubtitle:
		st.ImageBased = IsImageSubtitle(s.CodecName)
	}
	return st, true
}

// IsImageSubtitle reports whether a subtitle codec carries bitmaps, which a
// client cannot restyle and which need burn-in when it cannot render them.
func IsImageSubtitle(codec string) bool {
	switch codec {
	case "hdmv_pgs_subtitle", "dvd_subtitle", "dvb_subtitle", "xsub":
		return true
	}
	return false
}

// Container normalizes ffprobe's format_name. Matroska and WebM share a demuxer,
// as do MP4 and QuickTime, so the extension decides between those pairs.
func Container(formatName, path string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	names := strings.Split(formatName, ",")
	has := func(n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
	switch {
	case has("matroska") || has("webm"):
		if ext == "webm" || (!has("matroska") && has("webm")) {
			return "webm"
		}
		return "mkv"
	case has("mov") || has("mp4"):
		if ext == "mov" || (!has("mp4") && has("mov")) {
			return "mov"
		}
		return "mp4"
	case has("mpegts"):
		if ext == "m2ts" || ext == "mts" {
			return "m2ts"
		}
		return "ts"
	case has("avi"):
		return "avi"
	case has("asf"):
		return "wmv"
	case has("flv"):
		return "flv"
	case has("mpeg") || has("mpegvideo") || has("vob"):
		return "mpeg"
	case has("ogg"):
		return "ogg"
	}
	if len(names) > 0 && names[0] != "" {
		return names[0]
	}
	return ext
}

// Bucket maps a frame size to a resolution class. Width decides first, because
// scope films are cropped: a 4K 2.39:1 encode is 3840x1600 and a 1080p one is
// 1920x800.
func Bucket(width, height int) string {
	switch {
	case width <= 0 && height <= 0:
		return ""
	case width >= 6000:
		return "8K"
	case width >= 3200:
		return "4K"
	case width >= 1800 || height >= 1000:
		return "1080p"
	case width >= 1200 || height >= 700:
		return "720p"
	case height >= 560:
		return "576p"
	case height >= 470:
		return "480p"
	}
	return "SD"
}

func hdr(s *ffStream) (kind string, dvProfile, dvCompat int) {
	for _, sd := range s.SideData {
		if strings.EqualFold(sd.Type, "DOVI configuration record") {
			return DolbyVision, sd.DVProfile, sd.DVCompatID
		}
	}
	for _, sd := range s.SideData {
		t := strings.ToLower(sd.Type)
		if strings.Contains(t, "2094-40") || strings.Contains(t, "hdr10+") {
			return HDR10Plus, 0, 0
		}
	}
	switch s.ColorTransfer {
	case "smpte2084":
		return HDR10, 0, 0
	case "arib-std-b67":
		return HLG, 0, 0
	}
	return "", 0, 0
}

func bitDepth(s *ffStream) int {
	if n := atoi64(s.BitsPerRawSample); n > 0 && n <= 16 {
		return int(n)
	}
	return pixFmtDepth(s.PixFmt)
}

// pixFmtDepth reads the bit depth from names like yuv420p10le, p010le or gray12be.
func pixFmtDepth(pf string) int {
	if pf == "" {
		return 0
	}
	p := strings.TrimSuffix(strings.TrimSuffix(pf, "le"), "be")
	if strings.HasPrefix(p, "p0") || strings.HasPrefix(p, "p2") || strings.HasPrefix(p, "p4") {
		// p010, p016, p210, p410: semi-planar high-depth formats.
		if n, err := strconv.Atoi(p[2:]); err == nil && n >= 8 && n <= 16 {
			return n
		}
	}
	i := len(p)
	for i > 0 && p[i-1] >= '0' && p[i-1] <= '9' {
		i--
	}
	if i < len(p) && i > 0 && (p[i-1] == 'p' || strings.HasSuffix(p[:i], "gray")) {
		if n, err := strconv.Atoi(p[i:]); err == nil {
			switch n {
			case 9, 10, 12, 14, 16:
				return n
			}
		}
	}
	return 8
}

func frameRate(s *ffStream) float64 {
	for _, fr := range []string{s.AvgFrameRate, s.RFrameRate} {
		num, den, ok := strings.Cut(fr, "/")
		if !ok {
			continue
		}
		n, err1 := strconv.ParseFloat(num, 64)
		d, err2 := strconv.ParseFloat(den, 64)
		if err1 != nil || err2 != nil || n <= 0 || d <= 0 {
			continue
		}
		return math.Round(n/d*1000) / 1000
	}
	return 0
}

func streamDurationMs(s *ffStream) int64 {
	if ms := secondsToMs(s.Duration); ms > 0 {
		return ms
	}
	// Matroska stores per-track duration only as a tag, e.g. 00:42:10.512000000.
	d := tag(s.Tags, "duration")
	h, rest, ok := strings.Cut(d, ":")
	if !ok {
		return 0
	}
	m, sec, ok := strings.Cut(rest, ":")
	if !ok {
		return 0
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	ss, err3 := strconv.ParseFloat(sec, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return 0
	}
	return int64(hh)*3_600_000 + int64(mm)*60_000 + int64(math.Round(ss*1000))
}

func secondsToMs(s string) int64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f <= 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return int64(math.Round(f * 1000))
}

func atoi64(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// tag reads a stream tag case-insensitively: Matroska writes DURATION and
// language, MP4 writes handler_name and language, and muxers disagree on case.
func tag(tags map[string]string, key string) string {
	if v, ok := tags[key]; ok {
		return strings.TrimSpace(v)
	}
	for k, v := range tags {
		if strings.EqualFold(k, key) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
