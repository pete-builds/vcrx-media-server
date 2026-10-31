// Package library walks a media folder, parses names, probes files and keeps
// the items, versions and streams tables in step with what is on disk. It
// treats the media as read-only: nothing here opens a file for writing.
package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/probe"
)

var (
	// ErrScanRunning means the library already has a scan in progress.
	ErrScanRunning = errors.New("a scan is already running for this library")
	// ErrNoLibrary means no library row has that id.
	ErrNoLibrary = errors.New("no such library")
	// ErrRootUnavailable means the library root is missing, unreadable or not a
	// folder. The run fails and nothing changes.
	ErrRootUnavailable = errors.New("library root unavailable")
	// ErrAborted means a safety check stopped the run before it changed
	// anything, or the scan was cancelled.
	ErrAborted = errors.New("scan aborted")
)

const (
	// MissingRetention is how long a vanished file keeps its rows (and its
	// watch history) before the scanner deletes them.
	MissingRetention = 30 * 24 * time.Hour
	// ProbeRetryAfter is how long a failed probe waits before a scheduled scan
	// tries again; manual, CLI and forced scans retry at once.
	ProbeRetryAfter = 24 * time.Hour
	// DefaultProbeConcurrency suits a NAS over SMB: more parallel ffprobes mostly
	// add seek contention.
	DefaultProbeConcurrency = 4
	// staleRunAfter lets a scan start when a "running" row is this old: its
	// process must have died without closing it.
	staleRunAfter = 24 * time.Hour
)

// Run statuses, matching the scan_runs.status column.
const (
	StatusRunning = "running"
	StatusOK      = "ok"
	StatusError   = "error"
	StatusAborted = "aborted"
)

var triggers = map[string]bool{"startup": true, "schedule": true, "manual": true, "webhook": true, "cli": true}

// Library is the part of a libraries row the scanner needs.
type Library struct {
	ID                             int64
	Name, Kind, RootPath, Grouping string
}

// Options shape one scan. SubPath limits it to one folder or file relative to
// the root; Force accepts a scan that would mark most of the library missing.
type Options struct {
	Trigger string
	SubPath string
	Force   bool
}

// Run mirrors a scan_runs row. Changed counts files that were re-probed or
// whose rows were rewritten because a sidecar, NFO, artwork or grouping changed.
type Run struct {
	ID, LibraryID                         int64
	Trigger, Status, Error, PathFilter    string
	Seen, Added, Changed, Missing, Failed int
	StartedAt, FinishedAt                 int64
}

// Scanner indexes libraries into the database. Callers must not run two scans
// of one library at once; Manager serializes them, and the run row refuses a
// second concurrent scan even across processes.
type Scanner struct {
	DB               *sql.DB
	Prober           probe.Prober
	Log              *slog.Logger
	ProbeConcurrency int
	// OnAdded, when set, is called after a successful scan that added titles,
	// with the highest item id from before the scan: every item above it is
	// new. Used for "recently added" notifications.
	OnAdded func(ctx context.Context, lib Library, run Run, idsAbove int64)
	Now     func() time.Time
}

func (s *Scanner) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Scanner) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Scan runs one scan to completion and records it in scan_runs. The returned
// error is non-nil when the run ended in error or aborted; Run says which.
func (s *Scanner) Scan(ctx context.Context, lib Library, opt Options) (Run, error) {
	run, err := s.begin(ctx, lib, opt)
	if err != nil {
		return run, err
	}
	return s.execute(ctx, lib, opt, run)
}

func normalize(lib Library, opt Options) (Library, Options, error) {
	if opt.Trigger == "" {
		opt.Trigger = "manual"
	}
	if !triggers[opt.Trigger] {
		return lib, opt, fmt.Errorf("unknown scan trigger %q", opt.Trigger)
	}
	if lib.Grouping == "" {
		lib.Grouping = "folder"
	}
	if lib.Kind != KindMovie && lib.Kind != KindShow {
		return lib, opt, fmt.Errorf("library %q: unknown kind %q", lib.Name, lib.Kind)
	}
	if lib.Grouping != "folder" && lib.Grouping != "title_year" {
		return lib, opt, fmt.Errorf("library %q: unknown grouping %q", lib.Name, lib.Grouping)
	}
	opt.SubPath = strings.TrimSpace(opt.SubPath)
	return lib, opt, nil
}

