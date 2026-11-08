package audiobook

import "testing"

// A position in the whole book maps onto one file and an offset into it, and
// back: the file starts are the running sum of the durations before them.
func TestLocateAcrossFiles(t *testing.T) {
	t.Parallel()
	starts := []float64{0, 600, 1500}
	durs := []float64{600, 900, 300}
	for _, c := range []struct {
		pos    float64
		file   int
		offset float64
	}{{0, 0, 0}, {599.5, 0, 599.5}, {600, 1, 0}, {1000, 1, 400}, {1500, 2, 0}, {1799, 2, 299}, {5000, 2, 300}, {-3, 0, 0}} {
		f, off := Locate(starts, durs, c.pos)
		if f != c.file || off != c.offset {
			t.Errorf("Locate(%v) = file %d at %v, want file %d at %v", c.pos, f, off, c.file, c.offset)
		}
		if c.pos >= 0 && c.pos <= 1800 && starts[f]+off != c.pos {
			t.Errorf("Locate(%v) does not map back: %v", c.pos, starts[f]+off)
		}
	}
	if f, off := Locate(nil, nil, 10); f != 0 || off != 0 {
		t.Errorf("an empty book: %d %v", f, off)
	}
}

func TestClampPosition(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		pos, dur, want float64
		end            bool
	}{{10, 100, 10, false}, {-5, 100, 0, false}, {150, 100, 100, true}, {99.2, 100, 99.2, true}, {98.9, 100, 98.9, false}, {12.34567, 0, 12.346, false}} {
		got, end := ClampPosition(c.pos, c.dur)
		if got != c.want || end != c.end {
			t.Errorf("ClampPosition(%v, %v) = %v %v, want %v %v", c.pos, c.dur, got, end, c.want, c.end)
		}
	}
}
