package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/collections"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// With smart collections stamping labels, the automatic merge still keeps
// copies apart while their ratings or their own labels differ, a stamp alone
// never decides it, and once merged the rebuild stamps the merged movie, so a
// friend denied the stamped label can play neither file.
func TestMergeWithSmartStamps(t *testing.T) {
	doc := func(rating string, tags ...string) string {
		d := `<movie><title>Quorra Vale</title><year>2011</year><tmdbid>501</tmdbid><genre>Horror</genre><mpaa>` + rating + `</mpaa>`
		for _, tag := range tags {
			d += `<tag>` + tag + `</tag>`
		}
		return d + `</movie>`
	}
	files := map[string]string{}
	for k, v := range twoReleases {
		files[k] = v
	}
	files[nfo1080] = doc("R")
	files[nfo2160] = doc("NC-17")
	e := newEnv(t, KindMovie, "folder", files)
	dir := t.TempDir()
	rule := "collections:\n  - name: R-Rated Horror\n    library: " + e.lib.Name + "\n    smart:\n      all:\n        - genre: Horror\n        - content_rating: R\n    label: horror\n"
	if err := os.WriteFile(filepath.Join(dir, "horror.yaml"), []byte(rule), 0o644); err != nil {
		t.Fatal(err)
	}
	smart := &collections.SmartBuilder{DB: e.db, Dir: dir}
	ctx := context.Background()
	rebuild := func() {
		t.Helper()
		if _, err := smart.Build(ctx); err != nil {
			t.Fatal(err)
		}
	}
	movies := func() int { return e.count(`SELECT COUNT(*) FROM items WHERE kind = 'movie'`) }
	mergeApart := func(want string) {
		t.Helper()
		rep, err := MergeByTMDb(ctx, e.db, 0)
		if err != nil || rep.Merged != 0 || len(rep.Apart) != 1 || !strings.Contains(rep.Apart[0], want) {
			t.Fatalf("automatic merge with stamps live: %+v, %v; want apart for %q", rep, err, want)
		}
	}

	e.scan(Options{})
	rebuild()
	if got := e.strs(`SELECT il.source FROM item_labels il JOIN labels l ON l.id = il.label_id WHERE l.name = 'horror'`); !slices.Equal(got, []string{"collection"}) {
		t.Fatalf("stamps before any merge = %q, want the R copy stamped", got)
	}
	mergeApart("content rating")
	if movies() != 2 {
		t.Fatal("differently rated copies were merged")
	}

	// Same rating now, both stamped, but only one copy's NFO says adult.
	e.write(map[string]string{nfo2160: doc("R", "adult")})
	e.scan(Options{})
	rebuild()
	if n := e.count(`SELECT COUNT(*) FROM item_labels il JOIN labels l ON l.id = il.label_id WHERE l.name = 'horror' AND il.source = 'collection'`); n != 2 {
		t.Fatalf("both copies should carry the stamp, %d do", n)
	}
	mergeApart("labels")

	// They agree: the scan merges them, and the rebuild stamps the merged movie.
	e.write(map[string]string{nfo1080: doc("R", "adult")})
	e.scan(Options{})
	rebuild()
	if movies() != 1 {
		t.Fatal("copies that agree stayed apart")
	}
	merged := e.id(`SELECT id FROM items WHERE kind = 'movie'`)
	got := e.strs(`SELECT l.name || ':' || il.source FROM item_labels il JOIN labels l ON l.id = il.label_id WHERE il.item_id = ? ORDER BY l.name`, merged)
	if !slices.Equal(got, []string{"adult:manual", "horror:collection"}) {
		t.Fatalf("labels on the merged movie = %q", got)
	}
	cat := &catalog.Catalog{DB: e.db}
	for _, deny := range []string{"horror", "adult"} {
		pol := policy.Policy{UserID: 99, LibraryIDs: []int64{e.lib.ID}, DenyLabels: []string{deny}, AllowTranscode: true}
		for _, v := range e.strs(`SELECT id FROM media_versions WHERE item_id = ?`, merged) {
			id := e.id(`SELECT ?`, v)
			if _, _, err := cat.VersionForPlayback(ctx, pol, id); !errors.Is(err, catalog.ErrNotFound) {
				t.Fatalf("a friend denied %q can play version %d of the merged movie: %v", deny, id, err)
			}
		}
	}
}
