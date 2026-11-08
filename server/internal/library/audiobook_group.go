package library

import (
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/pete-builds/vcrx-media-server/server/internal/probe"
)

// KindAudiobook is an audiobook library: Author/Book/, Author/Series/Book/,
// or loose files, each book one file with its own chapters or a folder of
// files played in order.
const KindAudiobook = "audiobook"

// unknownAuthor names an author when neither the tags nor the folders do.
const unknownAuthor = "Unknown Author"

// bookFile is one audio file as grouping sees it: where it is and what its
// tags and chapter marks say, from a fresh probe or from the stored row.
type bookFile struct {
	rel, abs     string
	size, mtime  int64
	tags         map[string]string
	chapters     []probe.Chapter
	container    string
	codec        string
	durationMs   int64
	bitrate      int64
	sampleRate   int
	channels     int
	embedded     bool
	probeStatus  string
	probeError   string
	probedAt     int64
	needsProbe   bool
	changed      bool
	missingSince int64
}

// bookFolder is what a folder holds besides audio: pictures, and the small
// text files Audiobookshelf and other tools leave beside a book.
type bookFolder struct {
	rel    string
	images []string
	// meta is metadata.json, already parsed; desc and reader are desc.txt
	// and reader.txt.
	meta   *bookMeta
	desc   string
	reader string
}

// bookMeta is the part of Audiobookshelf's metadata.json the scan uses. Every
// field is optional and wins over the tags when set.
type bookMeta struct {
	Title         string   `json:"title"`
	Subtitle      string   `json:"subtitle"`
	Authors       []string `json:"authors"`
	Narrators     []string `json:"narrators"`
	Series        []string `json:"series"`
	Genres        []string `json:"genres"`
	PublishedYear string   `json:"publishedYear"`
	PublishedDate string   `json:"publishedDate"`
	Publisher     string   `json:"publisher"`
	Description   string   `json:"description"`
}

// parseBookMeta reads metadata.json; nil when it is not one.
func parseBookMeta(b []byte) *bookMeta {
	var m bookMeta
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return &m
}

// bookPlan is one book as the scan will write it.
type bookPlan struct {
	key         string
	folder      string
	title       string
	subtitle    string
	author      string
	narrator    string
	series      string
	seriesIndex *float64
	year        int
	date        string
	genre       string
	publisher   string
	description string
	coverSource string
	coverPath   string
	files       []*bookFile
	chapters    []chapterPlan
	durationMs  int64
}

// chapterPlan is one entry of a book's table of contents. file is the index
// into the book's files of the file it starts in.
type chapterPlan struct {
	title          string
	startMs, endMs int64
	file           int
}

// bookExts are the files an audiobook scan indexes.
var bookExts = map[string]bool{".m4b": true, ".m4a": true, ".mp3": true, ".ogg": true, ".oga": true, ".opus": true, ".flac": true,
	".aac": true}

// IsBookAudio reports whether a file name is one an audiobook scan indexes.
func IsBookAudio(name string) bool {
	return bookExts[strings.ToLower(path.Ext(name))]
}

// bookFolderOf is the folder a file's book lives in: the file's own folder,
// or its parent when the file sits in a disc folder (CD1, Disc 2).
func bookFolderOf(rel string) string {
	dir := parentRel(rel)
	if dir != "" && strings.Contains(dir, "/") && DiscFolder(path.Base(dir)) > 0 {
		return parentRel(dir)
	}
	if dir != "" && !strings.Contains(dir, "/") && DiscFolder(path.Base(dir)) > 0 {
		return ""
	}
	return dir
}

// ownBook reports whether a file is a book on its own rather than a part of
// its folder's book: a loose file at the root, or one of several files in a
// folder that each carry their own chapters or are M4Bs (an author's folder
// of single-file books).
func ownBook(f *bookFile, siblings []*bookFile) bool {
	if bookFolderOf(f.rel) == "" {
		return true
	}
	if len(siblings) < 2 {
		return false
	}
	for _, s := range siblings {
		if len(s.chapters) < 2 && !strings.EqualFold(path.Ext(s.rel), ".m4b") {
			return false
		}
	}
	return true
}

