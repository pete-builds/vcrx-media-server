//go:build unix

package library

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/scanlock"
)

// A scan keeps its library's lock from begin to end, and a scan while another
// process holds the lock is refused even when no run row says so (a row
// older than staleRunAfter, or one a restarted server closed).
func TestScanHoldsItsLibraryLock(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	e.sc.LockDir = t.TempDir()
	run, err := e.sc.begin(context.Background(), e.lib, Options{})
	if err != nil {
		t.Fatal(err)
	}
	release, held, err := scanlock.Library(e.sc.LockDir, e.lib.ID)
	release()
	if err != nil || !held {
		t.Fatalf("lock during the scan: held=%v err=%v", held, err)
	}
	if _, err := e.sc.execute(context.Background(), e.lib, Options{}, run); err != nil {
		t.Fatal(err)
	}
	release, held, _ = scanlock.Library(e.sc.LockDir, e.lib.ID)
	release()
	if held {
		t.Fatal("the lock outlived the scan")
	}

	// Another process holds the lock: refused.
	other, _, err := scanlock.Library(e.sc.LockDir, e.lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer other()
	if _, err := e.sc.Scan(context.Background(), e.lib, Options{}); !errors.Is(err, ErrScanRunning) {
		t.Fatalf("scan while another process holds the lock: %v", err)
	}
}

// Server start closes only the runs nobody is scanning: a CLI scan that is
// still going keeps its run, a dead one's run is aborted.
func TestAbortOrphanedRunsLeavesALiveScan(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	dir := t.TempDir()
	if _, err := e.db.Exec(`INSERT INTO scan_runs (library_id, trigger, status, started_at) VALUES (?, 'cli', 'running', ?)`,
		e.lib.ID, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	cli, _, err := scanlock.Library(dir, e.lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := AbortOrphanedRuns(context.Background(), e.db, dir, time.Now()); err != nil || n != 0 {
		t.Fatalf("with the CLI scan alive: aborted %d (%v)", n, err)
	}
	if got := e.str(`SELECT status FROM scan_runs ORDER BY id DESC LIMIT 1`); got != "running" {
		t.Fatalf("live run status = %q", got)
	}
	cli()
	if n, err := AbortOrphanedRuns(context.Background(), e.db, dir, time.Now()); err != nil || n != 1 {
		t.Fatalf("after the CLI scan died: aborted %d (%v)", n, err)
	}
	if got := e.str(`SELECT status FROM scan_runs ORDER BY id DESC LIMIT 1`); got != "aborted" {
		t.Fatalf("dead run status = %q", got)
	}
}
