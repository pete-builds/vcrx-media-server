package collections

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// SmartBuilder rebuilds the smart collections from the YAML files in Dir, and
// stamps and clears the labels they own.
//
// Safety rules, each covered by a test:
//   - A file that fails to load is refused whole, and its collections are built
//     from the last good copy, which is kept in each row's definition column.
//     So a typo never wipes collections or labels, even across a restart.
//   - A missing or unreadable directory changes nothing (a mount that went
//     away is not an admin deleting every file). An empty directory does
//     remove everything built from YAML.
//   - Labels are owned through item_labels.source = 'collection'. Only those
//     rows are ever removed; a label set by hand or read from an NFO file is
//     never touched, and an NFO that names a stamped label takes it over.
//   - A label whose definition could not be built this time is only added to,
//     never cleared, until a build of it succeeds. While any file is refused,
//     stamps of labels no definition owns any more are kept too.
//   - Everything is written in one transaction, so access policies that filter
//     by label see the new labels the moment the build commits.
type SmartBuilder struct {
	DB  *sql.DB
	Dir string
	Log *slog.Logger
	Now func() time.Time

	build  sync.Mutex // one build at a time
	once   sync.Once
	kick   chan struct{}
	mu     sync.Mutex // guards last
	last   *Report
	logged map[string]string // file errors already logged, so a timer does not repeat them
}

// Report is what the last build did, for the admin API and logs.
type Report struct {
	Dir     string `json:"dir"`
	BuiltAt int64  `json:"built_at"`
	// Error is set when the directory could not be read; nothing changed then.
	Error         string       `json:"error,omitempty"`
	Files         []FileReport `json:"files"`
	Collections   []DefReport  `json:"collections"`
	LabelsAdded   int          `json:"labels_added"`
	LabelsRemoved int          `json:"labels_removed"`
	Removed       int          `json:"collections_removed"`
}

// FileReport is one YAML file.
type FileReport struct {
	File        string `json:"file"`
	Error       string `json:"error,omitempty"`
	Collections int    `json:"collections"`
	// LastGood is true when the file failed to load and its collections came
	// from the last copy that did.
	LastGood bool `json:"last_good"`
}

// DefReport is one collection definition and how its build went.
type DefReport struct {
	File     string  `json:"file"`
	Line     int     `json:"line"`
	Name     string  `json:"name"`
	Library  string  `json:"library"`
	Slug     string  `json:"slug"`
	Label    string  `json:"label,omitempty"`
	Visible  Visible `json:"visible"`
	Items    int     `json:"items"`
	LastGood bool    `json:"last_good"`
	Error    string  `json:"error,omitempty"`
}

func (b *SmartBuilder) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *SmartBuilder) init() {
	b.once.Do(func() { b.kick = make(chan struct{}, 1) })
}

// Trigger asks Run for a build soon, for example after a scan. It never blocks.
func (b *SmartBuilder) Trigger() {
	b.init()
	select {
	case b.kick <- struct{}{}:
	default:
	}
}

// Status returns the last build's report, or nil before the first build.
func (b *SmartBuilder) Status() *Report {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.last == nil {
		return nil
	}
	r := *b.last
	return &r
}

// Run builds at once, then on every Trigger and every interval.
func (b *SmartBuilder) Run(ctx context.Context, interval time.Duration) {
	b.init()
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-b.kick:
		}
		res, err := b.Build(ctx)
		switch {
		case errors.Is(err, context.Canceled):
			return
		case err != nil && b.Log != nil:
			b.Log.Warn("smart collections build failed", "err", err)
		case b.Log != nil && (res.LabelsAdded > 0 || res.LabelsRemoved > 0 || res.Removed > 0):
			b.Log.Info("smart collections built", "collections", len(res.Collections), "labels_added", res.LabelsAdded,
				"labels_removed", res.LabelsRemoved, "removed", res.Removed)
		}
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		t.Reset(interval)
	}
}

// existingRow is a smart collection already in the database.
type existingRow struct {
	id, library int64
	slug        string
	def         Definition
	ok          bool // the definition column parsed
}

