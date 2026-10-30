// Package metadata reads Kodi-style NFO files. It only ever reads them: VCRX
// never writes beside the media.
package metadata

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// NFO kinds, named after the Kodi root elements.
const (
	KindMovie   = "movie"
	KindTVShow  = "tvshow"
	KindEpisode = "episodedetails"
)

// MaxSize is the most of an NFO file that is read. Real ones are a few KB;
// anything larger is not metadata.
const MaxSize = 1 << 20

// NFO is the metadata one file holds. Kind is empty when the file was not Kodi
// XML (a scene NFO or a bare URL); then only IMDbID or TMDbID may be set.
type NFO struct {
	Kind             string
	Title            string
	OriginalTitle    string
	SortTitle        string
	ShowTitle        string
	Year             int
	Plot             string
	Tagline          string
	ContentRating    string
	RuntimeMs        int64
	Premiered        string
	Genres           []string
	Studios          []string
	Directors        []string
	Writers          []string
	Countries        []string
	Tags             []string
	Actors           []Actor
	Ratings          []Rating
	IMDbID           string
	TMDbID           int64
	SetName          string
	TMDbCollectionID int64
	Season           int
	Episode          int
	EpisodeEnd       int
}

// Actor is one cast entry.
type Actor struct {
	Name  string
	Role  string
	Order int
}

// Rating is one score. Source is lower-cased, e.g. imdb, tmdb, themoviedb.
type Rating struct {
	Source  string
	Value   float64
	Votes   int
	Default bool
}

// Structured reports whether the file was real Kodi XML.
func (n *NFO) Structured() bool { return n != nil && n.Kind != "" }

// Read opens path read-only and parses at most MaxSize bytes of it.
func Read(path string) (*NFO, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxSize))
	if err != nil {
		return nil, err
	}
	return Parse(data), nil
}

var (
	imdbRe = regexp.MustCompile(`\btt\d{7,8}\b`)
	tmdbRe = regexp.MustCompile(`themoviedb\.org/(?:movie|tv)/(\d+)`)
)

// Parse never fails: anything that is not Kodi XML yields an NFO with only the
// ids found in its text, so a scene NFO full of ASCII art cannot break a scan.
func Parse(data []byte) *NFO {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if n, err := parseXML(data); err == nil {
		return n
	}
	n := &NFO{}
	if m := imdbRe.Find(data); m != nil {
		n.IMDbID = string(m)
	}
	if m := tmdbRe.FindSubmatch(data); m != nil {
		n.TMDbID, _ = strconv.ParseInt(string(m[1]), 10, 64)
	}
	return n
}

// xmlDoc covers <movie>, <tvshow> and <episodedetails>; they share most fields.
type xmlDoc struct {
	XMLName       xml.Name
	Title         string      `xml:"title"`
	OriginalTitle string      `xml:"originaltitle"`
	SortTitle     string      `xml:"sorttitle"`
	ShowTitle     string      `xml:"showtitle"`
	Year          string      `xml:"year"`
	Plot          string      `xml:"plot"`
	Outline       string      `xml:"outline"`
	Tagline       string      `xml:"tagline"`
	MPAA          string      `xml:"mpaa"`
	Certification string      `xml:"certification"`
	Runtime       string      `xml:"runtime"`
	Premiered     string      `xml:"premiered"`
	Aired         string      `xml:"aired"`
	ReleaseDate   string      `xml:"releasedate"`
	Genres        []string    `xml:"genre"`
	Studios       []string    `xml:"studio"`
	Directors     []string    `xml:"director"`
	Credits       []string    `xml:"credits"`
	Writers       []string    `xml:"writer"`
	Countries     []string    `xml:"country"`
	Tags          []string    `xml:"tag"`
	Actors        []xmlActor  `xml:"actor"`
	Rating        string      `xml:"rating"`
	Votes         string      `xml:"votes"`
	Ratings       []xmlRating `xml:"ratings>rating"`
	UniqueIDs     []xmlID     `xml:"uniqueid"`
	ID            string      `xml:"id"`
	IMDbID        string      `xml:"imdbid"`
	IMDb          string      `xml:"imdb_id"`
	TMDbID        string      `xml:"tmdbid"`
	Set           xmlSet      `xml:"set"`
	TMDbColID     string      `xml:"tmdbcolid"`
	TMDbColID2    string      `xml:"tmdbCollectionId"`
	CollectionNum string      `xml:"collectionnumber"`
	Season        string      `xml:"season"`
	Episode       string      `xml:"episode"`
}

