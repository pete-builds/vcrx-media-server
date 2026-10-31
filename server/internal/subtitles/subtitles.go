// Package subtitles turns text subtitles into WebVTT, the only format browsers
// read. Sidecar files convert on request, which takes milliseconds. Tracks inside
// a media file are extracted in one background pass per file, because ffmpeg has
// to read the whole file to reach every subtitle packet: a 60 GB remux on the NAS
// takes minutes, so the first request starts the pass and answers "not yet".
package subtitles

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrUnavailable means the track cannot be converted: the codec is image based,
// or ffmpeg failed recently and the failure is not retried yet.
var ErrUnavailable = errors.New("subtitle unavailable")

// TextCodec reports whether ffmpeg can turn a subtitle codec into WebVTT.
// Image-based codecs (PGS, VobSub, DVB) need burn-in instead.
func TextCodec(codec string) bool {
	switch strings.ToLower(codec) {
	case "subrip", "srt", "ass", "ssa", "mov_text", "webvtt", "vtt", "text", "tx3g":
		return true
	}
	return false
}

// Track is one embedded subtitle stream: its row id and its index in the file.
type Track struct {
	StreamID int64
	Index    int64
}

type Service struct {
	FFmpeg string
	Dir    string
	Log    *slog.Logger
	// Timeout bounds one extraction pass. Zero means 45 minutes.
	Timeout time.Duration
	// RetryAfter is how long a failed conversion is remembered. Zero means a day.
	RetryAfter time.Duration

	mu      sync.Mutex
	running map[string]bool
	// sem allows one whole-file pass at a time, so a burst of requests cannot
	// saturate the NAS link while someone is streaming.
	sem chan struct{}
}

func New(dir, ffmpeg string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{FFmpeg: ffmpeg, Dir: dir, Log: log, running: map[string]bool{}, sem: make(chan struct{}, 1)}
}

func (s *Service) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return 45 * time.Minute
}

func (s *Service) retryAfter() time.Duration {
	if s.RetryAfter > 0 {
		return s.RetryAfter
	}
	return 24 * time.Hour
}

// fileKey ties cached output to the source file's identity, so a replaced or
// re-encoded file is extracted again rather than served stale cues.
func fileKey(path string) (string, error) {
	// The same file reached through a symlink must share one cache entry and one pass.
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", path, fi.Size(), fi.ModTime().UnixNano())))
	return hex.EncodeToString(sum[:8]), nil
}

func (s *Service) out(key string, t Track) string {
	return filepath.Join(s.Dir, key, strconv.FormatInt(t.StreamID, 10)+"-"+strconv.FormatInt(t.Index, 10)+".vtt")
}

func (s *Service) failed(path string) bool {
	fi, err := os.Stat(path + ".fail")
	return err == nil && time.Since(fi.ModTime()) < s.retryAfter()
}

// Embedded returns the WebVTT file for one track of the media file at src, which
// the caller has already resolved inside its library root. When it is not ready
// yet, ready is false and a background pass over every track in all is running,
// so the other languages are ready by the time someone asks for them.
func (s *Service) Embedded(src string, want Track, all []Track) (path string, ready bool, err error) {
	key, err := fileKey(src)
	if err != nil {
		return "", false, ErrUnavailable
	}
	path = s.out(key, want)
	if _, err := os.Stat(path); err == nil {
		return path, true, nil
	}
	if s.failed(path) {
		return "", false, ErrUnavailable
	}
	var todo []Track
	for _, t := range all {
		p := s.out(key, t)
		if _, err := os.Stat(p); err != nil && !s.failed(p) {
			todo = append(todo, t)
		}
	}
	if len(todo) == 0 {
		todo = []Track{want}
	}
	s.mu.Lock()
	if !s.running[key] {
		s.running[key] = true
		go s.extract(src, key, todo)
	}
	s.mu.Unlock()
	return path, false, nil
}

// Wait blocks until no pass is running for src. Tests use it.
func (s *Service) Wait(src string) {
	key, err := fileKey(src)
	if err != nil {
		return
	}
	for {
		s.mu.Lock()
		r := s.running[key]
		s.mu.Unlock()
		if !r {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *Service) extract(src, key string, tracks []Track) {
	defer func() {
		s.mu.Lock()
		delete(s.running, key)
		s.mu.Unlock()
	}()
	s.sem <- struct{}{}
	defer func() { <-s.sem }()

	dir := filepath.Join(s.Dir, key)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		s.Log.Error("subtitles: cache dir", "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout())
	defer cancel()
	// One read of the file writes every requested track.
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-i", src}
	for _, t := range tracks {
		args = append(args, "-map", "0:"+strconv.FormatInt(t.Index, 10), "-c:s", "webvtt", "-f", "webvtt", "-y", s.out(key, t)+".tmp")
	}
	started := time.Now()
	msg, err := exec.CommandContext(ctx, s.FFmpeg, args...).CombinedOutput()
	for _, t := range tracks {
		p := s.out(key, t)
		if err != nil {
			_ = os.Remove(p + ".tmp")
			_ = os.WriteFile(p+".fail", []byte(trim(string(msg), 500)), 0o600)
			continue
		}
		if rerr := os.Rename(p+".tmp", p); rerr != nil {
			_ = os.WriteFile(p+".fail", []byte(rerr.Error()), 0o600)
		}
	}
	if err != nil {
		s.Log.Warn("subtitles: extraction failed", "file", filepath.Base(src), "tracks", len(tracks), "err", err, "ffmpeg", trim(string(msg), 300))
		return
	}
	s.Log.Info("subtitles: extracted", "file", filepath.Base(src), "tracks", len(tracks), "took", time.Since(started).Round(time.Second).String())
}

// Sidecar returns a WebVTT copy of a sidecar subtitle file, converting it on the
// first request. A .vtt sidecar is returned as is.
func (s *Service) Sidecar(ctx context.Context, src string) (string, error) {
	if strings.EqualFold(filepath.Ext(src), ".vtt") {
		return src, nil
	}
	key, err := fileKey(src)
	if err != nil {
		return "", ErrUnavailable
	}
	out := filepath.Join(s.Dir, "sidecar", key+".vtt")
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	if s.failed(out) {
		return "", ErrUnavailable
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	tmp := out + ".tmp"
	msg, err := exec.CommandContext(ctx, s.FFmpeg, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", src, "-c:s", "webvtt", "-f", "webvtt", "-y", tmp).CombinedOutput()
	if err != nil {
		_ = os.Remove(tmp)
		if ctx.Err() == nil {
			_ = os.WriteFile(out+".fail", []byte(trim(string(msg), 500)), 0o600)
		}
		return "", fmt.Errorf("%w: %s", ErrUnavailable, trim(string(msg), 200))
	}
	return out, os.Rename(tmp, out)
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}
