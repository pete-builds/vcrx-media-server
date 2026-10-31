package newsletter

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/notify"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// A fixed zone with no daylight saving keeps the slot arithmetic exact.
var zone = time.FixedZone("Test", -5*3600)

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := db.Migrate(context.Background(), conn, "", quiet); err != nil {
		t.Fatal(err)
	}
	return conn
}

type library struct {
	t      *testing.T
	conn   *sql.DB
	movies int64
	tv     int64
	friend int64
}

// newLibrary makes a Movies and a TV library and one friend account granted
// both, so the default audience (every friend) sees everything in them.
func newLibrary(t *testing.T, conn *sql.DB) *library {
	l := &library{t: t, conn: conn}
	l.movies = l.exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('Movies', 'movie', '/media/m', 0, 0)`)
	l.tv = l.exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('TV', 'show', '/media/t', 0, 0)`)
	l.friend = l.user("friend", false, l.movies, l.tv)
	return l
}

func (l *library) user(name string, admin bool, libs ...int64) int64 {
	id := l.exec(`INSERT INTO users (username, password_hash, is_admin, created_at, updated_at) VALUES (?, 'x', ?, 0, 0)`, name, admin)
	for _, lib := range libs {
		l.exec(`INSERT INTO library_grants (user_id, library_id) VALUES (?, ?)`, id, lib)
	}
	return id
}

// version gives an item a file on disk; policy shows only playable titles.
func (l *library) version(item, lib int64) {
	l.exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, resolution, added_at, updated_at)
		VALUES (?, ?, ?, 'f.mkv', 1, 0, '1080p', 0, 0)`, item, lib, fmt.Sprintf("/media/%d.mkv", item))
}

func (l *library) label(item int64, name string) {
	l.exec(`INSERT OR IGNORE INTO labels (name) VALUES (?)`, name)
	l.exec(`INSERT INTO item_labels (item_id, label_id) VALUES (?, (SELECT id FROM labels WHERE name = ?))`, item, name)
}

func (l *library) exec(q string, args ...any) int64 {
	l.t.Helper()
	res, err := l.conn.Exec(q, args...)
	if err != nil {
		l.t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (l *library) movie(title string, year int, added time.Time) int64 {
	return l.movieIn(l.movies, title, year, added)
}

func (l *library) movieIn(lib int64, title string, year int, added time.Time) int64 {
	id := l.exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, year, added_at, updated_at)
		VALUES (?, 'movie', ?, ?, ?, ?, ?, 0)`, lib, title, title, title, year, added.UnixMilli())
	l.version(id, lib)
	return id
}

func (l *library) show(title string) (show, season int64) {
	show = l.exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (?, 'show', ?, ?, ?, 0, 0)`,
		l.tv, title, title, title)
	season = l.exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, added_at, updated_at)
		VALUES (?, 'season', ?, '1', 'Season 1', 'Season 1', 1, 0, 0)`, l.tv, show)
	return show, season
}

