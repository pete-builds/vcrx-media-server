// Package music answers for music libraries: artists, albums, tracks, search,
// play queues, cover art, and how each track reaches a device. The library
// package's music scan fills the tables; this package only reads them, and
// every query goes through the caller's library grants, as video does.
package music

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"strconv"
	"strings"

	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// ErrNotFound covers rows that do not exist and rows the caller may not see,
// so a friend cannot tell the two apart.
var ErrNotFound = errors.New("not found")

// Catalog reads music for one caller's policy.
type Catalog struct {
	DB *sql.DB
}

// Artist is an artist in a grid or a header.
type Artist struct {
	ID            int64   `json:"id"`
	LibraryID     int64   `json:"library_id"`
	Name          string  `json:"name"`
	SortName      string  `json:"sort_name"`
	MusicBrainzID *string `json:"musicbrainz_id"`
	AlbumCount    int     `json:"album_count"`
	TrackCount    int     `json:"track_count"`
	HasImage      bool    `json:"has_image"`
}

// Album is an album in a grid or a header. A live show carries its date,
// venue, place and source.
type Album struct {
	ID          int64   `json:"id"`
	LibraryID   int64   `json:"library_id"`
	ArtistID    int64   `json:"artist_id"`
	ArtistName  string  `json:"artist_name"`
	Title       string  `json:"title"`
	Year        *int64  `json:"year"`
	Date        *string `json:"date"`
	Genre       *string `json:"genre"`
	Compilation bool    `json:"compilation"`
	Live        bool    `json:"live"`
	Venue       *string `json:"venue"`
	Location    *string `json:"location"`
	Source      *string `json:"source"`
	TrackCount  int     `json:"track_count"`
	DurationMs  int64   `json:"duration_ms"`
	HasCover    bool    `json:"has_cover"`
	AddedAt     int64   `json:"added_at"`
}

// ReplayGain values in dB (gain) and linear (peak), when the tags have them.
type ReplayGain struct {
	TrackGain *float64 `json:"track_gain"`
	TrackPeak *float64 `json:"track_peak"`
	AlbumGain *float64 `json:"album_gain"`
	AlbumPeak *float64 `json:"album_peak"`
}

// Track is one playable file.
type Track struct {
	ID          int64      `json:"id"`
	LibraryID   int64      `json:"library_id"`
	AlbumID     int64      `json:"album_id"`
	ArtistID    int64      `json:"artist_id"`
	Title       string     `json:"title"`
	ArtistName  string     `json:"artist_name"`
	AlbumTitle  string     `json:"album_title"`
	AlbumArtist string     `json:"album_artist"`
	DiscNumber  *int64     `json:"disc_number"`
	TrackNumber *int64     `json:"track_number"`
	DurationMs  *int64     `json:"duration_ms"`
	Codec       *string    `json:"codec"`
	Container   *string    `json:"container"`
	Bitrate     *int64     `json:"bitrate"`
	SampleRate  *int64     `json:"sample_rate"`
	Channels    *int64     `json:"channels"`
	BitDepth    *int64     `json:"bit_depth"`
	ReplayGain  ReplayGain `json:"replaygain"`
	HasCover    bool       `json:"has_cover"`
	// MD5 and FFP are what the show's checksum files declare for this file.
	MD5 *string `json:"md5"`
	FFP *string `json:"ffp"`
}

// Checksum is one declared checksum of a show's files.
type Checksum struct {
	FileName  string `json:"file_name"`
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
	Source    string `json:"source"`
	// TrackID is the track the line names, when there is one.
	TrackID *int64 `json:"track_id"`
}

