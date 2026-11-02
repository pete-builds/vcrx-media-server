package admin

import (
	"context"
	"encoding/json"
	"fmt"
)

// UnplayableTitle is one title a scan found on disk but could not index, with
// the reason: disc_image, disc_folder, archive or incomplete.
type UnplayableTitle struct {
	LibraryID int64  `json:"library_id"`
	Path      string `json:"path"`
	Reason    string `json:"reason"`
}

// Unplayable lists what each library's latest complete scan found but could
// not play. Only a full scan that finished ok counts: a targeted scan saw one
// folder, and an aborted one did not see everything. Total can exceed the list,
// which a run caps.
func (a *Admin) Unplayable(ctx context.Context) (titles []UnplayableTitle, total int, err error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT r.library_id, r.files_unplayable, r.unplayable FROM scan_runs r
		WHERE r.id = (SELECT id FROM scan_runs WHERE library_id = r.library_id AND status = 'ok' AND path_filter IS NULL
			ORDER BY started_at DESC, id DESC LIMIT 1)
		ORDER BY r.library_id`)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	titles = []UnplayableTitle{}
	for rows.Next() {
		var lib int64
		var n int
		var raw string
		if err := rows.Scan(&lib, &n, &raw); err != nil {
			return nil, 0, err
		}
		var list []UnplayableTitle
		if err := json.Unmarshal([]byte(raw), &list); err != nil {
			return nil, 0, fmt.Errorf("scan_runs.unplayable for library %d: %w", lib, err)
		}
		for _, t := range list {
			t.LibraryID = lib
			titles = append(titles, t)
		}
		total += n
	}
	return titles, total, rows.Err()
}
