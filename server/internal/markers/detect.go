package markers

import (
	"math"
	"sort"
)

// Finding a season's intro (or credits) from pairs of episodes
//
// Each episode is compared with up to Partners of its neighbours in the
// season. Every pair yields a few shared stretches (Align). A theme song
// turns up in nearly every pair, at the same place in this episode; a recap
// ("previously on") shares a scene with one earlier episode only. So the
// stretches are grouped by where they sit in this episode, and the group seen
// with the most partners wins, as long as at least half of the partners (and
// at least two of them, when there are two or more) agree. Its edges are the
// medians of the group's edges.
//
// With a single partner (a season of two) one clean match is accepted. A
// season of one has nothing to compare and gets no detected markers.
//
// False positives are worse than misses: a Skip Intro button over the opening
// scene teaches people to distrust it. So a stretch must be long enough to be
// a theme (MinMs), short enough not to be the episode itself (MaxMs), and in
// the right half of the episode; anything doubtful is left unmarked.

// Clip is the fingerprint of one window of one episode.
type Clip struct {
	FP Fingerprint
	// OffsetMs is where the window starts in the file.
	OffsetMs int64
	// DurationMs is the whole episode's length, 0 when unknown.
	DurationMs int64
}

// Rules bound what counts as an intro or credits.
type Rules struct {
	MinMs, MaxMs int64
	// Partners is how many neighbours each episode is compared with.
	Partners int
	// Credits: the stretch must start in the second half of the episode
	// (otherwise the first half).
	Credits bool
}

// Found is a detected stretch of one episode, in file time.
type Found struct {
	StartMs, EndMs int64
	Confidence     float64
}

// Detect finds the shared stretch for each of targets (indexes into clips),
// comparing it with its neighbours. Episodes with nothing found are absent
// from the result.
func Detect(clips []Clip, targets []int, r Rules) map[int]Found {
	if r.Partners <= 0 {
		r.Partners = 4
	}
	minFrames := int(r.MinMs * SampleRate / Hop / 1000)
	pairs := map[[2]int][]Match{}
	var matches func(i, j int) []Match
	matches = func(i, j int) []Match {
		if i < j {
			if m, ok := pairs[[2]int{i, j}]; ok {
				return m
			}
			m := Align(clips[i].FP, clips[j].FP, minFrames, 4)
			pairs[[2]int{i, j}] = m
			return m
		}
		ms := matches(j, i)
		out := make([]Match, len(ms))
		for k, m := range ms {
			out[k] = m.swap()
		}
		return out
	}
	out := map[int]Found{}
	for _, t := range targets {
		if t < 0 || t >= len(clips) || clips[t].FP.Len() == 0 {
			continue
		}
		partners := neighbours(clips, t, r.Partners)
		var cands []cand
		for pi, p := range partners {
			for _, m := range matches(t, p) {
				cands = append(cands, cand{partner: pi, start: m.AStart, end: m.AEnd, ber: m.BER})
			}
		}
		if f, ok := choose(cands, len(partners), clips[t], r); ok {
			out[t] = f
		}
	}
	return out
}

type cand struct {
	partner    int
	start, end int
	ber        float64
}

// neighbours picks up to n other episodes nearest t in season order, after
// then before, skipping any without audio.
func neighbours(clips []Clip, t, n int) []int {
	var out []int
	for step := 1; len(out) < n && (t-step >= 0 || t+step < len(clips)); step++ {
		for _, i := range []int{t + step, t - step} {
			if i >= 0 && i < len(clips) && clips[i].FP.Len() > 0 && len(out) < n {
				out = append(out, i)
			}
		}
	}
	return out
}

// choose groups the candidates by where they sit and returns the best
// supported group's median edges, or false when nothing is convincing.
func choose(all []cand, partners int, clip Clip, r Rules) (Found, bool) {
	// Only stretches that could be the thing sought take part: an intro in
	// the first half, credits in the second, of a plausible length. On a
	// short episode the two windows overlap, and the theme must not outvote
	// the credits there (or the other way round).
	var cands []cand
	for _, c := range all {
		if plausible(clip.OffsetMs+FrameMs(c.start), clip.OffsetMs+FrameMs(c.end), clip, r) {
			cands = append(cands, c)
		}
	}
	if partners == 0 || len(cands) == 0 {
		return Found{}, false
	}
	need := max(2, (partners+1)/2)
	if partners == 1 {
		need = 1
	}
	var best []cand
	bestBER := 1.0
	for _, c := range cands {
		// One member per partner: the one that overlaps c the most.
		group := map[int]cand{}
		for _, o := range cands {
			if iou(c, o) < 0.5 {
				continue
			}
			if g, ok := group[o.partner]; !ok || iou(c, o) > iou(c, g) {
				group[o.partner] = o
			}
		}
		members := make([]cand, 0, len(group))
		var ber float64
		for _, g := range group {
			members = append(members, g)
			ber += g.ber
		}
		ber /= float64(len(members))
		if len(members) > len(best) || (len(members) == len(best) && ber < bestBER) {
			best, bestBER = members, ber
		}
	}
	if len(best) < need {
		return Found{}, false
	}
	// A lone partner has nobody to agree with, so its match must be clean.
	if partners == 1 && bestBER > 0.2 {
		return Found{}, false
	}
	starts := make([]int, len(best))
	ends := make([]int, len(best))
	for i, c := range best {
		starts[i], ends[i] = c.start, c.end
	}
	f := Found{
		StartMs: clip.OffsetMs + FrameMs(median(starts)),
		EndMs:   clip.OffsetMs + FrameMs(median(ends)),
	}
	if f.StartMs < clip.OffsetMs {
		f.StartMs = clip.OffsetMs
	}
	if d := clip.DurationMs; d > 0 && f.EndMs > d {
		f.EndMs = d
	}
	if !plausible(f.StartMs, f.EndMs, clip, r) {
		return Found{}, false
	}
	support := float64(len(best)) / float64(partners)
	f.Confidence = math.Round(100*support*math.Max(0, 1-2*bestBER)) / 100
	return f, true
}

// plausible: long enough to be a theme or credits, too short to be the
// episode, and in the right half of it.
func plausible(startMs, endMs int64, clip Clip, r Rules) bool {
	if l := endMs - startMs; l < r.MinMs || l > r.MaxMs {
		return false
	}
	if d := clip.DurationMs; d > 0 && (r.Credits != (startMs >= d/2)) {
		return false
	}
	return true
}

func iou(a, b cand) float64 {
	shared := min(a.end, b.end) - max(a.start, b.start)
	union := max(a.end, b.end) - min(a.start, b.start)
	if shared <= 0 || union <= 0 {
		return 0
	}
	return float64(shared) / float64(union)
}

func median(v []int) int {
	s := append([]int(nil), v...)
	sort.Ints(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
