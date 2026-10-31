package markers

import "testing"

func clipsOf(durS float64, eps ...[]float64) []Clip {
	cs := make([]Clip, len(eps))
	for i, e := range eps {
		cs[i] = Clip{FP: Compute(pcm(e, uint64(i+1))), DurationMs: int64(durS * 1000)}
	}
	return cs
}

// A recap shares a scene with one episode only; the theme is in all of them.
// The theme wins, after a cold open of a different length in each episode.
func TestDetectPrefersThemeOverRecap(t *testing.T) {
	th := theme(1, 30)
	ep1Scene := programme(100, 20)
	clips := clipsOf(400,
		cat(programme(1, 8), th, ep1Scene, programme(2, 60)),
		cat(ep1Scene, programme(3, 4), th, programme(4, 60)), // recap of episode 1, then the theme
		cat(programme(5, 25), th, programme(6, 60)),
		cat(th, programme(7, 80)),
	)
	found := Detect(clips, []int{0, 1, 2, 3}, introRules)
	want := []int64{8_000, 24_000, 25_000, 0}
	for i, at := range want {
		f, ok := found[i]
		if !ok {
			t.Errorf("episode %d: nothing found", i+1)
			continue
		}
		near(t, "intro start", f.StartMs, at)
		near(t, "intro end", f.EndMs, at+30_000)
	}
}

// One partner agreeing is not enough when there are several to ask: a scene
// shared by two episodes of four is not an intro.
func TestDetectNeedsAgreement(t *testing.T) {
	scene := programme(100, 30)
	clips := clipsOf(400,
		cat(programme(1, 10), scene, programme(2, 60)),
		cat(scene, programme(3, 70)),
		cat(programme(4, 100)),
		cat(programme(5, 100)),
	)
	if found := Detect(clips, []int{0, 1, 2, 3}, introRules); len(found) != 0 {
		t.Errorf("found %+v from a scene two episodes of four share", found)
	}
	// A season of one has nobody to compare with.
	if found := Detect(clipsOf(400, cat(programme(1, 10), theme(1, 30), programme(2, 60))), []int{0}, introRules); len(found) != 0 {
		t.Errorf("season of one: %+v", found)
	}
}

// The intro must be in the first half and credits in the second, and neither
// may be too short or too long.
func TestPlausible(t *testing.T) {
	c := Clip{DurationMs: 1_200_000}
	cases := []struct {
		start, end int64
		r          Rules
		want       bool
	}{
		{60_000, 120_000, introRules, true},
		{60_000, 70_000, introRules, false},   // 10 s: too short for a theme
		{60_000, 400_000, introRules, false},  // nearly 6 minutes
		{700_000, 760_000, introRules, false}, // second half
		{1_100_000, 1_190_000, creditsRules, true},
		{100_000, 160_000, creditsRules, false}, // first half
	}
	for _, tc := range cases {
		if got := plausible(tc.start, tc.end, c, tc.r); got != tc.want {
			t.Errorf("plausible(%d, %d, credits=%v) = %v", tc.start, tc.end, tc.r.Credits, got)
		}
	}
}
