package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"sync"

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/probe"
)

// maxBookTextBytes caps metadata.json, desc.txt and reader.txt.
const maxBookTextBytes = 64 << 10

// scanAudiobooks indexes an audiobook library. It keeps the other scanners'
// safety rules: a missing or emptied root changes nothing, a file that
// vanishes is marked missing and kept for MissingRetention, and nothing is
// written until the walk is complete and the checks pass. A targeted scan
// walks the whole library: books are small in number, and a book's shape
// depends on every file in its folder.
func (s *Scanner) scanAudiobooks(ctx context.Context, lib Library, opt Options, run *Run) (warning string, err error) {
	ap, ok := s.Prober.(probe.AudioProber)
	if !ok {
		return "", errors.New("the scanner's prober cannot read audio files")
	}
	log := s.log().With("library", lib.Name, "run", run.ID)
	if err := checkRoot(lib.RootPath); err != nil {
		return "", err
	}
	if _, _, err := resolveSubPath(lib.RootPath, opt.SubPath); err != nil {
		return "", err
	}
	run.PathFilter = ""
	w := newWalker(lib.RootPath, KindAudiobook, log)
	folders := map[string]*bookFolder{}
	var found []*bookFile
	if err := walkBooks(ctx, w, folders, &found); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: %s: %w", ErrRootUnavailable, lib.RootPath, err)
	}
	run.Seen = len(found)

	stored, err := loadBookFiles(ctx, s.DB, lib.ID)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for _, f := range found {
		seen[f.abs] = true
	}
	present, gone, stillMissing := 0, 0, 0
	for _, f := range stored {
		if f.missingSince != 0 {
			if !seen[f.abs] {
				stillMissing++
			}
			continue
		}
		present++
		if !seen[f.abs] && !w.underFailed(f.rel) {
			gone++
		}
	}
	if !opt.Force {
		if len(found) == 0 && present > 0 {
			return "", abortf("%s holds no audio files but %d are indexed under it; the share may be unmounted or emptied. Nothing was changed; rescan with force to accept", lib.RootPath, present)
		}
		if gone > 0 && (gone+stillMissing)*2 > len(stored) {
			return "", abortf("%d of %d indexed files (more than half) are no longer under %s%s. Nothing was changed; rescan with force to accept",
				gone+stillMissing, len(stored), lib.RootPath, alreadyMissing(stillMissing))
		}
	}

	nowMs := s.now().UnixMilli()
	var all, toProbe []*bookFile
	for _, f := range found {
		old := stored[f.abs]
		switch {
		case old == nil:
			f.needsProbe = true
		case old.size != f.size || old.mtime != f.mtime:
			f.needsProbe, f.changed = true, true
		case old.probeStatus == "error" && (opt.Force || opt.Trigger == "manual" || opt.Trigger == "cli" || nowMs-old.probedAt > ProbeRetryAfter.Milliseconds()):
			f.needsProbe, f.changed = true, true
		default:
			*f = *old
			f.missingSince = 0
		}
		if f.needsProbe {
			toProbe = append(toProbe, f)
		}
		all = append(all, f)
	}
	for abs, f := range stored {
		if seen[abs] {
			continue
		}
		if f.missingSince == 0 && !w.underFailed(f.rel) {
			f.missingSince = nowMs
			run.Missing++
		}
		all = append(all, f)
	}
	run.Failed += s.probeBooks(ctx, ap, toProbe, nowMs)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// A book whose folder could not be read is left exactly as it was.
	var books []*bookPlan
	for _, b := range groupBooks(all, folders) {
		if w.underFailed(b.folder) {
			log.Info("book left as it was: its folder could not be read", "folder", b.folder)
			continue
		}
		books = append(books, b)
	}
	if err := s.writeBooks(ctx, lib, books, run, nowMs); err != nil {
		return "", err
	}
	if len(w.failed) > 0 {
		warning = fmt.Sprintf("completed with warnings: %d folders or files could not be read (first: %s); their files were left as they were", len(w.failed), w.failed[0])
	}
	return warning, nil
}

