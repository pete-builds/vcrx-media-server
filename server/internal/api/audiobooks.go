package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/pete-builds/vcrx-media-server/server/internal/audiobook"
	"github.com/pete-builds/vcrx-media-server/server/internal/music"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// Audiobooks is the audiobook libraries' services. Nil answers every
// audiobook route with 404, as if there were no books.
type Audiobooks struct {
	Catalog *audiobook.Catalog
	Player  *audiobook.Player
	// Covers renders book covers; it is music's renderer, which reads both
	// a picture file and one embedded in an audio file.
	Covers *music.Covers
}

// audiobookRoutes are the audiobook libraries' routes. Every one is signed
// in and goes through the caller's library grants, like music.
func (s *Server) audiobookRoutes() []route {
	return []route{
		{"GET", "/api/v1/audiobooks/libraries/{library_id}/books", signedIn, s.audiobookList},
		{"GET", "/api/v1/audiobooks/continue", signedIn, s.audiobookContinue},
		{"GET", "/api/v1/audiobooks/books/{book_id}", signedIn, s.audiobookBook},
		{"GET", "/api/v1/audiobooks/books/{book_id}/cover", signedIn, s.audiobookCover},
		{"GET", "/api/v1/audiobooks/books/{book_id}/progress", signedIn, s.audiobookProgress},
		{"PUT", "/api/v1/audiobooks/books/{book_id}/progress", signedIn, s.audiobookSetProgress},
		{"POST", "/api/v1/audiobooks/books/{book_id}/finished", signedIn, s.audiobookFinished},
		{"DELETE", "/api/v1/audiobooks/books/{book_id}/finished", signedIn, s.audiobookFinished},
		{"POST", "/api/v1/audiobooks/books/{book_id}/queue", signedIn, s.audiobookQueue},
		{"POST", "/api/v1/audiobooks/files/{file_id}/sessions", signedIn, s.audiobookListen},
		{"GET", "/api/v1/audiobooks/files/{file_id}/file", signedIn, s.audiobookFile},
		{"HEAD", "/api/v1/audiobooks/files/{file_id}/file", signedIn, s.audiobookFile},
		{"GET", "/api/v1/audiobooks/files/{file_id}/stream", signedIn, s.audiobookStream},
		{"HEAD", "/api/v1/audiobooks/files/{file_id}/stream", signedIn, s.audiobookStream},
	}
}

// audiobooksOK writes a 404 when audiobooks are off and reports whether they
// are on.
func (s *Server) audiobooksOK(w http.ResponseWriter) bool {
	if s.Audiobooks == nil || s.Audiobooks.Catalog == nil {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return false
	}
	return true
}

func (s *Server) audiobookErr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, audiobook.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	s.internal(w, r, err)
}

func (s *Server) audiobookList(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "library_id")
	if !ok || !s.audiobooksOK(w) {
		return
	}
	qs := r.URL.Query()
	state := qs.Get("state")
	if state != "" && state != "in_progress" && state != "finished" && state != "unplayed" {
		writeError(w, http.StatusBadRequest, "bad_request", "state must be in_progress, finished or unplayed.")
		return
	}
	q := audiobook.BookQuery{Sort: qs.Get("sort"), Author: clip(strings.TrimSpace(qs.Get("author")), 300),
		Series: clip(strings.TrimSpace(qs.Get("series")), 300), State: state, Limit: queryInt(r, "limit", 100), Offset: queryInt(r, "offset", 0)}
	ri := info(r)
	list, err := s.Audiobooks.Catalog.Books(r.Context(), ri.Policy, ri.Session.User.ID, id, q)
	if err != nil {
		s.audiobookErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// audiobookContinue is "Continue listening": the caller's books in progress.
func (s *Server) audiobookContinue(w http.ResponseWriter, r *http.Request) {
	if !s.audiobooksOK(w) {
		return
	}
	ri := info(r)
	list, err := s.Audiobooks.Catalog.Continue(r.Context(), ri.Policy, ri.Session.User.ID, queryInt(r, "limit", 20))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"books": list})
}

func (s *Server) audiobookBook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "book_id")
	if !ok || !s.audiobooksOK(w) {
		return
	}
	ri := info(r)
	d, err := s.Audiobooks.Catalog.Book(r.Context(), ri.Policy, ri.Session.User.ID, id)
	if err != nil {
		s.audiobookErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) audiobookCover(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "book_id")
	if !ok || !s.audiobooksOK(w) {
		return
	}
	if s.Audiobooks.Covers == nil {
		writeError(w, http.StatusNotFound, "no_artwork", "No artwork.")
		return
	}
	root, rel, err := s.Audiobooks.Catalog.CoverSource(r.Context(), info(r).Policy, id)
	var p string
	if err == nil {
		p, err = s.Audiobooks.Covers.Render(r.Context(), root, rel, queryInt(r, "width", 0))
	}
	if err != nil {
		if !errors.Is(err, audiobook.ErrNotFound) && !errors.Is(err, music.ErrNotFound) {
			s.Log.Warn("audiobook cover", "book", id, "err", err)
		}
		writeError(w, http.StatusNotFound, "no_artwork", "No artwork.")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	// p is a cache file named from a hash, never from the request.
	http.ServeFile(w, r, p) //nolint:gosec // see above
}

