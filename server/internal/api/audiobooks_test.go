package api

import (
	"context"
	"fmt"
	"math"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/audiobook"
	"github.com/pete-builds/vcrx-media-server/server/internal/library"
	"github.com/pete-builds/vcrx-media-server/server/internal/music"
	mediaprobe "github.com/pete-builds/vcrx-media-server/server/internal/probe"
	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
)

type bookIDs struct {
	library, m4b, multi int64
	// m4bFile is the M4B's one file; multiFiles the folder book's, in order.
	m4bFile    int64
	multiFiles []int64
}

// withAudiobooks adds a scanned audiobook library to the fixture: "The Sample
// Book", one M4B of three one-second chapters with a cover.jpg beside it,
// and "Second Steps", two MP3 files that are a chapter each. The friend is
// not granted it.
func withAudiobooks(t *testing.T, f *fixture) bookIDs {
	t.Helper()
	ff, err1 := osexec.LookPath("ffmpeg")
	_, err2 := osexec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("ffmpeg and ffprobe must be installed in CI")
		}
		t.Skip("ffmpeg or ffprobe not on PATH")
	}
	root := filepath.Join(filepath.Dir(f.root), "Audiobooks")
	run := func(args ...string) {
		t.Helper()
		if b, err := osexec.Command(ff, append([]string{"-loglevel", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v %s", err, b)
		}
	}
	book := filepath.Join(root, "A. Author", "The Sample Book")
	multi := filepath.Join(root, "B. Writer", "Second Steps")
	for _, d := range []string{book, multi} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	meta := filepath.Join(t.TempDir(), "meta.txt")
	if err := os.WriteFile(meta, []byte(";FFMETADATA1\ntitle=The Sample Book\nartist=A. Author\ncomposer=N. Narrator\n"+
		"[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=1000\ntitle=Opening\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=1000\nEND=2000\ntitle=Middle\n"+
		"[CHAPTER]\nTIMEBASE=1/1000\nSTART=2000\nEND=3000\ntitle=Ending\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("-f", "lavfi", "-i", "sine=frequency=300:duration=3", "-i", meta, "-map", "0:a", "-map_metadata", "1", "-map_chapters", "1",
		"-c:a", "aac", "-f", "mp4", filepath.Join(book, "The Sample Book.m4b"))
	run("-f", "lavfi", "-i", "color=c=0x1b3a6b:s=300x300", "-frames:v", "1", filepath.Join(book, "cover.jpg"))
	for i, title := range []string{"Arrival", "Departure"} {
		run("-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=%d:duration=2", 400+50*i), "-metadata", "title="+title,
			"-metadata", "album=Second Steps", "-metadata", "artist=B. Writer", "-metadata", fmt.Sprintf("track=%d", i+1),
			"-c:a", "libmp3lame", filepath.Join(multi, fmt.Sprintf("0%d %s.mp3", i+1, title)))
	}
	now := time.Now().UnixMilli()
	res, err := f.db.Exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES ('Audiobooks', 'audiobook', ?, ?, ?)`, root, now, now)
	if err != nil {
		t.Fatal(err)
	}
	var ids bookIDs
	ids.library, _ = res.LastInsertId()
	sc := &library.Scanner{DB: f.db, Prober: &mediaprobe.FFprobe{}}
	if _, err := sc.Scan(context.Background(), library.Library{ID: ids.library, Name: "Audiobooks", Kind: library.KindAudiobook, RootPath: root},
		library.Options{Trigger: "cli"}); err != nil {
		t.Fatal(err)
	}
	q := func(sql string, dst *int64) {
		t.Helper()
		if err := f.db.QueryRow(sql).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	q(`SELECT id FROM audiobook_books WHERE title = 'The Sample Book'`, &ids.m4b)
	q(`SELECT id FROM audiobook_books WHERE title = 'Second Steps'`, &ids.multi)
	q(`SELECT id FROM audiobook_files WHERE book_id = (SELECT id FROM audiobook_books WHERE title = 'The Sample Book')`, &ids.m4bFile)
	ids.multiFiles = queryIDs(t, f, `SELECT id FROM audiobook_files WHERE book_id = ? ORDER BY position`, ids.multi)
	rmx := f.srv.Remux
	if rmx == nil {
		rmx = remux.New("ffmpeg", "ffprobe", 2, nil)
		f.srv.Remux = rmx
	}
	_ = rmx.DetectAudio(context.Background())
	bcat := &audiobook.Catalog{DB: f.db}
	f.srv.Audiobooks = &Audiobooks{Catalog: bcat, Player: &audiobook.Player{DB: f.db, Catalog: bcat, Remux: rmx},
		Covers: &music.Covers{FFmpeg: ff, CacheDir: filepath.Join(t.TempDir(), "book-covers")}}
	return ids
}

// The book page lists the M4B's own chapters and the folder book's files as
// chapters, laid end to end, and the queue gives each file a stream.
func TestAudiobookDetailAndQueue(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ids := withAudiobooks(t, f)
	var d audiobook.BookDetail
	r := f.do("GET", fmt.Sprintf("/api/v1/audiobooks/books/%d", ids.m4b), f.adminTok, nil)
	r.json(t, &d)
	if r.code != 200 || d.Author != "A. Author" || d.Narrator == nil || *d.Narrator != "N. Narrator" || !d.HasCover || len(d.Chapters) != 3 ||
		d.Chapters[2].Title != "Ending" || d.Chapters[2].StartS != 2 || d.Progress != nil {
		t.Fatalf("m4b book: %d %s", r.code, r.body)
	}
	f.do("GET", fmt.Sprintf("/api/v1/audiobooks/books/%d", ids.multi), f.adminTok, nil).json(t, &d)
	if len(d.Files) != 2 || len(d.Chapters) != 2 || d.Chapters[1].Title != "Departure" || d.Files[1].StartS != d.Files[0].DurationS ||
		d.Chapters[1].FileID != d.Files[1].ID {
		t.Fatalf("multi-file book: %+v", d)
	}
	var q audiobook.Queue
	r = f.do("POST", fmt.Sprintf("/api/v1/audiobooks/books/%d/queue", ids.multi), f.adminTok, map[string]any{"capabilities": musicBrowserCaps()})
	r.json(t, &q)
	if r.code != 200 || len(q.Files) != 2 || q.Files[0].Stream == nil || q.Files[0].Stream.Protocol != "progressive" ||
		!strings.HasSuffix(q.Files[0].Stream.URL, fmt.Sprintf("/audiobooks/files/%d/file", ids.multiFiles[0])) {
		t.Fatalf("queue: %d %s", r.code, r.body)
	}
	// The file plays with Range, and the cover is a JPEG.
	if r := f.do("GET", fmt.Sprintf("/api/v1/audiobooks/files/%d/file", ids.m4bFile), f.adminTok, nil, "Range", "bytes=0-99"); r.code != 206 {
		t.Errorf("range on the m4b: %d", r.code)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/audiobooks/books/%d/cover?width=96", ids.m4b), f.adminTok, nil); r.code != 200 {
		t.Errorf("cover: %d %s", r.code, r.body)
	}
	// The library list counts books.
	var libs struct {
		Libraries []struct {
			ID        int64  `json:"id"`
			Kind      string `json:"kind"`
			ItemCount int    `json:"item_count"`
		} `json:"libraries"`
	}
	f.do("GET", "/api/v1/libraries", f.adminTok, nil).json(t, &libs)
	found := false
	for _, l := range libs.Libraries {
		if l.ID == ids.library {
			found = l.Kind == "audiobook" && l.ItemCount == 2
		}
	}
	if !found {
		t.Errorf("libraries: %+v", libs)
	}
}

// Progress is one position across the whole book: saved, read back, listed
// in Continue listening, clamped to the book, and cleared by finishing.
func TestAudiobookProgress(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ids := withAudiobooks(t, f)
	path := fmt.Sprintf("/api/v1/audiobooks/books/%d/progress", ids.multi)
	var p audiobook.Progress
	f.do("GET", path, f.adminTok, nil).json(t, &p)
	if p.PositionS != 0 || p.Finished || p.UpdatedAt != 0 {
		t.Errorf("before any listen: %+v", p)
	}
	// 2.5 s is half a second into the second file.
	r := f.do("PUT", path, f.adminTok, map[string]any{"position_s": 2.5})
	r.json(t, &p)
	if r.code != 200 || p.PositionS != 2.5 || p.Finished || p.UpdatedAt == 0 {
		t.Fatalf("save: %d %s", r.code, r.body)
	}
	var d audiobook.BookDetail
	f.do("GET", fmt.Sprintf("/api/v1/audiobooks/books/%d", ids.multi), f.adminTok, nil).json(t, &d)
	starts, durs := []float64{}, []float64{}
	for _, fl := range d.Files {
		starts, durs = append(starts, fl.StartS), append(durs, fl.DurationS)
	}
	if file, off := audiobook.Locate(starts, durs, d.Progress.PositionS); file != 1 || math.Abs(off-(2.5-d.Files[1].StartS)) > 1e-9 {
		t.Errorf("2.5 s into the book is file %d at %v", file, off)
	}
	var cont struct {
		Books []audiobook.Book `json:"books"`
	}
	f.do("GET", "/api/v1/audiobooks/continue", f.adminTok, nil).json(t, &cont)
	if len(cont.Books) != 1 || cont.Books[0].ID != ids.multi || cont.Books[0].Progress == nil {
		t.Fatalf("continue listening: %+v", cont)
	}
	var list audiobook.BookList
	f.do("GET", fmt.Sprintf("/api/v1/audiobooks/libraries/%d/books?state=in_progress", ids.library), f.adminTok, nil).json(t, &list)
	if list.Total != 1 || list.Books[0].ID != ids.multi || len(list.Authors) != 2 {
		t.Errorf("in progress filter: %+v", list)
	}
	f.do("GET", fmt.Sprintf("/api/v1/audiobooks/libraries/%d/books?author=A.%%20Author", ids.library), f.adminTok, nil).json(t, &list)
	if list.Total != 1 || list.Books[0].ID != ids.m4b {
		t.Errorf("author filter: %+v", list)
	}
	// Past the end is the end, and finished.
	f.do("PUT", path, f.adminTok, map[string]any{"position_s": 9999}).json(t, &p)
	if !p.Finished || p.PositionS != d.DurationS {
		t.Errorf("past the end: %+v (book is %v s)", p, d.DurationS)
	}
	f.do("DELETE", fmt.Sprintf("/api/v1/audiobooks/books/%d/finished", ids.multi), f.adminTok, nil).json(t, &p)
	if p.Finished || p.PositionS != 0 {
		t.Errorf("unfinished: %+v", p)
	}
	f.do("PUT", path, f.adminTok, map[string]any{"position_s": 1}).json(t, &p)
	f.do("POST", fmt.Sprintf("/api/v1/audiobooks/books/%d/finished", ids.multi), f.adminTok, nil).json(t, &p)
	if !p.Finished || p.PositionS != 0 {
		t.Errorf("finished: %+v", p)
	}
	f.do("GET", "/api/v1/audiobooks/continue", f.adminTok, nil).json(t, &cont)
	if len(cont.Books) != 0 {
		t.Errorf("a finished book is still in Continue listening: %+v", cont)
	}
	if r := f.do("PUT", path, f.adminTok, map[string]any{}); r.code != 400 {
		t.Errorf("no position: %d", r.code)
	}
	// Progress is the caller's own: the friend, granted, sees none of it.
	grantAll(t, f)
	f.do("GET", path, f.friendTok, nil).json(t, &p)
	if p.Finished || p.PositionS != 0 {
		t.Errorf("the friend sees the admin's progress: %+v", p)
	}
}

// grantAll grants the friend every library.
func grantAll(t *testing.T, f *fixture) {
	t.Helper()
	var friendID int64
	if err := f.db.QueryRow(`SELECT id FROM users WHERE username = 'friend'`).Scan(&friendID); err != nil {
		t.Fatal(err)
	}
	if err := f.srv.Admin.SetGrants(context.Background(), friendID, queryIDs(t, f, `SELECT id FROM libraries`)); err != nil {
		t.Fatal(err)
	}
}

// queryIDs runs a query that selects one id column and returns the ids.
func queryIDs(t *testing.T, f *fixture, q string, args ...any) []int64 {
	t.Helper()
	rows, err := f.db.Query(q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// A friend without the grant gets 404 everywhere and an empty Continue
// listening; a video library is not an audiobook library even to the admin;
// and a server without audiobooks answers 404, not 500.
func TestAudiobooksFollowLibraryGrants(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ids := withAudiobooks(t, f)
	if _, err := f.db.Exec(`INSERT INTO audiobook_progress (user_id, book_id, position_s, updated_at)
		SELECT id, ?, 1, 1 FROM users WHERE username = 'friend'`, ids.multi); err != nil {
		t.Fatal(err)
	}
	var cont struct {
		Books []audiobook.Book `json:"books"`
	}
	r := f.do("GET", "/api/v1/audiobooks/continue", f.friendTok, nil)
	r.json(t, &cont)
	if r.code != 200 || len(cont.Books) != 0 {
		t.Errorf("continue without the grant: %d %s", r.code, r.body)
	}
	for _, path := range []string{fmt.Sprintf("/api/v1/audiobooks/libraries/%d/books", ids.library), fmt.Sprintf("/api/v1/audiobooks/books/%d", ids.m4b),
		fmt.Sprintf("/api/v1/audiobooks/books/%d/progress", ids.m4b), fmt.Sprintf("/api/v1/audiobooks/files/%d/file", ids.m4bFile)} {
		if r := f.do("GET", path, f.friendTok, nil); r.code != 404 {
			t.Errorf("without the grant: GET %s = %d", path, r.code)
		}
	}
	if r := f.do("GET", "/api/v1/audiobooks/libraries/1/books", f.adminTok, nil); r.code != 404 {
		t.Errorf("a movie library listed as audiobooks: %d", r.code)
	}
	grantAll(t, f)
	f.do("GET", "/api/v1/audiobooks/continue", f.friendTok, nil).json(t, &cont)
	if len(cont.Books) != 1 {
		t.Errorf("continue with the grant: %+v", cont)
	}
	f.srv.Audiobooks = nil
	if r := f.do("GET", fmt.Sprintf("/api/v1/audiobooks/books/%d", ids.m4b), f.adminTok, nil); r.code != 404 {
		t.Errorf("a server without audiobooks: %d", r.code)
	}
}
