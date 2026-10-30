package playback

import (
	"strings"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
)

func sp(s string) *string { return &s }
func ip(n int64) *int64   { return &n }

// tcl is a plausible 4K Google TV: HEVC Main 10, no AV1, HDR10 and HLG, AC-3 and
// E-AC-3 in hardware, DTS and TrueHD through the FFmpeg extension.
var tcl = Capabilities{
	Containers: []string{"mkv", "mp4", "webm", "ts"},
	VideoCodecs: []VideoCodecCap{
		{Codec: "h264", Profiles: []string{"baseline", "main", "high"}, MaxBitDepth: 8, MaxWidth: 3840, MaxHeight: 2160},
		{Codec: "hevc", Profiles: []string{"main", "main 10"}, MaxBitDepth: 10, MaxWidth: 3840, MaxHeight: 2160},
		{Codec: "vp9", MaxWidth: 3840, MaxHeight: 2160},
	},
	AudioCodecs: []AudioCodecCap{{Codec: "aac"}, {Codec: "ac3"}, {Codec: "eac3"}, {Codec: "dts", Decoder: "ffmpeg_extension"},
		{Codec: "truehd", Decoder: "ffmpeg_extension"}},
	HDRTypes: []string{"hdr10", "hlg"},
}

func version(container, codec, profile string, w, h, depth int64, audio ...string) catalog.Version {
	v := catalog.Version{ProbeStatus: "ok", Container: sp(container), VideoCodec: sp(codec), VideoProfile: sp(profile),
		Width: ip(w), Height: ip(h), BitDepth: ip(depth), Resolution: sp("1080p")}
	for _, a := range audio {
		v.AudioTracks = append(v.AudioTracks, catalog.Track{Kind: "audio", Codec: sp(a)})
	}
	return v
}

func TestDecide(t *testing.T) {
	dv := func(compat int64) catalog.Version {
		v := version("mkv", "hevc", "main 10", 3840, 2160, 10, "truehd")
		v.HDRType, v.DVCompatID = sp("dolby_vision"), ip(compat)
		return v
	}
	hdr10 := version("mkv", "hevc", "main 10", 3840, 1600, 10, "eac3")
	hdr10.HDRType = sp("hdr10")
	cases := []struct {
		name   string
		v      catalog.Version
		caps   Capabilities
		want   string
		reason string
	}{
		{"1080p h264 mkv", version("mkv", "h264", "high", 1920, 1080, 8, "ac3"), tcl, DirectPlay, "plays mkv"},
		{"4K hevc 10-bit", version("mkv", "hevc", "main 10", 3840, 2160, 10, "eac3"), tcl, DirectPlay, ""},
		{"truehd via extension", version("mkv", "hevc", "main 10", 3840, 2160, 10, "truehd"), tcl, DirectPlay, ""},
		{"only an unsupported audio track", version("mkv", "h264", "high", 1920, 1080, 8, "mlp"), tcl, AudioTranscode, "audio"},
		{"player picks the decodable track", version("mkv", "h264", "high", 1920, 1080, 8, "mlp", "ac3"), tcl, DirectPlay, ""},
		{"avi container", version("avi", "h264", "high", 1280, 720, 8, "mp3"), Capabilities{Containers: []string{"mkv"},
			VideoCodecs: tcl.VideoCodecs, AudioCodecs: append(tcl.AudioCodecs, AudioCodecCap{Codec: "mp3"})}, Remux, "container"},
		{"av1 without a decoder", version("mkv", "av1", "main", 3840, 2160, 10, "eac3"), tcl, Transcode, "no av1"},
		{"h264 10-bit", version("mkv", "h264", "high 10", 1920, 1080, 10, "aac"), tcl, Transcode, "profile"},
		{"too large", version("mkv", "hevc", "main", 7680, 4320, 8, "aac"), tcl, Transcode, "exceeds"},
		{"hdr10 on an hdr10 display", hdr10, tcl, DirectPlay, ""},
		{"dv profile 8.1 falls back to hdr10", dv(1), tcl, DirectPlay, "HDR10 base layer"},
		{"dv profile 5 needs dv", dv(0), tcl, Transcode, "Dolby Vision"},
		{"unprobed file", catalog.Version{ProbeStatus: "error"}, tcl, DirectPlay, "not been inspected"},
	}
	for _, c := range cases {
		d := Decide(c.v, c.caps)
		if d.Kind != c.want {
			t.Errorf("%s: kind = %s (%s), want %s", c.name, d.Kind, d.Reason, c.want)
		}
		if c.reason != "" && !strings.Contains(d.Reason, c.reason) {
			t.Errorf("%s: reason %q lacks %q", c.name, d.Reason, c.reason)
		}
		if d.Reason == "" {
			t.Errorf("%s: empty reason", c.name)
		}
	}
}

func TestHDROnSDRDisplayWarns(t *testing.T) {
	v := version("mkv", "hevc", "main 10", 3840, 2160, 10, "eac3")
	v.HDRType = sp("hdr10")
	sdr := tcl
	sdr.HDRTypes = nil
	d := Decide(v, sdr)
	if d.Kind != DirectPlay || len(d.Warnings) == 0 || !strings.Contains(d.Reason, "washed out") {
		t.Fatalf("got %+v", d)
	}
}

func TestChooseVersionPrefersDirectPlay(t *testing.T) {
	av1 := version("mkv", "av1", "main", 3840, 2160, 10, "eac3")
	av1.ID = 1
	h264 := version("mkv", "h264", "high", 1920, 1080, 8, "ac3")
	h264.ID = 2
	v, d := ChooseVersion([]catalog.Version{av1, h264}, tcl)
	if v.ID != 2 || d.Kind != DirectPlay {
		t.Fatalf("chose %d (%s)", v.ID, d.Kind)
	}
	v, d = ChooseVersion([]catalog.Version{av1}, tcl)
	if v.ID != 1 || d.Kind != Transcode {
		t.Fatalf("only version: chose %d (%s)", v.ID, d.Kind)
	}
}