type libInfo struct {
	id   int64
	kind string
}

// planned is one definition ready to build.
type planned struct {
	def      Definition
	lastGood bool
	lib      libInfo
	members  []int64
	err      error
}

// Build loads the YAML files and rebuilds every smart collection and label.
func (b *SmartBuilder) Build(ctx context.Context) (Report, error) {
	b.build.Lock()
	defer b.build.Unlock()
	now := b.now().UnixMilli()
	rep := Report{Dir: b.Dir, BuiltAt: now, Files: []FileReport{}, Collections: []DefReport{}}

	existing, err := b.existing(ctx)
	if err != nil {
		return rep, err
	}
	lastGood := map[string][]Definition{}
	for _, e := range existing {
		if e.ok {
			lastGood[e.def.File] = append(lastGood[e.def.File], e.def)
		}
	}

	files, dirErr := LoadDir(b.Dir)
	if dirErr != nil {
		// Without the directory there is nothing to compare against: keep every
		// collection and label exactly as the last good load left them.
		rep.Error = dirErr.Error()
		if errors.Is(dirErr, fs.ErrNotExist) {
			rep.Error = "collections directory not found: " + b.Dir
		}
		files = nil
		for _, f := range sortedKeys(lastGood) {
			files = append(files, FileResult{File: f, Err: errors.New(rep.Error)})
		}
	}

	// While any file is refused, labels no definition owns are not cleared
	// either: a renamed file with a typo must not unlabel what it used to own.
	hold := dirErr != nil
	var plan []*planned
	for _, f := range files {
		fr := FileReport{File: f.File}
		defs, fromLast := f.Defs, false
		if f.Err != nil {
			fr.Error = f.Err.Error()
			defs, fromLast = lastGood[f.File], true
			fr.LastGood = len(defs) > 0
			hold = true
			b.logFileError(f.File, fr.Error)
		} else {
			b.logFileError(f.File, "")
		}
		fr.Collections = len(defs)
		rep.Files = append(rep.Files, fr)
		for _, d := range defs {
			plan = append(plan, &planned{def: d, lastGood: fromLast})
		}
	}

	libs, err := b.libraries(ctx)
	if err != nil {
		return rep, err
	}
	taken := map[string]string{}
	for _, p := range plan {
		lib, ok := libs[strings.ToLower(p.def.Library)]
		if !ok {
			p.err = fmt.Errorf("%s:%d: no library named %q", p.def.File, p.def.Line, p.def.Library)
			continue
		}
		p.lib = lib
		key := fmt.Sprintf("%d\x00%s", lib.id, p.def.Slug())
		if other, dup := taken[key]; dup {
			p.err = fmt.Errorf("%s:%d: collection %q in library %q is already defined in %s", p.def.File, p.def.Line, p.def.Name, p.def.Library, other)
			continue
		}
		taken[key] = p.def.File
		p.members, p.err = b.match(ctx, p.def, lib, now)
	}

	if err := b.write(ctx, plan, existing, now, &rep, dirErr != nil, hold); err != nil {
		return rep, err
	}
	for _, p := range plan {
		dr := DefReport{File: p.def.File, Line: p.def.Line, Name: p.def.Name, Library: p.def.Library, Slug: p.def.Slug(),
			Label: p.def.Label, Visible: p.def.Visible, Items: len(p.members), LastGood: p.lastGood}
		if p.err != nil {
			dr.Error = p.err.Error()
			if b.Log != nil {
				b.Log.Warn("smart collection not built", "file", p.def.File, "collection", p.def.Name, "err", p.err)
			}
		}
		rep.Collections = append(rep.Collections, dr)
	}
	b.mu.Lock()
	b.last = &rep
	b.mu.Unlock()
	return rep, nil
}

// logFileError logs a file's load error once per change, so the timer does not
// repeat the same warning every run.
func (b *SmartBuilder) logFileError(file, msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.logged == nil {
		b.logged = map[string]string{}
	}
	if b.logged[file] == msg {
		return
	}
	b.logged[file] = msg
	if b.Log == nil {
		return
	}
	if msg != "" {
		b.Log.Error("collections file refused; using its last good load", "file", file, "err", msg)
	} else {
		b.Log.Info("collections file loaded", "file", file)
	}
}

