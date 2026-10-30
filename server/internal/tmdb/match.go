package tmdb

import (
	"context"
	"sort"
	"strings"
	"unicode"
)

// A wrong match is worse than none: it puts another film's poster and plot on
// the item. So a search result is accepted only when its title matches after
// normalising and the year rules below leave exactly one candidate, or when
// evidence from the files (runtime, seasons and episode counts) or a clear
// popularity lead settles a tie. Anything else is reported as unmatched with a
// reason.

// Normalize lowercases a title, drops punctuation and a leading article, folds
// "&" into "and" and common accented letters into ASCII, and collapses spaces.
func Normalize(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "&", " and "))
	var b strings.Builder
	space := true
	for _, r := range s {
		if f, ok := fold[r]; ok {
			r = f
		}
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			space = false
		case r == '\'' || r == '’':
			// "Don't" and "Dont" are the same title.
		default:
			if !space {
				b.WriteByte(' ')
				space = true
			}
		}
	}
	out := strings.TrimSpace(b.String())
	for _, a := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(out, a) && len(out) > len(a) {
			out = out[len(a):]
			break
		}
	}
	return out
}

var fold = map[rune]rune{
	'à': 'a', 'á': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a', 'å': 'a', 'ç': 'c', 'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e',
	'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i', 'ñ': 'n', 'ò': 'o', 'ó': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o', 'ø': 'o',
	'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u', 'ý': 'y', 'ÿ': 'y',
}

// countryTags maps a trailing tag in a show's folder name to TMDb's origin_country.
var countryTags = map[string]string{"US": "US", "UK": "GB", "GB": "GB", "AU": "AU", "CA": "CA", "NZ": "NZ", "IE": "IE"}

// Qualifier splits a trailing country tag off a title: "Office (US)" and
// "Office US" give "Office" and "US". Without a tag the title comes back as is.
func Qualifier(title string) (string, string) {
	t := strings.TrimSpace(title)
	for tag, code := range countryTags {
		for _, suffix := range []string{" (" + tag + ")", " " + tag} {
			if strings.HasSuffix(t, suffix) && len(t) > len(suffix) {
				return strings.TrimSpace(strings.TrimSuffix(t, suffix)), code
			}
		}
	}
	return t, ""
}

// Shortlist returns the distinct results whose title matches and whose year
// fits: the same year when any result has it, otherwise one year off. Without
// a year every title match is listed. A country, when given, must be among a
// show's origin countries. An empty list comes with the reason.
func Shortlist(results []Result, title string, year int, country string) ([]Result, string) {
	want := Normalize(title)
	if want == "" {
		return nil, "empty title"
	}
	var exact []Result
	for _, r := range results {
		for _, t := range r.titles() {
			if Normalize(t) == want {
				exact = append(exact, r)
				break
			}
		}
	}
	exact = distinct(exact)
	if len(exact) == 0 {
		return nil, "no result with this title"
	}
	if country != "" {
		var keep []Result
		for _, r := range exact {
			for _, c := range r.OriginCountry {
				if c == country {
					keep = append(keep, r)
					break
				}
			}
		}
		if len(keep) == 0 {
			return nil, "no result with this title from " + country
		}
		exact = keep
	}
	if year <= 0 {
		return exact, ""
	}
	var same, near []Result
	for _, r := range exact {
		switch r.year() - year {
		case 0:
			same = append(same, r)
		case 1, -1:
			near = append(near, r)
		}
	}
	if len(same) > 0 {
		return same, ""
	}
	if len(near) > 0 {
		return near, ""
	}
	return nil, "no result with this title in or next to this year"
}

// Pick returns the one result Shortlist leaves, or 0 and the reason there is
// not exactly one.
func Pick(results []Result, title string, year int) (int64, string) {
	list, reason := Shortlist(results, title, year, "")
	switch {
	case len(list) == 1:
		return list[0].ID, ""
	case len(list) > 1 && year > 0:
		return 0, "several results share this title and year"
	case len(list) > 1:
		return 0, "several results share this title and there is no year to choose by"
	}
	return 0, reason
}

// Standout returns the result whose popularity is at least three times the
// next one's and which has some votes, or false when none stands out.
func Standout(list []Result) (Result, bool) {
	if len(list) == 0 {
		return Result{}, false
	}
	sorted := append([]Result(nil), list...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Popularity > sorted[j].Popularity })
	top := sorted[0]
	if top.VoteCount < 10 {
		return Result{}, false
	}
	if len(sorted) == 1 || top.Popularity >= 3*sorted[1].Popularity {
		return top, true
	}
	return Result{}, false
}

func distinct(rs []Result) []Result {
	seen := map[int64]bool{}
	var out []Result
	for _, r := range rs {
		if !seen[r.ID] {
			seen[r.ID] = true
			out = append(out, r)
		}
	}
	return out
}

// Candidates searches for a movie or show and returns Shortlist's result. It
// searches with the year first, then without it so a release one year off is
// found too.
func (c *Client) Candidates(ctx context.Context, kind, title string, year int, country string) ([]Result, string, error) {
	results, err := c.Search(ctx, kind, title, year)
	if err != nil {
		return nil, "", err
	}
	list, reason := Shortlist(results, title, year, country)
	if len(list) > 0 || year <= 0 {
		return list, reason, nil
	}
	more, err := c.Search(ctx, kind, title, 0)
	if err != nil {
		return nil, "", err
	}
	list, reason = Shortlist(append(results, more...), title, year, country)
	return list, reason, nil
}
