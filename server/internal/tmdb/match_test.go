package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Titles here are invented. The repo is public, so no real library titles.

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"The Lantern Keeper":        "lantern keeper",
		"Lantern Keeper":            "lantern keeper",
		"Salt & Signal":             "salt and signal",
		"Paper Harbor: Second Tide": "paper harbor second tide",
		"Paper.Harbor.Second.Tide":  "paper harbor second tide",
		"Don't Wake the Orchard":    "dont wake the orchard",
		"Café Sólo":                 "cafe solo",
		"A":                         "a",
		"  Lots   of   Spaces  ":    "lots of spaces",
	} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPick(t *testing.T) {
	remakes := []Result{
		{ID: 1, Title: "Paper Harbor", ReleaseDate: "1998-05-01"},
		{ID: 2, Title: "Paper Harbor", ReleaseDate: "2019-10-11"},
		{ID: 3, Title: "Paper Harbor Returns", ReleaseDate: "2001-01-01"},
	}
	cases := []struct {
		name    string
		results []Result
		title   string
		year    int
		want    int64
	}{
		{"exact title and year", remakes, "Paper Harbor", 2019, 2},
		{"the older remake", remakes, "Paper Harbor", 1998, 1},
		{"one year off", remakes, "Paper Harbor", 2020, 2},
		{"remake with no year is ambiguous", remakes, "Paper Harbor", 0, 0},
		{"wrong decade is refused", remakes, "Paper Harbor", 2010, 0},
		{"two years off is refused", remakes, "Paper Harbor", 2017, 0},
		{"two years off the other way is refused", remakes, "Paper Harbor", 2021, 0},
		{"sequel title is not the film", remakes, "Paper Harbor Returns", 2001, 3},
		{"a prefix is not a match", remakes, "Paper", 2019, 0},
		{"unique title needs no year", []Result{{ID: 9, Title: "The Lantern Keeper", ReleaseDate: "2011-02-02"}}, "Lantern Keeper", 0, 9},
		{"original title counts", []Result{{ID: 7, Title: "Night Ferry", OriginalTitle: "Nachtfähre", ReleaseDate: "2004-01-01"}}, "Nachtfähre", 2004, 7},
		{"show names count", []Result{{ID: 5, Name: "Glass Orchard", FirstAirDate: "2016-09-01"}}, "Glass Orchard", 2016, 5},
		{"same title and year twice is ambiguous", []Result{{ID: 1, Title: "Quillmark", ReleaseDate: "2005-01-01"}, {ID: 2, Title: "Quillmark", ReleaseDate: "2005-06-01"}}, "Quillmark", 2005, 0},
		{"duplicate hit of one id is fine", []Result{{ID: 4, Title: "Quillmark", ReleaseDate: "2005-01-01"}, {ID: 4, Title: "Quillmark", ReleaseDate: "2005-01-01"}}, "Quillmark", 0, 4},
		{"two one-off candidates is ambiguous", []Result{{ID: 1, Title: "Quillmark", ReleaseDate: "2004-01-01"}, {ID: 2, Title: "Quillmark", ReleaseDate: "2006-01-01"}}, "Quillmark", 2005, 0},
		{"nothing found", nil, "Anything", 2000, 0},
	}
	for _, c := range cases {
		got, reason := Pick(c.results, c.title, c.year)
		if got != c.want {
			t.Errorf("%s: got %d (%s), want %d", c.name, got, reason, c.want)
		}
		if got == 0 && reason == "" {
			t.Errorf("%s: no match must give a reason", c.name)
		}
	}
}

func fake(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("v3key", "en-US")
	c.BaseURL, c.ImageURL, c.Interval = srv.URL, srv.URL+"/img", time.Millisecond
	return c
}

func TestAuthModes(t *testing.T) {
	var gotKey, gotAuth string
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotAuth = r.URL.Query().Get("api_key"), r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"results":[]}`))
	})
	if _, err := c.Search(context.Background(), "movie", "x", 0); err != nil {
		t.Fatal(err)
	}
	if gotKey != "v3key" || gotAuth != "" {
		t.Fatalf("v3 key: api_key=%q auth=%q", gotKey, gotAuth)
	}
	c.Key = strings.Repeat("h", 20) + "." + strings.Repeat("p", 60) + ".sig"
	if _, err := c.Search(context.Background(), "movie", "x", 0); err != nil {
		t.Fatal(err)
	}
	if gotKey != "" || gotAuth != "Bearer "+c.Key {
		t.Fatalf("read token: api_key=%q auth=%q", gotKey, gotAuth)
	}
}

func TestRetriesAndErrors(t *testing.T) {
	var calls atomic.Int32
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/movie/401"):
			w.WriteHeader(http.StatusUnauthorized)
		case strings.Contains(r.URL.Path, "/movie/404"):
			w.WriteHeader(http.StatusNotFound)
		case calls.Add(1) == 1:
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			_, _ = w.Write([]byte(`{"id":12,"title":"Lantern Keeper"}`))
		}
	})
	ctx := context.Background()
	m, err := c.Movie(ctx, 12)
	if err != nil || m.Title != "Lantern Keeper" || calls.Load() != 2 {
		t.Fatalf("429 then 200: m=%+v err=%v calls=%d", m, err, calls.Load())
	}
	if _, err := c.Movie(ctx, 401); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("401: %v", err)
	}
	if _, err := c.Movie(ctx, 404); !errors.Is(err, ErrNotFound) {
		t.Fatalf("404: %v", err)
	}
}

func TestImagePathIsConfined(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("x")) })
	for _, p := range []string{"../etc/passwd", "/a/../../b.jpg", "//evil.example/x.jpg", "/x.svg", "x.jpg", ""} {
		if _, err := c.Image(context.Background(), "w780", p); err == nil {
			t.Errorf("Image(%q) was allowed", p)
		}
	}
	if _, err := c.Image(context.Background(), "w780", "/abcDEF123_-.jpg"); err != nil {
		t.Errorf("valid path refused: %v", err)
	}
}
