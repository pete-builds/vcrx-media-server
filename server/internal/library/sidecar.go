package library

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// subtitleCodecs maps sidecar extensions to the codec names ffprobe uses, so
// clients see one vocabulary for embedded and external tracks.
var subtitleCodecs = map[string]string{
	"srt": "subrip", "ass": "ass", "ssa": "ass", "vtt": "webvtt", "sup": "hdmv_pgs_subtitle", "idx": "dvd_subtitle",
}

var subsDirs = map[string]bool{"subs": true, "sub": true, "subtitles": true, "subtitle": true}

// Sidecar is one subtitle file beside a video.
type Sidecar struct {
	Path            string
	Codec           string
	Language        string
	Title           string
	Default         bool
	Forced          bool
	HearingImpaired bool
	ImageBased      bool
}

// dirEntry is one name in a folder listing; symlinks that leave the root have
// already been dropped.
type dirEntry struct {
	name  string
	isDir bool
}

type listing struct {
	rel     string
	entries []dirEntry
}

func (l *listing) files() []string {
	if l == nil {
		return nil
	}
	var out []string
	for _, e := range l.entries {
		if !e.isDir {
			out = append(out, e.name)
		}
	}
	return out
}

func (l *listing) subdir(match func(string) bool) []string {
	if l == nil {
		return nil
	}
	var out []string
	for _, e := range l.entries {
		if e.isDir && match(e.name) {
			out = append(out, e.name)
		}
	}
	return out
}

// lookup finds a file in a listing case-insensitively.
func (l *listing) lookup(name string) string {
	if l == nil {
		return ""
	}
	for _, e := range l.entries {
		if !e.isDir && strings.EqualFold(e.name, name) {
			return e.name
		}
	}
	return ""
}

func joinRel(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return dir + "/" + name
}

