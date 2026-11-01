package remux

// HLS is the same converted stream written as an HLS playlist of fragmented
// MP4 segments in a directory, for Safari. Safari plays video only from a
// server that answers byte ranges, which a stream made on the fly cannot, but
// it plays HLS natively.
//
// One stream runs per playback session. A seek starts a new one at the new
// position, exactly like the fragmented MP4 stream, and replaces the old one:
// its ffmpeg is stopped and its files deleted. Each stream has a random run
// id in its URLs, so a request for a stream that was replaced finds nothing
// rather than restarting it.
//
// The playlist is an EVENT playlist that grows as ffmpeg writes segments. It
// is not a full VOD playlist written up front: copied video can only be cut
// on its own keyframes, which are not known without reading the whole file,
// so segment boundaries cannot be promised in advance. The web player draws
// its own seek bar over the whole file and seeks by starting a new stream,
// so it does not need one.
//
// ffmpeg writes much faster than anyone watches, so a janitor bounds the disk
// each stream takes: it pauses ffmpeg (SIGSTOP) when it is far enough ahead
// of the player, deletes segments the player has left well behind, stops a
// stream nobody has asked anything of for a while, and removes everything on
// shutdown. NewHLS removes what a previous process left behind.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrNoStream means the session has no HLS stream with that run id: it was
// never started, it was replaced by a seek, or it ended.
var ErrNoStream = errors.New("no such HLS stream")

// ErrBadName means a file name the server never writes, such as a path.
var ErrBadName = errors.New("not a file this stream has")

// HLSSegmentSeconds is the target segment length. Transcoded video has a
// keyframe every 2 s, so its segments are exactly this long; copied video is
// cut on its own keyframes, so its segments are at least this long.
const HLSSegmentSeconds = 4

// HLSPlaylist is the playlist's file name in every stream's URLs.
const HLSPlaylist = "index.m3u8"

// hlsMinSegments is how many segments a playlist that is still growing must
// list before a player is given it. A player starts a playlist without
// EXT-X-ENDLIST no closer than three target durations from its end (RFC 8216,
// 6.3.3); Chrome refuses one with fewer, with a parse error.
const hlsMinSegments = 3

var (
	sessionName = regexp.MustCompile(`^[0-9a-f]{32}$`)
	// The only files a stream serves: its init segment and media segments.
	// ffmpeg's .tmp files and the playlist (served on its own) are not.
	segmentName = regexp.MustCompile(`^(init\.mp4|seg([0-9]{5,9})\.m4s)$`)
)

// HLS runs and serves the HLS streams of all sessions.
type HLS struct {
	svc  *Service
	root string
	log  *slog.Logger

	// Idle: a stream nobody has requested anything from for this long is
	// stopped and its files deleted. A paused Safari may stop asking, so the
	// player starts a new stream after a long pause.
	Idle time.Duration
	// Ahead: ffmpeg is paused once it has written this many segments past
	// the last one the player asked for, and resumes at half that.
	Ahead int
	// Behind: segments more than this many before the last one the player
	// asked for are deleted.
	Behind int
	// MaxBytes caps one stream's files. Over it, ffmpeg pauses and only two
	// segments are kept behind the player. ffmpeg always runs when the player
	// is within two segments of its end, so the cap may be passed by about
	// that much rather than stall playback.
	MaxBytes int64
	// Ready is how long a playlist request waits for the first segments.
	Ready time.Duration

	mu     sync.Mutex
	jobs   map[string]*hlsJob
	closed bool
}

type hlsJob struct {
	session, run, dir string
	cancel            context.CancelFunc
	proc              *os.Process
	// done closes when ffmpeg has exited and its slot is free; err and
	// stderr are set before that.
	done   chan struct{}
	err    error
	stderr strings.Builder

	// Guarded by HLS.mu.
	last   time.Time
	asked  int // highest segment index requested, -1 before any
	paused bool
}

// NewHLS keeps streams under root, which it creates, and removes any stream
// directories a previous process left there.
func NewHLS(svc *Service, root string, log *slog.Logger) (*HLS, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("HLS directory: %w", err)
	}
	h := &HLS{svc: svc, root: root, log: log, Idle: 2 * time.Minute, Ahead: 30, Behind: 15,
		MaxBytes: 1536 << 20, Ready: 30 * time.Second, jobs: map[string]*hlsJob{}}
	n, err := h.sweep()
	if err != nil {
		return nil, err
	}
	if n > 0 {
		log.Info("removed HLS files left by the last run", "sessions", n)
	}
	return h, nil
}

