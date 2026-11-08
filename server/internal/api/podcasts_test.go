package api

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/podcasts"
)

// podcastAudio is an episode's bytes as its host serves them.
var podcastAudio = func() []byte {
	b := make([]byte, 4000)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}()

// podcastHost is a podcast host on loopback: two synthetic feeds, one marked
// explicit, their artwork and audio, and a stand-in for Apple's search. The
// service under test reaches it only because its address is in AllowHosts.
type podcastHost struct {
	srv                     *httptest.Server
	explicitURL, cleanURL   string
	podcast, episode        int64 // the explicit show and its episode, once the walk subscribes
	explicitTitle, epTitle  string
	cleanTitle, cleanEpName string
}

func podcastFeed(base, title, explicit, episode, audio string) string {
	return fmt.Sprintf(`<?xml version="1.0"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"><channel>
<title>%s</title><itunes:author>Walk Host</itunes:author><description>A show for the walk.</description>
<itunes:explicit>%s</itunes:explicit><itunes:image href="%s/art.png"/>
<item><title>%s</title><guid>%s-1</guid><pubDate>%s</pubDate><itunes:duration>600</itunes:duration>
<itunes:image href="%s/ep.png"/><enclosure url="%s/%s" type="audio/mpeg" length="4000"/></item>
</channel></rss>`, title, explicit, base, episode, audio, time.Now().Add(-time.Hour).Format(time.RFC1123Z), base, base, audio)
}

func withPodcasts(t *testing.T, f *fixture) *podcastHost {
	t.Helper()
	h := &podcastHost{explicitTitle: "Walk After Dark", epTitle: "Night Shift", cleanTitle: "Walk Mornings", cleanEpName: "Sunrise"}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/after-dark.xml":
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = io.WriteString(w, podcastFeed(base, h.explicitTitle, "yes", h.epTitle, "night.mp3"))
		case "/mornings.xml":
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = io.WriteString(w, podcastFeed(base, h.cleanTitle, "no", h.cleanEpName, "page.mp3"))
		case "/art.png", "/ep.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = io.WriteString(w, "\x89PNG\r\n\x1a\n"+r.URL.Path)
		case "/night.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			http.ServeContent(w, r, "night.mp3", time.Unix(0, 0), bytes.NewReader(podcastAudio))
		case "/page.mp3":
			// A host that answers with a page instead of audio.
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<script>alert(1)</script>")
		case "/search":
			_, _ = fmt.Fprintf(w, `{"results":[
				{"collectionName":%q,"artistName":"Walk Host","feedUrl":"%s/after-dark.xml","collectionExplicitness":"explicit"},
				{"collectionName":%q,"artistName":"Walk Host","feedUrl":"%s/mornings.xml","collectionExplicitness":"notExplicit"}]}`,
				h.explicitTitle, base, h.cleanTitle, base)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(h.srv.Close)
	h.explicitURL, h.cleanURL = h.srv.URL+"/after-dark.xml", h.srv.URL+"/mornings.xml"
	svc := podcasts.NewService(&podcasts.Store{DB: f.db}, podcasts.Guard{AllowHosts: []string{strings.TrimPrefix(h.srv.URL, "http://")}}, t.TempDir())
	svc.Search, svc.SearchURL = true, h.srv.URL+"/search"
	svc.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	f.srv.Podcasts = &Podcasts{Service: svc}
	return h
}

// subscribe subscribes tok to feed and returns the show's id and its newest
// episode's.
func (h *podcastHost) subscribe(t *testing.T, f *fixture, tok, feed string) (int64, int64) {
	t.Helper()
	r := f.do("POST", "/api/v1/podcasts", tok, map[string]any{"url": feed})
	var out struct{ Podcast struct{ ID int64 } }
	r.json(t, &out)
	if (r.code != 201 && r.code != 200) || out.Podcast.ID == 0 {
		t.Fatalf("subscribe to %s: %d %s", feed, r.code, r.body)
	}
	r = f.do("GET", fmt.Sprintf("/api/v1/podcasts/shows/%d", out.Podcast.ID), tok, nil)
	var show struct{ Episodes []struct{ ID int64 } }
	r.json(t, &show)
	if r.code != 200 || len(show.Episodes) == 0 {
		t.Fatalf("show %d: %d %s", out.Podcast.ID, r.code, r.body)
	}
	return out.Podcast.ID, show.Episodes[0].ID
}

