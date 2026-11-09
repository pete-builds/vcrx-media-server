package podcasts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := db.Migrate(context.Background(), conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"ann", "bob"} {
		if _, err := conn.Exec(`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES (?, 'x', 0, 0)`, u); err != nil {
			t.Fatal(err)
		}
	}
	return conn
}

// feedHost serves a synthetic feed, its artwork and its audio, answering
// conditional requests the way a well-behaved host does.
type feedHost struct {
	srv      *httptest.Server
	feed     atomic.Value // string
	reads    atomic.Int32
	notMod   atomic.Int32
	explicit atomic.Bool
}

func newFeedHost(t *testing.T) *feedHost {
	t.Helper()
	h := &feedHost{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed.xml":
			h.reads.Add(1)
			body := h.feed.Load().(string)
			etag := fmt.Sprintf(`"%x"`, len(body))
			if r.Header.Get("If-None-Match") == etag {
				h.notMod.Add(1)
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = io.WriteString(w, body)
		case "/art.png", "/ep.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = io.WriteString(w, "\x89PNG\r\n\x1a\n"+r.URL.Path)
		case "/page.html":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<html><body>hello</body></html>")
		case "/huge.xml":
			_, _ = io.WriteString(w, `<rss><channel><title>x</title>`+strings.Repeat(" ", maxFeedBytes+10))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(h.srv.Close)
	h.setEpisodes(3)
	return h
}

func (h *feedHost) host() string { return strings.TrimPrefix(h.srv.URL, "http://") }

func (h *feedHost) setEpisodes(n int) {
	var b strings.Builder
	explicit := "no"
	if h.explicit.Load() {
		explicit = "yes"
	}
	fmt.Fprintf(&b, `<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"><channel><title>Test Cast</title>
		<itunes:explicit>%s</itunes:explicit><itunes:image href="%s/art.png"/>`, explicit, h.srv.URL)
	now := time.Now()
	for i := n; i >= 1; i-- {
		img := ""
		if i == n {
			img = fmt.Sprintf(`<itunes:image href="%s/ep.png"/>`, h.srv.URL)
		}
		fmt.Fprintf(&b, `<item><title>Ep %d</title><guid>ep-%d</guid><pubDate>%s</pubDate><itunes:duration>600</itunes:duration>%s
			<enclosure url="%s/ep%d.mp3" type="audio/mpeg" length="1000"/></item>`, i, i, now.Add(-time.Duration(n-i)*24*time.Hour).Format(time.RFC1123Z), img, h.srv.URL, i)
	}
	b.WriteString(`</channel></rss>`)
	h.feed.Store(b.String())
}

func newService(t *testing.T, h *feedHost) *Service {
	t.Helper()
	s := NewService(&Store{DB: openDB(t)}, Guard{AllowHosts: []string{h.host()}}, t.TempDir())
	s.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	return s
}

func TestSubscribeFetchesOnceAndSharesTheFeed(t *testing.T) {
	t.Parallel()
	h := newFeedHost(t)
	s := newService(t, h)
	ctx := context.Background()
	ann, bob := Viewer{UserID: 1}, Viewer{UserID: 2}
	p, created, err := s.Subscribe(ctx, ann, h.srv.URL+"/feed.xml")
	if err != nil || !created {
		t.Fatalf("subscribe: %v %v", created, err)
	}
	if p.Title != "Test Cast" || p.EpisodeCount != 3 || p.UnplayedCount != 3 || p.NewCount != 3 || !p.HasArtwork {
		t.Fatalf("podcast: %+v", p)
	}
	if _, created, err := s.Subscribe(ctx, ann, h.srv.URL+"/feed.xml"); err != nil || created {
		t.Fatalf("subscribing again: created %v err %v", created, err)
	}
	if _, _, err := s.Subscribe(ctx, bob, h.srv.URL+"/feed.xml"); err != nil {
		t.Fatal(err)
	}
	if n := h.reads.Load(); n != 1 {
		t.Fatalf("the feed was read %d times for three subscriptions, want once", n)
	}
	// Artwork is downloaded and served from the image folder.
	name, err := s.Store.ShowArtwork(ctx, ann, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	path, ctype, ok := s.ArtworkPath(name)
	if b, err := os.ReadFile(path); !ok || ctype != "image/png" || err != nil || !strings.Contains(string(b), "/art.png") {
		t.Fatalf("artwork %s %s %v %v", path, ctype, ok, err)
	}
	eps, total, err := s.Store.Episodes(ctx, ann, p.ID, 50, 0)
	if err != nil || total != 3 || len(eps) != 3 || eps[0].Title != "Ep 3" {
		t.Fatalf("episodes %d %v %+v", total, err, eps)
	}
	if f, err := s.Store.EpisodeArtwork(ctx, ann, eps[0].ID); err != nil || f == name {
		t.Fatalf("newest episode's own artwork: %q %v", f, err)
	}
	if f, err := s.Store.EpisodeArtwork(ctx, ann, eps[1].ID); err != nil || f != name {
		t.Fatalf("an episode without its own falls back to the show's: %q %v", f, err)
	}
	if strings.Contains(fmt.Sprint(eps), "ep3.mp3") {
		t.Fatal("an episode view carries its enclosure URL")
	}
}

func TestProgressIsEachUsersOwn(t *testing.T) {
	t.Parallel()
	h := newFeedHost(t)
	s := newService(t, h)
	ctx := context.Background()
	ann, bob := Viewer{UserID: 1}, Viewer{UserID: 2}
	p, _, err := s.Subscribe(ctx, ann, h.srv.URL+"/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	eps, _, _ := s.Store.Episodes(ctx, ann, p.ID, 10, 0)
	ep := eps[0].ID
	// Bob is not subscribed: he cannot see or touch the episode.
	if _, err := s.Store.SetProgress(ctx, bob, ep, 30, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob's progress on a show he does not subscribe to: %v", err)
	}
	if _, err := s.Store.Enclosure(ctx, bob, ep); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob's stream of a show he does not subscribe to: %v", err)
	}
	if _, _, err := s.Subscribe(ctx, bob, h.srv.URL+"/feed.xml"); err != nil {
		t.Fatal(err)
	}
	if pr, err := s.Store.SetProgress(ctx, ann, ep, 125.5, 0); err != nil || pr.Position != 125.5 || pr.Played {
		t.Fatalf("ann's progress: %+v %v", pr, err)
	}
	if pr, _ := s.Store.GetProgress(ctx, bob, ep); pr.Position != 0 {
		t.Fatalf("bob sees ann's place: %+v", pr)
	}
	got, _ := s.Store.Episode(ctx, ann, ep)
	if got.Position != 125.5 || got.New {
		t.Fatalf("a started episode: %+v", got)
	}
	// Within 30 seconds of the end (600 s) is played, and the place resets.
	if pr, err := s.Store.SetProgress(ctx, ann, ep, 575, 0); err != nil || !pr.Played || pr.Position != 0 {
		t.Fatalf("near the end: %+v %v", pr, err)
	}
	if pr, err := s.Store.SetPlayed(ctx, ann, ep, false); err != nil || pr.Played {
		t.Fatalf("unplayed: %+v %v", pr, err)
	}
	if _, err := s.Store.SetPlayed(ctx, ann, eps[1].ID, true); err != nil {
		t.Fatal(err)
	}
	show, _ := s.Store.Show(ctx, ann, p.ID)
	if show.UnplayedCount != 2 || show.NewCount != 2 {
		t.Fatalf("counts after one played: %+v", show)
	}
	if show, _ := s.Store.Show(ctx, bob, p.ID); show.UnplayedCount != 3 {
		t.Fatalf("bob's counts follow ann's plays: %+v", show)
	}
	news, err := s.Store.NewEpisodes(ctx, ann, 10)
	if err != nil || len(news) != 2 {
		t.Fatalf("new episodes: %v %+v", err, news)
	}
	// Unsubscribing drops ann's places; the feed stays for bob.
	if err := s.Unsubscribe(ctx, ann, p.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM podcast_episode_state WHERE user_id = 1`).Scan(&n)
	if n != 0 {
		t.Fatalf("ann's state left behind: %d rows", n)
	}
	if _, err := s.Store.Show(ctx, bob, p.ID); err != nil {
		t.Fatalf("bob lost the show when ann left: %v", err)
	}
	art, _ := s.Store.ShowArtwork(ctx, bob, p.ID)
	if err := s.Unsubscribe(ctx, bob, p.ID); err != nil {
		t.Fatal(err)
	}
	_ = s.Store.DB.QueryRow(`SELECT COUNT(*) FROM podcasts`).Scan(&n)
	if n != 0 {
		t.Fatal("a show nobody subscribes to was kept")
	}
	if path, _, _ := s.ArtworkPath(art); fileExists(path) {
		t.Fatal("its artwork was kept")
	}
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestRefreshIsConditionalAndCapsEpisodes(t *testing.T) {
	t.Parallel()
	h := newFeedHost(t)
	s := newService(t, h)
	s.MaxEpisodes = 4
	ctx := context.Background()
	ann := Viewer{UserID: 1}
	p, _, err := s.Subscribe(ctx, ann, h.srv.URL+"/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Refresh(ctx, p.ID, false); err != nil {
		t.Fatal(err)
	}
	if h.notMod.Load() != 1 {
		t.Fatalf("an unchanged feed was not asked for conditionally: %d 304s", h.notMod.Load())
	}
	// By hand right after: not read again.
	reads := h.reads.Load()
	if err := s.Refresh(ctx, p.ID, true); err != nil || h.reads.Load() != reads {
		t.Fatalf("a refresh by hand within a minute read the feed: %v", err)
	}
	h.setEpisodes(7)
	if err := s.Refresh(ctx, p.ID, false); err != nil {
		t.Fatal(err)
	}
	_, total, _ := s.Store.Episodes(ctx, ann, p.ID, 50, 0)
	if total != 4 {
		t.Fatalf("kept %d episodes, want the cap of 4", total)
	}
	// A feed that breaks keeps its episodes and says why.
	h.feed.Store("<html>gone</html>")
	if err := s.Refresh(ctx, p.ID, false); err == nil {
		t.Fatal("a broken feed refreshed")
	}
	show, _ := s.Store.Show(ctx, ann, p.ID)
	if show.EpisodeCount != 4 || show.LastError == nil || !strings.Contains(*show.LastError, "not a podcast") {
		t.Fatalf("after a failed refresh: %+v", show)
	}
}

func TestSubscribeRefusals(t *testing.T) {
	t.Parallel()
	h := newFeedHost(t)
	s := newService(t, h)
	ctx := context.Background()
	ann := Viewer{UserID: 1}
	var fe *FeedError
	if _, _, err := s.Subscribe(ctx, ann, h.srv.URL+"/page.html"); !errors.As(err, &fe) {
		t.Fatalf("an HTML page: %v", err)
	}
	if _, _, err := s.Subscribe(ctx, ann, h.srv.URL+"/missing.xml"); !errors.As(err, &fe) || !strings.Contains(fe.Reason, "404") {
		t.Fatalf("a 404: %v", err)
	}
	if _, _, err := s.Subscribe(ctx, ann, h.srv.URL+"/huge.xml"); !errors.As(err, &fe) || !strings.Contains(fe.Reason, "10 MB") {
		t.Fatalf("a huge feed: %v", err)
	}
	// Without the test allowance, the same loopback feed is refused.
	strict := NewService(s.Store, Guard{}, t.TempDir())
	if _, _, err := strict.Subscribe(ctx, ann, h.srv.URL+"/feed.xml"); !Refused(err) {
		t.Fatalf("loopback feed without the allowance: %v", err)
	}
	// A kids profile may not subscribe to an explicit show, new or known.
	h.explicit.Store(true)
	h.setEpisodes(2)
	kid := Viewer{UserID: 2, NoExplicit: true}
	if _, _, err := s.Subscribe(ctx, kid, h.srv.URL+"/feed.xml"); !errors.Is(err, ErrExplicit) {
		t.Fatalf("kid subscribing to an explicit show: %v", err)
	}
	p, _, err := s.Subscribe(ctx, ann, h.srv.URL+"/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Subscribe(ctx, kid, h.srv.URL+"/feed.xml"); !errors.Is(err, ErrExplicit) {
		t.Fatalf("kid subscribing to a known explicit show: %v", err)
	}
	// And a kids viewer of an explicit show's subscription sees none of it.
	if _, err := s.Store.Subscribe(ctx, 2, p.ID); err != nil {
		t.Fatal(err)
	}
	if subs, _ := s.Store.Subscriptions(ctx, kid); len(subs) != 0 {
		t.Fatalf("kid sees an explicit show: %+v", subs)
	}
	if _, err := s.Store.Show(ctx, kid, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("kid opens an explicit show: %v", err)
	}
}

func TestSearchDirectory(t *testing.T) {
	t.Parallel()
	var got atomic.Value
	itunes := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.URL.RawQuery)
		_, _ = io.WriteString(w, `{"resultCount":3,"results":[
			{"collectionName":"Tide Tables","artistName":"Ann","feedUrl":"https://feeds.example.com/tide.xml","primaryGenreName":"Science","collectionExplicitness":"notExplicit"},
			{"collectionName":"Salty","artistName":"Bob","feedUrl":"https://feeds.example.com/salty.xml","collectionExplicitness":"explicit"},
			{"collectionName":"No feed","artistName":"Cy"}]}`)
	}))
	defer itunes.Close()
	s := NewService(&Store{}, Guard{AllowHosts: []string{strings.TrimPrefix(itunes.URL, "http://")}}, "")
	s.SearchURL = itunes.URL + "/search"
	if _, err := s.SearchDirectory(context.Background(), Viewer{}, "tide"); !errors.Is(err, ErrSearchOff) {
		t.Fatalf("search while off: %v", err)
	}
	s.Search = true
	res, err := s.SearchDirectory(context.Background(), Viewer{}, "tide tables")
	if err != nil || len(res) != 2 || res[0].FeedURL != "https://feeds.example.com/tide.xml" || !res[1].Explicit {
		t.Fatalf("results %+v %v", res, err)
	}
	if q, _ := got.Load().(string); !strings.Contains(q, "media=podcast") || !strings.Contains(q, "term=tide+tables") {
		t.Fatalf("query %q", q)
	}
	if res, _ := s.SearchDirectory(context.Background(), Viewer{NoExplicit: true}, "tide"); len(res) != 1 {
		t.Fatalf("kids results %+v", res)
	}
}
