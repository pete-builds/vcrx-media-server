package library

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/probe"
)

func bf(rel string, durMs int64, tags map[string]string, chapters ...probe.Chapter) *bookFile {
	if tags == nil {
		tags = map[string]string{}
	}
	return &bookFile{rel: rel, abs: "/books/" + rel, durationMs: durMs, tags: tags, chapters: chapters}
}

func bookByKey(t *testing.T, books []*bookPlan, key string) *bookPlan {
	t.Helper()
	for _, b := range books {
		if b.key == key {
			return b
		}
	}
	var keys []string
	for _, b := range books {
		keys = append(keys, b.key)
	}
	t.Fatalf("no book %q among %v", key, keys)
	return nil
}

// Every layout the brief names: Author/Book as one file with chapters,
// Author/Series/NN - Book as a folder of files, a loose file at the root, and
// an author's folder of single-file books.
func TestGroupBooksLayouts(t *testing.T) {
	t.Parallel()
	files := []*bookFile{
		bf("A. Author/The Sample Book/The Sample Book.m4b", 3000, map[string]string{"title": "The Sample Book", "artist": "A. Author", "composer": "N. Narrator"},
			probe.Chapter{Title: "Opening", StartMs: 0, EndMs: 1000}, probe.Chapter{Title: "Middle", StartMs: 1000, EndMs: 2000},
			probe.Chapter{Title: "Ending", StartMs: 2000, EndMs: 3000}),
		bf("B. Writer/Sample Series/02 - Second Steps/Part 10.mp3", 2000, nil),
		bf("B. Writer/Sample Series/02 - Second Steps/Part 2.mp3", 1500, nil),
		bf("B. Writer/Sample Series/02 - Second Steps/Part 1.mp3", 1000, nil),
		bf("Loose Tale.mp3", 500, map[string]string{"album": "A Loose Tale", "artist": "C. Teller"}),
		bf("D. Penner/First.m4b", 1000, nil),
		bf("D. Penner/Second.m4b", 1000, nil),
	}
	folders := map[string]*bookFolder{
		"A. Author/The Sample Book": {rel: "A. Author/The Sample Book", images: []string{"cover.jpg"}},
		"B. Writer/Sample Series/02 - Second Steps": {rel: "B. Writer/Sample Series/02 - Second Steps", desc: "A synthetic description.",
			reader: "R. Reader"},
	}
	books := groupBooks(files, folders)
	if len(books) != 5 {
		t.Fatalf("got %d books, want 5", len(books))
	}

	m4b := bookByKey(t, books, "dir:A. Author/The Sample Book")
	if m4b.title != "The Sample Book" || m4b.author != "A. Author" || m4b.narrator != "N. Narrator" {
		t.Errorf("m4b book: %q by %q read by %q", m4b.title, m4b.author, m4b.narrator)
	}
	if len(m4b.chapters) != 3 || m4b.chapters[1].title != "Middle" || m4b.chapters[1].startMs != 1000 || m4b.durationMs != 3000 {
		t.Errorf("m4b chapters: %+v, duration %d", m4b.chapters, m4b.durationMs)
	}
	if m4b.coverSource != "file" || m4b.coverPath != "A. Author/The Sample Book/cover.jpg" {
		t.Errorf("m4b cover: %s %s", m4b.coverSource, m4b.coverPath)
	}

	multi := bookByKey(t, books, "dir:B. Writer/Sample Series/02 - Second Steps")
	var order []string
	for _, f := range multi.files {
		order = append(order, filepath.Base(f.rel))
	}
	if strings.Join(order, ",") != "Part 1.mp3,Part 2.mp3,Part 10.mp3" {
		t.Errorf("multi-file order: %v", order)
	}
	if multi.title != "Second Steps" || multi.author != "B. Writer" || multi.series != "Sample Series" ||
		multi.seriesIndex == nil || *multi.seriesIndex != 2 {
		t.Errorf("multi-file book: %q by %q, series %q #%v", multi.title, multi.author, multi.series, multi.seriesIndex)
	}
	if multi.narrator != "R. Reader" || multi.description != "A synthetic description." {
		t.Errorf("reader.txt and desc.txt: %q %q", multi.narrator, multi.description)
	}
	// Each file a chapter, laid end to end.
	want := []struct {
		title      string
		start, end int64
	}{{"Part 1", 0, 1000}, {"Part 2", 1000, 2500}, {"Part 10", 2500, 4500}}
	if len(multi.chapters) != len(want) {
		t.Fatalf("multi-file chapters: %+v", multi.chapters)
	}
	for i, w := range want {
		c := multi.chapters[i]
		if c.title != w.title || c.startMs != w.start || c.endMs != w.end || c.file != i {
			t.Errorf("chapter %d: %+v, want %+v", i, c, w)
		}
	}

	loose := bookByKey(t, books, "file:Loose Tale.mp3")
	if loose.title != "A Loose Tale" || loose.author != "C. Teller" || len(loose.chapters) != 1 {
		t.Errorf("loose file: %q by %q, %d chapters", loose.title, loose.author, len(loose.chapters))
	}
	first := bookByKey(t, books, "file:D. Penner/First.m4b")
	if first.title != "First" || first.author != "D. Penner" {
		t.Errorf("single-file book in an author folder: %q by %q", first.title, first.author)
	}
}

