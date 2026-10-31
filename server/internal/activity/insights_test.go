package activity

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
)

func TestLocation(t *testing.T) {
	for ip, want := range map[string]string{
		"192.168.1.20":           "Local network",
		"10.0.0.5":               "Local network",
		"10.9.9.9":               "Local network",
		"fe80::1":                "Local network",
		"fd12:3456::1":           "Local network",
		"100.101.102.103":        "Tailscale",
		"fd7a:115c:a1e0::1234":   "Tailscale",
		"::ffff:100.101.102.103": "Tailscale",
		"127.0.0.1":              "This server",
		"::1":                    "This server",
		"203.0.113.9":            "Internet",
		"100.128.0.1":            "Internet", // just past the Tailscale range
		"2001:db8:85a3::8a2e:1":  "Internet",
		"":                       "",
		"not an address":         "",
	} {
		if got := Location(ip); got != want {
			t.Errorf("Location(%q) = %q, want %q", ip, got, want)
		}
	}
}

func TestPlatformName(t *testing.T) {
	for _, c := range [][3]string{
		{"android_tv", "Some TV", "Google TV"},
		{"web", "Chrome", "Web (Chrome)"},
		{"web", "", "Web"},
		{"", "", "Unknown"},
		{"ios", "", "ios"},
	} {
		if got := PlatformName(c[0], c[1]); got != c[2] {
			t.Errorf("PlatformName(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

// statsZone is fixed so hour and weekday buckets do not move with the machine.
var statsZone = time.FixedZone("Test", -5*3600)

type statsFixture struct {
	t     *testing.T
	conn  *sql.DB
	n     int
	users map[string]int64
}

func newStatsFixture(t *testing.T) *statsFixture {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := db.Migrate(context.Background(), conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	f := &statsFixture{t: t, conn: conn, users: map[string]int64{}}
	for _, u := range []string{"ana", "ben"} {
		f.users[u] = f.exec(`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES (?, 'x', 0, 0)`, u)
	}
	return f
}

func (f *statsFixture) exec(q string, args ...any) int64 {
	f.t.Helper()
	res, err := f.conn.Exec(q, args...)
	if err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
	id, _ := res.LastInsertId()
	return id
}

type play struct {
	user, kind, title string
	item              int64
	decision          string
	platform, model   string
	start             time.Time
	minutes           int
	playedMs          int64
	ip                string
}

func (f *statsFixture) play(p play) {
	f.t.Helper()
	f.n++
	end := p.start.Add(time.Duration(p.minutes) * time.Minute).UnixMilli()
	state := "stopped"
	if p.decision == "refused" {
		state = "refused"
	}
	f.exec(`INSERT INTO playback_sessions (id, user_id, item_id, username, item_title, item_kind, platform, device_model, device_name,
			decision, decision_reason, state, played_ms, ip, started_at, last_event_at, ended_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'Device', ?, 'test', ?, ?, ?, ?, ?, ?)`,
		fmt.Sprintf("s%d", f.n), f.users[p.user], p.item, p.user, p.title, p.kind, p.platform, p.model,
		p.decision, state, p.playedMs, p.ip, p.start.UnixMilli(), end, end)
}

func TestStatsFromSeededSessions(t *testing.T) {
	f := newStatsFixture(t)
	lib := f.exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('All', 'show', '/media', 0, 0)`)
	item := func(kind string, parent any, title string) int64 {
		return f.exec(`INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, added_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 0, 0)`, lib, kind, parent, title, title, title)
	}
	harbor, paper := item("movie", nil, "Harbor Lights"), item("movie", nil, "Paper Trail")
	show := item("show", nil, "Quiet Street")
	season := item("season", show, "Season 1")
	ep1, ep2 := item("episode", season, "Pilot"), item("episode", season, "Second")

	at := func(day, hour, min int) time.Time { return time.Date(2026, 3, day, hour, min, 0, 0, statsZone) }
	hour := int64(3_600_000)
	// Monday 9 March: two films at once in the evening, on two kinds of device.
	f.play(play{"ana", "movie", "Harbor Lights", harbor, "direct_play", "android_tv", "TV", at(9, 21, 10), 60, hour, "192.168.1.20"})
	f.play(play{"ben", "movie", "Harbor Lights", harbor, "remux", "web", "Chrome", at(9, 21, 30), 60, hour, "100.101.102.103"})
	// Tuesday: a morning episode, a second straight after it (touching, not
	// overlapping), and one in the evening.
	f.play(play{"ana", "episode", "Pilot", ep1, "transcode", "web", "Firefox", at(10, 8, 5), 20, 1_200_000, "192.168.1.20"})
	f.play(play{"ana", "episode", "Second", ep2, "direct_play", "android_tv", "TV", at(10, 8, 25), 10, 600_000, "192.168.1.20"})
	f.play(play{"ben", "episode", "Second", ep2, "direct_play", "android_tv", "TV", at(10, 21, 40), 40, 2_000_000, "192.168.1.30"})
	// Not counted: a misclick, a refusal, and a play from before the range.
	f.play(play{"ana", "movie", "Paper Trail", paper, "direct_play", "android_tv", "TV", at(11, 10, 0), 1, 10_000, "100.101.102.103"})
	f.play(play{"ben", "movie", "Paper Trail", paper, "refused", "web", "Safari", at(11, 11, 0), 0, 0, ""})
	f.play(play{"ben", "movie", "Paper Trail", paper, "direct_play", "web", "Safari", time.Date(2026, 2, 1, 21, 0, 0, 0, statsZone), 90, hour, ""})

	s := &Service{DB: f.conn, Location: statsZone, Now: func() time.Time { return at(11, 20, 0) }}
	st, err := s.Stats(context.Background(), 7)
	if err != nil {
		t.Fatal(err)
	}
	if st.Plays != 5 || st.Users != 2 || st.Refused != 1 {
		t.Fatalf("totals: plays %d users %d refused %d", st.Plays, st.Users, st.Refused)
	}
	if st.ByHour[21] != 3 || st.ByHour[8] != 2 || st.ByHour[10] != 0 {
		t.Fatalf("by hour: %v", st.ByHour)
	}
	if st.ByWeekday[time.Monday] != 2 || st.ByWeekday[time.Tuesday] != 3 || st.ByWeekday[time.Wednesday] != 0 {
		t.Fatalf("by weekday: %v", st.ByWeekday)
	}
	if len(st.TopUsers) != 2 || st.TopUsers[0].Name != "ana" || st.TopUsers[0].Plays != 3 || st.TopUsers[1].Plays != 2 {
		t.Fatalf("top users: %+v", st.TopUsers)
	}
	if len(st.TopMovies) != 1 || st.TopMovies[0].Name != "Harbor Lights" || st.TopMovies[0].Plays != 2 || st.TopMovies[0].Users != 2 {
		t.Fatalf("top movies: %+v", st.TopMovies)
	}
	if len(st.TopShows) != 1 || st.TopShows[0].Name != "Quiet Street" || st.TopShows[0].Plays != 3 || st.TopShows[0].Users != 2 {
		t.Fatalf("top shows: %+v", st.TopShows)
	}
	want := map[string]int{"direct_play": 3, "remux": 1, "transcode": 1}
	if len(st.PlaybackTypes) != 3 || st.PlaybackTypes[0].Name != "direct_play" {
		t.Fatalf("playback types: %+v", st.PlaybackTypes)
	}
	for _, r := range st.PlaybackTypes {
		if want[r.Name] != r.Plays {
			t.Fatalf("playback types: %+v", st.PlaybackTypes)
		}
	}
	plat := map[string]int{}
	for _, r := range st.PlatformTypes {
		plat[r.Name] = r.Plays
	}
	if len(plat) != 3 || plat["Google TV"] != 3 || plat["Web (Chrome)"] != 1 || plat["Web (Firefox)"] != 1 || st.PlatformTypes[0].Name != "Google TV" {
		t.Fatalf("platform types: %+v", st.PlatformTypes)
	}
	if st.PeakStreams != 2 || st.PeakAt != at(9, 21, 30).UnixMilli() {
		t.Fatalf("peak: %d at %v", st.PeakStreams, time.UnixMilli(st.PeakAt).In(statsZone))
	}

	// A shorter range drops Monday and Tuesday entirely.
	s.Now = func() time.Time { return at(20, 12, 0) }
	st, err = s.Stats(context.Background(), 7)
	if err != nil || st.Plays != 0 || st.PeakStreams != 0 || st.PeakAt != 0 || len(st.PlaybackTypes) != 0 {
		t.Fatalf("empty range: %+v %v", st, err)
	}

	// History and users carry the address's kind of network.
	hist, err := s.History(context.Background(), HistoryFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	loc := map[string]string{}
	for _, x := range hist.Sessions {
		if x.IP != nil {
			loc[*x.IP] = x.Location
		}
	}
	if loc["100.101.102.103"] != "Tailscale" || loc["192.168.1.20"] != "Local network" {
		t.Fatalf("history locations: %v", loc)
	}
	users, err := s.Users(context.Background())
	if err != nil || len(users) != 2 || users[0].LastLocation != "Tailscale" {
		t.Fatalf("users: %+v %v", users, err)
	}
}

// One stream ending as another starts is a hand-over, not two at once.
func TestPeakTouchingIsNotOverlap(t *testing.T) {
	f := newStatsFixture(t)
	at := func(h, m int) time.Time { return time.Date(2026, 3, 9, h, m, 0, 0, statsZone) }
	hour := int64(3_600_000)
	lib := f.exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('M', 'movie', '/media', 0, 0)`)
	m := f.exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (?, 'movie', 'a', 'A', 'A', 0, 0)`, lib)
	f.play(play{"ana", "movie", "A", m, "direct_play", "web", "Chrome", at(10, 0), 60, hour, ""})
	f.play(play{"ana", "movie", "B", m, "direct_play", "web", "Chrome", at(11, 0), 60, hour, ""})
	f.play(play{"ben", "movie", "C", m, "direct_play", "web", "Chrome", at(10, 30), 15, 900_000, ""})
	f.play(play{"ben", "movie", "D", m, "direct_play", "web", "Chrome", at(10, 50), 40, hour, ""})
	s := &Service{DB: f.conn, Location: statsZone}
	n, when, err := s.peak(context.Background(), at(0, 0).UnixMilli())
	if err != nil || n != 2 || when != at(10, 30).UnixMilli() {
		t.Fatalf("peak %d at %v, want 2 at 10:30 (%v)", n, time.UnixMilli(when).In(statsZone), err)
	}
}
