package markers

import "testing"

// A theme at different places in two episodes is found where it is, to the
// second, whatever the frame grid does: offsets of a whole, a quarter and
// half a hop.
func TestAlignFindsSharedThemeOnBothSides(t *testing.T) {
	t.Parallel()
	th := theme(1, 30)
	for _, frac := range []float64{0, 0.25, 0.5} {
		bAt := 12.0 + frac*Hop/SampleRate
		a := Compute(pcm(cat(programme(10, 47), th, programme(11, 120)), 1))
		b := Compute(pcm(cat(programme(20, bAt), th, programme(21, 140)), 2))
		ms := Align(a, b, MsFrame(15_000), 4)
		if len(ms) != 1 {
			t.Fatalf("offset %.2f hop: %d matches, want 1: %+v", frac, len(ms), ms)
		}
		m := ms[0]
		near(t, "start in a", FrameMs(m.AStart), 47_000)
		near(t, "end in a", FrameMs(m.AEnd), 77_000)
		near(t, "start in b", FrameMs(m.BStart), int64(bAt*1000))
		near(t, "end in b", FrameMs(m.BEnd()), int64(bAt*1000)+30_000)
		if m.BER > 0.2 {
			t.Errorf("offset %.2f hop: BER %.3f", frac, m.BER)
		}
		// The same stretch seen from b.
		if s := m.swap(); s.AStart != m.BStart || s.BStart != m.AStart || s.Len() != m.Len() {
			t.Errorf("swap: %+v from %+v", s, m)
		}
	}
}

// Unrelated audio, silence and a steady tone never match, the last two
// however long they are shared.
func TestAlignFindsNothingUnshared(t *testing.T) {
	t.Parallel()
	cases := map[string][2][]float64{
		"unrelated": {programme(30, 300), programme(31, 300)},
		"silence":   {cat(programme(32, 20), silence(60), programme(33, 20)), cat(silence(60), programme(34, 40))},
		"tone":      {cat(programme(35, 20), tone(440, 60)), cat(tone(440, 60), programme(36, 20))},
	}
	for name, c := range cases {
		if ms := Align(Compute(pcm(c[0], 1)), Compute(pcm(c[1], 2)), MsFrame(15_000), 4); len(ms) != 0 {
			t.Errorf("%s: %d matches, want none: %+v", name, len(ms), ms)
		}
	}
}

// Two different shared stretches (a theme, and a scene repeated as a recap)
// both come back, best first.
func TestAlignReturnsEachSharedStretch(t *testing.T) {
	t.Parallel()
	th, scene := theme(1, 30), programme(77, 20)
	a := Compute(pcm(cat(programme(1, 10), th, programme(2, 20), scene, programme(3, 30)), 1))
	b := Compute(pcm(cat(scene, programme(4, 5), th, programme(5, 60)), 2))
	ms := Align(a, b, MsFrame(15_000), 4)
	if len(ms) != 2 {
		t.Fatalf("%d matches, want the theme and the scene: %+v", len(ms), ms)
	}
	near(t, "theme first", FrameMs(ms[0].AStart), 10_000)
	near(t, "scene second", FrameMs(ms[1].AStart), 60_000)
	near(t, "scene in b", FrameMs(ms[1].BStart), 0)
}
