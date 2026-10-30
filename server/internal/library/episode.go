package library

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Episode is what an episode's path says. Season 0 holds specials. Episode is 0
// for a file with no number, which becomes an unnumbered special.
type Episode struct {
	Show       string
	ShowYear   int
	Season     int
	Episode    int
	EpisodeEnd int
	AirDate    string
	Title      string
	// ShowDir and SeasonDir are the folders (relative to the root) that hold
	// show-level and season-level artwork and NFOs; empty when there are none.
	ShowDir   string
	SeasonDir string
	MediaTags
}

// marker is an episode number found in a name and where it sits.
type marker struct {
	season, episode, end int
	date                 string
	start, stop          int
	seasonOnly           bool
}

var (
	seasonWordsRe = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:season|series)[ ._-]*(\d{1,3})[ ._-]*(?:episode|ep|e)[ ._-]*(\d{1,4})(?:[ ._]*-[ ._]*(\d{1,4}))?(?:[^0-9]|$)`)
	crossRe       = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(\d{1,2})x(\d{2,3})(?:[-_](?:\d{1,2}x)?(\d{2,3}))?(?:[^0-9p]|$)`)
	dateRe        = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)\d\d)[ ._-](\d\d)[ ._-](\d\d)(?:[^0-9]|$)`)
	bareEpRe      = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])(?:episode|ep|e)[ ._-]?(\d{1,3})(?:[^a-z0-9]|$)`)
	animeRe       = regexp.MustCompile(`\s-\s(\d{2,4})(?:v\d)?(?:\s|$|\[|\()`)
	leadingNumRe  = regexp.MustCompile(`^(\d{1,3})(?:[ ._-]|$)`)
	seasonPackRe  = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s(\d{1,3})(?:[^0-9e]|$)`)
	seasonDirRe   = regexp.MustCompile(`(?i)^(?:season|series|staffel|saison|temporada|seizoen)[ ._-]*(\d{1,3})(?:[^0-9].*)?$`)
	shortSeasonRe = regexp.MustCompile(`(?i)^s(\d{1,3})$`)
	specialsRe    = regexp.MustCompile(`(?i)^specials?$`)
)

// findSxxEyy scans for S01E02 and its multi-episode forms: S01E01E02, S01E01-E02,
// S01E01-02 and S01 E01. Hand-rolled because Go regexps cannot look behind.
func findSxxEyy(s string) (marker, bool) {
	ls := strings.ToLower(s)
	for i := 0; i < len(ls); i++ {
		if ls[i] != 's' || (i > 0 && isAlnumByte(ls[i-1])) {
			continue
		}
		j := i + 1
		season, j2 := digits(ls, j, 3)
		if j2 == j {
			continue
		}
		j = j2
		if j < len(ls) && strings.IndexByte(" ._-", ls[j]) >= 0 && j+1 < len(ls) && ls[j+1] == 'e' {
			j++
		}
		if j >= len(ls) || ls[j] != 'e' {
			continue
		}
		ep, j3 := digits(ls, j+1, 4)
		if j3 == j+1 {
			continue
		}
		j = j3
		m := marker{season: season, episode: ep, start: i, stop: j}
		for {
			k := j
			dash := false
			if k < len(ls) && strings.IndexByte(" ._", ls[k]) >= 0 {
				k++
			}
			if k < len(ls) && ls[k] == '-' {
				dash = true
				k++
				if k < len(ls) && strings.IndexByte(" ._", ls[k]) >= 0 {
					k++
				}
			}
			if k < len(ls) && ls[k] == 's' {
				if s2, k2 := digits(ls, k+1, 3); k2 > k+1 && s2 == season {
					k = k2
				}
			}
			if k < len(ls) && ls[k] == 'e' {
				if e2, k2 := digits(ls, k+1, 4); k2 > k+1 && e2 > m.episode && e2 > m.end {
					m.end, j = e2, k2
					continue
				}
			}
			if dash {
				if e2, k2 := digits(ls, k, 4); k2 > k && (k2 >= len(ls) || !isAlnumByte(ls[k2])) && e2 > m.episode && e2-m.episode <= 30 {
					m.end, j = e2, k2
					continue
				}
			}
			break
		}
		m.stop = j
		return m, true
	}
	return marker{}, false
}

func digits(s string, i, maxN int) (int, int) {
	j := i
	for j < len(s) && j-i < maxN && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == i || (j < len(s) && s[j] >= '0' && s[j] <= '9') {
		return 0, i
	}
	n, _ := strconv.Atoi(s[i:j])
	return n, j
}

func isAlnumByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// findMarker looks for an episode number in one name. strict allows only the
// unambiguous forms, for movie libraries where "1x01" or a date could be part
// of a film's name.
func findMarker(name string, strict bool, inSeason bool) (marker, bool) {
	if m, ok := findSxxEyy(name); ok {
		return m, true
	}
	if m := seasonWordsRe.FindStringSubmatchIndex(name); m != nil {
		mk := marker{season: atoiSub(name, m, 1), episode: atoiSub(name, m, 2), start: m[0], stop: m[1]}
		if m[6] >= 0 {
			if e := atoiSub(name, m, 3); e > mk.episode {
				mk.end = e
			}
		}
		return mk, true
	}
	if strict {
		return marker{}, false
	}
	if m := crossRe.FindStringSubmatchIndex(name); m != nil {
		ep := atoiSub(name, m, 2)
		// 5.1x264 is audio and a codec, not season 1 episode 264.
		if ep != 264 && ep != 265 {
			mk := marker{season: atoiSub(name, m, 1), episode: ep, start: m[0], stop: m[1]}
			if m[6] >= 0 {
				if e := atoiSub(name, m, 3); e > ep {
					mk.end = e
				}
			}
			return mk, true
		}
	}
	if m := dateRe.FindStringSubmatchIndex(name); m != nil {
		y, mo, d := atoiSub(name, m, 1), atoiSub(name, m, 2), atoiSub(name, m, 3)
		if mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
			return marker{season: y, episode: mo*100 + d, date: fmt.Sprintf("%04d-%02d-%02d", y, mo, d), start: m[0], stop: m[1]}, true
		}
	}
	if m := bareEpRe.FindStringSubmatchIndex(name); m != nil {
		return marker{season: -1, episode: atoiSub(name, m, 1), start: m[0], stop: m[1]}, true
	}
	if m := animeRe.FindStringSubmatchIndex(name); m != nil {
		return marker{season: -1, episode: atoiSub(name, m, 1), start: m[0], stop: m[1]}, true
	}
	if inSeason {
		if m := leadingNumRe.FindStringSubmatchIndex(name); m != nil {
			return marker{season: -1, episode: atoiSub(name, m, 1), start: 0, stop: m[1]}, true
		}
	}
	if m := seasonPackRe.FindStringSubmatchIndex(name); m != nil {
		return marker{season: atoiSub(name, m, 1), seasonOnly: true, start: m[0], stop: m[1]}, true
	}
	return marker{}, false
}

func atoiSub(s string, m []int, g int) int {
	if m[2*g] < 0 {
		return 0
	}
	n, _ := strconv.Atoi(s[m[2*g]:m[2*g+1]])
	return n
}

// seasonFolder returns the season a folder name stands for, or -1.
func seasonFolder(name string) int {
	name = strings.TrimSpace(name)
	if specialsRe.MatchString(name) {
		return 0
	}
	for _, re := range []*regexp.Regexp{seasonDirRe, shortSeasonRe} {
		if m := re.FindStringSubmatch(name); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return -1
}

// looksLikeRelease reports whether a folder name is a release (it carries an
// episode or season marker or quality tags) rather than a plain show name.
func looksLikeRelease(name string) bool {
	if _, ok := findMarker(name, false, false); ok {
		return true
	}
	ts := tokenize(name)
	for i := 1; i < len(ts); i++ {
		if info := classifyWord(ts[i].lower); info.hard && techKind(info.kind) {
			return true
		}
	}
	return false
}

// showName parses a show name and year from the text before an episode marker,
// or from a folder name.
func showName(s string) (string, int) {
	m := ParseMovieName(strings.TrimRight(strings.TrimSpace(s), " .-_"))
	return m.Title, m.Year
}

// episodeTitle takes the words after the marker up to the first tag, dropping
// an absolute episode number such as the 135 in "Show.S04E04.135.480p".
func episodeTitle(s string) string {
	ts := trimDashes(tokenize(s))
	for len(ts) > 0 && isDigits(ts[0].lower) && ts[0].group == 0 && len(ts[0].lower) <= 4 {
		ts = trimDashes(ts[1:])
	}
	end := len(ts)
	for i := 0; i < len(ts); {
		info, w := classifyAt(ts, i)
		if info.hard {
			end = i
			break
		}
		i += w
	}
	for i := 0; i < end; i++ {
		if softRun(ts, i, end) {
			end = i
			break
		}
	}
	return joinTitle(ts[:end])
}

func hasTechTag(ts []token) bool {
	for i := range ts {
		if info := classifyWord(ts[i].lower); info.hard && techKind(info.kind) {
			return true
		}
	}
	return false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ParseEpisodePath parses an episode from its path relative to the library
// root. strict is for movie libraries: only S01E02 and "Season 1 Episode 2"
// forms count, and ok is false when neither appears. In a show library ok is
// always true, though Episode may be 0 for a file with no number at all.
func ParseEpisodePath(rel string, strict bool) (Episode, bool) {
	rel = strings.Trim(path.Clean("/"+rel), "/")
	dirs := strings.Split(path.Dir(rel), "/")
	if dirs[0] == "." {
		dirs = nil
	}
	for i := range dirs {
		dirs[i] = cleanName(dirs[i])
	}
	base := cleanName(stripExt(path.Base(rel)))

	var ep Episode
	seasonIdx := -1
	for i := len(dirs) - 1; i >= 0; i-- {
		if n := seasonFolder(dirs[i]); n >= 0 {
			seasonIdx = i
			ep.Season = n
			ep.SeasonDir = strings.Join(dirs[:i+1], "/")
			break
		}
	}
	if seasonIdx < 0 {
		ep.Season = -1
	}

	mk, found := findMarker(base, strict, seasonIdx >= 0)
	fileHasEpisode := found && !mk.seasonOnly
	// A release folder can carry the number when the file inside is a hash.
	if !fileHasEpisode {
		for i := len(dirs) - 1; i >= 0 && i > seasonIdx; i-- {
			if m, ok := findMarker(dirs[i], strict, false); ok {
				if !m.seasonOnly || !found {
					mk, found = m, true
				}
				if !m.seasonOnly {
					break
				}
			}
		}
	}
	if strict && (!found || mk.seasonOnly) {
		return Episode{}, false
	}
	if found {
		if mk.season >= 0 {
			ep.Season = mk.season
		}
		if !mk.seasonOnly {
			ep.Episode, ep.EpisodeEnd, ep.AirDate = mk.episode, mk.end, mk.date
		}
	}
	if ep.Season < 0 {
		ep.Season = 1
		if !found || mk.seasonOnly || mk.episode == 0 {
			ep.Season = 0
		}
	}

	// Episode title and quality tags come from the file name.
	if fileHasEpisode {
		// A number found among the tags ("Show.1983.1080p.E02.WEB") has no
		// title after it, only more tags.
		if !hasTechTag(tokenize(base[:mk.start])) {
			ep.Title = episodeTitle(base[mk.stop:])
		}
	} else if !found || ep.Episode == 0 {
		ep.Title = ParseMovieName(base).Title
	}
	_, ep.MediaTags = collectTags(tokenize(base))
	if len(dirs) > 0 {
		_, folderTags := collectTags(tokenize(dirs[len(dirs)-1]))
		ep.MediaTags = mergeTags(ep.MediaTags, folderTags)
	}

	ep.Show, ep.ShowYear, ep.ShowDir = showFor(dirs, seasonIdx, base, strict)
	if ep.Title != "" && NormalizeTitle(ep.Title) == NormalizeTitle(ep.Show) && ep.Episode > 0 {
		ep.Title = ""
	}
	return ep, true
}

// showFor picks the show name. In a show library the folder above a season
// folder, or else the top-level folder, names the show unless it is itself a
// release; a release folder beats the file name, which beats nothing.
func showFor(dirs []string, seasonIdx int, base string, strict bool) (name string, year int, showDir string) {
	fromName := func(s string) (string, int) {
		if m, ok := findMarker(s, strict, false); ok {
			return showName(s[:m.start])
		}
		return showName(s)
	}
	fileShow, fileYear := fromName(base)
	if strict {
		// Movie libraries hold release folders, not show folders: trust the file,
		// borrowing the folder's year when the folder names the same show.
		if len(dirs) > 0 {
			fn, fy := fromName(dirs[len(dirs)-1])
			if fileShow == "" {
				return fn, fy, ""
			}
			if NormalizeTitle(fn) == NormalizeTitle(fileShow) {
				return fn, max(fy, fileYear), ""
			}
		}
		return fileShow, fileYear, ""
	}
	idx := 0
	if seasonIdx > 0 {
		idx = seasonIdx - 1
	}
	if len(dirs) > idx && seasonIdx != 0 {
		d := dirs[idx]
		if !looksLikeRelease(d) {
			n, y := showName(d)
			if n != "" {
				return n, y, strings.Join(dirs[:idx+1], "/")
			}
		} else if n, y := fromName(d); n != "" {
			return n, y, ""
		}
	}
	if fileShow != "" {
		return fileShow, fileYear, ""
	}
	return ParseMovieName(base).Title, 0, ""
}