// groupBooks turns every file the library holds into books. Files that are
// missing still take part, so a book keeps its shape while a file is away.
func groupBooks(files []*bookFile, folders map[string]*bookFolder) []*bookPlan {
	byFolder := map[string][]*bookFile{}
	for _, f := range files {
		byFolder[bookFolderOf(f.rel)] = append(byFolder[bookFolderOf(f.rel)], f)
	}
	plans := map[string]*bookPlan{}
	var keys []string
	add := func(key, folder string, f *bookFile) {
		p := plans[key]
		if p == nil {
			p = &bookPlan{key: key, folder: folder}
			plans[key] = p
			keys = append(keys, key)
		}
		p.files = append(p.files, f)
	}
	for folder, fs := range byFolder {
		for _, f := range fs {
			if ownBook(f, fs) {
				add("file:"+f.rel, folder, f)
			} else {
				add("dir:"+folder, folder, f)
			}
		}
	}
	sort.Strings(keys)
	out := make([]*bookPlan, 0, len(keys))
	for _, k := range keys {
		p := plans[k]
		orderBookFiles(p.files)
		p.describe(folders[p.folder])
		p.buildChapters()
		out = append(out, p)
	}
	return out
}

// orderBookFiles puts a book's files in play order: by disc and track tags,
// then by the disc folder and the file name, numbers compared as numbers.
func orderBookFiles(files []*bookFile) {
	type key struct{ disc, track int }
	keys := map[*bookFile]key{}
	for _, f := range files {
		t := squashTags(f.tags)
		disc := numberOf(t.get("disc", "discnumber", "tpos"))
		if disc == 0 {
			disc = DiscFolder(path.Base(parentRel(f.rel)))
		}
		keys[f] = key{disc, numberOf(t.get("track", "tracknumber", "trck"))}
	}
	sort.SliceStable(files, func(i, j int) bool {
		a, b := keys[files[i]], keys[files[j]]
		if a.disc != b.disc {
			return a.disc < b.disc
		}
		if a.track != b.track && a.track > 0 && b.track > 0 {
			return a.track < b.track
		}
		return NaturalLess(files[i].rel, files[j].rel)
	})
}

// NaturalLess compares two names with runs of digits read as numbers, so
// "Part 2" sorts before "Part 10". Case is ignored.
func NaturalLess(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		da, db := digitRun(a), digitRun(b)
		if da != "" && db != "" {
			na, nb := strings.TrimLeft(da, "0"), strings.TrimLeft(db, "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			a, b = a[len(da):], b[len(db):]
			continue
		}
		ra, rb := []rune(a)[0], []rune(b)[0]
		if ra != rb {
			return ra < rb
		}
		a, b = a[len(string(ra)):], b[len(string(rb)):]
	}
	return len(a) < len(b)
}