// The stream route is a proxy: a Range goes to the host and its partial
// answer comes back as it is, a full GET is the whole file (the phone app's
// download), HEAD has no body, and a host answering with anything but audio
// is passed on as bytes a browser will not run.
func TestPodcastStreamPassesRangeOn(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := withPodcasts(t, f)
	_, ep := h.subscribe(t, f, f.friendTok, h.explicitURL)
	path := fmt.Sprintf("/api/v1/podcasts/episodes/%d/stream", ep)

	r := f.do("GET", path, f.friendTok, nil, "Range", "bytes=100-199")
	if r.code != http.StatusPartialContent || r.hdr.Get("Content-Range") != "bytes 100-199/4000" || !bytes.Equal(r.body, podcastAudio[100:200]) {
		t.Fatalf("range: %d %q %d bytes", r.code, r.hdr.Get("Content-Range"), len(r.body))
	}
	if r.hdr.Get("Content-Type") != "audio/mpeg" || r.hdr.Get("Content-Length") != "100" {
		t.Fatalf("range headers: %v", r.hdr)
	}
	r = f.do("GET", path, f.friendTok, nil)
	if r.code != 200 || !bytes.Equal(r.body, podcastAudio) || r.hdr.Get("Accept-Ranges") != "bytes" || r.hdr.Get("Content-Length") != "4000" {
		t.Fatalf("download: %d %d bytes %v", r.code, len(r.body), r.hdr)
	}
	r = f.do("HEAD", path, f.friendTok, nil)
	if r.code != 200 || len(r.body) != 0 || r.hdr.Get("Content-Length") != "4000" {
		t.Fatalf("head: %d %d bytes %v", r.code, len(r.body), r.hdr)
	}
	r = f.do("GET", path, f.friendTok, nil, "Range", "bytes=9000-")
	if r.code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("range past the end: %d", r.code)
	}

	_, page := h.subscribe(t, f, f.friendTok, h.cleanURL)
	r = f.do("GET", fmt.Sprintf("/api/v1/podcasts/episodes/%d/stream", page), f.friendTok, nil)
	// The feed says audio/mpeg; the host's text/html is not passed on.
	if r.code != 200 || r.hdr.Get("Content-Type") != "audio/mpeg" || r.hdr.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(r.hdr.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("page as audio: %d %v", r.code, r.hdr)
	}
}

