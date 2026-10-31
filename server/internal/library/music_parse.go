package library

import (
	"bufio"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// KindMusic is a music library: artists, albums and tracks, live shows
// included. It is scanned by scanMusic, not by the movie and show scanner.
const KindMusic = "music"

// audioExts are the files a music scan indexes. SHN (Shorten) is still how
// older shows circulate among tapers; ffprobe reads it, browsers do not, so
// it always plays converted.
var audioExts = map[string]bool{
	".flac": true, ".mp3": true, ".m4a": true, ".aac": true, ".alac": true, ".opus": true, ".ogg": true,
	".oga": true, ".wav": true, ".shn": true, ".aif": true, ".aiff": true, ".wv": true,
}

// IsAudio reports whether a file name is one a music scan indexes.
func IsAudio(name string) bool {
	return audioExts[strings.ToLower(path.Ext(name))]
}

// Show is what a live recording's folder name and info file say about it.
type Show struct {
	// Date is YYYY-MM-DD.
	Date     string
	Venue    string
	Location string
	// Source is SBD, AUD, MTX or FM; SourceDetail is the info file's Source
	// line as the taper wrote it ("SBD > DAT > CD > EAC > FLAC").
	Source       string
	SourceDetail string
	Lineage      string
	Taper        string
	// Artist is the first line of the info file, when it has the usual
	// artist, date, venue, place header.
	Artist string
}

var (
	// 2023-05-12, 2023.05.12 or 2023_05_12 anywhere in a name.
	isoDateRe = regexp.MustCompile(`(?:^|[^0-9])((?:19|20)[0-9]{2})[-._ /]([01][0-9])[-._ /]([0-3][0-9])(?:[^0-9]|$)`)
	// The etree shorthand: band letters, then a two-digit year (gd77-05-08).
	etreeDateRe = regexp.MustCompile(`^[a-zA-Z]{1,8}([0-9]{2})-([01][0-9])-([0-3][0-9])(?:[^0-9]|$)`)
	// Words that name a recording's source, in a folder name or an info file.
	sourceWords = map[string]string{
		"sbd": "SBD", "soundboard": "SBD", "dsbd": "SBD", "board": "SBD",
		"aud": "AUD", "audience": "AUD",
		"mtx": "MTX", "matrix": "MTX",
		"fm": "FM", "prefm": "FM", "broadcast": "FM",
	}
	// Disc and set folders inside a show or album folder.
	discDirRe = regexp.MustCompile(`(?i)^(?:cd|disc|disk|d|set)[ ._-]*([0-9]{1,2})$`)
	// d1t03 or s2t07 in a taper's file name, and t03 for a one-disc show.
	tapeTrackRe = regexp.MustCompile(`(?i)(?:^|[^a-z])(?:d|cd|s)([0-9]{1,2})t([0-9]{1,3})(?:[^0-9]|$)`)
	tapeOnlyRe  = regexp.MustCompile(`(?i)(?:^|[^a-z])()t([0-9]{2,3})(?:[^0-9]|$)`)
	// "03 - Title", "03. Title", "1-03 Title" at the start of a file name.
	trackNumRe = regexp.MustCompile(`^(?:([0-9]{1,2})[-.])?([0-9]{1,3})(?:\s*[-._)]\s*|\s+)(.*)$`)
	// Bracketed source notes such as [SBD] or (AUD) in a folder name.
	bracketRe = regexp.MustCompile(`[\[(][^\])]*[\])]`)
)

// ParseShowDate finds a show date in a folder or file name: an ISO-style date
// anywhere, or the etree shorthand (gd77-05-08), whose two-digit year means
// this century up to the current year and the last one after it.
func ParseShowDate(name string) (date string, at int, end int, ok bool) {
	if m := isoDateRe.FindStringSubmatchIndex(name); m != nil {
		y, mo, d := name[m[2]:m[3]], name[m[4]:m[5]], name[m[6]:m[7]]
		if validDate(y, mo, d) {
			return y + "-" + mo + "-" + d, m[2], m[7], true
		}
	}
	if m := etreeDateRe.FindStringSubmatchIndex(name); m != nil {
		yy, _ := strconv.Atoi(name[m[2]:m[3]])
		century := 1900
		if yy <= time.Now().Year()%100 {
			century = 2000
		}
		y, mo, d := strconv.Itoa(century+yy), name[m[4]:m[5]], name[m[6]:m[7]]
		if validDate(y, mo, d) {
			return y + "-" + mo + "-" + d, m[2], m[7], true
		}
	}
	return "", 0, 0, false
}

func validDate(y, m, d string) bool {
	_, err := time.Parse("2006-01-02", y+"-"+m+"-"+d)
	return err == nil
}

// SourceOf classifies free text ("Matrix of SBD and AUD", "sbd.miller") as one
// source. A matrix mixes the others, so it wins; a soundboard beats a
// broadcast, which beats an audience tape.
func SourceOf(text string) string {
	found := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if s, ok := sourceWords[w]; ok {
			found[s] = true
		}
	}
	for _, s := range []string{"MTX", "SBD", "FM", "AUD"} {
		if found[s] {
			return s
		}
	}
	return ""
}

