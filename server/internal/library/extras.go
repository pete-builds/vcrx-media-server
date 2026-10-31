package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/pete-builds/vcrx-media-server/server/internal/probe"
)

// Local extras, the Plex layout: "<name>-trailer.mkv" beside a movie, the
// folders Trailers, Featurettes, Behind The Scenes, Deleted Scenes,
// Interviews, Scenes, Shorts and Other inside a movie's or show's folder (or a
// season's, which attaches to the show), and theme.mp3 in a show's folder.
// None of them is ever a title: the walk skips them as titles and this pass
// attaches each to the item whose folder it sits in. Like versions, a file
// that vanishes is marked missing and kept for MissingRetention; it is never
// deleted on the first miss.

// extraRow is an item_extras row as the scan needs it.
type extraRow struct {
	id, itemID   int64
	rel, kind    string
	title        string
	size, mtime  int64
	probeStatus  string
	probedAt     int64
	missingSince int64
}

// extraFile is one extra found this run and the item it belongs to.
type extraFile struct {
	videoFile
	itemID int64
	kind   string
	title  string
}

// extraOwners maps folders to the items whose files they hold: a movie's
// folder to the movie, a show's and a season's folder to the show. A folder
// holding several titles maps to all of them. It is built from every present
// version in the library, not only this run's, so a targeted scan of one
// trailer still finds its movie.
type extraOwners struct {
	byDir    map[string]map[int64]bool
	byBase   map[string]int64 // folder + "/" + lower-case video base name, for "<name>-trailer" files
	showDirs []string         // folders of shows, where theme.mp3 lives
}

func (o *extraOwners) add(dir string, id int64) {
	if o.byDir[dir] == nil {
		o.byDir[dir] = map[int64]bool{}
	}
	o.byDir[dir][id] = true
}

// only returns the one item a folder belongs to, 0 when none or several.
func (o *extraOwners) only(dir string) int64 {
	if len(o.byDir[dir]) != 1 {
		return 0
	}
	for id := range o.byDir[dir] {
		return id
	}
	return 0
}

// gone reports whether a version is missing or about to be marked missing by
// this run, so it owns nothing.
func ownersOf(st *state, lib Library, gone func(*verRow) bool) *extraOwners {
	o := &extraOwners{byDir: map[string]map[int64]bool{}, byBase: map[string]int64{}}
	shows := map[string]bool{}
	for _, v := range st.versions {
		it := st.items[v.itemID]
		if gone(v) || it == nil {
			continue
		}
		dir := parentRel(v.rel)
		if it.kind == "movie" {
			o.add(dir, it.id)
			o.byBase[dir+"/"+strings.ToLower(stripExt(path.Base(v.rel)))] = it.id
			continue
		}
		// An episode: its season's parent is the show.
		season := st.items[it.parent]
		if season == nil || st.items[season.parent] == nil {
			continue
		}
		show := season.parent
		ep, ok := ParseEpisodePath(v.rel, lib.Kind == KindMovie)
		if !ok {
			continue
		}
		for _, d := range []string{ep.ShowDir, ep.SeasonDir} {
			if d != "" {
				o.add(d, show)
			}
		}
		if ep.ShowDir != "" && !shows[ep.ShowDir] {
			shows[ep.ShowDir] = true
			o.showDirs = append(o.showDirs, ep.ShowDir)
		}
	}
	sort.Strings(o.showDirs)
	return o
}

// owner finds the item a "<name>-trailer" style file belongs to: the video
// named <name> in its folder, else the folder's only title. A bare trailer.mp4
// needs a folder with one title. The top of a library is never one title's
// folder, so a file there must name its video.
func (o *extraOwners) owner(dir, name string) int64 {
	if name != "" {
		if id := o.byBase[dir+"/"+strings.ToLower(name)]; id != 0 {
			return id
		}
	}
	if dir == "" {
		return 0
	}
	return o.only(dir)
}

// extraTitle is what the Extras row shows for a file: its name, with dots and
// underscores read as spaces when it has no spaces of its own.
func extraTitle(name string) string {
	t := stripExt(name)
	if !strings.Contains(t, " ") {
		t = strings.NewReplacer(".", " ", "_", " ").Replace(t)
	}
	if t = strings.TrimSpace(t); t == "" {
		return "Extra"
	}
	return t
}

// kindTitles name an extra that is named only by its marker ("-trailer").
var kindTitles = map[string]string{
	ExtraTrailer: "Trailer", ExtraFeaturette: "Featurette", ExtraBehindTheScenes: "Behind the scenes",
	ExtraDeletedScene: "Deleted scene", ExtraInterview: "Interview", ExtraScene: "Scene", ExtraShort: "Short",
	ExtraOther: "Extra", ExtraTheme: "Theme music",
}

