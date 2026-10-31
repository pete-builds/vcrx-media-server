// Package collections builds automatic collections, the way Kometa's
// franchise default does for Plex: every TMDb collection (a film series) that
// has two or more movies in a library becomes a collection in that library,
// in release order. Names come from TMDb once and are kept; the membership is
// rebuilt from the library each run, so a new sequel joins and a removed film
// leaves without anyone touching it.
package collections

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Namer looks up a TMDb collection's name. nil when there is no TMDb key:
// collections whose names are not known yet then wait.
type Namer func(ctx context.Context, id int64) (string, error)

type Builder struct {
	DB    *sql.DB
	Names Namer
	Log   *slog.Logger
	Now   func() time.Time
}

func (b *Builder) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// Result says what a build did.
type Result struct {
	Collections int // franchise collections now present
	Named       int // names fetched this run
	Removed     int // collections that no longer have two movies
	Waiting     int // franchises without a name yet (no TMDb key, or a lookup failed)
}

func slug(id int64) string { return "tmdb-" + strconv.FormatInt(id, 10) }

// Build rebuilds the franchise collections of every movie library.
func (b *Builder) Build(ctx context.Context) (Result, error) {
	var res Result
	libs, err := ids(ctx, b.DB, `SELECT id FROM libraries WHERE kind = 'movie'`)
	if err != nil {
		return res, err
	}
	for _, lib := range libs {
		if err := b.buildLibrary(ctx, lib, &res); err != nil {
			return res, fmt.Errorf("library %d: %w", lib, err)
		}
	}
	return res, nil
}

func (b *Builder) buildLibrary(ctx context.Context, lib int64, res *Result) error {
	members, order, err := b.franchises(ctx, lib)
	if err != nil {
		return err
	}
	existing, err := b.existing(ctx, lib)
	if err != nil {
		return err
	}

	keep := map[int64]bool{}
	now := b.now().UnixMilli()
	for _, coll := range order {
		items := members[coll]
		if len(items) < 2 {
			continue
		}
		id, ok := existing[slug(coll)]
		if !ok {
			if b.Names == nil {
				res.Waiting++
				continue
			}
			name, err := b.Names(ctx, coll)
			if err != nil || strings.TrimSpace(name) == "" {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				res.Waiting++
				if b.Log != nil {
					b.Log.Info("collection name not available yet", "tmdb_collection", coll, "err", err)
				}
				continue
			}
			def := fmt.Sprintf(`{"tmdb_collection_id":%d}`, coll)
			r, err := b.DB.ExecContext(ctx, `INSERT INTO collections (library_id, slug, name, sort_title, source, definition, updated_at)
				VALUES (?, ?, ?, ?, 'auto', ?, ?)`, lib, slug(coll), name, sortTitle(name), def, now)
			if err != nil {
				return err
			}
			id, _ = r.LastInsertId()
			res.Named++
		}
		keep[id] = true
		if err := b.setMembers(ctx, id, items, now); err != nil {
			return err
		}
		res.Collections++
	}
	for _, id := range existing {
		if !keep[id] {
			if _, err := b.DB.ExecContext(ctx, `DELETE FROM collections WHERE id = ?`, id); err != nil {
				return err
			}
			res.Removed++
		}
	}
	return nil
}

// franchises maps each TMDb collection in the library to its present movies,
// in release order, and lists the collections in first-seen order.
func (b *Builder) franchises(ctx context.Context, lib int64) (map[int64][]int64, []int64, error) {
	rows, err := b.DB.QueryContext(ctx, `SELECT tmdb_collection_id, id FROM items
		WHERE library_id = ? AND kind = 'movie' AND tmdb_collection_id IS NOT NULL AND missing_since IS NULL
		ORDER BY tmdb_collection_id, COALESCE(year, 9999), COALESCE(premiere_date, ''), sort_title`, lib)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	members := map[int64][]int64{}
	var order []int64
	for rows.Next() {
		var coll, item int64
		if err := rows.Scan(&coll, &item); err != nil {
			return nil, nil, err
		}
		if _, seen := members[coll]; !seen {
			order = append(order, coll)
		}
		members[coll] = append(members[coll], item)
	}
	return members, order, rows.Err()
}

// existing maps the slugs of the library's automatic franchise collections to their ids.
func (b *Builder) existing(ctx context.Context, lib int64) (map[string]int64, error) {
	rows, err := b.DB.QueryContext(ctx, `SELECT id, slug FROM collections WHERE library_id = ? AND source = 'auto' AND slug LIKE 'tmdb-%'`, lib)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int64{}
	for rows.Next() {
		var id int64
		var sl string
		if err := rows.Scan(&id, &sl); err != nil {
			return nil, err
		}
		out[sl] = id
	}
	return out, rows.Err()
}

func (b *Builder) setMembers(ctx context.Context, id int64, items []int64, now int64) error {
	tx, err := b.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM collection_items WHERE collection_id = ?`, id); err != nil {
		return err
	}
	for i, item := range items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO collection_items (collection_id, item_id, position) VALUES (?, ?, ?)`, id, item, i); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE collections SET last_built_at = ?, last_error = NULL WHERE id = ?`, now, id); err != nil {
		return err
	}
	return tx.Commit()
}

// sortTitle drops a leading article so "The Matrix Collection" sorts under M.
func sortTitle(name string) string {
	lower := strings.ToLower(name)
	for _, a := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(lower, a) {
			return strings.TrimSpace(name[len(a):])
		}
	}
	return name
}

func ids(ctx context.Context, db *sql.DB, q string, args ...any) ([]int64, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Run builds once shortly after startup and then every interval.
func (b *Builder) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTimer(90 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		res, err := b.Build(ctx)
		switch {
		case errors.Is(err, context.Canceled):
			return
		case err != nil && b.Log != nil:
			b.Log.Warn("collections build failed", "err", err)
		case b.Log != nil && (res.Named > 0 || res.Removed > 0):
			b.Log.Info("collections built", "collections", res.Collections, "named", res.Named, "removed", res.Removed, "waiting", res.Waiting)
		}
		t.Reset(interval)
	}
}
