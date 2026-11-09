package api

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/podcasts"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// Podcasts are RSS subscriptions (podcasts.enabled in vcrx.yaml). Nil, or
// off, every podcast route answers 404.
type Podcasts struct {
	Service *podcasts.Service
}

// podcastRoutes are the podcast routes. They answer accounts and household
// profiles only (the accounts access level): an invited device or a linked
// server has no podcasts. Every one reads only what the caller subscribes to.
func (s *Server) podcastRoutes() []route {
	return []route{
		{"GET", "/api/v1/podcasts", accounts, s.podcastList},
		{"POST", "/api/v1/podcasts", accounts, s.podcastSubscribe},
		{"GET", "/api/v1/podcasts/new", accounts, s.podcastNew},
		{"GET", "/api/v1/podcasts/search", accounts, s.podcastSearch},
		{"GET", "/api/v1/podcasts/opml", accounts, s.podcastExport},
		{"POST", "/api/v1/podcasts/opml", accounts, s.podcastImport},
		{"GET", "/api/v1/podcasts/shows/{podcast_id}", accounts, s.podcastShow},
		{"DELETE", "/api/v1/podcasts/shows/{podcast_id}", accounts, s.podcastUnsubscribe},
		{"POST", "/api/v1/podcasts/shows/{podcast_id}/refresh", accounts, s.podcastRefresh},
		{"GET", "/api/v1/podcasts/shows/{podcast_id}/artwork", accounts, s.podcastArtwork},
		{"GET", "/api/v1/podcasts/episodes/{episode_id}", accounts, s.podcastEpisode},
		{"GET", "/api/v1/podcasts/episodes/{episode_id}/progress", accounts, s.podcastProgress},
		{"PUT", "/api/v1/podcasts/episodes/{episode_id}/progress", accounts, s.podcastSetProgress},
		{"PUT", "/api/v1/podcasts/episodes/{episode_id}/played", accounts, s.podcastSetPlayed},
		{"GET", "/api/v1/podcasts/episodes/{episode_id}/artwork", accounts, s.podcastEpisodeArtwork},
		{"GET", "/api/v1/podcasts/episodes/{episode_id}/stream", accounts, s.podcastStream},
		{"HEAD", "/api/v1/podcasts/episodes/{episode_id}/stream", accounts, s.podcastStream},
	}
}

func (s *Server) podcastsOn() bool { return s.Podcasts != nil && s.Podcasts.Service != nil }

// podcastViewer is the caller as the podcast store sees them. A content
// rating ceiling below R (a kids profile) hides everything marked explicit.
func podcastViewer(r *http.Request) podcasts.Viewer {
	ri := info(r)
	pol := ri.Policy
	return podcasts.Viewer{UserID: ri.Session.User.ID, NoExplicit: noExplicit(pol)}
}

func noExplicit(pol policy.Policy) bool {
	if pol.IsAdmin || pol.MaxContentRating == "" {
		return false
	}
	return policy.ContentRatingRank(pol.MaxContentRating) < policy.ContentRatingRank("R")
}

// podcastGate answers 404 while podcasts are off.
func (s *Server) podcastGate(w http.ResponseWriter) bool {
	if !s.podcastsOn() {
		writeError(w, http.StatusNotFound, "podcasts_off", "Podcasts are not turned on on this server.")
		return false
	}
	return true
}

func (s *Server) podcastNotFound(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, podcasts.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	s.internal(w, r, err)
}

func (s *Server) podcastList(w http.ResponseWriter, r *http.Request) {
	if !s.podcastGate(w) {
		return
	}
	list, err := s.Podcasts.Service.Store.Subscriptions(r.Context(), podcastViewer(r))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"podcasts": list})
}

// subscribeError answers a failed subscription.
func (s *Server) subscribeError(w http.ResponseWriter, r *http.Request, err error) {
	var fe *podcasts.FeedError
	switch {
	case podcasts.Refused(err):
		writeError(w, http.StatusBadRequest, "bad_url", "That is not a feed address the server will fetch: give an http or https URL on the public internet.")
	case errors.As(err, &fe):
		writeError(w, http.StatusUnprocessableEntity, "feed_failed", fe.Reason)
	case errors.Is(err, podcasts.ErrExplicit):
		writeError(w, http.StatusForbidden, "explicit", "This show is marked explicit, and this profile does not show explicit podcasts.")
	default:
		s.internal(w, r, err)
	}
}

