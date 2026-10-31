package api

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
)

// hlsBase is the directory URL of one HLS stream; the playlist names its
// segments relative to it.
func hlsBase(session, run string) string {
	return "/api/v1/playback/sessions/" + session + "/hls/" + run + "/"
}

// hlsPlaylist serves the playlist of a session's current HLS stream, which the
// seek endpoint started. Only the session's owner gets it, and only while the
// session is open, with the same checks as the fragmented MP4 stream.
// hlsAllowed is convertedSession plus the caller's policy, checked on every
// playlist and segment: a run already going keeps writing segments, so a
// policy tightened mid-play must stop it being served here, as it does for
// the fragmented MP4 stream.
func (s *Server) hlsAllowed(w http.ResponseWriter, r *http.Request) bool {
	ss, ok := s.convertedSession(w, r)
	if !ok {
		return false
	}
	allowed, err := s.Catalog.CanPlay(r.Context(), info(r).Policy, ss.VersionID)
	if err != nil {
		s.internal(w, r, err)
		return false
	}
	if !allowed {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
	}
	return allowed
}

func (s *Server) hlsPlaylist(w http.ResponseWriter, r *http.Request) {
	if !s.hlsAllowed(w, r) {
		return
	}
	if s.HLS == nil {
		writeError(w, http.StatusNotFound, "not_found", "This server does not make HLS streams.")
		return
	}
	b, err := s.HLS.Playlist(r.Context(), r.PathValue("session_id"), r.PathValue("run"))
	switch {
	case errors.Is(err, remux.ErrNoStream) || errors.Is(err, remux.ErrBadName):
		writeError(w, http.StatusNotFound, "not_found", "That stream has ended or was replaced by a seek. Ask the seek endpoint for a new one.")
		return
	case errors.Is(err, context.Canceled):
		return
	case err != nil:
		s.Log.Warn("HLS playlist", "session", r.PathValue("session_id"), "err", err)
		writeError(w, http.StatusBadGateway, "stream_failed", "The server could not convert this file.")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	// It grows as segments are written, so it is never cached.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(b)
}

// hlsSegment serves the init segment or a media segment of a session's
// current HLS stream, by a name its playlist uses and no other.
func (s *Server) hlsSegment(w http.ResponseWriter, r *http.Request) {
	if !s.hlsAllowed(w, r) {
		return
	}
	if s.HLS == nil {
		writeError(w, http.StatusNotFound, "not_found", "This server does not make HLS streams.")
		return
	}
	path, err := s.HLS.Segment(r.PathValue("session_id"), r.PathValue("run"), r.PathValue("segment"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No such segment.")
		return
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		// Deleted once the player was well past it, or not written yet.
		writeError(w, http.StatusNotFound, "not_found", "No such segment.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "video/mp4")
	// A segment never changes, and its URL is unique to this stream.
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.Time{}, f)
}
