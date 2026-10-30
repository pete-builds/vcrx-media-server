package library

import (
	"path"
	"strings"
)

// Library kinds, matching the libraries.kind column.
const (
	KindMovie = "movie"
	KindShow  = "show"
)

var videoExts = map[string]bool{
	"mkv": true, "mp4": true, "m4v": true, "avi": true, "mov": true, "ts": true, "m2ts": true, "mts": true,
	"webm": true, "wmv": true, "mpg": true, "mpeg": true, "flv": true, "ogv": true,
}

// IsVideo reports whether a file name has a playable video extension. Disc
// images (.iso) are not playable and are left out.
func IsVideo(name string) bool {
	return videoExts[strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))]
}

// junkDirs are system and NAS folders that never hold media.
var junkDirs = map[string]bool{
	"@eadir": true, "#recycle": true, "#snapshot": true, "lost+found": true, "$recycle.bin": true,
	"system volume information": true, "@recycle": true, "@tmp": true,
	// Blu-ray and DVD folder structures play only as discs, not as loose files.
	"bdmv": true, "video_ts": true, "audio_ts": true, "certificate": true,
}

// extrasDirs hold bonus material that is not a title of its own. "Specials" is
// an extras folder for movies but season 0 for shows.
var extrasDirs = map[string]bool{
	"extras": true, "featurettes": true, "behind the scenes": true, "deleted scenes": true,
	"interviews": true, "trailers": true, "shorts": true,
}

// SkipDir reports why a folder is not walked, or "" to walk it.
func SkipDir(name, kind string) string {
	l := strings.ToLower(strings.TrimSpace(name))
	norm := strings.Join(strings.FieldsFunc(l, func(r rune) bool { return r == '.' || r == '_' || r == ' ' || r == '-' }), " ")
	switch {
	case strings.HasPrefix(name, "."):
		return "hidden"
	case junkDirs[l]:
		return "system"
	case norm == "sample" || norm == "samples":
		return "sample"
	case extrasDirs[norm]:
		return "extras"
	case kind == KindMovie && (norm == "specials" || norm == "special features"):
		return "extras"
	}
	return ""
}

// extraSuffixes mark bonus files by name, the Plex and Kodi convention.
var extraSuffixes = []string{"-trailer", "-featurette", "-behindthescenes", "-deleted", "-deletedscene", "-interview"}

// SkipFile reports why a file is not indexed, or "" to index it. size is the
// file size in bytes; it settles whether "sample" is a marker or a title word.
func SkipFile(name string, size int64) string {
	l := strings.ToLower(name)
	switch {
	case strings.HasPrefix(name, "._"):
		return "appledouble"
	case strings.HasPrefix(name, "."):
		return "hidden"
	case !IsVideo(name):
		return "not video"
	case IsSample(name, size):
		return "sample"
	}
	base := stripExt(l)
	for _, s := range extraSuffixes {
		if strings.HasSuffix(base, s) {
			return "extras"
		}
	}
	if base == "trailer" {
		return "extras"
	}
	return ""
}

// sampleTitleMax is the size above which a file whose title itself contains
// the word "sample" is taken to be a real film rather than a preview clip.
const sampleTitleMax = 1 << 30

// IsSample reports whether a file is a scene preview clip. The word "sample"
// must stand alone ("x.sample.mkv", "sample-x.mkv", "x-Sample.mp4"), so
// "Samples of Quorvex" is safe; and when the word sits inside the parsed title,
// as in a film called "The Sample", only a small file counts.
func IsSample(name string, size int64) bool {
	base := stripExt(name)
	words := strings.FieldsFunc(strings.ToLower(base), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	found := false
	for _, w := range words {
		if w == "sample" {
			found = true
		}
	}
	if !found {
		return false
	}
	title := strings.Fields(NormalizeTitle(ParseMovieName(base).Title))
	if len(title) == 1 && title[0] == "sample" {
		return true
	}
	inTitle := false
	for _, w := range title {
		if w == "sample" {
			inTitle = true
		}
	}
	return !inTitle || size < sampleTitleMax
}
