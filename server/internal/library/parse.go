package library

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MediaTags are the quality facts a release name states. Probe results are
// authoritative; these fill in when probing failed and feed version labels.
type MediaTags struct {
	Resolution string
	Source     string
	VideoCodec string
	AudioCodec string
	HDR        []string
	BitDepth   int
	ThreeD     bool
}

// Movie is what a movie's file and folder names say.
type Movie struct {
	Title   string
	Year    int
	Edition string
	MediaTags
}

// maxYear rejects numbers such as the 2049 in "Station 2049" as years.
var maxYear = time.Now().Year() + 1

func isYear(t token) bool {
	if !isYearWord(t.lower) {
		return false
	}
	y, _ := strconv.Atoi(t.lower)
	return y >= 1900 && y <= maxYear
}

// techKind tags never begin a title, so they end it even in first position.
func techKind(k tagKind) bool {
	switch k {
	case tagResolution, tagSource, tagVideoCodec, tagAudio, tagHDR, tagBitDepth:
		return true
	}
	return false
}

var (
	// Usenet posting names prefix the real one: "Some Pack [13+42] - Real.Name".
	postingRe = regexp.MustCompile(`^.*?\[\d+\+\d+\]\s*(?:-\s*)?`)
	// Download clients mark folders they are still working on.
	clientRe = regexp.MustCompile(`(?i)^_(?:unpack|failed)_`)
	// Plex and Jellyfin name editions explicitly: "Movie (1932) {edition-Workprint}".
	editionRe = regexp.MustCompile(`(?i)\{edition-([^}]+)\}`)
)

// cleanName strips wrappers that are not part of any release name.
func cleanName(s string) string {
	s = clientRe.ReplaceAllString(s, "")
	if loc := postingRe.FindStringIndex(s); loc != nil && loc[1] < len(s) {
		s = s[loc[1]:]
	}
	return s
}

// ParseMovieName parses one file name (without extension) or folder name.
func ParseMovieName(name string) Movie {
	name = cleanName(name)
	var explicit string
	if m := editionRe.FindStringSubmatch(name); m != nil {
		explicit = strings.TrimSpace(m[1])
		name = editionRe.ReplaceAllString(name, " ")
	}
	ts := leadingCleanup(tokenize(name))
	end, yearIdx := titleEnd(ts)
	var m Movie
	if yearIdx >= 0 {
		m.Year, _ = strconv.Atoi(ts[yearIdx].lower)
	}
	m.Title = joinTitle(ts[:end])
	m.Edition, m.MediaTags = collectTags(ts[end:])
	if explicit != "" {
		m.Edition = explicit
	}
	return m
}

// titleEnd finds where the title stops: at the release year when one precedes
// every hard tag, otherwise at the first hard tag or trailing run of soft tags.
func titleEnd(ts []token) (end, yearIdx int) {
	n := len(ts)
	hard := n
	for i := 0; i < n; {
		info, w := classifyAt(ts, i)
		if info.hard && (i > 0 || techKind(info.kind)) {
			hard = i
			break
		}
		i += w
	}
	yearIdx = -1
	// A year in brackets is deliberate; prefer it over bare numbers.
	for i := 1; i < hard; i++ {
		if ts[i].group != 0 && isYear(ts[i]) {
			yearIdx = i
		}
	}
	if yearIdx < 0 {
		// The last year before the tags, so "1931 2019" and "2004 A Velloran Odyssey 1968"
		// keep the number that is part of the title.
		for i := 1; i < hard; i++ {
			if isYear(ts[i]) {
				yearIdx = i
			}
		}
	}
	if yearIdx >= 0 {
		return yearIdx, yearIdx
	}
	end = hard
	for i := 1; i < end; i++ {
		if softRun(ts, i, end) {
			end = i
			break
		}
	}
	for i := max(hard, 1); i < n; i++ {
		if isYear(ts[i]) {
			yearIdx = i
			break
		}
	}
	return end, yearIdx
}

