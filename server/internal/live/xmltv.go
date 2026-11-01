package live

import (
	"bufio"
	"compress/gzip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/ianaindex"
)

// GuideChannel is an XMLTV <channel>: its id and display names.
type GuideChannel struct {
	ID    string
	Names []string
}

// Programme is an XMLTV <programme>.
type Programme struct {
	Channel    string
	Start      time.Time
	Stop       time.Time
	Title      string
	SubTitle   string
	Desc       string
	Categories []string
	Episode    string // S01E02, or the guide's own on-screen form
}

// maxGuideBytes caps what a guide may make the server read once unzipped.
const maxGuideBytes = 512 << 20

// ErrNotXMLTV is a body whose root element is not <tv>.
var ErrNotXMLTV = errors.New("not an XMLTV guide: the root element is not <tv>")

// ParseXMLTV reads an XMLTV guide, gzipped or not, one element at a time:
// onChannel for each <channel> (the format lists them all before the first
// programme) and onProgramme for each <programme> with a valid start and
// stop. An error from either stops the read and is returned. Text in any
// charset the guide declares is read as UTF-8.
func ParseXMLTV(r io.Reader, onChannel func(GuideChannel), onProgramme func(Programme) error) error {
	br := bufio.NewReader(r)
	if magic, err := br.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return fmt.Errorf("unzip the guide: %w", err)
		}
		defer func() { _ = gz.Close() }()
		br = bufio.NewReader(gz)
	}
	dec := xml.NewDecoder(io.LimitReader(br, maxGuideBytes))
	dec.CharsetReader = func(label string, in io.Reader) (io.Reader, error) {
		enc, err := ianaindex.IANA.Encoding(label)
		if err != nil || enc == nil {
			return nil, fmt.Errorf("the guide's charset %q is not supported", label)
		}
		return enc.NewDecoder().Reader(in), nil
	}
	sawRoot := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			if !sawRoot {
				return ErrNotXMLTV
			}
			return nil
		}
		if err != nil {
			if !sawRoot {
				return ErrNotXMLTV
			}
			return fmt.Errorf("read the guide: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if !sawRoot {
			if se.Name.Local != "tv" {
				return ErrNotXMLTV
			}
			sawRoot = true
			continue
		}
		switch se.Name.Local {
		case "channel":
			var c xmlChannel
			if err := dec.DecodeElement(&c, &se); err != nil {
				return fmt.Errorf("read the guide: %w", err)
			}
			gc := GuideChannel{ID: strings.TrimSpace(c.ID)}
			for _, n := range c.Names {
				if n = strings.TrimSpace(n); n != "" {
					gc.Names = append(gc.Names, clip(n))
				}
			}
			if gc.ID != "" && onChannel != nil {
				onChannel(gc)
			}
		case "programme":
			var p xmlProgramme
			if err := dec.DecodeElement(&p, &se); err != nil {
				return fmt.Errorf("read the guide: %w", err)
			}
			prog, ok := p.programme()
			if !ok {
				continue
			}
			if err := onProgramme(prog); err != nil {
				return err
			}
		default:
			if err := dec.Skip(); err != nil {
				return fmt.Errorf("read the guide: %w", err)
			}
		}
	}
}

type xmlChannel struct {
	ID    string   `xml:"id,attr"`
	Names []string `xml:"display-name"`
}

type xmlEpisode struct {
	System string `xml:"system,attr"`
	Value  string `xml:",chardata"`
}

type xmlProgramme struct {
	Start      string       `xml:"start,attr"`
	Stop       string       `xml:"stop,attr"`
	Channel    string       `xml:"channel,attr"`
	Titles     []string     `xml:"title"`
	SubTitles  []string     `xml:"sub-title"`
	Descs      []string     `xml:"desc"`
	Categories []string     `xml:"category"`
	Episodes   []xmlEpisode `xml:"episode-num"`
}

func (p xmlProgramme) programme() (Programme, bool) {
	start, err1 := parseXMLTVTime(p.Start)
	stop, err2 := parseXMLTVTime(p.Stop)
	title := first(p.Titles)
	if err1 != nil || err2 != nil || !stop.After(start) || title == "" || strings.TrimSpace(p.Channel) == "" {
		return Programme{}, false
	}
	out := Programme{Channel: strings.TrimSpace(p.Channel), Start: start, Stop: stop, Title: clip(title),
		SubTitle: clip(first(p.SubTitles)), Desc: clipTo(first(p.Descs), 4096), Episode: episode(p.Episodes)}
	for _, c := range p.Categories {
		if c = strings.TrimSpace(c); c != "" && len(out.Categories) < 8 {
			out.Categories = append(out.Categories, clip(c))
		}
	}
	return out, true
}

// parseXMLTVTime reads "20261101203000 -0400"; the offset, the seconds and the
// minutes may each be left off (UTC, :00).
func parseXMLTVTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	stamp, zone, _ := strings.Cut(s, " ")
	for _, layout := range []string{"20060102150405", "200601021504", "2006010215"} {
		if len(stamp) != len(layout) {
			continue
		}
		loc := time.UTC
		if zone != "" {
			z, err := time.Parse("-0700", strings.TrimSpace(zone))
			if err != nil {
				return time.Time{}, fmt.Errorf("time zone %q", zone)
			}
			loc = z.Location()
		}
		return time.ParseInLocation(layout, stamp, loc)
	}
	return time.Time{}, fmt.Errorf("time %q", s)
}

// episode prefers the guide's on-screen form; else it turns xmltv_ns, which
// counts from zero ("0.4.0/1" is season 1, episode 5), into S01E05.
func episode(eps []xmlEpisode) string {
	for _, e := range eps {
		if e.System == "onscreen" && strings.TrimSpace(e.Value) != "" {
			return clipTo(strings.TrimSpace(e.Value), 32)
		}
	}
	for _, e := range eps {
		if e.System != "xmltv_ns" {
			continue
		}
		parts := strings.Split(strings.ReplaceAll(e.Value, " ", ""), ".")
		if len(parts) < 2 {
			continue
		}
		num := func(p string) (int, bool) {
			p, _, _ = strings.Cut(p, "/")
			n, err := strconv.Atoi(p)
			return n + 1, err == nil && n >= 0
		}
		s, okS := num(parts[0])
		ep, okE := num(parts[1])
		switch {
		case okS && okE:
			return fmt.Sprintf("S%02dE%02d", s, ep)
		case okE:
			return fmt.Sprintf("E%02d", ep)
		}
	}
	return ""
}

func first(list []string) string {
	for _, s := range list {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return ""
}

// clipTo keeps at most n bytes of s without splitting a character.
func clipTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
