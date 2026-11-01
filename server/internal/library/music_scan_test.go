package library

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/probe"
)

// countingProber is the real ffprobe, counting audio probes per file.
type countingProber struct {
	probe.FFprobe
	mu    sync.Mutex
	calls map[string]int
}

func (c *countingProber) ProbeAudio(ctx context.Context, p string) (*probe.AudioResult, error) {
	c.mu.Lock()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[p]++
	c.mu.Unlock()
	return c.FFprobe.ProbeAudio(ctx, p)
}

func (c *countingProber) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range c.calls {
		n += v
	}
	return n
}

func needFFmpeg(t *testing.T) string {
	t.Helper()
	ff, err1 := exec.LookPath("ffmpeg")
	_, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("ffmpeg and ffprobe must be installed in CI")
		}
		t.Skip("ffmpeg or ffprobe not on PATH")
	}
	return ff
}

// tone writes a short sine tone with tags, as ffmpeg writes them. cover, when
// set, is embedded as the file's picture.
func tone(t *testing.T, ff, root, rel string, freq int, codec string, tags map[string]string, cover string) string {
	t.Helper()
	out := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=" + strconv.Itoa(freq) + ":duration=1"}
	if cover != "" {
		args = append(args, "-i", cover, "-map", "0:a", "-map", "1:v", "-c:v", "mjpeg", "-disposition:v", "attached_pic")
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-metadata", k+"="+tags[k])
	}
	args = append(args, "-c:a", codec, out)
	if b, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %s: %v\n%s", rel, err, b)
	}
	return out
}

// streamMD5 reads the MD5 of the decoded audio that FLAC keeps in its
// STREAMINFO block, which is what an .ffp file lists.
func streamMD5(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil || len(b) < 42 {
		t.Fatalf("read %s: %v", p, err)
	}
	return hex.EncodeToString(b[26:42])
}

type musicFixture struct {
	root, cover string
	conn        *sql.DB
	lib         Library
	sc          *Scanner
	pr          *countingProber
}

func newMusicFixture(t *testing.T) *musicFixture {
	t.Helper()
	ff := needFFmpeg(t)
	root := t.TempDir()
	cover := filepath.Join(t.TempDir(), "art.png")
	if b, err := exec.Command(ff, "-loglevel", "error", "-f", "lavfi", "-i", "color=c=0x1b3a6b:s=64x64", "-frames:v", "1", cover).CombinedOutput(); err != nil {
		t.Fatalf("cover: %v %s", err, b)
	}
	// A studio album with an album artist tag, ReplayGain, MusicBrainz ids and
	// a cover.jpg beside it.
	for i, title := range []string{"Low Tide", "Breakwater"} {
		tone(t, ff, root, "Glass Harbor/Signal Fires (2019)/0"+strconv.Itoa(i+1)+" - "+title+".flac", 330+110*i, "flac", map[string]string{
			"title": title, "artist": "Glass Harbor", "album_artist": "Glass Harbor", "album": "Signal Fires", "track": strconv.Itoa(i+1) + "/2",
			"disc": "1/1", "date": "2019-04-02", "genre": "Rock", "REPLAYGAIN_TRACK_GAIN": "-6.20 dB", "REPLAYGAIN_ALBUM_GAIN": "-5.90 dB",
			"MUSICBRAINZ_ALBUMID": "b5a1e2c0-0000-4000-8000-000000000001",
		}, "")
	}
	if b, err := exec.Command(ff, "-loglevel", "error", "-i", cover, filepath.Join(root, "Glass Harbor", "Signal Fires (2019)", "cover.jpg")).CombinedOutput(); err != nil {
		t.Fatalf("cover.jpg: %v %s", err, b)
	}
	// A compilation without an album artist: two artists, one album title.
	tone(t, ff, root, "Compilations/Night Market Sampler/01 Neon Rain.mp3", 500, "libmp3lame",
		map[string]string{"title": "Neon Rain", "artist": "Lumen Drive", "album": "Night Market Sampler", "track": "1", "date": "2021"}, cover)
	tone(t, ff, root, "Compilations/Night Market Sampler/02 Static Bloom.mp3", 520, "libmp3lame",
		map[string]string{"title": "Static Bloom", "artist": "Cedar Static", "album": "Night Market Sampler", "track": "2", "date": "2021"}, cover)
	// ALAC in MP4, flagged as a compilation.
	tone(t, ff, root, "Cedar Static/Low Orbit/01 Apogee.m4a", 600, "alac",
		map[string]string{"title": "Apogee", "artist": "Cedar Static", "album": "Low Orbit Covers", "compilation": "1"}, "")
	// A live show as tapers ship it: etree folder and file names, an info
	// file, an .ffp and an .md5, and only artist and title tags.
	show := "Glass Harbor/gh2024-06-14.sbd.miller.flac16"
	var ffp, md5 []string
	for i, title := range []string{"Low Tide", "Breakwater", "Undertow"} {
		name := "gh2024-06-14d1t0" + strconv.Itoa(i+1) + ".flac"
		p := tone(t, ff, root, show+"/"+name, 400+50*i, "flac", map[string]string{"title": title, "artist": "Glass Harbor"}, "")
		ffp = append(ffp, name+":"+streamMD5(t, p))
		md5 = append(md5, strings.Repeat(strconv.Itoa(i+1), 32)+" *"+name)
	}
	writeFiles(t, root, map[string]string{
		show + "/gh2024-06-14.sbd.miller.flac16.ffp": strings.Join(ffp, "\r\n") + "\r\n",
		show + "/gh2024-06-14.sbd.miller.flac16.md5": strings.Join(md5, "\n") + "\n",
		show + "/gh2024-06-14.sbd.miller.flac16.txt": "Glass Harbor\n2024-06-14\nThe Lantern Room\nPortland, OR\n\n" +
			"Source: SBD > Zoom F8 > SD\nLineage: SD > WAV > FLAC\nTaper: Mo Ellery\n\nd1t01 Low Tide\n",
	})
	conn := newDB(t)
	f := &musicFixture{root: root, cover: cover, conn: conn, lib: addLibrary(t, conn, "Music", KindMusic, root, "folder"), pr: &countingProber{}}
	f.sc = &Scanner{DB: conn, Prober: f.pr, Log: quiet}
	return f
}

