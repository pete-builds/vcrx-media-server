package enrich

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
)

func (e *Enricher) commit(ctx context.Context, plans []*plan) error {
	tx, err := e.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := e.now().UnixMilli()
	for _, p := range plans {
		if err := writePlan(ctx, tx, p, now); err != nil {
			return err
		}
		if err := e.saveState(ctx, tx, p.c.id, p.status, p.tmdbID, p.key, p.next, p.reason); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func writePlan(ctx context.Context, tx *sql.Tx, p *plan, now int64) error {
	if p.source != "" {
		p.set["metadata_source"] = p.source
	}
	if len(p.set) > 0 {
		cols := make([]string, 0, len(p.set))
		for c := range p.set {
			cols = append(cols, c)
		}
		// Fixed order keeps the statement stable; every name comes from this package.
		sort.Strings(cols)
		q := "UPDATE items SET updated_at = ?"
		args := []any{now}
		for _, c := range cols {
			q += ", " + c + " = ?"
			args = append(args, p.set[c])
		}
		if _, err := tx.ExecContext(ctx, q+" WHERE id = ?", append(args, p.c.id)...); err != nil {
			return err
		}
	}
	for kind, values := range p.tags {
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_tags WHERE item_id = ? AND kind = ?`, p.c.id, kind); err != nil {
			return err
		}
		for i, t := range values {
			var role any
			if t.role != "" {
				role = t.role
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO item_tags (item_id, kind, value, position, role) VALUES (?, ?, ?, ?, ?)`,
				p.c.id, kind, t.value, i, role); err != nil {
				return err
			}
		}
	}
	if p.rating != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_ratings (item_id, source, value, votes) VALUES (?, 'tmdb', ?, ?)
			ON CONFLICT (item_id, source) DO UPDATE SET value = excluded.value, votes = excluded.votes`,
			p.c.id, p.rating[0], int64(p.rating[1])); err != nil {
			return err
		}
	}
	for _, a := range p.art {
		// The WHERE keeps a local image a scan recorded in the meantime.
		if _, err := tx.ExecContext(ctx, `INSERT INTO artwork (item_id, kind, source, url, cache_path, updated_at) VALUES (?, ?, 'tmdb', ?, ?, ?)
			ON CONFLICT (item_id, kind) DO UPDATE SET source = 'tmdb', source_path = NULL, url = excluded.url,
			cache_path = excluded.cache_path, width = NULL, height = NULL, updated_at = excluded.updated_at
			WHERE artwork.source = 'tmdb'`, p.c.id, a.kind, a.url, a.file, now); err != nil {
			return err
		}
	}
	return nil
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (e *Enricher) saveState(ctx context.Context, tx *sql.Tx, id int64, status string, tmdbID int64, key string, written map[string]string, reason string) error {
	var x execer = e.DB
	if tx != nil {
		x = tx
	}
	if written == nil {
		written = map[string]string{}
	}
	w, err := json.Marshal(written)
	if err != nil {
		return err
	}
	var tid, why any
	if tmdbID > 0 {
		tid = tmdbID
	}
	if reason != "" {
		why = reason
	}
	_, err = x.ExecContext(ctx, `INSERT INTO tmdb_state (item_id, status, tmdb_id, match_key, written, reason, checked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (item_id) DO UPDATE SET status = excluded.status, tmdb_id = excluded.tmdb_id, match_key = excluded.match_key,
		written = excluded.written, reason = excluded.reason, checked_at = excluded.checked_at`,
		id, status, tid, key, string(w), why, e.now().UnixMilli())
	return err
}

// Status summarises enrichment for the CLI.
type Status struct {
	Counts    map[string]int
	Unmatched []Unmatched
}

type Unmatched struct {
	ID     int64
	Kind   string
	Title  string
	Year   int
	Reason string
}
