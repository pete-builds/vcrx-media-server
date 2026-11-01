package live

// Watching live (step 3): one ffmpeg per channel pulls its stream and writes
// a sliding HLS playlist of fragmented MP4 segments, video copied and audio
// converted to AAC, the same shape as the server's other HLS streams. Every
// viewer of a channel shares that one pull, so a provider that allows two
// connections serves the whole household two channels, not two viewers. A
// pull nobody has asked anything of for Idle is stopped and its files
// deleted. Stream URLs never leave the server: clients only see VCRX's own
// playlist and segments.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ErrBusy is every stream slot in use: the source's max_streams, or the
// server's.
var ErrBusy = errors.New("all live streams are in use")

// ErrNoStream is a run that was never started, has ended, or was replaced.
var ErrNoStream = errors.New("no such live stream")

// ErrRefusedAddress is a stream whose host is not a public address while its
// playlist came from the internet.
var ErrRefusedAddress = errors.New("the stream's address is not a public one")

const (
	liveSegmentSeconds = 4
	liveListSize       = 8
	livePlaylist       = "index.m3u8"
	// A player starts a live playlist three target durations from its end.
	liveMinSegments = 3
)

var liveSegment = regexp.MustCompile(`^(init\.mp4|seg[0-9]{5,9}\.m4s)$`)

// Streams runs the live pulls.
type Streams struct {
	FFmpeg string
	Root   string
	Log    *slog.Logger
	// Idle stops a pull nobody has asked anything of for this long.
	Idle time.Duration
	// Ready is how long a playlist request waits for the first segments.
	Ready time.Duration
	// MaxStreams caps pulls across all sources; 0 is no cap.
	MaxStreams int
	// SegmentSeconds is the segment length, 4 by default (tests use 1).
	SegmentSeconds int
	// Resolve looks up a stream's host, for the address check. Nil uses DNS.
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)

	mu     sync.Mutex
	jobs   map[int64]*watchJob
	closed bool
}

type watchJob struct {
	channel, source int64
	run, dir        string
	cancel          context.CancelFunc
	done            chan struct{}
	err             error
	stderr          strings.Builder
	last            time.Time // guarded by Streams.mu
}

// WatchRequest is one viewer asking for a channel.
type WatchRequest struct {
	Channel int64
	Source  int64
	// SourceMax is the source's max_streams; 0 is no cap.
	SourceMax int
	URL       string
	// Playlist is where the channel's playlist comes from. A file the admin
	// wrote, or a playlist served from the LAN, may name streams on the LAN;
	// one from the internet may name only public addresses.
	Playlist string
}

// Watch joins the channel's pull, starting one if none is running, and
// returns its run id.
func (s *Streams) Watch(ctx context.Context, w WatchRequest) (string, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", errors.New("the server is shutting down")
	}
	if j := s.jobs[w.Channel]; j != nil && !isDone(j) {
		j.last = time.Now()
		s.mu.Unlock()
		return j.run, nil
	}
	s.mu.Unlock()
	if s.overLimit(w) {
		return "", ErrBusy
	}
	if err := s.checkAddress(ctx, w.URL, s.lanPlaylist(ctx, w.Playlist)); err != nil {
		return "", err
	}

	b := make([]byte, 8)
	_, _ = rand.Read(b)
	j := &watchJob{channel: w.Channel, source: w.Source, run: hex.EncodeToString(b), done: make(chan struct{}), last: time.Now()}
	j.dir = filepath.Join(s.Root, fmt.Sprintf("ch%d-%s", w.Channel, j.run))
	if err := os.MkdirAll(j.dir, 0o700); err != nil {
		return "", fmt.Errorf("live stream directory: %w", err)
	}
	jctx, cancel := context.WithCancel(context.Background())
	seconds := s.SegmentSeconds
	if seconds <= 0 {
		seconds = liveSegmentSeconds
	}
	cmd := exec.CommandContext(jctx, s.FFmpeg, liveArgs(w.URL, j.dir, seconds)...) //nolint:gosec // the URL is an argument, never a shell
	cmd.Stderr = &limitedWriter{w: &j.stderr, n: 2000}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.RemoveAll(j.dir)
		return "", fmt.Errorf("start ffmpeg: %w", err)
	}
	j.cancel = cancel
	started := time.Now()
	go func() {
		err := cmd.Wait()
		if jctx.Err() == nil && err != nil {
			// stderr can quote the stream URL; it stays in the server's own log.
			j.err = fmt.Errorf("ffmpeg after %s: %w", time.Since(started).Round(time.Second), err)
			s.log().Warn("live stream ended", "channel", w.Channel, "err", err, "ffmpeg", strings.TrimSpace(j.stderr.String()))
		}
		close(j.done)
	}()

	s.mu.Lock()
	if s.jobs == nil {
		s.jobs = map[int64]*watchJob{}
	}
	prev := s.jobs[w.Channel]
	if prev != nil && !isDone(prev) {
		// Two viewers raced to start the same channel: keep the first pull.
		s.mu.Unlock()
		s.finish(j)
		return prev.run, nil
	}
	if s.overLimitLocked(w) {
		// Another channel took the last slot while this one started.
		s.mu.Unlock()
		s.finish(j)
		return "", ErrBusy
	}
	s.jobs[w.Channel] = j
	s.mu.Unlock()
	if prev != nil {
		s.finish(prev)
	}
	return j.run, nil
}

