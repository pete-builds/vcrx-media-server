package catalog

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Season is a holiday or time of year with its own Home row, such as
// Halloween. Seasons are matched offline from what the library already knows
// (genres, titles, taglines, plots and NFO tags), never from a web list.
type Season struct {
	ID    string `yaml:"id"`
	Title string `yaml:"title"`
	// Start and End are inclusive local dates: "MM-DD", or "thanksgiving"
	// (the fourth Thursday of November) with an optional offset in days, such
	// as "thanksgiving-3". A range that wraps the new year (12-26 to 01-05)
	// works.
	Start      string `yaml:"start"`
	End        string `yaml:"end"`
	Enabled    *bool  `yaml:"enabled"`
	SeasonRule `yaml:",inline"`
	// Family is the row for kids' profiles (a content-rating ceiling of PG or
	// below), shown to them instead of the main row and to everyone else
	// beside it.
	Family *FamilyRule `yaml:"family"`
}

// SeasonRule says which titles belong. A title matches when it has one of
// Genres or mentions one of Keywords, and then only if it has one of
// RequireGenres (when set) and none of ExcludeGenres.
type SeasonRule struct {
	Genres        []string `yaml:"genres"`
	Keywords      []string `yaml:"keywords"`
	RequireGenres []string `yaml:"require_genres"`
	ExcludeGenres []string `yaml:"exclude_genres"`
	// Kinds limits the row to movies or shows; empty means both.
	Kinds []string `yaml:"kinds"`
	// Sort is "rating" (the default: best match, then best rated) or "votes"
	// (most voted first, for blockbusters).
	Sort string `yaml:"sort"`
}

// FamilyRule is a season's row for kids' profiles.
type FamilyRule struct {
	Title      string `yaml:"title"`
	SeasonRule `yaml:",inline"`
}

// DefaultSeasons is the built-in table. A seasons.yaml in the config
// directory overrides it by id, adds to it, or turns a season off.
var DefaultSeasons = []Season{
	{
		ID: "halloween", Title: "Halloween Picks", Start: "10-01", End: "10-31",
		SeasonRule: SeasonRule{
			Genres:   []string{"Horror", "Thriller"},
			Keywords: []string{"halloween", "supernatural", "haunted", "haunting", "ghost", "ghosts", "witch", "witches", "vampire", "vampires", "zombie", "zombies", "werewolf", "poltergeist", "demon", "possession", "exorcism"},
			// Family films about Halloween have their own row beside this one.
			ExcludeGenres: []string{"Family", "Animation", "Kids", "Children"},
		},
		Family: &FamilyRule{Title: "Family Halloween", SeasonRule: SeasonRule{
			Keywords:      []string{"halloween", "ghost", "ghosts", "witch", "witches", "monster", "monsters", "haunted", "pumpkin", "spooky", "trick-or-treat", "trick or treat", "costume"},
			RequireGenres: []string{"Family", "Animation", "Kids", "Children"},
			ExcludeGenres: []string{"Horror"},
		}},
	},
	{
		ID: "thanksgiving", Title: "Thanksgiving Week", Start: "thanksgiving-3", End: "thanksgiving+3",
		SeasonRule: SeasonRule{Keywords: []string{"thanksgiving", "pilgrim", "pilgrims", "turkey dinner", "family reunion", "homecoming"}},
	},
	{
		ID: "christmas", Title: "Christmas and the Holidays", Start: "11-25", End: "12-31",
		SeasonRule: SeasonRule{
			Keywords: []string{"christmas", "xmas", "santa", "santa claus", "hanukkah", "chanukah", "kwanzaa", "reindeer", "mistletoe", "yuletide", "nutcracker", "north pole", "new year's eve", "scrooge", "elves"},
		},
		Family: &FamilyRule{Title: "Family Christmas", SeasonRule: SeasonRule{
			Keywords:      []string{"christmas", "xmas", "santa", "reindeer", "north pole", "elves", "snowman", "hanukkah", "nutcracker"},
			RequireGenres: []string{"Family", "Animation", "Kids", "Children"},
		}},
	},
	{
		ID: "valentines", Title: "Valentine's Day Romance", Start: "02-07", End: "02-14",
		SeasonRule: SeasonRule{Genres: []string{"Romance"}, Keywords: []string{"valentine", "valentine's"}},
	},
	{
		ID: "summer", Title: "Summer Blockbusters", Start: "06-01", End: "08-31",
		SeasonRule: SeasonRule{
			Genres: []string{"Action", "Adventure", "Science Fiction"}, ExcludeGenres: []string{"Documentary"},
			Kinds: []string{"movie"}, Sort: "votes",
		},
	},
}

// LoadSeasons reads seasons.yaml from the config directory, if there is one,
// and merges it over the built-in table. A broken file is an error, so a typo
// fails loudly at startup rather than quietly dropping a season.
func LoadSeasons(configDir string) ([]Season, error) {
	file := configDir + string(os.PathSeparator) + "seasons.yaml"
	b, err := os.ReadFile(file) //nolint:gosec // the operator chooses the config directory
	if errors.Is(err, os.ErrNotExist) {
		return DefaultSeasons, nil
	}
	if err != nil {
		return nil, err
	}
	return mergeSeasons(DefaultSeasons, b)
}

