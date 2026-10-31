package enrich

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/tmdb"
)

func newPlan(c *candidate, tmdbID int64, key string) *plan {
	return &plan{c: c, set: map[string]any{}, next: map[string]string{}, tags: map[string][]tag{}, status: "matched", tmdbID: tmdbID, key: key}
}

// col writes a column when TMDb has a value and the column is empty or still
// holds what enrichment wrote last time.
func (p *plan) col(name, value string, v any) {
	if value == "" || p.c.locked[name] {
		return
	}
	cur := p.c.cur[name]
	if cur != "" && cur != p.c.written[name] {
		return
	}
	p.next[name] = value
	if cur != value {
		p.set[name] = v
	}
}

// title replaces an episode title that still comes from the file name.
func (p *plan) title(value string) {
	if value == "" || p.c.locked["title"] {
		return
	}
	cur := p.c.cur["title"]
	if p.c.source != "filename" && cur != p.c.written["title"] {
		return
	}
	p.next["title"] = value
	if cur != value {
		p.set["title"] = value
	}
	if p.c.source == "filename" {
		p.source = "tmdb"
	}
}

func (p *plan) tagList(kind string, values []tag) {
	if len(values) == 0 || p.c.locked[kind] {
		return
	}
	seen := map[string]bool{}
	var clean []tag
	var names []string
	for _, t := range values {
		t.value = strings.TrimSpace(t.value)
		k := strings.ToLower(t.value)
		if t.value == "" || seen[k] {
			continue
		}
		seen[k] = true
		clean = append(clean, t)
		names = append(names, t.value)
	}
	if len(clean) == 0 {
		return
	}
	joined := strings.Join(names, tagSep)
	cur, key := p.c.tags[kind], "tag:"+kind
	if cur != "" && !strings.EqualFold(cur, p.c.written[key]) && !strings.EqualFold(cur, joined) {
		return
	}
	p.next[key] = joined
	if cur != joined {
		p.tags[kind] = clean
	}
}

func (p *plan) setRating(avg float64, votes int) {
	if votes > 0 && avg > 0 && !p.c.locked["rating"] {
		p.rating = &[2]float64{avg, float64(votes)}
	}
}

func names(ns []tmdb.Named, limit int) []tag {
	var out []tag
	for _, n := range ns {
		if limit > 0 && len(out) == limit {
			break
		}
		out = append(out, tag{value: n.Name})
	}
	return out
}

func actors(cast []tmdb.Cast) []tag {
	var out []tag
	for _, a := range cast {
		if len(out) == maxActors {
			break
		}
		out = append(out, tag{value: a.Name, role: a.Character})
	}
	return out
}

func crew(cs []tmdb.Crew, keep func(tmdb.Crew) bool, limit int) []tag {
	var out []tag
	for _, c := range cs {
		if len(out) == limit {
			break
		}
		if keep(c) {
			out = append(out, tag{value: c.Name})
		}
	}
	return out
}

