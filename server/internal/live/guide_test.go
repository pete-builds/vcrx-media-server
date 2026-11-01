package live

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// guideAt is 2026-11-02 00:30 UTC: News is on now, Cooking is next.
var guideAt = time.Date(2026, 11, 2, 0, 30, 0, 0, time.UTC)

func xmltvTime(t time.Time) string { return t.UTC().Format("20060102150405 +0000") }

func programme(channel, title string, start, stop time.Time) string {
	return fmt.Sprintf(`<programme start=%q stop=%q channel=%q><title>%s</title></programme>`, xmltvTime(start), xmltvTime(stop), channel, title)
}

func writeGuide(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "guide.xml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestGuideMatchesKeepsAWindowAndFillsNowNext(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := &Store{DB: newDB(t), Now: func() time.Time { return guideAt }}
	h := time.Hour
	guide := writeGuide(t, `<?xml version="1.0"?><tv>`+
		`<channel id="NEWS.US"><display-name>Anything</display-name></channel>`+
		`<channel id="kitchen.tv"><display-name>Cooking</display-name></channel>`+
		programme("NEWS.US", "Evening News", guideAt.Add(-h/2), guideAt.Add(h/2))+
		programme("NEWS.US", "Late News", guideAt.Add(h/2), guideAt.Add(3*h/2))+
		programme("NEWS.US", "Last Week", guideAt.Add(-8*24*h), guideAt.Add(-8*24*h+h))+
		programme("NEWS.US", "Next Month", guideAt.Add(30*24*h), guideAt.Add(30*24*h+h))+
		programme("kitchen.tv", "Soup Hour", guideAt.Add(h), guideAt.Add(2*h))+
		programme("nobody", "Unmatched", guideAt, guideAt.Add(h))+
		`</tv>`)
	r := &Refresher{Store: s, Log: quiet, Sources: []SourceSpec{{Name: "Free", Location: "/config/free.m3u", Guide: guide}}}
	if err := r.Register(ctx); err != nil {
		t.Fatal(err)
	}
	src := r.ids["Free"]
	// News by tvg-id (case aside), Cooking by the guide's display name, Weather has no guide.
	if err := s.Sync(ctx, src, []Entry{
		{Name: "News", TvgID: "news.us", Number: "1", URL: "https://s.example/n.m3u8"},
		{Name: "Cooking", Number: "2", URL: "https://s.example/c.m3u8"},
		{Name: "Weather", Number: "3", URL: "https://s.example/w.m3u8"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.RefreshGuide(ctx, src); err != nil {
		t.Fatal(err)
	}
	sources, _ := s.Sources(ctx)
	if !sources[0].HasGuide || sources[0].ProgramCount != 3 || sources[0].GuideRefreshedAt == nil || sources[0].GuideError != nil {
		t.Fatalf("source %+v: want 3 programmes kept (last week and next month are outside the window)", sources[0])
	}

	chans, _ := s.Channels(ctx, true)
	if err := s.NowNext(ctx, chans); err != nil {
		t.Fatal(err)
	}
	news, cooking, weather := chans[0], chans[1], chans[2]
	if news.Now == nil || news.Now.Title != "Evening News" || news.Next == nil || news.Next.Title != "Late News" {
		t.Errorf("news now/next: %+v %+v", news.Now, news.Next)
	}
	if cooking.Now != nil || cooking.Next == nil || cooking.Next.Title != "Soup Hour" {
		t.Errorf("cooking: nothing on now, Soup Hour next; got %+v %+v", cooking.Now, cooking.Next)
	}
	if weather.Now != nil || weather.Next != nil {
		t.Errorf("weather has no guide: %+v %+v", weather.Now, weather.Next)
	}

	g, err := s.Guide(ctx, true, guideAt, guideAt.Add(h))
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 3 || len(g[0].Programs) != 2 || len(g[1].Programs) != 0 || g[0].Programs[0].Categories == nil {
		t.Errorf("guide %+v: News overlaps the hour twice, Soup Hour starts at its end", g)
	}
	if g, _ := s.Guide(ctx, false, guideAt, guideAt.Add(h)); len(g) != 0 {
		t.Errorf("guide without the grant: %d channels", len(g))
	}
}

func TestAFailedGuideReadKeepsTheGuide(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := &Store{DB: newDB(t), Now: func() time.Time { return guideAt }}
	served := `<tv>` + programme("news", "Evening News", guideAt, guideAt.Add(time.Hour)) + `</tv>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, served) }))
	t.Cleanup(srv.Close)
	r := &Refresher{Store: s, Log: quiet, Sources: []SourceSpec{{Name: "Free", Location: "/config/free.m3u", Guide: srv.URL + "/epg.xml?key=s3cret"}}}
	if err := r.Register(ctx); err != nil {
		t.Fatal(err)
	}
	src := r.ids["Free"]
	if err := s.Sync(ctx, src, []Entry{{Name: "News", TvgID: "news", URL: "https://s.example/n.m3u8"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.RefreshGuide(ctx, src); err != nil {
		t.Fatal(err)
	}
	served = "<html>Sign in</html>"
	if err := r.RefreshGuide(ctx, src); err == nil {
		t.Fatal("a sign-in page was read as a guide")
	}
	if _, err := s.DB.Exec(`UPDATE live_sources SET guide_url = 'http://127.0.0.1:1/epg.xml?key=s3cret' WHERE id = ?`, src); err != nil {
		t.Fatal(err)
	}
	if err := r.RefreshGuide(ctx, src); err == nil {
		t.Fatal("an unreachable guide was read")
	}
	sources, _ := s.Sources(ctx)
	if sources[0].ProgramCount != 1 || sources[0].GuideError == nil || strings.Contains(*sources[0].GuideError, "s3cret") {
		t.Errorf("source %+v: want the old programme kept and a reason without the URL", sources[0])
	}
	g, _ := s.Guide(ctx, true, guideAt, guideAt.Add(time.Hour))
	if len(g) != 1 || len(g[0].Programs) != 1 {
		t.Errorf("guide after failed reads: %+v", g)
	}
}

func TestASourceWithoutAGuideIsLeftAlone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := &Store{DB: newDB(t)}
	r := &Refresher{Store: s, Log: quiet, Sources: []SourceSpec{{Name: "Free", Location: "/config/free.m3u"}}}
	if err := r.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.RefreshGuide(ctx, r.ids["Free"]); err != nil {
		t.Errorf("no guide: %v", err)
	}
	sources, _ := s.Sources(ctx)
	if sources[0].HasGuide || sources[0].GuideRefreshedAt != nil {
		t.Errorf("source %+v", sources[0])
	}
}
