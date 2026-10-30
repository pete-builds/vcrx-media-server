package library

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
)

// movieTree mixes a tidy folder with two versions, two scene release folders
// of one film, and the junk a real download folder carries.
var movieTree = map[string]string{
	"Night of the Living Dead (1968)/Night of the Living Dead (1968) - 1080p.mkv":        "",
	"Night of the Living Dead (1968)/Night of the Living Dead (1968) - 4K HDR REMUX.mkv": "",
	"Night of the Living Dead (1968)/._Night of the Living Dead (1968) - 1080p.mkv":      "",
	"Nosferatu.1922.1080p.BluRay.x264-VCRXTEST/vcrxtest-nosferatu-1080p.mkv":             "",
	"Nosferatu.1922.1080p.BluRay.x264-VCRXTEST/vcrxtest-nosferatu-1080p-sample.mkv":      "",
	"Nosferatu.1922.1080p.BluRay.x264-VCRXTEST/Nosferatu.1922.1080p.BluRay.x264.nfo":     "== VCRXTEST == ascii art ==",
	"Nosferatu.1922.1080p.BluRay.x264-VCRXTEST/Nosferatu.1922.1080p.BluRay.x264.sfv":     "",
	"Nosferatu.1922.1080p.BluRay.x264-VCRXTEST/vcrxtest-nosferatu.r00":                   "",
	"Nosferatu.1922.2160p.UHD.BluRay.x265-SAMPLEGRP/Sample/sample.mkv":                   "",
	"Nosferatu.1922.2160p.UHD.BluRay.x265-SAMPLEGRP/Nosferatu.1922.2160p.UHD.BluRay.mkv": "",
	"Nosferatu.1922.2160p.UHD.BluRay.x265-SAMPLEGRP/Nosferatu.1922.2160p.UHD.BluRay.iso": "",
	"His Girl Friday (1940)/His.Girl.Friday.1940.720p.WEB-DL.mp4":                        "",
	"His Girl Friday (1940)/Extras/Trailer.mp4":                                          "",
	"His Girl Friday (1940)/His.Girl.Friday.1940.720p.WEB-DL-trailer.mp4":                "",
	"@eaDir/thumbs.mkv": "",
	".hidden/movie.mkv": "",
	"Plan.9.from.Outer.Space.1959.DVDRip.XviD-VCRXTEST/plan9.sub":                        "",
	"Plan.9.from.Outer.Space.1959.DVDRip.XviD-VCRXTEST/Plan.9.from.Outer.Space.1959.avi": "",
	"Quillmoor.2022.1080p.WEBRip.x265-VCRXTEST/Quillmoor S01E05 Fifth Bell.mkv":          "",
	"Quillmoor.2022.1080p.WEBRip.x265-VCRXTEST/Quillmoor S01E06 Sixth Bell.mkv":          "",
}

func titles(e *env, kind string) []string {
	return e.strs(`SELECT title || IFNULL(' (' || year || ')', '') FROM items WHERE kind = ? ORDER BY sort_title`, kind)
}

func versionsOf(e *env, title string) []string {
	return e.strs(`SELECT IFNULL(v.label, '') FROM media_versions v JOIN items i ON i.id = v.item_id WHERE i.title = ? ORDER BY v.label`, title)
}

func TestScanFolderGrouping(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	run := e.scan(Options{})
	if run.Seen != 8 || run.Added != 8 || run.Failed != 0 {
		t.Fatalf("run = %+v, want 8 seen and added", run)
	}
	want := []string{"His Girl Friday (1940)", "Night of the Living Dead (1968)", "Nosferatu (1922)", "Nosferatu (1922)", "Plan 9 from Outer Space (1959)"}
	if got := titles(e, "movie"); !slices.Equal(got, want) {
		t.Fatalf("movies = %q, want %q", got, want)
	}
	// Same folder and prefix: one item, two labelled versions.
	if got := versionsOf(e, "Night of the Living Dead"); !slices.Equal(got, []string{"1080p", "4K HDR REMUX"}) {
		t.Fatalf("versions = %q", got)
	}
	// Episodes in a movie library become a show tree.
	if got := titles(e, "show"); !slices.Equal(got, []string{"Quillmoor (2022)"}) {
		t.Fatalf("shows = %q", got)
	}
	if n := e.count(`SELECT COUNT(*) FROM items WHERE kind = 'episode'`); n != 2 {
		t.Fatalf("episodes = %d", n)
	}
	// The scene NFO is ASCII art, so the scan reads it without error and keeps
	// the filename metadata.
	if src := e.str(`SELECT metadata_source FROM items WHERE title = 'Nosferatu' LIMIT 1`); src != "filename" {
		t.Fatalf("metadata_source = %q", src)
	}
	if got := e.str(`SELECT sort_title FROM items WHERE title = 'Night of the Living Dead'`); got != "Night of the Living Dead" {
		t.Fatalf("sort_title = %q", got)
	}
	// Paths are absolute in path and relative in rel_path.
	p := e.str(`SELECT path FROM media_versions WHERE rel_path = ?`, "His Girl Friday (1940)/His.Girl.Friday.1940.720p.WEB-DL.mp4")
	if p != e.path("His Girl Friday (1940)/His.Girl.Friday.1940.720p.WEB-DL.mp4") {
		t.Fatalf("path = %q", p)
	}
	if n := e.count(`SELECT COUNT(*) FROM media_streams WHERE is_external = 0`); n != 8*3 {
		t.Fatalf("embedded streams = %d, want 24", n)
	}
	if got := e.str(`SELECT resolution || ' ' || hdr_type || ' ' || container FROM media_versions WHERE rel_path LIKE '%4K HDR REMUX%'`); got != "4K hdr10 mkv" {
		t.Fatalf("probe columns = %q", got)
	}
	if n := e.count(`SELECT COUNT(*) FROM items WHERE runtime_ms = 5400000 AND kind = 'movie'`); n != 5 {
		t.Fatalf("runtime from probe on %d movies, want 5", n)
	}
	if got := e.str(`SELECT status FROM scan_runs WHERE id = ?`, run.ID); got != "ok" {
		t.Fatalf("run status row = %q", got)
	}
}

