// Package markers finds the intro and the end credits of TV episodes, and
// stores those markers, so players can offer Skip Intro and Skip Credits.
//
// A season's episodes share their theme song and usually their credits music.
// So the start of each episode is compared with the start of its neighbours,
// and the end with the end, by audio fingerprint: a stretch of audio that
// appears in several episodes is the intro (or the credits). Chapter titles
// and Plex-style markers in an NFO win over detection, and an admin's own
// markers win over everything.
//
// The fingerprint needs no chromaprint (Homebrew's and Debian's ffmpeg may not
// have it). ffmpeg decodes mono 11025 Hz PCM and this package does the rest.
//
// # Fingerprint
//
// The audio is cut into frames of 2048 samples (186 ms) every 1024 samples
// (93 ms). Each frame is windowed (Hann) and transformed (FFT), and its power
// is summed into 33 bands spaced evenly in pitch between 300 and 3500 Hz,
// where music carries most of its identity, and taken as a logarithm, with
// any band more than 30 dB under the frame's loudest raised to that floor
// (near-empty bands then read as equal instead of as noise that flips bits
// between two encodes of the same audio). Each frame then gets 32 bits, one
// per neighbouring pair of bands: bit b is set when the difference between
// bands b and b+1 grew since the previous frame. That is the hash Haitsma and
// Kalker published for audio identification ("A Highly Robust Audio
// Fingerprinting System", 2002). It ignores loudness and survives lossy
// re-encoding, because it records which way the spectrum moves, not how loud
// it is.
//
// Two kinds of frame carry no identity and are marked invalid: near-silence
// (digital silence hashes to all zeros, so two silent stretches would match
// perfectly) and frames whose spectrum barely changed (a steady tone hashes
// to noise in its tiny fluctuations, which can still repeat exactly between
// synthetic files). An invalid frame counts as a coin toss when comparing.
//
// # Matching
//
// See align.go.
package markers

import (
	"math"
	"math/bits"
)

const (
	// SampleRate is what ffmpeg is asked to decode to.
	SampleRate = 11025
	// FrameSize and Hop are in samples: 186 ms frames every 93 ms.
	FrameSize = 2048
	Hop       = 1024

	numBands = 33
	lowHz    = 300.0
	highHz   = 3500.0

	// silenceRMS is about -50 dBFS: quieter frames are treated as silence.
	silenceRMS = 0.003
	// minFlux is the least average change in log band power between frames
	// (about 0.4 dB) for a frame to count. Music changes far more than this;
	// a steady tone or a constant hum far less.
	minFlux = 0.05
	// floorRatio is 30 dB: weaker bands are raised to this under the loudest.
	floorRatio = 1e-3
)

// Fingerprint is one hash per frame, with frames that carry no identity
// marked invalid.
type Fingerprint struct {
	Hashes []uint32
	Valid  []bool
	// TrailingSilent counts the silent frames at the end, for spotting dead
	// air after the credits.
	TrailingSilent int
}

// Len is the number of frames.
func (f Fingerprint) Len() int { return len(f.Hashes) }

// FrameMs converts a frame index to milliseconds from the start of the
// decoded audio: the middle of the frame, where its hash is centred.
func FrameMs(i int) int64 {
	return int64((float64(i)*Hop + FrameSize/2) * 1000 / SampleRate)
}

// MsFrame is the frame whose middle is nearest ms.
func MsFrame(ms int64) int {
	return int(math.Round((float64(ms)*SampleRate/1000 - FrameSize/2) / Hop))
}

