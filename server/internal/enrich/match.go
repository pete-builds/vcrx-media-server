package enrich

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/tmdb"
)

// resolve finds the TMDb id: an id from an NFO or an admin is trusted, a match
// enrichment made itself is reused while the title and year are unchanged, and
// otherwise the title is searched.
func (e *Enricher) resolve(ctx context.Context, c *candidate) (int64, string, error) {
	if c.itemTMDb > 0 && c.written["tmdb_id"] != strconv.FormatInt(c.itemTMDb, 10) {
		return c.itemTMDb, "", nil
	}
	if c.status == "matched" && c.keySame && c.stateTMDb > 0 {
		return c.stateTMDb, "", nil
	}
	if c.imdb != "" && c.written["imdb_id"] != c.imdb {
		id, err := e.TMDb.FindIMDb(ctx, c.kind, c.imdb)
		if err == nil {
			return id, "", nil
		}
		if !errors.Is(err, tmdb.ErrNotFound) {
			return 0, "", err
		}
	}
	return e.match(ctx, c)
}

// maxTieBreak caps how many tied candidates get their details fetched.
const maxTieBreak = 6

// match searches by title and year. A tie is settled by evidence from the files
// first (a movie's runtime, a show's seasons and episode counts), then by a
// clear popularity lead; otherwise the item stays unmatched.
func (e *Enricher) match(ctx context.Context, c *candidate) (int64, string, error) {
	title, country := tmdb.Qualifier(c.title)
	list, reason, err := e.TMDb.Candidates(ctx, c.kind, title, c.year, country)
	if err != nil || len(list) == 0 {
		return 0, reason, err
	}
	if len(list) == 1 {
		return list[0].ID, "", nil
	}
	if len(list) > maxTieBreak {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Popularity > list[j].Popularity })
		list = list[:maxTieBreak]
	}
	fits, evidence, err := e.fitting(ctx, c, list)
	if err != nil {
		return 0, "", err
	}
	if evidence != "" {
		if len(fits) == 1 {
			c.how = "picked by " + evidence
			return fits[0].ID, "", nil
		}
		if len(fits) == 0 {
			return 0, "no result with this title fits the " + evidence, nil
		}
		list = fits
	}
	if top, ok := tmdb.Standout(list); ok {
		c.how = "picked by popularity"
		if evidence != "" {
			c.how = "picked by " + evidence + " and popularity"
		}
		return top.ID, "", nil
	}
	if c.year > 0 {
		return 0, "several results share this title and year", nil
	}
	return 0, "several results share this title and there is no year to choose by", nil
}

// fitting keeps the candidates consistent with the files on disk and names the
// evidence used, or returns no evidence when there is none to check.
func (e *Enricher) fitting(ctx context.Context, c *candidate, list []tmdb.Result) ([]tmdb.Result, string, error) {
	switch c.kind {
	case "movie":
		disk, _ := strconv.ParseInt(c.cur["runtime_ms"], 10, 64)
		if disk <= 0 || c.written["runtime_ms"] == c.cur["runtime_ms"] {
			return nil, "", nil
		}
		var fits []tmdb.Result
		for _, r := range list {
			m, err := e.TMDb.Movie(ctx, r.ID)
			if errors.Is(err, tmdb.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, "", err
			}
			if m.Runtime > 0 && absDiff(int64(m.Runtime)*60_000, disk) <= runtimeSlack.Milliseconds() {
				fits = append(fits, r)
			}
		}
		return fits, "runtime", nil
	case "show":
		episodes, err := e.diskSeasons(ctx, c.id)
		if err != nil || len(episodes) == 0 {
			return nil, "", err
		}
		var fits []tmdb.Result
		for _, r := range list {
			s, err := e.TMDb.Show(ctx, r.ID)
			if errors.Is(err, tmdb.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, "", err
			}
			counts := map[int]int{}
			for _, sr := range s.Seasons {
				counts[sr.SeasonNumber] = sr.EpisodeCount
			}
			ok := true
			for season, maxEp := range episodes {
				if n, found := counts[season]; !found || n < maxEp {
					ok = false
					break
				}
			}
			if ok {
				fits = append(fits, r)
			}
		}
		return fits, "seasons and episodes on disk", nil
	}
	return nil, "", nil
}

// runtimeSlack allows for credits, studio logos and cuts that differ a little.
const runtimeSlack = 10 * time.Minute

func absDiff(a, b int64) int64 {
	if a > b {
		return a - b
	}
	return b - a
}

// diskSeasons maps each regular season on disk (specials excluded) to its
// highest episode number.
func (e *Enricher) diskSeasons(ctx context.Context, show int64) (map[int]int, error) {
	out := map[int]int{}
	err := each(ctx, e.DB, func(rows *sql.Rows) error {
		var season, ep int
		if err := rows.Scan(&season, &ep); err != nil {
			return err
		}
		out[season] = ep
		return nil
	}, `SELECT se.season_number, MAX(IFNULL(ep.episode_number_end, ep.episode_number)) FROM items se
		JOIN items ep ON ep.parent_id = se.id AND ep.kind = 'episode' AND ep.missing_since IS NULL
		WHERE se.parent_id = ? AND se.kind = 'season' AND se.season_number > 0 AND ep.episode_number IS NOT NULL
		GROUP BY se.season_number`, show)
	return out, err
}
