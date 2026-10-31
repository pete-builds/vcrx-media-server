package library

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// extrasMovieTree has one film with every Plex extras layout, a folder of two
// films where a trailer names its film, and a library-level "Other" folder
// that holds a real film: only inside a title's folder is "Other" an extras folder.
var extrasMovieTree = map[string]string{
	"Lantern Bay (2004)/Lantern Bay (2004).mkv":                     "",
	"Lantern Bay (2004)/Lantern Bay (2004)-trailer.mp4":             "",
	"Lantern Bay (2004)/Trailers/Teaser One.mp4":                    "",
	"Lantern Bay (2004)/Featurettes/Making the Bay.mkv":             "",
	"Lantern Bay (2004)/Behind The Scenes/On Set.mkv":               "",
	"Lantern Bay (2004)/Deleted Scenes/Alternate Ending.mkv":        "",
	"Lantern Bay (2004)/Interviews/The Director.mkv":                "",
	"Lantern Bay (2004)/Scenes/Opening.mkv":                         "",
	"Lantern Bay (2004)/Shorts/Prequel.mkv":                         "",
	"Lantern Bay (2004)/Other/Gag.Reel.mkv":                         "",
	"Lantern Bay (2004)/Extras/Poster Tour.mkv":                     "",
	"Lantern Bay (2004)/Trailers/notes.txt":                         "",
	"Pairs/Ember Road (1999).mkv":                                   "",
	"Pairs/Ember Road (1999)-trailer.mkv":                           "",
	"Pairs/Glass Orchard (2011).mkv":                                "",
	"Other/Quiet Harbor (2012)/Quiet Harbor (2012).mkv":             "",
	"Scenes From a Kitchen (2018)/Scenes From a Kitchen (2018).mkv": "",
}

func extrasOf(e *env, title string) []string {
	return e.strs(`SELECT x.kind || ':' || x.title FROM item_extras x JOIN items i ON i.id = x.item_id
		WHERE i.title = ? AND x.missing_since IS NULL ORDER BY x.kind, x.title`, title)
}

