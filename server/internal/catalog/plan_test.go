package catalog

import (
	"strings"
	"testing"
)

// For a friend (granted libraries, so the policy says library_id IN (...)),
// SQLite once chose to answer "this show's episodes" by scanning every episode
// through the (library_id, kind) index: five seconds for a page of 100 shows
// on a library of 10,000 episodes. These queries must start from the show.
func TestEpisodeQueriesStartFromTheShow(t *testing.T) {
	f := newHomeFx(t)
	cond, args := f.friend.ItemCond("e")
	plan := func(name, q string, qargs ...any) {
		t.Helper()
		rows, err := f.db.Query("EXPLAIN QUERY PLAN "+q, append(qargs, args...)...)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		defer func() { _ = rows.Close() }()
		var lines []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		all := strings.Join(lines, "\n")
		if strings.Contains(all, "SCAN e") || strings.Contains(all, "SEARCH e USING INDEX items_library_kind") {
			t.Errorf("%s reads episodes by library, not by show:\n%s", name, all)
		}
	}
	plan("show episode count", episodeCountSQL("show", cond), 1, 1)
	plan("season episode count", episodeCountSQL("season", cond), 1, 1)
	plan("following episode", followingSQL(" AND s.season_number > 0", cond), 1, 1, 1, 1)
}
