package collections

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
)

var testNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

const day = int64(24 * 60 * 60 * 1000)

// smartDB is a small library of invented titles that exercises every field.
//
//	1 Night Hollow   1982 Horror            R     4K dolby_vision  added 2 days ago
//	2 Quiet Meadow   1995 Drama             PG    1080p            added 400 days ago
//	3 Laugh Track    1988 Comedy, Horror    PG-13 720p (+ a 4K file that went missing), label kids-ok (hand-set)
//	4 Deep Signal    2004 Thriller          R     4K hdr10
//	5 Gone Title     1983 Horror            R     4K, but the title itself is missing
//	10 Harbor Lights (show) Drama, network Channel Nine; one episode, 4K, added 3 days ago
type smartDB struct {
	t    *testing.T
	conn *sql.DB
	now  time.Time // the builders' clock
}

func newSmartDB(t *testing.T) *smartDB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := db.Migrate(context.Background(), conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	s := &smartDB{t: t, conn: conn, now: testNow}
	now := testNow.UnixMilli()
	s.exec(`INSERT INTO libraries (id, name, kind, root_path, created_at, updated_at) VALUES (1, 'Movies', 'movie', '/m', 0, 0), (2, 'TV', 'show', '/t', 0, 0)`)
	movie := func(id int64, title string, year int, rating string, addedDaysAgo int64, genres ...string) {
		s.exec(`INSERT INTO items (id, library_id, kind, group_key, title, sort_title, year, content_rating, added_at, updated_at)
			VALUES (?, 1, 'movie', ?, ?, ?, ?, ?, ?, 0)`, id, title, title, title, year, rating, now-addedDaysAgo*day)
		for _, g := range genres {
			s.tag(id, "genre", g)
		}
	}
	movie(1, "Night Hollow", 1982, "R", 2, "Horror")
	movie(2, "Quiet Meadow", 1995, "PG", 400, "Drama")
	movie(3, "Laugh Track", 1988, "PG-13", 400, "Comedy", "Horror")
	movie(4, "Deep Signal", 2004, "R", 400, "Thriller")
	movie(5, "Gone Title", 1983, "R", 400, "Horror")
	s.exec(`UPDATE items SET missing_since = 1 WHERE id = 5`)
	s.tag(1, "director", "Ann Vale")
	s.tag(1, "studio", "Grey Pictures")
	s.tag(1, "writer", "Dee Moss")
	s.tag(2, "director", "Ben Ostrow")
	s.tag(2, "studio", "Lark Films")
	s.tag(2, "actor", "Cleo Park")
	s.tag(4, "country", "Iceland")
	s.version(1, 1, 1, "4K", "dolby_vision", false)
	s.version(2, 2, 1, "1080p", "", false)
	s.version(3, 3, 1, "720p", "", false)
	s.version(4, 3, 1, "4K", "", true)
	s.version(5, 4, 1, "4K", "hdr10", false)
	s.version(6, 5, 1, "4K", "", false)
	s.label(3, "kids-ok", "manual")

	s.exec(`INSERT INTO items (id, library_id, kind, group_key, title, sort_title, year, added_at, updated_at) VALUES (10, 2, 'show', 'harbor', 'Harbor Lights', 'Harbor Lights', 2010, ?, 0)`, now-900*day)
	s.exec(`INSERT INTO items (id, library_id, kind, parent_id, group_key, title, sort_title, season_number, added_at, updated_at) VALUES (11, 2, 'season', 10, '1', 'Season 1', 'Season 1', 1, ?, 0)`, now-900*day)
	s.exec(`INSERT INTO items (id, library_id, kind, parent_id, group_key, title, sort_title, season_number, episode_number, added_at, updated_at) VALUES (12, 2, 'episode', 11, '1', 'Pilot', 'Pilot', 1, 1, ?, 0)`, now-3*day)
	s.tag(10, "genre", "Drama")
	s.tag(10, "network", "Channel Nine")
	s.version(7, 12, 2, "4K", "", false)
	return s
}

