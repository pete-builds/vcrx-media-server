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

// Kinds of extra, matching the item_extras.kind column. A show's theme.mp3 is
// ExtraTheme; it plays behind the details screen rather than in the Extras row.
const (
	ExtraTrailer         = "trailer"
	ExtraFeaturette      = "featurette"
	ExtraBehindTheScenes = "behind_the_scenes"
	ExtraDeletedScene    = "deleted_scene"
	ExtraInterview       = "interview"
	ExtraScene           = "scene"
	ExtraShort           = "short"
	ExtraOther           = "other"
	ExtraTheme           = "theme"
)

// extrasDirs hold bonus material that is not a title of its own, by the kind of
// extra their files are. "Specials" is an extras folder for movies but season 0
// for shows.
var extrasDirs = map[string]string{
	"extras": ExtraOther, "featurettes": ExtraFeaturette, "behind the scenes": ExtraBehindTheScenes,
	"deleted scenes": ExtraDeletedScene, "interviews": ExtraInterview, "trailers": ExtraTrailer, "shorts": ExtraShort,
}

// nestedExtrasDirs are extras folders only inside a title's folder: at the top
// of a library, "Other" or "Scenes" could as well be a folder of films.
var nestedExtrasDirs = map[string]string{"scenes": ExtraScene, "other": ExtraOther}

func normDir(name string) string {
	l := strings.ToLower(strings.TrimSpace(name))
	return strings.Join(strings.FieldsFunc(l, func(r rune) bool { return r == '.' || r == '_' || r == ' ' || r == '-' }), " ")
}

// SkipDir reports why a folder at the top of a library is not walked, or "" to
// walk it.
func SkipDir(name, kind string) string { return skipDirAt(name, kind, false) }

// skipDirAt is SkipDir for a folder that is nested (inside another folder of
// the library) or not.
func skipDirAt(name, kind string, nested bool) string {
	l := strings.ToLower(strings.TrimSpace(name))
	norm := normDir(name)
	switch {
	case strings.HasPrefix(name, "."):
		return "hidden"
	case junkDirs[l]:
		return "system"
	case norm == "sample" || norm == "samples":
		return "sample"
	case ExtrasDirKind(name, kind, nested) != "":
		return "extras"
	}
	return ""
}

// ExtrasDirKind is the kind of extra a folder's files are, or "" when it is not
// an extras folder.
func ExtrasDirKind(name, kind string, nested bool) string {
	norm := normDir(name)
	if k := extrasDirs[norm]; k != "" {
		return k
	}
	if k := nestedExtrasDirs[norm]; k != "" && nested {
		return k
	}
	if kind == KindMovie && (norm == "specials" || norm == "special features") {
		return ExtraFeaturette
	}
	return ""
}

// extraSuffixes mark bonus files by name, the Plex and Kodi convention.
var extraSuffixes = []string{"-trailer", "-featurette", "-behindthescenes", "-deleted", "-deletedscene", "-interview"}

var suffixKinds = map[string]string{
	"-trailer": ExtraTrailer, "-featurette": ExtraFeaturette, "-behindthescenes": ExtraBehindTheScenes,
	"-deleted": ExtraDeletedScene, "-deletedscene": ExtraDeletedScene, "-interview": ExtraInterview,
}

// ExtraFileKind reports the kind of extra a file's name marks it as, and the
// name before the marker ("Some Film (2009)" for "Some Film (2009)-trailer.mkv",
// "" for a bare trailer.mp4). ok is false for anything else.
func ExtraFileKind(name string) (kind, owner string, ok bool) {
	base := stripExt(name)
	l := strings.ToLower(base)
	if l == "trailer" {
		return ExtraTrailer, "", true
	}
	for _, s := range extraSuffixes {
		if strings.HasSuffix(l, s) {
			return suffixKinds[s], strings.TrimSpace(base[:len(base)-len(s)]), true
		}
	}
	return "", "", false
}

// SkipFile reports why a file is not indexed, or "" to index it. size is the
// file size in bytes; it settles whether "sample" is a marker or a title word.
func SkipFile(name string, size int64) string {
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
	if _, _, ok := ExtraFileKind(name); ok {
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
