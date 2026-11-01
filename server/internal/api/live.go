package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
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
}

// liveRoutes are Live TV's routes. A viewer's routes list and show only what
// policy.CanWatchLive allows; stream and playlist URLs are never sent.
func (s *Server) liveRoutes() []route {
	return []route{
		{"GET", "/api/v1/live/channels", signedIn, s.liveChannels},
		{"GET", "/api/v1/live/channels/{channel_id}/logo", signedIn, s.liveLogo},
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
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": list})
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
	w.WriteHeader(http.StatusNoContent)
}