func TestScanTitleYearGrouping(t *testing.T) {
	e := newEnv(t, KindMovie, "title_year", movieTree)
	e.scan(Options{})
	want := []string{"His Girl Friday (1940)", "Night of the Living Dead (1968)", "Nosferatu (1922)", "Plan 9 from Outer Space (1959)"}
	if got := titles(e, "movie"); !slices.Equal(got, want) {
		t.Fatalf("movies = %q, want %q", got, want)
	}
	// Two release folders, one film. Labels come from the names plus the probe.
	if got := versionsOf(e, "Nosferatu"); !slices.Equal(got, []string{"1080p BluRay", "4K HDR BluRay"}) {
		t.Fatalf("versions = %q", got)
	}
}

func TestGroupingModeChangeMovesVersions(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	e.scan(Options{})
	before := e.count(`SELECT COUNT(*) FROM items WHERE kind = 'movie'`)
	e.lib.Grouping = "title_year"
	run := e.scan(Options{})
	after := e.count(`SELECT COUNT(*) FROM items WHERE kind = 'movie'`)
	if before != 5 || after != 4 {
		t.Fatalf("movies %d then %d, want 5 then 4", before, after)
	}
	if run.Changed == 0 || run.Added != 0 || e.pr.total() != 8 {
		t.Fatalf("run = %+v, probes = %d: a grouping change must not re-probe", run, e.pr.total())
	}
}

