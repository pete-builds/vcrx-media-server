package api

import (
	"net/http"

	"github.com/pete-builds/vcrx-media-server/server/internal/collections"
)

func (s *Server) libraryCollections(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "library_id")
	if !ok {
		return
	}
	cols, err := s.Catalog.LibraryCollections(r.Context(), info(r).Policy, id)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"collections": cols})
}

func (s *Server) libraryFilters(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "library_id")
	if !ok {
		return
	}
	f, err := s.Catalog.LibraryFilters(r.Context(), info(r).Policy, id)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) collection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "collection_id")
	if !ok {
		return
	}
	ri := info(r)
	col, items, err := s.Catalog.CollectionItems(r.Context(), ri.Policy, ri.Session.User.ID, id)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"collection": col, "items": items})
}

// adminSmartCollections lists the smart collection definitions loaded from
// YAML and how the last build of each went, including files that were refused.
func (s *Server) adminSmartCollections(w http.ResponseWriter, r *http.Request) {
	var rep *collections.Report
	if s.SmartCollections != nil {
		rep = s.SmartCollections.Status()
	}
	if rep == nil {
		writeJSON(w, http.StatusOK, map[string]any{"built": false, "files": []any{}, "collections": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Built bool `json:"built"`
		*collections.Report
	}{true, rep})
}
