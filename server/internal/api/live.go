package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/live"
)

// Live is Live TV (milestone 11): the channels from the admin's playlists.
// Nil lists no channels, as if no playlist were set up.
type Live struct {
	Store     *live.Store
	Refresher *live.Refresher
	// LogoDir holds the channel logos the refresher fetched.
	LogoDir string
	// Streams pulls channels for watching; nil turns watching off.
	Streams *live.Streams
}

// liveRoutes are Live TV's routes. A viewer's routes list and show only what
// policy.CanWatchLive allows; stream and playlist URLs are never sent.
func (s *Server) liveRoutes() []route {
	return []route{
		{"GET", "/api/v1/live/channels", signedIn, s.liveChannels},
		{"GET", "/api/v1/live/channels/{channel_id}/logo", signedIn, s.liveLogo},
		{"GET", "/api/v1/live/guide", signedIn, s.liveGuide},
		{"POST", "/api/v1/live/channels/{channel_id}/watch", signedIn, s.liveWatch},
		{"GET", "/api/v1/live/channels/{channel_id}/hls/{run}/index.m3u8", signedIn, s.liveWatchPlaylist},
		{"GET", "/api/v1/live/channels/{channel_id}/hls/{run}/{segment}", signedIn, s.liveWatchSegment},
		{"GET", "/api/v1/admin/live/sources", adminOnly, s.adminLiveSources},
		{"POST", "/api/v1/admin/live/sources/{source_id}/refresh", adminOnly, s.adminLiveRefresh},
		{"GET", "/api/v1/admin/live/channels", adminOnly, s.adminLiveChannels},
		{"PUT", "/api/v1/admin/live/channels/{channel_id}", adminOnly, s.adminLiveUpdate},
	}
}

func (s *Server) liveOn() bool { return s.Live != nil && s.Live.Store != nil }

func (s *Server) liveChannels(w http.ResponseWriter, r *http.Request) {
	if !s.liveOn() {
		writeJSON(w, http.StatusOK, map[string]any{"channels": []live.Channel{}})
		return
	}
	list, err := s.Live.Store.Channels(r.Context(), info(r).Policy.CanWatchLive())
	if err == nil {
		err = s.Live.Store.NowNext(r.Context(), list)
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": list})
}

// liveGuide is the grid: each channel the caller may watch with its
// programmes between from and to (Unix milliseconds; now to three hours on
// by default, at most a day).
func (s *Server) liveGuide(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	from, to := now, now.Add(3*time.Hour)
	if v := r.URL.Query().Get("from"); v != "" {
		ms, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "from is Unix milliseconds.")
			return
		}
		from = time.UnixMilli(ms)
		to = from.Add(3 * time.Hour)
	}
	if v := r.URL.Query().Get("to"); v != "" {
		ms, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "to is Unix milliseconds.")
			return
		}
		to = time.UnixMilli(ms)
	}
	if !to.After(from) || to.Sub(from) > live.MaxGuideWindow {
		writeError(w, http.StatusBadRequest, "bad_request", "to must be after from, and at most a day later.")
		return
	}
	if !s.liveOn() {
		writeJSON(w, http.StatusOK, map[string]any{"from": from.UnixMilli(), "to": to.UnixMilli(), "channels": []live.ChannelGuide{}})
		return
	}
	g, err := s.Live.Store.Guide(r.Context(), info(r).Policy.CanWatchLive(), from, to)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"from": from.UnixMilli(), "to": to.UnixMilli(), "channels": g})
}

// logoTypes are the content types the refresher saves logos as, by extension.
var logoTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif"}