func TestIncrementalRescan(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	e.scan(Options{})
	probes := e.pr.total()
	before := e.digest()
	e.clock.advance(time.Hour)
	run := e.scan(Options{Trigger: "schedule"})
	if e.pr.total() != probes {
		t.Fatalf("rescan probed %d files", e.pr.total()-probes)
	}
	if run.Changed != 0 || run.Added != 0 || run.Missing != 0 {
		t.Fatalf("rescan run = %+v", run)
	}
	if after := e.digest(); after != before {
		t.Fatalf("an unchanged rescan rewrote rows:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	// A file whose size changes is probed again; nothing else is.
	e.write(map[string]string{"His Girl Friday (1940)/His.Girl.Friday.1940.720p.WEB-DL.mp4": "a longer body than before, so the size differs"})
	run = e.scan(Options{Trigger: "schedule"})
	if run.Changed != 1 || e.pr.total() != probes+1 {
		t.Fatalf("after a change: run = %+v, probes = %d", run, e.pr.total()-probes)
	}
	// An mtime change alone counts too.
	p := e.path("Plan.9.from.Outer.Space.1959.DVDRip.XviD-VCRXTEST/Plan.9.from.Outer.Space.1959.avi")
	if err := os.Chtimes(p, time.Now(), time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	run = e.scan(Options{Trigger: "schedule"})
	if run.Changed != 1 || e.pr.total() != probes+2 {
		t.Fatalf("after a touch: run = %+v", run)
	}
}

func TestProbeFailureStillIndexes(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	e.pr.fail = "Plan.9"
	run := e.scan(Options{})
	if run.Failed != 1 || run.Added != 8 {
		t.Fatalf("run = %+v", run)
	}
	status := e.str(`SELECT probe_status || ': ' || probe_error FROM media_versions WHERE rel_path LIKE '%Plan.9%'`)
	if !strings.HasPrefix(status, "error: ffprobe:") {
		t.Fatalf("probe status = %q", status)
	}
	if got := e.str(`SELECT container FROM media_versions WHERE rel_path LIKE '%Plan.9%'`); got != "avi" {
		t.Fatalf("container from extension = %q", got)
	}
	// The title is visible even though the file could not be probed.
	if n := e.count(`SELECT COUNT(*) FROM items WHERE title = 'Plan 9 from Outer Space' AND missing_since IS NULL`); n != 1 {
		t.Fatal("item missing")
	}
	calls := e.pr.total()
	e.scan(Options{Trigger: "schedule"})
	if e.pr.total() != calls {
		t.Fatal("a scheduled scan retried a recent probe failure")
	}
	e.pr.fail = ""
	run = e.scan(Options{Trigger: "manual"})
	if e.pr.total() != calls+1 || run.Failed != 0 {
		t.Fatalf("a manual scan should retry the failed probe: %+v", run)
	}
	if got := e.str(`SELECT probe_status FROM media_versions WHERE rel_path LIKE '%Plan.9%'`); got != "ok" {
		t.Fatalf("after retry: %q", got)
	}
}

var tvTree = map[string]string{
	"The Bexbury Mysteries (2020)/tvshow.nfo":                                                               `<tvshow><title>The Bexbury Mysteries</title><year>2020</year><plot>A town.</plot><studio>VCRX Network</studio><genre>Mystery</genre></tvshow>`,
	"The Bexbury Mysteries (2020)/poster.jpg":                                                               "",
	"The Bexbury Mysteries (2020)/fanart.jpg":                                                               "",
	"The Bexbury Mysteries (2020)/season01-poster.jpg":                                                      "",
	"The Bexbury Mysteries (2020)/Season 1/The Bexbury Mysteries (2020) - S01E01 - Opening Bell.mkv":        "",
	"The Bexbury Mysteries (2020)/Season 1/The Bexbury Mysteries (2020) - S01E01 - Opening Bell-thumb.jpg":  "",
	"The Bexbury Mysteries (2020)/Season 1/The Bexbury Mysteries (2020) - S01E02E03 - Two Parter.mkv":       "",
	"The Bexbury Mysteries (2020)/Season 1/The Bexbury Mysteries (2020) - S01E04 - Lighthouse.mkv":          "",
	"The Bexbury Mysteries (2020)/Season 1/The Bexbury Mysteries (2020) - S01E04 - Lighthouse.nfo":          `<episodedetails><title>The Lighthouse Keeper</title><season>1</season><episode>4</episode><plot>Lights.</plot></episodedetails>`,
	"The Bexbury Mysteries (2020)/Season 2/folder.jpg":                                                      "",
	"The Bexbury Mysteries (2020)/Season 2/The.Bexbury.Mysteries.S02E01.Return.1080p.WEB.h264-VCRXTEST.mkv": "",
	"The Bexbury Mysteries (2020)/Specials/The.Bexbury.Mysteries.S00E01.Making.Of.mkv":                      "",
	"The Bexbury Mysteries (2020)/Specials/Marsh Lantern.mkv":                                               "",
	"Saltwick.Patrol.S01E03.Copper.Claws.720p.HDTV.x264-VCRXTEST.mkv":                                       "",
	"Saltwick Patrol/Season 1/Saltwick.Patrol.S01E03.Copper.Claws.1080p.WEB-DL.mkv":                         "",
	"Saltwick Patrol/Season 1/Saltwick.Patrol.S01E04.1080p.WEB-DL.mkv":                                      "",
	"Saltwick.Patrol.S04.1080p.AMZN.WEB-DL.x264-VCRXTEST/Saltwick.Patrol.S04E05.Kettle.Drum.1080p.mkv":      "",
	"Saltwick.Patrol.S04.1080p.AMZN.WEB-DL.x264-VCRXTEST/Saltwick.Patrol.S04E06.Tin.Lantern.1080p.mkv":      "",
	"Quorvex at Ten/Quorvex.Ten.2026.07.27.Guest.Name.1080p.WEB.h264-VCRXTEST.mkv":                          "",
	"Quorvex at Ten/Quorvex.Ten.2026.07.27.Guest.Name.1080p.WEB.h264-VCRXTEST-sample.mkv":                   "",
}

func TestScanShows(t *testing.T) {
	e := newEnv(t, KindShow, "folder", tvTree)
	run := e.scan(Options{})
	if run.Seen != 12 {
		t.Fatalf("seen = %d, want 12", run.Seen)
	}
	if got := titles(e, "show"); !slices.Equal(got, []string{"The Bexbury Mysteries (2020)", "Quorvex at Ten", "Saltwick Patrol"}) {
		t.Fatalf("shows = %q", got)
	}
	tree := e.strs(`SELECT sh.title || ' / ' || se.title || ' / ' || IFNULL(ep.season_number, '') || 'x' || IFNULL(ep.episode_number, '-') ||
			IFNULL('-' || ep.episode_number_end, '') || ' ' || ep.title || ' [' || (SELECT COUNT(*) FROM media_versions v WHERE v.item_id = ep.id) || ']'
		FROM items ep JOIN items se ON se.id = ep.parent_id JOIN items sh ON sh.id = se.parent_id
		WHERE ep.kind = 'episode' ORDER BY sh.sort_title, ep.season_number, ep.episode_number, ep.title`)
	want := []string{
		"The Bexbury Mysteries / Specials / 0x- Marsh Lantern [1]",
		"The Bexbury Mysteries / Specials / 0x1 Making Of [1]",
		"The Bexbury Mysteries / Season 1 / 1x1 Opening Bell [1]",
		"The Bexbury Mysteries / Season 1 / 1x2-3 Two Parter [1]",
		"The Bexbury Mysteries / Season 1 / 1x4 The Lighthouse Keeper [1]",
		"The Bexbury Mysteries / Season 2 / 2x1 Return [1]",
		"Quorvex at Ten / Season 2026 / 2026x727 Guest Name [1]",
		"Saltwick Patrol / Season 1 / 1x3 Copper Claws [2]",
		"Saltwick Patrol / Season 1 / 1x4 Episode 4 [1]",
		"Saltwick Patrol / Season 4 / 4x5 Kettle Drum [1]",
		"Saltwick Patrol / Season 4 / 4x6 Tin Lantern [1]",
	}
	if !slices.Equal(tree, want) {
		t.Fatalf("tree:\n%s\nwant:\n%s", strings.Join(tree, "\n"), strings.Join(want, "\n"))
	}
	// tvshow.nfo fills the show; its studio is a network.
	if got := e.str(`SELECT overview || ' ' || metadata_source FROM items WHERE kind = 'show' AND title = 'The Bexbury Mysteries'`); got != "A town. nfo" {
		t.Fatalf("show nfo = %q", got)
	}
	if got := e.str(`SELECT t.kind || ':' || t.value FROM item_tags t JOIN items i ON i.id = t.item_id WHERE i.kind = 'show' AND t.kind = 'network'`); got != "network:VCRX Network" {
		t.Fatalf("network tag = %q", got)
	}
	if got := e.str(`SELECT date FROM (SELECT premiere_date AS date FROM items WHERE kind = 'episode' AND title = 'Guest Name')`); got != "2026-07-27" {
		t.Fatalf("air date = %q", got)
	}
	art := e.strs(`SELECT i.kind || ' ' || a.kind || ' ' || replace(a.source_path, ?, '') FROM artwork a JOIN items i ON i.id = a.item_id
		ORDER BY i.kind, a.kind, a.source_path`, e.root+"/")
	wantArt := []string{
		"episode thumb The Bexbury Mysteries (2020)/Season 1/The Bexbury Mysteries (2020) - S01E01 - Opening Bell-thumb.jpg",
		"season poster The Bexbury Mysteries (2020)/Season 2/folder.jpg",
		"season poster The Bexbury Mysteries (2020)/season01-poster.jpg",
		"show backdrop The Bexbury Mysteries (2020)/fanart.jpg",
		"show poster The Bexbury Mysteries (2020)/poster.jpg",
	}
	if !slices.Equal(art, wantArt) {
		t.Fatalf("artwork:\n%s", strings.Join(art, "\n"))
	}
	// Every parent link is in the same library and points the right way.
	if n := e.count(`SELECT COUNT(*) FROM items c JOIN items p ON p.id = c.parent_id WHERE NOT (
		(c.kind = 'season' AND p.kind = 'show') OR (c.kind = 'episode' AND p.kind = 'season'))`); n != 0 {
		t.Fatalf("%d bad parent links", n)
	}
}

func TestSidecarSubtitles(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", map[string]string{
		"Nosferatu (1922)/Nosferatu (1922).mkv":                           "",
		"Nosferatu (1922)/Nosferatu (1922).srt":                           "",
		"Nosferatu (1922)/Nosferatu (1922).en.srt":                        "",
		"Nosferatu (1922)/Nosferatu (1922).eng.forced.srt":                "",
		"Nosferatu (1922)/Nosferatu (1922).English.sdh.srt":               "",
		"Nosferatu (1922)/Nosferatu (1922).en.hi.ass":                     "",
		"Nosferatu (1922)/Nosferatu (1922).fr.default.vtt":                "",
		"Nosferatu (1922)/Nosferatu (1922).pt-BR.srt":                     "",
		"Nosferatu (1922)/Nosferatu (1922).de.sup":                        "",
		"Nosferatu (1922)/Nosferatu (1922).idx":                           "",
		"Nosferatu (1922)/Nosferatu (1922).sub":                           "",
		"Nosferatu (1922)/Nosferatu (1922).nl.idx":                        "",
		"Nosferatu (1922)/Nosferatu (1922).Commentary.en.srt":             "",
		"Nosferatu (1922)/Subs/2_English.srt":                             "",
		"Nosferatu (1922)/Subs/Nosferatu (1922).es.srt":                   "",
		"Nosferatu (1922)/notes.txt":                                      "",
		"His Girl Friday (1940)/His Girl Friday (1940).mkv":               "",
		"His Girl Friday (1940)/His Girl Friday (1940) Extended.mkv":      "",
		"His Girl Friday (1940)/His Girl Friday (1940) Extended.en.srt":   "",
		"His Girl Friday (1940)/His Girl Friday (1940).en.srt":            "",
		"His Girl Friday (1940)/Subs/His Girl Friday (1940)/3_French.srt": "",
		"His Girl Friday (1940)/Subs/4_German.srt":                        "",
	})
	e.scan(Options{})
	got := e.strs(`SELECT replace(s.external_path, ?, '') || ' ' || s.codec || ' ' || IFNULL(s.language, '-') || ' ' ||
			s.is_forced || s.is_hearing_impaired || s.is_default || s.is_image_based || IFNULL(' ' || s.title, '')
		FROM media_streams s WHERE s.is_external = 1 ORDER BY s.external_path`, e.root+"/")
	want := []string{
		"His Girl Friday (1940)/His Girl Friday (1940) Extended.en.srt subrip eng 0000",
		"His Girl Friday (1940)/His Girl Friday (1940).en.srt subrip eng 0000",
		"His Girl Friday (1940)/Subs/His Girl Friday (1940)/3_French.srt subrip fre 0000",
		"Nosferatu (1922)/Nosferatu (1922).Commentary.en.srt subrip eng 0000 Commentary",
		"Nosferatu (1922)/Nosferatu (1922).English.sdh.srt subrip eng 0100",
		"Nosferatu (1922)/Nosferatu (1922).de.sup hdmv_pgs_subtitle ger 0001",
		"Nosferatu (1922)/Nosferatu (1922).en.hi.ass ass eng 0100",
		"Nosferatu (1922)/Nosferatu (1922).en.srt subrip eng 0000",
		"Nosferatu (1922)/Nosferatu (1922).eng.forced.srt subrip eng 1000",
		"Nosferatu (1922)/Nosferatu (1922).fr.default.vtt webvtt fre 0010",
		"Nosferatu (1922)/Nosferatu (1922).idx dvd_subtitle - 0001",
		"Nosferatu (1922)/Nosferatu (1922).pt-BR.srt subrip por 0000",
		"Nosferatu (1922)/Nosferatu (1922).srt subrip - 0000",
		"Nosferatu (1922)/Subs/2_English.srt subrip eng 0000",
		"Nosferatu (1922)/Subs/Nosferatu (1922).es.srt subrip spa 0000",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("sidecars:\n%s", strings.Join(got, "\n"))
	}
	// Each sidecar belongs to the video with the longest matching name.
	owner := e.str(`SELECT v.rel_path FROM media_streams s JOIN media_versions v ON v.id = s.version_id WHERE s.external_path LIKE '%Extended.en.srt'`)
	if owner != "His Girl Friday (1940)/His Girl Friday (1940) Extended.mkv" {
		t.Fatalf("owner = %q", owner)
	}

	// A subtitle Bazarr adds later is picked up without a probe.
	probes := e.pr.total()
	e.write(map[string]string{"Nosferatu (1922)/Nosferatu (1922).it.srt": ""})
	run := e.scan(Options{})
	if run.Changed != 1 || e.pr.total() != probes {
		t.Fatalf("new sidecar: run = %+v, probes %d", run, e.pr.total()-probes)
	}
	if n := e.count(`SELECT COUNT(*) FROM media_streams WHERE language = 'ita' AND is_external = 1`); n != 1 {
		t.Fatal("new sidecar not recorded")
	}
}

func TestMovieArtworkAndNFO(t *testing.T) {
	nfo := `<?xml version="1.0" encoding="UTF-8"?>
<movie>
  <title>Night of the Living Dead</title>
  <sorttitle>Living Dead 1968</sorttitle>
  <year>1968</year>
  <plot>A farmhouse under siege.</plot>
  <mpaa>Rated Unrated</mpaa>
  <uniqueid type="imdb">tt0063350</uniqueid>
  <uniqueid type="tmdb">10331</uniqueid>
  <genre>Horror</genre>
  <director>George A. Romero</director>
  <studio>Image Ten</studio>
  <ratings><rating name="imdb" max="10"><value>7.8</value><votes>140000</votes></rating></ratings>
  <tag>public domain</tag>
  <actor><name>Duane Jones</name><role>Ben</role><order>0</order></actor>
</movie>`
	e := newEnv(t, KindMovie, "folder", map[string]string{
		"NOTLD/notld.1968.1080p.mkv":            "",
		"NOTLD/movie.nfo":                       nfo,
		"NOTLD/poster.jpg":                      "",
		"NOTLD/notld.1968.1080p-fanart.jpg":     "",
		"NOTLD/clearlogo.png":                   "",
		"Nosferatu (1922)/Nosferatu (1922).mkv": "",
		"Nosferatu (1922)/Nosferatu.1922.nfo":   "https://www.imdb.com/title/tt0013442/\n",
		"Nosferatu (1922)/folder.jpg":           "",
		"loose.film.1999.mkv":                   "",
		"poster.jpg":                            "",
	})
	e.scan(Options{})
	got := e.str(`SELECT title || '|' || sort_title || '|' || year || '|' || overview || '|' || content_rating || '|' || imdb_id || '|' || tmdb_id || '|' || metadata_source
		FROM items WHERE kind = 'movie' AND imdb_id = 'tt0063350'`)
	if got != "Night of the Living Dead|Living Dead 1968|1968|A farmhouse under siege.|Unrated|tt0063350|10331|nfo" {
		t.Fatalf("nfo item = %q", got)
	}
	tags := e.strs(`SELECT t.kind || '=' || t.value || IFNULL('/' || t.role, '') FROM item_tags t ORDER BY t.kind, t.value`)
	if !slices.Equal(tags, []string{"actor=Duane Jones/Ben", "director=George A. Romero", "genre=Horror", "studio=Image Ten"}) {
		t.Fatalf("tags = %q", tags)
	}
	if got := e.str(`SELECT source || '=' || value || '/' || votes FROM item_ratings`); got != "imdb=7.8/140000" {
		t.Fatalf("ratings = %q", got)
	}
	if got := e.str(`SELECT l.name || ':' || il.source FROM item_labels il JOIN labels l ON l.id = il.label_id`); got != "public domain:nfo" {
		t.Fatalf("labels = %q", got)
	}
	// A bare-URL NFO gives only the IMDb id.
	if got := e.str(`SELECT imdb_id || ' ' || metadata_source || ' ' || title FROM items WHERE title = 'Nosferatu'`); got != "tt0013442 filename Nosferatu" {
		t.Fatalf("url nfo = %q", got)
	}
	art := e.strs(`SELECT i.title || ' ' || a.kind || ' ' || replace(a.source_path, ?, '') FROM artwork a JOIN items i ON i.id = a.item_id ORDER BY 1`, e.root+"/")
	wantArt := []string{
		"Night of the Living Dead backdrop NOTLD/notld.1968.1080p-fanart.jpg",
		"Night of the Living Dead logo NOTLD/clearlogo.png",
		"Night of the Living Dead poster NOTLD/poster.jpg",
		"Nosferatu poster Nosferatu (1922)/folder.jpg",
	}
	if !slices.Equal(art, wantArt) {
		t.Fatalf("artwork:\n%s", strings.Join(art, "\n"))
	}

	// A locked title survives an NFO edit; unlocked fields follow it.
	if _, err := e.db.Exec(`UPDATE items SET locked_fields = '["title"]', title = 'My Title' WHERE imdb_id = 'tt0063350'`); err != nil {
		t.Fatal(err)
	}
	e.write(map[string]string{"NOTLD/movie.nfo": strings.Replace(nfo, "A farmhouse under siege.", "Updated plot.", 1)})
	e.clock.advance(time.Minute)
	e.scan(Options{})
	if got := e.str(`SELECT title || '|' || overview FROM items WHERE imdb_id = 'tt0063350'`); got != "My Title|Updated plot." {
		t.Fatalf("after nfo edit = %q", got)
	}

	// Local art replaces a TMDb poster, and a deleted file drops its row.
	if _, err := e.db.Exec(`UPDATE artwork SET source = 'tmdb', source_path = NULL, url = 'https://image.tmdb.org/x.jpg' WHERE kind = 'poster' AND source_path LIKE '%folder.jpg'`); err != nil {
		t.Fatal(err)
	}
	e.remove("NOTLD/clearlogo.png")
	e.scan(Options{})
	if got := e.str(`SELECT source FROM artwork a JOIN items i ON i.id = a.item_id WHERE i.title = 'Nosferatu' AND a.kind = 'poster'`); got != "local" {
		t.Fatalf("detour poster source = %q", got)
	}
	if n := e.count(`SELECT COUNT(*) FROM artwork WHERE kind = 'logo'`); n != 0 {
		t.Fatal("deleted logo still recorded")
	}
}

// Rule 1: a missing, unreadable or non-folder root fails and changes nothing.
func TestRootUnavailable(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	e.scan(Options{})
	before := e.digest()
	check := func(name string) {
		t.Helper()
		run, err := e.scanErr(Options{})
		if !errors.Is(err, ErrRootUnavailable) || run.Status != StatusError || run.Error == "" {
			t.Fatalf("%s: err = %v, run = %+v", name, err, run)
		}
		if got := e.str(`SELECT status || ': ' || error FROM scan_runs WHERE id = ?`, run.ID); !strings.HasPrefix(got, "error: library root unavailable") {
			t.Fatalf("%s: run row = %q", name, got)
		}
		if after := e.digest(); after != before {
			t.Fatalf("%s: rows changed", name)
		}
	}
	real := e.root
	moved := real + ".away"
	if err := os.Rename(real, moved); err != nil {
		t.Fatal(err)
	}
	check("missing")
	if err := os.WriteFile(real, []byte("not a folder"), 0o644); err != nil {
		t.Fatal(err)
	}
	check("file")
	if err := os.Remove(real); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, real); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		if err := os.Chmod(real, 0o000); err != nil {
			t.Fatal(err)
		}
		check("unreadable")
		if err := os.Chmod(real, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e.lib.RootPath = "relative/path"
	check("relative")
}

// Rule 2: a root that suddenly holds nothing aborts unless forced.
func TestEmptyRootAborts(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	e.scan(Options{})
	before := e.digest()
	for rel := range movieTree {
		e.remove(strings.Split(rel, "/")[0])
	}
	run, err := e.scanErr(Options{})
	if !errors.Is(err, ErrAborted) || run.Status != StatusAborted || !strings.Contains(run.Error, "holds no video files") {
		t.Fatalf("err = %v, run = %+v", err, run)
	}
	if e.digest() != before {
		t.Fatal("aborted run changed rows")
	}
	run = e.scan(Options{Force: true})
	if run.Missing != 8 {
		t.Fatalf("forced run = %+v", run)
	}
}

// Rule 3: more than half the library vanishing in one full scan aborts.
func TestMassMissingAborts(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	e.scan(Options{})
	before := e.digest()
	e.remove("Night of the Living Dead (1968)", "Nosferatu.1922.1080p.BluRay.x264-VCRXTEST", "Nosferatu.1922.2160p.UHD.BluRay.x265-SAMPLEGRP",
		"Quillmoor.2022.1080p.WEBRip.x265-VCRXTEST")
	run, err := e.scanErr(Options{})
	if !errors.Is(err, ErrAborted) || run.Status != StatusAborted || !strings.Contains(run.Error, "6 of 8") {
		t.Fatalf("err = %v, run = %+v", err, run)
	}
	if e.digest() != before {
		t.Fatal("aborted run changed rows")
	}
	if got := e.str(`SELECT status FROM scan_runs WHERE id = ?`, run.ID); got != "aborted" {
		t.Fatalf("run row status = %q", got)
	}
	run = e.scan(Options{Force: true})
	if run.Missing != 6 {
		t.Fatalf("forced run = %+v", run)
	}
}

// Rule 4: missing files are flagged, come back cleanly, and are only deleted
// after the retention period.
func TestMissingLifecycle(t *testing.T) {
	e := newEnv(t, KindShow, "folder", tvTree)
	e.scan(Options{})
	gone := "Saltwick.Patrol.S01E03.Copper.Claws.720p.HDTV.x264-VCRXTEST.mkv"
	stash := filepath.Join(t.TempDir(), "stash.mkv")
	if err := os.Rename(e.path(gone), stash); err != nil {
		t.Fatal(err)
	}
	e.remove("Quorvex at Ten")
	e.clock.advance(time.Hour)
	run := e.scan(Options{})
	if run.Missing != 2 {
		t.Fatalf("run = %+v", run)
	}
	if n := e.count(`SELECT COUNT(*) FROM media_versions WHERE missing_since IS NOT NULL`); n != 2 {
		t.Fatalf("missing versions = %d", n)
	}
	// Copper Claws keeps its 1080p version, so the episode stays visible; Quorvex
	// at Ten lost its only file, so the episode, season and show are hidden.
	if n := e.count(`SELECT COUNT(*) FROM items WHERE title = 'Copper Claws' AND missing_since IS NULL`); n != 1 {
		t.Fatal("episode with a remaining version was hidden")
	}
	hidden := e.strs(`SELECT kind FROM items WHERE missing_since IS NOT NULL ORDER BY kind`)
	if !slices.Equal(hidden, []string{"episode", "season", "show"}) {
		t.Fatalf("hidden = %q", hidden)
	}
	// It comes back: everything is visible again with the same ids.
	id := e.count(`SELECT id FROM media_versions WHERE rel_path = ?`, gone)
	if err := os.Rename(stash, e.path(gone)); err != nil {
		t.Fatal(err)
	}
	e.scan(Options{})
	if n := e.count(`SELECT COUNT(*) FROM media_versions WHERE id = ? AND missing_since IS NULL`, id); n != 1 {
		t.Fatal("reappeared file not restored")
	}
	// Still gone after 30 days: rows go, and the empty episode, season and show with them.
	e.clock.advance(MissingRetention - 2*time.Hour)
	e.scan(Options{})
	if n := e.count(`SELECT COUNT(*) FROM items WHERE title = 'Quorvex at Ten'`); n != 1 {
		t.Fatal("deleted before the retention period")
	}
	e.clock.advance(3 * time.Hour)
	e.scan(Options{})
	if n := e.count(`SELECT COUNT(*) FROM items WHERE title IN ('Quorvex at Ten', 'Season 2026', 'Guest Name')`); n != 0 {
		t.Fatalf("%d empty items left after retention", n)
	}
	if n := e.count(`SELECT COUNT(*) FROM media_versions WHERE missing_since IS NOT NULL`); n != 0 {
		t.Fatal("expired versions kept")
	}
}

// Rule 5: a targeted scan touches only its path and cannot leave the root.
func TestTargetedScan(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	e.scan(Options{})
	e.remove("His Girl Friday (1940)/His.Girl.Friday.1940.720p.WEB-DL.mp4")
	e.write(map[string]string{"Night of the Living Dead (1968)/Night of the Living Dead (1968) - Colorized.mkv": ""})
	run := e.scan(Options{Trigger: "webhook", SubPath: "Night of the Living Dead (1968)"})
	if run.Seen != 3 || run.Added != 1 || run.Missing != 0 || run.PathFilter != "Night of the Living Dead (1968)" {
		t.Fatalf("run = %+v", run)
	}
	if n := e.count(`SELECT COUNT(*) FROM media_versions WHERE missing_since IS NOT NULL`); n != 0 {
		t.Fatal("targeted scan marked a file outside its path missing")
	}
	// A single file works too, and an absolute path inside the root.
	e.write(map[string]string{"Nosferatu (1922)/Nosferatu (1922).mkv": ""})
	run = e.scan(Options{Trigger: "webhook", SubPath: e.path("Nosferatu (1922)/Nosferatu (1922).mkv")})
	if run.Added != 1 || run.Seen != 1 {
		t.Fatalf("file run = %+v", run)
	}

	outside := t.TempDir()
	writeFiles(t, outside, map[string]string{"Secret (2000)/Secret (2000).mkv": ""})
	if err := os.Symlink(filepath.Join(outside, "Secret (2000)"), e.path("Linked")); err != nil {
		t.Fatal(err)
	}
	inside := e.path("Nosferatu.1922.1080p.BluRay.x264-VCRXTEST")
	if err := os.Symlink(inside, e.path("Alias")); err != nil {
		t.Fatal(err)
	}
	before := e.digest()
	for _, sub := range []string{"../", "../outside", "Night of the Living Dead (1968)/../../x", outside, "Linked", "Alias", "Does Not Exist"} {
		run, err := e.scanErr(Options{SubPath: sub})
		if err == nil || run.Status != StatusError {
			t.Fatalf("SubPath %q: err = %v, run = %+v", sub, err, run)
		}
	}
	if _, err := e.scanErr(Options{SubPath: "../x"}); !errors.Is(err, fsafe.ErrOutsideRoot) {
		t.Fatalf("../ err = %v", err)
	}
	if e.digest() != before {
		t.Fatal("rejected targeted scans changed rows")
	}
}

// Rule 6: symlinks leading out of the root are never followed.
func TestSymlinksStayInRoot(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", map[string]string{"Nosferatu (1922)/Nosferatu (1922).mkv": ""})
	outside := t.TempDir()
	writeFiles(t, outside, map[string]string{"Secret (2000)/Secret (2000).mkv": "", "Other (2001).mkv": "", "Secret (2000)/poster.jpg": ""})
	links := map[string]string{
		"Secret Folder":                            filepath.Join(outside, "Secret (2000)"),
		"Other (2001).mkv":                         filepath.Join(outside, "Other (2001).mkv"),
		"Nosferatu (1922)/poster.jpg":              filepath.Join(outside, "Secret (2000)", "poster.jpg"),
		"Nosferatu (1922)/Nosferatu (1922).en.srt": filepath.Join(outside, "missing.srt"),
		"Nosferatu Again (1922).mkv":               e.path("Nosferatu (1922)/Nosferatu (1922).mkv"),
		"Loop":                                     e.root,
	}
	for rel, target := range links {
		if err := os.Symlink(target, e.path(rel)); err != nil {
			t.Fatal(err)
		}
	}
	run := e.scan(Options{})
	got := e.strs(`SELECT rel_path FROM media_versions ORDER BY rel_path`)
	// The link to a file inside the root is kept; nothing outside is reached,
	// and the loop back to the root is not descended.
	if !slices.Equal(got, []string{"Nosferatu (1922)/Nosferatu (1922).mkv", "Nosferatu Again (1922).mkv"}) || run.Seen != 2 {
		t.Fatalf("versions = %q", got)
	}
	if n := e.count(`SELECT COUNT(*) FROM artwork`); n != 0 {
		t.Fatal("artwork followed a symlink out of the root")
	}
	if n := e.count(`SELECT COUNT(*) FROM media_streams WHERE is_external = 1`); n != 0 {
		t.Fatal("dangling subtitle link recorded")
	}
	for p := range e.pr.calls {
		if !strings.HasPrefix(p, e.root) {
			t.Fatalf("probed outside the root: %s", p)
		}
	}
}

// Rule 7: scanning never modifies the media tree, even when it is writable,
// and works when it is read-only.
func TestMediaIsReadOnly(t *testing.T) {
	files := map[string]string{}
	for k, v := range movieTree {
		files[k] = v
	}
	for k, v := range tvTree {
		files["TV/"+k] = v
	}
	e := newEnv(t, KindMovie, "folder", files)
	snap := snapshot(t, e.root)
	e.scan(Options{})
	e.scan(Options{Trigger: "manual", Force: true})
	if got := snapshot(t, e.root); got != snap {
		t.Fatal("the scan modified the media tree")
	}
	if os.Geteuid() == 0 {
		t.Log("running as root: read-only permissions are not enforced, the snapshot check above still applies")
	}
	setMode(t, e.root, 0o555, 0o444)
	t.Cleanup(func() { setMode(t, e.root, 0o755, 0o644) })
	// A fresh database over the read-only tree indexes everything.
	conn := newDB(t)
	fresh := addLibrary(t, conn, "Read only", KindMovie, e.root, "folder")
	run, err := (&Scanner{DB: conn, Prober: &fakeProber{}, Log: quiet}).Scan(context.Background(), fresh, Options{})
	if err != nil || run.Status != StatusOK || run.Added != run.Seen || run.Seen == 0 {
		t.Fatalf("read-only scan: %v, %+v", err, run)
	}
	setMode(t, e.root, 0o755, 0o644)
	if got := snapshot(t, e.root); got != snap {
		t.Fatal("the scan modified the media tree")
	}
}

// snapshot records every path under root with its type, size, mode, mtime and
// a content hash.
func snapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		sum := ""
		if fi.Mode().IsRegular() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum = fmt.Sprintf("%x", sha256.Sum256(data))
		}
		fmt.Fprintf(&b, "%s %v %d %d %s\n", strings.TrimPrefix(p, root), fi.Mode(), fi.Size(), fi.ModTime().UnixNano(), sum)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func setMode(t *testing.T, root string, dirMode, fileMode os.FileMode) {
	t.Helper()
	// Directories are opened for writing first so their children stay reachable.
	var dirs []string
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return os.Chmod(p, 0o755)
		}
		return os.Chmod(p, fileMode)
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i], dirMode); err != nil {
			t.Fatal(err)
		}
	}
}
