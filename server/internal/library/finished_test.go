package library

import "testing"

// OnFinished fires after every successful scan, new titles or not, so smart
// collections rebuild after each one.
func TestOnFinishedAfterEveryScan(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", map[string]string{"Paper Lantern (1931)/Paper.Lantern.1931.mkv": "a"})
	calls := 0
	e.sc.OnFinished = func(lib Library, run Run) {
		if run.Status != StatusOK {
			t.Errorf("called for a %s run", run.Status)
		}
		calls++
	}
	e.scan(Options{})
	e.scan(Options{})
	if calls != 2 {
		t.Fatalf("OnFinished called %d times, want 2", calls)
	}
}

// An NFO tag takes over the same label a smart collection stamped, so the
// collection can never clear it; a hand-set label stays the admin's.
func TestNFOLabelTakesOverCollectionStamp(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", map[string]string{"Paper Lantern (1931)/Paper.Lantern.1931.mkv": "a"})
	e.scan(Options{})
	for _, q := range []string{
		`INSERT INTO labels (name) VALUES ('horror'), ('keep')`,
		`INSERT INTO item_labels (item_id, label_id, source) SELECT i.id, l.id, 'collection' FROM items i, labels l WHERE l.name = 'horror'`,
		`INSERT INTO item_labels (item_id, label_id, source) SELECT i.id, l.id, 'manual' FROM items i, labels l WHERE l.name = 'keep'`,
	} {
		if _, err := e.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	e.write(map[string]string{"Paper Lantern (1931)/movie.nfo": "<movie><title>Paper Lantern</title><year>1931</year><tag>horror</tag><tag>keep</tag></movie>"})
	e.scan(Options{})
	got := e.strs(`SELECT l.name || ':' || il.source FROM item_labels il JOIN labels l ON l.id = il.label_id ORDER BY l.name`)
	if len(got) != 2 || got[0] != "horror:nfo" || got[1] != "keep:manual" {
		t.Fatalf("labels %v, want [horror:nfo keep:manual]", got)
	}
}
