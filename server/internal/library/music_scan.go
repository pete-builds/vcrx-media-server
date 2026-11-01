package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/probe"
)

const (
	// maxInfoBytes caps how much of a show's info file is kept.
	maxInfoBytes = 64 << 10
	// maxChecksumBytes caps a checksum file; a show has a few dozen lines.
	maxChecksumBytes = 1 << 20
)

// musicWalk is what one walk of a music library found.
type musicWalk struct {
	w       *walker
	files   []*musicFile
	folders map[string]*musicFolder
	// textFiles are .txt, .md5 and .ffp names per folder, read after the walk.
	textFiles map[string][]string
}

// scanMusic indexes a music library. It keeps the video scanner's safety
// rules: a missing or emptied root changes nothing, a file that vanishes is
// marked missing and kept for MissingRetention, and nothing is written until
// the walk is complete and the checks pass.
func (s *Scanner) scanMusic(ctx context.Context, lib Library, opt Options, run *Run) (warning string, err error) {
	ap, ok := s.Prober.(probe.AudioProber)
	if !ok {
		return "", errors.New("the scanner's prober cannot read audio files")
	}
	log := s.log().With("library", lib.Name, "run", run.ID)
	if err := checkRoot(lib.RootPath); err != nil {
		return "", err
	}
	sub, isFile, err := resolveSubPath(lib.RootPath, opt.SubPath)
	if err != nil {
		return "", err
	}
	run.PathFilter = sub
	mw := &musicWalk{w: newWalker(lib.RootPath, KindMusic, log), folders: map[string]*musicFolder{}, textFiles: map[string][]string{}}
	switch {
	case musicSkipped(sub, isFile):
	case isFile:
		if err := mw.folder(parentRel(sub)); err != nil {
			return "", fmt.Errorf("read %s: %w", parentRel(sub), err)
		}
		mw.addFile(sub)
	default:
		if err := mw.walk(ctx, sub); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("%w: %s: %w", ErrRootUnavailable, mw.w.absOf(sub), err)
		}
	}
	run.Seen = len(mw.files)

	stored, err := loadMusicTracks(ctx, s.DB, lib.ID)
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for _, f := range mw.files {
		seen[f.abs] = true
	}
	// As for video: files already missing and still not back count toward
	// the half rule, so losing folders a scan at a time aborts too.
	present, gone, stillMissing, indexed := 0, 0, 0, 0
	for _, t := range stored {
		if !inScope(t.rel, sub, isFile) {
			continue
		}
		indexed++
		if t.missingSince != 0 {
			if !seen[t.abs] {
				stillMissing++
			}
			continue
		}
		present++
		if !seen[t.abs] && !mw.w.underFailed(t.rel) {
			gone++
		}
	}
	if !opt.Force {
		if len(mw.files) == 0 && present > 0 {
			where := lib.RootPath
			if sub != "" {
				where = mw.w.absOf(sub)
			}
			return "", abortf("%s holds no audio files but %d are indexed under it; the share may be unmounted or emptied. Nothing was changed; rescan with force to accept", where, present)
		}
		if sub == "" && gone > 0 && (gone+stillMissing)*2 > indexed {
			return "", abortf("%d of %d indexed files (more than half) are no longer under %s%s. Nothing was changed; rescan with force to accept",
				gone+stillMissing, indexed, lib.RootPath, alreadyMissing(stillMissing))
		}
	}

	// Every file the library holds takes part in grouping: the walked ones as
	// found, the rest (outside a targeted scan, or gone) as stored.
	nowMs := s.now().UnixMilli()
	var all, toProbe []*musicFile
	for _, f := range mw.files {
		old := stored[f.abs]
		switch {
		case old == nil:
			f.needsProbe = true
		case old.size != f.size || old.mtime != f.mtime:
			f.needsProbe, f.changed = true, true
		case old.probeStatus == "error" && (opt.Force || opt.Trigger == "manual" || opt.Trigger == "cli" || nowMs-old.probedAt > ProbeRetryAfter.Milliseconds()):
			f.needsProbe, f.changed = true, true
		default:
			*f = *old
			f.missingSince = 0
		}
		if f.needsProbe {
			toProbe = append(toProbe, f)
		}
		all = append(all, f)
	}
	for abs, t := range stored {
		if seen[abs] {
			continue
		}
		if inScope(t.rel, sub, isFile) && t.missingSince == 0 && !mw.w.underFailed(t.rel) {
			t.missingSince = nowMs
			run.Missing++
		}
		all = append(all, t)
	}
	run.Failed += s.probeMusic(ctx, ap, toProbe, nowMs)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	for _, f := range all {
		folder, _ := albumFolderOf(f.rel)
		for _, dir := range []string{folder, parentRel(folder)} {
			if _, ok := mw.folders[dir]; !ok {
				// A folder outside a targeted scan: its listing still decides
				// whether it is a show and what its cover is.
				_ = mw.folder(dir)
			}
		}
	}
	mw.readTextFiles(log)

	albums, artists := groupMusic(all, mw.folders)
	if err := s.writeMusic(ctx, lib, albums, artists, run, nowMs); err != nil {
		return "", err
	}
	if len(mw.w.failed) > 0 {
		warning = fmt.Sprintf("completed with warnings: %d folders could not be read (first: %s); their files were left as they were", len(mw.w.failed), mw.w.failed[0])
	}
	return warning, nil
}