// Progress, played and new are the caller's own; a place near the end marks
// the episode played.
func TestPodcastProgressIsTheCallersOwn(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := withPodcasts(t, f)
	show, ep := h.subscribe(t, f, f.friendTok, h.cleanURL)
	h.subscribe(t, f, f.adminTok, h.cleanURL)
	prog := fmt.Sprintf("/api/v1/podcasts/episodes/%d/progress", ep)

	if r := f.do("PUT", prog, f.friendTok, map[string]any{"position_seconds": 125.5}); r.code != 200 {
		t.Fatalf("save: %d %s", r.code, r.body)
	}
	var p struct {
		Position float64 `json:"position_seconds"`
		Played   bool
		Duration *int64 `json:"duration_seconds"`
	}
	f.do("GET", prog, f.friendTok, nil).json(t, &p)
	if p.Position != 125.5 || p.Played || p.Duration == nil || *p.Duration != 600 {
		t.Fatalf("friend's place: %+v", p)
	}
	p.Position = -1
	f.do("GET", prog, f.adminTok, nil).json(t, &p)
	if p.Position != 0 {
		t.Fatalf("the admin sees the friend's place: %+v", p)
	}
	// Started, so no longer new for the friend; still new for the admin.
	var news struct{ Episodes []struct{ ID int64 } }
	f.do("GET", "/api/v1/podcasts/new", f.friendTok, nil).json(t, &news)
	if len(news.Episodes) != 0 {
		t.Fatalf("a started episode is new: %+v", news)
	}
	f.do("GET", "/api/v1/podcasts/new", f.adminTok, nil).json(t, &news)
	if len(news.Episodes) != 1 || news.Episodes[0].ID != ep {
		t.Fatalf("admin's new: %+v", news)
	}
	if r := f.do("PUT", prog, f.friendTok, map[string]any{"position_seconds": 590}); r.code != 200 || !strings.Contains(string(r.body), `"played":true`) {
		t.Fatalf("near the end: %d %s", r.code, r.body)
	}
	var list struct {
		Podcasts []struct{ ID, UnplayedCount int64 }
	}
	f.do("GET", "/api/v1/podcasts", f.friendTok, nil).json(t, &list)
	if len(list.Podcasts) != 1 || list.Podcasts[0].ID != show || list.Podcasts[0].UnplayedCount != 0 {
		t.Fatalf("unplayed after playing: %+v", list)
	}
	played := fmt.Sprintf("/api/v1/podcasts/episodes/%d/played", ep)
	if r := f.do("PUT", played, f.friendTok, map[string]any{"played": false}); r.code != 200 || !strings.Contains(string(r.body), `"played":false`) {
		t.Fatalf("unplayed: %d %s", r.code, r.body)
	}
	for _, bad := range []map[string]any{{}, {"position_seconds": -3}, {"position_seconds": "x"}} {
		if r := f.do("PUT", prog, f.friendTok, bad); r.code != 400 {
			t.Errorf("bad progress %v: %d", bad, r.code)
		}
	}
	if r := f.do("PUT", played, f.friendTok, map[string]any{}); r.code != 400 {
		t.Errorf("played without a value: %d", r.code)
	}
	// Unsubscribed, the episode is gone for the friend, not for the admin.
	if r := f.do("DELETE", fmt.Sprintf("/api/v1/podcasts/shows/%d", show), f.friendTok, nil); r.code != 204 {
		t.Fatalf("unsubscribe: %d %s", r.code, r.body)
	}
	if r := f.do("GET", prog, f.friendTok, nil); r.code != 404 {
		t.Fatalf("after unsubscribing: %d", r.code)
	}
	if r := f.do("GET", prog, f.adminTok, nil); r.code != 200 {
		t.Fatalf("the admin's subscription: %d", r.code)
	}
}

// Podcasts are for accounts and household profiles. An invited device or a
// linked server is refused every route, and /me tells each caller whether it
// has podcasts. Off, every route is a 404.
func TestPodcastsAreForAccountsOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	me := func(tok string) (bool, bool) {
		var out struct {
			Podcasts      bool `json:"podcasts"`
			PodcastSearch bool `json:"podcast_search"`
		}
		r := f.do("GET", "/api/v1/me", tok, nil)
		r.json(t, &out)
		return out.Podcasts, out.PodcastSearch
	}
	if on, _ := me(f.friendTok); on {
		t.Fatal("podcasts offered while off")
	}
	if r := f.do("GET", "/api/v1/podcasts", f.friendTok, nil); r.code != 404 || !strings.Contains(string(r.body), "podcasts_off") {
		t.Fatalf("off: %d %s", r.code, r.body)
	}
	withPodcasts(t, f)
	if on, search := me(f.friendTok); !on || !search {
		t.Fatalf("account: podcasts %v search %v", on, search)
	}
	device := f.invite(map[string]any{"name": "Den TV"})
	peer := f.linkServer(map[string]any{"name": "A friend's server"})
	for _, tok := range []string{device.token, peer.token} {
		if on, _ := me(tok); on {
			t.Error("podcasts offered to an invited device or linked server")
		}
		for _, rt := range f.srv.podcastRoutes() {
			path := routeParam.ReplaceAllString(rt.Path, "1")
			if r := f.do(rt.Method, path, tok, nil); r.code != http.StatusForbidden || (rt.Method != "HEAD" && !strings.Contains(string(r.body), "accounts_only")) {
				t.Errorf("%s %s: %d %.120s, want 403 accounts_only", rt.Method, rt.Path, r.code, r.body)
			}
		}
	}
}