func idStr(id int64) string {
	if id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

func (e *Enricher) country() string {
	if e.Country != "" {
		return e.Country
	}
	return "US"
}

func (e *Enricher) applyMovie(ctx context.Context, c *candidate, m *tmdb.Movie, key string) error {
	p := newPlan(c, m.ID, key)
	p.reason = c.how
	p.col("tmdb_id", idStr(m.ID), m.ID)
	p.col("imdb_id", m.IMDbID, m.IMDbID)
	p.col("overview", m.Overview, m.Overview)
	p.col("tagline", m.Tagline, m.Tagline)
	cert := m.Certification(e.country())
	p.col("content_rating", cert, cert)
	p.col("premiere_date", m.ReleaseDate, m.ReleaseDate)
	if m.Runtime > 0 {
		ms := int64(m.Runtime) * 60_000
		p.col("runtime_ms", strconv.FormatInt(ms, 10), ms)
	}
	if m.Collection != nil && m.Collection.ID > 0 {
		p.col("tmdb_collection_id", idStr(m.Collection.ID), m.Collection.ID)
	}
	p.tagList("genre", names(m.Genres, 0))
	p.tagList("studio", names(m.Companies, maxStudios))
	p.tagList("country", names(m.Countries, 0))
	p.tagList("director", crew(m.Credits.Crew, func(x tmdb.Crew) bool { return x.Job == "Director" }, maxWriters))
	p.tagList("writer", crew(m.Credits.Crew, func(x tmdb.Crew) bool { return x.Department == "Writing" }, maxWriters))
	p.tagList("actor", actors(m.Credits.Cast))
	p.setRating(m.VoteAverage, m.VoteCount)
	if err := e.planArt(ctx, p, "poster", posterSize, m.PosterPath); err != nil {
		return err
	}
	if err := e.planArt(ctx, p, "backdrop", backdropSize, m.BackdropPath); err != nil {
		return err
	}
	return e.commit(ctx, []*plan{p})
}

func (e *Enricher) applyShow(ctx context.Context, c *candidate, s *tmdb.Show, key string) (int, error) {
	p := newPlan(c, s.ID, key)
	p.reason = c.how
	p.col("tmdb_id", idStr(s.ID), s.ID)
	p.col("imdb_id", s.ExternalIDs.IMDbID, s.ExternalIDs.IMDbID)
	p.col("overview", s.Overview, s.Overview)
	p.col("tagline", s.Tagline, s.Tagline)
	cert := s.Certification(e.country())
	p.col("content_rating", cert, cert)
	p.col("premiere_date", s.FirstAirDate, s.FirstAirDate)
	p.tagList("genre", names(s.Genres, 0))
	p.tagList("network", names(s.Networks, maxStudios))
	p.tagList("actor", actors(s.Credits.Cast))
	p.setRating(s.VoteAverage, s.VoteCount)
	if err := e.planArt(ctx, p, "poster", posterSize, s.PosterPath); err != nil {
		return 0, err
	}
	if err := e.planArt(ctx, p, "backdrop", backdropSize, s.BackdropPath); err != nil {
		return 0, err
	}
	plans := []*plan{p}

	seasons, err := e.children(ctx, c.id, "season")
	if err != nil {
		return 0, err
	}
	onTMDb := map[int]bool{}
	for _, r := range s.Seasons {
		onTMDb[r.SeasonNumber] = true
	}
	episodes := 0
	for _, sid := range seasons {
		sc := &candidate{id: sid}
		if err := e.load(ctx, sc); err != nil {
			return 0, err
		}
		eps, err := e.children(ctx, sid, "episode")
		if err != nil {
			return 0, err
		}
		var season *tmdb.Season
		if sc.hasSeason && onTMDb[sc.seasonNumber] {
			season, err = e.TMDb.Season(ctx, s.ID, sc.seasonNumber)
			if err != nil && !errors.Is(err, tmdb.ErrNotFound) {
				return 0, err
			}
		}
		if season != nil {
			sp := newPlan(sc, 0, "")
			sp.col("overview", season.Overview, season.Overview)
			if err := e.planArt(ctx, sp, "poster", posterSize, season.PosterPath); err != nil {
				return 0, err
			}
			plans = append(plans, sp)
		}
		byNum := map[int]*tmdb.Episode{}
		if season != nil {
			for i := range season.Episodes {
				byNum[season.Episodes[i].EpisodeNumber] = &season.Episodes[i]
			}
		}
		for _, eid := range eps {
			ec := &candidate{id: eid}
			if err := e.load(ctx, ec); err != nil {
				return 0, err
			}
			ep := byNum[ec.episodeNumber]
			if !ec.hasEp || ep == nil {
				up := newPlan(ec, 0, "")
				up.status, up.reason = "unmatched", "episode not on TMDb"
				up.next = ec.written
				plans = append(plans, up)
				continue
			}
			episodes++
			epl := newPlan(ec, 0, "")
			epl.title(ep.Name)
			epl.col("overview", ep.Overview, ep.Overview)
			epl.col("premiere_date", ep.AirDate, ep.AirDate)
			epl.setRating(ep.VoteAverage, ep.VoteCount)
			if err := e.planArt(ctx, epl, "thumb", stillSize, ep.StillPath); err != nil {
				return 0, err
			}
			plans = append(plans, epl)
		}
	}
	return episodes, e.commit(ctx, plans)
}

func (e *Enricher) children(ctx context.Context, parent int64, kind string) ([]int64, error) {
	rows, err := e.DB.QueryContext(ctx, `SELECT id FROM items WHERE parent_id = ? AND kind = ? AND missing_since IS NULL ORDER BY id`, parent, kind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// planArt downloads an image unless local art, a lock or an up-to-date copy
// makes it unnecessary.
func (e *Enricher) planArt(ctx context.Context, p *plan, kind, size, path string) error {
	if path == "" || !tmdb.ValidImagePath(path) || p.c.locked[kind] || p.c.locked["artwork"] {
		return nil
	}
	if a, ok := p.c.art[kind]; ok {
		if a.source == "local" {
			return nil
		}
		if a.source == "tmdb" && a.url == path && a.file != "" && fsafe.Within(e.Dir, a.file) {
			if _, err := os.Stat(a.file); err == nil {
				return nil
			}
		}
	}
	b, err := e.TMDb.Image(ctx, size, path)
	if errors.Is(err, tmdb.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("download %s: %w", kind, err)
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || (format != "jpeg" && format != "png") {
		return fmt.Errorf("TMDb %s for item %d is not a usable image", kind, p.c.id)
	}
	dir := filepath.Join(e.Dir, strconv.FormatInt(p.c.id, 10))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	ext := ".jpg"
	if format == "png" {
		ext = ".png"
	}
	base := strings.TrimSuffix(strings.TrimPrefix(path, "/"), filepath.Ext(path))
	out := filepath.Join(dir, kind+"-"+base+ext)
	tmp, err := os.CreateTemp(dir, ".dl-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if a, ok := p.c.art[kind]; ok && a.source == "tmdb" && a.file != "" && a.file != out && fsafe.Within(e.Dir, a.file) {
		_ = os.Remove(a.file)
	}
	p.art = append(p.art, artPlan{kind: kind, url: path, file: out})
	return nil
}