// begin records a running scan_runs row, refusing when another live run holds
// the library. The check and the insert share one write transaction, so two
// processes cannot both start.
func (s *Scanner) begin(ctx context.Context, lib Library, opt Options) (Run, error) {
	lib, opt, err := normalize(lib, opt)
	if err != nil {
		return Run{}, err
	}
	now := s.now().UnixMilli()
	run := Run{LibraryID: lib.ID, Trigger: opt.Trigger, Status: StatusRunning, PathFilter: filepath.ToSlash(opt.SubPath), StartedAt: now}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return run, err
	}
	defer func() { _ = tx.Rollback() }()
	var busy int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM scan_runs WHERE library_id = ? AND status = 'running' AND started_at > ?`,
		lib.ID, now-staleRunAfter.Milliseconds()).Scan(&busy); err != nil {
		return run, err
	}
	if busy > 0 {
		return run, ErrScanRunning
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO scan_runs (library_id, trigger, path_filter, status, started_at) VALUES (?, ?, ?, 'running', ?)`,
		lib.ID, run.Trigger, nullStr(run.PathFilter), now)
	if err != nil {
		return run, err
	}
	if run.ID, err = res.LastInsertId(); err != nil {
		return run, err
	}
	return run, tx.Commit()
}

// execute does the work for a run begin created and closes its row.
func (s *Scanner) execute(ctx context.Context, lib Library, opt Options, run Run) (Run, error) {
	lib, opt, _ = normalize(lib, opt)
	start := s.now()
	var before int64
	if s.OnAdded != nil {
		_ = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM items`).Scan(&before)
	}
	warning, err := s.scan(ctx, lib, opt, &run)
	switch {
	case err == nil:
		run.Status = StatusOK
		run.Error = warning
	case errors.Is(err, ErrAborted) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		run.Status = StatusAborted
		run.Error = err.Error()
		if !errors.Is(err, ErrAborted) {
			err = fmt.Errorf("%w: %w", ErrAborted, err)
			run.Error = err.Error()
		}
	default:
		run.Status = StatusError
		run.Error = err.Error()
	}
	run.FinishedAt = s.now().UnixMilli()
	// The row must close even when ctx was cancelled.
	if _, ferr := s.DB.ExecContext(context.WithoutCancel(ctx), `UPDATE scan_runs SET status = ?, error = ?, files_seen = ?,
		files_added = ?, files_changed = ?, files_missing = ?, files_failed = ?, finished_at = ? WHERE id = ?`,
		run.Status, nullStr(run.Error), run.Seen, run.Added, run.Changed, run.Missing, run.Failed, run.FinishedAt, run.ID); ferr != nil && err == nil {
		err = ferr
	}
	s.log().Info("scan finished", "library", lib.Name, "run", run.ID, "status", run.Status, "seen", run.Seen, "added", run.Added,
		"changed", run.Changed, "missing", run.Missing, "failed", run.Failed, "took", s.now().Sub(start).Round(time.Millisecond).String(),
		"error", run.Error)
	if s.OnAdded != nil && run.Status == StatusOK && run.Added > 0 {
		s.OnAdded(context.WithoutCancel(ctx), lib, run, before)
	}
	return run, err
}

func abortf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrAborted, fmt.Sprintf(format, args...))
}

// checkRoot fails unless the root is an absolute, readable folder. A missing
// NAS mount must stop the scan here, before anything can be marked missing.
func checkRoot(root string) error {
	if root == "" || !filepath.IsAbs(root) {
		return fmt.Errorf("%w: root %q is not an absolute path", ErrRootUnavailable, root)
	}
	fi, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrRootUnavailable, root, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%w: %s is not a folder", ErrRootUnavailable, root)
	}
	f, err := os.Open(root)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrRootUnavailable, root, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %s cannot be read: %w", ErrRootUnavailable, root, err)
	}
	return nil
}

// resolveSubPath confines a targeted scan to the root: it rejects "../"
// escapes, symlinks that lead out of the root or through a linked folder, and
// paths that do not exist. It returns the slash-separated path relative to the
// root and whether it names a file.
func resolveSubPath(root, sub string) (rel string, isFile bool, err error) {
	if sub == "" {
		return "", false, nil
	}
	root = filepath.Clean(root)
	p := filepath.FromSlash(sub)
	if filepath.IsAbs(p) {
		p = filepath.Clean(p)
		if !fsafe.Within(root, p) {
			return "", false, fmt.Errorf("scan path %q: %w", sub, fsafe.ErrOutsideRoot)
		}
		if p, err = filepath.Rel(root, p); err != nil {
			return "", false, err
		}
	}
	p = filepath.Clean(p)
	if p == "." {
		return "", false, nil
	}
	if p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("scan path %q: %w", sub, fsafe.ErrOutsideRoot)
	}
	real, err := fsafe.ResolveUnder(root, p)
	if err != nil {
		return "", false, fmt.Errorf("scan path %q: %w", sub, err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false, err
	}
	// The walker never follows linked folders, so a targeted scan must not
	// either, or it would index files a full scan cannot see.
	if real != filepath.Join(realRoot, p) {
		return "", false, fmt.Errorf("scan path %q goes through a symbolic link", sub)
	}
	fi, err := os.Stat(real)
	if err != nil {
		return "", false, err
	}
	switch {
	case fi.IsDir():
		return filepath.ToSlash(p), false, nil
	case fi.Mode().IsRegular():
		return filepath.ToSlash(p), true, nil
	}
	return "", false, fmt.Errorf("scan path %q is not a file or folder", sub)
}

// skippedPath reports whether any folder on a relative path is one the walker
// would skip, such as Sample or @eaDir.
func skippedPath(rel, kind string, isFile bool) bool {
	parts := strings.Split(rel, "/")
	if isFile {
		parts = parts[:len(parts)-1]
	}
	for _, p := range parts {
		if p != "" && SkipDir(p, kind) != "" {
			return true
		}
	}
	return false
}

func inScope(rel, sub string, isFile bool) bool {
	if sub == "" {
		return true
	}
	if isFile {
		return rel == sub
	}
	return rel == sub || strings.HasPrefix(rel, sub+"/")
}

// scan walks, checks the safety rules, then writes. Nothing is written until
// the walk is complete and the checks pass, so an aborted run changes nothing.
func (s *Scanner) scan(ctx context.Context, lib Library, opt Options, run *Run) (warning string, err error) {
	if s.Prober == nil {
		return "", errors.New("scanner has no prober")
	}
	log := s.log().With("library", lib.Name, "run", run.ID)
	if err := checkRoot(lib.RootPath); err != nil {
		return "", err
	}
	sub, isFile, err := resolveSubPath(lib.RootPath, opt.SubPath)
	if err != nil {
		return "", err
	}
	run.PathFilter = sub
	w := newWalker(lib.RootPath, lib.Kind, log)
	switch {
	case skippedPath(sub, lib.Kind, isFile):
	case isFile:
		if _, err := w.read(parentRel(sub)); err != nil {
			return "", fmt.Errorf("read %s: %w", parentRel(sub), err)
		}
		w.addFile(sub)
	default:
		if err := w.walk(ctx, sub); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("%w: %s: %w", ErrRootUnavailable, w.absOf(sub), err)
		}
	}
	run.Seen = len(w.videos)

	st, err := loadState(ctx, s.DB, lib)
	if err != nil {
		return "", err
	}
	seen := make(map[string]bool, len(w.videos))
	for _, v := range w.videos {
		seen[v.abs] = true
	}
	present, gone := 0, 0
	for _, v := range st.versions {
		if !inScope(v.rel, sub, isFile) || v.missingSince != 0 {
			continue
		}
		present++
		if !seen[v.path] && !w.underFailed(v.rel) {
			gone++
		}
	}
	if !opt.Force {
		if len(w.videos) == 0 && present > 0 {
			where := lib.RootPath
			if sub != "" {
				where = w.absOf(sub)
			}
			return "", abortf("%s holds no video files but %d are indexed under it; the share may be unmounted or emptied. Nothing was changed; rescan with force to accept", where, present)
		}
		if sub == "" && present > 0 && gone*2 > present {
			return "", abortf("%d of %d indexed files (more than half) are no longer under %s. Nothing was changed; rescan with force to accept", gone, present, lib.RootPath)
		}
	}

	plans := planFiles(lib, w)
	if err := s.process(ctx, lib, opt, st, w, plans, run, log); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := s.reconcile(ctx, st, w, seen, sub, isFile, run); err != nil {
		return "", err
	}
	// A new copy of a film TMDb or an NFO already identified joins it now
	// rather than after the next enrichment pass. A failure here leaves the
	// copies apart, which the next pass retries, so it does not fail the scan.
	rep, err := mergeByTMDb(ctx, s.DB, lib.ID, s.now().UnixMilli(), true)
	if err != nil {
		log.Warn("merging copies with the same TMDb id", "err", err)
	}
	rep.Log(log)
	if len(w.failed) > 0 {
		warning = fmt.Sprintf("completed with warnings: %d folders could not be read (first: %s); their files were left as they were", len(w.failed), w.failed[0])
	}
	return warning, nil
}

func parentRel(rel string) string {
	d := path.Dir(rel)
	if d == "." {
		return ""
	}
	return d
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