func sortedKeys(m map[string][]Definition) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (b *SmartBuilder) existing(ctx context.Context) ([]existingRow, error) {
	rows, err := b.DB.QueryContext(ctx, `SELECT id, library_id, slug, definition FROM collections WHERE source = 'yaml' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []existingRow
	for rows.Next() {
		var e existingRow
		var def string
		if err := rows.Scan(&e.id, &e.library, &e.slug, &def); err != nil {
			return nil, err
		}
		e.ok = json.Unmarshal([]byte(def), &e.def) == nil && e.def.File != "" && e.def.Name != ""
		out = append(out, e)
	}
	return out, rows.Err()
}

func (b *SmartBuilder) libraries(ctx context.Context) (map[string]libInfo, error) {
	rows, err := b.DB.QueryContext(ctx, `SELECT id, name, kind FROM libraries`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]libInfo{}
	for rows.Next() {
		var l libInfo
		var name string
		if err := rows.Scan(&l.id, &name, &l.kind); err != nil {
			return nil, err
		}
		out[strings.ToLower(name)] = l
	}
	return out, rows.Err()
}

// match lists the titles a definition selects: movies in a movie library,
// shows in a show library, present on disk, in title order.
func (b *SmartBuilder) match(ctx context.Context, d Definition, lib libInfo, now int64) ([]int64, error) {
	cond, args := d.Smart.sql("i", now)
	q := `SELECT i.id FROM items i WHERE i.library_id = ? AND i.kind = ? AND i.missing_since IS NULL AND ` + cond + `
		ORDER BY i.sort_title COLLATE NOCASE, i.id`
	got, err := ids(ctx, b.DB, q, append([]any{lib.id, lib.kind}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("%s:%d: %w", d.File, d.Line, err)
	}
	return got, nil
}

// labelPlan is one label the definitions own.
type labelPlan struct {
	name   string
	want   map[int64]bool
	frozen bool // a definition that owns it failed: add only, never clear
	id     int64
}

// write saves the plan in one transaction. keepRows keeps every existing row
// (the directory could not be read); holdOrphans keeps stamped labels that no
// definition owns.
func (b *SmartBuilder) write(ctx context.Context, plan []*planned, existing []existingRow, now int64, rep *Report, keepRows, holdOrphans bool) error {
	tx, err := b.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	labels := map[string]*labelPlan{}
	keep := map[string]bool{}
	for _, p := range plan {
		if p.def.Label != "" {
			k := strings.ToLower(p.def.Label)
			lp := labels[k]
			if lp == nil {
				lp = &labelPlan{name: p.def.Label, want: map[int64]bool{}}
				labels[k] = lp
			}
			if p.err != nil {
				lp.frozen = true
			}
			for _, id := range p.members {
				lp.want[id] = true
			}
		}
		if p.lib.id == 0 {
			continue
		}
		// A definition that failed keeps its row (and members) as they were.
		keep[fmt.Sprintf("%d\x00%s", p.lib.id, p.def.Slug())] = true
	}

	// Labels first, so collection rows can point at them.
	for _, lp := range labels {
		if _, err := tx.ExecContext(ctx, `INSERT INTO labels (name, hidden) VALUES (?, 1) ON CONFLICT (name) DO NOTHING`, lp.name); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT id FROM labels WHERE name = ?`, lp.name).Scan(&lp.id); err != nil {
			return err
		}
	}

	for _, p := range plan {
		if p.err != nil {
			if p.lib.id != 0 {
				if _, err := tx.ExecContext(ctx, `UPDATE collections SET last_error = ? WHERE library_id = ? AND slug = ? AND source = 'yaml'`,
					p.err.Error(), p.lib.id, p.def.Slug()); err != nil {
					return err
				}
			}
			continue
		}
		def, err := json.Marshal(p.def)
		if err != nil {
			return err
		}
		var labelID any
		if p.def.Label != "" {
			labelID = labels[strings.ToLower(p.def.Label)].id
		}
		var id int64
		err = tx.QueryRowContext(ctx, `INSERT INTO collections (library_id, slug, name, sort_title, source, definition, label_id,
				visible_library, visible_home, visible_shared, last_built_at, last_error, updated_at)
			VALUES (?, ?, ?, ?, 'yaml', ?, ?, ?, ?, ?, ?, NULL, ?)
			ON CONFLICT (library_id, slug) DO UPDATE SET name = excluded.name, sort_title = excluded.sort_title,
				definition = excluded.definition, label_id = excluded.label_id, visible_library = excluded.visible_library,
				visible_home = excluded.visible_home, visible_shared = excluded.visible_shared,
				last_built_at = excluded.last_built_at, last_error = NULL, updated_at = excluded.updated_at
			WHERE collections.source = 'yaml'
			RETURNING id`,
			p.lib.id, p.def.Slug(), p.def.Name, sortTitle(p.def.Name), string(def), labelID,
			p.def.Visible.Library, p.def.Visible.Home, p.def.Visible.Shared, now, now).Scan(&id)
		if err != nil {
			return fmt.Errorf("%s:%d: save collection %q: %w", p.def.File, p.def.Line, p.def.Name, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM collection_items WHERE collection_id = ?`, id); err != nil {
			return err
		}
		for i, item := range p.members {
			if _, err := tx.ExecContext(ctx, `INSERT INTO collection_items (collection_id, item_id, position) VALUES (?, ?, ?)`, id, item, i); err != nil {
				return err
			}
		}
	}

	for _, e := range existing {
		if keepRows || keep[fmt.Sprintf("%d\x00%s", e.library, e.slug)] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM collections WHERE id = ?`, e.id); err != nil {
			return err
		}
		rep.Removed++
	}

	if err := reconcileLabels(ctx, tx, labels, holdOrphans, rep); err != nil {
		return err
	}
	return tx.Commit()
}

