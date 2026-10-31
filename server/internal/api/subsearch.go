package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/opensubtitles"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
	"github.com/pete-builds/vcrx-media-server/server/internal/subsearch"
	"github.com/pete-builds/vcrx-media-server/server/internal/subtitles"
)

// Subtitle search and download (OpenSubtitles), and the per-user subtitle delay.

func (s *Server) searchEnabled() bool { return s.SubSearch != nil && s.SubSearch.Enabled() }

// mayDownload: admins, and users whose policy allows it, when an account is set.
func (s *Server) mayDownload(p policy.Policy) bool {
	return s.searchEnabled() && s.SubSearch.CanDownload() && (p.IsAdmin || p.AllowSubtitleDownload)
}

// subtitleOffset is the caller's stored delay for a version, 0 when none.
func (s *Server) subtitleOffset(ctx context.Context, userID, versionID int64) int64 {
	var off int64
	_ = s.DB.QueryRowContext(ctx, `SELECT offset_ms FROM subtitle_offsets WHERE user_id = ? AND version_id = ?`,
		userID, versionID).Scan(&off)
	return off
}

// decorateSession adds the subtitle delay and what the subtitle menu may
// offer to a new playback session.
func (s *Server) decorateSession(r *http.Request, out *playback.Session) {
	ri := info(r)
	out.SubtitleOffsetMs = s.subtitleOffset(r.Context(), ri.Session.User.ID, out.Version.ID)
	out.SubtitleSearch = playback.SubtitleSearch{Enabled: s.searchEnabled(), CanDownload: s.mayDownload(ri.Policy)}
}

// visibleVersion answers 404 unless the caller may play the version.
func (s *Server) visibleVersion(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := pathID(w, r, "version_id")
	if !ok {
		return 0, false
	}
	if _, _, err := s.Catalog.VersionForPlayback(r.Context(), info(r).Policy, id); err != nil {
		s.notFoundOr(w, r, err)
		return 0, false
	}
	return id, true
}