// sweep removes leftover session directories. Only names this package
// creates are touched, so a misconfigured root loses nothing else.
func (h *HLS) sweep() (int, error) {
	entries, err := os.ReadDir(h.root)
	if err != nil {
		return 0, fmt.Errorf("HLS directory: %w", err)
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && sessionName.MatchString(e.Name()) {
			if err := os.RemoveAll(filepath.Join(h.root, e.Name())); err != nil {
				return n, fmt.Errorf("remove leftover HLS files: %w", err)
			}
			n++
		}
	}
	return n, nil
}

// Start begins a stream for a session, replacing any it had, and returns its
// run id. It takes a slot like any converted stream and returns ErrBusy when
// none frees up within o.Wait.
func (h *HLS) Start(ctx context.Context, session string, o Options) (string, error) {
	if !sessionName.MatchString(session) {
		return "", ErrBadName
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return "", errors.New("the server is shutting down")
	}
	old := h.jobs[session]
	delete(h.jobs, session)
	h.mu.Unlock()
	if old != nil {
		// Its slot frees as it exits, so a seek never needs a second one.
		h.finish(old)
	}

	sem := h.svc.sem
	if o.Video != nil {
		sem = h.svc.tsem
	}
	if !acquire(ctx, sem, o.Wait) {
		return "", ErrBusy
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	j := &hlsJob{session: session, run: hex.EncodeToString(b), done: make(chan struct{}), asked: -1, last: time.Now()}
	j.dir = filepath.Join(h.root, session, j.run)
	if err := os.MkdirAll(j.dir, 0o700); err != nil {
		<-sem
		return "", fmt.Errorf("HLS directory: %w", err)
	}
	jctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(jctx, h.svc.FFmpeg, h.svc.hlsArgs(o, j.dir)...)
	cmd.Stderr = &limited{w: &j.stderr, n: 2000}
	if err := cmd.Start(); err != nil {
		cancel()
		<-sem
		// Only this run's folder: a racing seek's run may be live beside it.
		_ = os.RemoveAll(j.dir)
		_ = os.Remove(filepath.Dir(j.dir)) // fails, harmlessly, while another run is in there
		return "", fmt.Errorf("start ffmpeg: %w", err)
	}
	j.cancel, j.proc = cancel, cmd.Process
	started := time.Now()
	go func() {
		err := cmd.Wait()
		if jctx.Err() == nil && err != nil {
			j.err = fmt.Errorf("ffmpeg after %s: %w: %s", time.Since(started).Round(time.Second), err, strings.TrimSpace(j.stderr.String()))
			h.log.Warn("HLS stream failed", "session", session, "err", j.err)
		}
		<-sem
		close(j.done)
	}()

	h.mu.Lock()
	prev := h.jobs[session]
	h.jobs[session] = j
	closed := h.closed
	h.mu.Unlock()
	if prev != nil {
		// Two seeks raced; the later one wins.
		h.finish(prev)
	}
	if closed {
		h.Stop(session)
		return "", errors.New("the server is shutting down")
	}
	return j.run, nil
}

// hlsArgs write the converted stream as an HLS EVENT playlist of fMP4
// segments into dir. temp_file makes every file appear whole or not at all.
func (s *Service) hlsArgs(o Options, dir string) []string {
	return append(s.convertArgs(o), "-f", "hls", "-hls_time", strconv.Itoa(HLSSegmentSeconds), "-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(dir, "seg%05d.m4s"), "-hls_flags", "temp_file+independent_segments",
		"-start_number", "0", filepath.Join(dir, HLSPlaylist))
}

// finish stops a job that is no longer in the map and deletes its files.
func (h *HLS) finish(j *hlsJob) {
	j.cancel()
	select {
	case <-j.done:
	case <-time.After(10 * time.Second):
		h.log.Warn("ffmpeg did not exit after being killed", "session", j.session)
	}
	if err := os.RemoveAll(j.dir); err != nil {
		h.log.Warn("remove HLS files", "session", j.session, "err", err)
	}
	// Fails, harmlessly, while a newer run of the session is in there.
	_ = os.Remove(filepath.Dir(j.dir))
}

// Stop ends a session's stream, if it has one, and deletes its files.
func (h *HLS) Stop(session string) bool {
	h.mu.Lock()
	j := h.jobs[session]
	delete(h.jobs, session)
	h.mu.Unlock()
	if j != nil {
		h.finish(j)
	}
	return j != nil
}

// Close stops every stream and refuses new ones. Call it at shutdown.
func (h *HLS) Close() {
	h.mu.Lock()
	h.closed = true
	jobs := h.jobs
	h.jobs = map[string]*hlsJob{}
	h.mu.Unlock()
	for _, j := range jobs {
		h.finish(j)
	}
}

// lookup finds the running stream and counts the request as activity.
func (h *HLS) lookup(session, run string) (*hlsJob, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	j := h.jobs[session]
	if j == nil || j.run != run {
		return nil, ErrNoStream
	}
	j.last = time.Now()
	return j, nil
}

// Playlist returns the stream's playlist once it lists hlsMinSegments
// segments or is complete, waiting up to h.Ready for ffmpeg to write them. It
// adds EXT-X-START so a player starts at the beginning of a playlist that is
// still growing rather than near its end, as it would for a live stream.
func (h *HLS) Playlist(ctx context.Context, session, run string) ([]byte, error) {
	j, err := h.lookup(session, run)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(h.Ready)
	for {
		b, err := os.ReadFile(filepath.Join(j.dir, HLSPlaylist))
		if err == nil && playable(string(b)) {
			return withStart(b), nil
		}
		select {
		case <-j.done:
			// It may have written its last segment just now.
			if b, err := os.ReadFile(filepath.Join(j.dir, HLSPlaylist)); err == nil && strings.Contains(string(b), "#EXTINF") {
				return withStart(b), nil
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
			return nil, fmt.Errorf("no segment after %s", h.Ready)
		}
	}
}

func playable(pl string) bool {
	return strings.Count(pl, "#EXTINF") >= hlsMinSegments || (strings.Contains(pl, "#EXT-X-ENDLIST") && strings.Contains(pl, "#EXTINF"))
}

func withStart(b []byte) []byte {
	s := string(b)
	if strings.Contains(s, "#EXT-X-START:") || !strings.HasPrefix(s, "#EXTM3U\n") {
		return b
	}
	return []byte("#EXTM3U\n#EXT-X-START:TIME-OFFSET=0\n" + strings.TrimPrefix(s, "#EXTM3U\n"))
}

// Segment returns the path of one of the stream's segments, by a name its
// playlist uses. Any other name is ErrBadName, so a request can never reach
// another file. The file may be gone: deleted once the player was well past
// it, which the caller answers with 404.
func (h *HLS) Segment(session, run, name string) (string, error) {
	m := segmentName.FindStringSubmatch(name)
	if m == nil {
		return "", ErrBadName
	}
	j, err := h.lookup(session, run)
	if err != nil {
		return "", err
	}
	if m[2] != "" {
		n, _ := strconv.Atoi(m[2])
		h.mu.Lock()
		j.asked = max(j.asked, n)
		h.mu.Unlock()
	}
	return filepath.Join(j.dir, name), nil
}

// Run checks every stream once a second until ctx ends, then stops them all.
func (h *HLS) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			h.Close()
			return
		case <-t.C:
			h.tick()
		}
	}
}