// A feed URL is the user's, so the server refuses to fetch anything that is
// not a public address: by the URL itself, and when a host it may fetch
// redirects somewhere it may not.
func TestPodcastSubscribeRefusesPrivateAddresses(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := withPodcasts(t, f)
	inside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the server fetched %s from a private address", r.URL)
	}))
	defer inside.Close()
	bounce := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, inside.URL+"/feed.xml", http.StatusFound)
	}))
	defer bounce.Close()
	g := f.srv.Podcasts.Service.Guard
	g.AllowHosts = append(g.AllowHosts, strings.TrimPrefix(bounce.URL, "http://"))
	svc := podcasts.NewService(f.srv.Podcasts.Service.Store, g, t.TempDir())
	f.srv.Podcasts.Service = svc

	for _, u := range []string{inside.URL + "/feed.xml", "http://localhost/feed.xml", "http://10.9.9.9/feed.xml", "http://[::1]/feed.xml",
		"http://169.254.169.254/latest/meta-data/", "file:///etc/passwd", "gopher://feeds.example.com/x"} {
		if r := f.do("POST", "/api/v1/podcasts", f.friendTok, map[string]any{"url": u}); r.code != 400 || !strings.Contains(string(r.body), "bad_url") {
			t.Errorf("%s: %d %s", u, r.code, r.body)
		}
	}
	r := f.do("POST", "/api/v1/podcasts", f.friendTok, map[string]any{"url": bounce.URL + "/feed.xml"})
	if r.code != 422 || !strings.Contains(string(r.body), "public internet") {
		t.Errorf("redirect to loopback: %d %s", r.code, r.body)
	}
	_ = h
}

// A kids profile (a content rating ceiling under R) never sees a show marked
// explicit: not in its list, search, OPML import or a subscription by URL.
func TestPodcastsHideExplicitFromAKidsProfile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	h := withPodcasts(t, f)
	h.subscribe(t, f, f.friendTok, h.explicitURL)
	h.subscribe(t, f, f.friendTok, h.cleanURL)
	friend := f.userID("friend")
	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/users/%d/policy", friend), f.adminTok, map[string]any{"max_content_rating": "PG"}); r.code != 204 {
		t.Fatalf("policy: %d %s", r.code, r.body)
	}
	for _, path := range []string{"/api/v1/podcasts", "/api/v1/podcasts/new", "/api/v1/podcasts/search?q=walk", "/api/v1/podcasts/opml"} {
		r := f.do("GET", path, f.friendTok, nil)
		if r.code != 200 || strings.Contains(string(r.body), h.explicitTitle) || strings.Contains(string(r.body), h.epTitle) {
			t.Errorf("%s: %d %s", path, r.code, r.body)
		}
		if path != "/api/v1/podcasts/new" && !strings.Contains(string(r.body), h.cleanTitle) {
			t.Errorf("%s lost the clean show: %s", path, r.body)
		}
	}
	if r := f.do("POST", "/api/v1/podcasts", f.friendTok, map[string]any{"url": h.explicitURL}); r.code != 403 || !strings.Contains(string(r.body), `"explicit"`) {
		t.Errorf("subscribe to explicit: %d %s", r.code, r.body)
	}
	opml := fmt.Sprintf(`<opml version="2.0"><body><outline type="rss" text="a" xmlUrl="%s"/><outline type="rss" text="b" xmlUrl="%s"/></body></opml>`, h.explicitURL, h.cleanURL)
	r := f.do("POST", "/api/v1/podcasts/opml", f.friendTok, rawBody(opml))
	if r.code != 200 || !strings.Contains(string(r.body), `"already":1`) || !strings.Contains(string(r.body), "Marked explicit") {
		t.Errorf("opml import: %d %s", r.code, r.body)
	}
}
