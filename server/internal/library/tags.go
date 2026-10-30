package library

import (
	"regexp"
	"strings"
)

type tagKind int

const (
	tagNone tagKind = iota
	tagResolution
	tagSource
	tagVideoCodec
	tagAudio
	tagHDR
	tagBitDepth
	tagEdition
	tag3D
	tagScene
)

// tagInfo classifies one word. A hard tag ends a title wherever it appears; a
// soft one ("WEB", "German", "DV", a bare "720") only counts once the title has
// already ended, because it is also an ordinary word: Orvel's Web, A German
// Quill, Quorvex Rising 3D.
type tagInfo struct {
	kind  tagKind
	value string
	hard  bool
}

var (
	resRe      = regexp.MustCompile(`^m?(480|576|720|1080|2160|4320)[pi]$`)
	dimsRe     = regexp.MustCompile(`^(\d{3,4})x(\d{3,4})$`)
	bareResRe  = regexp.MustCompile(`^(480|576|720|1080|2160)$`)
	audioRe    = regexp.MustCompile(`^(ddp|dd\+|dd|eac3|e-ac3|ac3|aac|flac|opus|lpcm|pcm|mp3|truehd|dts)(\d(\.\d)?)?$`)
	channelsRe = regexp.MustCompile(`^\dch$`)
	layoutRe   = regexp.MustCompile(`^\d\.\d$`)
	subsRe     = regexp.MustCompile(`^[a-z]{2,}subs?$`)
	discRe     = regexp.MustCompile(`^(bd25|bd50|bd66|bd100|dvd5|dvd9)$`)
	cdRe       = regexp.MustCompile(`^\d?cd\d?$`)
)

var resolutionValue = map[string]string{"480": "480p", "576": "576p", "720": "720p", "1080": "1080p", "2160": "4K", "4320": "8K"}