func (s *Streams) overLimit(w WatchRequest) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.overLimitLocked(w)
}

// overLimitLocked counts the running pulls, all and from w's source, against
// the caps. s.mu must be held.
func (s *Streams) overLimitLocked(w WatchRequest) bool {
	total, fromSource := 0, 0
	for _, j := range s.jobs {
		if isDone(j) {
			continue
		}
		total++
		if j.source == w.Source {
			fromSource++
		}
	}
	return (s.MaxStreams > 0 && total >= s.MaxStreams) || (w.SourceMax > 0 && fromSource >= w.SourceMax)
}

// lanPlaylist reports whether a playlist location is the admin's own file or
// is served from a private address, so its streams may be on the LAN too.
func (s *Streams) lanPlaylist(ctx context.Context, loc string) bool {
	if !strings.HasPrefix(loc, "http://") && !strings.HasPrefix(loc, "https://") {
		return true
	}
	u, err := url.Parse(loc)
	if err != nil {
		return false
	}
	addrs, err := s.resolve(ctx, u.Hostname())
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		if publicAddr(a) {
			return false
		}
	}
	return true
}

func (s *Streams) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if a, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{a}, nil
	}
	if s.Resolve != nil {
		return s.Resolve(ctx, host)
	}
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// liveArgs pulls url, copies its first video stream and converts its first
// audio stream to stereo AAC, and writes a sliding playlist of eight fMP4
// segments, seconds long each, deleting older ones. Only network protocols
// are allowed, so a stream (or an HLS playlist it points to) cannot make
// ffmpeg read a file.
func liveArgs(url, dir string, seconds int) []string {
	return []string{"-hide_banner", "-loglevel", "error", "-nostdin",
		"-protocol_whitelist", "http,https,tcp,tls,crypto",
		"-user_agent", "VCRX",
		"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5", "-rw_timeout", "15000000",
		"-i", url,
		"-map", "0:v:0?", "-map", "0:a:0?", "-c:v", "copy", "-c:a", "aac", "-ac", "2", "-b:a", "160k",
		"-f", "hls", "-hls_time", fmt.Sprint(seconds), "-hls_list_size", fmt.Sprint(liveListSize),
		"-hls_flags", "delete_segments+temp_file+independent_segments+omit_endlist",
		"-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(dir, "seg%05d.m4s"), "-start_number", "0",
		filepath.Join(dir, livePlaylist)}
}

// checkAddress refuses a stream that is not http(s), or whose host resolves
// to anything but public addresses when its playlist came from the internet.
// ffmpeg resolves the host again itself, so a host that changes its answer
// in between is not caught; the protocol whitelist still keeps it to HTTP.
func (s *Streams) checkAddress(ctx context.Context, raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return ErrRefusedAddress
	}
	if allowPrivate {
		return nil
	}
	addrs, err := s.resolve(ctx, u.Hostname())
	if err != nil || len(addrs) == 0 {
		return fmt.Errorf("look up the stream's host: %w", err)
	}
	for _, a := range addrs {
		if !publicAddr(a) {
			return ErrRefusedAddress
		}
	}
	return nil
}

func publicAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsGlobalUnicast() && !a.IsPrivate() && !a.IsLoopback() && !a.IsLinkLocalUnicast() && !cgnat.Contains(a)
}

// lookup finds a channel's running pull by run id and counts the request.
func (s *Streams) lookup(channel int64, run string) (*watchJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[channel]
	if j == nil || j.run != run {
		return nil, ErrNoStream
	}
	j.last = time.Now()
	return j, nil
}

// Playlist returns the channel's live playlist once it lists three segments,
// waiting up to Ready for ffmpeg to write them.
func (s *Streams) Playlist(ctx context.Context, channel int64, run string) ([]byte, error) {
	j, err := s.lookup(channel, run)
	if err != nil {
		return nil, err
	}
	ready := s.Ready
	if ready <= 0 {
		ready = 20 * time.Second
	}
	deadline := time.Now().Add(ready)
	for {
		b, err := os.ReadFile(filepath.Join(j.dir, livePlaylist))
		if err == nil && strings.Count(string(b), "#EXTINF") >= liveMinSegments {
			return b, nil
		}
		select {
		case <-j.done:
			if b, err := os.ReadFile(filepath.Join(j.dir, livePlaylist)); err == nil && strings.Contains(string(b), "#EXTINF") {
				return b, nil
			}
			if j.err != nil {
				return nil, j.err
			}
			return nil, ErrNoStream
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("no segment after %s", ready)
		}
	}
}

// Segment returns the path of one of the pull's files by a name its playlist
// uses; any other name is ErrNoStream, so a request never reaches another file.
func (s *Streams) Segment(channel int64, run, name string) (string, error) {
	if !liveSegment.MatchString(name) {
		return "", ErrNoStream
	}
	j, err := s.lookup(channel, run)
	if err != nil {
		return "", err
	}
	return filepath.Join(j.dir, name), nil
}

// Stop ends a channel's pull, for a channel turned off or a grant revoked.
func (s *Streams) Stop(channel int64) {
	s.mu.Lock()
	j := s.jobs[channel]
	delete(s.jobs, channel)
	s.mu.Unlock()
	if j != nil {
		s.finish(j)
	}
}

// Running is how many pulls are running, for the admin and for tests.
func (s *Streams) Running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, j := range s.jobs {
		if !isDone(j) {
			n++
		}
	}
	return n
}

// Run stops idle and ended pulls every few seconds until ctx ends, then
// stops them all. It first removes what a previous process left in Root.
func (s *Streams) Run(ctx context.Context) {
	if entries, err := os.ReadDir(s.Root); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "ch") && e.IsDir() {
				_ = os.RemoveAll(filepath.Join(s.Root, e.Name()))
			}
		}
	}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.Close()
			return
		case <-t.C:
			s.sweep()
		}
	}
}

func (s *Streams) sweep() {
	idle := s.Idle
	if idle <= 0 {
		idle = time.Minute
	}
	s.mu.Lock()
	var stale []*watchJob
	for ch, j := range s.jobs {
		if isDone(j) || time.Since(j.last) > idle {
			delete(s.jobs, ch)
			stale = append(stale, j)
		}
	}
	s.mu.Unlock()
	for _, j := range stale {
		s.finish(j)
	}
}

// Close stops every pull and refuses new ones.
func (s *Streams) Close() {
	s.mu.Lock()
	s.closed = true
	jobs := s.jobs
	s.jobs = nil
	s.mu.Unlock()
	for _, j := range jobs {
		s.finish(j)
	}
}

func (s *Streams) finish(j *watchJob) {
	j.cancel()
	select {
	case <-j.done:
	case <-time.After(10 * time.Second):
		s.log().Warn("ffmpeg did not exit after being stopped", "channel", j.channel)
	}
	_ = os.RemoveAll(j.dir)
}

func (s *Streams) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func isDone(j *watchJob) bool {
	select {
	case <-j.done:
		return true
	default:
		return false
	}
}

// limitedWriter keeps the first n bytes written to it.
type limitedWriter struct {
	w *strings.Builder
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	if room := l.n - l.w.Len(); room > 0 {
		if len(p) > room {
			l.w.Write(p[:room])
		} else {
			l.w.Write(p)
		}
	}
	return len(p), nil
}
