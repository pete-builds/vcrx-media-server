package markers

import "testing"

// A detected intro below ShowIntroConfidence never reaches the player; one
// above it gets a button, and auto skip only at AutoSkipConfidence. Credits
// keep their button however low they score.
func TestForPlayerConfidence(t *testing.T) {
	ms := []Marker{
		{Kind: Intro, StartMs: 0, EndMs: 30000, Confidence: 0.42, Source: SourceDetected},
		{Kind: Credits, StartMs: 3000000, EndMs: 3100000, Confidence: 0.3, Source: SourceDetected},
	}
	got := ForPlayer(ms, ModeAuto)
	if len(got) != 1 || got[0].Kind != Credits || got[0].AutoSkip {
		t.Fatalf("0.42 intro and 0.3 credits: got %+v, want the credits alone, not auto skipped", got)
	}

	for _, c := range []struct {
		conf float64
		auto bool
	}{{0.5, false}, {0.74, false}, {0.75, true}, {1, true}} {
		got := ForPlayer([]Marker{{Kind: Intro, EndMs: 30000, Confidence: c.conf, Source: SourceDetected}}, ModeAuto)
		if len(got) != 1 || got[0].AutoSkip != c.auto {
			t.Errorf("intro at %.2f: got %+v, want one marker with auto_skip %v", c.conf, got, c.auto)
		}
	}

	if got := ForPlayer([]Marker{{Kind: Intro, EndMs: 30000, Confidence: 0.9}}, ModeButton); len(got) != 1 || got[0].AutoSkip {
		t.Errorf("button mode: got %+v, want a button and no auto skip", got)
	}
}
