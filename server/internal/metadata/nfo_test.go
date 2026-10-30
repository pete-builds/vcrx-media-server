package metadata

import (
	"path/filepath"
	"reflect"
	"testing"
)

func read(t *testing.T, name string) *NFO {
	t.Helper()
	n, err := Read(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if n == nil {
		t.Fatalf("%s: nil NFO", name)
	}
	return n
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", what, got, want)
	}
}

func TestKodiMovie(t *testing.T) {
	n := read(t, "movie_kodi.nfo")
	eq(t, "kind", n.Kind, KindMovie)
	eq(t, "structured", n.Structured(), true)
	eq(t, "title", n.Title, "Night of the Living Dead")
	eq(t, "sort", n.SortTitle, "Night of the Living Dead 1")
	eq(t, "year", n.Year, 1968)
	eq(t, "plot prefers plot over outline", n.Plot, "A group of strangers barricade themselves in a farmhouse.")
	eq(t, "tagline", n.Tagline, "They keep coming back.")
	eq(t, "rating", n.ContentRating, "Unrated")
	eq(t, "runtime", n.RuntimeMs, int64(96*60_000))
	eq(t, "premiered", n.Premiered, "1968-10-04")
	eq(t, "genres dedup case-insensitively", n.Genres, []string{"Horror", "Thriller"})
	eq(t, "studios", n.Studios, []string{"Image Ten"})
	eq(t, "directors", n.Directors, []string{"George A. Romero"})
	eq(t, "writers", n.Writers, []string{"John A. Russo", "George A. Romero"})
	eq(t, "countries", n.Countries, []string{"United States of America"})
	eq(t, "tags", n.Tags, []string{"public domain", "monochrome"})
	eq(t, "actors", n.Actors, []Actor{{"Duane Jones", "Ben", 0}, {"Judith O'Dea", "Barbra", 1}})
	eq(t, "imdb", n.IMDbID, "tt0063350")
	eq(t, "tmdb", n.TMDbID, int64(10331))
	eq(t, "set", n.SetName, "Living Dead Collection")
	eq(t, "set id", n.TMDbCollectionID, int64(261382))
	eq(t, "ratings", n.Ratings, []Rating{
		{Source: "imdb", Value: 7.8, Votes: 140000, Default: true},
		{Source: "themoviedb", Value: 7.5, Votes: 2000},
		{Source: "rottentomatoes", Value: 9.6},
	})
}

func TestOldStyleMovie(t *testing.T) {
	n := read(t, "movie_old.nfo")
	eq(t, "title", n.Title, "His Girl Friday")
	eq(t, "rating strips country and keeps the first", n.ContentRating, "Approved")
	eq(t, "imdb from <id>", n.IMDbID, "tt0032599")
	eq(t, "tmdb from <tmdbid>", n.TMDbID, int64(3085))
	eq(t, "set as text", n.SetName, "Screwball Comedies")
	eq(t, "genres split on slash", n.Genres, []string{"Comedy", "Romance"})
	eq(t, "writer", n.Writers, []string{"Charles Lederer"})
	eq(t, "single rating", n.Ratings, []Rating{{Value: 7.9, Votes: 62000, Default: true}})
}

func TestShowAndEpisodes(t *testing.T) {
	s := read(t, "tvshow.nfo")
	eq(t, "kind", s.Kind, KindTVShow)
	eq(t, "title", s.Title, "The Bexbury Mysteries")
	eq(t, "tmdb ignores tvdb", s.TMDbID, int64(424242))
	eq(t, "imdb", s.IMDbID, "tt9999991")
	eq(t, "rating", s.ContentRating, "TV-14")

	e := read(t, "episode.nfo")
	eq(t, "kind", e.Kind, KindEpisode)
	eq(t, "numbers", [3]int{e.Season, e.Episode, e.EpisodeEnd}, [3]int{1, 3, 0})
	eq(t, "aired", e.Premiered, "2020-03-15")
	eq(t, "year from aired", e.Year, 2020)
	eq(t, "show", e.ShowTitle, "The Bexbury Mysteries")

	m := read(t, "episode_multi.nfo")
	eq(t, "multi numbers", [3]int{m.Season, m.Episode, m.EpisodeEnd}, [3]int{2, 1, 2})
	eq(t, "multi title is the opener", m.Title, "Two Part Opener (1)")
}

func TestLatin1(t *testing.T) {
	n := read(t, "movie_latin1.nfo")
	eq(t, "title", n.Title, "Nosferatu, eine Symphonie des Grauens")
	eq(t, "plot", n.Plot, "Ein Grübler.")
	eq(t, "year", n.Year, 1922)
}

// Scene NFOs, bare URLs, HTML and broken XML must never fail a scan: they give
// at most an id.
func TestNonKodiFiles(t *testing.T) {
	cases := []struct {
		file, imdb string
		tmdb       int64
	}{
		{"scene.nfo", "tt0052077", 0},
		{"scene_noid.nfo", "", 0},
		{"url_only.nfo", "tt0052077", 0},
		{"tmdb_url.nfo", "", 10513},
		{"broken.nfo", "tt0013442", 0},
		{"html.nfo", "tt0032599", 0},
	}
	for _, c := range cases {
		n := read(t, c.file)
		if n.Structured() {
			t.Errorf("%s: parsed as Kodi XML", c.file)
		}
		eq(t, c.file+" imdb", n.IMDbID, c.imdb)
		eq(t, c.file+" tmdb", n.TMDbID, c.tmdb)
		eq(t, c.file+" title", n.Title, "")
	}
	for _, junk := range [][]byte{nil, []byte("   "), []byte("<"), []byte("<<<>>>"), []byte("\xef\xbb\xbf<movie"), {0xff, 0xfe, 0x00}} {
		if n := Parse(junk); n == nil || n.Structured() {
			t.Errorf("Parse(%q) = %+v", junk, n)
		}
	}
}

func TestContentRating(t *testing.T) {
	cases := map[string]string{
		"Rated R": "R", "rated PG-13": "PG-13", "US:PG-13": "PG-13", "US:R / UK:18": "R", "TV-MA": "TV-MA",
		"de:FSK 16": "FSK 16", "  NC-17 ": "NC-17", "": "", "Not Rated": "Not Rated",
	}
	for in, want := range cases {
		if got := ContentRating(in); got != want {
			t.Errorf("ContentRating(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadMissing(t *testing.T) {
	if _, err := Read(filepath.Join(t.TempDir(), "nope.nfo")); err == nil {
		t.Fatal("missing file read")
	}
}
