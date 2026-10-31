package tmdb

import (
	"context"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Video is one entry of a title's /videos list.
type Video struct {
	Key         string `json:"key"`
	Site        string `json:"site"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Language    string `json:"iso_639_1"`
	Country     string `json:"iso_3166_1"`
	Official    bool   `json:"official"`
	PublishedAt string `json:"published_at"`
	Size        int    `json:"size"`
}

// youTubeKey is the only shape of YouTube video id kept. TMDb is a third
// party: a key that is anything else could carry a path, a query or markup
// into an embed URL or an Android intent, so it is dropped.
var youTubeKey = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// ValidYouTubeKey reports whether k has the shape of a YouTube video id.
func ValidYouTubeKey(k string) bool { return youTubeKey.MatchString(k) }

// Videos lists a movie's (kind "movie") or show's (kind "show") videos, in the
// client's language plus English and videos with no language.
func (c *Client) Videos(ctx context.Context, kind string, id int64) ([]Video, error) {
	p := "/movie/"
	if kind == "show" {
		p = "/tv/"
	}
	langs := []string{}
	if l := primaryLanguage(c.Language); l != "" && l != "en" {
		langs = append(langs, l)
	}
	langs = append(langs, "en", "null")
	var out struct {
		Results []Video `json:"results"`
	}
	q := url.Values{"include_video_language": {strings.Join(langs, ",")}}
	if err := c.get(ctx, p+strconv.FormatInt(id, 10)+"/videos", q, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

// primaryLanguage is "de" for "de-DE".
func primaryLanguage(tag string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(tag), "-")
	return strings.ToLower(l)
}

// BestTrailer picks the trailer a Trailer button should open from a /videos
// list: YouTube only, a Trailer before a Teaser (clips, featurettes and the
// rest are not trailers), then official before unofficial, then the
// configured language (e.g. en-US) before others, then the newest. ok is
// false when nothing qualifies.
func BestTrailer(videos []Video, language string) (Video, bool) {
	lang := primaryLanguage(language)
	var cands []Video
	for _, v := range videos {
		if !strings.EqualFold(v.Site, "YouTube") || !ValidYouTubeKey(v.Key) {
			continue
		}
		if v.Type != "Trailer" && v.Type != "Teaser" {
			continue
		}
		cands = append(cands, v)
	}
	if len(cands) == 0 {
		return Video{}, false
	}
	first := func(a, b bool) (bool, bool) { return a && !b, a != b }
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if less, differ := first(a.Type == "Trailer", b.Type == "Trailer"); differ {
			return less
		}
		if less, differ := first(a.Official, b.Official); differ {
			return less
		}
		if less, differ := first(strings.EqualFold(a.Language, lang), strings.EqualFold(b.Language, lang)); differ {
			return less
		}
		// TMDb's RFC 3339 times are all UTC, so they compare as strings.
		return a.PublishedAt > b.PublishedAt
	})
	return cands[0], true
}
