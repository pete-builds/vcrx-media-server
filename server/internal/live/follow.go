package live

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

var playlistSegment = regexp.MustCompile(`seg([0-9]{5,9})\.m4s`)

// followCheckEvery is how many segments Follow writes between asking whether
// the viewer may still watch.
const followCheckEvery = 3

// Follow writes a channel's pull to w as one endless fragmented MP4: the init
// segment, then each media segment as ffmpeg finishes it (ffmpeg renames a
// segment into place only once it is whole), starting one behind the newest,
// or at the first as soon as there is one, so playback starts close to live.
// Browsers other than Safari play this natively, with no HLS library. A viewer who falls so far behind that their
// next segment has slid out of the playlist jumps to the live edge. Follow
// returns when ctx ends, the pull ends, or allowed reports false, which it
// asks every few segments so a grant taken away ends the stream.
func (s *Streams) Follow(ctx context.Context, channel int64, run, viewer string, w io.Writer, flush func(), allowed func() bool) error {
	// One segment is enough to start: unlike an HLS player, a progressive
	// stream has no live edge to keep three segments back from.
	pl, err := s.waitPlaylist(ctx, channel, run, viewer, 1)
	if err != nil {
		return err
	}
	next := newestSegment(pl) - 1
	if next < 0 {
		next = 0
	}
	init, err := s.Segment(channel, run, viewer, "init.mp4")
	if err != nil {
		return err
	}
	if err := copyFile(w, init); err != nil {
		return err
	}
	flush()
	written := 0
	for {
		name := fmt.Sprintf("seg%05d.m4s", next)
		path, err := s.Segment(channel, run, viewer, name) // also keeps the viewer on the pull
		if err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			if err := copyFile(w, path); err != nil {
				return err
			}
			flush()
			next++
			written++
			if written%followCheckEvery == 0 && !allowed() {
				return ErrNoStream
			}
			continue
		}
		// Not written yet, or already deleted: a newest segment well past
		// this one means the viewer fell behind the sliding window.
		if b, err := os.ReadFile(filepath.Join(filepath.Dir(path), livePlaylist)); err == nil {
			if newest := newestSegment(b); newest > next+1 {
				next = newest - 1
				continue
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		s.mu.Lock()
		j := s.jobs[channel]
		watching := false
		if j != nil {
			_, watching = j.viewers[viewer]
		}
		s.mu.Unlock()
		if j == nil || j.run != run || isDone(j) || !watching {
			// The pull ended, or this viewer left it.
			return ErrNoStream
		}
	}
}

// newestSegment is the highest segment number a playlist lists, -1 for none.
func newestSegment(pl []byte) int {
	newest := -1
	for _, m := range playlistSegment.FindAllSubmatch(pl, -1) {
		if n, err := strconv.Atoi(string(m[1])); err == nil && n > newest {
			newest = n
		}
	}
	return newest
}

func copyFile(w io.Writer, path string) error {
	f, err := os.Open(path) //nolint:gosec // a segment of this pull, by a name Segment checked
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}
