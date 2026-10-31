package catalog

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// seasonCacheTTL is how long a computed seasonal match list is reused. The
// match reads every title's plot, so it runs at most this often rather than on
// every Home request; a title added since shows up in its season within it.
const seasonCacheTTL = 15 * time.Minute

// seasonCache holds each rule's matches across all libraries, before any
// policy. Each request filters them through the caller's policy.
type seasonCache struct {
	mu      sync.Mutex
	at      time.Time
	corpus  []corpusItem
	matches map[string][]int64
}

// corpusItem is what seasonal matching reads of a movie or show.
type corpusItem struct {
	id     int64
	kind   string
	title  string
	text   string // title, original title, tagline, plot and NFO tags
	genres []string
	score  float64
	votes  int64
}

// seasonal builds a season's row; arg is the season id, with ":family" for
// its family row. Home asks only for seasons in progress; See all for any.
func (q *homeQuery) seasonal(arg string, forHome bool) (*builtRow, error) {
	id, variant, _ := strings.Cut(arg, ":")
	var season *Season
	for _, s := range q.c.seasons() {
		if s.ID == id {
			season = &s
			break
		}
	}
	if season == nil || (variant != "" && (variant != "family" || season.Family == nil)) {
		return nil, ErrNotFound
	}
	if forHome && !season.Active(q.today) {
		return nil, nil
	}
	rule, title := season.SeasonRule, season.Title
	if variant == "family" {
		rule, title = season.Family.SeasonRule, season.Family.Title
	}
	cands, err := q.c.seasonMatches(q, arg, rule)
	if err != nil {
		return nil, err
	}
	ids, err := q.visibleTop(cands)
	if err != nil {
		return nil, err
	}
	return &builtRow{HomeRow: HomeRow{ID: "seasonal:" + arg, Kind: "seasonal", Title: title, SeeAll: true}, ids: ids}, nil
}

// seasonMatches lists every title matching a rule, best first, from a corpus
// loaded at most once per seasonCacheTTL.
func (c *Catalog) seasonMatches(q *homeQuery, key string, rule SeasonRule) ([]int64, error) {
	c.seasonCache.mu.Lock()
	defer c.seasonCache.mu.Unlock()
	sc := &c.seasonCache
	if sc.corpus == nil || q.now.Sub(sc.at) > seasonCacheTTL || q.now.Before(sc.at) {
		corpus, err := c.loadCorpus(q)
		if err != nil {
			return nil, err
		}
		sc.corpus, sc.at, sc.matches = corpus, q.now, map[string][]int64{}
	}
	if m, ok := sc.matches[key]; ok {
		return m, nil
	}
	m := matchRule(sc.corpus, rule)
	sc.matches[key] = m
	return m, nil
}

func (c *Catalog) loadCorpus(q *homeQuery) ([]corpusItem, error) {
	rows, err := c.DB.QueryContext(q.ctx, `WITH `+ratedCTE+`
		SELECT i.id, i.kind, i.title, IFNULL(i.original_title, ''), IFNULL(i.tagline, ''), IFNULL(i.overview, ''),
			IFNULL((SELECT group_concat(t.value, '|') FROM item_tags t WHERE t.item_id = i.id AND t.kind = 'genre'), ''),
			IFNULL((SELECT group_concat(l.name, ' | ') FROM item_labels il JOIN labels l ON l.id = il.label_id WHERE il.item_id = i.id), ''),
			IFNULL(rated.score, 0), IFNULL(rated.votes, 0)
		FROM items i LEFT JOIN rated ON rated.id = i.id
		WHERE i.parent_id IS NULL AND i.kind IN ('movie', 'show') AND i.missing_since IS NULL`)
	if err != nil {
		return nil, err
	}
	var out []corpusItem
	err = each(rows, func() error {
		var it corpusItem
		var orig, tagline, overview, genres, labels string
		if err := rows.Scan(&it.id, &it.kind, &it.title, &orig, &tagline, &overview, &genres, &labels, &it.score, &it.votes); err != nil {
			return err
		}
		it.text = strings.Join([]string{it.title, orig, tagline, overview, labels}, " | ")
		if genres != "" {
			it.genres = strings.Split(genres, "|")
		}
		out = append(out, it)
		return nil
	})
	return out, err
}

// matchRule ranks the titles a rule matches: a keyword match and the rule's
// first genre count most, then rating (or votes, for blockbusters).
func matchRule(corpus []corpusItem, rule SeasonRule) []int64 {
	re := keywordRE(rule.Keywords)
	has := func(list []string, genres []string) (int, bool) {
		for i, want := range list {
			for _, g := range genres {
				if strings.EqualFold(g, want) {
					return i, true
				}
			}
		}
		return 0, false
	}
	type hit struct {
		it       corpusItem
		strength int
	}
	var hits []hit
	for _, it := range corpus {
		if len(rule.Kinds) > 0 {
			if _, ok := has(rule.Kinds, []string{it.kind}); !ok {
				continue
			}
		}
		strength := 0
		if i, ok := has(rule.Genres, it.genres); ok {
			strength++
			if i == 0 {
				strength++
			}
		}
		if re != nil && re.MatchString(it.text) {
			strength += 2
		}
		if strength == 0 {
			continue
		}
		if len(rule.RequireGenres) > 0 {
			if _, ok := has(rule.RequireGenres, it.genres); !ok {
				continue
			}
		}
		if _, ok := has(rule.ExcludeGenres, it.genres); ok {
			continue
		}
		hits = append(hits, hit{it, strength})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.strength != b.strength {
			return a.strength > b.strength
		}
		if rule.Sort == "votes" && a.it.votes != b.it.votes {
			return a.it.votes > b.it.votes
		}
		if a.it.score != b.it.score {
			return a.it.score > b.it.score
		}
		return strings.ToLower(a.it.title) < strings.ToLower(b.it.title)
	})
	ids := make([]int64, len(hits))
	for i, h := range hits {
		ids[i] = h.it.id
	}
	return ids
}
