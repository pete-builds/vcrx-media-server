package enrich

import (
	"context"
	"errors"
	"testing"
	"time"
)

// An ambiguous title stays unmatched until an admin picks; the pick fills it at
// once and later passes keep it.
func TestFixMatchPicksAndSticks(t *testing.T) {
	v := setup(t)
	v.f.responses["/movie/1"] = `{"id":1,"title":"Paper Harbor","overview":"The 1998 one.","release_date":"1998-05-01"}`
	lib := v.library("movie")
	id := v.item(lib, "movie", nil, "k", "Paper Harbor", nil, nil, nil)
	if st := v.run(); st.Unmatched != 1 {
		t.Fatalf("expected unmatched first: %+v", st)
	}
	choices, err := v.e.Search(context.Background(), id, "", 0)
	if err != nil || len(choices) != 2 || choices[0].TMDbID != 1 || choices[0].Year != 1998 || choices[1].Year != 2019 {
		t.Fatalf("search by the item's own title: %+v %v", choices, err)
	}
	st, err := v.e.Fix(context.Background(), id, 1)
	if err != nil || st.Matched != 1 {
		t.Fatalf("fix: %+v %v", st, err)
	}
	if got := v.str(`SELECT overview FROM items WHERE id = ?`, id); got != "The 1998 one." {
		t.Fatalf("overview after fix = %q", got)
	}
	if got := v.str(`SELECT status || ' ' || tmdb_id FROM tmdb_state WHERE item_id = ?`, id); got != "matched 1" {
		t.Fatalf("state after fix = %q", got)
	}
	// A refresh months later keeps the choice rather than searching again.
	v.now = v.now.Add(400 * 24 * time.Hour)
	v.run()
	if got := v.str(`SELECT status || ' ' || tmdb_id FROM tmdb_state WHERE item_id = ?`, id); got != "matched 1" {
		t.Fatalf("state after a refresh = %q", got)
	}
}

// A wrong automatic match is replaced by the admin's pick.
func TestFixMatchOverridesAnAutomaticMatch(t *testing.T) {
	v := setup(t)
	v.f.responses["/movie/602"] = `{"id":602,"title":"Iron Lantern","overview":"The obscure one.","release_date":"2019-03-01"}`
	lib := v.library("movie")
	id := v.item(lib, "movie", nil, "k", "Iron Lantern", 2019, nil, nil)
	v.run()
	if got := v.str(`SELECT overview FROM items WHERE id = ?`, id); got != "The famous one." {
		t.Fatalf("automatic match: %q", got)
	}
	if _, err := v.e.Fix(context.Background(), id, 602); err != nil {
		t.Fatal(err)
	}
	if got := v.str(`SELECT overview FROM items WHERE id = ?`, id); got != "The obscure one." {
		t.Fatalf("after fix: %q (the automatic match's values must be replaced)", got)
	}
	v.now = v.now.Add(400 * 24 * time.Hour)
	v.run()
	if got := v.str(`SELECT tmdb_id FROM tmdb_state WHERE item_id = ?`, id); got != "602" {
		t.Fatalf("a refresh went back to the automatic match: %s", got)
	}
}

func TestFixMatchRefusesEpisodes(t *testing.T) {
	v := setup(t)
	lib := v.library("show")
	show := v.item(lib, "show", nil, "s", "Glass Orchard", nil, nil, nil)
	season := v.item(lib, "season", show, "1", "Season 1", nil, 1, nil)
	ep := v.item(lib, "episode", season, "1", "Seedlings", nil, 1, 1)
	if _, err := v.e.Fix(context.Background(), ep, 300); !errors.Is(err, ErrNotMatchable) {
		t.Fatalf("episode fix: %v", err)
	}
	if _, err := v.e.Search(context.Background(), ep, "x", 0); !errors.Is(err, ErrNotMatchable) {
		t.Fatalf("episode search: %v", err)
	}
}
