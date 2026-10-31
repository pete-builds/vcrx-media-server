package artwork

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// all is an admin's policy: every version, every item.
var all = policy.Policy{IsAdmin: true}

func TestBucket(t *testing.T) {
	for in, want := range map[int]int{0: 640, 1: 160, 160: 160, 161: 240, 700: 960, 99999: 3840} {
		if got := bucket(in); got != want {
			t.Errorf("bucket(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestResizeAndCache(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := db.Migrate(context.Background(), conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "Movies")
	if err := os.MkdirAll(filepath.Join(root, "Film"), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 1000, 1500))
	for y := range 1500 {
		for x := range 1000 {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 80, 255})
		}
	}
	poster := filepath.Join(root, "Film", "poster.png")
	f, _ := os.Create(poster)
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	mustExec := func(q string, args ...any) {
		if _, err := conn.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO libraries (id, name, kind, root_path, created_at, updated_at) VALUES (1, 'Movies', 'movie', ?, 0, 0)`, root)
	mustExec(`INSERT INTO items (id, library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (1, 1, 'movie', 'k', 'Film', 'Film', 0, 0)`)
	mustExec(`INSERT INTO artwork (item_id, kind, source, source_path, updated_at) VALUES (1, 'poster', 'local', ?, 0)`, poster)

	s := New(conn, filepath.Join(dir, "cache"), "ffmpeg")
	out, err := s.Get(context.Background(), all, 1, "poster", 300, true)
	if err != nil {
		t.Fatal(err)
	}
	cf, _ := os.Open(out)
	got, err := jpeg.Decode(cf)
	_ = cf.Close()
	if err != nil {
		t.Fatal(err)
	}
	if b := got.Bounds(); b.Dx() != 320 || b.Dy() != 480 {
		t.Fatalf("resized to %v, want 320x480", b)
	}
	again, _ := s.Get(context.Background(), all, 1, "poster", 310, true)
	if again != out {
		t.Fatalf("same bucket rendered twice: %s vs %s", again, out)
	}
	if _, err := s.Get(context.Background(), all, 1, "backdrop", 300, true); !errors.Is(err, ErrNoArtwork) {
		t.Fatalf("missing backdrop: %v", err)
	}
	// A poster path tampered to point outside the library is refused.
	outside := filepath.Join(dir, "outside.png")
	if err := os.Rename(poster, outside); err != nil {
		t.Fatal(err)
	}
	mustExec(`UPDATE artwork SET source_path = ? WHERE item_id = 1`, outside)
	if _, err := s.Get(context.Background(), all, 1, "poster", 640, true); err == nil {
		t.Fatal("rendered artwork from outside the library root")
	}
}

func TestTMDbArtwork(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := db.Migrate(context.Background(), conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	meta := filepath.Join(dir, "metadata")
	if err := os.MkdirAll(filepath.Join(meta, "1"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(meta, "1", "poster-abc.png")
	f, _ := os.Create(file)
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 200, 300))); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	mustExec := func(q string, args ...any) {
		if _, err := conn.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO libraries (id, name, kind, root_path, created_at, updated_at) VALUES (1, 'Movies', 'movie', ?, 0, 0)`, filepath.Join(dir, "Movies"))
	mustExec(`INSERT INTO items (id, library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (1, 1, 'movie', 'k', 'Film', 'Film', 0, 0)`)
	mustExec(`INSERT INTO artwork (item_id, kind, source, url, cache_path, updated_at) VALUES (1, 'poster', 'tmdb', '/abc.png', ?, 0)`, file)

	s := New(conn, filepath.Join(dir, "cache"), "ffmpeg")
	if _, err := s.Get(context.Background(), all, 1, "poster", 160, true); !errors.Is(err, ErrNoArtwork) {
		t.Fatalf("TMDb artwork served with no metadata dir: %v", err)
	}
	s.MetadataDir = meta
	if _, err := s.Get(context.Background(), all, 1, "poster", 160, true); err != nil {
		t.Fatalf("TMDb poster: %v", err)
	}
	// A cache_path pointing outside the metadata dir is refused.
	outside := filepath.Join(dir, "outside.png")
	if err := os.Rename(file, outside); err != nil {
		t.Fatal(err)
	}
	mustExec(`UPDATE artwork SET cache_path = ? WHERE item_id = 1`, outside)
	if _, err := s.Get(context.Background(), all, 1, "poster", 240, true); err == nil {
		t.Fatal("rendered TMDb artwork from outside the metadata dir")
	}
}

type fakeOverlay struct {
	sig   string
	calls int
}

func (f *fakeOverlay) Poster(ctx context.Context, itemID int64, pol policy.Policy) (string, func(*image.RGBA) error, error) {
	if f.sig == "" {
		return "", nil, nil
	}
	return f.sig, func(img *image.RGBA) error {
		f.calls++
		for y := range 20 {
			for x := range 20 {
				img.Set(x, y, color.RGBA{255, 0, 0, 255})
			}
		}
		return nil
	}, nil
}

func TestPosterOverlaysAreCachedSeparately(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := db.Migrate(context.Background(), conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "Movies")
	_ = os.MkdirAll(root, 0o755)
	poster := filepath.Join(root, "poster.png")
	fh, _ := os.Create(poster)
	_ = png.Encode(fh, image.NewRGBA(image.Rect(0, 0, 200, 300)))
	_ = fh.Close()
	for _, q := range []string{
		`INSERT INTO libraries (id, name, kind, root_path, created_at, updated_at) VALUES (1, 'Movies', 'movie', '` + root + `', 0, 0)`,
		`INSERT INTO items (id, library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (1, 1, 'movie', 'k', 'Film', 'Film', 0, 0)`,
		`INSERT INTO artwork (item_id, kind, source, source_path, updated_at) VALUES (1, 'poster', 'local', '` + poster + `', 0)`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	ov := &fakeOverlay{sig: "aaa"}
	s := New(conn, filepath.Join(dir, "cache"), "ffmpeg")
	s.Overlays = ov
	ctx := context.Background()
	with, err := s.Get(ctx, all, 1, "poster", 200, true)
	if err != nil {
		t.Fatal(err)
	}
	clean, _ := s.Get(ctx, all, 1, "poster", 200, false)
	if with == clean || ov.calls != 1 {
		t.Fatalf("overlay and clean posters share %s (paint calls %d)", with, ov.calls)
	}
	again, _ := s.Get(ctx, all, 1, "poster", 200, true)
	if again != with || ov.calls != 1 {
		t.Fatal("an unchanged overlay should come from the cache")
	}
	// A new signature (a rating moved) draws a new file.
	ov.sig = "bbb"
	moved, _ := s.Get(ctx, all, 1, "poster", 200, true)
	if moved == with || ov.calls != 2 {
		t.Fatalf("changed facts reused the old poster: %s", moved)
	}
	// Backdrops never get badges.
	if _, err := s.Get(ctx, all, 1, "backdrop", 200, true); err == nil || ov.calls != 2 {
		t.Fatalf("backdrop: err=%v calls=%d", err, ov.calls)
	}
	fc, _ := os.Open(with)
	img, err := jpeg.Decode(fc)
	_ = fc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _, _ := img.At(8, 8).RGBA(); r < 0x8000 {
		t.Fatal("the overlay paint did not reach the served poster")
	}
}

// A friend capped at 1080p gets an extracted frame from the 1080p file, never
// the 4K one, and the 4K poster badge is not theirs either.
func TestFramesAndBadgesFollowThePolicy(t *testing.T) {
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := db.Migrate(context.Background(), conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "Movies")
	for _, q := range []string{
		`INSERT INTO libraries (id, name, kind, root_path, created_at, updated_at) VALUES (1, 'Movies', 'movie', '` + root + `', 0, 0)`,
		`INSERT INTO items (id, library_id, kind, group_key, title, sort_title, added_at, updated_at) VALUES (1, 1, 'movie', 'k', 'Film', 'Film', 0, 0)`,
		`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, height, resolution, probe_status, added_at, updated_at)
			VALUES (1, 1, '` + root + `/a/film.2160p.mkv', 'a', 1, 0, 2160, '4K', 'ok', 0, 0)`,
		`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, height, resolution, probe_status, added_at, updated_at)
			VALUES (1, 1, '` + root + `/b/film.1080p.mkv', 'b', 1, 0, 1080, '1080p', 'ok', 0, 0)`,
	} {
		if _, err := conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s := New(conn, filepath.Join(dir, "cache"), "ffmpeg")
	capped := policy.Policy{UserID: 2, LibraryIDs: []int64{1}, MaxResolution: "1080p"}
	for _, c := range []struct {
		pol  policy.Policy
		want string
	}{{all, "film.2160p.mkv"}, {capped, "film.1080p.mkv"}} {
		src, err := s.lookup(context.Background(), c.pol, 1, "thumb")
		if err != nil || filepath.Base(src.path) != c.want || !src.extract {
			t.Errorf("max resolution %q: frame from %q (%v), want %s", c.pol.MaxResolution, src.path, err, c.want)
		}
	}
}
