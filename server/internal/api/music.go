package api

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/music"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
)

// Music is the music library's services. Nil answers every music route with
// 404, as if the library were empty.
type Music struct {
	Catalog *music.Catalog
	Player  *music.Player
	Covers  *music.Covers
}

// musicRoutes are the music library's routes. Every one is signed in and goes
// through the caller's library grants, like video.
func (s *Server) musicRoutes() []route {
	return []route{
		{"GET", "/api/v1/music/libraries/{library_id}/artists", signedIn, s.musicArtists},
		{"GET", "/api/v1/music/libraries/{library_id}/albums", signedIn, s.musicAlbums},
		{"GET", "/api/v1/music/artists/{artist_id}", signedIn, s.musicArtist},
		{"GET", "/api/v1/music/artists/{artist_id}/image", signedIn, s.musicArtistImage},
		{"GET", "/api/v1/music/albums/{album_id}", signedIn, s.musicAlbum},
		{"GET", "/api/v1/music/albums/{album_id}/cover", signedIn, s.musicAlbumCover},
		{"GET", "/api/v1/music/search", signedIn, s.musicSearch},
		{"POST", "/api/v1/music/queue", signedIn, s.musicQueue},
		{"POST", "/api/v1/music/tracks/{track_id}/sessions", signedIn, s.musicListen},
		{"GET", "/api/v1/music/tracks/{track_id}/file", signedIn, s.musicFile},
		{"HEAD", "/api/v1/music/tracks/{track_id}/file", signedIn, s.musicFile},
		{"GET", "/api/v1/music/tracks/{track_id}/stream", signedIn, s.musicStream},
		{"HEAD", "/api/v1/music/tracks/{track_id}/stream", signedIn, s.musicStream},
	}
}

// musicOK writes a 404 when music is off and reports whether it is on.
func (s *Server) musicOK(w http.ResponseWriter) bool {
	if s.Music == nil || s.Music.Catalog == nil {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return false
	}
	return true
}

func (s *Server) musicErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, music.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	s.internal(w, r, err)
}

func (s *Server) musicArtists(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "library_id")
	if !ok || !s.musicOK(w) {
		return
	}
	list, total, err := s.Music.Catalog.Artists(r.Context(), info(r).Policy, id, queryInt(r, "limit", 100), queryInt(r, "offset", 0))
	if err != nil {
		s.musicErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"artists": list, "total": total})
}

func (s *Server) musicAlbums(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "library_id")
	if !ok || !s.musicOK(w) {
		return
	}
	q := music.AlbumQuery{Sort: r.URL.Query().Get("sort"), Limit: queryInt(r, "limit", 100), Offset: queryInt(r, "offset", 0)}
	if v := r.URL.Query().Get("live"); v != "" {
		live, err := strconv.ParseBool(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "live must be true or false.")
			return
		}
		q.Live = &live
	}
	list, total, err := s.Music.Catalog.Albums(r.Context(), info(r).Policy, id, q)
	if err != nil {
		s.musicErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"albums": list, "total": total})
}

func (s *Server) musicArtist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "artist_id")
	if !ok || !s.musicOK(w) {
		return
	}
	d, err := s.Music.Catalog.Artist(r.Context(), info(r).Policy, id)
	if err != nil {
		s.musicErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) musicAlbum(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "album_id")
	if !ok || !s.musicOK(w) {
		return
	}
	d, err := s.Music.Catalog.Album(r.Context(), info(r).Policy, id)
	if err != nil {
		s.musicErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) musicAlbumCover(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "album_id")
	if !ok || !s.musicOK(w) {
		return
	}
	s.serveCover(w, r, func() (string, error) {
		return s.Music.Covers.AlbumCover(r.Context(), info(r).Policy, id, queryInt(r, "width", 0))
	})
}

func (s *Server) musicArtistImage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "artist_id")
	if !ok || !s.musicOK(w) {
		return
	}
	s.serveCover(w, r, func() (string, error) {
		return s.Music.Covers.ArtistImage(r.Context(), info(r).Policy, id, queryInt(r, "width", 0))
	})
}

func (s *Server) serveCover(w http.ResponseWriter, r *http.Request, get func() (string, error)) {
	if s.Music.Covers == nil {
		writeError(w, http.StatusNotFound, "no_artwork", "No artwork.")
		return
	}
	p, err := get()
	if err != nil {
		if !errors.Is(err, music.ErrNotFound) {
			s.Log.Warn("music cover", "path", r.URL.Path, "err", err)
		}
		writeError(w, http.StatusNotFound, "no_artwork", "No artwork.")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	// p is a cache file named from a hash, never from the request.
	http.ServeFile(w, r, p) //nolint:gosec // see above
}

func (s *Server) musicSearch(w http.ResponseWriter, r *http.Request) {
	if !s.musicOK(w) {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Type something to search for.")
		return
	}
	res, err := s.Music.Catalog.Search(r.Context(), info(r).Policy, clip(q, 200), queryInt(r, "limit", 30))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) musicQueue(w http.ResponseWriter, r *http.Request) {
	if !s.musicOK(w) {
		return
	}
	var req music.QueueRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.AlbumID <= 0 && req.ArtistID <= 0 && len(req.TrackIDs) == 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "Name an album_id, an artist_id or track_ids.")
		return
	}
	tracks, err := s.Music.Player.Queue(r.Context(), info(r).Policy, req)
	if err != nil {
		s.musicErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tracks": tracks})
}

