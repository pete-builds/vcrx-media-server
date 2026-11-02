package library

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

// The messy library: every shape found when a real, long-lived download
// library was mirrored and scanned on 2026-11-02, with invented titles. Each
// entry is a way a scan went wrong in silence there, or nearly did:
// unfinished SABnzbd jobs indexed as titles, disc images, disc folders, RAR
// sets and stalled downloads left out with "0 failed", leftover archives
// beside a finished unpack, obfuscated names and duplicate copies.
//
// Sizes matter: the sample rule and the unplayable report both read them, so
// the large files are sparse files of their real size.

const gb = int64(1) << 30

type messyFile struct {
	rel  string
	size int64 // 0 writes a few bytes
}

var messyMovies = []messyFile{
	// A clean title, and a second copy in a release folder of its own.
	{"Quorvex Rising (2011)/Quorvex.Rising.2011.1080p.BluRay.x264-GRP.mkv", 0},
	{"Quorvex.Rising.2011.2160p.UHD.BluRay.REMUX.HDR-GRP/Quorvex.Rising.2011.2160p.UHD.BluRay.REMUX.HDR-GRP.mkv", 0},
	// Download-client jobs still unpacking, or failed.
	{"_UNPACK_Ember.Vale.2019.1080p.BluRay-GRP/Ember.Vale.2019.1080p.BluRay-GRP.mkv", 0},
	{"_FAILED_Ember.Vale.2019.720p.WEB-GRP/Ember.Vale.2019.720p.WEB-GRP.mkv", 0},
	{"Wren.Hollow.2021.1080p.WEB-GRP/_unpack/Wren.Hollow.2021.1080p.WEB-GRP.mkv", 0},
	// Disc images, and a small .img that is not one.
	{"Lantern Pass (2023)/Lantern.Pass.2023.1080p.DDP5.1.BR-25-ISO.iso", 23 * gb},
	{"Lantern Pass (2023)/folder.img", 0},
	{"Copper Mile 2014 PAL DVD9 -RENTAL-/D0zVkCaUXew5KPr.ISO", 6 * gb},
	// Disc folders, one nested a few levels down.
	{"Glass Harbor 2012 COMPLETE UHD BLURAY-GRP/BDMV/STREAM/00042.m2ts", 59 * gb},
	{"Glass Harbor 2012 COMPLETE UHD BLURAY-GRP/BDMV/index.bdmv", 0},
	{"Velvet Static (2003) 1080P/FullDisc/VELVET STATIC/BDMV/STREAM/00001.m2ts", 22 * gb},
	{"Paper Kites 2014 PAL DVDR/VIDEO_TS/VTS_01_1.VOB", gb},
	// The only copy is still packed, in every naming a client leaves.
	{"Tin.Orchard.2016.720p.BluRay.x264-GRP/tin-orchard.part01.rar", 50 << 20},
	{"Tin.Orchard.2016.720p.BluRay.x264-GRP/tin-orchard.part02.rar", 50 << 20},
	{"Tin.Orchard.2016.720p.BluRay.x264-GRP/tin-orchard.part02.rar.2", 50 << 20},
	{"Tin.Orchard.2016.720p.BluRay.x264-GRP/tin-orchard.nfo", 0},
	{"Gale.Season.2015.1080p.BluRay-GRP/3fa9c0d1e2b4.r00", 50 << 20},
	{"Gale.Season.2015.1080p.BluRay-GRP/3fa9c0d1e2b4.rar", 50 << 20},
	// Archives left behind by a finished unpack, beside it and above it.
	{"Salt.Meridian.2018.720p.BluRay-GRP/salt-meridian.rar", 50 << 20},
	{"Salt.Meridian.2018.720p.BluRay-GRP/Salt.Meridian.2018.720p.BluRay-GRP.mkv", 0},
	{"North.Atrium.2017.720p.BluRay-GRP - 516139a0cd.nfo - (0337)/north.rar", 50 << 20},
	{"North.Atrium.2017.720p.BluRay-GRP - 516139a0cd.nfo - (0337)/North.Atrium.2017.720p.BluRay-GRP/North Atrium 2017 720p.mp4", 0},
	// Stalled downloads.
	{"Hollow Crown Lane (2013)/Hollow.Crown.Lane.2013.720p.mkv.part", 700 << 20},
	{"Bramble.Cove.2010.1080p-GRP/bramble.cove.mkv.!qB", 2 * gb},
	// An obfuscated file name, with a sample beside it.
	{"Iron Fable (2009)/a8F3kQ9zLm2X7.mkv", 0},
	{"Iron Fable (2009)/Sample/sample-iron-fable.mkv", 0},
	// No year anywhere.
	{"Paper Lanterns/Paper.Lanterns.1080p.WEB-DL.mkv", 0},
	// Clutter a download leaves, none of it a title.
	{"Quorvex Rising (2011)/Quorvex.Rising.2011.1080p.BluRay.x264-GRP.nfo", 0},
	{"Quorvex Rising (2011)/Quorvex.Rising.2011.1080p.BluRay.x264-GRP.srr", 0},
	{"Quorvex Rising (2011)/poster.jpg", 0},
	{"Quorvex Rising (2011)/Quorvex.Rising.2011.par2", 0},
}