// Disc and track tags decide the order before file names; a book in disc
// folders is one book.
func TestBookFilesOrderByDiscAndTrack(t *testing.T) {
	t.Parallel()
	files := []*bookFile{
		bf("E. Scribe/Long Book/CD2/01.mp3", 1000, map[string]string{"track": "1"}),
		bf("E. Scribe/Long Book/CD1/02.mp3", 1000, map[string]string{"track": "2/2"}),
		bf("E. Scribe/Long Book/CD1/01.mp3", 1000, map[string]string{"track": "1/2"}),
	}
	books := groupBooks(files, map[string]*bookFolder{})
	if len(books) != 1 {
		t.Fatalf("got %d books, want one across both disc folders", len(books))
	}
	var order []string
	for _, f := range books[0].files {
		order = append(order, f.rel)
	}
	if strings.Join(order, ",") != "E. Scribe/Long Book/CD1/01.mp3,E. Scribe/Long Book/CD1/02.mp3,E. Scribe/Long Book/CD2/01.mp3" {
		t.Errorf("order: %v", order)
	}
}

// metadata.json, as Audiobookshelf writes it, wins over tags and folders.
func TestBookMetadataJSON(t *testing.T) {
	t.Parallel()
	meta := parseBookMeta([]byte(`{"title":"The Sample Book","authors":["A. Author","Z. Second"],"narrators":["N. Narrator"],
		"series":["Sample Saga #3"],"publishedYear":"2001","description":"Invented.","genres":["Fiction"]}`))
	files := []*bookFile{bf("Somebody/Folder Name/01.mp3", 1000, nil), bf("Somebody/Folder Name/02.mp3", 1000, nil)}
	b := groupBooks(files, map[string]*bookFolder{"Somebody/Folder Name": {meta: meta}})[0]
	if b.title != "The Sample Book" || b.author != "A. Author, Z. Second" || b.narrator != "N. Narrator" || b.series != "Sample Saga" ||
		b.seriesIndex == nil || *b.seriesIndex != 3 || b.year != 2001 || b.description != "Invented." || b.genre != "Fiction" {
		t.Errorf("metadata.json: %+v", b)
	}
}

