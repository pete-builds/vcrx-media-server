package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/pete-builds/vcrx-media-server/server/internal/enrich"
)

func (s *Server) metadataOff(w http.ResponseWriter) bool {
	if s.Metadata == nil {
		writeError(w, http.StatusNotFound, "tmdb_off", "TMDb is not set up on this server (VCRX_TMDB_API_KEY).")
		return true
	}
	return false
}

// unmatched lists movies and shows TMDb matching left alone, with the reason.
func (s *Server) unmatched(w http.ResponseWriter, r *http.Request) {
	if s.metadataOff(w) {
		return
	}
	st, err := s.Metadata.Status(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	type row struct {
		ID     int64  `json:"id"`
		Kind   string `json:"kind"`
		Title  string `json:"title"`
		Year   int    `json:"year"`
		Reason string `json:"reason"`
	}
	out := []row{}
	for _, u := range st.Unmatched {
		out = append(out, row{u.ID, u.Kind, u.Title, u.Year, u.Reason})
	}
	writeJSON(w, http.StatusOK, map[string]any{"unmatched": out})
}

func (s *Server) matchSearch(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok || s.metadataOff(w) {
		return
	}
	year, _ := strconv.Atoi(r.URL.Query().Get("year"))
	choices, err := s.Metadata.Search(r.Context(), id, r.URL.Query().Get("query"), year)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
	case errors.Is(err, enrich.ErrNotMatchable):
		writeError(w, http.StatusBadRequest, "not_matchable", err.Error())
	case err != nil:
		s.Log.Warn("tmdb search failed", "item", id, "err", err)
		writeError(w, http.StatusBadGateway, "tmdb_failed", "TMDb did not answer the search.")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"choices": choices})
	}
}

func (s *Server) matchSet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok || s.metadataOff(w) {
		return
	}
	var req struct {
		TMDbID int64 `json:"tmdb_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.TMDbID <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "tmdb_id is required.")
		return
	}
	st, err := s.Metadata.Fix(r.Context(), id, req.TMDbID)
	switch {
	case errors.Is(err, enrich.ErrNotMatchable):
		writeError(w, http.StatusBadRequest, "not_matchable", err.Error())
	case err != nil:
		s.Log.Warn("fix match failed", "item", id, "tmdb", req.TMDbID, "err", err)
		writeError(w, http.StatusBadGateway, "tmdb_failed", "TMDb did not answer for that id.")
	case st.Matched == 0:
		writeError(w, http.StatusUnprocessableEntity, "no_match", "TMDb has no title with that id.")
	default:
		s.Log.Info("match fixed by hand", "item", id, "tmdb", req.TMDbID, "by", info(r).Session.User.Username)
		writeJSON(w, http.StatusOK, map[string]any{"matched": true, "episodes": st.Episodes})
	}
}
