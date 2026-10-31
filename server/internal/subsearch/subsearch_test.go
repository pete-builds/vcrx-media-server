package subsearch

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/opensubtitles"
	"github.com/pete-builds/vcrx-media-server/server/internal/opensubtitles/ostest"
)

type env struct {
	t     *testing.T
	db    *sql.DB
	tmp   string
	media string
	fake  *ostest.Server
	svc   *Service
	lib   int64
	now   time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	tmp := t.TempDir()
	conn, err := db.Open(filepath.Join(tmp, "config", "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if _, err := db.Migrate(context.Background(), conn, "", quiet); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, db: conn, tmp: tmp, media: filepath.Join(tmp, "media"), fake: ostest.New(t),
		now: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)}
	if err := os.MkdirAll(e.media, 0o755); err != nil {
		t.Fatal(err)
	}
	e.lib = e.exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('Movies', 'movie', ?, 0, 0)`, e.media)
	e.svc = New(conn, e.fake.Client("viewer", "secret"), filepath.Join(tmp, "config", "subtitles"), []string{"en"}, quiet)
	e.svc.Now = func() time.Time { return e.now }
	return e
}

func (e *env) exec(q string, args ...any) int64 {
	e.t.Helper()
	res, err := e.db.Exec(q, args...)
	if err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return n
}

// movie adds a movie with one version. size >= 128 KiB gives it a hash.
func (e *env) movie(title, imdb string, size int) (item, version int64) {
	e.t.Helper()
	dir := filepath.Join(e.media, title)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	p := filepath.Join(dir, title+".mkv")
	if err := os.WriteFile(p, make([]byte, size), 0o644); err != nil {
		e.t.Fatal(err)
	}
	var id any
	if imdb != "" {
		id = imdb
	}
	item = e.exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, year, imdb_id, added_at, updated_at)
		VALUES (?, 'movie', ?, ?, ?, 2001, ?, 0, 0)`, e.lib, title, title, title, id)
	version = e.exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, probe_status, added_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, 'ok', 0, 0)`, item, e.lib, p, title+"/"+title+".mkv", size)
	return item, version
}

func (e *env) queries() []url.Values {
	var out []url.Values
	for _, r := range e.fake.Requests() {
		if r.Path == "/api/v1/subtitles" {
			q, _ := url.ParseQuery(r.RawQuery)
			out = append(out, q)
		}
	}
	return out
}

// files lists every regular file under root, relative to it.
func files(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func ids(hits []Hit) []int64 {
	out := make([]int64, len(hits))
	for i, h := range hits {
		out[i] = h.FileID
	}
	return out
}

func TestSearchLadderAndRanking(t *testing.T) {
	e := newEnv(t)
	_, v := e.movie("The Film", "tt1234567", 200_000)
	hits, err := e.svc.Search(context.Background(), v, []string{"eng"}, false)
	if err != nil {
		t.Fatal(err)
	}
	qs := e.queries()
	if len(qs) != 2 || qs[0].Get("moviehash") == "" || qs[1].Get("imdb_id") != "1234567" || qs[1].Get("languages") != "en" {
		t.Fatalf("searches %v: want the hash, then the IMDb id, and no title search", qs)
	}
	// Hash match; trusted (admin upload, then trusted SDH, since SDH is not
	// preferred); then the most downloaded untrusted one.
	want := []int64{8000001, 8000004, 8000003, 8000002}
	if got := ids(hits); !equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	if !hits[0].HashMatch || hits[0].MatchedBy != "hash" || hits[0].Language == nil || *hits[0].Language != "eng" {
		t.Fatalf("first hit %+v", hits[0])
	}
	// Preferring hearing-impaired swaps the trusted pair.
	hits, _ = e.svc.Search(context.Background(), v, []string{"en"}, true)
	if got := ids(hits); !equal(got, []int64{8000001, 8000003, 8000004, 8000002}) {
		t.Fatalf("hearing-impaired order %v", got)
	}
	// The same search again within ten minutes costs no requests.
	before := len(e.fake.Requests())
	if _, err := e.svc.Search(context.Background(), v, []string{"eng"}, false); err != nil || len(e.fake.Requests()) != before {
		t.Fatalf("repeat search: %v, %d new requests", err, len(e.fake.Requests())-before)
	}
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTitleSearchOnlyWhenIdsFindNothing(t *testing.T) {
	e := newEnv(t)
	e.fake.NoIDResults = true
	_, v := e.movie("The Film", "tt1234567", 1000) // too small to hash
	hits, err := e.svc.Search(context.Background(), v, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	qs := e.queries()
	if len(qs) != 2 || qs[0].Get("imdb_id") == "" || qs[1].Get("query") != "the film" || qs[1].Get("year") != "2001" {
		t.Fatalf("searches %v", qs)
	}
	if len(hits) != 1 || hits[0].MatchedBy != "title" || hits[0].HashMatch {
		t.Fatalf("hits %+v", hits)
	}

	// An episode searches by the show's id with season and episode.
	show := e.exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, imdb_id, added_at, updated_at)
		VALUES (?, 'show', 'harbor', 'Harbor Watch', 'Harbor Watch', 'tt0070000', 0, 0)`, e.lib)
	season := e.exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, added_at, updated_at)
		VALUES (?, 'season', ?, '2', 'Season 2', 'Season 2', 2, 0, 0)`, e.lib, show)
	ep := e.exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, episode_number, added_at, updated_at)
		VALUES (?, 'episode', ?, '5', 'Low Tide', 'Low Tide', 2, 5, 0, 0)`, e.lib, season)
	epv := e.exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, probe_status, added_at, updated_at)
		VALUES (?, ?, ?, 'x', 1, 0, 'ok', 0, 0)`, ep, e.lib, filepath.Join(e.media, "missing.mkv"))
	if _, err := e.svc.Search(context.Background(), epv, []string{"de"}, false); err != nil {
		t.Fatal(err)
	}
	qs = e.queries()[2:]
	if len(qs) != 2 {
		t.Fatalf("episode searches %v", qs)
	}
	id, title := qs[0], qs[1]
	if id.Get("parent_imdb_id") != "70000" || id.Get("season_number") != "2" || id.Get("episode_number") != "5" ||
		id.Get("type") != "episode" || id.Get("languages") != "de" {
		t.Fatalf("episode id search %v", id)
	}
	if title.Get("query") != "harbor watch" || title.Get("season_number") != "2" {
		t.Fatalf("episode title search %v", title)
	}
}

func TestDownloadLandsOnlyUnderTheDataDir(t *testing.T) {
	e := newEnv(t)
	_, v := e.movie("The Film", "tt1234567", 200_000)
	mediaBefore := files(t, e.media)
	if _, err := e.svc.Download(context.Background(), v, 8000003, 0); !errors.Is(err, ErrNotSearched) {
		t.Fatalf("download before a search: %v", err)
	}
	if _, err := e.svc.Search(context.Background(), v, []string{"en"}, false); err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.Download(context.Background(), v, 8000003, 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Existing || d.Codec != "subrip" || d.Language == nil || *d.Language != "eng" || !d.HearingImpaired {
		t.Fatalf("downloaded %+v", d)
	}
	var rel string
	if err := e.db.QueryRow(`SELECT external_path FROM media_streams WHERE id = ? AND is_external = 1 AND downloaded_id IS NOT NULL`,
		d.StreamID).Scan(&rel); err != nil {
		t.Fatal(err)
	}
	if got := files(t, e.svc.Dir); len(got) != 1 || got[0] != rel || !strings.HasPrefix(rel, strconv.FormatInt(v, 10)+string(filepath.Separator)) {
		t.Fatalf("data dir holds %v; track points at %q", got, rel)
	}
	b, _ := os.ReadFile(filepath.Join(e.svc.Dir, rel))
	if string(b) != ostest.Content {
		t.Fatalf("stored %q", b)
	}
	if got := files(t, e.media); !equalStrings(got, mediaBefore) {
		t.Fatalf("the media folder changed: %v -> %v", mediaBefore, got)
	}
	// Asking again for the same file spends nothing.
	again, err := e.svc.Download(context.Background(), v, 8000003, 0)
	if err != nil || !again.Existing || again.StreamID != d.StreamID || e.fake.Count("/api/v1/download") != 1 {
		t.Fatalf("second download %+v, %v, %d provider downloads", again, err, e.fake.Count("/api/v1/download"))
	}
	hits, _ := e.svc.Search(context.Background(), v, []string{"en"}, false)
	for _, h := range hits {
		if (h.FileID == 8000003) != (h.StreamID != nil) {
			t.Fatalf("hit %d stream %v", h.FileID, h.StreamID)
		}
	}

	if err := e.svc.Delete(context.Background(), d.StreamID); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT COUNT(*) FROM media_streams WHERE downloaded_id IS NOT NULL`) + e.count(`SELECT COUNT(*) FROM downloaded_subtitles`); n != 0 {
		t.Fatalf("%d rows left after delete", n)
	}
	if got := files(t, e.svc.Dir); len(got) != 0 {
		t.Fatalf("files left after delete: %v", got)
	}
	if err := e.svc.Delete(context.Background(), d.StreamID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A file name from the API that tries to climb out of the data dir is
// refused, and nothing is written anywhere.
func TestCraftedFileNameIsRefused(t *testing.T) {
	e := newEnv(t)
	_, v := e.movie("The Film", "tt1234567", 200_000)
	if _, err := e.svc.Search(context.Background(), v, []string{"en"}, false); err != nil {
		t.Fatal(err)
	}
	before := files(t, e.tmp)
	for _, name := range []string{"../../../outside/evil.srt", `..\..\evil.srt`, "/etc/cron.d/evil.srt", ".evil.srt", "evil.exe"} {
		e.fake.DownloadName = name
		_, err := e.svc.Download(context.Background(), v, 8000002, 0)
		if !errors.Is(err, ErrBadFile) {
			t.Fatalf("name %q: err %v, want ErrBadFile", name, err)
		}
	}
	if got := files(t, e.tmp); !equalStrings(got, before) {
		t.Fatalf("files written: before %v, after %v", before, got)
	}
	if n := e.count(`SELECT COUNT(*) FROM downloaded_subtitles`) + e.count(`SELECT COUNT(*) FROM media_streams`); n != 0 {
		t.Fatalf("%d rows written for refused files", n)
	}
	for name, ok := range map[string]bool{"Film.EN.SRT": true, "a.ass": true, "a.ssa": true, "a.vtt": true, "": true,
		"a/b.srt": false, "a.sub": false, "a\x00.srt": false} {
		if _, err := fileExt(name); (err == nil) != ok {
			t.Errorf("fileExt(%q): %v", name, err)
		}
	}
}

func TestQuotaExhaustedStopsDownloadsAndTheAutoPass(t *testing.T) {
	e := newEnv(t)
	e.fake.Remaining = 0
	_, v := e.movie("The Film", "tt1234567", 200_000)
	if _, err := e.svc.Search(context.Background(), v, []string{"en"}, false); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Download(context.Background(), v, 8000002, 0)
	var qe *opensubtitles.QuotaError
	if !errors.As(err, &qe) {
		t.Fatalf("err %v, want a QuotaError", err)
	}
	if n := e.count(`SELECT COUNT(*) FROM downloaded_subtitles`); n != 0 {
		t.Fatalf("%d rows for a refused download", n)
	}

	// The automatic fetch stops without counting an attempt, and while the
	// quota is known to be used up it does not ask at all.
	if err := e.svc.InitAuto(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, nv := e.movie("New Film", "tt7654321", 200_000)
	e.svc.offered = map[offerKey]offer{}
	n, err := e.svc.AutoPass(context.Background())
	if !errors.As(err, &qe) || n != 0 {
		t.Fatalf("auto pass: %d, %v", n, err)
	}
	if c := e.count(`SELECT COUNT(*) FROM subtitle_fetch_attempts`); c != 0 {
		t.Fatalf("%d attempts recorded for a quota stop", c)
	}
	requests := len(e.fake.Requests())
	if _, err := e.svc.AutoPass(context.Background()); !errors.As(err, &qe) || len(e.fake.Requests()) != requests {
		t.Fatalf("second pass: %v, %d requests", err, len(e.fake.Requests())-requests)
	}
	_ = nv
}

func TestAutoFetchOncePerNewVersionWithBackoff(t *testing.T) {
	e := newEnv(t)
	e.fake.NoIDResults = true
	_, old := e.movie("Old Film", "tt1000001", 200_000)
	if err := e.svc.InitAuto(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, fresh := e.movie("Fresh Film", "tt1000002", 200_000)
	_, hasEng := e.movie("Subbed Film", "tt1000003", 200_000)
	e.exec(`INSERT INTO media_streams (version_id, kind, codec, language, is_external, external_path) VALUES (?, 'subtitle', 'subrip', 'eng', 1, 'x.srt')`, hasEng)
	_, forcedOnly := e.movie("Forced Film", "tt1000004", 200_000)
	e.exec(`INSERT INTO media_streams (version_id, kind, codec, language, is_forced, stream_index) VALUES (?, 'subtitle', 'subrip', 'eng', 1, 2)`, forcedOnly)
	_, noHash := e.movie("Small Film", "tt1000005", 1000) // no hash, and ids find nothing

	n, err := e.svc.AutoPass(context.Background())
	if err != nil || n != 2 {
		t.Fatalf("first pass: %d, %v; want 2 fetched", n, err)
	}
	got := map[int64]bool{}
	for _, id := range []int64{old, fresh, hasEng, forcedOnly, noHash} {
		got[id] = e.count(`SELECT COUNT(*) FROM downloaded_subtitles WHERE version_id = ?`, id) > 0
	}
	if !got[fresh] || !got[forcedOnly] || got[old] || got[hasEng] || got[noHash] {
		t.Fatalf("downloaded for %v (fresh %d, forced-only %d; not old %d, subbed %d, small %d)", got, fresh, forcedOnly, old, hasEng, noHash)
	}
	// Strict: a title search is never accepted unseen.
	for _, q := range e.queries() {
		if q.Get("query") != "" {
			t.Fatalf("automatic fetch ran a title search: %v", q)
		}
	}

	attempt := func() (int, bool, string) {
		var a int
		var done bool
		var res string
		if err := e.db.QueryRow(`SELECT attempts, done, last_result FROM subtitle_fetch_attempts WHERE version_id = ? AND language = 'eng'`,
			noHash).Scan(&a, &done, &res); err != nil {
			t.Fatal(err)
		}
		return a, done, res
	}
	if a, done, res := attempt(); a != 1 || done || res != "none" {
		t.Fatalf("after one try: %d %v %s", a, done, res)
	}
	// Nothing is due yet: a second pass asks nothing.
	requests := len(e.fake.Requests())
	if n, err := e.svc.AutoPass(context.Background()); err != nil || n != 0 || len(e.fake.Requests()) != requests {
		t.Fatalf("second pass: %d, %v, %d requests", n, err, len(e.fake.Requests())-requests)
	}
	// Each retry waits longer; after four tries it gives up for good.
	for _, wait := range []time.Duration{6 * time.Hour, 24 * time.Hour, 96 * time.Hour} {
		e.now = e.now.Add(wait - time.Minute)
		if _, err := e.svc.AutoPass(context.Background()); err != nil {
			t.Fatal(err)
		}
		before, _, _ := attempt()
		e.now = e.now.Add(2 * time.Minute)
		if _, err := e.svc.AutoPass(context.Background()); err != nil {
			t.Fatal(err)
		}
		if after, _, _ := attempt(); after != before+1 {
			t.Fatalf("after waiting %s: attempts %d -> %d", wait, before, after)
		}
	}
	if a, done, res := attempt(); a != MaxAutoAttempts || !done || res != "gave_up" {
		t.Fatalf("final: %d %v %s", a, done, res)
	}
	e.now = e.now.Add(365 * 24 * time.Hour)
	requests = len(e.fake.Requests())
	if _, err := e.svc.AutoPass(context.Background()); err != nil || len(e.fake.Requests()) != requests {
		t.Fatalf("a year later: %v, %d requests", err, len(e.fake.Requests())-requests)
	}
	// A downloaded subtitle an admin deleted is not fetched again.
	var sid int64
	_ = e.db.QueryRow(`SELECT s.id FROM media_streams s JOIN downloaded_subtitles d ON d.id = s.downloaded_id WHERE d.version_id = ?`, fresh).Scan(&sid)
	if err := e.svc.Delete(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.AutoPass(context.Background()); err != nil || n != 0 {
		t.Fatalf("after delete: %d, %v", n, err)
	}
}

// Offline, the pass stops at the first failure and records nothing, so the
// versions are tried in full once the network is back.
func TestAutoPassOffline(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.InitAuto(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.movie("Fresh Film", "tt1000002", 200_000)
	e.fake.Close()
	if _, err := e.svc.AutoPass(context.Background()); !errors.Is(err, opensubtitles.ErrUnavailable) {
		t.Fatalf("err %v", err)
	}
	if n := e.count(`SELECT COUNT(*) FROM subtitle_fetch_attempts`); n != 0 {
		t.Fatalf("%d attempts recorded while offline", n)
	}
}

// A download whose link fails has already spent quota. The pass records the
// attempt (with backoff) so the next passes do not spend again; a 4xx moves
// on to the next file, a 5xx stops the pass.
func TestAutoPassRecordsFailedLinks(t *testing.T) {
	for _, c := range []struct {
		status   int
		stops    bool
		attempts int
	}{{410, false, 2}, {503, true, 1}} {
		e := newEnv(t)
		e.fake.LinkStatus = c.status
		if err := e.svc.InitAuto(context.Background()); err != nil {
			t.Fatal(err)
		}
		e.movie("Fresh Film", "tt1000002", 200_000)
		e.movie("Second Film", "tt1000003", 200_000)
		_, err := e.svc.AutoPass(context.Background())
		if (err != nil) != c.stops {
			t.Fatalf("link %d: pass err %v", c.status, err)
		}
		spent := e.fake.Count("/api/v1/download")
		if n := e.count(`SELECT COUNT(*) FROM subtitle_fetch_attempts WHERE last_result = 'link_failed' AND done = 0 AND next_attempt_at > ?`,
			e.now.UnixMilli()); n != c.attempts || spent != c.attempts {
			t.Fatalf("link %d: %d attempts recorded, %d downloads spent; want %d", c.status, n, spent, c.attempts)
		}
		// Many more passes within the backoff spend at most one download per
		// file in all (the 5xx pass stopped before the second file).
		for range 6 {
			e.now = e.now.Add(30 * time.Minute)
			_, _ = e.svc.AutoPass(context.Background())
		}
		if got, tries := e.fake.Count("/api/v1/download"), e.count(`SELECT COUNT(*) FROM subtitle_fetch_attempts WHERE attempts = 1`); got != 2 || tries != 2 {
			t.Fatalf("link %d: %d downloads spent on 2 files (first pass %d), %d single attempts", c.status, got, spent, tries)
		}
	}
}