// AlbumDetail is an album page: the album, its tracks in order, and for a
// live show what the taper wrote.
type AlbumDetail struct {
	Album
	SourceDetail     *string    `json:"source_detail"`
	Lineage          *string    `json:"lineage"`
	Taper            *string    `json:"taper"`
	InfoText         *string    `json:"info_text"`
	MusicBrainzID    *string    `json:"musicbrainz_id"`
	ReleaseGroupMBID *string    `json:"musicbrainz_release_group_id"`
	ReplayGain       ReplayGain `json:"replaygain"`
	Tracks           []Track    `json:"tracks"`
	Checksums        []Checksum `json:"checksums"`
}

// ArtistDetail is an artist page: their albums, their live shows, and the
// compilations they appear on.
type ArtistDetail struct {
	Artist
	Albums    []Album `json:"albums"`
	Live      []Album `json:"live"`
	AppearsOn []Album `json:"appears_on"`
}

// libCond limits an alias's library_id to the caller's grants.
func libCond(pol policy.Policy, alias string) (string, []any) {
	if pol.IsAdmin {
		return "1", nil
	}
	if len(pol.LibraryIDs) == 0 {
		return "0", nil
	}
	args := make([]any, len(pol.LibraryIDs))
	for i, id := range pol.LibraryIDs {
		args[i] = id
	}
	return alias + ".library_id IN (" + strings.TrimSuffix(strings.Repeat("?,", len(args)), ",") + ")", args
}

const albumColumns = `a.id, a.library_id, a.artist_id, ar.name, a.title, a.year, a.date, a.genre, a.compilation, a.live, a.venue,
	a.location, a.source,
	(SELECT COUNT(*) FROM music_tracks t WHERE t.album_id = a.id AND t.missing_since IS NULL),
	(SELECT COALESCE(SUM(t.duration_ms), 0) FROM music_tracks t WHERE t.album_id = a.id AND t.missing_since IS NULL),
	a.cover_source IS NOT NULL, a.added_at`

func scanAlbum(rows interface{ Scan(...any) error }, a *Album) error {
	return rows.Scan(&a.ID, &a.LibraryID, &a.ArtistID, &a.ArtistName, &a.Title, &a.Year, &a.Date, &a.Genre, &a.Compilation, &a.Live,
		&a.Venue, &a.Location, &a.Source, &a.TrackCount, &a.DurationMs, &a.HasCover, &a.AddedAt)
}