// musicSkipped reports whether a targeted path lies in a folder the walk skips.
func musicSkipped(rel string, isFile bool) bool {
	parts := strings.Split(rel, "/")
	if isFile {
		parts = parts[:len(parts)-1]
	}
	for _, p := range parts {
		if p != "" && musicSkipDir(p) {
			return true
		}
	}
	return false
}

// musicSkipDir skips hidden and system folders. Unlike a movie library, a
// folder called Interviews or Extras in a music library may well hold music.
func musicSkipDir(name string) bool {
	return strings.HasPrefix(name, ".") || junkDirs[strings.ToLower(strings.TrimSpace(name))]
}

// folder lists one folder (not its subfolders) and files what it holds.
func (mw *musicWalk) folder(rel string) error {
	l, err := mw.w.read(rel)
	if err != nil {
		return err
	}
	f := mw.folders[rel]
	if f == nil {
		f = &musicFolder{rel: rel}
		mw.folders[rel] = f
	}
	for _, e := range l.entries {
		if e.isDir || strings.HasPrefix(e.name, ".") {
			continue
		}
		switch ext := strings.ToLower(path.Ext(e.name)); {
		case IsAudio(e.name):
			f.hasAudio = true
		case isImage(e.name):
			f.images = append(f.images, e.name)
		case ext == ".txt" || ext == ".md5" || ext == ".ffp":
			mw.textFiles[rel] = append(mw.textFiles[rel], e.name)
		}
	}
	return nil
}

func (mw *musicWalk) walk(ctx context.Context, start string) error {
	if _, err := mw.w.read(start); err != nil {
		return err
	}
	stack := []string{start}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if err := mw.folder(rel); err != nil {
			mw.w.log.Warn("folder unreadable, its files will not be marked missing", "path", rel, "err", err)
			mw.w.failed = append(mw.w.failed, rel)
			continue
		}
		var subdirs []string
		for _, e := range mw.w.dirs[rel].entries {
			child := joinRel(rel, e.name)
			if e.isDir {
				if !musicSkipDir(e.name) {
					subdirs = append(subdirs, child)
				}
				continue
			}
			mw.addFile(child)
		}
		for i := len(subdirs) - 1; i >= 0; i-- {
			stack = append(stack, subdirs[i])
		}
	}
	return nil
}

func (mw *musicWalk) addFile(rel string) {
	name := path.Base(rel)
	if !IsAudio(name) || strings.HasPrefix(name, ".") {
		return
	}
	abs := mw.w.absOf(rel)
	fi, err := os.Stat(abs)
	if err != nil || !fi.Mode().IsRegular() {
		return
	}
	mw.files = append(mw.files, &musicFile{rel: rel, abs: abs, size: fi.Size(), mtime: fi.ModTime().UnixMilli()})
}