func writeMessy(t *testing.T, root string, files []messyFile) {
	t.Helper()
	plain := map[string]string{}
	for _, f := range files {
		plain[f.rel] = ""
	}
	writeFiles(t, root, plain)
	for _, f := range files {
		if f.size > 0 {
			if err := os.Truncate(root+"/"+f.rel, f.size); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func unplayableSet(list []Unplayable) []string {
	out := make([]string, 0, len(list))
	for _, u := range list {
		out = append(out, u.Reason+" "+u.Path)
	}
	slices.Sort(out)
	return out
}

func TestMessyMovieLibrary(t *testing.T) {
	e := newEnv(t, KindMovie, "title_year", nil)
	writeMessy(t, e.root, messyMovies)
	run := e.scan(Options{})

	// Titles: the two Quorvex copies are one movie, nothing from a job folder,
	// a disc, an archive or a partial download became a title, and the
	// obfuscated name took its folder's title.
	got := e.strs(`SELECT i.title || ' (' || IFNULL(i.year, 0) || ') x' || COUNT(v.id) FROM items i
		JOIN media_versions v ON v.item_id = i.id GROUP BY i.id ORDER BY i.title`)
	want := []string{
		"Iron Fable (2009) x1",
		"North Atrium (2017) x1",
		"Paper Lanterns (0) x1",
		"Quorvex Rising (2011) x2",
		"Salt Meridian (2018) x1",
	}
	if !slices.Equal(got, want) {
		t.Errorf("titles\n got %q\nwant %q", got, want)
	}
	if run.Seen != 6 {
		t.Errorf("seen = %d, want 6 (five titles, one with two copies)", run.Seen)
	}
	if run.Failed != 0 {
		t.Errorf("failed = %d, want 0", run.Failed)
	}

	// Everything left out is reported, once per title, and nothing else is:
	// not the small .img, not the sample, not the leftover archives of
	// Salt Meridian and North Atrium, not the clutter.
	wantU := []string{
		"archive Gale.Season.2015.1080p.BluRay-GRP",
		"archive Tin.Orchard.2016.720p.BluRay.x264-GRP",
		"disc_folder Glass Harbor 2012 COMPLETE UHD BLURAY-GRP",
		"disc_folder Paper Kites 2014 PAL DVDR",
		"disc_folder Velvet Static (2003) 1080P/FullDisc/VELVET STATIC",
		"disc_image Copper Mile 2014 PAL DVD9 -RENTAL-/D0zVkCaUXew5KPr.ISO",
		"disc_image Lantern Pass (2023)/Lantern.Pass.2023.1080p.DDP5.1.BR-25-ISO.iso",
		"incomplete Bramble.Cove.2010.1080p-GRP/bramble.cove.mkv.!qB",
		"incomplete Hollow Crown Lane (2013)/Hollow.Crown.Lane.2013.720p.mkv.part",
		"incomplete Wren.Hollow.2021.1080p.WEB-GRP/_unpack",
		"incomplete _FAILED_Ember.Vale.2019.720p.WEB-GRP",
		"incomplete _UNPACK_Ember.Vale.2019.1080p.BluRay-GRP",
	}
	if gotU := unplayableSet(run.Unplayable); !slices.Equal(gotU, wantU) {
		t.Errorf("unplayable\n got %q\nwant %q", gotU, wantU)
	}

	// The run row keeps the count and the list, sorted.
	if n := e.count(`SELECT files_unplayable FROM scan_runs WHERE id = ?`, run.ID); n != len(wantU) {
		t.Errorf("files_unplayable = %d, want %d", n, len(wantU))
	}
	var stored []Unplayable
	if err := json.Unmarshal([]byte(e.str(`SELECT unplayable FROM scan_runs WHERE id = ?`, run.ID)), &stored); err != nil {
		t.Fatal(err)
	}
	if !slices.IsSortedFunc(stored, func(a, b Unplayable) int { return strings.Compare(a.Path, b.Path) }) || len(stored) != len(wantU) {
		t.Errorf("stored list unsorted or short: %v", stored)
	}

	// A second scan of the same mess changes nothing and reports the same.
	before := e.digest()
	again := e.scan(Options{})
	if after := e.digest(); after != before {
		t.Errorf("rescan changed rows:\n%s\nvs\n%s", before, after)
	}
	if again.Added != 0 || again.Changed != 0 || again.Missing != 0 {
		t.Errorf("rescan run = %+v", again)
	}
	if gotU := unplayableSet(again.Unplayable); !slices.Equal(gotU, wantU) {
		t.Errorf("rescan unplayable = %q", gotU)
	}
}

// When the unpack finishes, the job folder is renamed and the title appears
// once, from its final path, and leaves the report.
func TestUnpackFinishing(t *testing.T) {
	e := newEnv(t, KindMovie, "title_year", nil)
	writeMessy(t, e.root, []messyFile{
		{"Quorvex Rising (2011)/Quorvex.Rising.2011.1080p.BluRay.x264-GRP.mkv", 0},
		{"_UNPACK_Ember.Vale.2019.1080p.BluRay-GRP/Ember.Vale.2019.1080p.BluRay-GRP.mkv", 0},
	})
	first := e.scan(Options{})
	if first.Seen != 1 || len(first.Unplayable) != 1 {
		t.Fatalf("first scan: seen %d, unplayable %v", first.Seen, first.Unplayable)
	}
	if err := os.Rename(e.path("_UNPACK_Ember.Vale.2019.1080p.BluRay-GRP"), e.path("Ember.Vale.2019.1080p.BluRay-GRP")); err != nil {
		t.Fatal(err)
	}
	second := e.scan(Options{})
	if second.Added != 1 || second.Missing != 0 || len(second.Unplayable) != 0 {
		t.Errorf("after the unpack: %+v", second)
	}
	if got := e.strs(`SELECT title FROM items WHERE kind = 'movie' ORDER BY title`); !slices.Equal(got, []string{"Ember Vale", "Quorvex Rising"}) {
		t.Errorf("titles = %q", got)
	}
}

// A targeted scan of a job folder, as a webhook could send mid-unpack, indexes
// nothing.
func TestTargetedScanOfUnpackFolder(t *testing.T) {
	e := newEnv(t, KindMovie, "title_year", nil)
	writeMessy(t, e.root, []messyFile{
		{"Quorvex Rising (2011)/Quorvex.Rising.2011.1080p.BluRay.x264-GRP.mkv", 0},
		{"_UNPACK_Ember.Vale.2019.1080p.BluRay-GRP/Ember.Vale.2019.1080p.BluRay-GRP.mkv", 0},
	})
	e.scan(Options{})
	run := e.scan(Options{SubPath: "_UNPACK_Ember.Vale.2019.1080p.BluRay-GRP/Ember.Vale.2019.1080p.BluRay-GRP.mkv"})
	if run.Seen != 0 || run.Added != 0 {
		t.Errorf("targeted scan of an unpacking job: %+v", run)
	}
}

func TestMessyShowLibrary(t *testing.T) {
	e := newEnv(t, KindShow, "folder", nil)
	writeMessy(t, e.root, []messyFile{
		{"Brass Tide (2020)/Season 1/Brass.Tide.2020.S01E01.1080p.WEB.h264-GRP.mkv", 0},
		{"Brass Tide (2020)/Season 1/Brass.Tide.2020.S01E02.1080p.WEB.h264-GRP.mkv", 0},
		// An episode mid-unpack at the top of the library must not become a
		// show of its own.
		{"_UNPACK_Brass.Tide.S01E03.1080p.WEB.h264-GRP/Brass.Tide.S01E03.1080p.WEB.h264-GRP.mkv", 0},
		// An episode still in its RAR set.
		{"Brass.Tide.S01E04.1080p.WEB.h264-GRP/brass.tide.s01e04.rar", 50 << 20},
		{"Brass.Tide.S01E04.1080p.WEB.h264-GRP/brass.tide.s01e04.r00", 50 << 20},
	})
	run := e.scan(Options{})
	if n := e.count(`SELECT COUNT(*) FROM items WHERE kind = 'show'`); n != 1 {
		t.Errorf("shows = %d, want 1", n)
	}
	if run.Seen != 2 {
		t.Errorf("seen = %d, want 2", run.Seen)
	}
	want := []string{
		"archive Brass.Tide.S01E04.1080p.WEB.h264-GRP",
		"incomplete _UNPACK_Brass.Tide.S01E03.1080p.WEB.h264-GRP",
	}
	if got := unplayableSet(run.Unplayable); !slices.Equal(got, want) {
		t.Errorf("unplayable\n got %q\nwant %q", got, want)
	}
}

func TestUnplayableFile(t *testing.T) {
	for _, c := range []struct {
		name string
		size int64
		want string
	}{
		{"x.iso", 4 * gb, UnplayableDiscImage},
		{"X.ISO", 4 * gb, UnplayableDiscImage},
		{"x.img", 4 * gb, UnplayableDiscImage},
		{"x.img", 4096, ""}, // a picture, not a disc
		{"x.mkv.part", gb, UnplayableIncomplete},
		{"x.mkv.part", 1024, ""},
		{"x.rar", 0, UnplayableArchive},
		{"x.part01.rar", 0, UnplayableArchive},
		{"x.part01.rar.2", 0, UnplayableArchive},
		{"x.r07", 0, UnplayableArchive},
		{"x.001", 0, UnplayableArchive},
		{"x.7z", 0, UnplayableArchive},
		{"x.mkv", 4 * gb, ""},
		{"x.srr", 0, ""},
		{"x.par2", 0, ""},
		{"x.vol03+04.par2", 0, ""},
		{".x.iso", 4 * gb, ""},
	} {
		if got := UnplayableFile(c.name, c.size); got != c.want {
			t.Errorf("UnplayableFile(%q, %d) = %q, want %q", c.name, c.size, got, c.want)
		}
	}
}

func TestIncompleteDir(t *testing.T) {
	for name, want := range map[string]bool{
		"_UNPACK_Some.Film.2019": true, "_unpack_x": true, "_FAILED_Some.Film": true, "_unpack": true,
		"Unpacked Memories (2004)": false, "_unpacked": false, "Failed State (2012)": false,
	} {
		if got := IncompleteDir(name); got != want {
			t.Errorf("IncompleteDir(%q) = %v, want %v", name, got, want)
		}
	}
}
