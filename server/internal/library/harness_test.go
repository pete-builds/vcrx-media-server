package library

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/probe"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := db.Migrate(context.Background(), conn, "", quiet); err != nil {
		t.Fatal(err)
	}
	return conn
}

func addLibrary(t *testing.T, conn *sql.DB, name, kind, root, grouping string) Library {
	t.Helper()
	now := time.Now().UnixMilli()
	res, err := conn.Exec(`INSERT INTO libraries (name, kind, root_path, grouping, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		name, kind, root, grouping, now, now)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return Library{ID: id, Name: name, Kind: kind, RootPath: root, Grouping: grouping}
}

// writeFiles creates files under root; a value is the content, and an empty
// value writes a few bytes so the file is not mistaken for anything special.
func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if body == "" {
			body = "video bytes for " + rel
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// fakeProber answers from the file name: 2160p or 4K names are HEVC HDR10 4K,
// anything else 1080p H.264. It counts calls per path.
type fakeProber struct {
	mu    sync.Mutex
	calls map[string]int
	fail  string // probes of paths containing this fail
	gate  chan struct{}
}

func (f *fakeProber) Probe(ctx context.Context, p string) (*probe.Result, error) {
	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[p]++
	f.mu.Unlock()
	if f.fail != "" && strings.Contains(p, f.fail) {
		return nil, fmt.Errorf("ffprobe: %s: Invalid data found when processing input", filepath.Base(p))
	}
	if strings.HasSuffix(p, ".mp3") {
		// ffprobe finds no video in an MP3; the real prober says so.
		return nil, fmt.Errorf("%s: %w", filepath.Base(p), probe.ErrNoVideo)
	}
	r := &probe.Result{Container: probe.Container("matroska,webm", p), DurationMs: 5_400_000, Bitrate: 8_000_000,
		VideoCodec: "h264", VideoProfile: "high", VideoLevel: 41, BitDepth: 8, Width: 1920, Height: 1080, FrameRate: 23.976}
	if strings.Contains(p, "2160p") || strings.Contains(p, "4K") {
		r.VideoCodec, r.VideoProfile, r.BitDepth, r.Width, r.Height, r.HDRType = "hevc", "main 10", 10, 3840, 2160, probe.HDR10
	}
	r.Resolution = probe.Bucket(r.Width, r.Height)
	r.Streams = []probe.Stream{
		{Index: 0, Kind: probe.KindVideo, Codec: r.VideoCodec, Profile: r.VideoProfile, Width: r.Width, Height: r.Height},
		{Index: 1, Kind: probe.KindAudio, Codec: "eac3", Language: "eng", Channels: 6, ChannelLayout: "5.1", Default: true},
		{Index: 2, Kind: probe.KindSubtitle, Codec: "subrip", Language: "eng"},
	}
	return r, nil
}

func (f *fakeProber) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

// clock is a settable time source for Scanner.Now.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type env struct {
	t     *testing.T
	db    *sql.DB
	root  string
	lib   Library
	pr    *fakeProber
	clock *clock
	sc    *Scanner
}

func newEnv(t *testing.T, kind, grouping string, files map[string]string) *env {
	t.Helper()
	conn := newDB(t)
	root := filepath.Join(t.TempDir(), "media")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, files)
	e := &env{t: t, db: conn, root: root, pr: &fakeProber{}, clock: &clock{t: time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)}}
	e.lib = addLibrary(t, conn, "Lib "+kind, kind, root, grouping)
	e.sc = &Scanner{DB: conn, Prober: e.pr, Log: quiet, ProbeConcurrency: 3, Now: e.clock.now}
	return e
}

func (e *env) scan(opt Options) Run {
	e.t.Helper()
	run, err := e.sc.Scan(context.Background(), e.lib, opt)
	if err != nil {
		e.t.Fatalf("scan: %v (run %+v)", err, run)
	}
	if run.Status != StatusOK {
		e.t.Fatalf("status = %s, error %q", run.Status, run.Error)
	}
	return run
}

func (e *env) scanErr(opt Options) (Run, error) {
	e.t.Helper()
	return e.sc.Scan(context.Background(), e.lib, opt)
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.db.QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return n
}

func (e *env) str(q string, args ...any) string {
	e.t.Helper()
	var s sql.NullString
	if err := e.db.QueryRow(q, args...).Scan(&s); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
	return s.String
}

func (e *env) strs(q string, args ...any) []string {
	e.t.Helper()
	rows, err := e.db.Query(q, args...)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s sql.NullString
		if err := rows.Scan(&s); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, s.String)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

func (e *env) dump(b *strings.Builder, q string) {
	e.t.Helper()
	rows, err := e.db.Query(q)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols, _ := rows.Columns()
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			e.t.Fatal(err)
		}
		fmt.Fprintln(b, vals...)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) path(rel string) string { return filepath.Join(e.root, filepath.FromSlash(rel)) }

func (e *env) write(files map[string]string) { writeFiles(e.t, e.root, files) }

func (e *env) remove(rels ...string) {
	e.t.Helper()
	for _, r := range rels {
		if err := os.RemoveAll(e.path(r)); err != nil {
			e.t.Fatal(err)
		}
	}
}

// digest renders every row the scanner may write, so a test can prove a run
// changed nothing.
func (e *env) digest() string {
	e.t.Helper()
	var b strings.Builder
	for _, q := range []string{
		`SELECT id, kind, IFNULL(parent_id,0), group_key, title, sort_title, IFNULL(year,0), metadata_source, extra, IFNULL(missing_since,0), updated_at FROM items ORDER BY id`,
		`SELECT id, item_id, path, size, mtime, IFNULL(label,''), probe_status, IFNULL(missing_since,0), updated_at FROM media_versions ORDER BY id`,
		`SELECT id, version_id, kind, IFNULL(codec,''), IFNULL(language,''), is_external, IFNULL(external_path,'') FROM media_streams ORDER BY id`,
		`SELECT item_id, kind, source, IFNULL(source_path,''), updated_at FROM artwork ORDER BY item_id, kind`,
		`SELECT item_id, kind, value FROM item_tags ORDER BY item_id, kind, value`,
		`SELECT item_id, source, value FROM item_ratings ORDER BY item_id, source`,
		`SELECT item_id, label_id, source FROM item_labels ORDER BY item_id, label_id`,
		`SELECT id, item_id, kind, title, path, size, mtime, probe_status, IFNULL(missing_since,0), updated_at FROM item_extras ORDER BY id`,
	} {
		e.dump(&b, q)
		b.WriteString("--\n")
	}
	return b.String()
}