func TestExtrasAttachAndAreNeverTitles(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", extrasMovieTree)
	run := e.scan(Options{})
	want := []string{"Ember Road (1999)", "Glass Orchard (2011)", "Lantern Bay (2004)", "Quiet Harbor (2012)", "Scenes From a Kitchen (2018)"}
	if got := titles(e, "movie"); !slices.Equal(got, want) {
		t.Fatalf("movies = %q, want %q", got, want)
	}
	if run.Seen != 5 {
		t.Fatalf("run = %+v: extras must not count as titles' files", run)
	}
	if n := e.count(`SELECT COUNT(*) FROM media_versions`); n != 5 {
		t.Fatalf("%d versions, want one per film", n)
	}
	wantExtras := []string{
		"behind_the_scenes:On Set", "deleted_scene:Alternate Ending", "featurette:Making the Bay", "interview:The Director",
		"other:Gag Reel", "other:Poster Tour", "scene:Opening", "short:Prequel", "trailer:Teaser One", "trailer:Trailer",
	}
	if got := extrasOf(e, "Lantern Bay"); !slices.Equal(got, wantExtras) {
		t.Fatalf("Lantern Bay extras = %q, want %q", got, wantExtras)
	}
	// In a folder of two films, the trailer goes to the film it names.
	if got := extrasOf(e, "Ember Road"); !slices.Equal(got, []string{"trailer:Trailer"}) {
		t.Fatalf("Ember Road extras = %q", got)
	}
	if got := extrasOf(e, "Glass Orchard"); len(got) != 0 {
		t.Fatalf("Glass Orchard took %q", got)
	}
	// Every extra is probed like a version.
	if n := e.count(`SELECT COUNT(*) FROM item_extras WHERE probe_status = 'ok' AND duration_ms > 0 AND video_codec = 'h264'`); n != 11 {
		t.Fatalf("%d probed extras, want 11", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM item_extras WHERE audio_streams LIKE '%"codec":"eac3"%'`); n != 11 {
		t.Fatalf("audio streams not kept: %d", n)
	}
}

// Every extras folder name, at the top of the library and inside a title's folder.
func TestExtrasFolderNames(t *testing.T) {
	cases := []struct {
		name, kind  string
		top, nested string
	}{
		{"Trailers", KindMovie, ExtraTrailer, ExtraTrailer},
		{"Featurettes", KindMovie, ExtraFeaturette, ExtraFeaturette},
		{"Behind The Scenes", KindShow, ExtraBehindTheScenes, ExtraBehindTheScenes},
		{"behind.the.scenes", KindMovie, ExtraBehindTheScenes, ExtraBehindTheScenes},
		{"Deleted Scenes", KindMovie, ExtraDeletedScene, ExtraDeletedScene},
		{"Interviews", KindShow, ExtraInterview, ExtraInterview},
		{"Shorts", KindMovie, ExtraShort, ExtraShort},
		{"Extras", KindMovie, ExtraOther, ExtraOther},
		{"Scenes", KindMovie, "", ExtraScene},
		{"Other", KindShow, "", ExtraOther},
		{"Specials", KindMovie, ExtraFeaturette, ExtraFeaturette},
		{"Specials", KindShow, "", ""},
		{"Season 01", KindShow, "", ""},
		{"Scenes From a Kitchen (2018)", KindMovie, "", ""},
	}
	for _, c := range cases {
		if got := ExtrasDirKind(c.name, c.kind, false); got != c.top {
			t.Errorf("%s (%s) at the top = %q, want %q", c.name, c.kind, got, c.top)
		}
		if got := ExtrasDirKind(c.name, c.kind, true); got != c.nested {
			t.Errorf("%s (%s) nested = %q, want %q", c.name, c.kind, got, c.nested)
		}
		if skip := skipDirAt(c.name, c.kind, true) == "extras"; skip != (c.nested != "") {
			t.Errorf("%s (%s) nested: walked as titles = %v", c.name, c.kind, !skip)
		}
	}
}

func TestShowExtrasAndThemeMusic(t *testing.T) {
	e := newEnv(t, KindShow, "folder", map[string]string{
		"Moss Hollow/Season 01/Moss Hollow - S01E01.mkv":              "",
		"Moss Hollow/Season 01/Moss Hollow - S01E02.mkv":              "",
		"Moss Hollow/Season 01/Deleted Scenes/Cut From the Pilot.mkv": "",
		"Moss Hollow/Trailers/Season One Trailer.mkv":                 "",
		"Moss Hollow/Featurettes/Building the Hollow.mkv":             "",
		"Moss Hollow/Other/Bloopers.mkv":                              "",
		"Moss Hollow/Theme.mp3":                                       "",
		"Moss Hollow/notes.mp3":                                       "",
	})
	e.scan(Options{})
	if got := e.strs(`SELECT kind || ':' || title FROM items ORDER BY kind, title`); !slices.Equal(got,
		[]string{"episode:Episode 1", "episode:Episode 2", "season:Season 1", "show:Moss Hollow"}) {
		t.Fatalf("items = %q", got)
	}
	want := []string{"deleted_scene:Cut From the Pilot", "featurette:Building the Hollow", "other:Bloopers", "theme:Theme music", "trailer:Season One Trailer"}
	if got := extrasOf(e, "Moss Hollow"); !slices.Equal(got, want) {
		t.Fatalf("show extras = %q, want %q", got, want)
	}
	// An MP3 has no video, which is right for theme music, not a probe failure.
	if got := e.str(`SELECT probe_status || ':' || container FROM item_extras WHERE kind = 'theme'`); got != "ok:mp3" {
		t.Fatalf("theme probe = %q", got)
	}
}

// An extra that vanishes is marked missing, never deleted on the first miss,
// comes back with its id, and goes only after MissingRetention.
func TestExtraMissingAndBack(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", extrasMovieTree)
	e.scan(Options{})
	rel := "Lantern Bay (2004)/Trailers/Teaser One.mp4"
	id := e.count(`SELECT id FROM item_extras WHERE rel_path = ?`, rel)
	probes := e.pr.total()
	stash := filepath.Join(t.TempDir(), "stash.mp4")
	if err := os.Rename(e.path(rel), stash); err != nil {
		t.Fatal(err)
	}
	e.clock.advance(time.Hour)
	run := e.scan(Options{})
	if run.Missing != 0 {
		t.Fatalf("run = %+v: a missing extra is not a missing title file", run)
	}
	if n := e.count(`SELECT COUNT(*) FROM item_extras WHERE id = ? AND missing_since IS NOT NULL`, id); n != 1 {
		t.Fatal("vanished extra not marked missing, or deleted on the first miss")
	}
	if got := extrasOf(e, "Lantern Bay"); slices.Contains(got, "trailer:Teaser One") {
		t.Fatal("missing extra still listed")
	}
	if err := os.Rename(stash, e.path(rel)); err != nil {
		t.Fatal(err)
	}
	e.scan(Options{})
	if n := e.count(`SELECT COUNT(*) FROM item_extras WHERE id = ? AND missing_since IS NULL`, id); n != 1 {
		t.Fatal("extra that came back was not restored with its id")
	}
	if e.pr.total() != probes {
		t.Fatalf("an unchanged extra that came back was probed again (%d probes)", e.pr.total()-probes)
	}

	// Gone for good: kept through the retention period, then deleted.
	if err := os.Remove(e.path(rel)); err != nil {
		t.Fatal(err)
	}
	e.scan(Options{})
	e.clock.advance(MissingRetention - time.Hour)
	e.scan(Options{})
	if n := e.count(`SELECT COUNT(*) FROM item_extras WHERE id = ?`, id); n != 1 {
		t.Fatal("extra deleted before the retention period")
	}
	e.clock.advance(2 * time.Hour)
	e.scan(Options{})
	if n := e.count(`SELECT COUNT(*) FROM item_extras WHERE id = ?`, id); n != 0 {
		t.Fatal("extra kept past the retention period")
	}
}

// Targeted scans: a webhook for one trailer file, or for a folder inside an
// extras folder, never marks other extras missing.
func TestExtrasTargetedScan(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", extrasMovieTree)
	e.scan(Options{})
	before := e.digest()
	e.scan(Options{Trigger: "webhook", SubPath: "Lantern Bay (2004)/Lantern Bay (2004)-trailer.mp4"})
	e.scan(Options{Trigger: "webhook", SubPath: "Lantern Bay (2004)/Trailers"})
	if after := e.digest(); after != before {
		t.Fatalf("targeted scans changed extras:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// A new trailer arriving by itself is found and attached.
	e.write(map[string]string{"Lantern Bay (2004)/Trailers/Teaser Two.mp4": ""})
	e.scan(Options{Trigger: "webhook", SubPath: "Lantern Bay (2004)"})
	if got := extrasOf(e, "Lantern Bay"); !slices.Contains(got, "trailer:Teaser Two") {
		t.Fatalf("extras = %q", got)
	}
}

// When the film itself goes, its extras are not adopted by anything else, and
// they return with it.
func TestExtrasFollowTheirFilm(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", extrasMovieTree)
	e.scan(Options{})
	stash := filepath.Join(t.TempDir(), "film.mkv")
	if err := os.Rename(e.path("Lantern Bay (2004)/Lantern Bay (2004).mkv"), stash); err != nil {
		t.Fatal(err)
	}
	e.scan(Options{})
	if n := e.count(`SELECT COUNT(*) FROM item_extras x JOIN items i ON i.id = x.item_id WHERE i.title = 'Lantern Bay' AND x.missing_since IS NULL`); n != 0 {
		t.Fatalf("%d extras still present without their film", n)
	}
	if err := os.Rename(stash, e.path("Lantern Bay (2004)/Lantern Bay (2004).mkv")); err != nil {
		t.Fatal(err)
	}
	e.scan(Options{})
	if got := extrasOf(e, "Lantern Bay"); len(got) != 10 {
		t.Fatalf("extras after the film came back = %q", got)
	}
}