// findExtras lists this run's extras with their owners. Folders that cannot be
// read are returned too, so their rows are left alone rather than marked missing.
func (s *Scanner) findExtras(lib Library, w *walker, o *extraOwners, sub string, isFile bool, log *slog.Logger) ([]*extraFile, []string) {
	var out []*extraFile
	var failed []string
	seen := map[string]bool{}
	add := func(rel, kind, title string, owner int64) {
		if owner == 0 || seen[rel] || !inScope(rel, sub, isFile) {
			if owner == 0 {
				log.Debug("extra has no title to belong to", "path", rel)
			}
			return
		}
		fi, err := os.Stat(w.absOf(rel))
		if err != nil || !fi.Mode().IsRegular() {
			return
		}
		seen[rel] = true
		out = append(out, &extraFile{videoFile: videoFile{rel: rel, abs: w.absOf(rel), size: fi.Size(), mtime: fi.ModTime().UnixMilli()},
			itemID: owner, kind: kind, title: title})
	}
	for _, rel := range w.extraFiles {
		kind, name, _ := ExtraFileKind(path.Base(rel))
		dir := parentRel(rel)
		title := kindTitles[kind]
		add(rel, kind, title, o.owner(dir, name))
	}
	for _, d := range w.extraDirs {
		l, err := w.read(d.rel)
		if err != nil {
			log.Warn("extras folder unreadable, its extras are left as they were", "path", d.rel, "err", err)
			failed = append(failed, d.rel)
			continue
		}
		owner := o.only(parentRel(d.rel))
		for _, name := range l.files() {
			if !IsVideo(name) || strings.HasPrefix(name, ".") {
				continue
			}
			add(joinRel(d.rel, name), d.kind, extraTitle(name), owner)
		}
	}
	// Theme music sits in a show's own folder.
	if lib.Kind == KindShow {
		for _, d := range o.showDirs {
			if n := w.listing(d).lookup("theme.mp3"); n != "" {
				add(joinRel(d, n), ExtraTheme, kindTitles[ExtraTheme], o.only(d))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, failed
}

func loadExtras(ctx context.Context, db *sql.DB, libID int64) (map[string]*extraRow, error) {
	out := map[string]*extraRow{}
	err := query(ctx, db, `SELECT id, item_id, path, rel_path, kind, title, size, mtime, probe_status, IFNULL(probed_at, 0),
		IFNULL(missing_since, 0) FROM item_extras WHERE library_id = ?`, libID, func(rows *sql.Rows) error {
		x := &extraRow{}
		var p string
		if err := rows.Scan(&x.id, &x.itemID, &p, &x.rel, &x.kind, &x.title, &x.size, &x.mtime, &x.probeStatus, &x.probedAt,
			&x.missingSince); err != nil {
			return err
		}
		out[p] = x
		return nil
	})
	return out, err
}

type extraProbe struct {
	f   *extraFile
	res *probe.Result
	err error
}

// scanExtras runs after the titles are written, so every owner exists, and
// before reconcile prunes emptied items. It
// probes new and changed extras outside any transaction, then writes them,
// marks unseen ones missing and drops ones missing past MissingRetention.
func (s *Scanner) scanExtras(ctx context.Context, lib Library, opt Options, st *state, w *walker, seenVideos map[string]bool, sub string, isFile bool, log *slog.Logger) error {
	gone := func(v *verRow) bool {
		return v.missingSince != 0 || (inScope(v.rel, sub, isFile) && !seenVideos[v.path] && !w.underFailed(v.rel))
	}
	found, failed := s.findExtras(lib, w, ownersOf(st, lib, gone), sub, isFile, log)
	rows, err := loadExtras(ctx, s.DB, lib.ID)
	if err != nil {
		return err
	}
	nowMs := s.now().UnixMilli()
	var toProbe []*extraFile
	for _, f := range found {
		x := rows[f.abs]
		var v *verRow
		if x != nil {
			v = &verRow{size: x.size, mtime: x.mtime, probeStatus: x.probeStatus, probedAt: x.probedAt}
		}
		if needsProbe(v, &f.videoFile, opt, nowMs) {
			toProbe = append(toProbe, f)
		}
	}
	probed := s.probeExtras(ctx, toProbe)
	if err := ctx.Err(); err != nil {
		return err
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	exec := func(q string, args ...any) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}
	seen := map[string]bool{}
	added, changed, missing, expired := 0, 0, 0, 0
	for _, f := range found {
		seen[f.abs] = true
		x := rows[f.abs]
		pr, fresh := probed[f]
		if !fresh {
			// Unchanged file: only its owner, kind, title or presence can differ.
			if x.itemID != f.itemID || x.kind != f.kind || x.title != f.title || x.missingSince != 0 {
				if err := exec(`UPDATE item_extras SET item_id = ?, kind = ?, title = ?, rel_path = ?, missing_since = NULL, updated_at = ?
					WHERE id = ?`, f.itemID, f.kind, f.title, f.rel, nowMs, x.id); err != nil {
					return err
				}
				changed++
			}
			continue
		}
		if f.kind == ExtraTheme && errors.Is(pr.err, probe.ErrNoVideo) {
			// Theme music is meant to be audio only; the probe's rule is for titles.
			pr = extraProbe{f: f, res: &probe.Result{Container: "mp3"}}
		}
		if pr.err != nil {
			log.Warn("extra probe failed", "path", f.rel, "err", pr.err)
		}
		if x == nil {
			var other string
			err := tx.QueryRowContext(ctx, `SELECT l.name FROM item_extras x JOIN libraries l ON l.id = x.library_id
				WHERE x.path = ? AND x.library_id <> ?`, f.abs, lib.ID).Scan(&other)
			if err == nil {
				log.Warn("extra already belongs to another library; check for overlapping roots", "path", f.rel, "library", other)
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			added++
		} else {
			changed++
		}
		if err := writeExtra(ctx, tx, lib.ID, x, f, pr.res, pr.err, nowMs); err != nil {
			return err
		}
	}
	cutoff := nowMs - MissingRetention.Milliseconds()
	underFailed := func(rel string) bool {
		if w.underFailed(rel) {
			return true
		}
		for _, d := range failed {
			if strings.HasPrefix(rel, d+"/") {
				return true
			}
		}
		return false
	}
	for p, x := range rows {
		if seen[p] || !inScope(x.rel, sub, isFile) {
			continue
		}
		switch {
		case x.missingSince == 0 && !underFailed(x.rel):
			if err := exec(`UPDATE item_extras SET missing_since = ?, updated_at = ? WHERE id = ?`, nowMs, nowMs, x.id); err != nil {
				return err
			}
			missing++
		case x.missingSince != 0 && x.missingSince < cutoff:
			if err := exec(`DELETE FROM item_extras WHERE id = ?`, x.id); err != nil {
				return err
			}
			expired++
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if added+changed+missing+expired > 0 {
		log.Info("extras", "found", len(found), "added", added, "changed", changed, "missing", missing, "deleted", expired)
	}
	return nil
}

// probeExtras probes with the scanner's concurrency; a cancelled context stops
// the rest.
func (s *Scanner) probeExtras(ctx context.Context, files []*extraFile) map[*extraFile]extraProbe {
	out := map[*extraFile]extraProbe{}
	if len(files) == 0 {
		return out
	}
	n := s.ProbeConcurrency
	if n <= 0 {
		n = DefaultProbeConcurrency
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	jobs := make(chan *extraFile)
	for range min(n, len(files)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				res, err := s.Prober.Probe(ctx, f.abs)
				mu.Lock()
				out[f] = extraProbe{f, res, err}
				mu.Unlock()
			}
		}()
	}
	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		jobs <- f
	}
	close(jobs)
	wg.Wait()
	return out
}

// audioJSON keeps what the playback decision needs from each audio stream.
func audioJSON(res *probe.Result) string {
	type stream struct {
		Index    int    `json:"index"`
		Codec    string `json:"codec,omitempty"`
		Language string `json:"language,omitempty"`
		Channels int    `json:"channels,omitempty"`
		Default  bool   `json:"default,omitempty"`
	}
	out := []stream{}
	for _, st := range res.Streams {
		if st.Kind == probe.KindAudio {
			out = append(out, stream{st.Index, st.Codec, st.Language, st.Channels, st.Default})
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func writeExtra(ctx context.Context, tx *sql.Tx, libID int64, x *extraRow, f *extraFile, res *probe.Result, perr error, now int64) error {
	status, msg := "ok", ""
	if perr != nil || res == nil {
		status = "error"
		if perr != nil {
			msg = perr.Error()
		} else {
			msg = "no probe result"
		}
		res = &probe.Result{Container: probe.Container("", f.abs)}
	}
	if res.Container == "" && strings.EqualFold(path.Ext(f.abs), ".mp3") {
		res.Container = "mp3"
	}
	cols := []any{f.itemID, f.kind, f.title, f.rel, f.size, f.mtime, nullStr(res.Container), nullInt(res.DurationMs), nullInt(res.Bitrate),
		nullStr(res.VideoCodec), nullStr(res.VideoProfile), nullInt(int64(res.VideoLevel)), nullInt(int64(res.BitDepth)),
		nullInt(int64(res.Width)), nullInt(int64(res.Height)), nullStr(res.Resolution), nullFloat(res.FrameRate), nullStr(res.HDRType),
		audioJSON(res), status, nullStr(msg), now, now}
	if x == nil {
		_, err := tx.ExecContext(ctx, `INSERT INTO item_extras (item_id, kind, title, rel_path, size, mtime, container, duration_ms, bitrate,
			video_codec, video_profile, video_level, bit_depth, width, height, resolution, frame_rate, hdr_type, audio_streams,
			probe_status, probe_error, probed_at, updated_at, library_id, path, added_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			append(cols, libID, f.abs, now)...)
		if err != nil {
			return fmt.Errorf("add extra %s: %w", f.rel, err)
		}
		return nil
	}
	_, err := tx.ExecContext(ctx, `UPDATE item_extras SET item_id = ?, kind = ?, title = ?, rel_path = ?, size = ?, mtime = ?, container = ?,
		duration_ms = ?, bitrate = ?, video_codec = ?, video_profile = ?, video_level = ?, bit_depth = ?, width = ?, height = ?,
		resolution = ?, frame_rate = ?, hdr_type = ?, audio_streams = ?, probe_status = ?, probe_error = ?, probed_at = ?,
		updated_at = ?, missing_since = NULL WHERE id = ?`, append(cols, x.id)...)
	return err
}
