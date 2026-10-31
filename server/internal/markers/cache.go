package markers

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// The fingerprint cache: one small file per window of each file (about 30 KB
// for 10 minutes), so a season can be looked at again when a new episode
// arrives without decoding the old ones. The name carries everything that
// would change the fingerprint; a file that changed gets a new name, and the
// old one is removed when the new one is written.

const cacheMagic = "VCRXFP1\n"

func cacheName(versionID, size, mtime int64, kind string, offsetMs, lenMs int64) string {
	return fmt.Sprintf("%d-%d-%d-%s-%d-%d.fp", versionID, size, mtime, kind, offsetMs, lenMs)
}

func readCache(path string) (Fingerprint, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Fingerprint{}, false
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	magic := make([]byte, len(cacheMagic))
	var n, trailing uint32
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != cacheMagic {
		return Fingerprint{}, false
	}
	if binary.Read(r, binary.LittleEndian, &n) != nil || binary.Read(r, binary.LittleEndian, &trailing) != nil || n > 1<<22 {
		return Fingerprint{}, false
	}
	fp := Fingerprint{Hashes: make([]uint32, n), Valid: make([]bool, n), TrailingSilent: int(trailing)}
	if binary.Read(r, binary.LittleEndian, fp.Hashes) != nil || binary.Read(r, binary.LittleEndian, fp.Valid) != nil {
		return Fingerprint{}, false
	}
	return fp, true
}

// writeCache stores fp atomically and removes stale copies for the same
// version and kind.
func writeCache(dir string, versionID int64, kind, name string, fp Fingerprint) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".fp-*")
	if err != nil {
		return err
	}
	w := bufio.NewWriter(tmp)
	_, _ = w.WriteString(cacheMagic)
	// Both counts are frames of at most a 30 minute window: far below 2^32.
	n, trailing := uint32(len(fp.Hashes)), uint32(fp.TrailingSilent) //nolint:gosec // see above
	err = errors.Join(
		binary.Write(w, binary.LittleEndian, n),
		binary.Write(w, binary.LittleEndian, trailing),
		binary.Write(w, binary.LittleEndian, fp.Hashes),
		binary.Write(w, binary.LittleEndian, fp.Valid),
		w.Flush(), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), filepath.Join(dir, name))
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	old, _ := filepath.Glob(filepath.Join(dir, fmt.Sprintf("%d-*-%s-*.fp", versionID, kind)))
	for _, p := range old {
		if filepath.Base(p) != name {
			_ = os.Remove(p)
		}
	}
	return nil
}