// ParseShowFolder reads a live show's folder name. Both shapes tapers use:
//
//	Umphrey's McGee - 2023-05-12 - Red Rocks Amphitheatre, Morrison, CO [SBD]
//	ph1997-11-22.sbd.miller.flac16
//
// ok is false when the name holds no date.
func ParseShowFolder(name string) (Show, bool) {
	date, _, end, ok := ParseShowDate(name)
	if !ok {
		return Show{}, false
	}
	sh := Show{Date: date, Source: SourceOf(name)}
	// The venue is whatever words follow the date, up to a bracket or the
	// end, when they read as a place rather than a taper's dotted tags.
	rest := strings.TrimSpace(bracketRe.ReplaceAllString(name[end:], " "))
	rest = strings.TrimLeft(rest, " -_.")
	if i := strings.Index(rest, " - "); i >= 0 {
		rest = rest[:i]
	}
	if rest != "" && (strings.ContainsRune(rest, ' ') || strings.ContainsRune(rest, ',')) {
		venue, location, _ := strings.Cut(rest, ",")
		sh.Venue = strings.TrimSpace(venue)
		sh.Location = strings.TrimSpace(location)
	}
	return sh, true
}

// infoDateLayouts are the ways an info file writes a show's date.
var infoDateLayouts = []string{"2006-01-02", "01/02/2006", "1/2/2006", "01-02-2006", "January 2, 2006", "January 2 2006",
	"Jan 2, 2006", "Jan 2 2006", "2 January 2006", "Monday, January 2, 2006", "Mon, January 2, 2006", "2006.01.02", "01/02/06", "1/2/06"}

func parseInfoDate(s string) (string, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "."))
	for _, l := range infoDateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.Format("2006-01-02"), true
		}
	}
	if d, _, _, ok := ParseShowDate(s); ok && len(s) <= 16 {
		return d, true
	}
	return "", false
}

// infoLabels map an info file's "Label: value" lines onto Show fields.
var infoLabels = map[string]string{
	"source": "source", "src": "source", "lineage": "lineage", "transfer": "lineage", "transferred by": "taper",
	"taper": "taper", "taped by": "taper", "recorded by": "taper", "recording": "taper",
	"venue": "venue", "location": "location", "date": "date",
}

// ParseInfoFile reads a show's .txt file. Tapers label the useful lines
// (Source:, Lineage:, Taper:); most also start with a header of artist, date,
// venue and place, one per line, which is read only when its second line is a
// date, so a setlist or notes are never mistaken for a venue.
func ParseInfoFile(text string) Show {
	var sh Show
	var header []string
	inHeader := true
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\uFEFF"))
		if line == "" {
			if len(header) > 0 {
				inHeader = false
			}
			continue
		}
		if label, value, ok := strings.Cut(line, ":"); ok && len(label) <= 16 {
			field := infoLabels[strings.ToLower(strings.TrimSpace(label))]
			value = strings.TrimSpace(value)
			switch {
			case field == "" || value == "":
			case field == "source" && sh.SourceDetail == "":
				sh.SourceDetail = value
			case field == "lineage" && sh.Lineage == "":
				sh.Lineage = value
			case field == "taper" && sh.Taper == "":
				sh.Taper = value
			case field == "venue" && sh.Venue == "":
				sh.Venue = value
			case field == "location" && sh.Location == "":
				sh.Location = value
			case field == "date" && sh.Date == "":
				sh.Date, _ = parseInfoDate(value)
			}
			if field != "" {
				inHeader = false
				continue
			}
		}
		if inHeader && len(header) < 4 {
			header = append(header, line)
		}
	}
	if len(header) >= 2 {
		if d, ok := parseInfoDate(header[1]); ok {
			sh.Artist = header[0]
			if sh.Date == "" {
				sh.Date = d
			}
			if len(header) >= 3 && sh.Venue == "" {
				sh.Venue = header[2]
			}
			if len(header) >= 4 && sh.Location == "" {
				sh.Location = header[3]
			}
		}
	}
	if sh.Venue != "" && sh.Location == "" {
		if v, loc, ok := strings.Cut(sh.Venue, ","); ok {
			sh.Venue, sh.Location = strings.TrimSpace(v), strings.TrimSpace(loc)
		}
	}
	sh.Source = SourceOf(sh.SourceDetail)
	return sh
}