func (s *smartDB) exec(q string, args ...any) {
	s.t.Helper()
	if _, err := s.conn.Exec(q, args...); err != nil {
		s.t.Fatalf("%s: %v", q, err)
	}
}

func (s *smartDB) tag(item int64, kind, value string) {
	s.exec(`INSERT INTO item_tags (item_id, kind, value) VALUES (?, ?, ?)`, item, kind, value)
}

func (s *smartDB) version(id, item, lib int64, res, hdr string, missing bool) {
	var h, m any
	if hdr != "" {
		h = hdr
	}
	if missing {
		m = 1
	}
	s.exec(`INSERT INTO media_versions (id, item_id, library_id, path, rel_path, size, mtime, resolution, hdr_type, missing_since, added_at, updated_at)
		VALUES (?, ?, ?, ?, 'f.mkv', 1, 0, ?, ?, ?, 0, 0)`, id, item, lib, "/x/"+strconv.FormatInt(id, 10), res, h, m)
}

func (s *smartDB) label(item int64, name, source string) {
	s.exec(`INSERT INTO labels (name) VALUES (?) ON CONFLICT (name) DO NOTHING`, name)
	s.exec(`INSERT INTO item_labels (item_id, label_id, source) SELECT ?, id, ? FROM labels WHERE name = ?`, item, source, name)
}

// labels lists "item:source" for one label, in item order.
func (s *smartDB) labels(name string) []string {
	s.t.Helper()
	rows, err := s.conn.Query(`SELECT il.item_id, il.source FROM item_labels il JOIN labels l ON l.id = il.label_id WHERE l.name = ? ORDER BY il.item_id`, name)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := []string{}
	for rows.Next() {
		var id int64
		var src string
		if err := rows.Scan(&id, &src); err != nil {
			s.t.Fatal(err)
		}
		out = append(out, strconv.FormatInt(id, 10)+":"+src)
	}
	if err := rows.Err(); err != nil {
		s.t.Fatal(err)
	}
	return out
}

// members lists a smart collection's items in order, by collection name.
func (s *smartDB) members(name string) []int64 {
	s.t.Helper()
	got, err := ids(context.Background(), s.conn, `SELECT ci.item_id FROM collection_items ci JOIN collections c ON c.id = ci.collection_id
		WHERE c.name = ? AND c.source = 'yaml' ORDER BY ci.position`, name)
	if err != nil {
		s.t.Fatal(err)
	}
	if got == nil {
		got = []int64{}
	}
	return got
}