// readTextFiles reads each folder's info file and checksum files, then hands
// a disc folder's checksums to its album folder, named from there.
func (mw *musicWalk) readTextFiles(log *slog.Logger) {
	for rel, names := range mw.textFiles {
		f := mw.folders[rel]
		var infos []string
		for _, n := range names {
			lower := strings.ToLower(n)
			algo := ""
			switch {
			case strings.HasSuffix(lower, ".md5") || (strings.HasSuffix(lower, ".txt") && strings.Contains(lower, "md5")):
				algo = "md5"
			case strings.HasSuffix(lower, ".ffp") || (strings.HasSuffix(lower, ".txt") && strings.Contains(lower, "ffp")):
				algo = "ffp"
			}
			if algo == "" {
				infos = append(infos, n)
				continue
			}
			text, err := mw.readText(joinRel(rel, n), maxChecksumBytes)
			if err != nil {
				log.Warn("checksum file unreadable", "path", joinRel(rel, n), "err", err)
				continue
			}
			for _, c := range ParseChecksumFile("x."+algo, text) {
				f.checksums = append(f.checksums, folderChecksum{Checksum: c, source: n})
			}
		}
		if info := pickInfoFile(path.Base(rel), infos); info != "" {
			text, err := mw.readText(joinRel(rel, info), maxInfoBytes)
			if err != nil {
				log.Warn("info file unreadable", "path", joinRel(rel, info), "err", err)
			} else {
				f.infoPath, f.infoText = joinRel(rel, info), text
			}
		}
	}
	for rel, f := range mw.folders {
		if DiscFolder(path.Base(rel)) == 0 || !strings.Contains(rel, "/") {
			continue
		}
		parent := mw.folders[parentRel(rel)]
		if parent == nil {
			continue
		}
		prefix := path.Base(rel) + "/"
		for _, c := range f.checksums {
			c.File, c.source = prefix+c.File, prefix+c.source
			parent.checksums = append(parent.checksums, c)
		}
		if parent.infoPath == "" {
			parent.infoPath, parent.infoText = f.infoPath, f.infoText
		}
	}
}

// pickInfoFile chooses a show's info file among a folder's .txt files: one
// named like the folder or with "info" in its name, else the first by name.
func pickInfoFile(folder string, names []string) string {
	best := ""
	for _, n := range names {
		stem := strings.ToLower(stripExt(n))
		if stem == strings.ToLower(folder) || strings.Contains(stem, "info") {
			return n
		}
		if best == "" || n < best {
			best = n
		}
	}
	return best
}

// readText reads at most limit bytes of a small text file under the root.
// Tapers' files are often Windows-1252; bytes that are not UTF-8 are read as
// Latin-1 so the text survives rather than turning into replacement marks.
func (mw *musicWalk) readText(rel string, limit int64) (string, error) {
	f, _, err := fsafe.OpenUnder(mw.w.root, rel)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return "", err
	}
	if utf8.Valid(b) {
		return string(b), nil
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r), nil
}

