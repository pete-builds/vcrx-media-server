//go:build unix

package fsafe

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Opening a FIFO for reading blocks until something writes to it, so a FIFO
// named like a sidecar (movie.srt, movie.nfo) on a share would hang whatever
// opened it. OpenUnder refuses anything but a regular file before opening.
func TestOpenUnderRefusesFIFO(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "movie.nfo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		f, _, err := OpenUnder(root, "movie.nfo")
		if f != nil {
			_ = f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("opened a FIFO")
		}
	case <-time.After(2 * time.Second):
		// Unblock the stuck open so the test can end.
		if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
			_ = w.Close()
		}
		t.Fatal("OpenUnder blocked on a FIFO")
	}
}