func (l *library) episode(season int64, n int, title string, added time.Time) int64 {
	id := l.exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, season_number, episode_number, added_at, updated_at)
		VALUES (?, 'episode', ?, ?, ?, ?, 1, ?, ?, 0)`, l.tv, season, n, title, title, n, added.UnixMilli())
	l.version(id, l.tv)
	return id
}

func (l *library) poster(item int64, source, url string) {
	l.exec(`INSERT INTO artwork (item_id, kind, source, url, updated_at) VALUES (?, 'poster', ?, ?, 0)`, item, source, url)
}

func TestBuildGroupsEpisodesByShow(t *testing.T) {
	conn := openDB(t, filepath.Join(t.TempDir(), "vcrx.db"))
	l := newLibrary(t, conn)
	until := time.Date(2026, 3, 13, 17, 0, 0, 0, zone)
	since := until.Add(-Window)
	in := func(h int) time.Time { return since.Add(time.Duration(h) * time.Hour) }

	harbor := l.movie("Harbor Lights", 2001, in(10))
	l.poster(harbor, "tmdb", "/harbor.jpg")
	paper := l.movie("Paper Trail", 1998, in(50))
	l.poster(paper, "local", "") // only on disk: no link a chat service could load
	l.movie("Too Old", 1990, since.Add(-time.Minute))
	l.movie("Too New", 1990, until)
	gone := l.movie("Gone Missing", 2005, in(5))
	l.exec(`UPDATE items SET missing_since = 1 WHERE id = ?`, gone)

	street, s1 := l.show("Quiet Street")
	l.poster(street, "tmdb", "/quiet.jpg")
	l.episode(s1, 3, "Episode 3", in(40))
	l.episode(s1, 1, "Pilot", in(20))
	l.episode(s1, 2, "Second", in(30))
	copper, s2 := l.show("Copper Valley")
	l.episode(s2, 7, "Late One", in(100))
	_, s3 := l.show("Old Show")
	l.episode(s3, 1, "Before", since.Add(-time.Hour))

	// "1": no audience rule, so this test is about contents and grouping only.
	d, err := Build(context.Background(), conn, since, until, "1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Movies) != 2 || d.Movies[0].Title != "Paper Trail" || d.Movies[1].Title != "Harbor Lights" ||
		d.Movies[1].PosterURL != "https://image.tmdb.org/t/p/w342/harbor.jpg" || d.Movies[0].PosterURL != "" || d.Movies[1].Year != 2001 {
		t.Fatalf("movies: %+v", d.Movies)
	}
	// Shows newest first; each show's episodes in order, the bare "Episode 3" title dropped.
	if len(d.Shows) != 2 || d.Shows[0].ID != copper || d.Shows[1].Title != "Quiet Street" ||
		d.Shows[1].PosterURL != "https://image.tmdb.org/t/p/w342/quiet.jpg" || d.Episodes() != 4 {
		t.Fatalf("shows: %+v", d.Shows)
	}
	eps := d.Shows[1].Episodes
	if eps[0].Title != "Pilot" || eps[1].Episode != 2 || eps[2].Episode != 3 || eps[2].Title != "" || eps[2].Code() != "S01E03" {
		t.Fatalf("episodes: %+v", eps)
	}

	empty, err := Build(context.Background(), conn, until.Add(time.Hour), until.Add(2*time.Hour), "1", nil)
	if err != nil || !empty.Empty() {
		t.Fatalf("empty window: %+v %v", empty, err)
	}
}

func TestLastSlot(t *testing.T) {
	fri := Settings{Weekday: int(time.Friday), Hour: 17}
	cases := []struct{ now, want time.Time }{
		// Friday 13 March 2026 at 17:00 exactly is that week's slot.
		{time.Date(2026, 3, 13, 17, 0, 0, 0, zone), time.Date(2026, 3, 13, 17, 0, 0, 0, zone)},
		{time.Date(2026, 3, 13, 16, 59, 0, 0, zone), time.Date(2026, 3, 6, 17, 0, 0, 0, zone)},
		{time.Date(2026, 3, 16, 9, 0, 0, 0, zone), time.Date(2026, 3, 13, 17, 0, 0, 0, zone)},
	}
	for _, c := range cases {
		if got := LastSlot(c.now, fri, zone); !got.Equal(c.want) {
			t.Errorf("LastSlot(%v) = %v, want %v", c.now, got, c.want)
		}
	}
}

type receiver struct {
	srv  *httptest.Server
	mu   sync.Mutex
	got  []map[string]any
	fail bool
}

func newReceiver(t *testing.T) *receiver {
	r := &receiver{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.fail {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var m map[string]any
		_ = json.NewDecoder(req.Body).Decode(&m)
		r.got = append(r.got, m)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

func TestTickSendsOnceAWeek(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vcrx.db")
	conn := openDB(t, path)
	l := newLibrary(t, conn)
	recv := newReceiver(t)
	hooks := notify.New([]string{recv.srv.URL}, nil, "VCRX", quiet)
	clock := time.Date(2026, 3, 11, 12, 0, 0, 0, zone) // a Wednesday
	svc := func() *Service {
		return &Service{DB: conn, Hooks: hooks, Log: quiet, Location: zone, Now: func() time.Time { return clock }}
	}
	ctx := context.Background()
	s := svc()

	// Off by default: nothing is sent even when something was added. One film
	// came last week, one this week.
	l.movie("Night Garden", 2020, time.Date(2026, 3, 4, 12, 0, 0, 0, zone))
	l.movie("Harbor Lights", 2001, clock.Add(-time.Hour))
	if sent, err := s.Tick(ctx); sent || err != nil {
		t.Fatalf("sent while off: %v %v", sent, err)
	}

	// Switching it on for Fridays at 17:00 does not send last Friday's digest now.
	if err := s.Save(ctx, Settings{Enabled: true, Weekday: int(time.Friday), Hour: 17}); err != nil {
		t.Fatal(err)
	}
	if sent, _ := s.Tick(ctx); sent || recv.count() != 0 {
		t.Fatal("switching on sent a past week")
	}

	// Friday 17:00: the week's digest goes out once.
	clock = time.Date(2026, 3, 13, 17, 0, 30, 0, zone)
	if sent, err := s.Tick(ctx); !sent || err != nil || recv.count() != 1 {
		t.Fatalf("due: sent=%v err=%v posts=%d", sent, err, recv.count())
	}
	if got := recv.got[0]; got["event"] != "newsletter" || len(got["movies"].([]any)) != 1 || got["preview"] != false {
		t.Fatalf("payload: %v", got)
	}
	if sent, _ := s.Tick(ctx); sent || recv.count() != 1 {
		t.Fatal("sent twice in one week")
	}

	// A restart: a new service on the same database, an hour later. The week
	// is remembered, so nothing is sent again.
	conn2 := openDB(t, path)
	clock = clock.Add(time.Hour)
	restarted := &Service{DB: conn2, Hooks: hooks, Log: quiet, Location: zone, Now: func() time.Time { return clock }}
	if sent, err := restarted.Tick(ctx); sent || err != nil || recv.count() != 1 {
		t.Fatalf("resent after a restart: sent=%v err=%v posts=%d", sent, err, recv.count())
	}

	// The next Friday nothing was added: nothing is sent, and the week is still
	// marked as handled so it is not retried every minute.
	clock = time.Date(2026, 3, 20, 17, 1, 0, 0, zone)
	if sent, err := restarted.Tick(ctx); sent || err != nil || recv.count() != 1 {
		t.Fatalf("empty week: sent=%v err=%v posts=%d", sent, err, recv.count())
	}
	last, _ := restarted.LastSent(ctx)
	if last != time.Date(2026, 3, 20, 17, 0, 0, 0, zone).UnixMilli() {
		t.Fatalf("empty week not marked handled: %d", last)
	}

	// A week where the webhook is down: the claim is undone and the next tick
	// sends once it is back.
	l.movie("Copper Valley", 2015, time.Date(2026, 3, 25, 12, 0, 0, 0, zone))
	clock = time.Date(2026, 3, 27, 17, 0, 0, 0, zone)
	recv.mu.Lock()
	recv.fail = true
	recv.mu.Unlock()
	if sent, err := restarted.Tick(ctx); sent || err == nil {
		t.Fatalf("failed send reported as sent: %v %v", sent, err)
	}
	recv.mu.Lock()
	recv.fail = false
	recv.mu.Unlock()
	clock = clock.Add(time.Minute)
	if sent, err := restarted.Tick(ctx); !sent || err != nil || recv.count() != 2 {
		t.Fatalf("retry after failure: sent=%v err=%v posts=%d", sent, err, recv.count())
	}
}

func TestPreview(t *testing.T) {
	conn := openDB(t, filepath.Join(t.TempDir(), "vcrx.db"))
	l := newLibrary(t, conn)
	recv := newReceiver(t)
	clock := time.Date(2026, 3, 11, 12, 0, 0, 0, zone)
	s := &Service{DB: conn, Hooks: notify.New([]string{recv.srv.URL}, nil, "VCRX", quiet), Log: quiet, Location: zone,
		Now: func() time.Time { return clock }}
	ctx := context.Background()
	if _, _, err := s.Preview(ctx); !errors.Is(err, ErrNothingAdded) || recv.count() != 0 {
		t.Fatalf("empty preview: %v, %d posts", err, recv.count())
	}
	l.movie("Harbor Lights", 2001, clock.Add(-48*time.Hour))
	d, n, err := s.Preview(ctx)
	if err != nil || n != 1 || len(d.Movies) != 1 || recv.got[0]["preview"] != true {
		t.Fatalf("preview: %+v %d %v", d, n, err)
	}
	// A preview, sent while the newsletter is off, is not this week's send.
	if last, _ := s.LastSent(ctx); last != 0 {
		t.Fatalf("preview recorded as sent: %d", last)
	}
}

func TestSettingsValidateAndStatus(t *testing.T) {
	conn := openDB(t, filepath.Join(t.TempDir(), "vcrx.db"))
	clock := time.Date(2026, 3, 11, 12, 0, 0, 0, zone)
	s := &Service{DB: conn, Location: zone, Now: func() time.Time { return clock }}
	ctx := context.Background()
	for _, bad := range []Settings{{Weekday: 7}, {Weekday: -1}, {Hour: 24}, {Hour: -1}} {
		if s.Save(ctx, bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	st, err := s.Status(ctx)
	if err != nil || st.Enabled || st.Weekday != int(time.Friday) || st.Hour != 17 || st.NextSend != nil || st.TimeZone != "Test" {
		t.Fatalf("default status: %+v %v", st, err)
	}
	if err := s.Save(ctx, Settings{Enabled: true, Weekday: int(time.Monday), Hour: 8}); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Status(ctx)
	if want := time.Date(2026, 3, 16, 8, 0, 0, 0, zone).UnixMilli(); st.NextSend == nil || *st.NextSend != want {
		t.Fatalf("next send: %v, want %d", st.NextSend, want)
	}
}

// titles lists a digest's movies and shows by name.
func titles(d notify.Digest) map[string]bool {
	out := map[string]bool{}
	for _, m := range d.Movies {
		out[m.Title] = true
	}
	for _, s := range d.Shows {
		out[s.Title] = true
	}
	return out
}

// The digest goes to friends, so it lists only what they may see: never a
// title from a library they are not granted, never a title (or a show's
// episode) carrying a label one of them is denied.
func TestAudiencePolicy(t *testing.T) {
	conn := openDB(t, filepath.Join(t.TempDir(), "vcrx.db"))
	l := newLibrary(t, conn)
	ctx := context.Background()
	clock := time.Date(2026, 3, 11, 12, 0, 0, 0, zone)
	recv := newReceiver(t)
	s := &Service{DB: conn, Hooks: notify.New([]string{recv.srv.URL}, nil, "VCRX", quiet), Log: quiet, Location: zone,
		Now: func() time.Time { return clock }}
	private := l.exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('Private', 'movie', '/media/p', 0, 0)`)
	// friend (from newLibrary) has Movies and TV and is denied "late night";
	// cousin also has Private and no label rules.
	l.exec(`INSERT INTO user_policies (user_id, deny_labels, updated_at) VALUES (?, '["late night"]', 0)`, l.friend)
	cousin := l.user("cousin", false, l.movies, l.tv, private)
	admin := l.user("boss", true)

	added := clock.Add(-24 * time.Hour)
	l.movie("Harbor Lights", 2001, added)
	night := l.movie("Night Garden", 2020, added)
	l.label(night, "late night")
	l.movieIn(private, "Locked Room", 2010, added)
	_, s1 := l.show("Quiet Street")
	l.episode(s1, 1, "Pilot", added)
	dark, s2 := l.show("Dark Hours")
	l.label(dark, "late night") // on the show: its episodes inherit it
	l.episode(s2, 1, "First", added)
	everything := map[string]bool{"Harbor Lights": true, "Night Garden": true, "Locked Room": true, "Quiet Street": true, "Dark Hours": true}
	safe := map[string]bool{"Harbor Lights": true, "Quiet Street": true}

	check := func(st Settings, want map[string]bool) {
		t.Helper()
		if err := s.Save(ctx, st); err != nil {
			t.Fatal(err)
		}
		d, _, err := s.Preview(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := titles(d)
		if len(got) != len(want) {
			t.Fatalf("audience %d: got %v, want %v", st.AudienceUserID, got, want)
		}
		for k := range want {
			if !got[k] {
				t.Fatalf("audience %d: got %v, want %v", st.AudienceUserID, got, want)
			}
		}
	}
	// Default: only what every friend can see. The friend's denied label and
	// the cousin's extra library both drop out.
	check(Settings{}, safe)
	check(Settings{AudienceUserID: l.friend}, safe)
	check(Settings{AudienceUserID: cousin}, everything)

	// The scheduled send uses the same rule.
	if err := s.Save(ctx, Settings{Enabled: true, Weekday: int(time.Friday), Hour: 17}); err != nil {
		t.Fatal(err)
	}
	clock = time.Date(2026, 3, 13, 17, 0, 0, 0, zone)
	if sent, err := s.Tick(ctx); !sent || err != nil {
		t.Fatalf("tick: %v %v", sent, err)
	}
	last := recv.got[len(recv.got)-1]
	var names []string
	for _, m := range last["movies"].([]any) {
		names = append(names, m.(map[string]any)["title"].(string))
	}
	for _, sh := range last["shows"].([]any) {
		names = append(names, sh.(map[string]any)["title"].(string))
	}
	if fmt.Sprint(names) != "[Harbor Lights Quiet Street]" {
		t.Fatalf("scheduled digest listed %v", names)
	}

	// An admin sees everything, so it cannot be the audience; nor can a
	// disabled or missing account.
	l.exec(`UPDATE users SET disabled = 1 WHERE id = ?`, cousin)
	for _, id := range []int64{admin, cousin, 9999} {
		if err := s.Save(ctx, Settings{AudienceUserID: id}); !errors.Is(err, ErrBadAudience) {
			t.Fatalf("audience %d accepted: %v", id, err)
		}
	}

	// With no friend accounts at all, nothing qualifies.
	l.exec(`UPDATE users SET disabled = 1 WHERE id = ?`, l.friend)
	if _, _, err := s.Preview(ctx); !errors.Is(err, ErrNothingAdded) {
		t.Fatalf("no friends: %v", err)
	}
}

