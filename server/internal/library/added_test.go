package library

import (
	"os"
	"testing"
	"time"
)

// Recently Added must follow when files arrived, not when VCRX first saw them.
func TestAddedAtFollowsFileMtime(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", map[string]string{
		"Nosferatu (1922)/Nosferatu.1922.mkv":            "a",
		"The General (1926)/The.General.1926.mkv":        "b",
		"Plan 9 from Outer Space (1959)/Plan.9.1959.mkv": "c",
	})
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	future := time.Now().Add(48 * time.Hour)
	for rel, mt := range map[string]time.Time{
		"Nosferatu (1922)/Nosferatu.1922.mkv":            old,
		"Plan 9 from Outer Space (1959)/Plan.9.1959.mkv": future,
	} {
		if err := os.Chtimes(e.path(rel), mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	e.scan(Options{})
	got := func(title string) int64 {
		var v int64
		if err := e.db.QueryRow(`SELECT added_at FROM items WHERE title = ?`, title).Scan(&v); err != nil {
			t.Fatalf("%s: %v", title, err)
		}
		return v
	}
	if a := got("Nosferatu"); a != old.UnixMilli() {
		t.Fatalf("Nosferatu added_at = %d, want the file mtime %d", a, old.UnixMilli())
	}
	if a := got("Plan 9 from Outer Space"); a > e.clock.now().UnixMilli() {
		t.Fatalf("a future mtime leaked into added_at: %d", a)
	}
	var vAdded int64
	if err := e.db.QueryRow(`SELECT added_at FROM media_versions WHERE rel_path LIKE 'Nosferatu%'`).Scan(&vAdded); err != nil || vAdded != old.UnixMilli() {
		t.Fatalf("version added_at = %d, %v", vAdded, err)
	}
}