// TestSmartRules runs every field, AND, OR and nesting against the database.
func TestSmartRules(t *testing.T) {
	s := newSmartDB(t)
	cases := []struct {
		name, library, smart string
		want                 []int64
	}{
		{"genre", "Movies", `{all: [{genre: Horror}]}`, []int64{3, 1}}, // title order: Laugh Track, Night Hollow
		{"genre list is OR", "Movies", `{all: [{genre: [comedy, Thriller]}]}`, []int64{4, 3}},
		{"AND", "Movies", `{all: [{genre: Horror}, {content_rating: R}]}`, []int64{1}},
		{"OR", "Movies", `{any: [{content_rating: PG}, {decade: 2000s}]}`, []int64{4, 2}},
		{"all and any together", "Movies", `{all: [{genre: Horror}], any: [{director: Ann Vale}, {content_rating: PG-13}]}`, []int64{3, 1}},
		{"nesting", "Movies", `{all: [{any: [{genre: Comedy}, {genre: Thriller}]}, {any: [{decade: 1980s}, {studio: Lark Films}]}]}`, []int64{3}},
		{"deep nesting", "Movies", `{any: [{all: [{genre: Drama}, {any: [{actor: Cleo Park}, {year: 1900}]}]}, {country: Iceland}]}`, []int64{4, 2}},
		{"decade", "Movies", `{all: [{decade: 1980s}]}`, []int64{3, 1}},
		{"decade without s", "Movies", `{all: [{decade: 1990}]}`, []int64{2}},
		{"year", "Movies", `{all: [{year: 1995}]}`, []int64{2}},
		{"year range", "Movies", `{all: [{year: 1980-1990}]}`, []int64{3, 1}},
		{"director ignores case", "Movies", `{all: [{director: ann vale}]}`, []int64{1}},
		{"studio", "Movies", `{all: [{studio: Lark Films}]}`, []int64{2}},
		{"writer", "Movies", `{all: [{writer: Dee Moss}]}`, []int64{1}},
		{"actor", "Movies", `{all: [{actor: Cleo Park}]}`, []int64{2}},
		{"country", "Movies", `{all: [{country: Iceland}]}`, []int64{4}},
		{"resolution of any present version", "Movies", `{all: [{resolution: 4k}]}`, []int64{4, 1}},
		{"resolution list", "Movies", `{all: [{resolution: [720p, 1080p]}]}`, []int64{3, 2}},
		{"hdr any", "Movies", `{all: [{hdr: true}]}`, []int64{4, 1}},
		{"hdr type", "Movies", `{all: [{hdr: dolby_vision}]}`, []int64{1}},
		{"content rating ignores case", "Movies", `{all: [{content_rating: r}]}`, []int64{4, 1}},
		{"label", "Movies", `{all: [{label: KIDS-OK}]}`, []int64{3}},
		{"added within days", "Movies", `{all: [{added_within_days: 30}]}`, []int64{1}},
		{"nothing matches", "Movies", `{all: [{genre: Western}]}`, []int64{}},
		{"show: genre", "TV", `{all: [{genre: Drama}]}`, []int64{10}},
		{"show: network", "TV", `{all: [{network: channel nine}]}`, []int64{10}},
		{"show: resolution through episodes", "TV", `{all: [{resolution: 4K}]}`, []int64{10}},
		{"show: added through episodes", "TV", `{all: [{added_within_days: 7}]}`, []int64{10}},
		{"show: added too long ago", "TV", `{all: [{added_within_days: 2}]}`, []int64{}},
	}
	libs, err := (&SmartBuilder{DB: s.conn}).libraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defs, err := ParseFile("t.yaml", []byte("collections:\n  - name: T\n    library: "+tc.library+"\n    smart: "+tc.smart+"\n"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := (&SmartBuilder{DB: s.conn}).match(context.Background(), defs[0], libs[strings.ToLower(tc.library)], testNow.UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			if got == nil {
				got = []int64{}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestParseErrors: every refusal names the file and the line at fault.
func TestParseErrors(t *testing.T) {
	head := "collections:\n  - name: Scary\n    library: Movies\n"
	cases := []struct{ name, body, want string }{
		{"unknown top-level key", "colections: []\n", `bad.yaml:1: unknown field "colections"`},
		{"unknown definition field", head + "    lable: horror\n    smart: {all: [{genre: Horror}]}\n", `bad.yaml:4: unknown field "lable"`},
		{"unknown rule field", head + "    smart:\n      all:\n        - genr: Horror\n", `bad.yaml:6: unknown field "genr"`},
		{"unknown smart key", head + "    smart:\n      every:\n        - genre: Horror\n", `bad.yaml:5: unknown field "every"`},
		{"bad resolution", head + "    smart:\n      all:\n        - resolution: 5K\n", `bad.yaml:6: resolution "5K" is not one of`},
		{"bad resolution in a list", head + "    smart:\n      all:\n        - resolution:\n            - 4K\n            - 3K\n", `bad.yaml:8: resolution "3K"`},
		{"bad decade", head + "    smart: {all: [{decade: 198}]}\n", `bad.yaml:4: decade "198"`},
		{"backwards years", head + "    smart: {all: [{year: 2001-1999}]}\n", `bad.yaml:4: year "2001-1999": the range runs backwards`},
		{"zero days", head + "    smart: {all: [{added_within_days: 0}]}\n", `bad.yaml:4: added_within_days must be`},
		{"bad hdr", head + "    smart: {all: [{hdr: hdr11}]}\n", `bad.yaml:4: hdr "hdr11"`},
		{"hdr false", head + "    smart: {all: [{hdr: false}]}\n", `bad.yaml:4: hdr: false is not supported`},
		{"two conditions in one entry", head + "    smart:\n      all:\n        - genre: Horror\n          decade: 1980s\n", `bad.yaml:6: one condition per list entry`},
		{"condition beside a group", head + "    smart:\n      all:\n        - any: [{genre: Horror}]\n          genre: Drama\n", `bad.yaml:7: "genre" cannot sit beside all: or any:`},
		{"empty list", head + "    smart:\n      all: []\n", `bad.yaml:5: all must be a non-empty list`},
		{"empty smart", head + "    smart: {}\n", `bad.yaml:4: smart needs all: or any:`},
		{"no smart", head, `bad.yaml:2: collection "Scary" has no smart rule`},
		{"no library", "collections:\n  - name: Scary\n    smart: {all: [{genre: Horror}]}\n", `bad.yaml:2: collection "Scary" has no library`},
		{"empty label", head + "    label:\n    smart: {all: [{genre: Horror}]}\n", `bad.yaml:4: label must be a single value`},
		{"bad visible", head + "    smart: {all: [{genre: Horror}]}\n    visible: {library: maybe}\n", `bad.yaml:5: visible.library must be true or false`},
		{"unknown visible key", head + "    smart: {all: [{genre: Horror}]}\n    visible: {friends: false}\n", `bad.yaml:5: unknown field "friends"`},
		{"key set twice", head + "    smart: {all: [{genre: Horror}]}\n    name: Again\n", `bad.yaml:5: "name" is set twice`},
		{"duplicate collection", head + "    smart: {all: [{genre: Horror}]}\n  - name: scary!\n    library: movies\n    smart: {all: [{genre: Drama}]}\n",
			`bad.yaml:5: collection "scary!" in library "movies" is already defined on line 2`},
		{"yaml syntax", head + "    smart: {all: [{genre: Horror}\n", `bad.yaml: yaml: line`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defs, err := ParseFile("bad.yaml", []byte(tc.body))
			if err == nil {
				t.Fatalf("accepted: %+v", defs)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want it to contain %q", err, tc.want)
			}
		})
	}
	// The spec's own example parses, with the hidden flags it sets.
	defs, err := ParseFile("ok.yaml", []byte(exampleFile))
	if err != nil || len(defs) != 2 {
		t.Fatalf("example: %v %+v", err, defs)
	}
	if defs[1].Label != "horror" || defs[1].Visible != (Visible{}) || len(defs[1].Smart.All) != 2 || defs[1].Line != 10 {
		t.Fatalf("example second definition: %+v", defs[1])
	}
	// Empty, comment-only and key-less files are refused; collections: [] is
	// the way to define nothing on purpose.
	for name, body := range map[string]string{"empty": "", "blank": " \n\n", "comments": "# nothing yet\n", "empty mapping": "{}\n"} {
		if defs, err := ParseFile("empty.yaml", []byte(body)); !errors.Is(err, ErrNoCollectionsKey) || !strings.HasPrefix(err.Error(), "empty.yaml") {
			t.Fatalf("%s file: %v %v", name, defs, err)
		}
	}
	if defs, err := ParseFile("none.yaml", []byte("collections: []\n")); err != nil || len(defs) != 0 {
		t.Fatalf("collections: []: %v %v", defs, err)
	}
}

const exampleFile = `collections:
  - name: 4K Resolution
    library: Movies
    smart:
      all:
        - resolution: 4K
    label: 4K
    visible: { library: false, home: false, shared: false }

  - name: R-Rated Horror
    library: Movies
    smart:
      all:
        - genre: Horror
        - content_rating: R
    label: horror
    visible: { library: false, home: false, shared: false }
`

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newSmart(s *smartDB, dir string) *SmartBuilder {
	return &SmartBuilder{DB: s.conn, Dir: dir, Now: func() time.Time { return s.now }, Debounce: 10 * time.Millisecond}
}

func (s *smartDB) build(b *SmartBuilder) Report {
	s.t.Helper()
	rep, err := b.Build(context.Background())
	if err != nil {
		s.t.Fatal(err)
	}
	return rep
}

const horrorFile = `collections:
  - name: R-Rated Horror
    library: Movies
    smart:
      all:
        - genre: Horror
        - content_rating: R
    label: horror
    visible: { library: false, home: false, shared: false }
`

// TestSmartLabelOwnership: a definition stamps its label, clears it when a
// title stops matching, and never touches the same label set by hand or by an
// NFO file.
func TestSmartLabelOwnership(t *testing.T) {
	s := newSmartDB(t)
	dir := t.TempDir()
	writeFile(t, dir, "horror.yaml", horrorFile)
	s.label(2, "horror", "manual") // Quiet Meadow: an admin's own judgement
	s.label(4, "horror", "nfo")    // Deep Signal: from its NFO file

	b := newSmart(s, dir)
	rep := s.build(b)
	if rep.LabelsAdded != 1 || rep.LabelsRemoved != 0 {
		t.Fatalf("first build: %+v", rep)
	}
	if got, want := s.labels("horror"), []string{"1:collection", "2:manual", "4:nfo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("labels %v, want %v", got, want)
	}

	// Laugh Track becomes R: stamped. Night Hollow is re-rated: cleared.
	s.exec(`UPDATE items SET content_rating = 'R' WHERE id = 3`)
	s.exec(`UPDATE items SET content_rating = 'PG-13' WHERE id = 1`)
	rep = s.build(b)
	if got, want := s.labels("horror"), []string{"2:manual", "3:collection", "4:nfo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after re-rating: %v, want %v (%+v)", got, want, rep)
	}

	// The hand-set and NFO labels survive even when their titles stop matching
	// and when they never matched at all; a stamped title that loses the genre
	// goes.
	s.exec(`DELETE FROM item_tags WHERE item_id = 3 AND value = 'Horror'`)
	s.build(b)
	if got, want := s.labels("horror"), []string{"2:manual", "4:nfo"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after losing the genre: %v, want %v", got, want)
	}

	// The definition goes away: its stamps go, the others stay.
	s.exec(`INSERT INTO item_tags (item_id, kind, value) VALUES (3, 'genre', 'Horror')`)
	s.build(b)
	if got := s.labels("horror"); !reflect.DeepEqual(got, []string{"2:manual", "3:collection", "4:nfo"}) {
		t.Fatalf("restamp: %v", got)
	}
	if err := os.Remove(filepath.Join(dir, "horror.yaml")); err != nil {
		t.Fatal(err)
	}
	rep = s.build(b)
	if got := s.labels("horror"); !reflect.DeepEqual(got, []string{"2:manual", "3:collection", "4:nfo"}) || rep.Removed != 0 || !rep.Files[0].LastGood {
		t.Fatalf("a just-deleted file was not held: %v %+v", got, rep)
	}
	s.now = s.now.Add(DeleteGrace)
	rep = s.build(b)
	if got := s.labels("horror"); !reflect.DeepEqual(got, []string{"2:manual", "4:nfo"}) || rep.Removed != 1 {
		t.Fatalf("after deleting the file: %v %+v", got, rep)
	}
}

// TestNFOTakesOverStampedLabel: when a scan reads the same label from an NFO,
// the row becomes the NFO's and the collection no longer clears it.
func TestNFOTakesOverStampedLabel(t *testing.T) {
	s := newSmartDB(t)
	dir := t.TempDir()
	writeFile(t, dir, "horror.yaml", horrorFile)
	b := newSmart(s, dir)
	s.build(b)
	// The statement the scanner runs for an NFO tag (library/store.go).
	s.exec(`INSERT INTO item_labels (item_id, label_id, source) SELECT 1, id, 'nfo' FROM labels WHERE name = 'horror'
		ON CONFLICT (item_id, label_id) DO UPDATE SET source = 'nfo' WHERE item_labels.source = 'collection'`)
	s.exec(`UPDATE items SET content_rating = 'PG' WHERE id = 1`)
	s.build(b)
	if got := s.labels("horror"); !reflect.DeepEqual(got, []string{"1:nfo"}) {
		t.Fatalf("labels %v, want the NFO's to survive", got)
	}
}

// TestSmartBrokenFileKeepsLastGood: a file that stops parsing keeps its
// collections and labels from the last good load, across a restart too, and
// does not disturb other files. A missing directory changes nothing.
func TestSmartBrokenFileKeepsLastGood(t *testing.T) {
	s := newSmartDB(t)
	dir := t.TempDir()
	writeFile(t, dir, "horror.yaml", horrorFile)
	writeFile(t, dir, "drama.yaml", "collections:\n  - name: Dramas\n    library: Movies\n    smart: {all: [{genre: Drama}]}\n    label: drama\n")
	s.build(newSmart(s, dir))
	if got := s.labels("horror"); !reflect.DeepEqual(got, []string{"1:collection"}) {
		t.Fatalf("setup: %v", got)
	}

	writeFile(t, dir, "horror.yaml", strings.Replace(horrorFile, "content_rating: R", "content_ratin: R", 1))
	// A fresh builder: nothing in memory, only the database, as after a restart.
	b := newSmart(s, dir)
	// Meanwhile a title starts matching the old rule and another stops matching
	// drama, so both files' builds are seen to run.
	s.exec(`UPDATE items SET content_rating = 'R' WHERE id = 3`)
	s.exec(`DELETE FROM item_tags WHERE item_id = 2 AND value = 'Drama'`)
	rep := s.build(b)
	if len(rep.Files) != 2 || rep.Files[1].File != "horror.yaml" || !rep.Files[1].LastGood ||
		!strings.Contains(rep.Files[1].Error, `horror.yaml:7: unknown field "content_ratin"`) {
		t.Fatalf("report files: %+v", rep.Files)
	}
	if got := s.labels("horror"); !reflect.DeepEqual(got, []string{"1:collection", "3:collection"}) {
		t.Fatalf("broken file: horror labels %v, want the last good rule still applied", got)
	}
	if got := s.members("R-Rated Horror"); !reflect.DeepEqual(got, []int64{3, 1}) {
		t.Fatalf("broken file: members %v", got)
	}
	if got := s.labels("drama"); len(got) != 0 {
		t.Fatalf("the good file did not build: drama labels %v", got)
	}

	// A label no definition owns any more (drama.yaml now stamps "weepy")
	// is held while another file is broken, since that file may be the one
	// that owns it after its typo is fixed.
	s.exec(`INSERT INTO item_tags (item_id, kind, value) VALUES (2, 'genre', 'Drama')`)
	s.build(b)
	writeFile(t, dir, "drama.yaml", "collections:\n  - name: Dramas\n    library: Movies\n    smart: {all: [{genre: Drama}]}\n    label: weepy\n")
	s.build(b)
	if got := s.labels("drama"); !reflect.DeepEqual(got, []string{"2:collection"}) {
		t.Fatalf("orphaned label cleared while a file is broken: %v", got)
	}
	if got := s.labels("weepy"); !reflect.DeepEqual(got, []string{"2:collection"}) {
		t.Fatalf("relabel: %v", got)
	}

	// The directory vanishes (a mount went away): nothing changes at all.
	gone := filepath.Join(t.TempDir(), "not-there")
	rep = s.build(newSmart(s, gone))
	if !strings.Contains(rep.Error, "not found") || rep.Removed != 0 || rep.LabelsRemoved != 0 {
		t.Fatalf("missing dir: %+v", rep)
	}
	if got := s.labels("horror"); !reflect.DeepEqual(got, []string{"1:collection", "3:collection"}) {
		t.Fatalf("missing dir changed labels: %v", got)
	}

	// Fixed: the new rule applies and the orphaned label clears.
	writeFile(t, dir, "horror.yaml", strings.Replace(horrorFile, "content_rating: R", "year: 1988", 1))
	s.now = s.now.Add(DeleteGrace)
	rep = s.build(b)
	if got := s.labels("horror"); !reflect.DeepEqual(got, []string{"3:collection"}) {
		t.Fatalf("fixed file: %v (%+v)", got, rep)
	}
	if got := s.labels("drama"); len(got) != 0 {
		t.Fatalf("orphan not cleared once every file loads: %v", got)
	}

	// Deleting the file removes what it built, once the grace has passed.
	if err := os.Remove(filepath.Join(dir, "horror.yaml")); err != nil {
		t.Fatal(err)
	}
	s.build(b)
	s.now = s.now.Add(DeleteGrace)
	rep = s.build(b)
	if got := s.labels("horror"); len(got) != 0 || rep.Removed != 1 {
		t.Fatalf("empty dir: %v %+v", got, rep)
	}
}

// TestSmartDefinitionErrors: an unknown library is reported per definition,
// and its label is never cleared while it cannot build.
func TestSmartDefinitionErrors(t *testing.T) {
	s := newSmartDB(t)
	dir := t.TempDir()
	writeFile(t, dir, "a.yaml", horrorFile)
	b := newSmart(s, dir)
	s.build(b)
	writeFile(t, dir, "a.yaml", strings.Replace(horrorFile, "library: Movies", "library: Films", 1))
	writeFile(t, dir, "b.yaml", strings.Replace(horrorFile, "label: horror", "label: scary", 1))
	rep := s.build(b)
	if len(rep.Collections) != 2 || !strings.Contains(rep.Collections[0].Error, `a.yaml:2: no library named "Films"`) ||
		rep.Collections[1].Error != "" || rep.Collections[1].Items != 1 {
		t.Fatalf("report: %+v", rep.Collections)
	}
	if got := s.labels("horror"); !reflect.DeepEqual(got, []string{"1:collection"}) {
		t.Fatalf("label of a failed definition was cleared: %v", got)
	}
	// The same name in another file of the same library is refused, loudly.
	writeFile(t, dir, "a.yaml", horrorFile)
	writeFile(t, dir, "b.yaml", horrorFile)
	rep = s.build(b)
	if !strings.Contains(rep.Collections[1].Error, `b.yaml:2: collection "R-Rated Horror" in library "Movies" is already defined in a.yaml`) {
		t.Fatalf("duplicate: %+v", rep.Collections)
	}
}

// TestSmartCoexistsWithFranchises: the two builders own separate rows.
func TestSmartCoexistsWithFranchises(t *testing.T) {
	s := newSmartDB(t)
	s.exec(`UPDATE items SET tmdb_collection_id = 77 WHERE id IN (1, 3)`)
	dir := t.TempDir()
	writeFile(t, dir, "horror.yaml", horrorFile)
	franchise := &Builder{DB: s.conn, Names: func(ctx context.Context, id int64) (string, error) { return "Hollow Collection", nil }}
	if _, err := franchise.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.build(newSmart(s, dir))
	if _, err := franchise.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.build(newSmart(s, dir))
	got, err := ids(context.Background(), s.conn, `SELECT COUNT(*) FROM collections WHERE (source = 'auto' AND slug = 'tmdb-77') OR (source = 'yaml' AND slug = 'yaml-r-rated-horror')`)
	if err != nil || len(got) != 1 || got[0] != 2 {
		t.Fatalf("both kinds present: %v %v", got, err)
	}
	var vis int
	if err := s.conn.QueryRow(`SELECT visible_library + visible_home + visible_shared FROM collections WHERE slug = 'yaml-r-rated-horror'`).Scan(&vis); err != nil || vis != 0 {
		t.Fatalf("visible flags: %d %v", vis, err)
	}
	// A label a definition creates is hidden.
	var hidden int
	if err := s.conn.QueryRow(`SELECT hidden FROM labels WHERE name = 'horror'`).Scan(&hidden); err != nil || hidden != 1 {
		t.Fatalf("label hidden = %d %v", hidden, err)
	}
}

// TestSmartRunTrigger: Run builds at start and again on Trigger.
func TestSmartRunTrigger(t *testing.T) {
	s := newSmartDB(t)
	dir := t.TempDir()
	b := newSmart(s, dir)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { b.Run(ctx, time.Hour); close(done) }()
	defer func() { cancel(); <-done }()
	waitFor(t, func() bool { r := b.Status(); return r != nil && len(r.Files) == 0 })
	writeFile(t, dir, "horror.yaml", horrorFile)
	b.Trigger()
	waitFor(t, func() bool { r := b.Status(); return r != nil && len(r.Collections) == 1 })
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

// The example in deploy/ parses, including its commented-out collection.
func TestDeployExampleParses(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "collections.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	defs, err := ParseFile("collections.example.yaml", b)
	if err != nil || len(defs) != 2 || defs[0].Label != "4K" || defs[1].Label != "horror" {
		t.Fatalf("example: %v %+v", err, defs)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "  #-") || strings.HasPrefix(l, "  #  ") {
			l = "  " + strings.TrimPrefix(l, "  #")
		}
		lines = append(lines, l)
	}
	defs, err = ParseFile("uncommented.yaml", []byte(strings.Join(lines, "\n")))
	if err != nil || len(defs) != 3 || len(defs[2].Smart.All) != 3 || len(defs[2].Smart.All[2].Any) != 2 {
		t.Fatalf("commented example: %v %+v", err, defs)
	}
}

// TestSmartTruncatedFileKeepsLabels: a file an editor or sync tool has emptied
// or cut short mid-save is refused, so friends never regain hidden titles.
// Only collections: [] empties a file on purpose.
func TestSmartTruncatedFileKeepsLabels(t *testing.T) {
	s := newSmartDB(t)
	dir := t.TempDir()
	writeFile(t, dir, "horror.yaml", horrorFile)
	b := newSmart(s, dir)
	s.build(b)
	want := []string{"1:collection"}
	for name, body := range map[string]string{
		"zero bytes":   "",
		"whitespace":   "\n  \n",
		"comment only": "# saving...\n",
		"cut short":    horrorFile[:40],
		"no key":       "{}\n",
	} {
		writeFile(t, dir, "horror.yaml", body)
		rep := s.build(b)
		if got := s.labels("horror"); !reflect.DeepEqual(got, want) || rep.LabelsRemoved != 0 || rep.Removed != 0 {
			t.Fatalf("%s: labels %v, report %+v", name, got, rep)
		}
		if len(rep.Files) != 1 || rep.Files[0].Error == "" || !rep.Files[0].LastGood {
			t.Fatalf("%s: not refused: %+v", name, rep.Files)
		}
	}
	// A brand new empty file has nothing to protect and is not an error.
	writeFile(t, dir, "new.yaml", "")
	writeFile(t, dir, "horror.yaml", horrorFile)
	if rep := s.build(b); rep.Files[1].Error != "" {
		t.Fatalf("new empty file: %+v", rep.Files)
	}
	// collections: [] is the admin emptying the file on purpose.
	writeFile(t, dir, "horror.yaml", "collections: []\n")
	rep := s.build(b)
	if got := s.labels("horror"); len(got) != 0 || rep.Removed != 1 {
		t.Fatalf("collections: []: %v %+v", got, rep)
	}
}
