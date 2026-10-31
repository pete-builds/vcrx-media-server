package markers

import (
	"math"
	"math/cmplx"
	"math/rand/v2"
	"testing"
)

// The FFT agrees with a plain DFT.
func TestFFTMatchesDFT(t *testing.T) {
	const n = 64
	r := rand.New(rand.NewPCG(1, 2))
	re, im := make([]float64, n), make([]float64, n)
	x := make([]complex128, n)
	for i := range re {
		re[i] = r.Float64()*2 - 1
		x[i] = complex(re[i], 0)
	}
	newFFT(n).transform(re, im)
	for k := range n {
		var want complex128
		for i, v := range x {
			want += v * cmplx.Exp(complex(0, -2*math.Pi*float64(k*i)/n))
		}
		if cmplx.Abs(want-complex(re[k], im[k])) > 1e-9 {
			t.Fatalf("bin %d: fft %v, dft %v", k, complex(re[k], im[k]), want)
		}
	}
}

func TestFrameTimes(t *testing.T) {
	for _, ms := range []int64{0, 46, 1000, 47_000, 599_999} {
		if got := FrameMs(MsFrame(ms)); got-ms > 50 || ms-got > 50 {
			t.Errorf("FrameMs(MsFrame(%d)) = %d", ms, got)
		}
	}
	if n := Compute(make([]int16, FrameSize-1)).Len(); n != 0 {
		t.Errorf("less than a frame gave %d frames", n)
	}
}

// Music is valid; digital silence and a steady tone are not, so they can
// never make a match however long they last.
func TestValidFrames(t *testing.T) {
	share := func(fp Fingerprint) float64 {
		n := 0
		for _, v := range fp.Valid {
			if v {
				n++
			}
		}
		return float64(n) / float64(fp.Len())
	}
	if v := share(Compute(pcm(theme(1, 20), 1))); v < 0.95 {
		t.Errorf("theme: only %.2f of frames valid", v)
	}
	for name, buf := range map[string][]float64{"silence": silence(20), "tone": tone(440, 20)} {
		fp := Compute(pcm(buf, 1))
		if v := share(fp); v != 0 {
			t.Errorf("%s: %.2f of frames valid, want none", name, v)
		}
	}
	fp := Compute(pcm(cat(theme(1, 20), silence(6)), 1))
	if s := fp.TrailingSilent; s < MsFrame(5500) || s > MsFrame(6500) {
		t.Errorf("trailing silence %d frames, want about %d", s, MsFrame(6000))
	}
}

// The same audio fingerprints the same way at any loudness.
func TestLoudnessDoesNotMatter(t *testing.T) {
	th := theme(3, 20)
	quiet := make([]float64, len(th))
	for i, v := range th {
		quiet[i] = v * 0.2
	}
	a, b := Compute(pcm(th, 1)), Compute(pcm(quiet, 2))
	diff := 0
	for i := range a.Hashes {
		diff += errBits(a, b, i, i)
	}
	if ber := float64(diff) / float64(32*a.Len()); ber > 0.15 {
		t.Errorf("14 dB quieter: %.2f of bits differ", ber)
	}
}