var hardWords = map[string]tagInfo{
	"4k": {tagResolution, "4K", true}, "uhd": {tagResolution, "4K", true}, "8k": {tagResolution, "8K", true},

	"bluray": {tagSource, "BluRay", true}, "blu-ray": {tagSource, "BluRay", true}, "bdrip": {tagSource, "BluRay", true},
	"brrip": {tagSource, "BluRay", true}, "bd-rip": {tagSource, "BluRay", true}, "br-rip": {tagSource, "BluRay", true},
	"bdremux": {tagSource, "REMUX", true}, "remux": {tagSource, "REMUX", true},
	"web-dl": {tagSource, "WEB-DL", true}, "webdl": {tagSource, "WEB-DL", true}, "web-dlx": {tagSource, "WEB-DL", true},
	"webmux": {tagSource, "WEB-DL", true}, "webrip": {tagSource, "WEBRip", true}, "web-rip": {tagSource, "WEBRip", true},
	"hdtv": {tagSource, "HDTV", true}, "pdtv": {tagSource, "HDTV", true}, "sdtv": {tagSource, "HDTV", true},
	"hdtvrip": {tagSource, "HDTV", true}, "tvrip": {tagSource, "HDTV", true},
	"dvdrip": {tagSource, "DVDRip", true}, "dvd-rip": {tagSource, "DVDRip", true},
	"dvdscr": {tagSource, "DVDScr", true}, "dvdscreener": {tagSource, "DVDScr", true}, "dvdr": {tagSource, "DVD", true},
	"hdrip": {tagSource, "HDRip", true}, "hdts": {tagSource, "TS", true}, "telesync": {tagSource, "TS", true},
	"hdtc": {tagSource, "TC", true}, "telecine": {tagSource, "TC", true}, "hdcam": {tagSource, "CAM", true},
	"camrip": {tagSource, "CAM", true}, "ppvrip": {tagSource, "PPV", true}, "vodrip": {tagSource, "VOD", true},
	"hddvd": {tagSource, "HD-DVD", true}, "hd-dvd": {tagSource, "HD-DVD", true},

	"x264": {tagVideoCodec, "H.264", true}, "h264": {tagVideoCodec, "H.264", true}, "h.264": {tagVideoCodec, "H.264", true},
	"avc": {tagVideoCodec, "H.264", true}, "x265": {tagVideoCodec, "HEVC", true}, "h265": {tagVideoCodec, "HEVC", true},
	"h.265": {tagVideoCodec, "HEVC", true}, "hevc": {tagVideoCodec, "HEVC", true}, "xvid": {tagVideoCodec, "XviD", true},
	"xvidhd": {tagVideoCodec, "XviD", true}, "divx": {tagVideoCodec, "DivX", true}, "av1": {tagVideoCodec, "AV1", true},
	"vp9": {tagVideoCodec, "VP9", true}, "mpeg2": {tagVideoCodec, "MPEG-2", true}, "vc-1": {tagVideoCodec, "VC-1", true},
	"vc1": {tagVideoCodec, "VC-1", true},

	"dts-hd": {tagAudio, "DTS-HD", true}, "dtshd": {tagAudio, "DTS-HD", true}, "dts-hdma": {tagAudio, "DTS-HD MA", true},
	"dts-x": {tagAudio, "DTS:X", true}, "dtsx": {tagAudio, "DTS:X", true}, "atmos": {tagAudio, "Atmos", true},
	"dd+": {tagAudio, "DD+", true},

	"hdr": {tagHDR, "HDR", true}, "hdr10": {tagHDR, "HDR", true}, "hdr10+": {tagHDR, "HDR10+", true},
	"hdr10plus": {tagHDR, "HDR10+", true}, "dovi": {tagHDR, "DV", true}, "dolbyvision": {tagHDR, "DV", true},
	"hlg": {tagHDR, "HLG", true}, "sdr": {tagScene, "", true},
	"10bit": {tagBitDepth, "10", true}, "10-bit": {tagBitDepth, "10", true}, "10bits": {tagBitDepth, "10", true},
	"12bit": {tagBitDepth, "12", true}, "8bit": {tagBitDepth, "8", true},

	"extended": {tagEdition, "Extended", true}, "unrated": {tagEdition, "Unrated", true},
	"remastered": {tagEdition, "Remastered", true}, "criterion": {tagEdition, "Criterion", true},
	"imax": {tagEdition, "IMAX", true}, "theatrical": {tagEdition, "Theatrical", true},

	"hsbs": {tag3D, "3D", true}, "h-sbs": {tag3D, "3D", true}, "half-sbs": {tag3D, "3D", true},
	"hou": {tag3D, "3D", true}, "h-ou": {tag3D, "3D", true}, "half-ou": {tag3D, "3D", true}, "3d-sbs": {tag3D, "3D", true},

	"proper": {tagScene, "", true}, "repack": {tagScene, "", true}, "rerip": {tagScene, "", true},
	"limited": {tagScene, "", true}, "internal": {tagScene, "", true}, "readnfo": {tagScene, "", true},
	"nfofix": {tagScene, "", true}, "dirfix": {tagScene, "", true}, "multi": {tagScene, "", true},
	"dual": {tagScene, "", true}, "docu": {tagScene, "", true}, "subbed": {tagScene, "", true},
	"dubbed": {tagScene, "", true}, "nordic": {tagScene, "", true}, "hc": {tagScene, "", true},
}

var softWords = map[string]tagInfo{
	"web": {tagSource, "WEB", false}, "bd": {tagSource, "BluRay", false}, "br": {tagSource, "BluRay", false},
	"dvd": {tagSource, "DVD", false}, "scr": {tagSource, "DVDScr", false}, "screener": {tagSource, "DVDScr", false},
	"ts": {tagSource, "TS", false}, "tc": {tagSource, "TC", false}, "cam": {tagSource, "CAM", false},
	"dv": {tagHDR, "DV", false}, "3d": {tag3D, "3D", false}, "sbs": {tag3D, "3D", false}, "mvc": {tag3D, "3D", false},
	"ma": {tagAudio, "", false}, "hq": {tagScene, "", false}, "complete": {tagScene, "", false},
	"ws": {tagScene, "", false}, "ntsc": {tagScene, "", false}, "pal": {tagScene, "", false},
	"mkv": {tagScene, "", false}, "mp4": {tagScene, "", false},
	"amzn": {tagScene, "", false}, "nf": {tagScene, "", false}, "dsnp": {tagScene, "", false}, "hmax": {tagScene, "", false},
	"atvp": {tagScene, "", false}, "hulu": {tagScene, "", false}, "pcok": {tagScene, "", false}, "pmtp": {tagScene, "", false},
	"dl": {tagScene, "", false}, "english": {tagScene, "", false}, "german": {tagScene, "", false},
	"french": {tagScene, "", false}, "truefrench": {tagScene, "", false}, "italian": {tagScene, "", false},
	"spanish": {tagScene, "", false}, "dutch": {tagScene, "", false}, "ita": {tagScene, "", false},
	"eng": {tagScene, "", false}, "nl": {tagScene, "", false}, "vff": {tagScene, "", false}, "vfi": {tagScene, "", false},
	"vostfr": {tagScene, "", false}, "latino": {tagScene, "", false}, "castellano": {tagScene, "", false},
	"japeng": {tagScene, "", false},
}