func (f *musicFixture) scan(t *testing.T, opt Options) Run {
	t.Helper()
	run, err := f.sc.Scan(context.Background(), f.lib, opt)
	if err != nil {
		t.Fatalf("scan: %v (%+v)", err, run)
	}
	return run
}

func (f *musicFixture) str(t *testing.T, q string, args ...any) string {
	t.Helper()
	var s sql.NullString
	if err := f.conn.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s.String
}

func (f *musicFixture) n(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.conn.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// A music scan with the real ffprobe: tags, grouping, compilations, a live
// show's date, venue and source, its checksums, and cover art.
func TestMusicScanReadsTagsAndGroups(t *testing.T) {
	f := newMusicFixture(t)
	run := f.scan(t, Options{Trigger: "cli"})
	if run.Seen != 8 || run.Added != 8 || run.Failed != 0 {
		t.Fatalf("run = %+v", run)
	}
	albums := map[string]string{}
	func() {
		rows, err := f.conn.Query(`SELECT a.title, ar.name || '|' || a.compilation || '|' || a.live || '|' || COALESCE(a.date, '') || '|' ||
				COALESCE(a.cover_source, '') || '|' || (SELECT COUNT(*) FROM music_tracks t WHERE t.album_id = a.id)
			FROM music_albums a JOIN music_artists ar ON ar.id = a.artist_id`)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				t.Fatal(err)
			}
			albums[k] = v
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}()
	want := map[string]string{
		"Signal Fires":                              "Glass Harbor|0|0|2019-04-02|file|2",
		"Night Market Sampler":                      VariousArtists + "|1|0|2021|embedded|2",
		"Low Orbit Covers":                          VariousArtists + "|1|0|||1",
		"2024-06-14 The Lantern Room, Portland, OR": "Glass Harbor|0|1|2024-06-14||3",
	}
	if len(albums) != len(want) {
		t.Errorf("albums = %v", albums)
	}
	for k, v := range want {
		if albums[k] != v {
			t.Errorf("album %q = %q, want %q", k, albums[k], v)
		}
	}
	// Tags on a studio track.
	var title, codec, container string
	var trackNo, disc, sampleRate, bitDepth int
	var gain float64
	if err := f.conn.QueryRow(`SELECT title, codec, container, track_number, disc_number, sample_rate, bit_depth, replaygain_track_gain
		FROM music_tracks WHERE rel_path LIKE '%02 - Breakwater.flac'`).Scan(&title, &codec, &container, &trackNo, &disc, &sampleRate, &bitDepth, &gain); err != nil {
		t.Fatal(err)
	}
	if title != "Breakwater" || codec != "flac" || container != "flac" || trackNo != 2 || disc != 1 || sampleRate != 44100 || bitDepth != 16 || gain != -6.2 {
		t.Errorf("studio track: %q %s %s #%d disc %d %d Hz %d-bit gain %v", title, codec, container, trackNo, disc, sampleRate, bitDepth, gain)
	}
	if g := f.str(t, `SELECT replaygain_gain FROM music_albums WHERE title = 'Signal Fires'`); g != "-5.9" {
		t.Errorf("album gain %q", g)
	}
	if m := f.str(t, `SELECT musicbrainz_id FROM music_albums WHERE title = 'Signal Fires'`); m != "b5a1e2c0-0000-4000-8000-000000000001" {
		t.Errorf("album MusicBrainz id %q", m)
	}
	if c := f.str(t, `SELECT codec || '/' || container FROM music_tracks WHERE rel_path LIKE '%Apogee.m4a'`); c != "alac/m4a" {
		t.Errorf("ALAC track: %s", c)
	}
	// A compilation's track keeps its own artist.
	if a := f.str(t, `SELECT ar.name FROM music_tracks t JOIN music_artists ar ON ar.id = t.artist_id WHERE t.title = 'Neon Rain'`); a != "Lumen Drive" {
		t.Errorf("compilation track artist %q", a)
	}
	// The live show: parsed and stored.
	var venue, location, source, detail, lineage, taper, info string
	if err := f.conn.QueryRow(`SELECT venue, location, source, source_detail, lineage, taper, info_path FROM music_albums WHERE live = 1`).
		Scan(&venue, &location, &source, &detail, &lineage, &taper, &info); err != nil {
		t.Fatal(err)
	}
	if venue != "The Lantern Room" || location != "Portland, OR" || source != "SBD" || detail != "SBD > Zoom F8 > SD" ||
		lineage != "SD > WAV > FLAC" || taper != "Mo Ellery" || !strings.HasSuffix(info, ".txt") {
		t.Errorf("show: %q %q %q %q %q %q %q", venue, location, source, detail, lineage, taper, info)
	}
	if got := f.str(t, `SELECT group_concat(track_number || ':' || title, ' ') FROM (SELECT t.track_number, t.title FROM music_tracks t
		JOIN music_albums a ON a.id = t.album_id WHERE a.live = 1 ORDER BY t.track_number)`); got != "1:Low Tide 2:Breakwater 3:Undertow" {
		t.Errorf("show tracks from file names: %q", got)
	}
	// Checksums: every file, both algorithms, the ffp matching the FLAC's own.
	if n := f.n(t, `SELECT COUNT(*) FROM music_checksums`); n != 6 {
		t.Errorf("%d checksum rows, want 6", n)
	}
	p := filepath.Join(f.root, "Glass Harbor", "gh2024-06-14.sbd.miller.flac16", "gh2024-06-14d1t02.flac")
	if v := f.str(t, `SELECT value FROM music_checksums WHERE file_name = 'gh2024-06-14d1t02.flac' AND algorithm = 'ffp'`); v != streamMD5(t, p) {
		t.Errorf("ffp %q", v)
	}
	if v := f.str(t, `SELECT value FROM music_checksums WHERE file_name = 'gh2024-06-14d1t03.flac' AND algorithm = 'md5'`); v != strings.Repeat("3", 32) {
		t.Errorf("md5 %q", v)
	}
	if n := f.n(t, `SELECT COUNT(*) FROM music_artists WHERE name IN ('Glass Harbor', 'Lumen Drive', 'Cedar Static', ?)`, VariousArtists); n != 4 {
		t.Errorf("%d artists", n)
	}
}

