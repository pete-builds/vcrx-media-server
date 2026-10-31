package api

import (
	"errors"
	"net/http"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
)

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	rows, err := s.Catalog.Home(r.Context(), ri.Policy, ri.Session.User.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

// homeRow is a Home row's See all: the same row with more titles.
func (s *Server) homeRow(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	row, err := s.Catalog.HomeRowItems(r.Context(), ri.Policy, ri.Session.User.ID, r.PathValue("row_id"))
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

type layoutInput struct {
	Rows []catalog.LayoutEntry `json:"rows"`
}

// homeLayout is the caller's Home layout, for the Customize Home editors on
// the web and the TV.
func (s *Server) homeLayout(w http.ResponseWriter, r *http.Request) {
	l, err := s.Catalog.HomeLayout(r.Context(), info(r).Session.User.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) setHomeLayout(w http.ResponseWriter, r *http.Request) {
	s.saveLayout(w, r, info(r).Session.User.ID)
}

func (s *Server) resetHomeLayout(w http.ResponseWriter, r *http.Request) {
	s.resetLayout(w, r, info(r).Session.User.ID)
}

func (s *Server) adminHomeLayout(w http.ResponseWriter, r *http.Request) {
	l, err := s.Catalog.ServerHomeLayout(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) adminSetHomeLayout(w http.ResponseWriter, r *http.Request) {
	s.saveLayout(w, r, 0)
}

func (s *Server) adminResetHomeLayout(w http.ResponseWriter, r *http.Request) {
	s.resetLayout(w, r, 0)
}

// saveLayout stores a layout (userID 0 is the server default) and answers
// with the layout as it now reads back.
func (s *Server) saveLayout(w http.ResponseWriter, r *http.Request, userID int64) {
	var in layoutInput
	if !readJSON(w, r, &in) {
		return
	}
	if err := s.Catalog.SetHomeLayout(r.Context(), userID, in.Rows); err != nil {
		if errors.Is(err, catalog.ErrBadLayout) {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		s.internal(w, r, err)
		return
	}
	s.layoutReply(w, r, userID)
}

func (s *Server) resetLayout(w http.ResponseWriter, r *http.Request, userID int64) {
	if err := s.Catalog.ResetHomeLayout(r.Context(), userID); err != nil {
		s.internal(w, r, err)
		return
	}
	s.layoutReply(w, r, userID)
}

func (s *Server) layoutReply(w http.ResponseWriter, r *http.Request, userID int64) {
	var l catalog.Layout
	var err error
	if userID > 0 {
		l, err = s.Catalog.HomeLayout(r.Context(), userID)
	} else {
		l, err = s.Catalog.ServerHomeLayout(r.Context())
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}
