package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
)

// convertedSource loads a converted-stream session the caller owns and
// resolves its file. It writes the error response itself and returns ok=false.
func (s *Server) convertedSource(w http.ResponseWriter, r *http.Request) (playback.StreamSession, catalog.Version, string, bool) {
	ri := info(r)
	id := r.PathValue("session_id")
	ss, err := s.Playback.ForStream(r.Context(), ri.Session.User.ID, id)
	if errors.Is(err, playback.ErrSessionNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Unknown playback session.")
		return ss, catalog.Version{}, "", false
	}
	if err != nil {
		s.internal(w, r, err)
		return ss, catalog.Version{}, "", false
	}
	if ss.Terminated != nil {
		writeError(w, http.StatusGone, "session_terminated", *ss.Terminated)
		return ss, catalog.Version{}, "", false
	}
	if ss.Ended {
		writeError(w, http.StatusConflict, "session_ended", "That playback session has already ended. Start a new one.")
		return ss, catalog.Version{}, "", false
	}
	if (ss.Decision != playback.Remux && ss.Decision != playback.AudioTranscode && ss.Decision != playback.Transcode) || s.Remux == nil {
		writeError(w, http.StatusNotFound, "not_found", "This session plays the file directly; use its file URL.")
		return ss, catalog.Version{}, "", false
	}
	v, root, err := s.Catalog.VersionForPlayback(r.Context(), ri.Policy, ss.VersionID)
	if err != nil {
		s.notFoundOr(w, r, err)
		return ss, v, "", false
	}
	path, err := fsafe.ResolveUnder(root, v.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "file_unavailable", "The media file cannot be read right now.")
		return ss, v, "", false
	}
	return ss, v, path, true
}

// convertedSeek answers where a stream asked for at position_ms starts: the
// keyframe at or before it when the video is copied, since copied video can
// only start on one, or position_ms itself when the video is transcoded.
func (s *Server) convertedSeek(w http.ResponseWriter, r *http.Request) {
	ss, _, path, ok := s.convertedSource(w, r)
	if !ok {
		return
	}
	pos := max(0, int64(queryInt(r, "position_ms", 0)))
	start := pos
	if ss.Decision != playback.Transcode {
		var err error
		start, err = s.Remux.KeyframeAt(r.Context(), path, pos)
		if err != nil {
			s.Log.Warn("keyframe lookup failed; starting from the beginning", "session", r.PathValue("session_id"), "err", err)
			start = 0
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"start_ms": start,
		"url":      "/api/v1/playback/sessions/" + r.PathValue("session_id") + "/stream.mp4?start_ms=" + strconv.FormatInt(start, 10),
	})
}

// convertedStream writes the file as fragmented MP4 from start_ms, which
// should be a value the seek endpoint returned.
func (s *Server) convertedStream(w http.ResponseWriter, r *http.Request) {
	ss, v, path, ok := s.convertedSource(w, r)
	if !ok {
		return
	}
	audio, transcode := playback.ChooseAudio(v, ss.Caps)
	if q := r.URL.Query().Get("audio"); q != "" {
		// A specific audio track by stream index, converted unless it can be copied.
		n, err := strconv.Atoi(q)
		found := false
		for _, t := range v.AudioTracks {
			if err == nil && t.Index != nil && int(*t.Index) == n {
				found = true
				transcode = !remux.MP4Audio(deref(t.Codec)) || !decodes(ss.Caps, deref(t.Codec))
			}
		}
		if !found {
			writeError(w, http.StatusBadRequest, "bad_request", "No such audio track.")
			return
		}
		audio = n
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		return
	}
	id := r.PathValue("session_id")
	opts := remux.Options{Path: path, StartMs: max(0, int64(queryInt(r, "start_ms", 0))), AudioIndex: audio, TranscodeAudio: transcode,
		HEVC: deref(v.VideoCodec) == "hevc"}
	if ss.Decision == playback.Transcode {
		video := playback.TranscodeVideo(v, ss.Caps)
		opts.Video, opts.HEVC = &video, false
		// A seek asks for the next stream as the player drops the last one,
		// whose transcode slot frees a moment later.
		opts.Wait = 3 * time.Second
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	s.streams.add(id, &cancel)
	defer s.streams.remove(id, &cancel)
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	err := s.Remux.Stream(ctx, &flushWriter{w: w}, opts)
	switch {
	case errors.Is(err, remux.ErrBusy):
		writeError(w, http.StatusServiceUnavailable, "busy", "The server is converting as many streams as it allows. Try again shortly.")
	case err != nil:
		s.Log.Warn("converted stream failed", "session", id, "version", v.ID, "err", err)
	}
}

func decodes(c playback.Capabilities, codec string) bool {
	for _, a := range c.AudioCodecs {
		if a.Codec == codec {
			return true
		}
	}
	return false
}

// flushWriter pushes each fragment to the client as ffmpeg writes it, so
// playback starts in a second rather than after a buffer fills.
type flushWriter struct {
	w     http.ResponseWriter
	wrote bool
}

func (f *flushWriter) Write(p []byte) (int, error) {
	if !f.wrote {
		f.wrote = true
		f.w.WriteHeader(http.StatusOK)
	}
	n, err := f.w.Write(p)
	if err == nil {
		_ = http.NewResponseController(f.w).Flush()
	}
	return n, err
}