// walkBooks lists every folder under the root, keeping the audio files and
// what each folder says about its book.
func walkBooks(ctx context.Context, w *walker, folders map[string]*bookFolder, found *[]*bookFile) error {
	if _, err := w.read(""); err != nil {
		return err
	}
	stack := []string{""}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		l, err := w.read(rel)
		if err != nil {
			w.log.Warn("folder unreadable, its files will not be marked missing", "path", rel, "err", err)
			w.failed = append(w.failed, rel)
			continue
		}
		bf := &bookFolder{rel: rel}
		folders[rel] = bf
		var subdirs []string
		for _, e := range l.entries {
			child := joinRel(rel, e.name)
			if strings.HasPrefix(e.name, ".") {
				continue
			}
			if e.isDir {
				if !musicSkipDir(e.name) {
					subdirs = append(subdirs, child)
				}
				continue
			}
			lower := strings.ToLower(e.name)
			switch {
			case IsBookAudio(e.name):
				fi := w.statFile(child)
				if fi != nil {
					*found = append(*found, &bookFile{rel: child, abs: w.absOf(child), size: fi.Size(), mtime: fi.ModTime().UnixMilli()})
				}
			case isImage(e.name):
				bf.images = append(bf.images, e.name)
			case lower == "metadata.json":
				if b, err := readSmall(w.root, child); err == nil {
					bf.meta = parseBookMeta(b)
				}
			case lower == "desc.txt":
				if b, err := readSmall(w.root, child); err == nil {
					bf.desc = strings.TrimSpace(string(b))
				}
			case lower == "reader.txt":
				if b, err := readSmall(w.root, child); err == nil {
					bf.reader = strings.TrimSpace(string(b))
				}
			}
		}
		for i := len(subdirs) - 1; i >= 0; i-- {
			stack = append(stack, subdirs[i])
		}
	}
	// A disc folder's pictures belong to its book.
	for rel, f := range folders {
		if rel == "" || DiscFolder(path.Base(rel)) == 0 {
			continue
		}
		if parent := folders[parentRel(rel)]; parent != nil && len(parent.images) == 0 {
			for _, img := range f.images {
				parent.images = append(parent.images, path.Base(rel)+"/"+img)
			}
		}
	}
	return nil
}

func readSmall(root, rel string) ([]byte, error) {
	f, _, err := fsafe.OpenUnder(root, rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, maxBookTextBytes))
}

