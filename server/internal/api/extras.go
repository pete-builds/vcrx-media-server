package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// canPlaySession is CanPlay for whatever a session plays: its version, or for a
// trailer or other extra (no version), the extra under the same policy.
func (s *Server) canPlaySession(ctx context.Context, pol policy.Policy, ss playback.StreamSession) (bool, error) {
	if ss.ExtraID == 0 {
		return s.Catalog.CanPlay(ctx, pol, ss.VersionID)
	}
	_, err := s.Catalog.ExtraForPlayback(ctx, pol, ss.ExtraID)
	if errors.Is(err, catalog.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// streamExtra serves an extra's file: a trailer for direct play, with the
// session from POST /playback/sessions, or a show's theme music, without one.
// Access is the item's: a user who cannot see the film gets 404 for its
// trailer too. Range, If-Range and HEAD come from http.ServeContent.
func (s *Server) streamExtra(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "extra_id")
	if !ok {
		return
	}
	session := r.URL.Query().Get("session")
	if session != "" {
		if msg, ok := s.Playback.Terminated(r.Context(), session); ok {
			writeError(w, http.StatusGone, "session_terminated", msg)
			return
		}
	}
	x, err := s.Catalog.ExtraForPlayback(r.Context(), info(r).Policy, id)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	f, fi, err := fsafe.OpenUnder(x.Root, x.Version.Path)
	if err != nil {
		if errors.Is(err, fsafe.ErrOutsideRoot) {
			s.Log.Error("refused extra outside library root", "extra", id, "path", x.Version.Path, "root", x.Root)
		} else {
			s.Log.Warn("extra file unavailable", "extra", id, "err", err)
		}
		writeError(w, http.StatusNotFound, "file_unavailable", "The file cannot be read right now.")
		return
	}
	defer func() { _ = f.Close() }()
	if c := x.Version.Container; c != nil && *c == "mp3" {
		w.Header().Set("Content-Type", "audio/mpeg")
	} else if m := playbackMime(c); m != "" {
		w.Header().Set("Content-Type", m)
	}
	w.Header().Set("Cache-Control", "private, no-transform")
	var body io.ReadSeeker = f
	if session != "" {
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		s.streams.add(session, &cancel)
		defer s.streams.remove(session, &cancel)
		body = ctxReader{ctx: ctx, rs: f}
	}
	http.ServeContent(w, r, filepath.Base(x.Version.Path), fi.ModTime(), body)
}
