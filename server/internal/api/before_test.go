package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/preroll"
)

type beforeList struct {
	Clips []struct {
		Kind, Title, URL string
	}
}

func (b beforeList) titles() string {
	var out []string
	for _, c := range b.Clips {
		out = append(out, c.Kind+":"+c.Title)
	}
	return strings.Join(out, ",")
}

// Before a movie: off by default; trailers only for other movies the profile
// may see and has not watched; the pre-roll last; nothing for an episode or
// an invited device; and settings saved one field at a time.
func TestBeforeMovie(t *testing.T) {
	t.Parallel()
	w := newWalkFixture(t)
	w.become("user")
	w.open()
	dir := filepath.Join(t.TempDir(), "Pre-Roll")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Retro Countdown.mp4"), []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	w.srv.Prerolls = preroll.Folder{Dir: dir}
	get := func(tok string, id int64) beforeList {
		t.Helper()
		var b beforeList
		r := w.do("GET", fmt.Sprintf("/api/v1/items/%d/before", id), tok, nil)
		if r.code != 200 {
			t.Fatalf("before %d: %d %s", id, r.code, r.body)
		}
		r.json(t, &b)
		return b
	}
	set := func(body map[string]any) playbackSettings {
		t.Helper()
		var out playbackSettings
		r := w.do("PUT", "/api/v1/me/playback-settings", w.friendTok, body)
		if r.code != 200 {
			t.Fatalf("settings %v: %d %s", body, r.code, r.body)
		}
		r.json(t, &out)
		return out
	}

	// The walk sets trailers to 3; back to the default first.
	if _, err := w.db.Exec(`UPDATE users SET before_movie_trailers = 0 WHERE id = ?`, w.friendID); err != nil {
		t.Fatal(err)
	}
	if got := get(w.friendTok, w.movieID).titles(); got != "" {
		t.Fatalf("off by default, got %s", got)
	}

	// One field at a time: the others keep their values.
	set(map[string]any{"skip_markers": "auto"})
	got := set(map[string]any{"before_movie_trailers": 3})
	if got.SkipMarkers != "auto" || got.BeforeMovieTrailers != 3 || got.BeforeMoviePreroll {
		t.Fatalf("after trailers: %+v", got)
	}
	got = set(map[string]any{"before_movie_preroll": true})
	if got.SkipMarkers != "auto" || got.BeforeMovieTrailers != 3 || !got.BeforeMoviePreroll {
		t.Fatalf("after pre-roll: %+v", got)
	}
	if r := w.do("PUT", "/api/v1/me/playback-settings", w.friendTok, map[string]any{"before_movie_trailers": 4}); r.code != 400 {
		t.Fatalf("4 trailers: %d", r.code)
	}

	// Both other movies' trailers in some order, then the pre-roll.
	b := get(w.friendTok, w.movieID)
	if len(b.Clips) != 3 || b.Clips[2].Kind != "preroll" || b.Clips[2].Title != "Retro Countdown" {
		t.Fatalf("clips: %s", b.titles())
	}
	if g := b.titles(); !strings.Contains(g, "trailer:Zebra Night") || !strings.Contains(g, "trailer:Zebra Lent") {
		t.Fatalf("trailers: %s", g)
	}
	// Never the movie's own trailer.
	if g := get(w.friendTok, w.horrorMovie).titles(); strings.Contains(g, "Zebra Night") {
		t.Fatalf("its own trailer: %s", g)
	}
	// A watched title's trailer is not offered.
	if r := w.do("PUT", fmt.Sprintf("/api/v1/items/%d/watched", w.lentMovie), w.friendTok, map[string]any{"played": true}); r.code != 204 {
		t.Fatalf("mark watched: %d", r.code)
	}
	if g := get(w.friendTok, w.movieID).titles(); strings.Contains(g, "Zebra Lent") {
		t.Fatalf("a watched title's trailer: %s", g)
	}
	// At most the number chosen.
	set(map[string]any{"before_movie_trailers": 1})
	if b := get(w.friendTok, w.ratedMovie); len(b.Clips) != 2 {
		t.Fatalf("one trailer and the pre-roll: %s", b.titles())
	}
	// Nothing before an episode.
	if g := get(w.friendTok, w.horrorEpisode).titles(); g != "" {
		t.Fatalf("before an episode: %s", g)
	}

	// The pre-roll plays, in ranges, by its listed name only.
	url := b.Clips[len(b.Clips)-1].URL
	if r := w.do("GET", url, w.friendTok, nil); r.code != 200 || string(r.body) != "0123456789" {
		t.Fatalf("pre-roll: %d %q", r.code, r.body)
	}
	if r := w.do("GET", url, w.friendTok, nil, "Range", "bytes=2-4"); r.code != 206 || string(r.body) != "234" {
		t.Fatalf("pre-roll range: %d %q", r.code, r.body)
	}
	for _, bad := range []string{"../Pre-Roll/Retro%20Countdown.mp4", "missing.mp4", ""} {
		if r := w.do("GET", "/api/v1/preroll?name="+bad, w.friendTok, nil); r.code != 404 {
			t.Errorf("pre-roll %q: %d", bad, r.code)
		}
	}

	// An invited device: an empty list and no bare file.
	d := w.invite(map[string]any{"name": "Guest TV", "library_ids": []int64{w.movies}})
	if _, err := w.db.Exec(`UPDATE users SET before_movie_trailers = 3, before_movie_preroll = 1 WHERE id = ?`, d.userID); err != nil {
		t.Fatal(err)
	}
	if g := get(d.token, w.movieID).titles(); g != "" {
		t.Fatalf("invited device: %s", g)
	}
	if r := w.do("GET", url, d.token, nil); r.code != 403 {
		t.Fatalf("invited device pre-roll: %d", r.code)
	}
}