// probeBooks probes files in parallel and returns how many failed.
func (s *Scanner) probeBooks(ctx context.Context, ap probe.AudioProber, files []*bookFile, nowMs int64) int {
	n := s.ProbeConcurrency
	if n <= 0 {
		n = DefaultProbeConcurrency
	}
	jobs := make(chan *bookFile)
	var wg sync.WaitGroup
	var mu sync.Mutex
	failed := 0
	for range min(n, max(len(files), 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				res, err := ap.ProbeAudio(ctx, f.abs)
				f.probedAt = nowMs
				if err != nil {
					f.probeStatus, f.probeError, f.tags, f.chapters = "error", clipText(err.Error(), 500), map[string]string{}, nil
					mu.Lock()
					failed++
					mu.Unlock()
					continue
				}
				f.probeStatus, f.probeError = "ok", ""
				f.tags, f.chapters, f.container, f.codec = res.Tags, res.Chapters, res.Container, res.Codec
				f.durationMs, f.bitrate, f.sampleRate, f.channels = res.DurationMs, res.Bitrate, res.SampleRate, res.Channels
				f.embedded = res.EmbeddedCover
			}
		}()
	}
	for _, f := range files {
		select {
		case jobs <- f:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	return failed
}

// loadBookFiles reads the stored files of a library by absolute path.
func loadBookFiles(ctx context.Context, conn *sql.DB, libraryID int64) (map[string]*bookFile, error) {
	rows, err := conn.QueryContext(ctx, `SELECT path, rel_path, size, mtime, tags, chapters, COALESCE(container, ''), COALESCE(codec, ''),
			COALESCE(duration_ms, 0), COALESCE(bitrate, 0), COALESCE(sample_rate, 0), COALESCE(channels, 0),
			embedded_cover, probe_status, COALESCE(probe_error, ''), COALESCE(probed_at, 0), COALESCE(missing_since, 0)
		FROM audiobook_files WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]*bookFile{}
	for rows.Next() {
		f := &bookFile{}
		var tags, chapters string
		if err := rows.Scan(&f.abs, &f.rel, &f.size, &f.mtime, &tags, &chapters, &f.container, &f.codec, &f.durationMs, &f.bitrate,
			&f.sampleRate, &f.channels, &f.embedded, &f.probeStatus, &f.probeError, &f.probedAt, &f.missingSince); err != nil {
			return nil, err
		}
		f.tags = map[string]string{}
		_ = json.Unmarshal([]byte(tags), &f.tags)
		_ = json.Unmarshal([]byte(chapters), &f.chapters)
		out[f.abs] = f
	}
	return out, rows.Err()
}

// writeBooks writes one scan's books in a single transaction.
func (s *Scanner) writeBooks(ctx context.Context, lib Library, books []*bookPlan, run *Run, nowMs int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, b := range books {
		added := int64(0)
		var present int64
		for _, f := range b.files {
			if added == 0 || f.mtime < added {
				added = f.mtime
			}
			if f.missingSince == 0 {
				present += f.durationMs
			}
		}
		if added <= 0 || added > nowMs {
			added = nowMs
		}
		var idx any
		if b.seriesIndex != nil {
			idx = *b.seriesIndex
		}
		var bookID int64
		err := tx.QueryRowContext(ctx, `INSERT INTO audiobook_books (library_id, group_key, folder, title, sort_title, subtitle, author,
				author_sort, narrator, series, series_index, year, date, genre, publisher, description, duration_ms, cover_source, cover_path,
				added_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (library_id, group_key) DO UPDATE SET folder = excluded.folder, title = excluded.title,
				sort_title = excluded.sort_title, subtitle = excluded.subtitle, author = excluded.author, author_sort = excluded.author_sort,
				narrator = excluded.narrator, series = excluded.series, series_index = excluded.series_index, year = excluded.year,
				date = excluded.date, genre = excluded.genre, publisher = excluded.publisher, description = excluded.description,
				duration_ms = excluded.duration_ms, cover_source = excluded.cover_source, cover_path = excluded.cover_path,
				updated_at = excluded.updated_at
			RETURNING id`,
			lib.ID, b.key, b.folder, b.title, sortName(b.title), nullStr(b.subtitle), b.author, authorSort(b.author), nullStr(b.narrator),
			nullStr(b.series), idx, nullInt(int64(b.year)), nullStr(b.date), nullStr(b.genre), nullStr(b.publisher), nullStr(b.description),
			present, nullStr(b.coverSource), nullStr(b.coverPath), added, nowMs).Scan(&bookID)
		if err != nil {
			return fmt.Errorf("book %q: %w", b.title, err)
		}
		fileIDs := make([]int64, len(b.files))
		var start int64
		for i, f := range b.files {
			id, err := writeBookFile(ctx, tx, lib.ID, bookID, i, start, f, run, nowMs)
			if err != nil {
				return fmt.Errorf("file %s: %w", f.rel, err)
			}
			fileIDs[i] = id
			start += fileSpan(f)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM audiobook_chapters WHERE book_id = ?`, bookID); err != nil {
			return err
		}
		for i, c := range b.chapters {
			if _, err := tx.ExecContext(ctx, `INSERT INTO audiobook_chapters (book_id, idx, title, start_ms, end_ms, file_id) VALUES (?, ?, ?, ?, ?, ?)`,
				bookID, i, c.title, c.startMs, c.endMs, fileIDs[c.file]); err != nil {
				return err
			}
		}
	}
	cutoff := nowMs - MissingRetention.Milliseconds()
	if _, err := tx.ExecContext(ctx, `DELETE FROM audiobook_files WHERE library_id = ? AND missing_since IS NOT NULL AND missing_since < ?`,
		lib.ID, cutoff); err != nil {
		return err
	}
	// A regroup can leave a book with no files; it goes.
	if _, err := tx.ExecContext(ctx, `DELETE FROM audiobook_books WHERE library_id = ?
		AND NOT EXISTS (SELECT 1 FROM audiobook_files f WHERE f.book_id = audiobook_books.id)`, lib.ID); err != nil {
		return err
	}
	// What has no file left is hidden, not deleted, until retention runs out.
	if _, err := tx.ExecContext(ctx, `UPDATE audiobook_books SET missing_since = CASE
			WHEN EXISTS (SELECT 1 FROM audiobook_files f WHERE f.book_id = audiobook_books.id AND f.missing_since IS NULL) THEN NULL
			ELSE COALESCE(missing_since, ?) END WHERE library_id = ?`, nowMs, lib.ID); err != nil {
		return err
	}
	return tx.Commit()
}

// fileSpan is how much of the book a file covers: its duration, or the end of
// its last chapter mark when that is later.
func fileSpan(f *bookFile) int64 {
	d := f.durationMs
	if n := len(f.chapters); n > 0 && f.chapters[n-1].EndMs > d {
		d = f.chapters[n-1].EndMs
	}
	return d
}

// authorSort sorts an author by surname: "A. Author" as "Author, A.". The
// first of several authors decides.
func authorSort(author string) string {
	first, _, _ := strings.Cut(author, ",")
	first, _, _ = strings.Cut(first, "&")
	first, _, _ = strings.Cut(first, ";")
	parts := strings.Fields(first)
	if len(parts) < 2 || first == unknownAuthor {
		return strings.TrimSpace(first)
	}
	return parts[len(parts)-1] + ", " + strings.Join(parts[:len(parts)-1], " ")
}

func writeBookFile(ctx context.Context, tx *sql.Tx, libraryID, bookID int64, position int, startMs int64, f *bookFile, run *Run, nowMs int64) (int64, error) {
	tags, _ := json.Marshal(f.tags)
	chapters := []byte("[]")
	if len(f.chapters) > 0 {
		chapters, _ = json.Marshal(f.chapters)
	}
	t := squashTags(f.tags)
	title := fileChapterTitle(f, position)
	disc, track := numberOf(t.get("disc", "discnumber")), numberOf(t.get("track", "tracknumber"))
	var id int64
	err := tx.QueryRowContext(ctx, `UPDATE audiobook_files SET book_id = ?, rel_path = ?, size = ?, mtime = ?, position = ?, start_ms = ?,
			title = ?, disc_number = ?, track_number = ?, duration_ms = ?, container = ?, codec = ?, bitrate = ?, sample_rate = ?,
			channels = ?, tags = ?, chapters = ?, embedded_cover = ?, probe_status = ?, probe_error = ?, probed_at = ?, missing_since = ?,
			updated_at = ?
		WHERE path = ? RETURNING id`,
		bookID, f.rel, f.size, f.mtime, position, startMs, title, nullInt(int64(disc)), nullInt(int64(track)), nullInt(f.durationMs),
		nullStr(f.container), nullStr(f.codec), nullInt(f.bitrate), nullInt(int64(f.sampleRate)), nullInt(int64(f.channels)),
		string(tags), string(chapters), f.embedded, bookProbeStatus(f), nullStr(f.probeError), nullInt(f.probedAt), nullInt(f.missingSince),
		nowMs, f.abs).Scan(&id)
	if err == nil {
		if f.changed {
			run.Changed++
		}
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	added := f.mtime
	if added <= 0 || added > nowMs {
		added = nowMs
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO audiobook_files (library_id, book_id, path, rel_path, size, mtime, position, start_ms, title,
			disc_number, track_number, duration_ms, container, codec, bitrate, sample_rate, channels, tags, chapters, embedded_cover,
			probe_status, probe_error, probed_at, missing_since, added_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		libraryID, bookID, f.abs, f.rel, f.size, f.mtime, position, startMs, title, nullInt(int64(disc)), nullInt(int64(track)),
		nullInt(f.durationMs), nullStr(f.container), nullStr(f.codec), nullInt(f.bitrate), nullInt(int64(f.sampleRate)),
		nullInt(int64(f.channels)), string(tags), string(chapters), f.embedded, bookProbeStatus(f), nullStr(f.probeError),
		nullInt(f.probedAt), nullInt(f.missingSince), added, nowMs).Scan(&id)
	if err == nil {
		run.Added++
	}
	return id, err
}

func bookProbeStatus(f *bookFile) string {
	if f.probeStatus == "" {
		return "pending"
	}
	return f.probeStatus
}
