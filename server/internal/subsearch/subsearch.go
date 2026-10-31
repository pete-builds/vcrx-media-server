// Package subsearch finds subtitles a file is missing on OpenSubtitles,
// downloads the one picked, and registers it as an external subtitle track so
// every client lists it. Files are written under the server's own data
// directory, never beside the media: the library mounts are read-only.
package subsearch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/library"
	"github.com/pete-builds/vcrx-media-server/server/internal/opensubtitles"
)

// Provider is OpenSubtitles, or a stub of it for development.
type Provider interface {
	Search(ctx context.Context, q opensubtitles.Query) ([]opensubtitles.Result, error)
	Download(ctx context.Context, fileID int64) (opensubtitles.File, error)
	Quota(ctx context.Context) (opensubtitles.Quota, error)
	LastQuota() opensubtitles.Quota
	CanDownload() bool
}

var (
	// ErrOff means no provider is configured.
	ErrOff = errors.New("subtitle search is not set up on this server")
	// ErrNotSearched means a download named a file no recent search offered for
	// that version. Downloads spend quota, so only search results are fetched.
	ErrNotSearched = errors.New("search for subtitles again before downloading")
	// ErrBadFile means the provider sent a file name or content that is refused.
	ErrBadFile  = errors.New("the subtitle file was refused")
	ErrNotFound = errors.New("not found")
)

type Service struct {
	DB       *sql.DB
	Provider Provider
	// Dir holds downloaded files, one folder per version.
	Dir string
	// Languages are the preferred subtitle languages as ISO 639-2/B codes,
	// used when a client names none and by the automatic fetch.
	Languages []string
	Log       *slog.Logger
	Now       func() time.Time

	mu      sync.Mutex
	offered map[offerKey]offer
	recent  map[string]recentSearch
	kick    chan struct{}
}

type offerKey struct{ version, file int64 }

type offer struct {
	hit Hit
	at  time.Time
}

type recentSearch struct {
	hits []Hit
	at   time.Time
}