// classifyWord looks up one lower-cased word.
func classifyWord(w string) tagInfo {
	if t, ok := hardWords[w]; ok {
		return t
	}
	if m := resRe.FindStringSubmatch(w); m != nil {
		return tagInfo{tagResolution, resolutionValue[m[1]], true}
	}
	if m := dimsRe.FindStringSubmatch(w); m != nil {
		return tagInfo{tagResolution, dimsResolution(m[1], m[2]), true}
	}
	if m := audioRe.FindStringSubmatch(w); m != nil {
		return tagInfo{tagAudio, audioValue(m[1]), true}
	}
	if channelsRe.MatchString(w) {
		return tagInfo{tagAudio, "", true}
	}
	if subsRe.MatchString(w) {
		return tagInfo{tagScene, "", true}
	}
	if discRe.MatchString(w) {
		return tagInfo{tagSource, "BluRay", true}
	}
	if t, ok := softWords[w]; ok {
		return t
	}
	if layoutRe.MatchString(w) {
		return tagInfo{tagAudio, "", false}
	}
	if m := bareResRe.FindStringSubmatch(w); m != nil {
		return tagInfo{tagResolution, resolutionValue[m[1]], false}
	}
	if cdRe.MatchString(w) && strings.Contains(w, "cd") && len(w) > 2 {
		return tagInfo{tagScene, "", false}
	}
	return tagInfo{}
}

// classifyAt also recognizes tags written as two words, such as "Director's
// Cut", "WEB DL", "H 264" or "Dolby Vision", and reports how many tokens matched.
func classifyAt(ts []token, i int) (tagInfo, int) {
	w := ts[i].lower
	next := ""
	if i+1 < len(ts) {
		next = ts[i+1].lower
	}
	pair := w + " " + next
	// "H 264-GRP" keeps the group glued to the number.
	nextNum, _, _ := strings.Cut(next, "-")
	switch {
	case next == "cut" && (w == "director's" || w == "directors" || w == "director’s"):
		return tagInfo{tagEdition, "Director's Cut", true}, 2
	case (next == "cut" || next == "edition") && (w == "collector's" || w == "collectors" || w == "collector’s"):
		return tagInfo{tagEdition, "Collector's Cut", true}, 2
	case (next == "cut" || next == "edition" || next == "version") && (w == "theatrical" || w == "extended" || w == "imax"):
		return classifyWord(w), 2
	case w == "criterion" && next == "collection":
		return classifyWord(w), 2
	case pair == "web dl" || pair == "web rip":
		if next == "dl" {
			return tagInfo{tagSource, "WEB-DL", true}, 2
		}
		return tagInfo{tagSource, "WEBRip", true}, 2
	case pair == "blu ray":
		return tagInfo{tagSource, "BluRay", true}, 2
	case pair == "dolby vision":
		return tagInfo{tagHDR, "DV", true}, 2
	case pair == "dts hd":
		return tagInfo{tagAudio, "DTS-HD", true}, 2
	case (w == "h" || w == "x") && (nextNum == "264" || nextNum == "265"):
		v := "H.264"
		if nextNum == "265" {
			v = "HEVC"
		}
		return tagInfo{tagVideoCodec, v, true}, 2
	}
	return classifyWord(w), 1
}

func dimsResolution(w, h string) string {
	switch {
	case w >= "3800" && len(w) == 4:
		return "4K"
	case w == "1920" || h == "1080":
		return "1080p"
	case w == "1280" || h == "720":
		return "720p"
	}
	return ""
}

func audioValue(codec string) string {
	switch codec {
	case "ddp", "dd+", "eac3", "e-ac3":
		return "DD+"
	case "dd", "ac3":
		return "DD"
	case "aac":
		return "AAC"
	case "truehd":
		return "TrueHD"
	case "dts":
		return "DTS"
	case "flac":
		return "FLAC"
	case "opus":
		return "Opus"
	case "mp3":
		return "MP3"
	}
	return "PCM"
}
