package podcasts

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// A synthetic feed in the shapes real ones come in: iTunes and Podcasting 2.0
// namespaces, HTML in CDATA, an item with no enclosure, a duplicate guid, an
// item with no guid, and durations as seconds, MM:SS and HH:MM:SS.
const sampleFeed = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd" xmlns:podcast="https://podcastindex.org/namespace/1.0"
  xmlns:content="http://purl.org/rss/1.0/modules/content/" xmlns:atom="http://www.w3.org/2005/Atom">
<channel>
  <title>Harbor Talk</title>
  <atom:link href="https://feeds.example.com/harbor.xml" rel="self" type="application/rss+xml"/>
  <link>https://harbor.example.com/</link>
  <description><![CDATA[<p>Conversations about <b>lighthouses</b> &amp; tides.</p><p>Weekly.</p>]]></description>
  <language>en-us</language>
  <itunes:author>Mara Quill</itunes:author>
  <itunes:image href="https://cdn.example.com/harbor.jpg"/>
  <itunes:explicit>false</itunes:explicit>
  <item>
    <title>Episode Two: Fog</title>
    <guid isPermaLink="false">harbor-2</guid>
    <pubDate>Tue, 03 Nov 2026 09:00:00 -0500</pubDate>
    <itunes:duration>1:02:03</itunes:duration>
    <itunes:season>1</itunes:season>
    <itunes:episode>2</itunes:episode>
    <itunes:episodeType>full</itunes:episodeType>
    <itunes:explicit>yes</itunes:explicit>
    <itunes:image href="https://cdn.example.com/fog.png"/>
    <podcast:chapters url="https://cdn.example.com/fog-chapters.json" type="application/json+chapters"/>
    <content:encoded><![CDATA[<p>Fog <i>rolls</i> in.<br/>Second line.</p>]]></content:encoded>
    <enclosure url="https://cdn.example.com/fog.mp3" length="12345678" type="audio/mpeg"/>
  </item>
  <item>
    <title>Episode One: Beacons</title>
    <guid>harbor-1</guid>
    <pubDate>Tue, 27 Oct 2026 09:00:00 GMT</pubDate>
    <itunes:duration>754</itunes:duration>
    <description>Plain text description.</description>
    <enclosure url="https://cdn.example.com/beacons.m4a" length="99" type="audio/x-m4a"/>
  </item>
  <item>
    <title>Episode One: Beacons (duplicate)</title>
    <guid>harbor-1</guid>
    <enclosure url="https://cdn.example.com/beacons-again.m4a" type="audio/x-m4a"/>
  </item>
  <item>
    <title>Blog post with no audio</title>
    <guid>harbor-post</guid>
  </item>
  <item>
    <title>Trailer</title>
    <pubDate>Sat, 1 Aug 2026 12:00:00 +0000</pubDate>
    <itunes:duration>02:30</itunes:duration>
    <itunes:episodeType>trailer</itunes:episodeType>
    <enclosure url="https://cdn.example.com/trailer.mp3" type="audio/mpeg"/>
  </item>
  <item>
    <title>Not a web enclosure</title>
    <guid>harbor-ftp</guid>
    <enclosure url="ftp://cdn.example.com/x.mp3" type="audio/mpeg"/>
  </item>
