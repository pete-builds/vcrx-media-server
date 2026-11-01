//go:build unix

package markers

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// An episode's NFO is read only from inside the library and only when it is a
// regular file: a symlink out of the root is ignored, and a FIFO (which would
// block the marker job forever) is refused at once.
func TestNFOStaysInsideTheLibrary(t *testing.T) {
	root := t.TempDir()
	media := filepath.Join(root, "Show", "ep.mkv")
	if err := os.MkdirAll(filepath.Dir(media), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere.nfo")
	if err := os.WriteFile(outside, []byte(`<episodedetails><Marker type="intro" startTimeOffset="0" endTimeOffset="51000"/></episodedetails>`), 0o600); err != nil {
		t.Fatal(err)
	}
	nfo := filepath.Join(root, "Show", "ep.nfo")
	if err := os.Symlink(outside, nfo); err != nil {
		t.Fatal(err)
	}
	if got, _ := fromNFO(root, media); len(got) != 0 {
		t.Fatalf("read markers through a symlink out of the library: %+v", got)
	}
	// Control: the same file inside the library is read.
	if err := os.Remove(nfo); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(outside)
	if err := os.WriteFile(nfo, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := fromNFO(root, media); err != nil || got[Intro].EndMs != 51000 {
		t.Fatalf("NFO inside the library: %+v, %v", got, err)
	}
	if err := os.Remove(nfo); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(nfo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = fromNFO(root, media)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		if w, err := os.OpenFile(nfo, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("reading a FIFO named like the NFO blocked")
	}
}
