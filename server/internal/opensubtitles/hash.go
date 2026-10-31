package opensubtitles

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// hashChunk is how much of each end of the file the hash reads.
const hashChunk = 64 << 10

// ErrTooSmall means the file is shorter than the two 64 KiB chunks the hash
// reads, so it has no OpenSubtitles hash.
var ErrTooSmall = errors.New("file too small to hash")

// MovieHash is OpenSubtitles' file hash: the file size plus every 64-bit
// little-endian word in the first and the last 64 KiB, wrapping on overflow,
// as 16 hex digits. It reads 128 KiB however large the file is, so hashing a
// 60 GB remux on the NAS costs two small reads.
func MovieHash(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // the caller resolved path inside its library root
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	return hashReader(f, fi.Size())
}

func hashReader(r io.ReaderAt, size int64) (string, error) {
	if size < 2*hashChunk {
		return "", ErrTooSmall
	}
	sum := uint64(size) //nolint:gosec // a file size is never negative
	buf := make([]byte, hashChunk)
	for _, off := range []int64{0, size - hashChunk} {
		if _, err := r.ReadAt(buf, off); err != nil {
			return "", fmt.Errorf("hash: %w", err)
		}
		for i := 0; i < hashChunk; i += 8 {
			sum += binary.LittleEndian.Uint64(buf[i:])
		}
	}
	return fmt.Sprintf("%016x", sum), nil
}
