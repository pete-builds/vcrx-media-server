package podcasts

import (
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/ianaindex"
)

// Feed is a podcast's RSS 2.0 channel with the iTunes and Podcasting 2.0
// fields VCRX uses.
type Feed struct {
	Title       string
	Author      string
	Description string
	Link        string
	Language    string
	Image       string
	Explicit    bool
	// NewURL is itunes:new-feed-url: the show moved. Recorded, never followed
	// on its own, since the feed's author wrote it.
	NewURL   string
	Episodes []Episode
}

// Episode is one RSS item with an audio (or video) enclosure.
type Episode struct {
	GUID        string
	Title       string
	Description string
	Published   time.Time
	Duration    int // seconds, 0 when unknown
	URL         string
	Type        string
	Length      int64
	Image       string
	Season      int
	Number      int
	EpisodeType string
	ChaptersURL string
	Explicit    bool
}

// Limits on what one feed may hold.
const (
	maxTitle       = 500
	maxDescription = 8000
	maxURL         = 2048
)

// ErrNotAFeed is a document that is not an RSS feed.
var ErrNotAFeed = errors.New("not an RSS feed")

type rssDoc struct {
	XMLName xml.Name   `xml:"rss"`
	Channel rssChannel `xml:"channel"`
}

type rssImage struct {
	URL  string `xml:"url"`
	Href string `xml:"href,attr"`
}

type rssChannel struct {
	Title       string     `xml:"title"`
	Link        []string   `xml:"link"`
	Description string     `xml:"description"`
	Summary     []string   `xml:"summary"`
	Language    string     `xml:"language"`
	Author      []string   `xml:"author"`
	Images      []rssImage `xml:"image"`
	Explicit    []string   `xml:"explicit"`
	NewFeedURL  string     `xml:"new-feed-url"`
	Items       []rssItem  `xml:"item"`
}

type rssEnclosure struct {
	URL    string `xml:"url,attr"`
	Type   string `xml:"type,attr"`
	Length string `xml:"length,attr"`
}

type rssChapters struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}

type rssGUID struct {
	Value string `xml:",chardata"`
}