// softRun reports whether ts[i:end] is all tags and is followed by a hard tag,
// as in "Quorvex Really 720 x264" or "Movie German DL 1080p".
func softRun(ts []token, i, end int) bool {
	if end >= len(ts) {
		return false
	}
	first := classifyWord(ts[i].lower)
	if first.kind != tagScene && first.kind != tagResolution && first.kind != tagAudio {
		return false
	}
	for j := i; j < end; j++ {
		if classifyWord(ts[j].lower).kind == tagNone && !isYear(ts[j]) && !isDash(ts[j]) {
			return false
		}
	}
	return true
}

// leadingCleanup drops decoration before a title: a [Group] prefix, scene
// "=Group=" banners and disc-size tags such as BD25.
func leadingCleanup(ts []token) []token {
	ts = trimDashes(ts)
	if len(ts) > 1 && ts[0].brack == '[' {
		g := ts[0].group
		i := 0
		for i < len(ts) && ts[i].group == g {
			i++
		}
		if i < len(ts) {
			ts = ts[i:]
		}
	}
	for len(ts) > 0 {
		t := ts[0]
		if i := strings.LastIndexByte(t.text, '='); i >= 0 {
			t.text = t.text[i+1:]
			t.lower = strings.ToLower(t.text)
			if t.text == "" {
				ts = ts[1:]
				continue
			}
			ts = append([]token{t}, ts[1:]...)
		}
		if len(ts) > 1 && discRe.MatchString(ts[0].lower) {
			ts = ts[1:]
			continue
		}
		break
	}
	return ts
}

