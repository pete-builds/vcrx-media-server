package library

// Merging and splitting movie versions.
//
// The scanner files every movie under a key. A file's natural key comes from
// its names (movieKey); an item owns its group_key plus every key merged into
// it, listed in items.extra under "merged_keys"; and a key of the form
// "p:<path relative to the root>" pins one file. Each scan routes a file to the
// item owning its pin key, else the item owning its natural key, else a new
// item. Merge and split only move keys and versions between items, so a rescan
// reproduces what an admin did instead of undoing it, and a file that arrives
// later under a merged key joins the merged movie.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"
)

const (
	// pinPrefix starts the group_key of an item made by splitting one file
	// off. The automatic TMDb merge never touches such an item.
	pinPrefix = "p:"
	// mergedKeysField is the items.extra entry listing keys merged into an item.
	mergedKeysField = "merged_keys"
)

var (
	// ErrItemNotFound means no movie, or no version, has that id.
	ErrItemNotFound = errors.New("no such item")
	// ErrNotMovie means a merge or split named something other than a movie.
	ErrNotMovie = errors.New("only movies can be merged or split")
	// ErrOtherLibrary means a merge named movies from different libraries.
	ErrOtherLibrary = errors.New("movies from different libraries cannot be merged")
	// ErrNothingToMerge means a merge named no movie besides the target.
	ErrNothingToMerge = errors.New("name at least one other movie to merge")
	// ErrOnlyVersion means a split named a movie's only file.
	ErrOnlyVersion = errors.New("this is the movie's only version, so there is nothing to split it from")
)

func pinKey(rel string) string { return pinPrefix + rel }

// mergedKeys reads the keys merged into an item from its extra column.
func mergedKeys(extra map[string]any) []string {
	raw, _ := extra[mergedKeysField].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// routed is the item a movie file belongs to by an admin's split or a merge,
// or nil when the file follows its natural key.
func (st *state) routed(p *plan) *itemRow {
	pin := pinKey(p.v.rel)
	if it := st.byKey[identity("movie", 0, pin)]; it != nil {
		return it
	}
	if it := st.alias[pin]; it != nil {
		return it
	}
	return st.alias[p.movieKey]
}

// libraryBusy reports a scan running in the library. Merge and split refuse
// then: the scan holds its own picture of the items and would write against
// rows that no longer exist.
func libraryBusy(ctx context.Context, tx *sql.Tx, libraryID, nowMs int64) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM scan_runs WHERE library_id = ? AND status = 'running' AND started_at > ?`,
		libraryID, nowMs-staleRunAfter.Milliseconds()).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrScanRunning
	}
	return nil
}

type movieRow struct {
	id, library int64
	kind, key   string
	extra       map[string]any
	locked      bool
}

func loadMovie(ctx context.Context, tx *sql.Tx, id int64) (movieRow, error) {
	m := movieRow{id: id}
	var extra, locked string
	err := tx.QueryRowContext(ctx, `SELECT library_id, kind, group_key, extra, locked_fields FROM items WHERE id = ?`, id).
		Scan(&m.library, &m.kind, &m.key, &extra, &locked)
	if errors.Is(err, sql.ErrNoRows) {
		return m, fmt.Errorf("%w: %d", ErrItemNotFound, id)
	}
	if err != nil {
		return m, err
	}
	if m.kind != "movie" {
		return m, ErrNotMovie
	}
	if json.Unmarshal([]byte(extra), &m.extra) != nil || m.extra == nil {
		m.extra = map[string]any{}
	}
	m.locked = len(parseLocked(locked)) > 0
	return m, nil
}

func saveKeys(ctx context.Context, tx *sql.Tx, m movieRow, keys []string, nowMs int64) error {
	sort.Strings(keys)
	keys = slices.Compact(keys)
	if len(keys) == 0 {
		delete(m.extra, mergedKeysField)
	} else {
		m.extra[mergedKeysField] = keys
	}
	b, err := json.Marshal(m.extra)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE items SET extra = ?, updated_at = ? WHERE id = ?`, string(b), nowMs, m.id)
	return err
}

