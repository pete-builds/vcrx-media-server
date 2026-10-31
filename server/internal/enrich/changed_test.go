package enrich

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/collections"
)

// An enrichment pass that brings the genre a smart rule wants stamps the rule's
// label at once, through Changed, without waiting for the builder's timer. So
// does a Fix Match.
func TestEnrichmentTriggersSmartCollections(t *testing.T) {
	v := setup(t)
	lib := v.library("movie")
	id := v.item(lib, "movie", nil, "lantern keeper|2011", "Lantern Keeper", 2011, nil, nil)
	other := v.item(lib, "movie", nil, "unknown reel|2011", "Unknown Reel", 2011, nil, nil) // TMDb finds nothing for it
	dir := t.TempDir()
	rule := "collections:\n  - name: Mysteries\n    library: movies\n    smart: {all: [{genre: Mystery}]}\n    label: mystery\n" +
		"    visible: {library: false, home: false, shared: false}\n"
	if err := os.WriteFile(filepath.Join(dir, "m.yaml"), []byte(rule), 0o600); err != nil {
		t.Fatal(err)
	}
	smart := &collections.SmartBuilder{DB: v.conn, Dir: dir, Debounce: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go smart.Run(ctx, time.Hour) // the timer never fires during the test
	stampedOn := func(item int64) bool {
		return v.str(`SELECT il.source FROM item_labels il JOIN labels l ON l.id = il.label_id WHERE il.item_id = ? AND l.name = 'mystery'`, item) == "collection"
	}
	stamped := func() bool { return stampedOn(id) }
	waitUntil(t, func() bool { return smart.Status() != nil }) // the startup build ran, before any genre
	if stamped() {
		t.Fatal("stamped before enrichment")
	}
	v.e.Changed = smart.Trigger
	go v.e.Run(ctx, 0, time.Hour)
	waitUntil(t, stamped)

	// The title TMDb could not find is fixed by hand to one with the genre:
	// stamped at once too.
	waitUntil(t, func() bool { return v.str(`SELECT status FROM tmdb_state WHERE item_id = ?`, other) != "" })
	if stampedOn(other) {
		t.Fatal("unmatched title stamped")
	}
	if _, err := v.e.Fix(context.Background(), other, 100); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return stampedOn(other) })
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	for range 500 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}