func (s *Server) searchSubtitles(w http.ResponseWriter, r *http.Request) {
	id, ok := s.visibleVersion(w, r)
	if !ok {
		return
	}
	if !s.searchEnabled() {
		writeError(w, http.StatusNotFound, "search_off", "Subtitle search is not set up on this server.")
		return
	}
	var langs []string
	for _, l := range strings.Split(r.URL.Query().Get("languages"), ",") {
		if l = strings.TrimSpace(l); l != "" {
			langs = append(langs, l)
		}
	}
	if len(langs) > 5 {
		langs = langs[:5]
	}
	preferHI, _ := strconv.ParseBool(r.URL.Query().Get("hearing_impaired"))
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	hits, err := s.SubSearch.Search(ctx, id, langs, preferHI)
	if err != nil {
		s.providerError(w, r, err)
		return
	}
	out := map[string]any{"results": hits, "can_download": s.mayDownload(info(r).Policy)}
	if info(r).Policy.IsAdmin {
		if q := s.SubSearch.Provider.LastQuota(); q.Known {
			out["quota"] = q
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) downloadSubtitle(w http.ResponseWriter, r *http.Request) {
	id, ok := s.visibleVersion(w, r)
	if !ok {
		return
	}
	var body struct {
		FileID int64 `json:"file_id"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	pol := info(r).Policy
	switch {
	case !s.searchEnabled():
		writeError(w, http.StatusNotFound, "search_off", "Subtitle search is not set up on this server.")
		return
	case !pol.IsAdmin && !pol.AllowSubtitleDownload:
		writeError(w, http.StatusForbidden, "forbidden", "Your account cannot download subtitles. Ask the server's admin.")
		return
	case !s.SubSearch.CanDownload():
		writeError(w, http.StatusForbidden, "download_off", "Downloads need an OpenSubtitles account in the server's settings.")
		return
	case body.FileID <= 0:
		writeError(w, http.StatusBadRequest, "bad_request", "file_id is required.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	d, err := s.SubSearch.Download(ctx, id, body.FileID, info(r).Session.User.ID)
	if err != nil {
		s.providerError(w, r, err)
		return
	}
	sid := strconv.FormatInt(d.StreamID, 10)
	out := map[string]any{
		"subtitle": d,
		// The same shape as a playback session's webvtt_subtitles entry, so a
		// player can add it to its menu and switch to it.
		"webvtt": playback.SubtitleSource{TrackID: d.StreamID, URL: "/api/v1/subtitles/" + sid + "?format=vtt", Codec: "webvtt",
			MimeType: "text/vtt", Language: d.Language, Downloaded: true},
	}
	if pol.IsAdmin {
		if q := s.SubSearch.Provider.LastQuota(); q.Known {
			out["quota"] = q
		}
	}
	status := http.StatusCreated
	if d.Existing {
		status = http.StatusOK
	}
	writeJSON(w, status, out)
}

// providerError turns a search or download failure into a message a person
// can act on. Being offline is a 503, never a 500.
func (s *Server) providerError(w http.ResponseWriter, r *http.Request, err error) {
	var rl *opensubtitles.RateLimitError
	var qe *opensubtitles.QuotaError
	switch {
	case errors.Is(err, subsearch.ErrOff):
		writeError(w, http.StatusNotFound, "search_off", "Subtitle search is not set up on this server.")
	case errors.Is(err, subsearch.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
	case errors.Is(err, subsearch.ErrBadRequest):
		writeError(w, http.StatusBadRequest, "bad_request", "Name languages as codes such as en or eng.")
	case errors.Is(err, subsearch.ErrNotSearched):
		writeError(w, http.StatusConflict, "search_again", "Search again, then pick a subtitle from the new results.")
	case errors.Is(err, opensubtitles.ErrNoAccount):
		writeError(w, http.StatusForbidden, "download_off", "Downloads need an OpenSubtitles account in the server's settings.")
	case errors.As(err, &qe):
		msg := "Today's OpenSubtitles downloads are used up."
		if !qe.Quota.ResetAt.IsZero() {
			msg += " They renew in " + until(qe.Quota.ResetAt) + "."
		}
		writeError(w, http.StatusTooManyRequests, "quota_exhausted", msg)
	case errors.As(err, &rl):
		secs := int(math.Ceil(time.Until(rl.Until).Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(max(1, secs)))
		writeError(w, http.StatusTooManyRequests, "rate_limited", "OpenSubtitles asked us to slow down. Try again in a few seconds.")
	case errors.Is(err, opensubtitles.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		s.Log.Warn("opensubtitles unavailable", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusServiceUnavailable, "provider_unavailable", "OpenSubtitles cannot be reached right now. Try again later.")
	case errors.Is(err, subsearch.ErrBadFile):
		writeError(w, http.StatusBadGateway, "bad_file", "OpenSubtitles sent a file that was refused. Pick another one.")
	case errors.Is(err, opensubtitles.ErrNotConfigured):
		writeError(w, http.StatusNotFound, "search_off", "Subtitle search is not set up on this server.")
	default:
		if strings.HasPrefix(err.Error(), "opensubtitles:") {
			s.Log.Warn("opensubtitles error", "path", r.URL.Path, "err", err)
			writeError(w, http.StatusBadGateway, "provider_error", "OpenSubtitles refused the request. Check the server log.")
			return
		}
		s.internal(w, r, err)
	}
}

func until(t time.Time) string {
	d := time.Until(t).Round(time.Minute)
	if d < time.Minute {
		return "a minute"
	}
	h, m := int(d.Hours()), int(d.Minutes())%60
	switch {
	case h == 0:
		return fmt.Sprintf("%d min", m)
	case m == 0:
		return fmt.Sprintf("%d h", h)
	}
	return fmt.Sprintf("%d h %d min", h, m)
}

func (s *Server) setSubtitleOffset(w http.ResponseWriter, r *http.Request) {
	id, ok := s.visibleVersion(w, r)
	if !ok {
		return
	}
	var body struct {
		OffsetMs *int64 `json:"offset_ms"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.OffsetMs == nil || *body.OffsetMs < -subtitles.MaxOffsetMs || *body.OffsetMs > subtitles.MaxOffsetMs {
		writeError(w, http.StatusBadRequest, "bad_request", "offset_ms must be between -600000 and 600000.")
		return
	}
	user := info(r).Session.User.ID
	var err error
	if *body.OffsetMs == 0 {
		_, err = s.DB.ExecContext(r.Context(), `DELETE FROM subtitle_offsets WHERE user_id = ? AND version_id = ?`, user, id)
	} else {
		_, err = s.DB.ExecContext(r.Context(), `INSERT INTO subtitle_offsets (user_id, version_id, offset_ms, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (user_id, version_id) DO UPDATE SET offset_ms = excluded.offset_ms, updated_at = excluded.updated_at`,
			user, id, *body.OffsetMs, time.Now().UnixMilli())
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"offset_ms": *body.OffsetMs})
}

func (s *Server) adminItemSubtitles(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok {
		return
	}
	out := map[string]any{"enabled": s.searchEnabled(), "can_download": s.searchEnabled() && s.SubSearch.CanDownload(),
		"subtitles": []subsearch.Listed{}, "quota": nil}
	if s.SubSearch != nil {
		list, err := s.SubSearch.ForItem(r.Context(), id)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		out["subtitles"] = list
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if q, ok := s.SubSearch.Quota(ctx); ok {
			out["quota"] = q
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminDeleteSubtitle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "stream_id")
	if !ok {
		return
	}
	if s.SubSearch == nil {
		writeError(w, http.StatusNotFound, "not_found", "Not a downloaded subtitle.")
		return
	}
	if err := s.SubSearch.Delete(r.Context(), id); err != nil {
		if errors.Is(err, subsearch.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Not a downloaded subtitle.")
			return
		}
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