func mergeSeasons(base []Season, yamlDoc []byte) ([]Season, error) {
	var doc struct {
		Seasons []Season `yaml:"seasons"`
	}
	dec := yaml.NewDecoder(strings.NewReader(string(yamlDoc)))
	dec.KnownFields(true)
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("seasons.yaml: %w", err)
	}
	out := append([]Season{}, base...)
	for _, s := range doc.Seasons {
		if s.ID == "" {
			return nil, errors.New("seasons.yaml: every season needs an id")
		}
		replaced := false
		for i := range out {
			if out[i].ID != s.ID {
				continue
			}
			// A season named only to switch it off keeps the rest of its definition.
			if s.Start == "" && s.End == "" && s.Title == "" && s.Enabled != nil {
				out[i].Enabled = s.Enabled
			} else {
				out[i] = s
			}
			replaced = true
		}
		if !replaced {
			out = append(out, s)
		}
	}
	for _, s := range out {
		if err := s.validate(); err != nil {
			return nil, fmt.Errorf("seasons.yaml: season %q: %w", s.ID, err)
		}
	}
	return out, nil
}

func (s Season) validate() error {
	if s.Title == "" {
		return errors.New("needs a title")
	}
	for _, d := range []string{s.Start, s.End} {
		if _, err := seasonDate(d, 2026); err != nil {
			return err
		}
	}
	rules := []SeasonRule{s.SeasonRule}
	if s.Family != nil {
		if s.Family.Title == "" {
			return errors.New("family needs a title")
		}
		rules = append(rules, s.Family.SeasonRule)
	}
	for _, r := range rules {
		if len(r.Genres) == 0 && len(r.Keywords) == 0 {
			return errors.New("needs genres or keywords")
		}
		for _, k := range r.Kinds {
			if k != "movie" && k != "show" {
				return fmt.Errorf("kinds: %q is not movie or show", k)
			}
		}
		if r.Sort != "" && r.Sort != "rating" && r.Sort != "votes" {
			return fmt.Errorf("sort: %q is not rating or votes", r.Sort)
		}
	}
	return nil
}

// Active reports whether the season covers the local date of t.
func (s Season) Active(t time.Time) bool {
	if s.Enabled != nil && !*s.Enabled {
		return false
	}
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	// A range may wrap the new year, so try it starting this year and last year.
	for _, year := range []int{t.Year(), t.Year() - 1} {
		start, err1 := seasonDate(s.Start, year)
		end, err2 := seasonDate(s.End, year)
		if err1 != nil || err2 != nil {
			return false
		}
		if end.Before(start) {
			end, err2 = seasonDate(s.End, year+1)
			if err2 != nil {
				return false
			}
		}
		if !day.Before(start) && !day.After(end) {
			return true
		}
	}
	return false
}

// ActiveSeasons lists the seasons covering t's local date, in table order.
func ActiveSeasons(seasons []Season, t time.Time) []Season {
	var out []Season
	for _, s := range seasons {
		if s.Active(t) {
			out = append(out, s)
		}
	}
	return out
}

var seasonDateRE = regexp.MustCompile(`^(?:(\d{2})-(\d{2})|thanksgiving)([+-]\d{1,2})?$`)

// seasonDate resolves a season boundary in a given year, as a UTC midnight.
func seasonDate(s string, year int) (time.Time, error) {
	m := seasonDateRE.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s)))
	if m == nil {
		return time.Time{}, fmt.Errorf("date %q: want MM-DD or thanksgiving with an optional +N or -N", s)
	}
	var d time.Time
	if m[1] != "" {
		mo, _ := strconv.Atoi(m[1])
		dd, _ := strconv.Atoi(m[2])
		if mo < 1 || mo > 12 || dd < 1 || dd > 31 {
			return d, fmt.Errorf("date %q is not a calendar date", s)
		}
		d = time.Date(year, time.Month(mo), dd, 0, 0, 0, 0, time.UTC)
		if d.Month() != time.Month(mo) {
			return d, fmt.Errorf("date %q is not a calendar date", s)
		}
	} else {
		d = thanksgiving(year)
	}
	if m[3] != "" {
		n, _ := strconv.Atoi(m[3])
		d = d.AddDate(0, 0, n)
	}
	return d, nil
}

// thanksgiving is the fourth Thursday of November (the US holiday).
func thanksgiving(year int) time.Time {
	d := time.Date(year, time.November, 1, 0, 0, 0, 0, time.UTC)
	for d.Weekday() != time.Thursday {
		d = d.AddDate(0, 0, 1)
	}
	return d.AddDate(0, 0, 21)
}

// kidsRatings are the content-rating ceilings that make a profile a kids'
// profile for Home: it gets a season's family row instead of the main one.
var kidsRatings = map[string]bool{"G": true, "PG": true, "TV-Y": true, "TV-Y7": true, "TV-G": true, "TV-PG": true}

// keywordRE matches any keyword as a whole word or phrase, ignoring case.
func keywordRE(words []string) *regexp.Regexp {
	if len(words) == 0 {
		return nil
	}
	parts := make([]string, len(words))
	for i, w := range words {
		parts[i] = regexp.QuoteMeta(strings.ToLower(w))
	}
	return regexp.MustCompile(`(?i)(?:^|[^\p{L}\p{N}])(?:` + strings.Join(parts, "|") + `)(?:$|[^\p{L}\p{N}])`)
}
