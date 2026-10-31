package music

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// Covers serves album and artist pictures, resized to a few widths and cached.
// One ffmpeg command reads both kinds of source: a cover.jpg beside the tracks,
// and a picture embedded in a track (its attached_pic stream). The original is
// never touched; the cache is keyed on the file, its size, its mtime and the
// width, so a replaced cover is picked up on its own.
type Covers struct {
	Catalog  *Catalog
	FFmpeg   string
	CacheDir string
	sem      chan struct{}
}

// coverWidths are the sizes served; a request snaps up to the next one.
var coverWidths = []int{96, 160, 240, 320, 480, 640, 1000}

func snapWidth(w int) int {
	if w <= 0 {
		return 640
	}
	for _, c := range coverWidths {
		if w <= c {
			return c
		}
	}
	return coverWidths[len(coverWidths)-1]
}

// AlbumCover returns the path of a cached JPEG of an album's cover.
func (c *Covers) AlbumCover(ctx context.Context, pol policy.Policy, albumID int64, width int) (string, error) {
	src, err := c.Catalog.AlbumCover(ctx, pol, albumID)
	if err != nil {
		return "", err
	}
	return c.render(ctx, src, snapWidth(width))
}

// ArtistImage returns the path of a cached JPEG of an artist's picture.
func (c *Covers) ArtistImage(ctx context.Context, pol policy.Policy, artistID int64, width int) (string, error) {
	src, err := c.Catalog.ArtistImage(ctx, pol, artistID)
	if err != nil {
		return "", err
	}
	return c.render(ctx, src, snapWidth(width))
}

func (c *Covers) render(ctx context.Context, src *coverSource, width int) (string, error) {
	abs, err := fsafe.ResolveUnder(src.Root, cleanRel(src.Path))
	if err != nil {
		return "", ErrNotFound
	}
	fi, err := os.Stat(abs)
	if err != nil || !fi.Mode().IsRegular() {
		return "", ErrNotFound
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%d", abs, fi.Size(), fi.ModTime().UnixNano(), width)))
	out := filepath.Join(c.CacheDir, hex.EncodeToString(sum[:])[:32]+".jpg")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	if c.sem == nil {
		c.sem = make(chan struct{}, 4)
	}
	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := os.MkdirAll(c.CacheDir, 0o750); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(c.CacheDir, "cover-*.jpg")
	if err != nil {
		return "", err
	}
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmp.Name()) }()
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Scaled down to the width, never up; the file: prefix stops a name being
	// read as a protocol.
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", "file:" + abs, "-map", "0:v:0", "-frames:v", "1",
		"-vf", "scale='min(" + strconv.Itoa(width) + ",iw)':-2", "-q:v", "3", "-f", "image2", "-c:v", "mjpeg", tmp.Name()}
	if b, err := exec.CommandContext(cctx, c.ffmpeg(), args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("cover from %s: %w: %s", filepath.Base(abs), err, strings.TrimSpace(string(b)))
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		return "", err
	}
	return out, nil
}

func (c *Covers) ffmpeg() string {
	if c.FFmpeg == "" {
		return "ffmpeg"
	}
	return c.FFmpeg
}