func (s *Server) audiobookProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "book_id")
	if !ok || !s.audiobooksOK(w) {
		return
	}
	ri := info(r)
	p, err := s.Audiobooks.Catalog.Progress(r.Context(), ri.Policy, ri.Session.User.ID, id)
	if err != nil {
		s.audiobookErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// audiobookSetProgress records where the caller is in a book, in seconds
// from its start, as a player saves it every few seconds and on pause.
func (s *Server) audiobookSetProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "book_id")
	if !ok || !s.audiobooksOK(w) {
		return
	}
	var req struct {
		PositionS *float64 `json:"position_s"`
		Finished  *bool    `json:"finished"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.PositionS == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "position_s is required.")
		return
	}
	ri := info(r)
	p, err := s.Audiobooks.Catalog.SetProgress(r.Context(), ri.Policy, ri.Session.User.ID, id, *req.PositionS, req.Finished)
	if err != nil {
		s.audiobookErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// audiobookFinished marks a book finished (POST) or not (DELETE).
func (s *Server) audiobookFinished(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "book_id")
	if !ok || !s.audiobooksOK(w) {
		return
	}
	ri := info(r)
	p, err := s.Audiobooks.Catalog.SetFinished(r.Context(), ri.Policy, ri.Session.User.ID, id, r.Method == http.MethodPost)
	if err != nil {
		s.audiobookErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// audiobookQueue is everything a player needs to play a book on this
// client: its files with streams or refusals, its chapters, and the caller's
// place in it.
func (s *Server) audiobookQueue(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "book_id")
	if !ok || !s.audiobooksOK(w) {
		return
	}
	var req struct {
		Capabilities playback.Capabilities `json:"capabilities"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ri := info(r)
	q, err := s.Audiobooks.Player.Queue(r.Context(), ri.Policy, ri.Session.User.ID, id, req.Capabilities)
	if err != nil {
		s.audiobookErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, q)
}

// audiobookListen opens a listen in Activity as one of a book's files
// starts. The client then reports start, progress and stop on the playback
// events route.
func (s *Server) audiobookListen(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "file_id")
	if !ok || !s.audiobooksOK(w) {
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
	c := audiobook.Client{UserID: sess.User.ID, Username: sess.User.Username, AuthSessionID: sess.ID, DeviceID: sess.Device.ID,
		DeviceName: sess.Device.Name, DeviceModel: sess.Device.Model, Platform: sess.Device.Platform, ClientName: sess.Device.Client,
		ClientVersion: sess.Device.ClientVersion, IP: ri.IP}
	out, refusal, err := s.Audiobooks.Player.Listen(r.Context(), ri.Policy, c, id, req.Capabilities)
	if err != nil {
		s.audiobookErr(w, r, err)
		return
	}
	if refusal != nil {
		writeError(w, refusal.Status, refusal.Code, refusal.Message)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

// audiobookSource loads a file for streaming, refusing a session an admin
// stopped. It writes the error response itself.
func (s *Server) audiobookSource(w http.ResponseWriter, r *http.Request) (*audiobook.FileSource, string, bool) {
	id, ok := pathID(w, r, "file_id")
	if !ok || !s.audiobooksOK(w) {
		return nil, "", false
	}
	session := r.URL.Query().Get("session")
	if session != "" && s.Playback != nil {
		if msg, ok := s.Playback.Terminated(r.Context(), session); ok {
			writeError(w, http.StatusGone, "session_terminated", msg)
			return nil, "", false
		}
	}
	// An invited device plays a book's file only inside its own listen to
	// that file, as for music.
	if !s.streamOnly(w, r, session, func(ss playback.StreamSession) bool { return ss.BookFileID == id }) {
		return nil, "", false
	}
	f, err := s.Audiobooks.Catalog.Source(r.Context(), info(r).Policy, id)
	if err != nil {
		s.audiobookErr(w, r, err)
		return nil, "", false
	}
	return f, session, true
}

const bookUnavailable = "The audiobook file cannot be read right now."

// audiobookFile serves a book's file for direct play, with Range, If-Range
// and HEAD. Access is checked on every request.
func (s *Server) audiobookFile(w http.ResponseWriter, r *http.Request) {
	f, session, ok := s.audiobookSource(w, r)
	if !ok {
		return
	}
	s.serveAudioFile(w, r, f.Root, f.Path, music.MimeFor(f.Container, f.Codec), session, "book_file", f.FileID, bookUnavailable)
}

// audiobookStream converts a book's file to AAC or Opus from start_ms, for a
// client that cannot decode it, under the allow_transcode rule.
func (s *Server) audiobookStream(w http.ResponseWriter, r *http.Request) {
	f, session, ok := s.audiobookSource(w, r)
	if !ok {
		return
	}
	s.streamAudioFile(w, r, f.Root, f.Path, session, "book_file", f.FileID, bookUnavailable)
}

// audiobookListenAllowed is false when the session is the caller's listen to
// a book file their policy no longer lets them play; true for anything else,
// which the events route checks its own way.
func (s *Server) audiobookListenAllowed(ctx context.Context, pol policy.Policy, userID int64, sessionID string) (bool, error) {
	var file sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT book_file_id FROM playback_sessions WHERE id = ? AND user_id = ?`, sessionID, userID).Scan(&file)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !file.Valid) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if s.Audiobooks == nil || s.Audiobooks.Catalog == nil {
		return false, nil
	}
	_, err = s.Audiobooks.Catalog.Source(ctx, pol, file.Int64)
	if errors.Is(err, audiobook.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}
