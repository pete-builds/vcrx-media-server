package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/collections"
)

const smartFile = `collections:
  - name: Dramas
    library: Movies
    smart:
      all:
        - genre: Drama
  - name: R-Rated Horror
    library: Movies
    smart:
      all:
        - genre: Horror
        - content_rating: R
    label: horror
    visible: { library: false, home: false, shared: false }
`

func (f *fixture) smart() *collections.SmartBuilder {
	f.t.Helper()
	dir := f.t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "movies.yaml"), []byte(smartFile), 0o600); err != nil {
		f.t.Fatal(err)
	}
	b := &collections.SmartBuilder{DB: f.db, Dir: dir}
	f.srv.SmartCollections = b
	return b
}

func (f *fixture) rebuild(b *collections.SmartBuilder) {
	f.t.Helper()
	if _, err := b.Build(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(q, args...); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
}

// friendSees reports whether the friend finds the movie by id and in the
// library listing; the two must agree.
func (f *fixture) friendSees() bool {
	f.t.Helper()
	detail := f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), f.friendTok, nil)
	var list struct{ Items []catalog.ItemSummary }
	f.do("GET", "/api/v1/libraries/1/items", f.friendTok, nil).json(f.t, &list)
	listed := false
	for _, it := range list.Items {
		listed = listed || it.ID == f.movieID
	}
	if (detail.code == 200) != listed {
		f.t.Fatalf("detail %d but listed %v", detail.code, listed)
	}
	return listed
}

// TestSmartLabelPolicy: a friend whose policy denies "horror" stops seeing a
// title the moment a rebuild finds it matches the horror rule, and sees it
// again when it stops matching. No one labels anything by hand.
func TestSmartLabelPolicy(t *testing.T) {
	f := newFixture(t)
	f.exec(`UPDATE user_policies SET deny_labels = '["horror"]' WHERE user_id = (SELECT id FROM users WHERE username = 'friend')`)
	b := f.smart()
	f.rebuild(b)
	if !f.friendSees() {
		t.Fatal("friend cannot see a drama")
	}

	// New metadata makes it R-rated horror (a rescan or TMDb would do this).
	f.exec(`INSERT INTO item_tags (item_id, kind, value) VALUES (?, 'genre', 'Horror')`, f.movieID)
	f.exec(`UPDATE items SET content_rating = 'R' WHERE id = ?`, f.movieID)
	f.rebuild(b)
	if f.friendSees() {
		t.Fatal("friend still sees a title the horror rule now labels")
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/versions/%d/file", f.v1080), f.friendTok, nil); r.code != 404 {
		t.Fatalf("friend can still stream it: %d", r.code)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), f.adminTok, nil); r.code != 200 {
		t.Fatalf("admin lost it: %d", r.code)
	}

	// Re-rated: it stops matching and comes back.
	f.exec(`UPDATE items SET content_rating = 'PG-13' WHERE id = ?`, f.movieID)
	f.rebuild(b)
	if !f.friendSees() {
		t.Fatal("friend does not see the title again after it stopped matching")
	}
}

// TestSmartHiddenCollections: a label-only collection is never listed, never
// named on a title, and cannot be opened; a visible one is all three.
func TestSmartHiddenCollections(t *testing.T) {
	f := newFixture(t)
	f.exec(`INSERT INTO item_tags (item_id, kind, value) VALUES (?, 'genre', 'Horror')`, f.movieID)
	f.exec(`UPDATE items SET content_rating = 'R' WHERE id = ?`, f.movieID)
	b := f.smart()
	f.rebuild(b)
	var hiddenID, shownID int64
	if err := f.db.QueryRow(`SELECT id FROM collections WHERE slug = 'yaml-r-rated-horror'`).Scan(&hiddenID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT id FROM collections WHERE slug = 'yaml-dramas'`).Scan(&shownID); err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{f.adminTok, f.friendTok} {
		var list struct{ Collections []catalog.Collection }
		f.do("GET", "/api/v1/libraries/1/collections", tok, nil).json(t, &list)
		if len(list.Collections) != 1 || list.Collections[0].ID != shownID {
			t.Fatalf("library collections: %+v", list.Collections)
		}
		var detail struct{ Collections []catalog.CollectionRef }
		f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), tok, nil).json(t, &detail)
		if len(detail.Collections) != 1 || detail.Collections[0].ID != shownID {
			t.Fatalf("item detail collections: %+v", detail.Collections)
		}
		if r := f.do("GET", fmt.Sprintf("/api/v1/collections/%d", hiddenID), tok, nil); r.code != 404 {
			t.Fatalf("hidden collection opened: %d", r.code)
		}
		if r := f.do("GET", fmt.Sprintf("/api/v1/collections/%d", shownID), tok, nil); r.code != 200 {
			t.Fatalf("visible collection: %d", r.code)
		}
	}
	// The hidden collection still exists, with its member, for the label.
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM collection_items WHERE collection_id = ?`, hiddenID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("hidden collection members: %d %v", n, err)
	}
}

// TestAdminSmartCollections: the admin route reports definitions and refused
// files; it is admin-only.
func TestAdminSmartCollections(t *testing.T) {
	f := newFixture(t)
	var out struct {
		Built       bool
		Files       []collections.FileReport
		Collections []collections.DefReport
	}
	f.do("GET", "/api/v1/admin/collections", f.adminTok, nil).json(t, &out)
	if out.Built {
		t.Fatalf("built before any build: %+v", out)
	}
	b := f.smart()
	if err := os.WriteFile(filepath.Join(b.Dir, "zz-broken.yaml"), []byte("collections:\n  - name: X\n    library: Movies\n    smart: {all: [{genre: [}]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.rebuild(b)
	r := f.do("GET", "/api/v1/admin/collections", f.adminTok, nil)
	if err := json.Unmarshal(r.body, &out); err != nil || r.code != 200 {
		t.Fatalf("%d %s", r.code, r.body)
	}
	if !out.Built || len(out.Files) != 2 || out.Files[1].Error == "" || len(out.Collections) != 2 ||
		out.Collections[1].Label != "horror" || out.Collections[1].Visible.Library {
		t.Fatalf("report: %s", r.body)
	}
	if r := f.do("GET", "/api/v1/admin/collections", f.friendTok, nil); r.code != 403 {
		t.Fatalf("friend read the admin route: %d", r.code)
	}
}
