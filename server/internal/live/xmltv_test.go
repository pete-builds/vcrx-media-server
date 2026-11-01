package live

import (
	"bytes"
	"compress/gzip"
	"errors"
	"strings"
	"testing"
	"time"
)

// guideXML is in ISO-8859-1, as many grabbers write it: "Café" is one byte é.
var guideXML = "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n<!DOCTYPE tv SYSTEM \"xmltv.dtd\">\n" +
	"<tv generator-info-name=\"test\">\n" +
	"  <channel id=\"news.us\"><display-name>Example News</display-name><display-name>4.1</display-name><icon src=\"x\"/></channel>\n" +
	"  <channel id=\"cooking\"><display-name>Caf\xe9 Kitchen</display-name></channel>\n" +
	"  <programme start=\"20261101200000 -0400\" stop=\"20261101210000 -0400\" channel=\"news.us\">\n" +
	"    <title lang=\"en\">Evening News</title><sub-title>Late Edition</sub-title><desc>Headlines.</desc>\n" +
	"    <category>News</category><category>Talk</category>\n" +
	"    <episode-num system=\"xmltv_ns\">1.4.0/1</episode-num>\n" +
	"  </programme>\n" +
	"  <programme start=\"202611020100\" stop=\"202611020130\" channel=\"cooking\">\n" +
	"    <title>Caf\xe9 Live</title><episode-num system=\"onscreen\">Ep. 12</episode-num><episode-num system=\"xmltv_ns\">.11.</episode-num>\n" +
	"  </programme>\n" +
	"  <programme start=\"20261102010000 +0000\" stop=\"20261102010000 +0000\" channel=\"cooking\"><title>Zero length</title></programme>\n" +
	"  <programme start=\"bad\" stop=\"20261102010000\" channel=\"cooking\"><title>Bad time</title></programme>\n" +
	"  <programme start=\"20261102010000\" stop=\"20261102020000\" channel=\"cooking\"><title></title></programme>\n" +
	"</tv>\n"

func parseAll(t *testing.T, body []byte) ([]GuideChannel, []Programme) {
	t.Helper()
	var chans []GuideChannel
	var progs []Programme
	err := ParseXMLTV(bytes.NewReader(body), func(c GuideChannel) { chans = append(chans, c) }, func(p Programme) error {
		progs = append(progs, p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return chans, progs
}

func TestParseXMLTV(t *testing.T) {
	t.Parallel()
	for name, body := range map[string][]byte{"plain": []byte(guideXML), "gzipped": gz(t, guideXML)} {
		chans, progs := parseAll(t, body)
		if len(chans) != 2 || chans[0].ID != "news.us" || strings.Join(chans[0].Names, "|") != "Example News|4.1" || chans[1].Names[0] != "Café Kitchen" {
			t.Fatalf("%s: channels %+v", name, chans)
		}
		if len(progs) != 2 {
			t.Fatalf("%s: %d programmes %+v, want the two valid ones", name, len(progs), progs)
		}
		news := progs[0]
		if news.Channel != "news.us" || news.Title != "Evening News" || news.SubTitle != "Late Edition" || news.Desc != "Headlines." ||
			strings.Join(news.Categories, ",") != "News,Talk" || news.Episode != "S02E05" {
			t.Errorf("%s: news %+v", name, news)
		}
		if !news.Start.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) || news.Stop.Sub(news.Start) != time.Hour {
			t.Errorf("%s: news times %v to %v", name, news.Start, news.Stop)
		}
		cafe := progs[1]
		if cafe.Title != "Café Live" || cafe.Episode != "Ep. 12" || !cafe.Start.Equal(time.Date(2026, 11, 2, 1, 0, 0, 0, time.UTC)) {
			t.Errorf("%s: cafe %+v", name, cafe)
		}
	}
}

func TestParseXMLTVRefusesWhatIsNotAGuide(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"", "<html><body>Sign in</body></html>", "#EXTM3U\n", "not xml at all"} {
		err := ParseXMLTV(strings.NewReader(body), nil, func(Programme) error { return nil })
		if !errors.Is(err, ErrNotXMLTV) {
			t.Errorf("ParseXMLTV(%q) = %v, want ErrNotXMLTV", body, err)
		}
	}
}

func TestParseXMLTVStopsWhenTold(t *testing.T) {
	t.Parallel()
	stop := errors.New("enough")
	n := 0
	err := ParseXMLTV(strings.NewReader(guideXML), nil, func(Programme) error { n++; return stop })
	if !errors.Is(err, stop) || n != 1 {
		t.Errorf("err %v after %d programmes", err, n)
	}
}

func TestEpisodeNumbers(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"0.4.0/1": "S01E05", "2 . 9 . ": "S03E10", ".11.": "E12", "x.y": "", "": ""} {
		if got := episode([]xmlEpisode{{System: "xmltv_ns", Value: in}}); got != want {
			t.Errorf("xmltv_ns %q: %q, want %q", in, got, want)
		}
	}
}

func TestClipKeepsWholeCharacters(t *testing.T) {
	t.Parallel()
	if got := clipTo("aé", 2); got != "a" {
		t.Errorf("clipTo split a character: %q", got)
	}
}

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