// musicListen opens a listen in Activity as a track starts. The client then
// reports start, progress and stop on the playback events route.
func (s *Server) musicListen(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "track_id")
	if !ok || !s.musicOK(w) {
		return
	}
	var req struct {
		Capabilities playback.Capabilities `json:"capabilities"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ri := info(r)
	sess := ri.Session
	c := music.Client{UserID: sess.User.ID, Username: sess.User.Username, AuthSessionID: sess.ID, DeviceID: sess.Device.ID,
		DeviceName: sess.Device.Name, DeviceModel: sess.Device.Model, Platform: sess.Device.Platform, ClientName: sess.Device.Client,
		ClientVersion: sess.Device.ClientVersion, IP: ri.IP}
	out, refusal, err := s.Music.Player.Listen(r.Context(), ri.Policy, c, id, req.Capabilities)
	if err != nil {
		s.musicErr(w, r, err)
		return
	}
	if refusal != nil {
		writeError(w, refusal.Status, refusal.Code, refusal.Message)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// musicTrack loads a track for streaming, refusing a session an admin
// stopped. It writes the error response itself.
func (s *Server) musicTrack(w http.ResponseWriter, r *http.Request) (*music.File, string, bool) {
	id, ok := pathID(w, r, "track_id")
	if !ok || !s.musicOK(w) {
		return nil, "", false
	}
	session := r.URL.Query().Get("session")
	if session != "" && s.Playback != nil {
		if msg, ok := s.Playback.Terminated(r.Context(), session); ok {
			writeError(w, http.StatusGone, "session_terminated", msg)
			return nil, "", false
		}
	}
	f, err := s.Music.Catalog.File(r.Context(), info(r).Policy, id)
	if err != nil {
		s.musicErr(w, r, err)
		return nil, "", false
	}
	return f, session, true
}

// musicFile serves a track for direct play, with Range, If-Range and HEAD from
// http.ServeContent. Access is checked on every request.
func (s *Server) musicFile(w http.ResponseWriter, r *http.Request) {
	f, session, ok := s.musicTrack(w, r)
	if !ok {
		return
	}
	file, fi, err := fsafe.OpenUnder(f.Root, f.Path)
	if err != nil {
		if errors.Is(err, fsafe.ErrOutsideRoot) {
			s.Log.Error("refused file outside library root", "track", f.TrackID, "path", f.Path, "root", f.Root)
		} else {
			s.Log.Warn("music file unavailable", "track", f.TrackID, "err", err)
		}
		writeError(w, http.StatusNotFound, "file_unavailable", "The music file cannot be read right now.")
		return
	}
	defer func() { _ = file.Close() }()
	w.Header().Set("Content-Type", music.MimeFor(f.Container, f.Codec))
	w.Header().Set("Cache-Control", "private, no-transform")
	var body io.ReadSeeker = file
	if session != "" {
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		s.streams.add(session, &cancel)
		defer s.streams.remove(session, &cancel)
		body = ctxReader{ctx: ctx, rs: file}
	}
	http.ServeContent(w, r, filepath.Base(f.Path), fi.ModTime(), body)
}

// musicStream converts a track to AAC or Opus as ffmpeg reads it, from
// start_ms, for a client that cannot decode the file. It follows the video
// rule: an account without allow_transcode cannot have files converted.
func (s *Server) musicStream(w http.ResponseWriter, r *http.Request) {
	f, session, ok := s.musicTrack(w, r)
	if !ok {
		return
	}
	pol := info(r).Policy
	if !pol.IsAdmin && !pol.AllowTranscode {
		writeError(w, http.StatusForbidden, "playback_refused", "Your account is not allowed to have files converted on the server.")
		return
	}
	codec := r.URL.Query().Get("codec")
	if codec == "" {
		codec = remux.AudioAAC
	}
	if (codec != remux.AudioAAC && codec != remux.AudioOpus) || s.Remux == nil || (codec == remux.AudioOpus && !s.Remux.Opus) {
		writeError(w, http.StatusBadRequest, "bad_request", "codec must be aac, or opus when the server has it.")
		return
	}
	abs, err := fsafe.ResolveUnder(f.Root, f.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "file_unavailable", "The music file cannot be read right now.")
		return
	}
	w.Header().Set("Content-Type", remux.AudioMime(codec))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if session != "" {
		s.streams.add(session, &cancel)
		defer s.streams.remove(session, &cancel)
	}
	opts := remux.AudioOptions{Path: abs, Codec: codec, StartMs: max(0, int64(queryInt(r, "start_ms", 0))), Wait: 3 * time.Second}
	err = streamUntilCancelled(ctx, w, func() error { return s.Remux.StreamAudio(ctx, &flushWriter{w: w}, opts) })
	switch {
	case errors.Is(err, remux.ErrBusy):
		writeError(w, http.StatusServiceUnavailable, "busy", "The server is converting as many streams as it allows. Try again shortly.")
	case err != nil:
		s.Log.Warn("converted music failed", "track", f.TrackID, "err", err)
	}
}

// musicListenAllowed is false when the session is the caller's listen of a
// track their policy no longer lets them play; true for anything else,
// which the events route checks its own way.
func (s *Server) musicListenAllowed(ctx context.Context, pol policy.Policy, userID int64, sessionID string) (bool, error) {
	var track sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT track_id FROM playback_sessions WHERE id = ? AND user_id = ?`, sessionID, userID).Scan(&track)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !track.Valid) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if s.Music == nil || s.Music.Catalog == nil {
		return false, nil
	}
	_, err = s.Music.Catalog.File(ctx, pol, track.Int64)
	if errors.Is(err, music.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