// settleMissing marks an item missing exactly when it has no file on disk.
func settleMissing(ctx context.Context, tx *sql.Tx, id, nowMs int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE items SET missing_since = CASE
		WHEN EXISTS (SELECT 1 FROM media_versions v WHERE v.item_id = items.id AND v.missing_since IS NULL) THEN NULL
		ELSE COALESCE(missing_since, ?) END WHERE id = ?`, nowMs, id)
	return err
}

// Merge folds the movies in from into the movie into: their files become its
// versions, and everything people did with them comes along (see fold). It is
// one transaction, so a failure leaves the library exactly as it was, and it
// refuses while the library is being scanned.
func Merge(ctx context.Context, db *sql.DB, into int64, from []int64) error {
	return inTx(ctx, db, func(tx *sql.Tx) error {
		nowMs := time.Now().UnixMilli()
		target, err := loadMovie(ctx, tx, into)
		if err != nil {
			return err
		}
		if err := libraryBusy(ctx, tx, target.library, nowMs); err != nil {
			return err
		}
		var others []movieRow
		seen := map[int64]bool{into: true}
		for _, id := range from {
			if seen[id] {
				continue
			}
			seen[id] = true
			m, err := loadMovie(ctx, tx, id)
			if err != nil {
				return err
			}
			if m.library != target.library {
				return ErrOtherLibrary
			}
			others = append(others, m)
		}
		if len(others) == 0 {
			return ErrNothingToMerge
		}
		return fold(ctx, tx, target, others, nowMs)
	})
}

// fold moves every version and every piece of user data from others onto
// target, takes over their keys, and deletes them. The target keeps its own
// title, metadata and match. Watch state is combined per user: played if
// either was, play counts added, the position from whichever changed last.
// Playback history moves over. Labels are united, so a label that hides a
// title from someone still hides the merged one; ratings, tags and artwork
// fill only what the target lacks; collection membership is united.
func fold(ctx context.Context, tx *sql.Tx, target movieRow, others []movieRow, nowMs int64) error {
	keys := mergedKeys(target.extra)
	for _, m := range others {
		keys = append(keys, m.key)
		keys = append(keys, mergedKeys(m.extra)...)
		steps := []string{
			`UPDATE media_versions SET item_id = :into, updated_at = :now WHERE item_id = :from`,
			`INSERT INTO watch_state (user_id, item_id, position_ms, duration_ms, played, play_count, last_version_id, last_played_at, updated_at)
				SELECT user_id, :into, position_ms, duration_ms, played, play_count, last_version_id, last_played_at, updated_at
				FROM watch_state WHERE item_id = :from
				ON CONFLICT (user_id, item_id) DO UPDATE SET
					played = MAX(watch_state.played, excluded.played),
					play_count = watch_state.play_count + excluded.play_count,
					position_ms = CASE WHEN excluded.updated_at > watch_state.updated_at THEN excluded.position_ms ELSE watch_state.position_ms END,
					duration_ms = CASE WHEN excluded.updated_at > watch_state.updated_at THEN excluded.duration_ms ELSE watch_state.duration_ms END,
					last_version_id = CASE WHEN excluded.updated_at > watch_state.updated_at THEN excluded.last_version_id ELSE watch_state.last_version_id END,
					last_played_at = NULLIF(MAX(IFNULL(watch_state.last_played_at, 0), IFNULL(excluded.last_played_at, 0)), 0),
					updated_at = MAX(watch_state.updated_at, excluded.updated_at)`,
			`UPDATE playback_sessions SET item_id = :into WHERE item_id = :from`,
			`INSERT OR IGNORE INTO item_labels (item_id, label_id, source) SELECT :into, label_id, source FROM item_labels WHERE item_id = :from`,
			`INSERT OR IGNORE INTO item_ratings (item_id, source, value, votes) SELECT :into, source, value, votes FROM item_ratings WHERE item_id = :from`,
			`INSERT OR IGNORE INTO item_tags (item_id, kind, value, position, role) SELECT :into, b.kind, b.value, b.position, b.role
				FROM item_tags b WHERE b.item_id = :from AND NOT EXISTS (SELECT 1 FROM item_tags a WHERE a.item_id = :into AND a.kind = b.kind)`,
			`INSERT OR IGNORE INTO artwork (item_id, kind, source, source_path, url, cache_path, width, height, updated_at)
				SELECT :into, kind, source, source_path, url, cache_path, width, height, updated_at FROM artwork WHERE item_id = :from`,
			`INSERT OR IGNORE INTO collection_items (collection_id, item_id, position) SELECT collection_id, :into, position FROM collection_items WHERE item_id = :from`,
			`UPDATE items SET added_at = MIN(added_at, (SELECT added_at FROM items WHERE id = :from)) WHERE id = :into`,
			`DELETE FROM items WHERE id = :from`,
		}
		for _, q := range steps {
			if _, err := tx.ExecContext(ctx, q, sql.Named("into", target.id), sql.Named("from", m.id), sql.Named("now", nowMs)); err != nil {
				return fmt.Errorf("merge %d into %d: %w", m.id, target.id, err)
			}
		}
	}
	if err := saveKeys(ctx, tx, target, keys, nowMs); err != nil {
		return err
	}
	return settleMissing(ctx, tx, target.id, nowMs)
}

// Split moves one version out of its movie into a movie of its own and
// returns the new item's id. The new item is keyed on the file's path, which
// is the record that keeps it apart: scans route the file to it, and the
// automatic TMDb merge leaves it alone. It starts as a copy of the movie it
// came from (metadata, match, labels, collections), so it is no easier to see
// than before; Fix Match can then point it at the right title. History of that
// file, and resume points on it, go with it. One transaction; refuses while
// the library is being scanned.
func Split(ctx context.Context, db *sql.DB, versionID int64) (int64, error) {
	var newID int64
	err := inTx(ctx, db, func(tx *sql.Tx) error {
		nowMs := time.Now().UnixMilli()
		var itemID int64
		var rel string
		err := tx.QueryRowContext(ctx, `SELECT item_id, rel_path FROM media_versions WHERE id = ?`, versionID).Scan(&itemID, &rel)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: version %d", ErrItemNotFound, versionID)
		}
		if err != nil {
			return err
		}
		src, err := loadMovie(ctx, tx, itemID)
		if err != nil {
			return err
		}
		if err := libraryBusy(ctx, tx, src.library, nowMs); err != nil {
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_versions WHERE item_id = ?`, itemID).Scan(&n); err != nil {
			return err
		}
		if n < 2 {
			return ErrOnlyVersion
		}
		// The copy keeps everything but the merged keys, which stay with the
		// movie the other files are still filed under.
		extra := map[string]any{}
		for k, v := range src.extra {
			if k != mergedKeysField {
				extra[k] = v
			}
		}
		b, err := json.Marshal(extra)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO items (library_id, kind, parent_id, group_key, title, sort_title, original_title, year,
				overview, tagline, content_rating, runtime_ms, premiere_date, tmdb_id, imdb_id, tmdb_collection_id, metadata_source,
				locked_fields, extra, missing_since, added_at, updated_at)
			SELECT i.library_id, 'movie', NULL, ?, i.title, i.sort_title, i.original_title, i.year, i.overview, i.tagline, i.content_rating,
				i.runtime_ms, i.premiere_date, i.tmdb_id, i.imdb_id, i.tmdb_collection_id, i.metadata_source, i.locked_fields, ?,
				v.missing_since, v.added_at, ?
			FROM items i JOIN media_versions v ON v.id = ? WHERE i.id = ?`, pinKey(rel), string(b), nowMs, versionID, itemID)
		if err != nil {
			return fmt.Errorf("split version %d: %w", versionID, err)
		}
		if newID, err = res.LastInsertId(); err != nil {
			return err
		}
		steps := []string{
			`INSERT INTO item_tags (item_id, kind, value, position, role) SELECT :to, kind, value, position, role FROM item_tags WHERE item_id = :from`,
			`INSERT INTO item_ratings (item_id, source, value, votes) SELECT :to, source, value, votes FROM item_ratings WHERE item_id = :from`,
			`INSERT INTO item_labels (item_id, label_id, source) SELECT :to, label_id, source FROM item_labels WHERE item_id = :from`,
			`INSERT INTO artwork (item_id, kind, source, source_path, url, cache_path, width, height, updated_at)
				SELECT :to, kind, source, source_path, url, cache_path, width, height, updated_at FROM artwork WHERE item_id = :from`,
			`INSERT INTO collection_items (collection_id, item_id, position) SELECT collection_id, :to, position FROM collection_items WHERE item_id = :from`,
			// Enrichment replaces only values it wrote itself, so the copy takes
			// its record too; otherwise Fix Match on the new item would keep the
			// old title's plot.
			`INSERT INTO tmdb_state (item_id, status, tmdb_id, match_key, written, reason, checked_at)
				SELECT :to, status, tmdb_id, match_key, written, reason, checked_at FROM tmdb_state WHERE item_id = :from`,
			`INSERT INTO watch_state (user_id, item_id, position_ms, duration_ms, played, play_count, last_version_id, last_played_at, updated_at)
				SELECT user_id, :to, position_ms, duration_ms, played, play_count, last_version_id, last_played_at, updated_at
				FROM watch_state WHERE item_id = :from AND last_version_id = :version`,
			`UPDATE watch_state SET last_version_id = NULL WHERE item_id = :from AND last_version_id = :version`,
			`UPDATE playback_sessions SET item_id = :to WHERE item_id = :from AND version_id = :version`,
			`UPDATE media_versions SET item_id = :to, updated_at = :now WHERE id = :version`,
		}
		for _, q := range steps {
			if _, err := tx.ExecContext(ctx, q, sql.Named("to", newID), sql.Named("from", itemID), sql.Named("version", versionID),
				sql.Named("now", nowMs)); err != nil {
				return fmt.Errorf("split version %d: %w", versionID, err)
			}
		}
		// If the file came in through a merge of an earlier split, its pin
		// now belongs to the new item alone.
		keys := slices.DeleteFunc(mergedKeys(src.extra), func(k string) bool { return k == pinKey(rel) })
		if err := saveKeys(ctx, tx, src, keys, nowMs); err != nil {
			return err
		}
		return settleMissing(ctx, tx, itemID, nowMs)
	})
	if err != nil {
		return 0, err
	}
	return newID, nil
}

// MergeByTMDb merges movies in one library that TMDb (or an NFO) gave the same
// id: a 4K and a 1080p copy in separate folders become one movie with two
// versions. Items an admin split off are never merged; shows, seasons and
// episodes are never touched. libraryID 0 means every library; a library being
// scanned is skipped, since its scan runs this itself when it finishes. It
// returns how many movies were merged away.
func MergeByTMDb(ctx context.Context, db *sql.DB, libraryID int64) (int, error) {
	return mergeByTMDb(ctx, db, libraryID, time.Now().UnixMilli(), false)
}

// autoMergeable picks the items the automatic merge may touch: movies with a
// TMDb id, never one an admin split off.
const autoMergeable = `kind = 'movie' AND tmdb_id > 0 AND group_key NOT LIKE '` + pinPrefix + `%'`

func mergeByTMDb(ctx context.Context, db *sql.DB, libraryID, nowMs int64, inScan bool) (int, error) {
	q := `SELECT library_id, tmdb_id FROM items WHERE ` + autoMergeable
	args := []any{}
	if libraryID != 0 {
		q += ` AND library_id = ?`
		args = append(args, libraryID)
	}
	groups, err := tmdbGroups(ctx, db, q+` GROUP BY library_id, tmdb_id HAVING COUNT(*) > 1 ORDER BY library_id, tmdb_id`, args...)
	if err != nil {
		return 0, err
	}
	merged := 0
	for _, g := range groups {
		err := inTx(ctx, db, func(tx *sql.Tx) error {
			if !inScan {
				if err := libraryBusy(ctx, tx, g.library, nowMs); err != nil {
					return err
				}
			}
			// Read again inside the transaction: the set may have changed.
			ids, err := txIDs(ctx, tx, `SELECT id FROM items WHERE library_id = ? AND tmdb_id = ? AND `+autoMergeable+` ORDER BY id`,
				g.library, g.tmdb)
			if err != nil || len(ids) < 2 {
				return err
			}
			var ms []movieRow
			for _, id := range ids {
				m, err := loadMovie(ctx, tx, id)
				if err != nil {
					return err
				}
				ms = append(ms, m)
			}
			// The survivor is the movie an admin edited by hand, else the oldest
			// (lowest id): its links, its place in Continue Watching and its
			// metadata stay as they were.
			best := 0
			for i, m := range ms {
				if m.locked && !ms[best].locked {
					best = i
				}
			}
			target := ms[best]
			others := append(append([]movieRow{}, ms[:best]...), ms[best+1:]...)
			if err := fold(ctx, tx, target, others, nowMs); err != nil {
				return err
			}
			merged += len(others)
			return nil
		})
		if errors.Is(err, ErrScanRunning) {
			continue
		}
		if err != nil {
			return merged, fmt.Errorf("merge TMDb %d in library %d: %w", g.tmdb, g.library, err)
		}
	}
	return merged, nil
}

type tmdbGroup struct{ library, tmdb int64 }

func tmdbGroups(ctx context.Context, db *sql.DB, q string, args ...any) ([]tmdbGroup, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []tmdbGroup
	for rows.Next() {
		var g tmdbGroup
		if err := rows.Scan(&g.library, &g.tmdb); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func txIDs(ctx context.Context, tx *sql.Tx, q string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// inTx runs fn in one write transaction and commits only if it succeeds.
func inTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
