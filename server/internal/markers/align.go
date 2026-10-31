package markers

import (
	"math/bits"
	"sort"
)

// Matching two fingerprints
//
// Shared audio sits at a different time in each episode (a cold open of a
// different length comes first), so every offset between the two is tried:
// for each offset the frames that line up are compared bit by bit. Unrelated
// audio differs in about half of its bits; the same audio, even re-encoded,
// in far fewer. A frame is "matched" when the 64 frames (6 s) centred on it
// differ in at most matchBER of their bits, and a run of matched frames at
// least minFrames long is a shared stretch. Its edges are then moved to where
// the 6 s average crosses halfway between the stretch's own error rate and
// unrelated audio's 0.5: where half the window lies inside the shared audio,
// whatever the encoding did to the bits.
//
// Tuning (align_test.go and job_test.go hold the cases): a theme re-encoded
// separately, at any offset against the frame grid, differs in 0.04 to 0.17
// of its bits; the lowest 6 s average between unrelated synthetic programmes
// over every offset of two 400 s clips was 0.375. matchBER sits between, at
// 0.27. A 3 s window let unrelated audio down to 0.32, too close. Edges land
// within 0.3 s of the truth in the tests (the brief asks for 1 s).
//
// Every offset costs one pass over the overlap: a pair of 10 minute windows
// (6,460 frames each) is about 42 million 32 bit comparisons.

const (
	// window is the number of frames averaged when judging a frame (~6 s).
	window = 64
	// matchBER is the most bits a window may differ in, as a fraction, and
	// still count as the same audio. See the tuning notes above.
	matchBER = 0.27
	// randomBER is what unrelated audio averages.
	randomBER = 0.5
)

// Match is a stretch of audio found in both fingerprints: frames [AStart,
// AEnd) of a line up with frames from BStart of b.
type Match struct {
	AStart, AEnd int
	BStart       int
	// BER is the fraction of bits that differ inside the stretch.
	BER float64
}

// Len is the length in frames.
func (m Match) Len() int { return m.AEnd - m.AStart }

// BEnd is the end of the stretch in b.
func (m Match) BEnd() int { return m.BStart + m.Len() }

// swap describes the same stretch from b's side.
func (m Match) swap() Match {
	return Match{AStart: m.BStart, AEnd: m.BEnd(), BStart: m.AStart, BER: m.BER}
}

// Align finds the stretches of at least minFrames frames that a and b share,
// best first: longest and cleanest. Copies of one stretch found at
// neighbouring offsets are dropped, and at most limit are returned.
func Align(a, b Fingerprint, minFrames, limit int) []Match {
	n, m := a.Len(), b.Len()
	if minFrames < window {
		minFrames = window
	}
	var found []Match
	sums := make([]int, max(n, m)+1)
	// A window whose bits differ this little somewhere on a diagonal is worth
	// a closer look; most diagonals have none and cost one pass.
	ber := matchBER // a variable: the constant product is not a whole number
	quick := int(float64(32*window) * ber)
	for d := -(n - 1); d <= m-1; d++ {
		i0, i1 := max(0, -d), min(n, m-d)
		l := i1 - i0
		if l < minFrames {
			continue
		}
		ha, hb := a.Hashes[i0:i1], b.Hashes[i0+d:i1+d]
		va, vb := a.Valid[i0:i1], b.Valid[i0+d:i1+d]
		s := sums[:l+1]
		total := 0
		for k := range ha {
			e := 16
			if va[k] && vb[k] {
				e = bits.OnesCount32(ha[k] ^ hb[k])
			}
			total += e
			s[k+1] = total
		}
		close := false
		for k := window; k <= l; k++ {
			if s[k]-s[k-window] <= quick {
				close = true
				break
			}
		}
		if !close {
			continue
		}
		for _, r := range runs(s, matchBER, minFrames) {
			r = refine(s, r)
			if r[1]-r[0] < minFrames {
				continue
			}
			found = append(found, Match{AStart: i0 + r[0], AEnd: i0 + r[1], BStart: i0 + r[0] + d,
				BER: float64(s[r[1]]-s[r[0]]) / float64(32*(r[1]-r[0]))})
		}
	}
	return best(found, limit)
}

// mean is the error rate of the window centred on frame k of a diagonal whose
// prefix sums are sums, clipped to the diagonal.
func mean(sums []int, k int) float64 {
	lo, hi := max(0, k-window/2), min(len(sums)-1, k+window/2)
	if hi <= lo {
		return randomBER
	}
	return float64(sums[hi]-sums[lo]) / float64(32*(hi-lo))
}

// runs lists [start, end) ranges of frames whose window error rate is at most
// limit, at least minLen long.
func runs(sums []int, limit float64, minLen int) [][2]int {
	var out [][2]int
	n := len(sums) - 1
	start := -1
	for k := 0; k <= n; k++ {
		in := k < n && mean(sums, k) <= limit
		switch {
		case in && start < 0:
			start = k
		case !in && start >= 0:
			if k-start >= minLen {
				out = append(out, [2]int{start, k})
			}
			start = -1
		}
	}
	return out
}

// refine moves a run's edges to where the window error rate crosses halfway
// between the run's own rate and unrelated audio's: where half of the window
// lies inside the shared audio.
func refine(sums []int, r [2]int) [2]int {
	n := len(sums) - 1
	inner := float64(sums[r[1]]-sums[r[0]]) / float64(32*(r[1]-r[0]))
	limit := (inner + randomBER) / 2
	s, e := r[0], r[1]
	for s > 0 && mean(sums, s-1) <= limit {
		s--
	}
	for s < e && mean(sums, s) > limit {
		s++
	}
	for e < n && mean(sums, e) <= limit {
		e++
	}
	for e > s && mean(sums, e-1) > limit {
		e--
	}
	return [2]int{s, e}
}

// best keeps the strongest matches, dropping any that repeats a kept one: the
// same stretch seen again at a neighbouring offset overlaps it on both sides.
func best(found []Match, limit int) []Match {
	score := func(m Match) float64 { return float64(m.Len()) * (randomBER - m.BER) }
	sort.SliceStable(found, func(i, j int) bool { return score(found[i]) > score(found[j]) })
	var kept []Match
	for _, c := range found {
		dup := false
		for _, k := range kept {
			if overlap(c.AStart, c.AEnd, k.AStart, k.AEnd) > 0.5 && overlap(c.BStart, c.BEnd(), k.BStart, k.BEnd()) > 0.5 {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, c)
			if len(kept) == limit {
				break
			}
		}
	}
	return kept
}

// overlap is the shared length of two ranges over the shorter one's length.
func overlap(s1, e1, s2, e2 int) float64 {
	shared := min(e1, e2) - max(s1, s2)
	shorter := min(e1-s1, e2-s2)
	if shared <= 0 || shorter <= 0 {
		return 0
	}
	return float64(shared) / float64(shorter)
}