func (s *Server) liveLogo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "channel_id")
	if !ok {
		return
	}
	if !s.liveOn() {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	name, err := s.Live.Store.Logo(r.Context(), id, info(r).Policy.CanWatchLive())
	if errors.Is(err, live.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	ctype, known := logoTypes[filepath.Ext(name)]
	if !known || filepath.Base(name) != name {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	f, err := os.Open(filepath.Join(s.Live.LogoDir, name)) //nolint:gosec // a name the refresher wrote, checked above
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

func (s *Server) adminLiveSources(w http.ResponseWriter, r *http.Request) {
	sources := []live.Source{}
	if s.liveOn() {
		var err error
		if sources, err = s.Live.Store.Sources(r.Context()); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": sources})
}

// adminLiveRefresh reads one playlist again now. It finishes even if the
// admin's browser goes away, and gives up after two minutes.
func (s *Server) adminLiveRefresh(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "source_id")
	if !ok {
		return
	}
	if !s.liveOn() || s.Live.Refresher == nil {
		writeError(w, http.StatusNotFound, "not_found", "No such playlist.")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancel()
	refreshErr := s.Live.Refresher.Refresh(ctx, id)
	if errors.Is(refreshErr, live.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No such playlist.")
		return
	}
	// Then its guide, which matches the channels just read. Its failure is in guide_error.
	_ = s.Live.Refresher.RefreshGuide(ctx, id)
	sources, err := s.Live.Store.Sources(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	for _, src := range sources {
		if src.ID == id {
			// A failed read is the playlist's problem, not the server's: the
			// reason is in last_error and the channels are as they were.
			writeJSON(w, http.StatusOK, src)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "No such playlist.")
}

func (s *Server) adminLiveChannels(w http.ResponseWriter, r *http.Request) {
	list := []live.Channel{}
	if s.liveOn() {
		var err error
		if list, err = s.Live.Store.AllChannels(r.Context()); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": list})
}

func (s *Server) adminLiveUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "channel_id")
	if !ok {
		return
	}
	var u live.ChannelUpdate
	if !readJSON(w, r, &u) {
		return
	}
	if u.Number != nil && len(*u.Number) > 16 {
		writeError(w, http.StatusBadRequest, "bad_request", "A channel number is at most 16 characters.")
		return
	}
	if !s.liveOn() {
		writeError(w, http.StatusNotFound, "not_found", "No such channel.")
		return
	}
	if err := s.Live.Store.Update(r.Context(), id, u); errors.Is(err, live.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No such channel.")
		return
	} else if err != nil {
		s.internal(w, r, err)
		return
	}
	if u.Enabled != nil && !*u.Enabled && s.Live.Streams != nil {
		// Turned off: whoever is watching it stops now.
		s.Live.Streams.Stop(id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// liveTarget is the channel in the path, if the caller may watch it now.
// Every live request asks again, so a grant taken away or a channel turned
// off ends a viewer's stream at their next request.
func (s *Server) liveTarget(w http.ResponseWriter, r *http.Request) (live.WatchTarget, bool) {
	id, ok := pathID(w, r, "channel_id")
	if !ok {
		return live.WatchTarget{}, false
	}
	if !s.liveOn() || s.Live.Streams == nil {
		writeError(w, http.StatusNotFound, "not_found", "No such channel.")
		return live.WatchTarget{}, false
	}
	t, err := s.Live.Store.WatchTarget(r.Context(), id, info(r).Policy.CanWatchLive())
	if errors.Is(err, live.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No such channel.")
		return live.WatchTarget{}, false
	}
	if err != nil {
		s.internal(w, r, err)
		return live.WatchTarget{}, false
	}
	return t, true
}

// liveWatch starts the channel's pull, or joins the one running, and
// answers with the playlist to play.
func (s *Server) liveWatch(w http.ResponseWriter, r *http.Request) {
	t, ok := s.liveTarget(w, r)
	if !ok {
		return
	}
	max := 0
	if s.Live.Refresher != nil {
		max = s.Live.Refresher.SourceMax(t.Source)
	}
	run, err := s.Live.Streams.Watch(r.Context(), live.WatchRequest{Channel: t.Channel, Source: t.Source, SourceMax: max, URL: t.URL, Playlist: t.Playlist})
	switch {
	case errors.Is(err, live.ErrBusy):
		writeError(w, http.StatusConflict, "streams_busy", "Every live stream is in use. Try again when someone stops watching a channel.")
		return
	case errors.Is(err, live.ErrRefusedAddress):
		s.Log.Warn("live: refused a stream address", "channel", t.Channel)
		writeError(w, http.StatusBadGateway, "stream_refused", "This channel's stream points somewhere the server will not go.")
		return
	case err != nil:
		s.Log.Warn("live: could not start a channel", "channel", t.Channel, "err", err)
		writeError(w, http.StatusBadGateway, "stream_failed", "The channel's stream could not be started.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run,
		"playlist": fmt.Sprintf("/api/v1/live/channels/%d/hls/%s/index.m3u8", t.Channel, run)})
}

func (s *Server) liveWatchPlaylist(w http.ResponseWriter, r *http.Request) {
	t, ok := s.liveTarget(w, r)
	if !ok {
		return
	}
	b, err := s.Live.Streams.Playlist(r.Context(), t.Channel, r.PathValue("run"))
	switch {
	case errors.Is(err, live.ErrNoStream):
		writeError(w, http.StatusNotFound, "not_found", "That stream has ended. Ask to watch the channel again.")
		return
	case errors.Is(err, context.Canceled):
		return
	case err != nil:
		s.Log.Warn("live: playlist", "channel", t.Channel, "err", err)
		writeError(w, http.StatusBadGateway, "stream_failed", "The channel's stream is not playing.")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

func (s *Server) liveWatchSegment(w http.ResponseWriter, r *http.Request) {
	t, ok := s.liveTarget(w, r)
	if !ok {
		return
	}
	path, err := s.Live.Streams.Segment(t.Channel, r.PathValue("run"), r.PathValue("segment"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No such segment.")
		return
	}
	f, err := os.Open(path) //nolint:gosec // a name the stream's playlist uses, checked by Segment
	if err != nil {
		// Slid out of the playlist and deleted, or not written yet.
		writeError(w, http.StatusNotFound, "not_found", "No such segment.")
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.Time{}, f)
}