// probeMusic probes files in parallel and returns how many failed.
func (s *Scanner) probeMusic(ctx context.Context, ap probe.AudioProber, files []*musicFile, nowMs int64) int {
	n := s.ProbeConcurrency
	if n <= 0 {
		n = DefaultProbeConcurrency
	}
	jobs := make(chan *musicFile)
	var wg sync.WaitGroup
	var mu sync.Mutex
	failed := 0
	for range min(n, max(len(files), 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				res, err := ap.ProbeAudio(ctx, f.abs)
				f.probedAt = nowMs
				if err != nil {
					f.probeStatus, f.probeError, f.tags = "error", clipText(err.Error(), 500), map[string]string{}
					mu.Lock()
					failed++
					mu.Unlock()
					continue
				}
				f.probeStatus, f.probeError = "ok", ""
				f.tags, f.container, f.codec = res.Tags, res.Container, res.Codec
				f.durationMs, f.bitrate, f.sampleRate, f.channels, f.bitDepth = res.DurationMs, res.Bitrate, res.SampleRate, res.Channels, res.BitDepth
				f.embedded = res.EmbeddedCover
			}
		}()
	}
	for _, f := range files {
		select {
		case jobs <- f:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	return failed
}

func clipText(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// loadMusicTracks reads the stored tracks of a library by absolute path.
func loadMusicTracks(ctx context.Context, conn *sql.DB, libraryID int64) (map[string]*musicFile, error) {
	rows, err := conn.QueryContext(ctx, `SELECT path, rel_path, size, mtime, tags, COALESCE(container, ''), COALESCE(codec, ''),
			COALESCE(duration_ms, 0), COALESCE(bitrate, 0), COALESCE(sample_rate, 0), COALESCE(channels, 0), COALESCE(bit_depth, 0),
			embedded_cover, probe_status, COALESCE(probe_error, ''), COALESCE(probed_at, 0), COALESCE(missing_since, 0)
		FROM music_tracks WHERE library_id = ?`, libraryID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]*musicFile{}
	for rows.Next() {
		f := &musicFile{}
		var tags string
		if err := rows.Scan(&f.abs, &f.rel, &f.size, &f.mtime, &tags, &f.container, &f.codec, &f.durationMs, &f.bitrate,
			&f.sampleRate, &f.channels, &f.bitDepth, &f.embedded, &f.probeStatus, &f.probeError, &f.probedAt, &f.missingSince); err != nil {
			return nil, err
		}
		f.tags = map[string]string{}
		_ = json.Unmarshal([]byte(tags), &f.tags)
		out[f.abs] = f
	}
	return out, rows.Err()
}

// writeMusic writes one scan's grouping in a single transaction.
func (s *Scanner) writeMusic(ctx context.Context, lib Library, albums []*albumPlan, artists map[string]*artistPlan, run *Run, nowMs int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	artistIDs := map[*artistPlan]int64{}
	for _, a := range artists {
		var id int64
		err := tx.QueryRowContext(ctx, `INSERT INTO music_artists (library_id, group_key, name, sort_name, musicbrainz_id, image_path, added_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (library_id, group_key) DO UPDATE SET name = excluded.name, sort_name = excluded.sort_name,
				musicbrainz_id = COALESCE(excluded.musicbrainz_id, music_artists.musicbrainz_id), image_path = excluded.image_path,
				updated_at = excluded.updated_at
			RETURNING id`, lib.ID, a.key, a.name, a.sort, nullStr(a.mbid), nullStr(a.image), nowMs, nowMs).Scan(&id)
		if err != nil {
			return fmt.Errorf("artist %q: %w", a.name, err)
		}
		artistIDs[a] = id
	}

	for _, al := range albums {
		added := int64(0)
		for _, tp := range al.tracks {
			if added == 0 || tp.file.mtime < added {
				added = tp.file.mtime
			}
		}
		if added <= 0 || added > nowMs {
			added = nowMs
		}
		var albumID int64
		err := tx.QueryRowContext(ctx, `INSERT INTO music_albums (library_id, artist_id, group_key, title, sort_title, year, date, genre,
				compilation, live, venue, location, source, source_detail, lineage, taper, info_path, info_text, folder, musicbrainz_id,
				musicbrainz_release_group_id, replaygain_gain, replaygain_peak, cover_source, cover_path, added_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (library_id, group_key) DO UPDATE SET artist_id = excluded.artist_id, title = excluded.title,
				sort_title = excluded.sort_title, year = excluded.year, date = excluded.date, genre = excluded.genre,
				compilation = excluded.compilation, live = excluded.live, venue = excluded.venue, location = excluded.location,
				source = excluded.source, source_detail = excluded.source_detail, lineage = excluded.lineage, taper = excluded.taper,
				info_path = excluded.info_path, info_text = excluded.info_text, folder = excluded.folder,
				musicbrainz_id = excluded.musicbrainz_id, musicbrainz_release_group_id = excluded.musicbrainz_release_group_id,
				replaygain_gain = excluded.replaygain_gain, replaygain_peak = excluded.replaygain_peak,
				cover_source = excluded.cover_source, cover_path = excluded.cover_path, updated_at = excluded.updated_at
			RETURNING id`,
			lib.ID, artistIDs[al.artist], al.key, al.title, sortName(al.title), nullInt(int64(al.year)), nullStr(al.date), nullStr(al.genre),
			al.compilation, al.live, nullStr(al.show.Venue), nullStr(al.show.Location), nullStr(al.show.Source), nullStr(al.show.SourceDetail),
			nullStr(al.show.Lineage), nullStr(al.show.Taper), nullStr(al.infoPath), nullStr(al.infoText), al.folder, nullStr(al.mbid),
			nullStr(al.mbRG), al.gain, al.peak, nullStr(al.coverSource), nullStr(al.coverPath), added, nowMs).Scan(&albumID)
		if err != nil {
			return fmt.Errorf("album %q: %w", al.title, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM music_checksums WHERE album_id = ?`, albumID); err != nil {
			return err
		}
		for _, c := range al.checksums {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO music_checksums (album_id, file_name, algorithm, value, source) VALUES (?, ?, ?, ?, ?)`,
				albumID, c.File, c.Algorithm, c.Value, c.source); err != nil {
				return err
			}
		}
		for _, tp := range al.tracks {
			if err := writeTrack(ctx, tx, lib.ID, albumID, artistIDs[tp.artist], tp, run, nowMs); err != nil {
				return fmt.Errorf("track %s: %w", tp.file.rel, err)
			}
		}
	}

	cutoff := nowMs - MissingRetention.Milliseconds()
	if _, err := tx.ExecContext(ctx, `DELETE FROM music_tracks WHERE library_id = ? AND missing_since IS NOT NULL AND missing_since < ?`,
		lib.ID, cutoff); err != nil {
		return err
	}
	// A regroup can leave an album or an artist with nothing; they go.
	for _, q := range []string{
		`DELETE FROM music_albums WHERE library_id = ? AND NOT EXISTS (SELECT 1 FROM music_tracks t WHERE t.album_id = music_albums.id)`,
		`DELETE FROM music_artists WHERE library_id = ? AND NOT EXISTS (SELECT 1 FROM music_albums a WHERE a.artist_id = music_artists.id)
			AND NOT EXISTS (SELECT 1 FROM music_tracks t WHERE t.artist_id = music_artists.id)`,
	} {
		if _, err := tx.ExecContext(ctx, q, lib.ID); err != nil {
			return err
		}
	}
	// What has no file left is hidden, not deleted, until retention runs out.
	for _, q := range []string{
		`UPDATE music_albums SET missing_since = CASE
			WHEN EXISTS (SELECT 1 FROM music_tracks t WHERE t.album_id = music_albums.id AND t.missing_since IS NULL) THEN NULL
			ELSE COALESCE(missing_since, ?) END WHERE library_id = ?`,
		`UPDATE music_artists SET missing_since = CASE
			WHEN EXISTS (SELECT 1 FROM music_albums a WHERE a.artist_id = music_artists.id AND a.missing_since IS NULL)
				OR EXISTS (SELECT 1 FROM music_tracks t WHERE t.artist_id = music_artists.id AND t.missing_since IS NULL) THEN NULL
			ELSE COALESCE(missing_since, ?) END WHERE library_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, nowMs, lib.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func writeTrack(ctx context.Context, tx *sql.Tx, libraryID, albumID, artistID int64, tp *trackPlan, run *Run, nowMs int64) error {
	f := tp.file
	tags, _ := json.Marshal(f.tags)
	res, err := tx.ExecContext(ctx, `UPDATE music_tracks SET album_id = ?, artist_id = ?, rel_path = ?, size = ?, mtime = ?, title = ?,
			artist_name = ?, disc_number = ?, track_number = ?, duration_ms = ?, container = ?, codec = ?, bitrate = ?, sample_rate = ?,
			channels = ?, bit_depth = ?, tags = ?, musicbrainz_recording_id = ?, replaygain_track_gain = ?, replaygain_track_peak = ?,
			embedded_cover = ?, probe_status = ?, probe_error = ?, probed_at = ?, missing_since = ?, updated_at = ?
		WHERE path = ?`,
		albumID, artistID, f.rel, f.size, f.mtime, tp.title, tp.artistName, nullInt(int64(tp.disc)), nullInt(int64(tp.track)),
		nullInt(f.durationMs), nullStr(f.container), nullStr(f.codec), nullInt(f.bitrate), nullInt(int64(f.sampleRate)),
		nullInt(int64(f.channels)), nullInt(int64(f.bitDepth)), string(tags), nullStr(tp.mbRecording), tp.gain, tp.peak, f.embedded,
		probeStatus(f), nullStr(f.probeError), nullInt(f.probedAt), nullInt(f.missingSince), nowMs, f.abs)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		if f.changed {
			run.Changed++
		}
		return nil
	}
	added := f.mtime
	if added <= 0 || added > nowMs {
		added = nowMs
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO music_tracks (library_id, album_id, artist_id, path, rel_path, size, mtime, title, artist_name,
			disc_number, track_number, duration_ms, container, codec, bitrate, sample_rate, channels, bit_depth, tags,
			musicbrainz_recording_id, replaygain_track_gain, replaygain_track_peak, embedded_cover, probe_status, probe_error, probed_at,
			missing_since, added_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		libraryID, albumID, artistID, f.abs, f.rel, f.size, f.mtime, tp.title, tp.artistName, nullInt(int64(tp.disc)), nullInt(int64(tp.track)),
		nullInt(f.durationMs), nullStr(f.container), nullStr(f.codec), nullInt(f.bitrate), nullInt(int64(f.sampleRate)),
		nullInt(int64(f.channels)), nullInt(int64(f.bitDepth)), string(tags), nullStr(tp.mbRecording), tp.gain, tp.peak, f.embedded,
		probeStatus(f), nullStr(f.probeError), nullInt(f.probedAt), nullInt(f.missingSince), added, nowMs)
	if err == nil {
		run.Added++
	}
	return err
}

func probeStatus(f *musicFile) string {
	if f.probeStatus == "" {
		return "pending"
	}
	return f.probeStatus
}
