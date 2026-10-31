package api

import (
	"errors"
	"net/http"

	"github.com/pete-builds/vcrx-media-server/server/internal/library"
)

// regroupError answers a failed merge or split. Every failure left the library
// as it was, so each message only says why.
func (s *Server) regroupError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, library.ErrItemNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
	case errors.Is(err, library.ErrNotMovie), errors.Is(err, library.ErrOtherLibrary), errors.Is(err, library.ErrNothingToMerge):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error()+".")
	case errors.Is(err, library.ErrOnlyVersion):
		writeError(w, http.StatusUnprocessableEntity, "only_version", "This is the movie's only version, so there is nothing to split it from.")
	case errors.Is(err, library.ErrAccessDiffers):
		// The message names the movie and the difference, and holds no path.
		writeError(w, http.StatusUnprocessableEntity, "access_differs", err.Error()+".")
	case errors.Is(err, library.ErrScanRunning):
		writeError(w, http.StatusConflict, "scan_running", "This library is being scanned. Try again when the scan finishes.")
	default:
		s.internal(w, r, err)
	}
}

// adminMergeItems folds other movies into this one: their files become its
// versions, and watch state and history come along.
func (s *Server) adminMergeItems(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok {
		return
	}
	var req struct {
		ItemIDs []int64 `json:"item_ids"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := library.Merge(r.Context(), s.DB, id, req.ItemIDs); err != nil {
		s.regroupError(w, r, err)
		return
	}
	s.Log.Info("movies merged by hand", "into", id, "from", req.ItemIDs, "by", info(r).Session.User.Username)
	writeJSON(w, http.StatusOK, map[string]any{"item_id": id})
}

// adminSplitVersion moves one version out into a movie of its own, for good.
func (s *Server) adminSplitVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "version_id")
	if !ok {
		return
	}
	newID, err := library.Split(r.Context(), s.DB, id)
	if err != nil {
		s.regroupError(w, r, err)
		return
	}
	s.Log.Info("version split off by hand", "version", id, "item", newID, "by", info(r).Session.User.Username)
	writeJSON(w, http.StatusOK, map[string]any{"item_id": newID})
}
