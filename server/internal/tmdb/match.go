package tmdb

import (
	"context"
	"strings"
	"unicode"
)

// A wrong match is worse than none: it puts another film's poster and plot on
// the item. So a search result is accepted only when its title matches after
// normalising and the year rules below leave exactly one candidate. Anything
// else is reported as unmatched with a reason.

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

// Pick chooses the result for a title and year, or returns 0 and the reason none
// was safe. With a year, the result's year must equal it, or be one off when
// that leaves a single candidate (release dates differ by country). Without a
// year, exactly one result may carry the title.
func Pick(results []Result, title string, year int) (int64, string) {
	want := Normalize(title)
	if want == "" {
		return 0, "empty title"
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
	if len(exact) == 0 {
		return 0, "no result with this title"
	}
	if year <= 0 {
		if ids := distinct(exact); len(ids) == 1 {
			return ids[0], ""
		}
		return 0, "several results share this title and there is no year to choose by"
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
	if ids := distinct(same); len(ids) == 1 {
		return ids[0], ""
	} else if len(ids) > 1 {
		return 0, "several results share this title and year"
	}
	if ids := distinct(near); len(ids) == 1 {
		return ids[0], ""
	}
	return 0, "no result with this title in or next to this year"
}

func distinct(rs []Result) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, r := range rs {
		if !seen[r.ID] {
			seen[r.ID] = true
			out = append(out, r.ID)
		}
	}
	return out
}

// Match finds the TMDb id for a movie or show by title and year. It searches
// with the year first, then without it so a release one year off is found.
func (c *Client) Match(ctx context.Context, kind, title string, year int) (int64, string, error) {
	results, err := c.Search(ctx, kind, title, year)
	if err != nil {
		return 0, "", err
	}
	id, reason := Pick(results, title, year)
	if id != 0 || year <= 0 {
		return id, reason, nil
	}
	more, err := c.Search(ctx, kind, title, 0)
	if err != nil {
		return 0, "", err
	}
	id, reason = Pick(append(results, more...), title, year)
	return id, reason, nil
}