// findSubtitles lists the sidecar subtitles of one video: files in its folder
// named after it ("Movie.en.forced.srt"), the same inside a Subs folder, a
// Subs/<video name>/ folder, and any subtitle in Subs when the video is the
// only one in its folder. siblings are the base names of the folder's videos;
// a file matching several goes to the longest, so "Movie.Extended.en.srt"
// belongs to "Movie.Extended" and not to "Movie". dirOf returns a listing.
func findSubtitles(rel string, siblings []string, dirOf func(string) *listing) []Sidecar {
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	base := stripExt(path.Base(rel))
	lb := strings.ToLower(base)
	owner := func(name string) bool {
		ln := strings.ToLower(name)
		best := ""
		for _, s := range siblings {
			ls := strings.ToLower(s)
			if strings.HasPrefix(ln, ls+".") && len(ls) > len(best) {
				best = ls
			}
		}
		return best == lb
	}
	var out []Sidecar
	add := func(l *listing, name, rest string) {
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
		codec, ok := subtitleCodecs[ext]
		if !ok {
			return
		}
		if ext == "idx" && l.lookup(stripExt(name)+".sub") == "" {
			return
		}
		sc := Sidecar{Path: joinRel(l.rel, name), Codec: codec, ImageBased: ext == "idx" || ext == "sup"}
		applySubtitleTags(&sc, rest)
		out = append(out, sc)
	}
	here := dirOf(dir)
	for _, name := range here.files() {
		if len(name) > len(base) && owner(name) {
			add(here, name, strings.TrimSuffix(name[len(base):], path.Ext(name)))
		}
	}
	only := len(siblings) == 1
	for _, sd := range here.subdir(func(n string) bool { return subsDirs[strings.ToLower(n)] }) {
		subs := dirOf(joinRel(dir, sd))
		for _, name := range subs.files() {
			switch {
			case len(name) > len(base) && strings.HasPrefix(strings.ToLower(name), lb+"."):
				add(subs, name, strings.TrimSuffix(name[len(base):], path.Ext(name)))
			case only:
				add(subs, name, stripExt(name))
			}
		}
		for _, d := range subs.subdir(func(n string) bool { return strings.EqualFold(n, base) }) {
			inner := dirOf(joinRel(subs.rel, d))
			for _, name := range inner.files() {
				add(inner, name, stripExt(name))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// applySubtitleTags reads language and flags from the part of a sidecar name
// after the video name: ".en", ".eng.forced", ".English.sdh", "2_English".
func applySubtitleTags(sc *Sidecar, rest string) {
	// Hyphens stay inside words so region tags such as "pt-BR" survive.
	words := strings.FieldsFunc(rest, func(r rune) bool { return r == '.' || r == '_' || r == ' ' })
	var title []string
	hi := false
	for _, w := range words {
		lw := strings.ToLower(w)
		switch {
		case lw == "forced" || lw == "foreign":
			sc.Forced = true
		case lw == "sdh" || lw == "cc" || lw == "hearing" || lw == "impaired":
			sc.HearingImpaired = true
		case lw == "hi":
			hi = true
		case lw == "default":
			sc.Default = true
		case isDigits(lw):
		default:
			if code := languageCode(lw); code != "" && sc.Language == "" {
				sc.Language = code
				continue
			}
			title = append(title, w)
		}
	}
	// "hi" is Hindi on its own and the hearing-impaired flag beside a language.
	if hi {
		if sc.Language == "" {
			sc.Language = "hin"
		} else {
			sc.HearingImpaired = true
		}
	}
	sc.Title = strings.Join(title, " ")
}

// languageCode turns a sidecar language tag into ISO 639-2/B, the form
// Matroska files carry, so external and embedded tracks compare equal.
func languageCode(w string) string {
	w = strings.ToLower(w)
	if c, ok := langAliases[w]; ok {
		return c
	}
	if i := strings.IndexAny(w, "-_"); i > 0 {
		return languageCode(w[:i])
	}
	if len(w) == 3 && langB[w] {
		return w
	}
	return ""
}

var langB = map[string]bool{}

var langAliases = func() map[string]string {
	// Each row: 639-2/B code, then the 639-1 code, the 639-2/T code when it
	// differs, and English names.
	rows := [][]string{
		{"eng", "en", "english"}, {"fre", "fr", "fra", "french", "francais"}, {"ger", "de", "deu", "german", "deutsch"},
		{"spa", "es", "spanish", "espanol", "castellano", "latino"}, {"ita", "it", "italian", "italiano"},
		{"dut", "nl", "nld", "dutch", "nederlands", "flemish"}, {"por", "pt", "portuguese", "brazilian"},
		{"rus", "ru", "russian"}, {"jpn", "ja", "japanese"}, {"chi", "zh", "zho", "chinese", "mandarin", "cantonese"},
		{"kor", "ko", "korean"}, {"swe", "sv", "swedish"}, {"nor", "no", "nb", "nob", "nno", "norwegian"},
		{"dan", "da", "danish"}, {"fin", "fi", "finnish"}, {"pol", "pl", "polish"}, {"tur", "tr", "turkish"},
		{"gre", "el", "ell", "greek"}, {"heb", "he", "iw", "hebrew"}, {"ara", "ar", "arabic"}, {"hin", "hindi"},
		{"hun", "hu", "hungarian"}, {"cze", "cs", "ces", "czech"}, {"rum", "ro", "ron", "romanian"},
		{"tha", "th", "thai"}, {"vie", "vi", "vietnamese"}, {"ind", "id", "indonesian"}, {"may", "ms", "msa", "malay"},
		{"ukr", "uk", "ukrainian"}, {"bul", "bg", "bulgarian"}, {"hrv", "hr", "croatian"}, {"srp", "sr", "serbian"},
		{"slv", "sl", "slovenian"}, {"slo", "sk", "slk", "slovak"}, {"est", "et", "estonian"}, {"lav", "lv", "latvian"},
		{"lit", "lt", "lithuanian"}, {"ice", "is", "isl", "icelandic"}, {"cat", "ca", "catalan"},
		{"baq", "eu", "eus", "basque"}, {"glg", "gl", "galician"}, {"per", "fa", "fas", "persian", "farsi"},
		{"tam", "ta", "tamil"}, {"tel", "te", "telugu"}, {"ben", "bn", "bengali"}, {"urd", "ur", "urdu"},
		{"fil", "tl", "tgl", "filipino", "tagalog"}, {"alb", "sq", "sqi", "albanian"}, {"mac", "mk", "mkd", "macedonian"},
		{"wel", "cy", "cym", "welsh"}, {"gle", "ga", "irish"}, {"afr", "af", "afrikaans"}, {"lat", "la", "latin"},
	}
	m := map[string]string{}
	for _, r := range rows {
		langB[r[0]] = true
		for _, a := range r {
			m[a] = r[0]
		}
	}
	// "hi" is handled by applySubtitleTags: it means hearing impaired beside a
	// language and Hindi alone.
	delete(m, "hi")
	return m
}()

var imageExts = []string{"jpg", "jpeg", "png", "webp"}

// findImage returns the first listed file named stem.<image ext>, trying stems
// in order.
func findImage(l *listing, stems ...string) string {
	if l == nil {
		return ""
	}
	for _, s := range stems {
		for _, ext := range imageExts {
			if n := l.lookup(s + "." + ext); n != "" {
				return joinRel(l.rel, n)
			}
		}
	}
	return ""
}

// Artwork kinds, matching the artwork.kind column.
const (
	artPoster   = "poster"
	artBackdrop = "backdrop"
	artThumb    = "thumb"
	artLogo     = "logo"
)

// movieArtwork finds Kodi and Plex style local art for a movie. Names without
// the video's own prefix (poster.jpg, folder.jpg) count only when the folder
// holds this one title.
func movieArtwork(l *listing, base string, generic bool) map[string]string {
	pick := func(own []string, shared []string) string {
		stems := make([]string, 0, len(own)+len(shared))
		for _, o := range own {
			stems = append(stems, base+o)
		}
		if generic {
			stems = append(stems, shared...)
		}
		return findImage(l, stems...)
	}
	return compact(map[string]string{
		artPoster:   pick([]string{"-poster", ""}, []string{"poster", "folder", "cover", "movie", "default"}),
		artBackdrop: pick([]string{"-fanart", "-backdrop", "-background"}, []string{"fanart", "backdrop", "background", "art"}),
		artLogo:     pick([]string{"-clearlogo", "-logo"}, []string{"clearlogo", "logo"}),
		artThumb:    pick([]string{"-landscape", "-thumb"}, []string{"landscape", "thumb"}),
	})
}

func showArtwork(l *listing) map[string]string {
	return compact(map[string]string{
		artPoster:   findImage(l, "poster", "folder", "show", "cover"),
		artBackdrop: findImage(l, "fanart", "backdrop", "background", "art"),
		artLogo:     findImage(l, "clearlogo", "logo"),
		artThumb:    findImage(l, "landscape", "thumb"),
	})
}

// seasonArtwork reads seasonNN-poster.jpg and similar names from the show folder, and
// poster.jpg or folder.jpg from the season folder itself.
func seasonArtwork(show, season *listing, n int) map[string]string {
	var stems []string
	if n == 0 {
		stems = append(stems, "season-specials", "season00", "season0", "specials")
	}
	stems = append(stems, fmt.Sprintf("season%02d", n), fmt.Sprintf("season%d", n))
	with := func(suffix string) []string {
		out := make([]string, len(stems))
		for i, s := range stems {
			out[i] = s + suffix
		}
		return out
	}
	poster := findImage(show, with("-poster")...)
	if poster == "" {
		poster = findImage(season, "poster", "folder", "cover", "season")
	}
	if poster == "" {
		poster = findImage(show, stems...)
	}
	backdrop := findImage(show, with("-fanart")...)
	if backdrop == "" {
		backdrop = findImage(season, "fanart", "backdrop", "background")
	}
	return compact(map[string]string{artPoster: poster, artBackdrop: backdrop})
}

func episodeArtwork(l *listing, base string) map[string]string {
	return compact(map[string]string{artThumb: findImage(l, base+"-thumb", base)})
}

func compact(m map[string]string) map[string]string {
	for k, v := range m {
		if v == "" {
			delete(m, k)
		}
	}
	return m
}

// movieNFO finds the NFO for a movie: <video>.nfo, then movie.nfo, then the only
// .nfo in a folder that holds one title, which is how scene releases ship
// theirs.
func movieNFO(l *listing, base string, generic bool) string {
	if n := l.lookup(base + ".nfo"); n != "" {
		return joinRel(l.rel, n)
	}
	if !generic {
		return ""
	}
	if n := l.lookup("movie.nfo"); n != "" {
		return joinRel(l.rel, n)
	}
	var only string
	for _, f := range l.files() {
		if strings.EqualFold(path.Ext(f), ".nfo") {
			if only != "" {
				return ""
			}
			only = f
		}
	}
	if only != "" {
		return joinRel(l.rel, only)
	}
	return ""
}
