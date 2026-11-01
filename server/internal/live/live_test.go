package live

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := db.Migrate(context.Background(), conn, "", quiet); err != nil {
		t.Fatal(err)
	}
	return conn
}

func names(cs []Channel) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Name)
	}
	return strings.Join(out, ",")
}

func TestSyncKeepsTheAdminsChoices(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := &Store{DB: newDB(t)}
	src, err := s.EnsureSource(ctx, "Free", "https://playlists.example/free.m3u", "")
	if err != nil {
		t.Fatal(err)
	}
	first := []Entry{
		{Name: "News", TvgID: "news", Number: "2", URL: "https://s.example/news.m3u8"},
		{Name: "Weather", TvgID: "wx", Number: "10", URL: "https://s.example/wx.m3u8"},
		{Name: "Cooking", URL: "https://s.example/cook.m3u8"},
	}
	if err := s.Sync(ctx, src, first); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Channels(ctx, true)
	if names(got) != "News,Weather,Cooking" {
		t.Fatalf("channels %q, want numbered first, in number order", names(got))
	}
	all, _ := s.AllChannels(ctx)
	byName := map[string]int64{}
	for _, c := range all {
		byName[c.Name] = c.ID
	}
	off, num := false, "1"
	if err := s.Update(ctx, byName["Weather"], ChannelUpdate{Number: &num}); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(ctx, byName["Cooking"], ChannelUpdate{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	// The playlist renames News, moves Weather's stream and drops Cooking.
	second := []Entry{
		{Name: "News Now", TvgID: "news", Number: "2", URL: "https://s.example/news.m3u8"},
		{Name: "Weather", TvgID: "wx", Number: "10", URL: "https://cdn.example/wx.m3u8"},
	}
	if err := s.Sync(ctx, src, second); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Channels(ctx, true)
	if names(got) != "Weather,News Now" {
		t.Fatalf("channels %q, want the admin's number 1 to survive the refresh", names(got))
	}
	if *got[0].Number != "1" || got[0].SourceID != 0 || got[0].Enabled != nil {
		t.Errorf("viewer's channel %+v: want number 1 and no admin fields", got[0])
	}
	if err := s.Sync(ctx, src, first); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Channels(ctx, true)
	if names(got) != "Weather,News" {
		t.Errorf("channels %q: Cooking came back to the playlist but the admin had turned it off", names(got))
	}
	// An empty number goes back to the playlist's.
	empty := ""
	if err := s.Update(ctx, byName["Weather"], ChannelUpdate{Number: &empty}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Channels(ctx, true)
	if names(got) != "News,Weather" {
		t.Errorf("channels %q after clearing Weather's number", names(got))
	}
	if err := s.Update(ctx, 9999, ChannelUpdate{Enabled: &off}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update of a missing channel: %v", err)
	}
}

func TestWithoutTheGrantNothingIsListed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := &Store{DB: newDB(t)}
	src, _ := s.EnsureSource(ctx, "Free", "/config/free.m3u", "")
	if err := s.Sync(ctx, src, []Entry{{Name: "News", URL: "https://s.example/n.m3u8", Logo: "https://img.example/n.png"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Channels(ctx, false); len(got) != 0 {
		t.Errorf("listed %d channels without the grant", len(got))
	}
	all, _ := s.AllChannels(ctx)
	if err := s.setLogo(ctx, all[0].ID, "https://img.example/n.png", "abc.png"); err != nil {
		t.Fatal(err)
	}
	if f, err := s.Logo(ctx, all[0].ID, true); err != nil || f != "abc.png" {
		t.Fatalf("Logo with the grant: %q %v", f, err)
	}
	if _, err := s.Logo(ctx, all[0].ID, false); !errors.Is(err, ErrNotFound) {
		t.Errorf("Logo without the grant: %v", err)
	}
}

func TestRefreshReadsThePlaylistAndKeepsTheURLsPrivate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	png := []byte("\x89PNG\r\n\x1a\nfake")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/list.m3u":
			_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:-1 tvg-logo=\"http://"+r.Host+"/logo.png\",News\nhttps://s.example/n.m3u8\n"+
				"#EXTINF:-1 tvg-logo=\"http://"+r.Host+"/page.html\",Weather\nhttps://s.example/w.m3u8\n")
		case "/logo.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(png)
		case "/page.html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<html>")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	s := &Store{DB: newDB(t)}
	logos := t.TempDir()
	// The test server is on loopback, which PublicOnlyClient refuses; the
	// plain client stands in for a public logo host here.
	r := &Refresher{Store: s, Sources: []SourceSpec{{Name: "Test", Location: srv.URL + "/list.m3u?token=s3cret"}}, LogoDir: logos, Log: quiet,
		Logos: srv.Client()}
	if err := r.Register(ctx); err != nil {
		t.Fatal(err)
	}
	src := r.ids["Test"]
	if err := r.Refresh(ctx, src); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Channels(ctx, true); got[0].HasLogo {
		t.Error("Refresh fetched a logo; logos are FetchLogos, after the guides")
	}
	r.FetchLogos(ctx, src)
	got, _ := s.Channels(ctx, true)
	if names(got) != "News,Weather" || !got[0].HasLogo || got[1].HasLogo {
		t.Fatalf("channels %+v: want both, News with its logo, Weather without (HTML is not a logo)", got)
	}
	f, _ := s.Logo(ctx, got[0].ID, true)
	if b, err := os.ReadFile(filepath.Join(logos, f)); err != nil || string(b) != string(png) {
		t.Errorf("cached logo %q: %v", f, err)
	}

	// A playlist server that fails: the reason is kept, the URL and its token are not.
	if _, err := s.DB.Exec(`UPDATE live_sources SET url = ? WHERE id = ?`, "http://127.0.0.1:1/list.m3u?token=s3cret", src); err != nil {
		t.Fatal(err)
	}
	if err := r.Refresh(ctx, src); err == nil {
		t.Fatal("refresh of an unreachable playlist succeeded")
	}
	sources, _ := s.Sources(ctx)
	if sources[0].LastError == nil || strings.Contains(*sources[0].LastError, "s3cret") || strings.Contains(*sources[0].LastError, "127.0.0.1:1/list") {
		t.Errorf("last_error %v: want a reason without the URL", sources[0].LastError)
	}
	if got, _ := s.Channels(ctx, true); len(got) != 2 {
		t.Errorf("a failed refresh changed the channels: %d left", len(got))
	}
}

func TestStartWithoutAnIntervalUsesTheDefault(t *testing.T) {
	t.Parallel()
	// No refresh_hours in vcrx.yaml used to reach time.NewTicker(0) and panic.
	list := filepath.Join(t.TempDir(), "free.m3u")
	if err := os.WriteFile(list, []byte("#EXTM3U\n#EXTINF:-1,News\nhttps://s.example/n.m3u8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: newDB(t)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &Refresher{Store: s, Sources: []SourceSpec{{Name: "File", Location: list}}, Log: quiet}
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got, _ := s.Channels(ctx, true); len(got) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the first refresh never ran")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestLogosComeFromPublicAddressesOnly(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"127.0.0.1:80", "[::1]:80", "192.168.1.1:80", "10.0.0.5:443", "10.9.9.9:80", "169.254.169.254:80",
		"100.101.102.103:80", "0.0.0.0:80", "224.0.0.1:80", "[fd7a:115c:a1e0::1]:80", "[fe80::1]:80", "[::ffff:127.0.0.1]:80"} {
		if checkPublic(addr) == nil {
			t.Errorf("%s allowed", addr)
		}
	}
	for _, addr := range []string{"8.8.8.8:443", "[2606:4700::1111]:443"} {
		if err := checkPublic(addr); err != nil {
			t.Errorf("%s refused: %v", addr, err)
		}
	}
	// And through the client: a loopback logo host is refused at the dial.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("x"))
	}))
	t.Cleanup(srv.Close)
	r := &Refresher{LogoDir: t.TempDir(), Logos: PublicOnlyClient(2e9)}
	if _, err := r.fetchLogo(context.Background(), srv.URL+"/logo.png"); err == nil || !strings.Contains(err.Error(), "not a public address") {
		t.Errorf("loopback logo: %v, want refused as not public", err)
	}
}
