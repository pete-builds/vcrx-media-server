// Package artwork serves item images. Originals are never modified: local files
// are decoded, resized to a width bucket and cached as JPEG under the cache dir.
// Images downloaded from TMDb are used when there is no local file, and episodes
// without either get a frame extracted with ffmpeg.
package artwork

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png" // decode local PNG artwork
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode local WebP artwork

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

var ErrNoArtwork = errors.New("no artwork")

// Widths are the only sizes rendered, so a client asking for odd sizes cannot fill
// the cache with variants.
var Widths = []int{160, 240, 320, 480, 640, 960, 1280, 1920, 3840}

func bucket(w int) int {
	if w <= 0 {
		return 640
	}
	for _, b := range Widths {
		if w <= b {
			return b
		}
	}
	return Widths[len(Widths)-1]
}

type Service struct {
	DB       *sql.DB
	CacheDir string
	FFmpeg   string
	// MetadataDir holds images downloaded from TMDb; empty turns them off.
	MetadataDir string
	// Overlays, when set, draws badges onto posters (Kometa style).
	Overlays Overlayer
	// sem bounds concurrent ffmpeg frame grabs and resizes.
	sem chan struct{}
}

// Overlayer draws badges onto a poster. sig is "" when the item gets none; it
// goes into the cache key, so a changed rating or a new 4K version redraws.
// Badges describe only the versions pol may play: a friend capped at 1080p
// never sees a 4K badge.
type Overlayer interface {
	Poster(ctx context.Context, itemID int64, pol policy.Policy) (sig string, paint func(*image.RGBA) error, err error)
}

func New(conn *sql.DB, cacheDir, ffmpeg string) *Service {
	return &Service{DB: conn, CacheDir: cacheDir, FFmpeg: ffmpeg, sem: make(chan struct{}, 3)}
}

type source struct {
	path    string // absolute path of the original
	root    string // library root it must stay under
	extract bool   // grab a frame from a video instead of decoding an image
	offset  time.Duration
}