// Rescans probe only what changed; a file that vanishes is marked missing,
// never deleted on the first miss, and comes back when the file does; an
// emptied root changes nothing.
// The half rule counts tracks already missing, so a music share that loses
// folders a scan at a time aborts once more than half is gone, as for video.
func TestMusicMassMissingAbortsAcrossScans(t *testing.T) {
	f := newMusicFixture(t)
	f.scan(t, Options{Trigger: "cli"})
	total := f.n(t, `SELECT COUNT(*) FROM music_tracks`)
	aborted := false
	for _, dir := range []string{"Cedar Static", "Compilations", "Glass Harbor/Signal Fires (2019)", "Glass Harbor"} {
		if err := os.RemoveAll(filepath.Join(f.root, filepath.FromSlash(dir))); err != nil {
			t.Fatal(err)
		}
		run, err := f.sc.Scan(context.Background(), f.lib, Options{Trigger: "schedule"})
		if errors.Is(err, ErrAborted) {
			if !strings.Contains(run.Error, "more than half") {
				t.Fatalf("abort after removing %s: %+v", dir, run)
			}
			aborted = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if n := f.n(t, `SELECT COUNT(*) FROM music_tracks WHERE missing_since IS NOT NULL`); n*2 > total {
			t.Fatalf("after removing %s, %d of %d tracks are missing and the scan did not abort", dir, n, total)
		}
	}
	if !aborted {
		t.Fatal("losing the whole music share over four scans never aborted")
	}
}

func TestMusicScanIncrementalAndMissing(t *testing.T) {
	f := newMusicFixture(t)
	f.scan(t, Options{Trigger: "cli"})
	first := f.pr.total()
	run := f.scan(t, Options{Trigger: "schedule"})
	if f.pr.total() != first || run.Changed != 0 || run.Added != 0 {
		t.Fatalf("an unchanged rescan probed %d files (run %+v)", f.pr.total()-first, run)
	}

	gone := filepath.Join(f.root, "Glass Harbor", "Signal Fires (2019)", "02 - Breakwater.flac")
	moved := gone + ".away"
	if err := os.Rename(gone, moved); err != nil {
		t.Fatal(err)
	}
	run = f.scan(t, Options{Trigger: "schedule"})
	if run.Missing != 1 {
		t.Fatalf("run = %+v", run)
	}
	if n := f.n(t, `SELECT COUNT(*) FROM music_tracks WHERE path = ? AND missing_since IS NOT NULL`, gone); n != 1 {
		t.Fatal("the vanished track is not kept and marked missing")
	}
	if n := f.n(t, `SELECT COUNT(*) FROM music_albums WHERE title = 'Signal Fires' AND missing_since IS NULL`); n != 1 {
		t.Fatal("the album went missing with one track left")
	}

	// Every file of an album gone: the album is hidden, its rows kept.
	low := filepath.Join(f.root, "Glass Harbor", "Signal Fires (2019)", "01 - Low Tide.flac")
	if err := os.Rename(low, low+".away"); err != nil {
		t.Fatal(err)
	}
	f.scan(t, Options{Trigger: "schedule"})
	if n := f.n(t, `SELECT COUNT(*) FROM music_albums WHERE title = 'Signal Fires' AND missing_since IS NOT NULL`); n != 1 {
		t.Fatal("an album with no files left is not marked missing")
	}
	if n := f.n(t, `SELECT COUNT(*) FROM music_tracks WHERE rel_path LIKE 'Glass Harbor/Signal Fires (2019)/%'`); n != 2 {
		t.Fatalf("%d track rows left, want both kept", n)
	}

	// Back again: missing cleared, no new rows.
	for _, p := range []string{gone, low} {
		if err := os.Rename(p+".away", p); err != nil {
			t.Fatal(err)
		}
	}
	run = f.scan(t, Options{Trigger: "schedule"})
	if run.Added != 0 {
		t.Errorf("returning files were added as new rows: %+v", run)
	}
	if n := f.n(t, `SELECT COUNT(*) FROM music_tracks WHERE missing_since IS NOT NULL`) + f.n(t, `SELECT COUNT(*) FROM music_albums WHERE missing_since IS NOT NULL`); n != 0 {
		t.Errorf("%d rows still missing after the files came back", n)
	}

	// A changed file is probed again.
	before := f.pr.total()
	if err := os.Chtimes(low, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	run = f.scan(t, Options{Trigger: "schedule"})
	if f.pr.total() != before+1 || run.Changed != 1 {
		t.Errorf("a touched file: %d probes, run %+v", f.pr.total()-before, run)
	}

	// An emptied root (an unmounted share) aborts and changes nothing.
	tracks := f.n(t, `SELECT COUNT(*) FROM music_tracks WHERE missing_since IS NULL`)
	empty := t.TempDir()
	lib := f.lib
	lib.RootPath = empty
	if _, err := f.conn.Exec(`UPDATE libraries SET root_path = ? WHERE id = ?`, empty, lib.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sc.Scan(context.Background(), lib, Options{Trigger: "schedule"}); err == nil || !strings.Contains(err.Error(), "no audio files") {
		t.Fatalf("an empty root scanned: %v", err)
	}
	if n := f.n(t, `SELECT COUNT(*) FROM music_tracks WHERE missing_since IS NULL`); n != tracks {
		t.Errorf("an aborted scan changed %d tracks", tracks-n)
	}
}

// After the retention period a missing track goes for good, and an album or
// artist left with nothing goes with it.
func TestMusicScanRetention(t *testing.T) {
	f := newMusicFixture(t)
	f.scan(t, Options{Trigger: "cli"})
	dir := filepath.Join(f.root, "Cedar Static")
	if err := os.Rename(dir, dir+" away"); err != nil {
		t.Fatal(err)
	}
	f.scan(t, Options{Trigger: "cli"})
	old := time.Now().Add(-MissingRetention - time.Hour).UnixMilli()
	if _, err := f.conn.Exec(`UPDATE music_tracks SET missing_since = ? WHERE missing_since IS NOT NULL`, old); err != nil {
		t.Fatal(err)
	}
	// The folder is still away under another name, which the scan indexes anew.
	f.scan(t, Options{Trigger: "cli"})
	if n := f.n(t, `SELECT COUNT(*) FROM music_tracks WHERE rel_path LIKE 'Cedar Static/%'`); n != 0 {
		t.Errorf("%d expired tracks kept", n)
	}
	if n := f.n(t, `SELECT COUNT(*) FROM music_tracks WHERE rel_path LIKE 'Cedar Static away/%'`); n != 1 {
		t.Errorf("the renamed folder was not indexed")
	}
}
