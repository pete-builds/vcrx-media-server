package api

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
)

type layoutJSON struct {
	Rows []struct {
		Key     string `json:"key"`
		Title   string `json:"title"`
		Enabled bool   `json:"enabled"`
	} `json:"rows"`
	Source string `json:"source"`
}

// The Customize Home editors on the web and the TV share this API: a user's
// order and hidden rows are theirs alone, and the admin's default covers
// everyone without their own.
func TestHomeLayoutAPI(t *testing.T) {
	f := newFixture(t)
	get := func(tok, path string) layoutJSON {
		t.Helper()
		r := f.do("GET", path, tok, nil)
		if r.code != 200 {
			t.Fatalf("GET %s: %d %s", path, r.code, r.body)
		}
		var l layoutJSON
		r.json(t, &l)
		return l
	}
	l := get(f.friendTok, "/api/v1/layout/home")
	if l.Source != "default" || len(l.Rows) != len(catalog.HomeSlots) || l.Rows[0].Key != "continue_watching" || l.Rows[0].Title == "" {
		t.Fatalf("default layout = %+v", l)
	}

	r := f.do("PUT", "/api/v1/layout/home", f.friendTok, map[string]any{"rows": []map[string]any{
		{"key": "trending", "enabled": true}, {"key": "continue_watching", "enabled": false}}})
	if r.code != 200 {
		t.Fatalf("save: %d %s", r.code, r.body)
	}
	l = get(f.friendTok, "/api/v1/layout/home")
	if l.Source != "user" || l.Rows[0].Key != "trending" || l.Rows[1].Key != "continue_watching" || l.Rows[1].Enabled {
		t.Fatalf("saved layout = %+v", l)
	}
	if other := get(f.strangerTok, "/api/v1/layout/home"); other.Source != "default" {
		t.Fatalf("another user's layout changed: %+v", other)
	}
	for _, bad := range []any{
		map[string]any{"rows": []map[string]any{{"key": "nope", "enabled": true}}},
		map[string]any{"rows": []map[string]any{{"key": "trending", "enabled": true}, {"key": "trending", "enabled": true}}},
		map[string]any{"rows": "trending"},
	} {
		if r := f.do("PUT", "/api/v1/layout/home", f.friendTok, bad); r.code != 400 {
			t.Errorf("bad layout %v: %d %s", bad, r.code, r.body)
		}
	}

	// The server default is the admin's, on the LAN only.
	def := map[string]any{"rows": []map[string]any{{"key": "top_rated_movies", "enabled": true}}}
	if r := f.do("PUT", "/api/v1/admin/layout/home", f.friendTok, def); r.code != 403 {
		t.Fatalf("a friend set the server default: %d", r.code)
	}
	if r := f.do("PUT", "/api/v1/admin/layout/home", f.adminTok, def, "RemoteAddr", "8.8.8.8:5555"); r.code != 403 {
		t.Fatalf("server default set from a public address: %d", r.code)
	}
	if r := f.do("PUT", "/api/v1/admin/layout/home", f.adminTok, def); r.code != 200 {
		t.Fatalf("admin default: %d %s", r.code, r.body)
	}
	if l := get(f.strangerTok, "/api/v1/layout/home"); l.Source != "server" || l.Rows[0].Key != "top_rated_movies" {
		t.Fatalf("stranger under the server default: %+v", l)
	}
	if l := get(f.friendTok, "/api/v1/layout/home"); l.Source != "user" {
		t.Fatalf("the friend's own layout lost to the default: %+v", l)
	}
	if r := f.do("DELETE", "/api/v1/layout/home", f.friendTok, nil); r.code != 200 {
		t.Fatalf("reset: %d %s", r.code, r.body)
	}
	if l := get(f.friendTok, "/api/v1/layout/home"); l.Source != "server" {
		t.Fatalf("after reset: %+v", l)
	}
	if r := f.do("DELETE", "/api/v1/admin/layout/home", f.adminTok, nil); r.code != 200 {
		t.Fatalf("admin reset: %d", r.code)
	}
	if l := get(f.adminTok, "/api/v1/admin/layout/home"); l.Source != "default" {
		t.Fatalf("after admin reset: %+v", l)
	}

	// Home follows the layout: with Continue Watching off, a title in
	// progress no longer leads Home.
	f.do("PUT", "/api/v1/layout/home", f.friendTok, map[string]any{"rows": []map[string]any{{"key": "continue_watching", "enabled": false}}})
	if _, err := f.db.Exec(`INSERT INTO watch_state (user_id, item_id, position_ms, last_played_at, updated_at)
		SELECT id, ?, 600000, 1, 1 FROM users WHERE username = 'friend'`, f.movieID); err != nil {
		t.Fatal(err)
	}
	var home struct {
		Rows []struct{ ID string } `json:"rows"`
	}
	f.do("GET", "/api/v1/home", f.friendTok, nil).json(t, &home)
	for _, row := range home.Rows {
		if row.ID == "continue_watching" {
			t.Fatal("Continue Watching shows although the friend turned it off")
		}
	}
	f.do("DELETE", "/api/v1/layout/home", f.friendTok, nil)
	f.do("GET", "/api/v1/home", f.friendTok, nil).json(t, &home)
	if len(home.Rows) == 0 || home.Rows[0].ID != "continue_watching" {
		t.Fatalf("control: Continue Watching missing with the default layout: %+v", home)
	}
}

// See all answers for a row id, encoded as the web app sends it, and 404s for
// rows that do not exist or that the caller cannot see.
func TestHomeRowSeeAll(t *testing.T) {
	f := newFixture(t)
	var lib int64
	if err := f.db.QueryRow(`SELECT library_id FROM items WHERE id = ?`, f.movieID).Scan(&lib); err != nil {
		t.Fatal(err)
	}
	var row struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	r := f.do("GET", "/api/v1/rows/"+url.PathEscape(fmt.Sprintf("recently_added_movies:%d", lib)), f.friendTok, nil)
	if r.code != 200 {
		t.Fatalf("See all: %d %s", r.code, r.body)
	}
	r.json(t, &row)
	if row.Title != "Recently Added Movies" || len(row.Items) != 1 || row.Items[0].ID != f.movieID {
		t.Fatalf("See all = %+v", row)
	}
	if r := f.do("GET", "/api/v1/rows/"+url.PathEscape("genre:Drama"), f.friendTok, nil); r.code != 200 {
		t.Fatalf("genre See all: %d %s", r.code, r.body)
	}
	for _, id := range []string{"nope", "continue_watching", fmt.Sprintf("recently_added_movies:%d", lib+100)} {
		if r := f.do("GET", "/api/v1/rows/"+url.PathEscape(id), f.friendTok, nil); r.code != 404 {
			t.Errorf("See all %s: %d", id, r.code)
		}
	}
	// The stranger has no libraries, so no library's row exists for them.
	if r := f.do("GET", "/api/v1/rows/"+url.PathEscape(fmt.Sprintf("recently_added_movies:%d", lib)), f.strangerTok, nil); r.code != 404 {
		t.Errorf("stranger See all: %d", r.code)
	}
}