func TestNaturalLess(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a, b string
		want bool
	}{{"Part 2", "Part 10", true}, {"Part 10", "Part 2", false}, {"a", "B", true}, {"ch01", "ch1x", true}, {"x", "x", false}, {"09", "10", true}} {
		if got := NaturalLess(c.a, c.b); got != c.want {
			t.Errorf("NaturalLess(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestAuthorSort(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"A. Author": "Author, A.", "Plato": "Plato", "B. Writer, C. Other": "Writer, B.", unknownAuthor: unknownAuthor} {
		if got := authorSort(in); got != want {
			t.Errorf("authorSort(%q) = %q, want %q", in, got, want)
		}
	}
}

// A real scan through ffprobe: an M4B's own chapters, a folder of MP3s, and
// a rescan that probes nothing again.
func TestScanAudiobooks(t *testing.T) {
	t.Parallel()
	ff := needFFmpeg(t)
	root := t.TempDir()
	meta := filepath.Join(t.TempDir(), "meta.txt")
	if err := os.WriteFile(meta, []byte(";FFMETADATA1\ntitle=The Sample Book\nartist=A. Author\ncomposer=N. Narrator\n"+
		"[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=1000\ntitle=Opening\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=1000\nEND=2000\ntitle=Middle\n"+
		"[CHAPTER]\nTIMEBASE=1/1000\nSTART=2000\nEND=3000\ntitle=Ending\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m4b := filepath.Join(root, "A. Author", "The Sample Book", "The Sample Book.m4b")
	if err := os.MkdirAll(filepath.Dir(m4b), 0o755); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command(ff, "-loglevel", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=300:duration=3", "-i", meta,
		"-map", "0:a", "-map_metadata", "1", "-map_chapters", "1", "-c:a", "aac", "-f", "mp4", m4b).CombinedOutput(); err != nil {
		t.Fatalf("m4b: %v %s", err, b)
	}
	for i, title := range []string{"Arrival", "Departure"} {
		tone(t, ff, root, "B. Writer/Second Steps/0"+string(rune('1'+i))+" "+title+".mp3", 400+50*i, "libmp3lame",
			map[string]string{"title": title, "album": "Second Steps", "artist": "B. Writer", "track": strconv.Itoa(i + 1)}, "")
	}
	conn := newDB(t)
	lib := addLibrary(t, conn, "Audiobooks", KindAudiobook, root, "folder")
	pr := &countingProber{}
	sc := &Scanner{DB: conn, Prober: pr, Log: quiet}
	run, err := sc.Scan(context.Background(), lib, Options{Trigger: "cli"})
	if err != nil || run.Added != 3 {
		t.Fatalf("scan: %v, %+v", err, run)
	}
	rows, err := conn.Query(`SELECT b.title, b.author, COALESCE(b.narrator, ''), b.duration_ms, ch.idx, ch.title, ch.start_ms, ch.end_ms
		FROM audiobook_books b JOIN audiobook_chapters ch ON ch.book_id = b.id ORDER BY b.title, ch.idx`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var title, author, narrator, ch string
		var dur, idx, start, end int64
		if err := rows.Scan(&title, &author, &narrator, &dur, &idx, &ch, &start, &end); err != nil {
			t.Fatal(err)
		}
		got = append(got, title+"|"+author+"|"+narrator+"|"+ch+"|"+strconv.FormatInt(start/100, 10)+"-"+strconv.FormatInt(end/100, 10))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// MP3 durations carry the encoder's padding, so they are compared in
	// tenths of a second.
	want := []string{
		"Second Steps|B. Writer||Arrival|0-10", "Second Steps|B. Writer||Departure|10-20",
		"The Sample Book|A. Author|N. Narrator|Opening|0-10", "The Sample Book|A. Author|N. Narrator|Middle|10-20",
		"The Sample Book|A. Author|N. Narrator|Ending|20-30",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("books and chapters:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	before := pr.total()
	if _, err := sc.Scan(context.Background(), lib, Options{Trigger: "cli"}); err != nil {
		t.Fatal(err)
	}
	if pr.total() != before {
		t.Errorf("a rescan of unchanged files probed %d of them again", pr.total()-before)
	}
	// An emptied root changes nothing.
	if err := os.RemoveAll(filepath.Join(root, "A. Author")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "B. Writer")); err != nil {
		t.Fatal(err)
	}
	if _, err := sc.Scan(context.Background(), lib, Options{Trigger: "cli"}); err == nil {
		t.Error("a scan of an emptied root went ahead")
	}
	var missing int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM audiobook_files WHERE missing_since IS NOT NULL`).Scan(&missing)
	if missing != 0 {
		t.Errorf("an aborted scan marked %d files missing", missing)
	}
}
