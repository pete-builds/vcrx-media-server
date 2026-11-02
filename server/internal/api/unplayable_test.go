package api

import (
	"strings"
	"testing"
	"time"
)

// The list comes from each library's latest full scan that finished ok: a
// newer targeted scan saw one folder and an aborted one did not see
// everything, so neither may replace it.
func TestAdminUnplayable(t *testing.T) {
	f := newFixture(t)
	now := time.Now().UnixMilli()
	run := func(lib int64, filter any, status string, at int64, n int, list string) {
		t.Helper()
		if _, err := f.db.Exec(`INSERT INTO scan_runs (library_id, trigger, path_filter, status, started_at, finished_at,
			files_unplayable, unplayable) VALUES (?, 'manual', ?, ?, ?, ?, ?, ?)`, lib, filter, status, at, at+1, n, list); err != nil {
			t.Fatal(err)
		}
	}
	movies := f.libraryID("Movies")
	run(movies, nil, "ok", now-4000, 1, `[{"path":"Old/old.iso","reason":"disc_image"}]`)
	run(movies, nil, "ok", now-3000, 501, `[{"path":"A Disc/a.iso","reason":"disc_image"},{"path":"_UNPACK_B","reason":"incomplete"}]`)
	run(movies, "Some Film (2001)", "ok", now-2000, 0, `[]`)
	run(movies, nil, "aborted", now-1000, 0, `[]`)

	var body struct {
		Total      int `json:"total"`
		Unplayable []struct {
			LibraryID int64  `json:"library_id"`
			Path      string `json:"path"`
			Reason    string `json:"reason"`
		} `json:"unplayable"`
	}
	r := f.do("GET", "/api/v1/admin/scans/unplayable", f.adminTok, nil)
	if r.code != 200 {
		t.Fatalf("code %d: %s", r.code, r.body)
	}
	r.json(t, &body)
	if body.Total != 501 || len(body.Unplayable) != 2 || body.Unplayable[0].Path != "A Disc/a.iso" ||
		body.Unplayable[1].Reason != "incomplete" || body.Unplayable[0].LibraryID != movies {
		t.Errorf("got %+v", body)
	}

	if r := f.do("GET", "/api/v1/admin/scans/unplayable", f.friendTok, nil); r.code != 403 {
		t.Errorf("non-admin: code %d", r.code)
	}
	if r := f.do("GET", "/api/v1/admin/scans", f.adminTok, nil); r.code != 200 || !strings.Contains(string(r.body), `"files_unplayable":501`) {
		t.Errorf("scan list lacks files_unplayable: %s", r.body)
	}
}
