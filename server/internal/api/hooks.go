package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/pete-builds/vcrx-media-server/server/internal/arr"
)

// arrHook takes a Radarr or Sonarr webhook and scans the folder it names. It
// answers only on the LAN and only with the shared token, given as ?token=,
// an X-VCRX-Token header, or the password of HTTP basic auth (Radarr's and
// Sonarr's Webhook connection has username and password fields).
func (s *Server) arrHook(w http.ResponseWriter, r *http.Request) {
	if s.ArrToken == "" || s.Arr == nil {
		writeError(w, http.StatusNotFound, "hooks_off", "Radarr and Sonarr webhooks are off (set VCRX_ARR_WEBHOOK_TOKEN).")
		return
	}
	if !IsLAN(info(r).IP) {
		writeError(w, http.StatusForbidden, "lan_only", "Webhooks are only accepted from the local network.")
		return
	}
	given := r.URL.Query().Get("token")
	if h := r.Header.Get("X-VCRX-Token"); h != "" {
		given = h
	}
	if _, pw, ok := r.BasicAuth(); ok && pw != "" {
		given = pw
	}
	if subtle.ConstantTimeCompare([]byte(given), []byte(s.ArrToken)) != 1 {
		writeError(w, http.StatusUnauthorized, "bad_token", "Wrong or missing webhook token.")
		return
	}
	// The arrs send many fields this does not read, so unknown fields are fine here.
	var p arr.Payload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", "Not a Radarr or Sonarr webhook body.")
		return
	}
	res, err := s.Arr.Handle(r.Context(), p)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.Log.Info("arr webhook", "event", p.EventType, "action", res.Action, "library", res.LibraryID, "path", res.Path, "reason", res.Reason)
	status := http.StatusAccepted
	if res.Action == "test" || res.Action == "ignored" {
		status = http.StatusOK
	}
	writeJSON(w, status, res)
}