// Compute fingerprints mono samples at SampleRate.
func Compute(samples []int16) Fingerprint {
	n := 0
	if len(samples) >= FrameSize {
		n = (len(samples)-FrameSize)/Hop + 1
	}
	fp := Fingerprint{Hashes: make([]uint32, n), Valid: make([]bool, n)}
	if n == 0 {
		return fp
	}
	ft := newFFT(FrameSize)
	edges := bandEdges()
	win := hann(FrameSize)
	re := make([]float64, FrameSize)
	im := make([]float64, FrameSize)
	prev := make([]float64, numBands)
	cur := make([]float64, numBands)
	prevLoud := false
	for i := range n {
		off := i * Hop
		var sq float64
		for k := range FrameSize {
			x := float64(samples[off+k]) / 32768
			sq += x * x
			re[k] = x * win[k]
			im[k] = 0
		}
		loud := math.Sqrt(sq/FrameSize) >= silenceRMS
		ft.transform(re, im)
		for b := range numBands {
			var e float64
			for k := edges[b]; k < edges[b+1]; k++ {
				e += re[k]*re[k] + im[k]*im[k]
			}
			cur[b] = e
		}
		// Log power with a floor 30 dB under the loudest band: bands with
		// next to nothing in them read as equal, so faint noise there cannot
		// flip their bits.
		peak := 0.0
		for _, e := range cur {
			peak = max(peak, e)
		}
		for b, e := range cur {
			cur[b] = math.Log(max(e, peak*floorRatio, 1e-12))
		}
		if i > 0 {
			var h uint32
			var diff float64
			for b := range numBands - 1 {
				if (cur[b]-cur[b+1])-(prev[b]-prev[b+1]) > 0 {
					h |= 1 << uint(b)
				}
			}
			for b := range numBands {
				diff += math.Abs(cur[b] - prev[b])
			}
			fp.Hashes[i] = h
			fp.Valid[i] = loud && prevLoud && diff/numBands >= minFlux
		}
		prev, cur = cur, prev
		prevLoud = loud
		if loud {
			fp.TrailingSilent = 0
		} else {
			fp.TrailingSilent++
		}
	}
	return fp
}

// errBits is how many of a pair's 32 bits differ; an invalid frame on either
// side counts as half, what two unrelated frames average.
func errBits(a, b Fingerprint, i, j int) int {
	if !a.Valid[i] || !b.Valid[j] {
		return 16
	}
	return bits.OnesCount32(a.Hashes[i] ^ b.Hashes[j])
}

// bandEdges are FFT bin boundaries for numBands bands evenly spaced in pitch.
func bandEdges() []int {
	edges := make([]int, numBands+1)
	binHz := float64(SampleRate) / FrameSize
	for b := range numBands + 1 {
		hz := lowHz * math.Pow(highHz/lowHz, float64(b)/numBands)
		edges[b] = int(math.Round(hz / binHz))
	}
	// Every band at least one bin wide.
	for b := 1; b <= numBands; b++ {
		if edges[b] <= edges[b-1] {
			edges[b] = edges[b-1] + 1
		}
	}
	return edges
}

func hann(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	return w
}

// fft is an in-place radix-2 transform with its tables computed once.
type fft struct {
	n        int
	rev      []int
	cos, sin []float64
}

func newFFT(n int) *fft {
	f := &fft{n: n, rev: make([]int, n), cos: make([]float64, n/2), sin: make([]float64, n/2)}
	shift := bits.LeadingZeros(uint(n)) + 1
	for i := range n {
		f.rev[i] = int(bits.Reverse(uint(i)) >> uint(shift))
	}
	for i := range n / 2 {
		a := -2 * math.Pi * float64(i) / float64(n)
		f.cos[i], f.sin[i] = math.Cos(a), math.Sin(a)
	}
	return f
}

func (f *fft) transform(re, im []float64) {
	for i, j := range f.rev {
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= f.n; size <<= 1 {
		half, step := size/2, f.n/size
		for start := 0; start < f.n; start += size {
			for k := range half {
				c, s := f.cos[k*step], f.sin[k*step]
				a, b := start+k, start+k+half
				tr := re[b]*c - im[b]*s
				ti := re[b]*s + im[b]*c
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a] += tr
				im[a] += ti
			}
		}
	}
}
