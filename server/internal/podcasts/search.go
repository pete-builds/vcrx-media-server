package podcasts

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// ErrSearchOff is a search while podcasts.search is off.
var ErrSearchOff = errors.New("podcast search is off")

// SearchResult is a show found in Apple's directory. No artwork: a client
// would have to load it from Apple.
type SearchResult struct {
	Title    string `json:"title"`
	Author   string `json:"author"`
	FeedURL  string `json:"feed_url"`
	Genre    string `json:"genre"`
	Explicit bool   `json:"explicit"`
}

// SearchDirectory asks the iTunes Search API for podcasts matching term.
// Only the search words leave the server.
func (s *Service) SearchDirectory(ctx context.Context, v Viewer, term string) ([]SearchResult, error) {
	if !s.Search {
		return nil, ErrSearchOff
	}
	q := url.Values{"media": {"podcast"}, "entity": {"podcast"}, "limit": {"25"}, "term": {term}}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.SearchURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.UserAgent)
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search: %s", reason(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search answered %d", resp.StatusCode)
	}
	var body struct {
		Results []struct {
			CollectionName         string `json:"collectionName"`
			ArtistName             string `json:"artistName"`
			FeedURL                string `json:"feedUrl"`
			PrimaryGenreName       string `json:"primaryGenreName"`
			CollectionExplicitness string `json:"collectionExplicitness"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	out := []SearchResult{}
	for _, r := range body.Results {
		feed := httpURL(r.FeedURL)
		if feed == "" {
			continue
		}
		ex := strings.EqualFold(r.CollectionExplicitness, "explicit")
		if ex && v.NoExplicit {
			continue
		}
		out = append(out, SearchResult{Title: clip(clean(r.CollectionName), maxTitle), Author: clip(clean(r.ArtistName), maxTitle),
			FeedURL: feed, Genre: clip(clean(r.PrimaryGenreName), 100), Explicit: ex})
	}
	return out, nil
}

// MaxOPMLFeeds caps how many feeds one OPML import subscribes to.
const MaxOPMLFeeds = 100

// ParseOPML lists the feed URLs in an OPML document, at most MaxOPMLFeeds.
func ParseOPML(r io.Reader) ([]string, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	var urls []string
	seen := map[string]bool{}
	sawOPML := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("not an OPML file: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch strings.ToLower(se.Name.Local) {
		case "opml":
			sawOPML = true
		case "outline":
			for _, a := range se.Attr {
				if strings.EqualFold(a.Name.Local, "xmlUrl") {
					if u := httpURL(a.Value); u != "" && !seen[u] {
						seen[u] = true
						urls = append(urls, u)
					}
				}
			}
		}
		if len(urls) >= MaxOPMLFeeds {
			break
		}
	}
	if !sawOPML {
		return nil, errors.New("not an OPML file")
	}
	return urls, nil
}

// WriteOPML writes subscriptions (title and feed URL) as OPML 2.0.
func WriteOPML(w io.Writer, title string, subs [][2]string) error {
	type outline struct {
		Type   string `xml:"type,attr"`
		Text   string `xml:"text,attr"`
		Title  string `xml:"title,attr"`
		XMLURL string `xml:"xmlUrl,attr"`
	}
	doc := struct {
		XMLName xml.Name `xml:"opml"`
		Version string   `xml:"version,attr"`
		Head    struct {
			Title string `xml:"title"`
		} `xml:"head"`
		Body struct {
			Outlines []outline `xml:"outline"`
		} `xml:"body"`
	}{Version: "2.0"}
	doc.Head.Title = title
	for _, s := range subs {
		doc.Body.Outlines = append(doc.Body.Outlines, outline{Type: "rss", Text: s[0], Title: s[0], XMLURL: s[1]})
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return err
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}
