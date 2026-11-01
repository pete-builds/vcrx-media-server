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

// convertedSession loads a converted-stream session the caller owns that is
// still open. It writes the error response itself and returns ok=false.
func (s *Server) convertedSession(w http.ResponseWriter, r *http.Request) (playback.StreamSession, bool) {
	ss, err := s.Playback.ForStream(r.Context(), info(r).Session.User.ID, r.PathValue("session_id"))
	if errors.Is(err, playback.ErrSessionNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Unknown playback session.")
		return ss, false
	}
	if err != nil {
		s.internal(w, r, err)
		return ss, false
	}
	if ss.Terminated != nil {
		writeError(w, http.StatusGone, "session_terminated", *ss.Terminated)
		return ss, false
	}
	if ss.Ended {
		writeError(w, http.StatusConflict, "session_ended", "That playback session has already ended. Start a new one.")
		return ss, false
	}
	if (ss.Decision != playback.Remux && ss.Decision != playback.AudioTranscode && ss.Decision != playback.Transcode) || s.Remux == nil {
		writeError(w, http.StatusNotFound, "not_found", "This session plays the file directly; use its file URL.")
		return ss, false
	}
	return ss, true
}

// convertedSource is convertedSession plus the session's version and file.
func (s *Server) convertedSource(w http.ResponseWriter, r *http.Request) (playback.StreamSession, catalog.Version, string, bool) {
	ss, ok := s.convertedSession(w, r)
	if !ok {
		return ss, catalog.Version{}, "", false
	}
	var v catalog.Version
	var root string
	var err error
	if ss.ExtraID != 0 {
		var x catalog.ExtraFile
		x, err = s.Catalog.ExtraForPlayback(r.Context(), info(r).Policy, ss.ExtraID)
		v, root = x.Version, x.Root
	} else {
		v, root, err = s.Catalog.VersionForPlayback(r.Context(), info(r).Policy, ss.VersionID)
	}
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
//
// For a client that plays HLS it also starts that stream, replacing the one
// the session had, and answers with its playlist. audio picks the track, as
// on the fragmented MP4 stream.
func (s *Server) convertedSeek(w http.ResponseWriter, r *http.Request) {
	ss, v, path, ok := s.convertedSource(w, r)
	if !ok {
		return
	}
	hls := ss.Caps.HLSStream && s.HLS != nil
	var audio int
	var transcode bool
	if hls {
		if audio, transcode, ok = streamAudio(w, r, v, ss, true); !ok {
			return
		}
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
	id := r.PathValue("session_id")
	if hls {
		run, err := s.HLS.Start(r.Context(), id, convertOptions(ss, v, path, start, audio, transcode))
		switch {
		case errors.Is(err, remux.ErrBusy):
			writeError(w, http.StatusServiceUnavailable, "busy", "The server is converting as many streams as it allows. Try again shortly.")
			return
		case err != nil:
			s.internal(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"start_ms": start, "url": hlsBase(id, run) + remux.HLSPlaylist})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"start_ms": start,
		"url":      "/api/v1/playback/sessions/" + id + "/stream.mp4?start_ms=" + strconv.FormatInt(start, 10),
	})
}

// streamAudio picks the audio for a converted stream: the audio query
// parameter's track if there is one, else what ChooseAudio picks. It writes
// a 400 itself for a track the file does not have. For HLS, audio HLS cannot
// carry is converted even when MP4 could.
func streamAudio(w http.ResponseWriter, r *http.Request, v catalog.Version, ss playback.StreamSession, hls bool) (int, bool, bool) {
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
			return 0, false, false
		}
		audio = n
	}
	if hls && !transcode {
		for _, t := range v.AudioTracks {
			if t.Index != nil && int(*t.Index) == audio && !remux.HLSAudio(deref(t.Codec)) {
				transcode = true
			}
		}
	}
	return audio, transcode, true
}

// convertOptions describe a session's converted stream from startMs.
func convertOptions(ss playback.StreamSession, v catalog.Version, path string, startMs int64, audio int, transcode bool) remux.Options {
	opts := remux.Options{Path: path, StartMs: max(0, startMs), AudioIndex: audio, TranscodeAudio: transcode,
		HEVC: deref(v.VideoCodec) == "hevc"}
	for _, t := range v.AudioTracks {
		if t.Index != nil && int(*t.Index) == audio {
			opts.AudioCodec = deref(t.Codec)
		}
	}
	if ss.Decision == playback.Transcode {
		video := playback.TranscodeVideo(v, ss.Caps)
		opts.Video, opts.HEVC = &video, false
		// A seek asks for the next stream as the player drops the last one,
		// whose transcode slot frees a moment later.
		opts.Wait = 3 * time.Second
	}
	return opts
}

// convertedStream writes the file as fragmented MP4 from start_ms, which
// should be a value the seek endpoint returned.
func (s *Server) convertedStream(w http.ResponseWriter, r *http.Request) {
	ss, v, path, ok := s.convertedSource(w, r)
	if !ok {
		return
	}
	audio, transcode, ok := streamAudio(w, r, v, ss, false)
	if !ok {
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		return
	}
	id := r.PathValue("session_id")
	opts := convertOptions(ss, v, path, int64(queryInt(r, "start_ms", 0)), audio, transcode)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if s.streams.addConverted(id, &cancel) && opts.Wait == 0 {
		// The stream this one replaced frees its slot a moment after it is stopped.
		opts.Wait = 3 * time.Second
	}
	defer s.streams.remove(id, &cancel)
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	err := streamUntilCancelled(ctx, w, func() error { return s.Remux.Stream(ctx, &flushWriter{w: w}, opts) })
	switch {
	case errors.Is(err, remux.ErrBusy):
		writeError(w, http.StatusServiceUnavailable, "busy", "The server is converting as many streams as it allows. Try again shortly.")
	case err != nil:
		s.Log.Warn("converted stream failed", "session", id, "version", v.ID, "err", err)
	}
}

// streamUntilCancelled runs stream and, if ctx is cancelled first (a stop, an
// admin terminate, or the client leaving), fails the response's pending write at
// once. Otherwise a reader that keeps the connection open but barely reads holds
// ffmpeg's output copy, and with it the conversion slot, until it drains the
// buffer. The deadline is set before this returns, never on a connection that
// has moved on to its next request.
func streamUntilCancelled(ctx context.Context, w http.ResponseWriter, stream func() error) error {
	done, cut := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(cut)
		select {
		case <-ctx.Done():
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now())
		case <-done:
		}
	}()
	err := stream()
	close(done)
	<-cut
	return err
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