// Checksum is one line of a .md5 or .ffp file.
type Checksum struct {
	// File is the name as the checksum file gives it, slash-separated.
	File string
	// Algorithm is "md5" (of the whole file) or "ffp" (FLAC fingerprint: the
	// MD5 of the decoded audio).
	Algorithm string
	// Value is lowercase hex.
	Value string
}

var hex32 = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// ParseChecksumFile reads md5sum output ("<hex>  name" or "<hex> *name") or a
// FLAC fingerprint file ("name:<hex>"). Lines it cannot read are skipped, as
// are comments; the algorithm comes from the file's extension.
func ParseChecksumFile(name, text string) []Checksum {
	algo := strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".")
	if algo != "md5" && algo != "ffp" {
		return nil
	}
	var out []Checksum
	seen := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\uFEFF"))
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		var file, value string
		if algo == "ffp" {
			i := strings.LastIndexByte(line, ':')
			if i < 0 {
				continue
			}
			file, value = line[:i], line[i+1:]
		} else {
			if len(line) < 34 {
				continue
			}
			value, file = line[:32], strings.TrimLeft(line[32:], " \t")
			file = strings.TrimPrefix(file, "*")
		}
		file = strings.TrimPrefix(path.Clean(strings.ReplaceAll(strings.TrimSpace(file), `\`, "/")), "./")
		if !hex32.MatchString(value) || file == "" || file == "." || strings.HasPrefix(file, "../") || seen[file] {
			continue
		}
		seen[file] = true
		out = append(out, Checksum{File: file, Algorithm: algo, Value: strings.ToLower(value)})
	}
	return out
}

// TrackNumbers reads disc and track from a file name when the tags have none:
// the tapers' d1t03 shape first, then a leading "03 - Title" or "1-03 Title".
// It also returns the title the name leaves once the number is taken off.
func TrackNumbers(file string) (disc, track int, title string) {
	stem := strings.TrimSuffix(path.Base(file), path.Ext(file))
	for _, re := range []*regexp.Regexp{tapeTrackRe, tapeOnlyRe} {
		if m := re.FindStringSubmatch(stem); m != nil {
			disc, _ = strconv.Atoi(m[1])
			track, _ = strconv.Atoi(m[2])
			return disc, track, stem
		}
	}
	if m := trackNumRe.FindStringSubmatch(stem); m != nil && m[3] != "" {
		disc, _ = strconv.Atoi(m[1])
		track, _ = strconv.Atoi(m[2])
		return disc, track, strings.TrimSpace(m[3])
	}
	return 0, 0, stem
}

// numberOf reads "3" or "3/12" (a track or disc tag).
func numberOf(s string) int {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "/")
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 || n > 9999 {
		return 0
	}
	return n
}

// DiscFolder reports the disc number a folder name stands for ("CD2",
// "Disc 1", "Set 2"), or 0.
func DiscFolder(name string) int {
	if m := discDirRe.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// gainOf reads a ReplayGain value ("-6.20 dB", "+1.5 dB", "0.988"). ok is
// false for anything else.
func gainOf(s string) (float64, bool) {
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(strings.ToLower(s), "db")), " "))
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < -100 || f > 100 {
		return 0, false
	}
	return f, true
}

// yearOf reads the year from a date tag ("1997", "1997-11-22", "1997/11").
func yearOf(s string) int {
	if len(s) < 4 {
		return 0
	}
	y, err := strconv.Atoi(s[:4])
	if err != nil || y < 1000 || y > 3000 {
		return 0
	}
	return y
}

// dateOf normalizes a date tag to YYYY, YYYY-MM or YYYY-MM-DD.
func dateOf(s string) string {
	s = strings.TrimSpace(s)
	if d, _, _, ok := ParseShowDate(s); ok && strings.HasPrefix(s, d[:4]) {
		return d
	}
	if y := yearOf(s); y > 0 {
		if len(s) >= 7 && (s[4] == '-' || s[4] == '/' || s[4] == '.') {
			if m, err := strconv.Atoi(s[5:7]); err == nil && m >= 1 && m <= 12 {
				return fmt.Sprintf("%04d-%02d", y, m)
			}
		}
		return strconv.Itoa(y)
	}
	return ""
}

// foldKey folds a name for matching: lowercase letters and digits, single
// spaces, nothing else, so "Umphrey's McGee" and "umphreys mcgee" are one.
func foldKey(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		case r == '\'' || r == '’' || r == '.':
			// Apostrophes and dots join: "Umphrey's" is "umphreys", "R.E.M." is "rem".
		default:
			space = true
		}
	}
	return b.String()
}

// sortName moves a leading "The " to the end for sorting: "Band, The".
func sortName(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 4 && strings.EqualFold(s[:4], "the ") {
		return strings.TrimSpace(s[4:]) + ", " + s[:3]
	}
	return s
}
