package subtitles

import (
	"strings"
	"testing"
)

const sample = `WEBVTT
X-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000

NOTE a note --> with an arrow stays a note

STYLE
::cue { color: yellow }

1
00:00:00.200 --> 00:00:00.400
Gone with a big negative shift

2
00:00:00.500 --> 00:00:02.000 line:90% align:center
Straddles zero

00:00:59.900 --> 00:01:01.250
No identifier, crosses a minute

01:59:59.999 --> 02:00:00.500
Crosses an hour
`

func TestShiftVTTLater(t *testing.T) {
	got := string(ShiftVTT([]byte(sample), 1500))
	for _, want := range []string{
		"00:00:01.700 --> 00:00:01.900\nGone with a big negative shift",
		"00:00:02.000 --> 00:00:03.500 line:90% align:center\nStraddles zero",
		"00:01:01.400 --> 00:01:02.750\nNo identifier",
		"02:00:01.499 --> 02:00:02.000\nCrosses an hour",
		"NOTE a note --> with an arrow stays a note",
		"X-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000",
		"STYLE\n::cue { color: yellow }",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if !strings.HasPrefix(got, "WEBVTT\n") {
		t.Errorf("header lost:\n%s", got)
	}
}

// A negative offset shows subtitles earlier. A cue that would end at or
// before zero is dropped with its identifier; one that starts before zero
// but ends after it starts at zero.
func TestShiftVTTEarlierDropsAndClamps(t *testing.T) {
	got := string(ShiftVTT([]byte(sample), -600))
	if strings.Contains(got, "Gone with a big negative shift") || strings.Contains(got, "\n1\n") {
		t.Errorf("cue ending before zero was kept:\n%s", got)
	}
	for _, want := range []string{
		"2\n00:00:00.000 --> 00:00:01.400 line:90% align:center\nStraddles zero",
		"00:00:59.300 --> 00:01:00.650\n",
		"01:59:59.399 --> 01:59:59.900\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	// Ending exactly at zero is gone too.
	exact := string(ShiftVTT([]byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nx\n"), -2000))
	if strings.Contains(exact, "x") {
		t.Errorf("cue ending exactly at zero was kept:\n%s", exact)
	}
}

func TestShiftVTTEdgeCases(t *testing.T) {
	if got := ShiftVTT([]byte(sample), 0); string(got) != sample {
		t.Error("zero offset changed the file")
	}
	// Short mm:ss.ttt timestamps and CRLF line endings, as some tools write.
	crlf := "WEBVTT\r\n\r\n01:02.003 --> 01:04.000\r\nshort form\r\n"
	if got := string(ShiftVTT([]byte(crlf), 250)); !strings.Contains(got, "00:01:02.253 --> 00:01:04.250\nshort form") {
		t.Errorf("short form:\n%q", got)
	}
	// A timing line that does not parse passes through untouched.
	odd := "WEBVTT\n\n00:00:01,000 --> 00:00:02,000\ncommas are SubRip\n"
	if got := string(ShiftVTT([]byte(odd), 1000)); !strings.Contains(got, "00:00:01,000 --> 00:00:02,000") {
		t.Errorf("unparseable line changed:\n%s", got)
	}
	for _, bad := range []string{"1:00.000", "00:60.000", "00:00:00.1000", "aa:00.000", "00:00:-1.000"} {
		if _, ok := parseTimestamp(bad); ok {
			t.Errorf("parseTimestamp(%q) accepted", bad)
		}
	}
	if ms, ok := parseTimestamp("100:00:00.001"); !ok || ms != 360_000_001 {
		t.Errorf("three-digit hours: %d %v", ms, ok)
	}
}