type xmlActor struct {
	Name  string `xml:"name"`
	Role  string `xml:"role"`
	Order string `xml:"order"`
}

type xmlRating struct {
	Name    string `xml:"name,attr"`
	Max     string `xml:"max,attr"`
	Default string `xml:"default,attr"`
	Value   string `xml:"value"`
	Votes   string `xml:"votes"`
}

type xmlID struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

// xmlSet accepts both the old <set>Name</set> and the newer
// <set tmdbcolid="10"><name>Name</name></set>.
type xmlSet struct {
	TMDbColID string `xml:"tmdbcolid,attr"`
	Name      string `xml:"name"`
	Text      string `xml:",chardata"`
}

var errNotKodi = errors.New("not a Kodi NFO")

func parseXML(data []byte) (*NFO, error) {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '<' {
		return nil, errNotKodi
	}
	dec := xml.NewDecoder(bytes.NewReader(trimmed))
	dec.Strict = false
	dec.CharsetReader = charsetReader
	var docs []xmlDoc
	for {
		tok, err := dec.Token()
		if err != nil {
			// Kodi allows a URL after the XML, and a multi-episode file holds one
			// <episodedetails> per episode with no common root.
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case KindMovie, KindTVShow, KindEpisode:
		default:
			if len(docs) == 0 {
				return nil, errNotKodi
			}
			continue
		}
		var d xmlDoc
		if err := dec.DecodeElement(&d, &se); err != nil {
			if len(docs) == 0 {
				return nil, fmt.Errorf("nfo xml: %w", err)
			}
			break
		}
		docs = append(docs, d)
		if se.Name.Local != KindEpisode {
			break
		}
	}
	if len(docs) == 0 {
		return nil, errNotKodi
	}
	n := convert(&docs[0])
	for _, d := range docs[1:] {
		if e := atoi(d.Episode); e > n.EpisodeEnd {
			n.EpisodeEnd = e
		}
	}
	return n, nil
}