func (s *Server) podcastSubscribe(w http.ResponseWriter, r *http.Request) {
	if !s.podcastGate(w) {
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	// The fetch finishes even if the client goes away.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
	defer cancel()
	p, created, err := s.Podcasts.Service.Subscribe(ctx, podcastViewer(r), body.URL)
	if err != nil {
		s.subscribeError(w, r, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{"podcast": p})
}

func (s *Server) podcastNew(w http.ResponseWriter, r *http.Request) {
	if !s.podcastGate(w) {
		return
	}
	limit := min(max(queryInt(r, "limit", 50), 1), 200)
	eps, err := s.Podcasts.Service.Store.NewEpisodes(r.Context(), podcastViewer(r), limit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"episodes": eps})
}

func (s *Server) podcastSearch(w http.ResponseWriter, r *http.Request) {
	if !s.podcastGate(w) {
		return
	}
	if !s.Podcasts.Service.Search {
		writeError(w, http.StatusNotFound, "search_off", "Podcast search is off on this server: add a show by its feed URL.")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) > 200 {
		writeError(w, http.StatusBadRequest, "bad_request", "Search for 1 to 200 characters.")
		return
	}
	res, err := s.Podcasts.Service.SearchDirectory(r.Context(), podcastViewer(r), q)
	if err != nil {
		s.Log.Warn("podcasts: search failed", "err", err)
		writeError(w, http.StatusBadGateway, "search_failed", "The podcast directory did not answer. Try again later.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
}

func (s *Server) podcastExport(w http.ResponseWriter, r *http.Request) {
	if !s.podcastGate(w) {
		return
	}
	subs, err := s.Podcasts.Service.Store.OPML(r.Context(), podcastViewer(r))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="vcrx-podcasts.opml"`)
	w.Header().Set("Cache-Control", "no-store")
	_ = podcasts.WriteOPML(w, s.ServerName+" podcasts", subs)
}

func (s *Server) podcastImport(w http.ResponseWriter, r *http.Request) {
	if !s.podcastGate(w) {
		return
	}
	urls, err := podcasts.ParseOPML(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "That is not an OPML file.")
		return
	}
	v := podcastViewer(r)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Minute)
	defer cancel()
	type failure struct {
		URL    string `json:"url"`
		Reason string `json:"reason"`
	}
	added, already, failed := 0, 0, []failure{}
	for _, u := range urls {
		_, created, err := s.Podcasts.Service.Subscribe(ctx, v, u)
		var fe *podcasts.FeedError
		switch {
		case err == nil && created:
			added++
		case err == nil:
			already++
		case podcasts.Refused(err):
			failed = append(failed, failure{u, "The server will not fetch that address."})
		case errors.As(err, &fe):
			failed = append(failed, failure{u, fe.Reason})
		case errors.Is(err, podcasts.ErrExplicit):
			failed = append(failed, failure{u, "Marked explicit."})
		default:
			s.internal(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "already": already, "failed": failed})
}

func (s *Server) podcastShow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "podcast_id")
	if !ok || !s.podcastGate(w) {
		return
	}
	v, st := podcastViewer(r), s.Podcasts.Service.Store
	p, err := st.Show(r.Context(), v, id)
	if err != nil {
		s.podcastNotFound(w, r, err)
		return
	}
	limit := min(max(queryInt(r, "limit", 50), 1), 200)
	offset := max(queryInt(r, "offset", 0), 0)
	eps, total, err := st.Episodes(r.Context(), v, id, limit, offset)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"podcast": p, "episodes": eps, "total": total})
}

func (s *Server) podcastUnsubscribe(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "podcast_id")
	if !ok || !s.podcastGate(w) {
		return
	}
	v := podcastViewer(r)
	// Hidden from a kids profile is the same as not there.
	if _, err := s.Podcasts.Service.Store.Show(r.Context(), v, id); err != nil {
		s.podcastNotFound(w, r, err)
		return
	}
	if err := s.Podcasts.Service.Unsubscribe(r.Context(), v, id); err != nil {
		s.podcastNotFound(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) podcastRefresh(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "podcast_id")
	if !ok || !s.podcastGate(w) {
		return
	}
	v, svc := podcastViewer(r), s.Podcasts.Service
	if _, err := svc.Store.Show(r.Context(), v, id); err != nil {
		s.podcastNotFound(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
	defer cancel()
	// A failed read is the feed's problem: its reason is in last_error.
	_ = svc.Refresh(ctx, id, true)
	p, err := svc.Store.Show(r.Context(), v, id)
	if err != nil {
		s.podcastNotFound(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"podcast": p})
}

func (s *Server) serveArtwork(w http.ResponseWriter, r *http.Request, name string, err error) {
	if err != nil {
		s.podcastNotFound(w, r, err)
		return
	}
	path, ctype, ok := s.Podcasts.Service.ArtworkPath(name)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	f, err := os.Open(path) //nolint:gosec // a name the service wrote, checked by ArtworkPath
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
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, fi.ModTime(), f)
}

func (s *Server) podcastArtwork(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "podcast_id")
	if !ok || !s.podcastGate(w) {
		return
	}
	name, err := s.Podcasts.Service.Store.ShowArtwork(r.Context(), podcastViewer(r), id)
	s.serveArtwork(w, r, name, err)
}

func (s *Server) podcastEpisodeArtwork(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "episode_id")
	if !ok || !s.podcastGate(w) {
		return
	}
	name, err := s.Podcasts.Service.Store.EpisodeArtwork(r.Context(), podcastViewer(r), id)
	s.serveArtwork(w, r, name, err)
}

func (s *Server) podcastEpisode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "episode_id")
	if !ok || !s.podcastGate(w) {
		return
	}
	e, err := s.Podcasts.Service.Store.Episode(r.Context(), podcastViewer(r), id)
	if err != nil {
		s.podcastNotFound(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"episode": e})
}

func (s *Server) podcastProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "episode_id")
	if !ok || !s.podcastGate(w) {
		return
	}
	p, err := s.Podcasts.Service.Store.GetProgress(r.Context(), podcastViewer(r), id)
	if err != nil {
		s.podcastNotFound(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) podcastSetProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "episode_id")
	if !ok || !s.podcastGate(w) {
		return
	}
	var body struct {
		Position *float64 `json:"position_seconds"`
		Duration float64  `json:"duration_seconds"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Position == nil || *body.Position < 0 || *body.Position > 7*24*3600 || body.Duration < 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "position_seconds is a number of seconds from 0.")
		return
	}
	p, err := s.Podcasts.Service.Store.SetProgress(r.Context(), podcastViewer(r), id, *body.Position, body.Duration)
	if err != nil {
		s.podcastNotFound(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) podcastSetPlayed(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "episode_id")
	if !ok || !s.podcastGate(w) {
		return
	}
	var body struct {
		Played *bool `json:"played"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Played == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "played is true or false.")
		return
	}
	p, err := s.Podcasts.Service.Store.SetPlayed(r.Context(), podcastViewer(r), id, *body.Played)
	if err != nil {
		s.podcastNotFound(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// streamTypes are the content types an episode is passed on as; anything
// else from the host goes out as application/octet-stream, so a host cannot
// make the browser run a page from VCRX's origin.
func streamType(host, fallback string) string {
	for _, v := range []string{host, fallback} {
		mt, _, err := mime.ParseMediaType(v)
		if err == nil && (strings.HasPrefix(mt, "audio/") || strings.HasPrefix(mt, "video/")) {
			return mt
		}
	}
	return "application/octet-stream"
}

// podcastStream plays an episode through the server: the host's answer is
// passed on as it arrives, Range and all, so seeking works and the web app's
// media stays same-origin. A full GET is a download.
func (s *Server) podcastStream(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "episode_id")
	if !ok || !s.podcastGate(w) {
		return
	}
	v, svc := podcastViewer(r), s.Podcasts.Service
	loc, err := svc.Store.Enclosure(r.Context(), v, id)
	if err != nil {
		s.podcastNotFound(w, r, err)
		return
	}
	ep, err := svc.Store.Episode(r.Context(), v, id)
	if err != nil {
		s.podcastNotFound(w, r, err)
		return
	}
	resp, err := svc.OpenStream(r.Context(), loc, r.Method, r.Header.Get("Range"))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		if podcasts.Refused(err) {
			s.Log.Warn("podcasts: refused an episode's address", "episode", id)
			writeError(w, http.StatusBadGateway, "stream_refused", "This episode's audio is somewhere the server will not go.")
			return
		}
		s.Log.Info("podcasts: episode host unreachable", "episode", id, "err", err)
		writeError(w, http.StatusBadGateway, "stream_failed", "The podcast's host did not answer.")
		return
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent, http.StatusRequestedRangeNotSatisfiable:
	default:
		s.Log.Info("podcasts: episode host answered an error", "episode", id, "status", resp.StatusCode)
		writeError(w, http.StatusBadGateway, "stream_failed", "The podcast's host answered "+strconv.Itoa(resp.StatusCode)+".")
		return
	}
	h := w.Header()
	for _, k := range []string{"Content-Length", "Content-Range", "Accept-Ranges", "ETag", "Last-Modified"} {
		if v := resp.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	h.Set("Content-Type", streamType(resp.Header.Get("Content-Type"), ep.MimeType))
	h.Set("Cache-Control", "private, no-transform")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, resp.Body)
}