// Get returns the path of a cached JPEG for the item's artwork at a width bucket,
// rendering it first if needed, with overlays on posters (false returns the
// clean image). The caller has already checked that pol may see the item; pol
// still decides which versions a frame or a badge may come from.
func (s *Service) Get(ctx context.Context, pol policy.Policy, itemID int64, kind string, width int, overlays bool) (string, error) {
	src, err := s.lookup(ctx, pol, itemID, kind)
	if err != nil {
		return "", err
	}
	w := bucket(width)
	fi, err := os.Stat(src.path)
	if err != nil {
		return "", ErrNoArtwork
	}
	var sig string
	var paint func(*image.RGBA) error
	if overlays && kind == "poster" && !src.extract && s.Overlays != nil {
		if sig, paint, err = s.Overlays.Poster(ctx, itemID, pol); err != nil {
			return "", err
		}
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%d|%v|%s", src.path, fi.Size(), fi.ModTime().UnixNano(), w, src.extract, sig)))
	out := filepath.Join(s.CacheDir, strconv.FormatInt(itemID, 10), kind+"-"+hex.EncodeToString(key[:8])+".jpg")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	// A recent failure (a corrupt image, a file ffmpeg cannot seek) is not retried
	// on every request.
	failMarker := out + ".fail"
	if fi, err := os.Stat(failMarker); err == nil && time.Since(fi.ModTime()) < 24*time.Hour {
		return "", ErrNoArtwork
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return "", err
	}
	if src.extract {
		err = s.extractFrame(ctx, src, w, out)
	} else {
		err = renderResized(src, w, out, paint)
	}
	if err != nil {
		// A request the client gave up on (a card scrolled past) says nothing
		// about the file, so only a failure with the request still live is
		// remembered. ffmpeg's own 30 s limit is a real failure and still counts.
		if ctx.Err() == nil {
			_ = os.WriteFile(failMarker, []byte(err.Error()), 0o600)
		}
		return "", err
	}
	return out, nil
}

func (s *Service) lookup(ctx context.Context, pol policy.Policy, itemID int64, kind string) (source, error) {
	var itemKind string
	var parent sql.NullInt64
	var libRoot string
	err := s.DB.QueryRowContext(ctx, `SELECT i.kind, i.parent_id, l.root_path FROM items i JOIN libraries l ON l.id = i.library_id WHERE i.id = ?`,
		itemID).Scan(&itemKind, &parent, &libRoot)
	if err != nil {
		return source{}, ErrNoArtwork
	}
	var p sql.NullString
	err = s.DB.QueryRowContext(ctx, `SELECT source_path FROM artwork WHERE item_id = ? AND kind = ? AND source = 'local'`, itemID, kind).Scan(&p)
	if err == nil && p.Valid {
		return source{path: p.String, root: libRoot}, nil
	}
	if s.MetadataDir != "" {
		err = s.DB.QueryRowContext(ctx, `SELECT cache_path FROM artwork WHERE item_id = ? AND kind = ? AND source = 'tmdb'`, itemID, kind).Scan(&p)
		if err == nil && p.Valid {
			return source{path: p.String, root: s.MetadataDir}, nil
		}
	}
	switch {
	case itemKind == "season" && kind == "poster" && parent.Valid:
		return s.lookup(ctx, pol, parent.Int64, "poster")
	case kind == "thumb" && (itemKind == "episode" || itemKind == "movie"):
		// A frame from the best version the caller may play, a fifth of the way
		// in, avoids most cold opens. The path is in the cache key, so a friend
		// capped at 1080p gets a frame from the 1080p file, never the 4K one.
		var vpath string
		var dur sql.NullInt64
		vc, vargs := pol.VersionCond("v")
		err := s.DB.QueryRowContext(ctx, `SELECT v.path, v.duration_ms FROM media_versions v WHERE v.item_id = ? AND `+vc+`
			ORDER BY CASE WHEN v.probe_status = 'ok' THEN 0 ELSE 1 END, v.height DESC LIMIT 1`, append([]any{itemID}, vargs...)...).Scan(&vpath, &dur)
		if err != nil {
			return source{}, ErrNoArtwork
		}
		off := 5 * time.Minute
		if dur.Valid && dur.Int64 > 0 {
			off = time.Duration(dur.Int64/5) * time.Millisecond
		}
		return source{path: vpath, root: libRoot, extract: true, offset: off}, nil
	}
	return source{}, ErrNoArtwork
}

func renderResized(src source, w int, out string, paint func(*image.RGBA) error) error {
	f, _, err := fsafe.OpenUnder(src.root, src.path)
	if err != nil {
		return ErrNoArtwork
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return fmt.Errorf("decode artwork %s: %w", src.path, err)
	}
	// Refuse absurd images before allocating them.
	if cfg.Width*cfg.Height > 80_000_000 {
		return fmt.Errorf("artwork %s too large (%dx%d)", src.path, cfg.Width, cfg.Height)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return fmt.Errorf("decode artwork %s: %w", src.path, err)
	}
	b := img.Bounds()
	if b.Dx() > w {
		h := b.Dy() * w / b.Dx()
		dst := image.NewRGBA(image.Rect(0, 0, w, max(h, 1)))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		img = dst
	}
	if paint != nil {
		rgba, ok := img.(*image.RGBA)
		if !ok {
			rgba = image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
			draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
		}
		if err := paint(rgba); err != nil {
			return err
		}
		img = rgba
	}
	return writeJPEG(img, out)
}

func writeJPEG(img image.Image, out string) error {
	tmp, err := os.CreateTemp(filepath.Dir(out), ".art-*")
	if err != nil {
		return err
	}
	if err := jpeg.Encode(tmp, img, &jpeg.Options{Quality: 85}); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), out)
}

func (s *Service) extractFrame(ctx context.Context, src source, w int, out string) error {
	real, err := fsafe.ResolveUnder(src.root, src.path)
	if err != nil {
		return ErrNoArtwork
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tmp := out + ".tmp.jpg"
	// -ss before -i seeks by keyframe, which is fast over SMB.
	cmd := exec.CommandContext(ctx, s.FFmpeg, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-ss", strconv.FormatFloat(src.offset.Seconds(), 'f', 1, 64), "-i", real,
		"-frames:v", "1", "-vf", fmt.Sprintf("scale=%d:-2", w), "-q:v", "4", "-y", tmp)
	if msg, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("extract frame from %s: %w: %s", src.path, err, truncate(string(msg), 300))
	}
	return os.Rename(tmp, out)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
