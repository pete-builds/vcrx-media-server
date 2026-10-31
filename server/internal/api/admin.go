package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/pete-builds/vcrx-media-server/server/internal/admin"
	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
)

func (s *Server) adminLibraries(w http.ResponseWriter, r *http.Request) {
	libs, err := s.Admin.Libraries(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraries": libs})
}

func (s *Server) adminCreateLibrary(w http.ResponseWriter, r *http.Request) {
	var in admin.LibraryInput
	if !readJSON(w, r, &in) {
		return
	}
	lib, err := s.Admin.CreateLibrary(r.Context(), in)
	if errors.Is(err, admin.ErrConflict) {
		writeError(w, http.StatusConflict, "conflict", "A library with that name already exists.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, lib)
}

func (s *Server) adminScan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "library_id")
	if !ok {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if _, err := s.Admin.Library(r.Context(), id); err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	runID, err := s.Scans.StartScan(context.WithoutCancel(r.Context()), id, req.Path, "manual")
	if errors.Is(err, admin.ErrScanRunning) {
		writeError(w, http.StatusConflict, "scan_running", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	run, err := s.Admin.Scan(r.Context(), runID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (s *Server) adminScans(w http.ResponseWriter, r *http.Request) {
	scans, err := s.Admin.Scans(r.Context(), int64(queryInt(r, "library_id", 0)), queryInt(r, "limit", 20))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scans": scans})
}

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.Admin.Users(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string  `json:"username"`
		Password    string  `json:"password"`
		DisplayName string  `json:"display_name"`
		IsAdmin     bool    `json:"is_admin"`
		LibraryIDs  []int64 `json:"library_ids"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	u, err := s.Auth.CreateUser(r.Context(), req.Username, req.Password, req.DisplayName, req.IsAdmin)
	if errors.Is(err, auth.ErrUsernameTaken) {
		writeError(w, http.StatusConflict, "conflict", "That username is taken.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if len(req.LibraryIDs) > 0 {
		if err := s.Admin.SetGrants(r.Context(), u.ID, req.LibraryIDs); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
	}
	out, err := s.Admin.User(r.Context(), u.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) adminSetGrants(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "user_id")
	if !ok {
		return
	}
	var req struct {
		LibraryIDs []int64 `json:"library_ids"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Admin.SetGrants(r.Context(), id, req.LibraryIDs); err != nil {
		if errors.Is(err, admin.ErrNotFound) {
			s.notFoundOr(w, r, err)
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminSetPolicy(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "user_id")
	if !ok {
		return
	}
	p := admin.UserPolicy{AllowTranscode: true}
	if !readJSON(w, r, &p) {
		return
	}
	if err := s.Admin.SetPolicy(r.Context(), id, p); err != nil {
		if errors.Is(err, admin.ErrNotFound) {
			s.notFoundOr(w, r, err)
			return
		}
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminLabels lists the labels on titles, for the policy editor's allow and
// deny lists.
func (s *Server) adminLabels(w http.ResponseWriter, r *http.Request) {
	labels, err := s.Admin.Labels(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"labels": labels})
}

func (s *Server) adminSessions(w http.ResponseWriter, r *http.Request) {
	active, _ := strconv.ParseBool(r.URL.Query().Get("active"))
	sessions, err := s.Admin.PlaybackSessions(r.Context(), active, int64(queryInt(r, "user_id", 0)), queryInt(r, "limit", 50), queryInt(r, "offset", 0))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}
