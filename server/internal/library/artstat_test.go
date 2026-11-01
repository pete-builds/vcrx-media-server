package library

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Local artwork whose file cannot be stat'ed right now (EIO, ESTALE on a
// flaky share) is not gone: its row must not be deleted. A file that no
// longer exists still is.
func TestStaleArtOnlyForFilesThatAreGone(t *testing.T) {
	dir := t.TempDir()
	flaky := filepath.Join(dir, "poster.jpg")
	if err := os.WriteFile(flaky, []byte("jpg"), 0o600); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "fanart.jpg")
	statFile = func(p string) (fs.FileInfo, error) {
		if p == flaky {
			return nil, &fs.PathError{Op: "stat", Path: p, Err: syscall.EIO}
		}
		return os.Stat(p)
	}
	t.Cleanup(func() { statFile = os.Stat })
	it := &itemRow{id: 7, art: map[string]artRow{
		artPoster:   {source: "local", path: flaky},
		artBackdrop: {source: "local", path: gone},
	}}
	stale := (&writer{}).staleArt(it, nil, nil, sidecars{})
	if stale[7][artPoster] {
		t.Error("artwork whose stat failed with EIO was treated as gone")
	}
	if !stale[7][artBackdrop] {
		t.Error("artwork whose file no longer exists was kept")
	}
}
