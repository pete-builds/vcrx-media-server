package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/markers"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// Skip Intro and Skip Credits. Markers ride along with the item details and
// the playback session, which already apply the user's policy, so they are
// never readable for an item the user cannot see. Changing them is for
// admins only.

// MarkerJob is the background detection, nil when detect_markers is off.
type MarkerJob interface {
	Rerun(ctx context.Context, itemID int64) (int, error)
	ApplyToSeason(ctx context.Context, episodeID int64, kind string) error
}

// itemWithMarkers is the item details plus the item's markers.
type itemWithMarkers struct {
	catalog.ItemDetail
	Markers []markers.Marker `json:"markers"`
}

// sessionWithMarkers is a started session plus the markers and the user's
// choice for them, so a player needs no second request.
type sessionWithMarkers struct {
	*playback.Session
	Markers     []markers.PlayerMarker `json:"markers"`
	SkipMarkers string                 `json:"skip_markers"`
}

func (s *Server) withMarkers(ctx context.Context, d catalog.ItemDetail) (itemWithMarkers, error) {
	ms, err := markers.ForItem(ctx, s.DB, d.ID)
	return itemWithMarkers{ItemDetail: d, Markers: ms}, err
}

// sessionMarkers adds the markers to a started session, only when the user
// may see the item itself: on main, starting a session checks the version's
// policy but not the item's grants and labels (#35 closes that), and markers
// must not leak through it in the meantime.
func (s *Server) sessionMarkers(ctx context.Context, pol policy.Policy, userID int64, out *playback.Session) (sessionWithMarkers, error) {
	mode, err := markers.SkipMode(ctx, s.DB, userID)
	if err != nil {
		return sessionWithMarkers{}, err
	}
	ms := []markers.Marker{}
	seen, err := s.Catalog.Summaries(ctx, pol, userID, []int64{out.ItemID})
	if err != nil {
		return sessionWithMarkers{}, err
	}
	if len(seen) == 1 {
		if ms, err = markers.ForItem(ctx, s.DB, out.ItemID); err != nil {
			return sessionWithMarkers{}, err
		}
	}
	return sessionWithMarkers{Session: out, Markers: markers.ForPlayer(ms, mode), SkipMarkers: mode}, nil
}

type playbackSettings struct {
	SkipMarkers string `json:"skip_markers"`
}

func (s *Server) playbackSettings(w http.ResponseWriter, r *http.Request) {
	mode, err := markers.SkipMode(r.Context(), s.DB, info(r).Session.User.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, playbackSettings{SkipMarkers: mode})
}

func (s *Server) setPlaybackSettings(w http.ResponseWriter, r *http.Request) {
	var req playbackSettings
	if !readJSON(w, r, &req) {
		return
	}
	if !markers.ValidMode(req.SkipMarkers) {
		writeError(w, http.StatusBadRequest, "bad_request", "skip_markers must be button, auto or off.")
		return
	}
	if err := markers.SetSkipMode(r.Context(), s.DB, info(r).Session.User.ID, req.SkipMarkers); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

// markerError answers for the marker package's refusals.
func (s *Server) markerError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, markers.ErrNotPlayable), errors.Is(err, markers.ErrNoSeasons):
		writeError(w, http.StatusNotFound, "not_found", "No episode or movie with that id.")
	case errors.Is(err, markers.ErrBadMarker):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case errors.Is(err, markers.ErrQueueFull):
		writeError(w, http.StatusServiceUnavailable, "busy", "Marker detection has too much queued; try again in a few minutes.")
	default:
		s.internal(w, r, err)
	}
}

func (s *Server) itemMarkers(w http.ResponseWriter, r *http.Request, id int64) {
	ms, err := markers.ForItem(r.Context(), s.DB, id)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"markers": ms})
}

func (s *Server) adminSetMarker(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok {
		return
	}
	var req struct {
		StartMs *int64 `json:"start_ms"`
		EndMs   *int64 `json:"end_ms"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.StartMs == nil || req.EndMs == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "start_ms and end_ms are required.")
		return
	}
	if err := markers.SetManual(r.Context(), s.DB, id, r.PathValue("kind"), *req.StartMs, *req.EndMs); err != nil {
		s.markerError(w, r, err)
		return
	}
	s.itemMarkers(w, r, id)
}

func (s *Server) adminClearMarker(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok {
		return
	}
	if err := markers.Clear(r.Context(), s.DB, id, r.PathValue("kind")); err != nil {
		s.markerError(w, r, err)
		return
	}
	s.itemMarkers(w, r, id)
}

func (s *Server) markersOff(w http.ResponseWriter) bool {
	if s.Markers != nil {
		return false
	}
	writeError(w, http.StatusConflict, "markers_off", "Marker detection is off (detect_markers: false).")
	return true
}

func (s *Server) adminDetectMarkers(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok || s.markersOff(w) {
		return
	}
	n, err := s.Markers.Rerun(r.Context(), id)
	if err != nil {
		s.markerError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"seasons": n})
}

func (s *Server) adminApplyMarkerToSeason(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok || s.markersOff(w) {
		return
	}
	if err := s.Markers.ApplyToSeason(r.Context(), id, r.PathValue("kind")); err != nil {
		s.markerError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) adminMarkerStatus(w http.ResponseWriter, r *http.Request) {
	st, err := markers.Progress(r.Context(), s.DB)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := struct {
		Enabled  bool   `json:"enabled"`
		Hours    string `json:"hours"`
		InWindow bool   `json:"in_window"`
		markers.Status
	}{Enabled: s.Markers != nil, Hours: s.MarkerHours.String(), InWindow: s.Markers != nil && s.MarkerHours.Contains(time.Now()), Status: st}
	writeJSON(w, http.StatusOK, out)
}