func collectTags(ts []token) (edition string, m MediaTags) {
	for i := 0; i < len(ts); {
		info, w := classifyAt(ts, i)
		i += w
		switch info.kind {
		case tagResolution:
			if m.Resolution == "" {
				m.Resolution = info.value
			}
		case tagSource:
			switch {
			case info.value == "REMUX":
				m.Source = "REMUX"
			case m.Source == "" || (m.Source == "WEB" && strings.HasPrefix(info.value, "WEB")):
				m.Source = info.value
			}
		case tagVideoCodec:
			if m.VideoCodec == "" {
				m.VideoCodec = info.value
			}
		case tagAudio:
			switch {
			case info.value == "Atmos" && m.AudioCodec != "" && !strings.HasSuffix(m.AudioCodec, "Atmos"):
				m.AudioCodec += " Atmos"
			case m.AudioCodec == "" && info.value != "":
				m.AudioCodec = info.value
			}
		case tagHDR:
			if !contains(m.HDR, info.value) {
				m.HDR = append(m.HDR, info.value)
			}
		case tagBitDepth:
			m.BitDepth, _ = strconv.Atoi(info.value)
		case tagEdition:
			if edition == "" {
				edition = info.value
			}
		case tag3D:
			m.ThreeD = true
		}
	}
	return edition, m
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// ParseMoviePath parses a movie from its path relative to the library root. The
// folder name usually beats the file name, which in scene releases is often an
// abbreviation or a hash. folderHoldsOneTitle is false when the folder holds
// several different films, so the folder name describes none of them.
func ParseMoviePath(rel string, folderHoldsOneTitle bool) Movie {
	dir, file := path.Split(path.Clean("/" + rel))
	base := stripExt(file)
	fileM := ParseMovieName(base)
	folder := titleFolder(strings.TrimSuffix(dir, "/"))
	if folder == "" || !folderHoldsOneTitle {
		return fileM
	}
	folderM := ParseMovieName(folder)
	out := fileM
	switch {
	case folderM.Title == "":
	case fileM.Title == "" || obfuscated(base):
		out.Title, out.Year = folderM.Title, folderM.Year
	case folderM.Year > 0 && allLower(folderM.Title) && !allLower(fileM.Title) && fileM.Year == folderM.Year:
		// A lowercase scene abbreviation in the folder ("x-q.v.v.1999.1080p")
		// loses to a properly cased file name of the same year.
	case folderM.Year > 0:
		out.Title, out.Year = folderM.Title, folderM.Year
	case fileM.Year > 0:
	default:
		out.Title = folderM.Title
	}
	if out.Edition == "" {
		out.Edition = folderM.Edition
	}
	out.MediaTags = mergeTags(fileM.MediaTags, folderM.MediaTags)
	return out
}

func allLower(s string) bool {
	return s != "" && strings.ToLower(s) == s && strings.ToUpper(s) != s
}

func mergeTags(a, b MediaTags) MediaTags {
	if a.Resolution == "" {
		a.Resolution = b.Resolution
	}
	if a.Source == "" || (b.Source == "REMUX" && a.Source == "BluRay") {
		a.Source = b.Source
	}
	if a.VideoCodec == "" {
		a.VideoCodec = b.VideoCodec
	}
	if a.AudioCodec == "" {
		a.AudioCodec = b.AudioCodec
	}
	if len(a.HDR) == 0 {
		a.HDR = b.HDR
	}
	if a.BitDepth == 0 {
		a.BitDepth = b.BitDepth
	}
	a.ThreeD = a.ThreeD || b.ThreeD
	return a
}

var genericFolderRe = regexp.MustCompile(`(?i)^(movies?|films?|videos?|hd|uhd|4k|1080p|720p|2160p|remux|new|complete|misc|other|subs?|subtitles|cd ?\d|dis[ck] ?\d|part ?\d)$`)

// titleFolder returns the nearest folder above a file that can name it,
// skipping generic ones such as "CD1" or "4K", at most two levels up.
func titleFolder(dir string) string {
	dir = strings.Trim(dir, "/")
	for range 3 {
		if dir == "" || dir == "." {
			return ""
		}
		name := path.Base(dir)
		if !genericFolderRe.MatchString(strings.TrimSpace(name)) && seasonFolder(name) < 0 {
			return name
		}
		dir = path.Dir(dir)
	}
	return ""
}

var hashRe = regexp.MustCompile(`^[A-Za-z0-9]{12,}$`)

// obfuscated spots download-client names like "0f3c9a7e5b1d4c2a8e6f9b0d7c5a3e1f"
// or "kQ7xT2mZp9LwR4vNcY8b" that carry no title at all.
func obfuscated(base string) bool {
	if !hashRe.MatchString(base) {
		return false
	}
	letters, digits := 0, 0
	for _, r := range base {
		if r >= '0' && r <= '9' {
			digits++
		} else {
			letters++
		}
	}
	return digits > 0 && letters > 0
}

func stripExt(file string) string {
	if ext := path.Ext(file); ext != "" && len(ext) <= 6 {
		return file[:len(file)-len(ext)]
	}
	return file
}

// versionPrefix splits "Movie (2014) - Director's Cut" into the shared prefix
// and the version label. The suffix counts only when the prefix repeats the
// folder name or the suffix is nothing but tags, so a title such as
// "Quorvex Harbor - Low Tide" is not cut in two.
func versionPrefix(base, folder string) (prefix, label string) {
	nf := NormalizeTitle(folder)
	for i := 0; ; {
		j := strings.Index(base[i:], " - ")
		if j < 0 {
			return base, ""
		}
		at := i + j
		pre, suf := strings.TrimSpace(base[:at]), strings.TrimSpace(base[at+3:])
		if pre != "" && suf != "" && ((nf != "" && NormalizeTitle(pre) == nf) || allTags(suf)) {
			return pre, suf
		}
		i = at + 3
	}
}

func allTags(s string) bool {
	ts := tokenize(s)
	if len(ts) == 0 {
		return false
	}
	for i := 0; i < len(ts); {
		info, w := classifyAt(ts, i)
		if info.kind == tagNone && !isDash(ts[i]) {
			return false
		}
		i += w
	}
	return true
}
