package live

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestParseReadsTheExtendedAttributes(t *testing.T) {
	t.Parallel()
	body := "\uFEFF#EXTM3U x-tvg-url=\"https://guide.example/epg.xml\"\n" +
		`#EXTINF:-1 tvg-id="news.us" tvg-chno="4.1" tvg-logo="https://img.example/news.png" group-title="News, Local",Example News` + "\n" +
		"https://streams.example/news/index.m3u8\n" +
		"\n" +
		`#EXTINF:-1 tvg-name="Weather Now",` + "\n" +
		"#EXTVLCOPT:http-user-agent=Something\n" +
		"#EXTGRP:Weather\n" +
		"http://streams.example/weather.ts\n" +
		`#EXTINF:0,No Stream` + "\n" +
		"rtmp://streams.example/live\n" +
		"https://streams.example/orphan.m3u8\n" +
		`#EXTINF:-1 tvg-id="docs",` + "\n" +
		"https://streams.example/docs.m3u8\n"
	got, truncated, err := Parse(strings.NewReader(body))
	if err != nil || truncated {
		t.Fatalf("Parse: %v, truncated %v", err, truncated)
	}
	want := []Entry{
		{Name: "Example News", URL: "https://streams.example/news/index.m3u8", TvgID: "news.us", Number: "4.1", Group: "News, Local", Logo: "https://img.example/news.png"},
		{Name: "Weather Now", URL: "http://streams.example/weather.ts", Group: "Weather"},
		// No name anywhere but a tvg-id: the id is the name.
		{Name: "docs", URL: "https://streams.example/docs.m3u8", TvgID: "docs"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseRefusesWhatIsNotAPlaylist(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"", "<html>Sign in</html>", "https://streams.example/a.m3u8\n"} {
		if _, _, err := Parse(strings.NewReader(body)); !errors.Is(err, ErrNotM3U) {
			t.Errorf("Parse(%q) = %v, want ErrNotM3U", body, err)
		}
	}
}

func TestParseStopsAtTheEntryLimit(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for i := range MaxEntries + 5 {
		fmt.Fprintf(&b, "#EXTINF:-1,Channel %d\nhttps://s.example/%d.m3u8\n", i, i)
	}
	got, truncated, err := Parse(strings.NewReader(b.String()))
	if err != nil || !truncated || len(got) != MaxEntries {
		t.Fatalf("got %d entries, truncated %v, err %v; want %d, true, nil", len(got), truncated, err, MaxEntries)
	}
}

func TestKeyPrefersTheTvgID(t *testing.T) {
	t.Parallel()
	a := Entry{TvgID: "news.us", URL: "https://a.example/1"}
	b := Entry{TvgID: "news.us", URL: "https://b.example/2"}
	if a.Key() != b.Key() {
		t.Errorf("same tvg-id, different keys: %s %s", a.Key(), b.Key())
	}
	c := Entry{URL: "https://a.example/1"}
	d := Entry{URL: "https://a.example/2"}
	if c.Key() == d.Key() || !strings.HasPrefix(c.Key(), "url:") {
		t.Errorf("URL keys: %s %s", c.Key(), d.Key())
	}
}