func convert(d *xmlDoc) *NFO {
	n := &NFO{
		Kind:          d.XMLName.Local,
		Title:         clean(d.Title),
		OriginalTitle: clean(d.OriginalTitle),
		SortTitle:     clean(d.SortTitle),
		ShowTitle:     clean(d.ShowTitle),
		Year:          atoi(d.Year),
		Plot:          clean(firstNonEmpty(d.Plot, d.Outline)),
		Tagline:       clean(d.Tagline),
		ContentRating: ContentRating(firstNonEmpty(d.MPAA, d.Certification)),
		Premiered:     date(firstNonEmpty(d.Premiered, d.Aired, d.ReleaseDate)),
		Genres:        splitList(d.Genres),
		Studios:       splitList(d.Studios),
		Directors:     splitList(d.Directors),
		Writers:       splitList(append(d.Credits, d.Writers...)),
		Countries:     splitList(d.Countries),
		Tags:          splitList(d.Tags),
		Season:        atoi(d.Season),
		Episode:       atoi(d.Episode),
	}
	if n.Year == 0 && len(n.Premiered) >= 4 {
		n.Year = atoi(n.Premiered[:4])
	}
	if m := atoi(d.Runtime); m > 0 {
		n.RuntimeMs = int64(m) * 60_000
	}
	for i, a := range d.Actors {
		if name := clean(a.Name); name != "" {
			order := i
			if o, err := strconv.Atoi(strings.TrimSpace(a.Order)); err == nil {
				order = o
			}
			n.Actors = append(n.Actors, Actor{Name: name, Role: clean(a.Role), Order: order})
		}
	}
	for _, r := range d.Ratings {
		v, err := strconv.ParseFloat(strings.TrimSpace(r.Value), 64)
		if err != nil || v <= 0 {
			continue
		}
		if mx, err := strconv.ParseFloat(strings.TrimSpace(r.Max), 64); err == nil && mx > 0 && mx != 10 {
			v = v * 10 / mx
		}
		n.Ratings = append(n.Ratings, Rating{Source: strings.ToLower(strings.TrimSpace(r.Name)), Value: v,
			Votes: atoi(strings.ReplaceAll(r.Votes, ",", "")), Default: r.Default == "true"})
	}
	if len(n.Ratings) == 0 {
		if v, err := strconv.ParseFloat(strings.TrimSpace(d.Rating), 64); err == nil && v > 0 {
			n.Ratings = append(n.Ratings, Rating{Value: v, Votes: atoi(strings.ReplaceAll(d.Votes, ",", "")), Default: true})
		}
	}
	for _, id := range d.UniqueIDs {
		v := strings.TrimSpace(id.Value)
		switch strings.ToLower(strings.TrimSpace(id.Type)) {
		case "imdb":
			if imdbRe.MatchString(v) {
				n.IMDbID = imdbRe.FindString(v)
			}
		case "tmdb", "themoviedb":
			if x, err := strconv.ParseInt(v, 10, 64); err == nil && x > 0 {
				n.TMDbID = x
			}
		}
	}
	for _, v := range []string{d.IMDbID, d.IMDb, d.ID} {
		if n.IMDbID == "" && imdbRe.MatchString(v) {
			n.IMDbID = imdbRe.FindString(v)
		}
	}
	if n.TMDbID == 0 {
		if x, err := strconv.ParseInt(strings.TrimSpace(d.TMDbID), 10, 64); err == nil && x > 0 {
			n.TMDbID = x
		}
	}
	n.SetName = clean(firstNonEmpty(d.Set.Name, d.Set.Text))
	for _, v := range []string{d.Set.TMDbColID, d.TMDbColID, d.TMDbColID2, d.CollectionNum} {
		if x, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && x > 0 {
			n.TMDbCollectionID = x
			break
		}
	}
	return n
}

var ratedPrefix = regexp.MustCompile(`(?i)^rated\s+`)

// ContentRating normalizes certifications such as "Rated R", "US:PG-13" or
// "US:R / UK:15" to the bare rating of the first entry.
func ContentRating(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, " / "); i >= 0 {
		s = s[:i]
	}
	if c, r, ok := strings.Cut(s, ":"); ok && len(strings.TrimSpace(c)) <= 3 {
		s = r
	}
	s = ratedPrefix.ReplaceAllString(strings.TrimSpace(s), "")
	return strings.TrimSpace(s)
}

func date(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 10 && s[4] == '-' && s[7] == '-' {
		return s[:10]
	}
	return ""
}

// splitList trims values and splits the " / " joined lists some tools write
// into a single element, dropping duplicates.
func splitList(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range in {
		for _, part := range strings.Split(v, " / ") {
			p := clean(part)
			k := strings.ToLower(p)
			if p == "" || seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, p)
		}
	}
	return out
}

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func atoi(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// charsetReader handles the single-byte encodings NFO tools declare. Latin-1
// maps each byte to the code point of the same value; the Windows-1252 extras in
// 0x80 to 0x9F are rare in metadata and come through as their Latin-1 values.
func charsetReader(label string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "utf-8", "utf8", "us-ascii", "ascii":
		return in, nil
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
		data, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		b.Grow(len(data))
		for _, c := range data {
			b.WriteRune(rune(c))
		}
		return &b, nil
	}
	return nil, fmt.Errorf("unsupported charset %q", label)
}
