// Package audiobook answers for audiobook libraries: books, their chapters,
// each listener's place in each book, covers, and how each file reaches a
// device. The library package's audiobook scan fills the tables; this package
// reads them, and every query goes through the caller's library grants, as
// music does. Books carry no content rating, so a grant is the whole rule.
package audiobook

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"

	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// ErrNotFound covers rows that do not exist and rows the caller may not see,
// so a friend cannot tell the two apart.
var ErrNotFound = errors.New("not found")

// Catalog reads audiobooks for one caller's policy.
type Catalog struct {
	DB *sql.DB
	// Now is the clock for progress writes; nil is time.Now in milliseconds.
	Now func() int64
}

// Progress is where a listener is in a book. PositionS is seconds from the
// start of the whole book, across its files.
type Progress struct {
	PositionS float64 `json:"position_s"`
	Finished  bool    `json:"finished"`
	UpdatedAt int64   `json:"updated_at"`
}

// Book is a book in a grid or a row.
type Book struct {
	ID           int64     `json:"id"`
	LibraryID    int64     `json:"library_id"`
	Title        string    `json:"title"`
	Subtitle     *string   `json:"subtitle"`
	Author       string    `json:"author"`
	Narrator     *string   `json:"narrator"`
	Series       *string   `json:"series"`
	SeriesIndex  *float64  `json:"series_index"`
	Year         *int64    `json:"year"`
	Genre        *string   `json:"genre"`
	DurationS    float64   `json:"duration_s"`
	FileCount    int       `json:"file_count"`
	ChapterCount int       `json:"chapter_count"`
	HasCover     bool      `json:"has_cover"`
	AddedAt      int64     `json:"added_at"`
	Progress     *Progress `json:"progress"`
}

// File is one file of a book, in play order. StartS is where it begins in
// the whole book.
type File struct {
	ID        int64   `json:"id"`
	Index     int     `json:"index"`
	Title     string  `json:"title"`
	StartS    float64 `json:"start_s"`
	DurationS float64 `json:"duration_s"`
	Container *string `json:"container"`
	Codec     *string `json:"codec"`
	Bitrate   *int64  `json:"bitrate"`
}

// Chapter is one entry of a book's table of contents, in seconds from the
// start of the whole book. FileID is the file it starts in.
type Chapter struct {
	Index  int     `json:"index"`
	Title  string  `json:"title"`
	StartS float64 `json:"start_s"`
	EndS   float64 `json:"end_s"`
	FileID int64   `json:"file_id"`
}

// BookDetail is a book's page: the book, what it says about itself, its files
// and its chapters.
type BookDetail struct {
	Book
	Date        *string   `json:"date"`
	Publisher   *string   `json:"publisher"`
	Description *string   `json:"description"`
	Files       []File    `json:"files"`
	Chapters    []Chapter `json:"chapters"`
}

// Facet is one author or series with how many books it has.
type Facet struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func (c *Catalog) now() int64 {
	if c.Now != nil {
		return c.Now()
	}
	return nowMs()
}

// libCond limits an alias's library_id to the caller's grants. A lent
// tape's borrower sees its one film and no books at all.
func libCond(pol policy.Policy, alias string) (string, []any) {
	if pol.Borrower {
		return "0", nil
	}
	if pol.IsAdmin {
		return "1", nil
	}
	if len(pol.LibraryIDs) == 0 {
		return "0", nil
	}
	args := make([]any, len(pol.LibraryIDs))
	for i, id := range pol.LibraryIDs {
		args[i] = id
	}
	return alias + ".library_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")", args
}

// audiobookLibrary fails with ErrNotFound unless the library exists, holds
// audiobooks, and is granted.
func (c *Catalog) audiobookLibrary(ctx context.Context, pol policy.Policy, id int64) error {
	if pol.Borrower || !pol.CanSeeLibrary(id) {
		return ErrNotFound
	}
	var kind string
	err := c.DB.QueryRowContext(ctx, `SELECT kind FROM libraries WHERE id = ?`, id).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && kind != "audiobook") {
		return ErrNotFound
	}
	return err
}

func secs(ms int64) float64 { return math.Round(float64(ms)) / 1000 }

const bookColumns = `b.id, b.library_id, b.title, b.subtitle, b.author, b.narrator, b.series, b.series_index, b.year, b.genre,
	b.duration_ms,
	(SELECT COUNT(*) FROM audiobook_files f WHERE f.book_id = b.id AND f.missing_since IS NULL),
	(SELECT COUNT(*) FROM audiobook_chapters ch WHERE ch.book_id = b.id),
	b.cover_source IS NOT NULL, b.added_at, p.position_s, p.finished, p.updated_at`

