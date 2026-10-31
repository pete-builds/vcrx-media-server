package api

import (
	"errors"
	"net/http"

	"github.com/pete-builds/vcrx-media-server/server/internal/newsletter"
	"github.com/pete-builds/vcrx-media-server/server/internal/notify"
)

func (s *Server) newsletterStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.Newsletter.Status(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) newsletterSave(w http.ResponseWriter, r *http.Request) {
	var in newsletter.Settings
	if !readJSON(w, r, &in) {
		return
	}
	if err := in.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := s.Newsletter.Save(r.Context(), in); errors.Is(err, newsletter.ErrBadAudience) {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	} else if err != nil {
		s.internal(w, r, err)
		return
	}
	s.newsletterStatus(w, r)
}

// newsletterPreview sends the last seven days' digest now, so the admin can
// see it in Discord before switching the weekly one on.
func (s *Server) newsletterPreview(w http.ResponseWriter, r *http.Request) {
	if !s.Webhooks.Wants(notify.Newsletter) {
		writeError(w, http.StatusConflict, "no_webhooks",
			"No webhook takes the newsletter. Add one with VCRX_WEBHOOK_URLS, and if webhook_events is set, include newsletter.")
		return
	}
	d, sent, err := s.Newsletter.Preview(r.Context())
	switch {
	case errors.Is(err, newsletter.ErrBadAudience):
		writeError(w, http.StatusConflict, "bad_audience", err.Error())
		return
	case errors.Is(err, newsletter.ErrNothingAdded):
		writeError(w, http.StatusConflict, "nothing_added", "Nothing was added in the last 7 days, so there is nothing to send.")
		return
	case sent == 0 && err != nil:
		s.Log.Warn("newsletter preview failed", "err", err)
		writeError(w, http.StatusBadGateway, "webhook_failed", "No webhook accepted the preview: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent, "failed": len(s.Webhooks.URLs) - sent,
		"movies": len(d.Movies), "shows": len(d.Shows), "episodes": d.Episodes()})
}