// New returns a service. A nil provider leaves search off.
func New(db *sql.DB, p Provider, dir string, languages []string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	var langs []string
	for _, l := range languages {
		if c := library.LanguageCode(l); c != "" && !contains(langs, c) {
			langs = append(langs, c)
		}
	}
	if len(langs) == 0 {
		langs = []string{"eng"}
	}
	return &Service{DB: db, Provider: p, Dir: filepath.Clean(dir), Languages: langs, Log: log,
		offered: map[offerKey]offer{}, recent: map[string]recentSearch{}, kick: make(chan struct{}, 1)}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Enabled reports whether search works at all.
func (s *Service) Enabled() bool { return s != nil && s.Provider != nil }

// CanDownload reports whether downloads are configured (an account is set).
func (s *Service) CanDownload() bool { return s.Enabled() && s.Provider.CanDownload() }

// Hit is one search result as clients see it.
type Hit struct {
	FileID int64 `json:"file_id"`
	// Language is ISO 639-2/B like every other track; LanguageCode is the
	// provider's own two-letter code.
	Language        *string `json:"language"`
	LanguageCode    string  `json:"language_code"`
	Release         string  `json:"release"`
	FileName        string  `json:"file_name"`
	HearingImpaired bool    `json:"hearing_impaired"`
	HashMatch       bool    `json:"hash_match"`
	Trusted         bool    `json:"trusted"`
	DownloadCount   int     `json:"download_count"`
	Uploader        string  `json:"uploader"`
	// MatchedBy is how it was found: hash, id or title.
	MatchedBy string `json:"matched_by"`
	// StreamID is set when this file is already downloaded for the version.
	StreamID *int64 `json:"stream_id"`
}

// target is what a search needs to know about a version.
type target struct {
	path, root         string
	kind, title        string
	year               int
	imdb, tmdb         int64
	season, episode    int
	showTitle          string
	showIMDb, showTMDb int64
}

func (s *Service) target(ctx context.Context, versionID int64) (target, error) {
	var t target
	var imdb, showIMDb string
	err := s.DB.QueryRowContext(ctx, `SELECT v.path, l.root_path, i.kind, i.title, IFNULL(i.year, 0), IFNULL(i.imdb_id, ''),
			IFNULL(i.tmdb_id, 0), IFNULL(i.season_number, 0), IFNULL(i.episode_number, 0),
			IFNULL(sh.title, ''), IFNULL(sh.imdb_id, ''), IFNULL(sh.tmdb_id, 0)
		FROM media_versions v JOIN items i ON i.id = v.item_id JOIN libraries l ON l.id = v.library_id
		LEFT JOIN items se ON se.id = i.parent_id AND i.kind = 'episode'
		LEFT JOIN items sh ON sh.id = se.parent_id
		WHERE v.id = ?`, versionID).Scan(&t.path, &t.root, &t.kind, &t.title, &t.year, &imdb, &t.tmdb,
		&t.season, &t.episode, &t.showTitle, &showIMDb, &t.showTMDb)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	t.imdb, t.showIMDb = imdbNumber(imdb), imdbNumber(showIMDb)
	return t, err
}

// imdbNumber turns "tt0123456" into 123456, the form the API wants.
func imdbNumber(s string) int64 {
	n, err := strconv.ParseInt(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "tt"), 10, 64)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// queries builds the search ladder: the file's hash, then its ids (the show's
// ids with season and episode for an episode), then title and year.
func (t target) queries(hash string, langs []string) []stageQuery {
	var out []stageQuery
	if hash != "" {
		out = append(out, stageQuery{"hash", opensubtitles.Query{MovieHash: hash, Languages: langs}})
	}
	if t.kind == "episode" {
		if t.showIMDb > 0 || t.showTMDb > 0 {
			q := opensubtitles.Query{Season: t.season, Episode: t.episode, Type: "episode", Languages: langs}
			if t.showIMDb > 0 {
				q.ParentIMDbID = t.showIMDb
			} else {
				q.ParentTMDbID = t.showTMDb
			}
			out = append(out, stageQuery{"id", q})
		}
		if t.showTitle != "" && t.season > 0 && t.episode > 0 {
			out = append(out, stageQuery{"title", opensubtitles.Query{Title: t.showTitle, Season: t.season, Episode: t.episode,
				Type: "episode", Languages: langs}})
		}
		return out
	}
	if t.imdb > 0 || t.tmdb > 0 {
		q := opensubtitles.Query{Type: "movie", Languages: langs}
		if t.imdb > 0 {
			q.IMDbID = t.imdb
		} else {
			q.TMDbID = t.tmdb
		}
		out = append(out, stageQuery{"id", q})
	}
	if t.title != "" {
		out = append(out, stageQuery{"title", opensubtitles.Query{Title: t.title, Year: t.year, Type: "movie", Languages: langs}})
	}
	return out
}

type stageQuery struct {
	stage string
	q     opensubtitles.Query
}

// providerLanguages turns any language tags into the provider's two-letter
// codes, falling back to the server's preferred languages.
func (s *Service) providerLanguages(tags []string) []string {
	if len(tags) == 0 {
		tags = s.Languages
	}
	var out []string
	for _, t := range tags {
		if c := library.ISO6391(t); c != "" && !contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// Search finds subtitles for a version in the given languages (any form; the
// server's preferred ones when empty). The caller has already checked the
// user may see the version.
func (s *Service) Search(ctx context.Context, versionID int64, languages []string, preferHI bool) ([]Hit, error) {
	if !s.Enabled() {
		return nil, ErrOff
	}
	langs := s.providerLanguages(languages)
	if len(langs) == 0 {
		return nil, fmt.Errorf("%w: no known language in %q", ErrBadRequest, strings.Join(languages, ","))
	}
	key := fmt.Sprintf("%d|%s|%t", versionID, strings.Join(langs, ","), preferHI)
	s.mu.Lock()
	if r, ok := s.recent[key]; ok && s.now().Sub(r.at) < 10*time.Minute {
		s.mu.Unlock()
		return s.markDownloaded(ctx, versionID, r.hits)
	}
	s.mu.Unlock()

	t, err := s.target(ctx, versionID)
	if err != nil {
		return nil, err
	}
	hits, err := s.search(ctx, t, langs, preferHI, false)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	now := s.now()
	for k, o := range s.offered {
		if now.Sub(o.at) > time.Hour {
			delete(s.offered, k)
		}
	}
	for k, r := range s.recent {
		if now.Sub(r.at) > 10*time.Minute {
			delete(s.recent, k)
		}
	}
	for _, h := range hits {
		s.offered[offerKey{versionID, h.FileID}] = offer{hit: h, at: now}
	}
	s.recent[key] = recentSearch{hits: hits, at: now}
	s.mu.Unlock()
	return s.markDownloaded(ctx, versionID, hits)
}

// ErrBadRequest marks a search the caller worded wrongly.
var ErrBadRequest = errors.New("bad request")

// search walks the ladder. It stops after the id stage when that found
// anything, so a title search (the least exact) only runs as a fallback.
// strict, for the automatic fetch, skips the title stage entirely.
func (s *Service) search(ctx context.Context, t target, langs []string, preferHI, strict bool) ([]Hit, error) {
	hash := ""
	if src, err := fsafe.ResolveUnder(t.root, t.path); err == nil {
		if h, err := opensubtitles.MovieHash(src); err == nil {
			hash = h
		}
	}
	seen := map[int64]int{}
	var hits []Hit
	idFound := false
	for _, sq := range t.queries(hash, langs) {
		if sq.stage == "title" && (idFound || strict) {
			break
		}
		res, err := s.Provider.Search(ctx, sq.q)
		if err != nil {
			if len(hits) > 0 {
				// Keep what the earlier stages found rather than fail the search.
				s.Log.Warn("subtitle search stage failed", "stage", sq.stage, "err", err)
				break
			}
			return nil, err
		}
		for _, r := range res {
			if i, ok := seen[r.FileID]; ok {
				hits[i].HashMatch = hits[i].HashMatch || r.HashMatch || sq.stage == "hash"
				continue
			}
			seen[r.FileID] = len(hits)
			hits = append(hits, toHit(r, sq.stage))
		}
		if sq.stage == "id" && len(res) > 0 {
			idFound = true
		}
	}
	rank(hits, preferHI)
	return hits, nil
}

func toHit(r opensubtitles.Result, stage string) Hit {
	h := Hit{FileID: r.FileID, LanguageCode: r.Language, Release: r.Release, FileName: r.FileName,
		HearingImpaired: r.HearingImpaired, HashMatch: r.HashMatch || stage == "hash", Trusted: r.Trusted,
		DownloadCount: r.DownloadCount, Uploader: r.Uploader, MatchedBy: stage}
	if h.HashMatch {
		h.MatchedBy = "hash"
	}
	if c := library.LanguageCode(r.Language); c != "" {
		h.Language = &c
	}
	return h
}

// rank orders hits: a hash match first (it was made for this exact file), then
// trusted uploaders, then the hearing-impaired preference, then popularity.
func rank(hits []Hit, preferHI bool) {
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.HashMatch != b.HashMatch {
			return a.HashMatch
		}
		if a.Trusted != b.Trusted {
			return a.Trusted
		}
		if (a.HearingImpaired == preferHI) != (b.HearingImpaired == preferHI) {
			return a.HearingImpaired == preferHI
		}
		return a.DownloadCount > b.DownloadCount
	})
}

// markDownloaded fills StreamID for files this version already has.
func (s *Service) markDownloaded(ctx context.Context, versionID int64, hits []Hit) ([]Hit, error) {
	out := make([]Hit, len(hits))
	copy(out, hits)
	have := map[int64]int64{}
	rows, err := s.DB.QueryContext(ctx, `SELECT d.provider_file_id, s.id FROM downloaded_subtitles d
		JOIN media_streams s ON s.downloaded_id = d.id WHERE d.version_id = ?`, versionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var f, id int64
		if err := rows.Scan(&f, &id); err != nil {
			return nil, err
		}
		have[f] = id
	}
	for i := range out {
		if id, ok := have[out[i].FileID]; ok {
			out[i].StreamID = &id
		} else {
			out[i].StreamID = nil
		}
	}
	return out, rows.Err()
}

// Downloaded is a subtitle registered on a version.
type Downloaded struct {
	StreamID        int64   `json:"stream_id"`
	VersionID       int64   `json:"version_id"`
	Language        *string `json:"language"`
	Release         string  `json:"release"`
	HearingImpaired bool    `json:"hearing_impaired"`
	HashMatch       bool    `json:"hash_match"`
	Codec           string  `json:"codec"`
	// Existing is true when the file was already there and no quota was spent.
	Existing bool `json:"existing"`
}

// Download fetches a file a recent search offered for this version, stores it
// under Dir and adds it as an external subtitle track. userID 0 is the
// automatic fetch.
func (s *Service) Download(ctx context.Context, versionID, fileID, userID int64) (Downloaded, error) {
	if !s.Enabled() {
		return Downloaded{}, ErrOff
	}
	s.mu.Lock()
	o, ok := s.offered[offerKey{versionID, fileID}]
	s.mu.Unlock()
	if !ok {
		return Downloaded{}, ErrNotSearched
	}
	return s.download(ctx, versionID, o.hit, userID)
}

func (s *Service) download(ctx context.Context, versionID int64, hit Hit, userID int64) (Downloaded, error) {
	d := Downloaded{VersionID: versionID, Language: hit.Language, Release: hit.Release, HearingImpaired: hit.HearingImpaired, HashMatch: hit.HashMatch}
	err := s.DB.QueryRowContext(ctx, `SELECT s.id, s.codec FROM downloaded_subtitles d JOIN media_streams s ON s.downloaded_id = d.id
		WHERE d.version_id = ? AND d.provider = 'opensubtitles' AND d.provider_file_id = ?`, versionID, hit.FileID).Scan(&d.StreamID, &d.Codec)
	if err == nil {
		d.Existing = true
		return d, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}
	// The name the search offered is checked before any quota is spent.
	if _, err := fileExt(hit.FileName); hit.FileName != "" && err != nil {
		return d, err
	}
	f, err := s.Provider.Download(ctx, hit.FileID)
	if err != nil {
		return d, err
	}
	ext, err := fileExt(f.Name)
	if err != nil {
		s.Log.Warn("subtitle download refused", "version", versionID, "file_id", hit.FileID, "name", f.Name, "err", err)
		return d, err
	}
	if len(f.Content) == 0 || strings.IndexByte(string(f.Content), 0) >= 0 {
		return d, fmt.Errorf("%w: empty or not text", ErrBadFile)
	}
	d.Codec = map[string]string{"srt": "subrip", "vtt": "webvtt", "ass": "ass", "ssa": "ass"}[ext]
	return d, s.store(ctx, &d, hit, ext, f.Content, userID)
}

// fileExt accepts a provider file name only when it is a plain name with a
// text subtitle extension. The name never becomes a path (the server names
// the file itself), but a name with a separator or a leading dot means the
// answer is not what it claims to be, so it is refused outright.
func fileExt(name string) (string, error) {
	if name == "" {
		return "srt", nil
	}
	if strings.ContainsAny(name, "/\\\x00") || strings.HasPrefix(name, ".") || filepath.IsAbs(name) {
		return "", fmt.Errorf("%w: unsafe file name %q", ErrBadFile, name)
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "srt", "vtt", "ass", "ssa":
		return ext, nil
	}
	return "", fmt.Errorf("%w: %q is not a text subtitle", ErrBadFile, name)
}

// store writes the file and its rows in one transaction; a failed write
// leaves neither a row nor a file behind.
func (s *Service) store(ctx context.Context, d *Downloaded, hit Hit, ext string, content []byte, userID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var by any
	if userID > 0 {
		by = userID
	}
	now := s.now().UnixMilli()
	res, err := tx.ExecContext(ctx, `INSERT INTO downloaded_subtitles (version_id, provider, provider_file_id, language, release_name,
			hearing_impaired, hash_match, rel_path, downloaded_by, created_at) VALUES (?, 'opensubtitles', ?, ?, ?, ?, ?, '', ?, ?)`,
		d.VersionID, hit.FileID, d.Language, nullStr(hit.Release), hit.HearingImpaired, hit.HashMatch, by, now)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	lang := "und"
	if d.Language != nil {
		lang = *d.Language
	}
	rel := filepath.Join(strconv.FormatInt(d.VersionID, 10), fmt.Sprintf("%d.%s.%s", id, lang, ext))
	full := filepath.Join(s.Dir, rel)
	if !fsafe.Within(s.Dir, full) {
		return fmt.Errorf("%w: path outside the download dir", ErrBadFile)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return err
	}
	tmp := full + ".tmp"
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, full); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(full)
		}
	}()
	if _, err := tx.ExecContext(ctx, `UPDATE downloaded_subtitles SET rel_path = ? WHERE id = ?`, rel, id); err != nil {
		return err
	}
	res, err = tx.ExecContext(ctx, `INSERT INTO media_streams (version_id, kind, codec, language, is_hearing_impaired,
			is_external, external_path, downloaded_id) VALUES (?, 'subtitle', ?, ?, ?, 1, ?, ?)`,
		d.VersionID, d.Codec, d.Language, hit.HearingImpaired, rel, id)
	if err != nil {
		return err
	}
	if d.StreamID, err = res.LastInsertId(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	s.Log.Info("subtitle downloaded", "version", d.VersionID, "language", lang, "release", hit.Release, "hash_match", hit.HashMatch)
	return nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Delete removes a downloaded subtitle: its track, its row and its file.
// Sidecar files and embedded tracks are not this package's to delete.
func (s *Service) Delete(ctx context.Context, streamID int64) error {
	var id int64
	var rel string
	err := s.DB.QueryRowContext(ctx, `SELECT d.id, d.rel_path FROM media_streams s JOIN downloaded_subtitles d ON d.id = s.downloaded_id
		WHERE s.id = ?`, streamID).Scan(&id, &rel)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM downloaded_subtitles WHERE id = ?`, id); err != nil {
		return err
	}
	if p, err := fsafe.ResolveUnder(s.Dir, rel); err == nil {
		if err := os.Remove(p); err != nil {
			s.Log.Warn("remove downloaded subtitle", "err", err)
		}
	}
	return nil
}

// Listed is a downloaded subtitle as admins see it.
type Listed struct {
	Downloaded
	CreatedAt    int64   `json:"created_at"`
	DownloadedBy *string `json:"downloaded_by"`
}

// ForItem lists the downloaded subtitles of an item's versions.
func (s *Service) ForItem(ctx context.Context, itemID int64) ([]Listed, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT s.id, d.version_id, d.language, IFNULL(d.release_name, ''), d.hearing_impaired,
			d.hash_match, IFNULL(s.codec, ''), d.created_at, u.username
		FROM downloaded_subtitles d JOIN media_streams s ON s.downloaded_id = d.id
		JOIN media_versions v ON v.id = d.version_id LEFT JOIN users u ON u.id = d.downloaded_by
		WHERE v.item_id = ? ORDER BY d.created_at, d.id`, itemID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Listed{}
	for rows.Next() {
		var l Listed
		var lang, by sql.NullString
		if err := rows.Scan(&l.StreamID, &l.VersionID, &lang, &l.Release, &l.HearingImpaired, &l.HashMatch, &l.Codec, &l.CreatedAt, &by); err != nil {
			return nil, err
		}
		if lang.Valid {
			l.Language = &lang.String
		}
		if by.Valid {
			l.DownloadedBy = &by.String
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Quota is the provider's remaining downloads; it never fails, because it is
// only shown. ok is false when it is not known.
func (s *Service) Quota(ctx context.Context) (opensubtitles.Quota, bool) {
	if !s.CanDownload() {
		return opensubtitles.Quota{}, false
	}
	q, err := s.Provider.Quota(ctx)
	if err != nil {
		q = s.Provider.LastQuota()
	}
	return q, q.Known
}