</channel>
</rss>`

func TestParseFeed(t *testing.T) {
	t.Parallel()
	f, err := Parse(strings.NewReader(sampleFeed), 0)
	if err != nil {
		t.Fatal(err)
	}
	if f.Title != "Harbor Talk" || f.Author != "Mara Quill" || f.Link != "https://harbor.example.com/" || f.Language != "en-us" {
		t.Fatalf("channel: %+v", f)
	}
	if f.Image != "https://cdn.example.com/harbor.jpg" || f.Explicit {
		t.Fatalf("image %q explicit %v", f.Image, f.Explicit)
	}
	if f.Description != "Conversations about lighthouses & tides.\nWeekly." {
		t.Fatalf("description %q", f.Description)
	}
	if len(f.Episodes) != 3 {
		t.Fatalf("got %d episodes, want 3 (no audio, duplicate guid and ftp dropped): %+v", len(f.Episodes), f.Episodes)
	}
	two, one, trailer := f.Episodes[0], f.Episodes[1], f.Episodes[2]
	if two.GUID != "harbor-2" || two.Duration != 3723 || two.Season != 1 || two.Number != 2 || !two.Explicit || two.Length != 12345678 ||
		two.Type != "audio/mpeg" || two.Image != "https://cdn.example.com/fog.png" || two.ChaptersURL != "https://cdn.example.com/fog-chapters.json" {
		t.Fatalf("episode two: %+v", two)
	}
	if two.Description != "Fog rolls in.\nSecond line." {
		t.Fatalf("episode two description %q", two.Description)
	}
	if want := time.Date(2026, 11, 3, 14, 0, 0, 0, time.UTC); !two.Published.Equal(want) {
		t.Fatalf("published %v, want %v", two.Published, want)
	}
	if one.GUID != "harbor-1" || one.URL != "https://cdn.example.com/beacons.m4a" || one.Duration != 754 || one.Explicit || one.EpisodeType != "full" {
		t.Fatalf("episode one: %+v", one)
	}
	// No guid: the enclosure URL stands in.
	if trailer.GUID != "https://cdn.example.com/trailer.mp3" || trailer.Duration != 150 || trailer.EpisodeType != "trailer" {
		t.Fatalf("trailer: %+v", trailer)
	}
}

func TestParseFeedKeepsTheNewest(t *testing.T) {
	t.Parallel()
	f, err := Parse(strings.NewReader(sampleFeed), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Episodes) != 2 || f.Episodes[0].GUID != "harbor-2" || f.Episodes[1].GUID != "harbor-1" {
		t.Fatalf("capped episodes: %+v", f.Episodes)
	}
}

func TestParseExplicitChannelMarksEveryEpisode(t *testing.T) {
	t.Parallel()
	feed := strings.Replace(sampleFeed, "<itunes:explicit>false</itunes:explicit>", "<itunes:explicit>true</itunes:explicit>", 1)
	f, err := Parse(strings.NewReader(feed), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Explicit {
		t.Fatal("channel not explicit")
	}
	for _, e := range f.Episodes {
		if !e.Explicit {
			t.Fatalf("%s is not explicit under an explicit channel", e.Title)
		}
	}
}

func TestParseRefusesWhatIsNotAFeed(t *testing.T) {
	t.Parallel()
	for name, doc := range map[string]string{
		"html page":   `<!doctype html><html><head><title>Hi</title></head><body><p>Not a feed</body></html>`,
		"atom":        `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><title>x</title></feed>`,
		"no title":    `<rss version="2.0"><channel><item><enclosure url="https://cdn.example.com/a.mp3"/></item></channel></rss>`,
		"json":        `{"title": "nope"}`,
		"empty":       ``,
		"unsupported": `<?xml version="1.0" encoding="x-made-up"?><rss><channel><title>x</title></channel></rss>`,
	} {
		if _, err := Parse(strings.NewReader(doc), 0); !errors.Is(err, ErrNotAFeed) {
			t.Errorf("%s: err %v, want ErrNotAFeed", name, err)
		}
	}
}

func TestParseDuration(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]int{"": 0, "90": 90, "1:30": 90, "01:01:01": 3661, "12.7": 12, "abc": 0, "-5": 0, "1:2:3:4": 0, "999999999": 0} {
		if got := parseDuration(in); got != want {
			t.Errorf("parseDuration(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseDates(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"Tue, 03 Nov 2026 09:00:00 -0500", "Tue, 3 Nov 2026 09:00:00 EST", "Tuesday, 03 Nov 2026 09:00:00 -0500",
		"2026-11-03T09:00:00-05:00", "03 Nov 2026 09:00:00 -0500"} {
		if _, ok := parseDate(in); !ok {
			t.Errorf("parseDate(%q) failed", in)
		}
	}
	if _, ok := parseDate("sometime last week"); ok {
		t.Error("parsed nonsense as a date")
	}
}

func TestOPMLRoundTrip(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	if err := WriteOPML(&b, "VCRX podcasts", [][2]string{{"Harbor & Tide", "https://feeds.example.com/harbor.xml"}, {"Fog", "https://feeds.example.com/fog.xml"}}); err != nil {
		t.Fatal(err)
	}
	urls, err := ParseOPML(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 || urls[0] != "https://feeds.example.com/harbor.xml" || urls[1] != "https://feeds.example.com/fog.xml" {
		t.Fatalf("round trip: %v from %s", urls, b.String())
	}
	if !strings.Contains(b.String(), `text="Harbor &amp; Tide"`) {
		t.Fatalf("title not escaped: %s", b.String())
	}
	if _, err := ParseOPML(strings.NewReader(`<rss><channel/></rss>`)); err == nil {
		t.Fatal("an RSS document parsed as OPML")
	}
}