func digitRun(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// seriesIndexRe reads a leading book number from a folder name: "01 - Title",
// "Book 2 - Title", "2.5. Title".
var seriesIndexRe = regexp.MustCompile(`(?i)^(?:book|vol(?:ume)?\.?|part)?\s*(\d{1,3}(?:\.\d)?)\s*(?:[-.:)]\s*|\s+)(.+)$`)

// seriesNumRe splits "Name #3" or "Name, Book 3" into a series and its number.
var seriesNumRe = regexp.MustCompile(`(?i)^(.*?)(?:\s*#|,?\s+book\s+)(\d{1,3}(?:\.\d+)?)\s*$`)

// describe fills a book's title, author, series and the rest: metadata.json
// first, then the tags, then the folder layout.
func (p *bookPlan) describe(folder *bookFolder) {
	first := squashTags(p.files[0].tags)
	single := strings.HasPrefix(p.key, "file:")
	tag := func(keys ...string) string {
		for _, f := range p.files {
			if v := squashTags(f.tags).get(keys...); v != "" {
				return v
			}
		}
		return ""
	}
	if single {
		p.title = first.get("album", "title")
	} else {
		p.title = tag("album")
	}
	p.author = tag("albumartist", "artist", "author")
	p.narrator = tag("composer", "narrator", "narratedby", "performer")
	p.genre = tag("genre")
	p.date = dateOf(tag("date", "year", "originaldate", "releasedate"))
	p.year = yearOf(p.date)
	p.publisher = tag("publisher", "label", "copyright")
	p.description = tag("description", "synopsis", "longdescription", "comment", "desc")
	p.series = tag("series", "mvnm", "seriesname")
	if v := tag("seriespart", "mvin", "seriesindex", "part"); v != "" {
		if n, err := strconv.ParseFloat(strings.TrimSpace(strings.Split(v, "/")[0]), 64); err == nil && n >= 0 {
			p.seriesIndex = &n
		}
	}

	// The layout: Author/Book or Author/Series/Book. A loose file at the root
	// names neither.
	parts := []string{}
	if p.folder != "" {
		parts = strings.Split(p.folder, "/")
	}
	bookName := ""
	if single {
		bookName = stripExt(path.Base(p.files[0].rel))
	} else if len(parts) > 0 {
		bookName = parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}
	if m := seriesIndexRe.FindStringSubmatch(bookName); m != nil && len(parts) >= 2 {
		if n, err := strconv.ParseFloat(m[1], 64); err == nil && p.seriesIndex == nil {
			p.seriesIndex = &n
		}
		bookName = strings.TrimSpace(m[2])
	}
	if p.author == "" && len(parts) > 0 {
		p.author = parts[0]
	}
	if p.series == "" && len(parts) >= 2 {
		p.series = parts[1]
	}
	if p.title == "" {
		p.title = bookName
	}

	if folder != nil {
		if folder.reader != "" && p.narrator == "" {
			p.narrator = folder.reader
		}
		if folder.desc != "" {
			p.description = folder.desc
		}
		if m := folder.meta; m != nil && !single {
			set := func(dst *string, v string) {
				if v = strings.TrimSpace(v); v != "" {
					*dst = v
				}
			}
			set(&p.title, m.Title)
			set(&p.subtitle, m.Subtitle)
			set(&p.author, strings.Join(m.Authors, ", "))
			set(&p.narrator, strings.Join(m.Narrators, ", "))
			set(&p.genre, strings.Join(m.Genres, ", "))
			set(&p.publisher, m.Publisher)
			set(&p.description, m.Description)
			if d := dateOf(m.PublishedDate); d != "" {
				p.date = d
			} else if d := dateOf(m.PublishedYear); d != "" {
				p.date = d
			}
			p.year = yearOf(p.date)
			if len(m.Series) > 0 {
				p.series, p.seriesIndex = splitSeries(m.Series[0])
			}
		}
	}
	if p.title == "" {
		p.title = "Untitled"
	}
	if p.author == "" {
		p.author = unknownAuthor
	}
	p.description = clipText(strings.TrimSpace(p.description), 8000)

	// The cover: a picture in the folder, then one inside a file.
	if folder != nil && !single {
		if img := coverImage(folder.images); img != "" {
			p.coverSource, p.coverPath = "file", joinRel(p.folder, img)
		}
	}
	if single && folder != nil {
		// A folder of single-file books shares one folder; a picture named
		// like the book is its cover.
		stem := stripExt(path.Base(p.files[0].rel))
		if img := pickImage(folder.images, []string{stem}); img != "" {
			p.coverSource, p.coverPath = "file", joinRel(p.folder, img)
		} else if bookFolderOf(p.files[0].rel) != "" && len(folder.images) > 0 && p.coverPath == "" && !p.files[0].embedded {
			if img := coverImage(folder.images); img != "" {
				p.coverSource, p.coverPath = "file", joinRel(p.folder, img)
			}
		}
	}
	if p.coverPath == "" {
		for _, f := range p.files {
			if f.embedded && f.missingSince == 0 {
				p.coverSource, p.coverPath = "embedded", f.rel
				break
			}
		}
	}
}

// splitSeries reads "Name #3" into the series name and its number.
func splitSeries(s string) (string, *float64) {
	s = strings.TrimSpace(s)
	if m := seriesNumRe.FindStringSubmatch(s); m != nil {
		if n, err := strconv.ParseFloat(m[2], 64); err == nil {
			return strings.TrimSpace(m[1]), &n
		}
	}
	return s, nil
}

// buildChapters lays the files end to end and lists the chapters: a file's
// own chapter marks, offset to where the file starts, or the file itself as
// one chapter when it has none. A file that is missing keeps its place, so
// the positions of everything after it do not move while it is away.
func (p *bookPlan) buildChapters() {
	var at int64
	p.chapters = nil
	for i, f := range p.files {
		dur := f.durationMs
		if len(f.chapters) > 0 {
			if end := f.chapters[len(f.chapters)-1].EndMs; end > dur {
				dur = end
			}
			for j, c := range f.chapters {
				title := strings.TrimSpace(c.Title)
				if title == "" {
					title = "Chapter " + strconv.Itoa(len(p.chapters)+1)
				}
				end := c.EndMs
				if j == len(f.chapters)-1 && dur > end {
					end = dur
				}
				p.chapters = append(p.chapters, chapterPlan{title: title, startMs: at + c.StartMs, endMs: at + end, file: i})
			}
		} else {
			p.chapters = append(p.chapters, chapterPlan{title: fileChapterTitle(f, i), startMs: at, endMs: at + dur, file: i})
		}
		at += dur
	}
	p.durationMs = at
}

// fileChapterTitle names a file that is one chapter: its title tag, or its
// file name without the leading track number.
func fileChapterTitle(f *bookFile, i int) string {
	if t := squashTags(f.tags).get("title"); t != "" {
		return t
	}
	_, _, title := TrackNumbers(f.rel)
	title = strings.TrimFunc(title, func(r rune) bool { return unicode.IsSpace(r) || r == '-' || r == '_' || r == '.' })
	if title == "" {
		return "Part " + strconv.Itoa(i+1)
	}
	return title
}
