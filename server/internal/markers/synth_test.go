package markers

import (
	"math"
	"math/rand/v2"
)

// Synthetic audio for the tests: a "theme" that repeats exactly, and
// "programme" audio that never repeats. Both are note sequences with
// harmonics and decaying envelopes, plus a little noise, so their spectra
// move the way music and speech do.

func secs(s float64) int { return int(s * SampleRate) }

// note adds a plucked note with four harmonics at sample offset at.
func note(buf []float64, at int, hz, durS, amp float64) {
	n := min(secs(durS), len(buf)-at)
	for k := range n {
		t := float64(k) / SampleRate
		env := math.Exp(-3*t) * math.Min(1, t/0.01)
		var v float64
		for h := 1.0; h <= 4; h++ {
			v += math.Sin(2*math.Pi*hz*h*t) / h
		}
		buf[at+k] += amp * env * v
	}
}

// theme is a fixed melody over a chord that swells and fades: the same
// samples every time it is called with the same seed.
func theme(seed uint64, durS float64) []float64 {
	r := rand.New(rand.NewPCG(seed, 7))
	buf := make([]float64, secs(durS))
	scale := []float64{220, 247, 262, 294, 330, 349, 392, 440, 494, 523, 587, 659}
	for at := 0; at < len(buf); {
		d := 0.2 + 0.1*float64(r.IntN(4))
		note(buf, at, scale[r.IntN(len(scale))], d*2, 0.25)
		if r.IntN(3) == 0 {
			note(buf, at, scale[r.IntN(len(scale))]/2, d*3, 0.15)
		}
		at += secs(d)
	}
	return buf
}

// programme is audio that never repeats: random notes, random noise bursts.
func programme(seed uint64, durS float64) []float64 {
	r := rand.New(rand.NewPCG(seed, 99))
	buf := make([]float64, secs(durS))
	for at := 0; at < len(buf); {
		d := 0.1 + 0.4*r.Float64()
		if r.IntN(2) == 0 {
			note(buf, at, 150+900*r.Float64(), d*2, 0.2)
		} else {
			n := min(secs(d), len(buf)-at)
			lp := 0.0
			for k := range n {
				lp += 0.3 * (r.NormFloat64() - lp)
				buf[at+k] += 0.15 * lp * math.Sin(math.Pi*float64(k)/float64(n))
			}
		}
		at += secs(d)
	}
	return buf
}

// silence is digital silence.
func silence(durS float64) []float64 { return make([]float64, secs(durS)) }

// tone is a steady sine, the dev library's sample-clip audio.
func tone(hz, durS float64) []float64 {
	buf := make([]float64, secs(durS))
	for k := range buf {
		buf[k] = 0.3 * math.Sin(2*math.Pi*hz*float64(k)/SampleRate)
	}
	return buf
}

func cat(parts ...[]float64) []float64 {
	var out []float64
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// pcm converts to 16 bit, adding faint noise unique to seed so no two
// episodes are bit-identical even where they share audio, as after lossy
// encoding.
func pcm(buf []float64, seed uint64) []int16 {
	r := rand.New(rand.NewPCG(seed, 3))
	out := make([]int16, len(buf))
	for i, v := range buf {
		v += 0.002 * r.NormFloat64()
		out[i] = int16(max(-32767, min(32767, v*32767)))
	}
	return out
}