func (c *Catalog) albums(ctx context.Context, pol policy.Policy, where string, args []any, order string, limit, offset int) ([]Album, error) {
	lc, largs := libCond(pol, "a")
	q := `SELECT ` + albumColumns + ` FROM music_albums a JOIN music_artists ar ON ar.id = a.artist_id
		WHERE a.missing_since IS NULL AND ` + lc + ` AND ` + where + ` ORDER BY ` + order
	all := append(largs, args...)
	if limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		all = append(all, limit, offset)
	}
	rows, err := c.DB.QueryContext(ctx, q, all...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Album{}
	for rows.Next() {
		var a Album
		if err := scanAlbum(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// musicLibrary fails with ErrNotFound unless the library exists, is a music
// library, and is granted.
func (c *Catalog) musicLibrary(ctx context.Context, pol policy.Policy, id int64) error {
	if !pol.CanSeeLibrary(id) {
		return ErrNotFound
	}
	var kind string
	err := c.DB.QueryRowContext(ctx, `SELECT kind FROM libraries WHERE id = ?`, id).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && kind != "music") {
		return ErrNotFound
	}
	return err
}

// Artists lists a library's album artists by sort name.
func (c *Catalog) Artists(ctx context.Context, pol policy.Policy, libraryID int64, limit, offset int) ([]Artist, int, error) {
	if err := c.musicLibrary(ctx, pol, libraryID); err != nil {
		return nil, 0, err
	}
	where := `ar.library_id = ? AND ar.missing_since IS NULL
		AND EXISTS (SELECT 1 FROM music_albums a WHERE a.artist_id = ar.id AND a.missing_since IS NULL)`
	var total int
	if err := c.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_artists ar WHERE `+where, libraryID).Scan(&total); err != nil {
		return nil, 0, err
	}
	out, err := c.artists(ctx, where+` ORDER BY ar.sort_name COLLATE NOCASE, ar.id LIMIT ? OFFSET ?`, libraryID, clampLimit(limit), max(0, offset))
	return out, total, err
}

func clampLimit(n int) int {
	if n <= 0 || n > 500 {
		return 100
	}
	return n
}

func (c *Catalog) artists(ctx context.Context, where string, args ...any) ([]Artist, error) {
	rows, err := c.DB.QueryContext(ctx, `SELECT ar.id, ar.library_id, ar.name, ar.sort_name, ar.musicbrainz_id,
			(SELECT COUNT(*) FROM music_albums a WHERE a.artist_id = ar.id AND a.missing_since IS NULL),
			(SELECT COUNT(*) FROM music_tracks t WHERE t.artist_id = ar.id AND t.missing_since IS NULL),
			ar.image_path IS NOT NULL OR EXISTS (SELECT 1 FROM music_albums a WHERE a.artist_id = ar.id AND a.missing_since IS NULL AND a.cover_source IS NOT NULL)
		FROM music_artists ar WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Artist{}
	for rows.Next() {
		var a Artist
		if err := rows.Scan(&a.ID, &a.LibraryID, &a.Name, &a.SortName, &a.MusicBrainzID, &a.AlbumCount, &a.TrackCount, &a.HasImage); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AlbumQuery pages through a library's albums.
type AlbumQuery struct {
	// Sort is title (the default), artist, year or added.
	Sort string
	// Live, when set, keeps only live shows (true) or only studio albums.
	Live          *bool
	Limit, Offset int
}

// Albums lists a library's albums.
func (c *Catalog) Albums(ctx context.Context, pol policy.Policy, libraryID int64, q AlbumQuery) ([]Album, int, error) {
	if err := c.musicLibrary(ctx, pol, libraryID); err != nil {
		return nil, 0, err
	}
	where, args := "a.library_id = ?", []any{libraryID}
	if q.Live != nil {
		where += " AND a.live = ?"
		args = append(args, *q.Live)
	}
	order := map[string]string{
		"artist": "ar.sort_name COLLATE NOCASE, a.date, a.sort_title COLLATE NOCASE",
		"year":   "COALESCE(a.date, '') DESC, a.sort_title COLLATE NOCASE",
		"added":  "a.added_at DESC, a.id DESC",
	}[q.Sort]
	if order == "" {
		order = "a.sort_title COLLATE NOCASE, ar.sort_name COLLATE NOCASE"
	}
	var total int
	if err := c.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM music_albums a WHERE a.missing_since IS NULL AND `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	out, err := c.albums(ctx, pol, where, args, order+", a.id", clampLimit(q.Limit), max(0, q.Offset))
	return out, total, err
}

// ArtistDetail is an artist's page.
func (c *Catalog) Artist(ctx context.Context, pol policy.Policy, id int64) (*ArtistDetail, error) {
	lc, largs := libCond(pol, "ar")
	list, err := c.artists(ctx, `ar.id = ? AND ar.missing_since IS NULL AND `+lc, append([]any{id}, largs...)...)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	d := &ArtistDetail{Artist: list[0], Albums: []Album{}, Live: []Album{}}
	own, err := c.albums(ctx, pol, "a.artist_id = ?", []any{id}, "COALESCE(a.date, '') DESC, a.sort_title COLLATE NOCASE", 0, 0)
	if err != nil {
		return nil, err
	}
	for _, a := range own {
		if a.Live {
			d.Live = append(d.Live, a)
		} else {
			d.Albums = append(d.Albums, a)
		}
	}
	d.AppearsOn, err = c.albums(ctx, pol, `a.artist_id != ? AND EXISTS (SELECT 1 FROM music_tracks t WHERE t.album_id = a.id
		AND t.artist_id = ? AND t.missing_since IS NULL)`, []any{id, id}, "COALESCE(a.date, '') DESC, a.sort_title COLLATE NOCASE", 0, 0)
	return d, err
}

// Album is an album's page, its tracks in disc and track order.
func (c *Catalog) Album(ctx context.Context, pol policy.Policy, id int64) (*AlbumDetail, error) {
	list, err := c.albums(ctx, pol, "a.id = ?", []any{id}, "a.id", 0, 0)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	d := &AlbumDetail{Album: list[0], Checksums: []Checksum{}}
	err = c.DB.QueryRowContext(ctx, `SELECT source_detail, lineage, taper, info_text, musicbrainz_id, musicbrainz_release_group_id,
			replaygain_gain, replaygain_peak FROM music_albums WHERE id = ?`, id).
		Scan(&d.SourceDetail, &d.Lineage, &d.Taper, &d.InfoText, &d.MusicBrainzID, &d.ReleaseGroupMBID,
			&d.ReplayGain.AlbumGain, &d.ReplayGain.AlbumPeak)
	if err != nil {
		return nil, err
	}
	if d.Tracks, err = c.tracks(ctx, pol, "t.album_id = ?", []any{id}, trackOrder); err != nil {
		return nil, err
	}
	rows, err := c.DB.QueryContext(ctx, `SELECT c.file_name, c.algorithm, c.value, c.source, t.id FROM music_checksums c
		JOIN music_albums a ON a.id = c.album_id
		LEFT JOIN music_tracks t ON t.album_id = c.album_id AND t.missing_since IS NULL
			AND substr(t.rel_path, length(a.folder) + CASE WHEN a.folder = '' THEN 1 ELSE 2 END) = c.file_name COLLATE NOCASE
		WHERE c.album_id = ? ORDER BY c.file_name, c.algorithm`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ck Checksum
		if err := rows.Scan(&ck.FileName, &ck.Algorithm, &ck.Value, &ck.Source, &ck.TrackID); err != nil {
			return nil, err
		}
		d.Checksums = append(d.Checksums, ck)
	}
	return d, rows.Err()
}

const trackOrder = "COALESCE(t.disc_number, 0), COALESCE(t.track_number, 0), t.rel_path"

// trackColumns include the checksum files' values for the track, matched by
// its path within the album folder.
const trackColumns = `t.id, t.library_id, t.album_id, t.artist_id, t.title, t.artist_name, a.title, ar.name, t.disc_number,
	t.track_number, t.duration_ms, t.codec, t.container, t.bitrate, t.sample_rate, t.channels, t.bit_depth, t.replaygain_track_gain,
	t.replaygain_track_peak, a.replaygain_gain, a.replaygain_peak, a.cover_source IS NOT NULL,
	(SELECT c.value FROM music_checksums c WHERE c.album_id = a.id AND c.algorithm = 'md5'
		AND c.file_name = substr(t.rel_path, length(a.folder) + CASE WHEN a.folder = '' THEN 1 ELSE 2 END) COLLATE NOCASE),
	(SELECT c.value FROM music_checksums c WHERE c.album_id = a.id AND c.algorithm = 'ffp'
		AND c.file_name = substr(t.rel_path, length(a.folder) + CASE WHEN a.folder = '' THEN 1 ELSE 2 END) COLLATE NOCASE)`

func (c *Catalog) tracks(ctx context.Context, pol policy.Policy, where string, args []any, order string) ([]Track, error) {
	lc, largs := libCond(pol, "t")
	rows, err := c.DB.QueryContext(ctx, `SELECT `+trackColumns+` FROM music_tracks t JOIN music_albums a ON a.id = t.album_id
		JOIN music_artists ar ON ar.id = a.artist_id
		WHERE t.missing_since IS NULL AND `+lc+` AND `+where+` ORDER BY `+order, append(largs, args...)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Track{}
	for rows.Next() {
		var t Track
		if err := rows.Scan(&t.ID, &t.LibraryID, &t.AlbumID, &t.ArtistID, &t.Title, &t.ArtistName, &t.AlbumTitle, &t.AlbumArtist,
			&t.DiscNumber, &t.TrackNumber, &t.DurationMs, &t.Codec, &t.Container, &t.Bitrate, &t.SampleRate, &t.Channels, &t.BitDepth,
			&t.ReplayGain.TrackGain, &t.ReplayGain.TrackPeak, &t.ReplayGain.AlbumGain, &t.ReplayGain.AlbumPeak, &t.HasCover,
			&t.MD5, &t.FFP); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SearchResult is music matching a query.
type SearchResult struct {
	Artists []Artist `json:"artists"`
	Albums  []Album  `json:"albums"`
	Tracks  []Track  `json:"tracks"`
}

// likeArg escapes a search term for LIKE ... ESCAPE '\'.
func likeArg(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

// Search finds artists by name, albums by title, venue, place or date, and
// tracks by title, in every music library the caller may see.
func (c *Catalog) Search(ctx context.Context, pol policy.Policy, q string, limit int) (*SearchResult, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	like := likeArg(q)
	lc, largs := libCond(pol, "ar")
	artists, err := c.artists(ctx, `ar.missing_since IS NULL AND `+lc+` AND ar.name LIKE ? ESCAPE '\'
		AND (EXISTS (SELECT 1 FROM music_albums a WHERE a.artist_id = ar.id AND a.missing_since IS NULL)
			OR EXISTS (SELECT 1 FROM music_tracks t WHERE t.artist_id = ar.id AND t.missing_since IS NULL))
		ORDER BY ar.sort_name COLLATE NOCASE LIMIT ?`, append(largs, like, limit)...)
	if err != nil {
		return nil, err
	}
	albums, err := c.albums(ctx, pol, `(a.title LIKE ? ESCAPE '\' OR a.venue LIKE ? ESCAPE '\' OR a.location LIKE ? ESCAPE '\'
		OR a.date LIKE ? ESCAPE '\')`, []any{like, like, like, like}, "a.sort_title COLLATE NOCASE", limit, 0)
	if err != nil {
		return nil, err
	}
	tracks, err := c.tracks(ctx, pol, `t.title LIKE ? ESCAPE '\' AND a.missing_since IS NULL`, []any{like},
		"t.title COLLATE NOCASE, a.date LIMIT "+strconv.Itoa(limit))
	if err != nil {
		return nil, err
	}
	return &SearchResult{Artists: artists, Albums: albums, Tracks: tracks}, nil
}

// Tracks returns tracks by id in the order given, leaving out any the caller
// may not see or that have gone.
func (c *Catalog) Tracks(ctx context.Context, pol policy.Policy, ids []int64) ([]Track, error) {
	if len(ids) == 0 {
		return []Track{}, nil
	}
	if len(ids) > 1000 {
		ids = ids[:1000]
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	list, err := c.tracks(ctx, pol, "t.id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+")", args, "t.id")
	if err != nil {
		return nil, err
	}
	byID := map[int64]Track{}
	for _, t := range list {
		byID[t.ID] = t
	}
	out := []Track{}
	for _, id := range ids {
		if t, ok := byID[id]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

// AlbumTracks are an album's tracks in order.
func (c *Catalog) AlbumTracks(ctx context.Context, pol policy.Policy, albumID int64) ([]Track, error) {
	return c.tracks(ctx, pol, "t.album_id = ? AND a.missing_since IS NULL", []any{albumID}, trackOrder)
}

// ArtistTracks are every track an artist performs or is the album artist
// of: their albums in date order, then their appearances.
func (c *Catalog) ArtistTracks(ctx context.Context, pol policy.Policy, artistID int64) ([]Track, error) {
	return c.tracks(ctx, pol, "(a.artist_id = ? OR t.artist_id = ?) AND a.missing_since IS NULL", []any{artistID, artistID},
		"a.artist_id != "+strconv.FormatInt(artistID, 10)+", COALESCE(a.date, ''), a.id, "+trackOrder)
}

// File is what serving a track needs: its file, the root it must stay
// under, and how it is encoded.
type File struct {
	TrackID    int64
	LibraryID  int64
	Path, Root string
	Title      string
	Artist     string
	Album      string
	Codec      string
	Container  string
	DurationMs int64
	Bitrate    int64
	SampleRate int64
	BitDepth   int64
}

// File loads a track the caller may play.
func (c *Catalog) File(ctx context.Context, pol policy.Policy, trackID int64) (*File, error) {
	lc, largs := libCond(pol, "t")
	f := &File{TrackID: trackID}
	var codec, container sql.NullString
	var dur, br, sr, bd sql.NullInt64
	err := c.DB.QueryRowContext(ctx, `SELECT t.library_id, t.path, l.root_path, t.title, t.artist_name, a.title, t.codec, t.container,
			t.duration_ms, t.bitrate, t.sample_rate, t.bit_depth
		FROM music_tracks t JOIN music_albums a ON a.id = t.album_id JOIN libraries l ON l.id = t.library_id
		WHERE t.id = ? AND t.missing_since IS NULL AND l.kind = 'music' AND `+lc, append([]any{trackID}, largs...)...).
		Scan(&f.LibraryID, &f.Path, &f.Root, &f.Title, &f.Artist, &f.Album, &codec, &container, &dur, &br, &sr, &bd)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	f.Codec, f.Container, f.DurationMs, f.Bitrate, f.SampleRate, f.BitDepth = codec.String, container.String, dur.Int64, br.Int64, sr.Int64, bd.Int64
	return f, nil
}

// coverSource is where an album's or an artist's picture comes from.
type coverSource struct {
	Root     string
	Path     string // relative to Root
	Embedded bool
}

// AlbumCover finds an album's picture for a caller who may see it.
func (c *Catalog) AlbumCover(ctx context.Context, pol policy.Policy, albumID int64) (*coverSource, error) {
	lc, largs := libCond(pol, "a")
	var src, rel sql.NullString
	var root string
	err := c.DB.QueryRowContext(ctx, `SELECT a.cover_source, a.cover_path, l.root_path FROM music_albums a JOIN libraries l ON l.id = a.library_id
		WHERE a.id = ? AND a.missing_since IS NULL AND `+lc, append([]any{albumID}, largs...)...).Scan(&src, &rel, &root)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !rel.Valid) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &coverSource{Root: root, Path: rel.String, Embedded: src.String == "embedded"}, nil
}

// ArtistImage finds an artist's picture: their own, else their newest
// album's cover.
func (c *Catalog) ArtistImage(ctx context.Context, pol policy.Policy, artistID int64) (*coverSource, error) {
	lc, largs := libCond(pol, "ar")
	var img sql.NullString
	var root string
	err := c.DB.QueryRowContext(ctx, `SELECT ar.image_path, l.root_path FROM music_artists ar JOIN libraries l ON l.id = ar.library_id
		WHERE ar.id = ? AND ar.missing_since IS NULL AND `+lc, append([]any{artistID}, largs...)...).Scan(&img, &root)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if img.Valid {
		return &coverSource{Root: root, Path: img.String}, nil
	}
	var albumID int64
	err = c.DB.QueryRowContext(ctx, `SELECT id FROM music_albums WHERE artist_id = ? AND missing_since IS NULL AND cover_source IS NOT NULL
		ORDER BY COALESCE(date, '') DESC, id DESC LIMIT 1`, artistID).Scan(&albumID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return c.AlbumCover(ctx, pol, albumID)
}

// cleanRel keeps a stored relative path relative.
func cleanRel(p string) string {
	return strings.TrimPrefix(path.Clean("/"+p), "/")
}