// bookFrom joins a book to the caller's progress in it.
const bookFrom = ` FROM audiobook_books b LEFT JOIN audiobook_progress p ON p.book_id = b.id AND p.user_id = ?`

func scanBook(rows interface{ Scan(...any) error }, b *Book) error {
	var dur int64
	var pos sql.NullFloat64
	var fin sql.NullBool
	var upd sql.NullInt64
	if err := rows.Scan(&b.ID, &b.LibraryID, &b.Title, &b.Subtitle, &b.Author, &b.Narrator, &b.Series, &b.SeriesIndex, &b.Year, &b.Genre,
		&dur, &b.FileCount, &b.ChapterCount, &b.HasCover, &b.AddedAt, &pos, &fin, &upd); err != nil {
		return err
	}
	b.DurationS = secs(dur)
	if upd.Valid {
		b.Progress = &Progress{PositionS: pos.Float64, Finished: fin.Bool, UpdatedAt: upd.Int64}
	}
	return nil
}

func (c *Catalog) books(ctx context.Context, pol policy.Policy, userID int64, where string, args []any, order string, limit, offset int) ([]Book, error) {
	lc, largs := libCond(pol, "b")
	q := `SELECT ` + bookColumns + bookFrom + ` WHERE b.missing_since IS NULL AND ` + lc + ` AND ` + where + ` ORDER BY ` + order
	all := append(append([]any{userID}, largs...), args...)
	if limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		all = append(all, limit, offset)
	}
	rows, err := c.DB.QueryContext(ctx, q, all...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Book{}
	for rows.Next() {
		var b Book
		if err := scanBook(rows, &b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BookQuery pages through a library's books.
type BookQuery struct {
	// Sort is title (the default), author, series, added (newest first) or
	// recent (most recently listened first).
	Sort string
	// Author and Series keep only books by that author or in that series.
	Author, Series string
	// State is in_progress, finished or unplayed; empty keeps every book.
	State         string
	Limit, Offset int
}

// BookList is a page of books with the library's authors and series, for
// filtering.
type BookList struct {
	Books   []Book  `json:"books"`
	Total   int     `json:"total"`
	Authors []Facet `json:"authors"`
	Series  []Facet `json:"series"`
}

// inProgress is the condition for a book the caller has started and not
// finished.
const inProgress = `p.finished = 0 AND p.position_s > 0`

// Books lists a library's books.
func (c *Catalog) Books(ctx context.Context, pol policy.Policy, userID, libraryID int64, q BookQuery) (*BookList, error) {
	if err := c.audiobookLibrary(ctx, pol, libraryID); err != nil {
		return nil, err
	}
	where, args := "b.library_id = ?", []any{libraryID}
	if q.Author != "" {
		where += " AND b.author = ?"
		args = append(args, q.Author)
	}
	if q.Series != "" {
		where += " AND b.series = ?"
		args = append(args, q.Series)
	}
	switch q.State {
	case "in_progress":
		where += " AND " + inProgress
	case "finished":
		where += " AND p.finished = 1"
	case "unplayed":
		where += " AND (p.user_id IS NULL OR (p.finished = 0 AND p.position_s = 0))"
	}
	order := map[string]string{
		"author": "b.author_sort COLLATE NOCASE, b.series COLLATE NOCASE, b.series_index, b.sort_title COLLATE NOCASE",
		"series": "b.series IS NULL, b.series COLLATE NOCASE, b.series_index, b.sort_title COLLATE NOCASE",
		"added":  "b.added_at DESC, b.id DESC",
		"recent": "p.updated_at IS NULL, p.updated_at DESC, b.sort_title COLLATE NOCASE",
	}[q.Sort]
	if order == "" {
		order = "b.sort_title COLLATE NOCASE, b.id"
	}
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	list, err := c.books(ctx, pol, userID, where, args, order, limit, max(0, q.Offset))
	if err != nil {
		return nil, err
	}
	out := &BookList{Books: list}
	if err := c.DB.QueryRowContext(ctx, `SELECT COUNT(*)`+bookFrom+` WHERE b.missing_since IS NULL AND `+where,
		append([]any{userID}, args...)...).Scan(&out.Total); err != nil {
		return nil, err
	}
	if out.Authors, err = c.facet(ctx, libraryID, "author", "MIN(author_sort) COLLATE NOCASE"); err != nil {
		return nil, err
	}
	if out.Series, err = c.facet(ctx, libraryID, "series", "series COLLATE NOCASE"); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Catalog) facet(ctx context.Context, libraryID int64, col, order string) ([]Facet, error) {
	rows, err := c.DB.QueryContext(ctx, `SELECT `+col+`, COUNT(*) FROM audiobook_books
		WHERE library_id = ? AND missing_since IS NULL AND `+col+` IS NOT NULL GROUP BY `+col+` ORDER BY `+order, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Facet{}
	for rows.Next() {
		var f Facet
		if err := rows.Scan(&f.Name, &f.Count); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Continue lists the books the caller is partway through, most recently
// listened first, from every audiobook library they may see: Home's
// "Continue listening" row.
func (c *Catalog) Continue(ctx context.Context, pol policy.Policy, userID int64, limit int) ([]Book, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return c.books(ctx, pol, userID, inProgress+` AND EXISTS (SELECT 1 FROM libraries l WHERE l.id = b.library_id AND l.kind = 'audiobook')`,
		nil, "p.updated_at DESC, b.id", limit, 0)
}

// Book is a book's page for a caller who may see it.
func (c *Catalog) Book(ctx context.Context, pol policy.Policy, userID, id int64) (*BookDetail, error) {
	lc, largs := libCond(pol, "b")
	d := &BookDetail{}
	row := c.DB.QueryRowContext(ctx, `SELECT `+bookColumns+`, b.date, b.publisher, b.description`+bookFrom+`
		JOIN libraries l ON l.id = b.library_id
		WHERE b.id = ? AND b.missing_since IS NULL AND l.kind = 'audiobook' AND `+lc, append([]any{userID, id}, largs...)...)
	var dur int64
	var pos sql.NullFloat64
	var fin sql.NullBool
	var upd sql.NullInt64
	b := &d.Book
	err := row.Scan(&b.ID, &b.LibraryID, &b.Title, &b.Subtitle, &b.Author, &b.Narrator, &b.Series, &b.SeriesIndex, &b.Year, &b.Genre,
		&dur, &b.FileCount, &b.ChapterCount, &b.HasCover, &b.AddedAt, &pos, &fin, &upd, &d.Date, &d.Publisher, &d.Description)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	b.DurationS = secs(dur)
	if upd.Valid {
		b.Progress = &Progress{PositionS: pos.Float64, Finished: fin.Bool, UpdatedAt: upd.Int64}
	}
	if d.Files, err = c.files(ctx, id); err != nil {
		return nil, err
	}
	rows, err := c.DB.QueryContext(ctx, `SELECT idx, title, start_ms, end_ms, COALESCE(file_id, 0) FROM audiobook_chapters WHERE book_id = ? ORDER BY idx`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	d.Chapters = []Chapter{}
	for rows.Next() {
		var ch Chapter
		var start, end int64
		if err := rows.Scan(&ch.Index, &ch.Title, &start, &end, &ch.FileID); err != nil {
			return nil, err
		}
		ch.StartS, ch.EndS = secs(start), secs(end)
		d.Chapters = append(d.Chapters, ch)
	}
	return d, rows.Err()
}

// files are a book's present files in play order.
func (c *Catalog) files(ctx context.Context, bookID int64) ([]File, error) {
	rows, err := c.DB.QueryContext(ctx, `SELECT id, position, title, start_ms, COALESCE(duration_ms, 0), container, codec, bitrate
		FROM audiobook_files WHERE book_id = ? AND missing_since IS NULL ORDER BY position, id`, bookID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []File{}
	for rows.Next() {
		var f File
		var start, dur int64
		if err := rows.Scan(&f.ID, &f.Index, &f.Title, &start, &dur, &f.Container, &f.Codec, &f.Bitrate); err != nil {
			return nil, err
		}
		f.StartS, f.DurationS = secs(start), secs(dur)
		out = append(out, f)
	}
	return out, rows.Err()
}

// visible fails with ErrNotFound unless the caller may see the book, and
// returns its length in seconds.
func (c *Catalog) visible(ctx context.Context, pol policy.Policy, bookID int64) (float64, error) {
	lc, largs := libCond(pol, "b")
	var dur int64
	err := c.DB.QueryRowContext(ctx, `SELECT b.duration_ms FROM audiobook_books b JOIN libraries l ON l.id = b.library_id
		WHERE b.id = ? AND b.missing_since IS NULL AND l.kind = 'audiobook' AND `+lc, append([]any{bookID}, largs...)...).Scan(&dur)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return secs(dur), err
}

// Progress is the caller's place in a book: zero, unfinished, when they have
// not started it.
func (c *Catalog) Progress(ctx context.Context, pol policy.Policy, userID, bookID int64) (*Progress, error) {
	if _, err := c.visible(ctx, pol, bookID); err != nil {
		return nil, err
	}
	p := &Progress{}
	err := c.DB.QueryRowContext(ctx, `SELECT position_s, finished, updated_at FROM audiobook_progress WHERE user_id = ? AND book_id = ?`,
		userID, bookID).Scan(&p.PositionS, &p.Finished, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	return p, err
}

// SetProgress records the caller's place in a book. The position is clamped
// to the book; reaching its last second finishes it, unless finished says
// otherwise.
func (c *Catalog) SetProgress(ctx context.Context, pol policy.Policy, userID, bookID int64, positionS float64, finished *bool) (*Progress, error) {
	dur, err := c.visible(ctx, pol, bookID)
	if err != nil {
		return nil, err
	}
	pos, done := ClampPosition(positionS, dur)
	if finished != nil {
		done = *finished
	}
	p := &Progress{PositionS: pos, Finished: done, UpdatedAt: c.now()}
	_, err = c.DB.ExecContext(ctx, `INSERT INTO audiobook_progress (user_id, book_id, position_s, finished, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id, book_id) DO UPDATE SET position_s = excluded.position_s, finished = excluded.finished, updated_at = excluded.updated_at`,
		userID, bookID, p.PositionS, p.Finished, p.UpdatedAt)
	return p, err
}

// SetFinished marks a book finished or not. Either way the next listen starts
// from the beginning.
func (c *Catalog) SetFinished(ctx context.Context, pol policy.Policy, userID, bookID int64, finished bool) (*Progress, error) {
	return c.SetProgress(ctx, pol, userID, bookID, 0, &finished)
}

// ClampPosition keeps a position inside a book of dur seconds, and reports
// whether it is at the end (within a second of it).
func ClampPosition(posS, durS float64) (float64, bool) {
	if math.IsNaN(posS) || posS < 0 {
		posS = 0
	}
	if durS > 0 && posS > durS {
		posS = durS
	}
	posS = math.Round(posS*1000) / 1000
	return posS, durS > 0 && posS >= durS-1
}

// Locate finds where a position in a book falls: the index of the file it is
// in and the offset into that file, both in seconds. starts and durations are
// the files' start offsets and lengths. A position past the end lands at the
// end of the last file.
func Locate(starts, durations []float64, posS float64) (file int, offsetS float64) {
	if len(starts) == 0 {
		return 0, 0
	}
	posS = max(0, posS)
	for i := len(starts) - 1; i >= 0; i-- {
		if posS >= starts[i] {
			file = i
			break
		}
	}
	offsetS = min(posS-starts[file], durations[file])
	return file, offsetS
}

// FileSource is a file the caller may play: where it is and what it holds.
type FileSource struct {
	FileID     int64
	BookID     int64
	LibraryID  int64
	Path, Root string
	Title      string
	BookTitle  string
	Author     string
	Codec      string
	Container  string
	DurationMs int64
	Bitrate    int64
	SampleRate int64
}

// Source loads a file the caller may play.
func (c *Catalog) Source(ctx context.Context, pol policy.Policy, fileID int64) (*FileSource, error) {
	lc, largs := libCond(pol, "f")
	f := &FileSource{FileID: fileID}
	var codec, container sql.NullString
	var dur, br, sr sql.NullInt64
	err := c.DB.QueryRowContext(ctx, `SELECT f.book_id, f.library_id, f.path, l.root_path, f.title, b.title, b.author, f.codec, f.container,
			f.duration_ms, f.bitrate, f.sample_rate
		FROM audiobook_files f JOIN audiobook_books b ON b.id = f.book_id JOIN libraries l ON l.id = f.library_id
		WHERE f.id = ? AND f.missing_since IS NULL AND l.kind = 'audiobook' AND `+lc, append([]any{fileID}, largs...)...).
		Scan(&f.BookID, &f.LibraryID, &f.Path, &f.Root, &f.Title, &f.BookTitle, &f.Author, &codec, &container, &dur, &br, &sr)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	f.Codec, f.Container, f.DurationMs, f.Bitrate, f.SampleRate = codec.String, container.String, dur.Int64, br.Int64, sr.Int64
	return f, nil
}

// CoverSource is where a book's picture is: a file relative to Root, which
// may be an image or an audio file with one embedded.
func (c *Catalog) CoverSource(ctx context.Context, pol policy.Policy, bookID int64) (root, rel string, err error) {
	lc, largs := libCond(pol, "b")
	var p sql.NullString
	err = c.DB.QueryRowContext(ctx, `SELECT l.root_path, b.cover_path FROM audiobook_books b JOIN libraries l ON l.id = b.library_id
		WHERE b.id = ? AND b.missing_since IS NULL AND l.kind = 'audiobook' AND `+lc, append([]any{bookID}, largs...)...).Scan(&root, &p)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !p.Valid) {
		return "", "", ErrNotFound
	}
	return root, p.String, err
}