type rssItem struct {
	Title       []string       `xml:"title"`
	GUID        rssGUID        `xml:"guid"`
	PubDate     string         `xml:"pubDate"`
	Description string         `xml:"description"`
	Summary     []string       `xml:"summary"`
	Encoded     string         `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
	Duration    string         `xml:"duration"`
	Enclosures  []rssEnclosure `xml:"enclosure"`
	Images      []rssImage     `xml:"image"`
	Season      []string       `xml:"season"`
	Episode     []string       `xml:"episode"`
	EpisodeType string         `xml:"episodeType"`
	Explicit    []string       `xml:"explicit"`
	Chapters    []rssChapters  `xml:"chapters"`
}

// Parse reads an RSS feed. Items without an enclosure are dropped, as are
// duplicates by GUID; at most maxEpisodes are kept, newest first.
func Parse(r io.Reader, maxEpisodes int) (*Feed, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = func(label string, in io.Reader) (io.Reader, error) {
		enc, err := ianaindex.IANA.Encoding(label)
		if err != nil || enc == nil {
			return nil, fmt.Errorf("the feed's charset %q is not supported", label)
		}
		return enc.NewDecoder().Reader(in), nil
	}
	var doc rssDoc
	if err := dec.Decode(&doc); err != nil {
		var se *xml.SyntaxError
		if errors.As(err, &se) || strings.Contains(err.Error(), "expected element type") {
			return nil, ErrNotAFeed
		}
		return nil, fmt.Errorf("%w: %w", ErrNotAFeed, err)
	}
	ch := doc.Channel
	f := &Feed{
		Title:       clip(clean(ch.Title), maxTitle),
		Description: clip(plain(firstNonEmpty(ch.Summary, ch.Description)), maxDescription),
		Language:    clip(clean(ch.Language), 32),
		Author:      clip(clean(firstNonEmpty(ch.Author)), maxTitle),
		Explicit:    explicit(ch.Explicit),
		NewURL:      httpURL(ch.NewFeedURL),
	}
	for _, l := range ch.Link {
		if u := httpURL(l); u != "" {
			f.Link = u
			break
		}
	}
	f.Image = image(ch.Images)
	if f.Title == "" {
		return nil, fmt.Errorf("%w: the channel has no title", ErrNotAFeed)
	}
	seen := map[string]bool{}
	for _, it := range ch.Items {
		var enc rssEnclosure
		for _, e := range it.Enclosures {
			if httpURL(e.URL) != "" {
				enc = e
				break
			}
		}
		if enc.URL == "" {
			continue
		}
		ep := Episode{
			Title:       clip(clean(firstNonEmpty(it.Title)), maxTitle),
			GUID:        clip(clean(it.GUID.Value), 512),
			URL:         httpURL(enc.URL),
			Type:        clip(strings.ToLower(clean(enc.Type)), 100),
			Description: clip(plain(firstNonEmpty(it.Summary, it.Description, it.Encoded)), maxDescription),
			Duration:    parseDuration(it.Duration),
			Image:       image(it.Images),
			Season:      positive(firstNonEmpty(it.Season)),
			Number:      positive(firstNonEmpty(it.Episode)),
			EpisodeType: episodeType(it.EpisodeType),
			Explicit:    f.Explicit || explicit(it.Explicit),
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(enc.Length), 10, 64); err == nil && n > 0 {
			ep.Length = n
		}
		if t, ok := parseDate(it.PubDate); ok {
			ep.Published = t
		}
		for _, c := range it.Chapters {
			if u := httpURL(c.URL); u != "" {
				ep.ChaptersURL = u
				break
			}
		}
		if ep.GUID == "" {
			ep.GUID = ep.URL
		}
		if ep.Title == "" {
			ep.Title = "Untitled episode"
		}
		if seen[ep.GUID] {
			continue
		}
		seen[ep.GUID] = true
		f.Episodes = append(f.Episodes, ep)
	}
	sort.SliceStable(f.Episodes, func(i, j int) bool { return f.Episodes[i].Published.After(f.Episodes[j].Published) })
	if maxEpisodes > 0 && len(f.Episodes) > maxEpisodes {
		f.Episodes = f.Episodes[:maxEpisodes]
	}
	return f, nil
}

func firstNonEmpty(lists ...any) string {
	for _, l := range lists {
		switch v := l.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return v
			}
		case []string:
			for _, s := range v {
				if strings.TrimSpace(s) != "" {
					return s
				}
			}
		}
	}
	return ""
}

func image(imgs []rssImage) string {
	for _, i := range imgs {
		if u := httpURL(i.Href); u != "" {
			return u
		}
		if u := httpURL(i.URL); u != "" {
			return u
		}
	}
	return ""
}

func explicit(vals []string) bool {
	for _, v := range vals {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "yes", "true", "explicit":
			return true
		}
	}
	return false
}

func episodeType(v string) string {
	switch t := strings.ToLower(strings.TrimSpace(v)); t {
	case "trailer", "bonus":
		return t
	}
	return "full"
}

func positive(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 || n > 1_000_000 {
		return 0
	}
	return n
}

// httpURL is s when it is an http(s) URL of a sane length, else "".
func httpURL(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxURL {
		return ""
	}
	low := strings.ToLower(s)
	if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") {
		return s
	}
	return ""
}

// parseDuration reads itunes:duration: seconds, MM:SS or HH:MM:SS.
func parseDuration(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	parts := strings.Split(v, ":")
	if len(parts) > 3 {
		return 0
	}
	total := 0.0
	for _, p := range parts {
		n, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || n < 0 {
			return 0
		}
		total = total*60 + n
	}
	if total > 7*24*3600 {
		return 0
	}
	return int(total)
}

var dateLayouts = []string{
	time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 02 Jan 2006 15:04 -0700", "2 Jan 2006 15:04:05 -0700", "02 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05", time.RFC3339, "2006-01-02T15:04:05", "2006-01-02",
}

// parseDate reads an RSS pubDate, which feeds write in many shapes.
func parseDate(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, v); err == nil {
			return t, true
		}
	}
	// Day names some feeds spell out in full, or get wrong.
	if i := strings.Index(v, ","); i > 0 && i < 10 {
		return parseDate(strings.TrimSpace(v[i+1:]))
	}
	return time.Time{}, false
}

var (
	tags   = regexp.MustCompile(`(?s)<[^>]*>`)
	blocks = regexp.MustCompile(`(?i)<\s*(br|/p|/div|/li)\s*/?>`)
	spaces = regexp.MustCompile(`[ \t\r\f\v]+`)
	lines  = regexp.MustCompile(`\n\s*\n\s*\n+`)
)

// plain turns a feed's HTML description into plain text.
func plain(s string) string {
	s = blocks.ReplaceAllString(s, "\n")
	s = tags.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = spaces.ReplaceAllString(s, " ")
	s = lines.ReplaceAllString(s, "\n\n")
	out := make([]string, 0, 8)
	for _, l := range strings.Split(s, "\n") {
		out = append(out, strings.TrimSpace(l))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// clean is plain text on one line: no tags, no control characters.
func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, html.UnescapeString(tags.ReplaceAllString(s, "")))
	return strings.TrimSpace(spaces.ReplaceAllString(s, " "))
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + "…"
}
