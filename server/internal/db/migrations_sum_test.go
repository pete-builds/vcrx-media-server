package db

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// A migration is tracked by its number alone: a database that applied 0013
// never runs 0013 again, so editing, renumbering or deleting one that has
// shipped leaves those databases with a schema no file describes. Each
// shipped migration's SHA-256 is in migrations.sum, which only ever grows.
func TestMigrationsAreAppendOnly(t *testing.T) {
	raw, err := os.ReadFile("migrations.sum")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("migrations.sum: bad line %q", line)
		}
		want[name] = sum
	}
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		h := sha256.Sum256([]byte(m.sql))
		got := hex.EncodeToString(h[:])
		sum, listed := want[m.name]
		switch {
		case !listed:
			t.Errorf("%s is new: add this line to server/internal/db/migrations.sum\n%s  %s", m.name, got, m.name)
		case sum != got:
			t.Errorf("%s changed after it shipped. Databases that applied it will never see the change: put it in a new migration and restore this file", m.name)
		}
		delete(want, m.name)
	}
	for name := range want {
		t.Errorf("%s is in migrations.sum but gone (deleted or renumbered); databases that applied it still record it by number", name)
	}
}
