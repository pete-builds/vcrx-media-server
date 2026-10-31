package tmdb

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The fixtures are recorded in TMDb's /videos shape with invented names and
// keys; no test here touches the network.
func fixture(t *testing.T, name string) []Video {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Results []Video `json:"results"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out.Results
}

func TestBestTrailer(t *testing.T) {
	movie := fixture(t, "movie_videos.json")
	cases := []struct {
		name     string
		videos   []Video
		language string
		want     string
	}{
		// Official English trailers beat the teaser, the fan upload, the Vimeo
		// one and the malformed key; the newer of two official ones wins.
		{"english", movie, "en-US", "OffTrlrEn01"},
		{"german", movie, "de-DE", "OffTrlrDe01"},
		// A newer teaser does not beat an older trailer.
		{"trailer before teaser", only(movie, "tEaSer0001a", "OldTrlrEn01"), "en-US", "OldTrlrEn01"},
		// No French trailer: the newest official trailer in any language.
		{"no match for the language", movie, "fr-FR", "OffTrlrDe01"},
		{"unofficial when nothing is official", only(movie, "FanTrlrEn01", "ClipClipCl1"), "en-US", "FanTrlrEn01"},
		{"a teaser when there is no trailer", fixture(t, "tv_videos.json"), "en-US", "ShowTeaser1"},
		{"clips and featurettes are not trailers", only(movie, "Featurette1", "ClipClipCl1", "BehindScen1"), "en-US", ""},
		{"Vimeo and malformed keys never", only(movie, "123456789", "x/../embed?a"), "en-US", ""},
		{"nothing", nil, "en-US", ""},
	}
	for _, c := range cases {
		got, ok := BestTrailer(c.videos, c.language)
		if ok != (c.want != "") || got.Key != c.want {
			t.Errorf("%s: got %q (ok %v), want %q", c.name, got.Key, ok, c.want)
		}
	}
}

// Trailers ranks every candidate, so the pass can fall back past one YouTube
// no longer plays.
func TestTrailersRanked(t *testing.T) {
	var got []string
	for _, v := range Trailers(fixture(t, "movie_videos.json"), "en-US") {
		got = append(got, v.Key)
	}
	want := []string{"OffTrlrEn01", "OldTrlrEn01", "OffTrlrDe01", "FanTrlrEn01", "tEaSer0001a"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ranked %v, want %v", got, want)
	}
}

func only(vs []Video, keys ...string) []Video {
	var out []Video
	for _, v := range vs {
		for _, k := range keys {
			if v.Key == k {
				out = append(out, v)
			}
		}
	}
	return out
}

func TestValidYouTubeKey(t *testing.T) {
	for k, want := range map[string]bool{
		"OffTrlrEn01": true, "a-b_c-D_e1Z": true, "short": false, "OffTrlrEn01x": false,
		"x/../embed?": false, "abc def ghi": false, `"><script>`: false, "": false,
	} {
		if ValidYouTubeKey(k) != want {
			t.Errorf("ValidYouTubeKey(%q) = %v", k, !want)
		}
	}
}

// Videos asks the right path for movies and shows, in the configured language
// plus English and language-less videos, against a local fake of TMDb.
func TestVideosRequest(t *testing.T) {
	body, err := os.ReadFile("testdata/movie_videos.json")
	if err != nil {
		t.Fatal(err)
	}
	var paths, langs, includes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		langs = append(langs, r.URL.Query().Get("language"))
		includes = append(includes, r.URL.Query().Get("include_video_language"))
		_, _ = w.Write(body)
	}))
	defer srv.Close()
	c := New("fake-tmdb-key", "de-DE")
	c.BaseURL, c.Interval = srv.URL, 0
	vs, err := c.Videos(context.Background(), "movie", 9101)
	if err != nil || len(vs) != 10 {
		t.Fatalf("videos: %d, %v", len(vs), err)
	}
	c.Language = "en-US"
	if _, err := c.Videos(context.Background(), "show", 9202); err != nil {
		t.Fatal(err)
	}
	if paths[0] != "/movie/9101/videos" || paths[1] != "/tv/9202/videos" {
		t.Fatalf("paths = %q", paths)
	}
	if langs[0] != "de-DE" || includes[0] != "de,en,null" || includes[1] != "en,null" {
		t.Fatalf("language %q, include_video_language %q", langs, includes)
	}
}
