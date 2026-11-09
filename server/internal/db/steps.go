package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// stepDirective names a Go step in a migration file, on a line of its own:
//
//	-- vcrx:step music_library_kind
//
// It is keyed by name rather than by version, so renumbering a migration
// before it merges never points it at the wrong step.
const stepDirective = "-- vcrx:step "

// steps are schema changes SQL alone cannot make safely. SQLite cannot alter a
// CHECK constraint in place; the table has to be rebuilt, and a rebuild that
// lists the columns by hand would silently drop any column another migration
// added since. These rebuild from the table's own stored definition instead.
var steps = map[string]func(ctx context.Context, tx *sql.Tx) error{
	// Libraries may now hold music as well as movies and shows.
	"music_library_kind": func(ctx context.Context, tx *sql.Tx) error {
		return rebuildTable(ctx, tx, "libraries", func(ddl string) (string, error) {
			return replaceOnce(ddl, "CHECK (kind IN ('movie', 'show'))", "CHECK (kind IN ('movie', 'show', 'music'))")
		})
	},
	// And audiobooks.
	"audiobook_library_kind": func(ctx context.Context, tx *sql.Tx) error {
		return rebuildTable(ctx, tx, "libraries", func(ddl string) (string, error) {
			return replaceOnce(ddl, "CHECK (kind IN ('movie', 'show', 'music'))", "CHECK (kind IN ('movie', 'show', 'music', 'audiobook'))")
		})
	},
}

func (m migration) step() (func(context.Context, *sql.Tx) error, error) {
	for _, line := range strings.Split(m.sql, "\n") {
		name, ok := strings.CutPrefix(strings.TrimSpace(line), stepDirective)
		if !ok {
			continue
		}
		fn := steps[strings.TrimSpace(name)]
		if fn == nil {
			return nil, fmt.Errorf("migration %s: unknown step %q", m.name, strings.TrimSpace(name))
		}
		return fn, nil
	}
	return nil, nil
}

func replaceOnce(ddl, old, repl string) (string, error) {
	if strings.Count(ddl, old) != 1 {
		return "", fmt.Errorf("expected exactly one %q in the table definition", old)
	}
	return strings.Replace(ddl, old, repl, 1), nil
}

// rebuildTable is SQLite's documented procedure for a change ALTER TABLE
// cannot make (sqlite.org/lang_altertable.html, "other kinds of table schema
// changes"): create the new table from the edited definition, copy every row,
// drop the old one, rename the new one into place, and recreate the indexes
// and triggers that went with it. The caller runs it inside a transaction with
// foreign keys off, so dropping the old table cascades nothing, and checks the
// foreign keys before committing. Rows keep their ids, so every reference to
// them still holds.
func rebuildTable(ctx context.Context, tx *sql.Tx, table string, edit func(ddl string) (string, error)) error {
	var ddl string
	err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE type = 'table' AND name = ?`, table).Scan(&ddl)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("rebuild %s: no such table", table)
	}
	if err != nil {
		return err
	}
	// A table an earlier step rebuilt is stored with its name quoted, which
	// is what ALTER TABLE ... RENAME writes.
	head := "CREATE TABLE " + table + " "
	if quoted := "CREATE TABLE \"" + table + "\" "; strings.HasPrefix(ddl, quoted) {
		head = quoted
	}
	if !strings.HasPrefix(ddl, head) {
		return fmt.Errorf("rebuild %s: unexpected definition %.60q", table, ddl)
	}
	edited, err := edit(ddl)
	if err != nil {
		return fmt.Errorf("rebuild %s: %w", table, err)
	}
	tmp := table + "__rebuild"
	extras, err := schemaFor(ctx, tx, table)
	if err != nil {
		return err
	}
	stmts := []string{
		"CREATE TABLE " + tmp + " " + strings.TrimPrefix(edited, head),
		// The definition is the old one edited, so the columns line up in order.
		"INSERT INTO " + tmp + " SELECT * FROM " + table,
		"DROP TABLE " + table,
		"ALTER TABLE " + tmp + " RENAME TO " + table,
	}
	for _, s := range append(stmts, extras...) {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("rebuild %s: %w", table, err)
		}
	}
	return nil
}

// schemaFor is the SQL of the indexes and triggers that belong to a table,
// which dropping it drops too.
func schemaFor(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT sql FROM sqlite_schema WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL
		ORDER BY type, name`, table)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// clearDanglingSetNull clears references left pointing at rows that were
// deleted while foreign keys were off (by hand, or by a tool that never turns
// them on). Where the reference is declared ON DELETE SET NULL, SQLite would
// have cleared it at the delete had foreign keys been on, so clearing it now
// is what the schema always said. Any other dangling reference is left alone
// for foreignKeyCheck to refuse. It returns how many references it cleared.
func clearDanglingSetNull(ctx context.Context, tx *sql.Tx) (int, error) {
	found, err := danglingReferences(ctx, tx)
	if err != nil {
		return 0, err
	}
	lists := map[string]map[int][]fkColumn{}
	cleared := 0
	for _, d := range found {
		fks, ok := lists[d.table]
		if !ok {
			fks, err = foreignKeyList(ctx, tx, d.table)
			if err != nil {
				return cleared, err
			}
			lists[d.table] = fks
		}
		cols := fks[d.fkid]
		if len(cols) == 0 {
			continue
		}
		setNull := true
		for _, c := range cols {
			if !strings.EqualFold(c.onDelete, "SET NULL") {
				setNull = false
			}
		}
		if !setNull {
			continue
		}
		for _, c := range cols {
			q := "UPDATE " + quoteIdent(d.table) + " SET " + quoteIdent(c.from) + " = NULL WHERE rowid = ?"
			if _, err := tx.ExecContext(ctx, q, d.rowid); err != nil {
				return cleared, err
			}
		}
		cleared++
	}
	return cleared, nil
}

// dangling is one row PRAGMA foreign_key_check reports, by its foreign key id.
type dangling struct {
	table string
	rowid int64
	fkid  int
}

// danglingReferences lists every row whose reference points at a missing
// row, read in full before any of them is changed.
func danglingReferences(ctx context.Context, tx *sql.Tx) ([]dangling, error) {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var found []dangling
	for rows.Next() {
		var table, parent string
		var rowid sql.NullInt64
		var fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return nil, err
		}
		if rowid.Valid {
			found = append(found, dangling{table, rowid.Int64, fkid})
		}
	}
	return found, rows.Err()
}

// fkColumn is one column of a foreign key and the key's ON DELETE action.
type fkColumn struct{ from, onDelete string }

// foreignKeyList maps each of a table's foreign keys (by PRAGMA id) to its
// columns and ON DELETE action.
func foreignKeyList(ctx context.Context, tx *sql.Tx, table string) (map[int][]fkColumn, error) {
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_list("+quoteIdent(table)+")")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int][]fkColumn{}
	for rows.Next() {
		var id, seq int
		var parent, from, onUpdate, onDelete, match string
		var to sql.NullString
		if err := rows.Scan(&id, &seq, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return nil, err
		}
		out[id] = append(out[id], fkColumn{from, onDelete})
	}
	return out, rows.Err()
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// foreignKeyCheck fails when any row points at a row that does not exist.
func foreignKeyCheck(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		var table string
		var rowid sql.NullInt64
		var parent string
		var fkid int
		if err := rows.Scan(&table, &rowid, &parent, &fkid); err != nil {
			return err
		}
		return fmt.Errorf("foreign key check failed: %s row %d points at a missing %s row", table, rowid.Int64, parent)
	}
	return rows.Err()
}
