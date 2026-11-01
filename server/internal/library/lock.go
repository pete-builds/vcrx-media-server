package library

import (
	"context"
	"database/sql"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/scanlock"
)

// AbortOrphanedRuns closes the scan runs left "running" by a process that is no
// longer scanning: the server calls it at start. A run whose library lock is
// still held belongs to a scan that is alive (a CLI scan beside the server)
// and is left alone. With no lockDir every running run is closed, as before
// locks existed.
func AbortOrphanedRuns(ctx context.Context, db *sql.DB, lockDir string, now time.Time) (int, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, library_id FROM scan_runs WHERE status = 'running'`)
	if err != nil {
		return 0, err
	}
	type open struct{ id, lib int64 }
	var runs []open
	for rows.Next() {
		var o open
		if err := rows.Scan(&o.id, &o.lib); err != nil {
			_ = rows.Close()
			return 0, err
		}
		runs = append(runs, o)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	n := 0
	for _, o := range runs {
		if lockDir != "" {
			release, held, err := scanlock.Library(lockDir, o.lib)
			release()
			if err == nil && held {
				continue // a live scan in another process
			}
		}
		if _, err := db.ExecContext(ctx, `UPDATE scan_runs SET status = 'aborted', error = 'server restarted during the scan', finished_at = ?
			WHERE id = ? AND status = 'running'`, now.UnixMilli(), o.id); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
