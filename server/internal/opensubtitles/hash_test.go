package opensubtitles_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/opensubtitles"
)

// The reference values are worked by hand from the published definition
// (size + sum of the little-endian uint64 words of the first and last 64 KiB,
// mod 2^64), not computed by the code under test. The published sample files
// (breakdance.avi, dummy.bin) are not redistributable, so they are not used.
func TestMovieHashReferenceValues(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	const chunk = 64 << 10

	zeros := write("zeros", make([]byte, 2*chunk))

	marked := make([]byte, 2*chunk)
	binary.LittleEndian.PutUint64(marked[0:], 0x0123456789abcdef)
	binary.LittleEndian.PutUint64(marked[len(marked)-8:], 0x1111111111111111)
	markedPath := write("marked", marked)

	wrap := make([]byte, 2*chunk)
	binary.LittleEndian.PutUint64(wrap[0:], 0xffffffffffffffff)
	binary.LittleEndian.PutUint64(wrap[len(wrap)-8:], 0xffffffffffffffff)
	wrapPath := write("wrap", wrap)

	ones := write("ones", bytes.Repeat([]byte{1}, 2*chunk))

	for _, c := range []struct {
		name, path, want string
	}{
		// All zero: the hash is the size alone, 131072 = 0x20000.
		{"zeros", zeros, "0000000000020000"},
		// 0x20000 + 0x0123456789abcdef + 0x1111111111111111.
		{"one word at each end", markedPath, "123456789abedf00"},
		// 0x20000 + 2 * (2^64 - 1) wraps to 0x20000 - 2.
		{"overflow wraps", wrapPath, "000000000001fffe"},
		// 16384 words of 0x0101010101010101, plus 0x20000.
		{"every word", ones, "4040404040424000"},
	} {
		got, err := opensubtitles.MovieHash(c.path)
		if err != nil || got != c.want {
			t.Errorf("%s: hash %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

// Only the two ends count: a change in the middle of a large file does not
// change the hash, and one in the last 64 KiB does.
func TestMovieHashReadsOnlyTheEnds(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.mkv")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	const size = 64 << 20
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	before, err := opensubtitles.MovieHash(p)
	if err != nil {
		t.Fatal(err)
	}
	if before != "0000000004000000" {
		t.Fatalf("sparse 64 MiB file: %s, want the size 0x4000000", before)
	}
	poke := func(off int64) {
		f, err := os.OpenFile(p, os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteAt([]byte{7}, off); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	poke(size / 2)
	if h, _ := opensubtitles.MovieHash(p); h != before {
		t.Fatalf("a byte in the middle changed the hash to %s", h)
	}
	poke(size - 8)
	if h, _ := opensubtitles.MovieHash(p); h != "0000000004000007" {
		t.Fatalf("a byte in the last word: %s, want 0000000004000007", h)
	}
}

func TestMovieHashRefusesSmallFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tiny")
	if err := os.WriteFile(p, make([]byte, 100_000), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := opensubtitles.MovieHash(p); !errors.Is(err, opensubtitles.ErrTooSmall) {
		t.Fatalf("err %v, want ErrTooSmall", err)
	}
}