// reconcileLabels stamps each owned label on the titles that want it and clears
// it from titles that no longer match. Only rows with source 'collection' are
// ever deleted; a hand-set or NFO label on the same title is left alone, and an
// insert never overwrites one.
func reconcileLabels(ctx context.Context, tx *sql.Tx, labels map[string]*labelPlan, holdOrphans bool, rep *Report) error {
	for _, lp := range labels {
		for item := range lp.want {
			r, err := tx.ExecContext(ctx, `INSERT INTO item_labels (item_id, label_id, source) VALUES (?, ?, 'collection')
				ON CONFLICT (item_id, label_id) DO NOTHING`, item, lp.id)
			if err != nil {
				return err
			}
			if n, _ := r.RowsAffected(); n > 0 {
				rep.LabelsAdded++
			}
		}
	}
	// Stamped rows nobody wants any more: their title stopped matching, or no
	// definition owns their label now.
	stamps, err := collectionStamps(ctx, tx)
	if err != nil {
		return err
	}
	var drop []stamp
	for _, s := range stamps {
		lp := labels[strings.ToLower(s.name)]
		if (lp == nil && holdOrphans) || (lp != nil && (lp.frozen || lp.want[s.item])) {
			continue
		}
		drop = append(drop, s)
	}
	for _, s := range drop {
		if _, err := tx.ExecContext(ctx, `DELETE FROM item_labels WHERE item_id = ? AND label_id = ? AND source = 'collection'`, s.item, s.label); err != nil {
			return err
		}
		rep.LabelsRemoved++
	}
	return nil
}

type stamp struct {
	item, label int64
	name        string
}

// collectionStamps lists every label row a smart collection stamped.
func collectionStamps(ctx context.Context, tx *sql.Tx) ([]stamp, error) {
	rows, err := tx.QueryContext(ctx, `SELECT il.item_id, il.label_id, l.name FROM item_labels il JOIN labels l ON l.id = il.label_id
		WHERE il.source = 'collection'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []stamp
	for rows.Next() {
		var s stamp
		if err := rows.Scan(&s.item, &s.label, &s.name); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