// tick stops idle streams, deletes segments left behind, and pauses or
// resumes ffmpeg to keep each stream within its bounds.
func (h *HLS) tick() {
	h.mu.Lock()
	var idle, live []*hlsJob
	for s, j := range h.jobs {
		if time.Since(j.last) > h.Idle {
			delete(h.jobs, s)
			idle = append(idle, j)
		} else {
			live = append(live, j)
		}
	}
	h.mu.Unlock()
	for _, j := range idle {
		h.log.Info("stopped an idle HLS stream", "session", j.session)
		h.finish(j)
	}
	for _, j := range live {
		h.bound(j)
	}
}

func (h *HLS) bound(j *hlsJob) {
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		return
	}
	h.mu.Lock()
	asked, paused := j.asked, j.paused
	h.mu.Unlock()
	var bytes int64
	newest := -1
	segs := map[int]int64{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		bytes += info.Size()
		if m := segmentName.FindStringSubmatch(e.Name()); m != nil && m[2] != "" {
			n, _ := strconv.Atoi(m[2])
			segs[n] = info.Size()
			newest = max(newest, n)
		}
	}
	keep := h.Behind
	if bytes > h.MaxBytes {
		keep = 2
	}
	for n, size := range segs {
		if n < asked-keep {
			if os.Remove(filepath.Join(j.dir, fmt.Sprintf("seg%05d.m4s", n))) == nil {
				bytes -= size
			}
		}
	}
	select {
	case <-j.done:
		return
	default:
	}
	ahead := newest - asked
	switch {
	case !paused && ahead > 2 && (ahead >= h.Ahead || bytes >= h.MaxBytes):
		if err := pauseProcess(j.proc); err == nil {
			h.setPaused(j, true)
		}
	case paused && (ahead <= 2 || (ahead <= h.Ahead/2 && bytes < h.MaxBytes)):
		if err := resumeProcess(j.proc); err == nil {
			h.setPaused(j, false)
		}
	}
}

func (h *HLS) setPaused(j *hlsJob, p bool) {
	h.mu.Lock()
	j.paused = p
	h.mu.Unlock()
}
