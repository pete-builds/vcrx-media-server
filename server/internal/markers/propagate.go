package markers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// ApplyToSeason copies one episode's marker to the rest of its season by
// finding the same audio in each: an admin fixes one episode by hand and the
// season follows. Episodes where that audio is not found, or that have a
// marker an admin set themselves, are left alone. It runs on the job's
// goroutine, now, and returns at once.
func (j *Job) ApplyToSeason(ctx context.Context, episodeID int64, kind string) error {
	if !ValidKind(kind) {
		return fmt.Errorf("%w: kind must be intro or credits", ErrBadMarker)
	}
	var season sql.NullInt64
	var start, end int64
	err := j.DB.QueryRowContext(ctx, `SELECT e.parent_id, k.start_ms, k.end_ms FROM items e
		JOIN item_markers k ON k.item_id = e.id AND k.kind = ? AND k.cleared = 0
		WHERE e.id = ? AND e.kind = 'episode'`, kind, episodeID).Scan(&season, &start, &end)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: that episode has no %s marker to copy", ErrBadMarker, kind)
	}
	if err != nil {
		return err
	}
	return j.enqueue(func(ctx context.Context) {
		n, err := j.propagate(ctx, season.Int64, episodeID, kind, start, end)
		if err != nil && ctx.Err() == nil {
			j.Log.Warn("copy marker to season", "episode", episodeID, "kind", kind, "err", err)
			return
		}
		j.Log.Info("marker copied to season", "episode", episodeID, "kind", kind, "episodes", n)
	})
}

func (j *Job) propagate(ctx context.Context, seasonID, sourceID int64, kind string, startMs, endMs int64) (int, error) {
	eps, err := j.loadSeason(ctx, seasonID)
	if err != nil {
		return 0, err
	}
	var src *episode
	for _, e := range eps {
		if e.id == sourceID {
			src = e
		}
	}
	if src == nil {
		return 0, ErrNotPlayable
	}
	j.prepare(ctx, src)
	clip := src.intro
	if kind == Credits {
		clip = src.credits
	}
	a, b := MsFrame(startMs-clip.OffsetMs), MsFrame(endMs-clip.OffsetMs)
	a, b = max(a, 0), min(b, clip.FP.Len())
	if b-a < window {
		return 0, fmt.Errorf("%w: the marker is outside the compared part of the file, or under %d s", ErrBadMarker, window*Hop/SampleRate)
	}
	seg := Fingerprint{Hashes: clip.FP.Hashes[a:b], Valid: clip.FP.Valid[a:b]}
	toEnd := src.duration > 0 && src.duration-endMs <= creditsGapMs
	tx, err := j.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	n := 0
	for _, e := range eps {
		if e.id == sourceID || ctx.Err() != nil {
			continue
		}
		j.prepare(ctx, e)
		other := e.intro
		if kind == Credits {
			other = e.credits
		}
		// The whole marked stretch, or nearly, must be there.
		ms := Align(seg, other.FP, (b-a)*4/5, 1)
		if len(ms) == 0 {
			continue
		}
		m := ms[0]
		shift := other.OffsetMs + FrameMs(m.BStart) - (clip.OffsetMs + FrameMs(a+m.AStart))
		mk := Marker{Kind: kind, StartMs: max(0, startMs+shift), EndMs: endMs + shift, Source: SourceManual,
			Confidence: math.Round(100*math.Max(0, 1-2*m.BER)) / 100}
		if toEnd || (e.duration > 0 && mk.EndMs > e.duration) {
			mk.EndMs = e.duration
		}
		if mk.EndMs <= mk.StartMs {
			continue
		}
		var cur string
		_ = tx.QueryRowContext(ctx, `SELECT source FROM item_markers WHERE item_id = ? AND kind = ?`, e.id, kind).Scan(&cur)
		if cur == SourceManual {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO item_markers (item_id, kind, start_ms, end_ms, confidence, source, cleared, updated_at)
			VALUES (?, ?, ?, ?, ?, 'manual', 0, ?)
			ON CONFLICT (item_id, kind) DO UPDATE SET start_ms = excluded.start_ms, end_ms = excluded.end_ms,
				confidence = excluded.confidence, source = 'manual', cleared = 0, updated_at = excluded.updated_at`,
			e.id, kind, mk.StartMs, mk.EndMs, mk.Confidence, j.now().UnixMilli()); err != nil {
			return n, err
		}
		n++
	}
	if ctx.Err() != nil {
		return n, ctx.Err()
	}
	return n, tx.Commit()
}

// videoExts are the files DryRun looks at.
var videoExts = map[string]bool{".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".ts": true, ".m2ts": true, ".webm": true, ".wmv": true}

// DryRun detects markers for every season folder under dir (each folder of
// video files is a season, files in name order) and prints them, storing
// nothing. It is for checking detection against a real library.
func (j *Job) DryRun(ctx context.Context, dir string, out io.Writer) error {
	j.init()
	seasons := map[string][]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && videoExts[strings.ToLower(filepath.Ext(p))] && !strings.HasPrefix(d.Name(), ".") {
			seasons[filepath.Dir(p)] = append(seasons[filepath.Dir(p)], p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(seasons) == 0 {
		return fmt.Errorf("no video files under %s", dir)
	}
	dirs := make([]string, 0, len(seasons))
	for d := range seasons {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	defer func() { _ = tw.Flush() }()
	for _, d := range dirs {
		files := seasons[d]
		sort.Strings(files)
		started := time.Now()
		eps := make([]*episode, len(files))
		targets := map[string][]int{}
		for i, f := range files {
			e := &episode{root: dir, path: f, due: true, has: map[string]bool{}}
			if fi, err := os.Stat(f); err == nil {
				e.size, e.mtime = fi.Size(), fi.ModTime().UnixMilli()
			}
			e.duration, _ = j.Decoder.Duration(ctx, f)
			eps[i] = e
			targets[Intro] = append(targets[Intro], i)
			targets[Credits] = append(targets[Credits], i)
		}
		final, err := j.compute(ctx, eps, targets)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, d)
		_, _ = fmt.Fprintf(tw, "\n%s (%d files, %s)\n", rel, len(files), time.Since(started).Round(time.Second))
		_, _ = fmt.Fprintln(tw, "file\tlength\tintro\tcredits\t")
		for i, e := range eps {
			show := func(m *Marker) string {
				if m == nil {
					return "-"
				}
				return fmt.Sprintf("%s-%s %s %.2f", clockMs(m.StartMs), clockMs(m.EndMs), m.Source, m.Confidence)
			}
			intro, credits := show(final[i][Intro]), show(final[i][Credits])
			if e.failed != nil {
				intro, credits = "error: "+e.failed.Error(), ""
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t\n", filepath.Base(e.path), clockMs(e.duration), intro, credits)
		}
	}
	return nil
}

func clockMs(ms int64) string {
	s := ms / 1000
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d.%d", s/3600, s/60%60, s%60, ms%1000/100)
	}
	return fmt.Sprintf("%d:%02d.%d", s/60, s%60, ms%1000/100)
}
