package household

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // decoders for the formats an upload may be in
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
)

const (
	// AvatarSize is the side of the square picture the server keeps.
	AvatarSize = 256
	// MaxAvatarBytes bounds an upload; a phone photo fits.
	MaxAvatarBytes = 10 << 20
	// maxAvatarPixels refuses decompression bombs before decoding.
	maxAvatarPixels = 50_000_000
)

var ErrNoAvatar = errors.New("no picture")

// AvatarPath is where a user's picture lives. The name comes from the numeric
// id alone, so no request can steer it.
func (s *Store) AvatarPath(id int64) string {
	return filepath.Join(s.AvatarDir, strconv.FormatInt(id, 10)+".jpg")
}

// SaveAvatar decodes an uploaded JPEG, PNG or GIF, crops it to a centred
// square, scales it to AvatarSize and stores it as a fresh JPEG. Re-encoding
// drops everything but the pixels, including any location a phone wrote into
// the photo.
func (s *Store) SaveAvatar(ctx context.Context, id int64, data []byte) error {
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return errors.New("that is not a JPEG, PNG or GIF picture")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxAvatarPixels {
		return fmt.Errorf("the picture is %dx%d; use one under 50 megapixels", cfg.Width, cfg.Height)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return errors.New("that picture could not be read")
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, squareThumb(src, AvatarSize), &jpeg.Options{Quality: 85}); err != nil {
		return err
	}
	if err := os.MkdirAll(s.AvatarDir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.AvatarDir, ".avatar-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := io.Copy(tmp, &out); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.AvatarPath(id)); err != nil {
		return err
	}
	return s.update(ctx, id, `avatar_updated_at = ?`, db.NowMs())
}

// DeleteAvatar removes the picture; the initials show again.
func (s *Store) DeleteAvatar(ctx context.Context, id int64) error {
	if err := s.update(ctx, id, `avatar_updated_at = ?`, nil); err != nil {
		return err
	}
	if err := os.Remove(s.AvatarPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// squareThumb crops the centred square of src and box-filters it down to
// size (or copies it up, for a small source). Plain stdlib: a 256 px avatar
// does not need a resampling library.
func squareThumb(src image.Image, size int) *image.RGBA {
	b := src.Bounds()
	side := min(b.Dx(), b.Dy())
	x0 := b.Min.X + (b.Dx()-side)/2
	y0 := b.Min.Y + (b.Dy()-side)/2
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		sy0, sy1 := y0+y*side/size, y0+(y+1)*side/size
		sy1 = max(sy1, sy0+1)
		for x := range size {
			sx0, sx1 := x0+x*side/size, x0+(x+1)*side/size
			sx1 = max(sx1, sx0+1)
			var r, g, bl, a, n uint64
			for sy := sy0; sy < sy1; sy++ {
				for sx := sx0; sx < sx1; sx++ {
					cr, cg, cb, ca := src.At(sx, sy).RGBA()
					r, g, bl, a, n = r+uint64(cr), g+uint64(cg), bl+uint64(cb), a+uint64(ca), n+1
				}
			}
			dst.Set(x, y, color.RGBA64{R: uint16(r / n), G: uint16(g / n), B: uint16(bl / n), A: uint16(a / n)}) //nolint:gosec // averages of 16-bit values fit
		}
	}
	return dst
}