// webhook_events applies to the newsletter like every other event.
func TestNewsletterRespectsWebhookEvents(t *testing.T) {
	conn := openDB(t, filepath.Join(t.TempDir(), "vcrx.db"))
	l := newLibrary(t, conn)
	recv := newReceiver(t)
	clock := time.Date(2026, 3, 11, 12, 0, 0, 0, zone)
	l.movie("Harbor Lights", 2001, clock.Add(-time.Hour))
	ctx := context.Background()
	s := &Service{DB: conn, Hooks: notify.New([]string{recv.srv.URL}, []string{notify.Play}, "VCRX", quiet), Log: quiet, Location: zone,
		Now: func() time.Time { return clock }}
	if err := s.Save(ctx, Settings{Enabled: true, Weekday: int(time.Friday), Hour: 17}); err != nil {
		t.Fatal(err)
	}
	clock = time.Date(2026, 3, 13, 17, 0, 0, 0, zone)
	if sent, _ := s.Tick(ctx); sent || recv.count() != 0 {
		t.Fatal("sent although webhook_events leaves out newsletter")
	}
	if _, n, err := s.Preview(ctx); n != 0 || err == nil || recv.count() != 0 {
		t.Fatalf("preview sent although webhook_events leaves out newsletter: %d %v", n, err)
	}
	if st, _ := s.Status(ctx); st.Webhooks != 0 {
		t.Fatalf("status counts %d webhooks for the newsletter", st.Webhooks)
	}
	s.Hooks = notify.New([]string{recv.srv.URL}, []string{notify.Play, notify.Newsletter}, "VCRX", quiet)
	if sent, err := s.Tick(ctx); !sent || err != nil || recv.count() != 1 {
		t.Fatalf("with newsletter in webhook_events: %v %v %d", sent, err, recv.count())
	}
}
