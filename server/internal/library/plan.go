package library

import (
	"path"
	"sort"
	"strconv"
	"strings"
)

// plan is what the scanner intends to store for one video, worked out from
// names alone before anything is probed or written.
type plan struct {
	v       *videoFile
	dir     string
	base    string
	episode bool
	movie   Movie
	ep      Episode
	// explicit is a version label the file name states after " - ".
	explicit string
	// oneTitle is true when the folder holds this one title, so shared names
	// such as poster.jpg and movie.nfo belong to it.
	oneTitle bool
	siblings []string

	movieKey string
	showKey  string
	epKey    string

	// The plan whose names give an item its title, so the result does not
	// depend on the order probes finish in.
	movieRep, showRep, epRep *plan
}

// planFiles parses every walked video and picks, for each item, the file
// whose names define it.
func planFiles(lib Library, w *walker) []*plan {
	videos := append([]*videoFile(nil), w.videos...)
	sort.Slice(videos, func(i, j int) bool { return videos[i].rel < videos[j].rel })
	oneTitle := map[string]bool{}
	siblings := map[string][]string{}
	for _, v := range videos {
		d := parentRel(v.rel)
		if _, ok := oneTitle[d]; !ok {
			oneTitle[d], siblings[d] = folderTitles(w.listing(d), d)
		}
	}
	plans := make([]*plan, 0, len(videos))
	for _, v := range videos {
		p := &plan{v: v, dir: parentRel(v.rel), base: stripExt(path.Base(v.rel))}
		p.oneTitle, p.siblings = oneTitle[p.dir], siblings[p.dir]
		if ep, ok := ParseEpisodePath(v.rel, lib.Kind == KindMovie); ok {
			p.episode, p.ep = true, ep
			p.showKey = showKey(ep.Show, ep.ShowYear)
			p.epKey = episodeKey(ep, p.base)
		} else {
			p.movie = ParseMoviePath(v.rel, p.oneTitle)
			folder := ""
			if p.dir != "" {
				folder = path.Base(p.dir)
			}
			prefix, label := versionPrefix(p.base, folder)
			p.explicit = label
			p.movieKey = movieKey(lib.Grouping, p.dir, prefix, p.movie)
		}
		plans = append(plans, p)
	}
	pickRepresentatives(plans)
	return plans
}

// folderTitles reports whether a folder holds a single title, judged from the
// file names of its videos, and lists those videos' base names.
func folderTitles(l *listing, dir string) (bool, []string) {
	var names []string
	keys := map[string]bool{}
	for _, f := range l.files() {
		if !IsVideo(f) || strings.HasPrefix(f, ".") {
			continue
		}
		base := stripExt(f)
		names = append(names, base)
		// Size 0 makes any file with a "sample" word count as a sample here.
		if SkipFile(f, 0) != "" || obfuscated(base) {
			continue
		}
		if _, ok := ParseEpisodePath(f, true); ok {
			continue
		}
		m := ParseMovieName(base)
		if m.Title == "" {
			continue
		}
		keys[NormalizeTitle(m.Title)+":"+strconv.Itoa(m.Year)] = true
	}
	return dir != "" && len(keys) <= 1, names
}

func movieKey(grouping, dir, prefix string, m Movie) string {
	if grouping == "title_year" && m.Title != "" && m.Year > 0 {
		return "t:" + NormalizeTitle(m.Title) + ":" + strconv.Itoa(m.Year)
	}
	np := NormalizeTitle(prefix)
	if np == "" {
		np = strings.ToLower(prefix)
	}
	return "f:" + dir + "/" + np
}

func showKey(name string, year int) string {
	k := NormalizeTitle(name)
	if k == "" {
		k = "unknown"
	}
	if year > 0 {
		k += ":" + strconv.Itoa(year)
	}
	return k
}

func episodeKey(ep Episode, base string) string {
	switch {
	case ep.Episode > 0 && ep.EpisodeEnd > ep.Episode:
		return strconv.Itoa(ep.Episode) + "-" + strconv.Itoa(ep.EpisodeEnd)
	case ep.Episode > 0:
		return strconv.Itoa(ep.Episode)
	}
	t := NormalizeTitle(ep.Title)
	if t == "" {
		t = NormalizeTitle(base)
	}
	return "x:" + t
}

func pickRepresentatives(plans []*plan) {
	movies := map[string]*plan{}
	shows := map[string]*plan{}
	eps := map[string]*plan{}
	for _, p := range plans {
		if !p.episode {
			if movies[p.movieKey] == nil {
				movies[p.movieKey] = p
			}
			continue
		}
		// A show folder names a show better than a release name does.
		if r := shows[p.showKey]; r == nil || (r.ep.ShowDir == "" && p.ep.ShowDir != "") {
			shows[p.showKey] = p
		}
		k := p.showKey + "|" + strconv.Itoa(p.ep.Season) + "|" + p.epKey
		if r := eps[k]; r == nil || (r.ep.Title == "" && p.ep.Title != "") {
			eps[k] = p
		}
	}
	for _, p := range plans {
		if !p.episode {
			p.movieRep = movies[p.movieKey]
			continue
		}
		p.showRep = shows[p.showKey]
		p.epRep = eps[p.showKey+"|"+strconv.Itoa(p.ep.Season)+"|"+p.epKey]
	}
}

// movieTitle is never empty: an unparseable name falls back to the file name.
func (p *plan) movieTitle() string {
	if p.movie.Title != "" {
		return p.movie.Title
	}
	return p.base
}

func (p *plan) showTitle() string {
	if p.ep.Show != "" {
		return p.ep.Show
	}
	return "Unknown Show"
}

func (p *plan) episodeTitle() string {
	switch {
	case p.ep.Title != "":
		return p.ep.Title
	case p.ep.AirDate != "":
		return p.ep.AirDate
	case p.ep.Episode > 0 && p.ep.EpisodeEnd > p.ep.Episode:
		return "Episodes " + strconv.Itoa(p.ep.Episode) + "-" + strconv.Itoa(p.ep.EpisodeEnd)
	case p.ep.Episode > 0:
		return "Episode " + strconv.Itoa(p.ep.Episode)
	}
	return p.base
}

func seasonTitle(n int) string {
	if n == 0 {
		return "Specials"
	}
	return "Season " + strconv.Itoa(n)
}

// tags are the quality facts the file's own names state.
func (p *plan) tags() (MediaTags, string) {
	if p.episode {
		return p.ep.MediaTags, ""
	}
	return p.movie.MediaTags, p.movie.Edition
}
