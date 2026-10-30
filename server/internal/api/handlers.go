package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/api"
	"github.com/pete-builds/vcrx-media-server/server/internal/admin"
	"github.com/pete-builds/vcrx-media-server/server/internal/artwork"
	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/subtitles"
)

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return 0, false
	}
	return id, true
}

func queryInt(r *http.Request, name string, def int) int {
	if v := r.URL.Query().Get(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func (s *Server) notFoundOr(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, catalog.ErrNotFound) || errors.Is(err, admin.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	s.internal(w, r, err)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz checks the database and that every library root answers. A hung SMB
// mount must not hang the probe, so each check has a deadline.
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	checks := map[string]string{}
	status := "ok"
	if err := s.DB.PingContext(ctx); err != nil {
		checks["database"] = "down: " + err.Error()
		status = "down"
	} else {
		checks["database"] = "ok"
	}
	if s.LibraryRoots != nil {
		roots, err := s.LibraryRoots(ctx)
		if err != nil {
			checks["libraries"] = "error: " + err.Error()
			status = "down"
		}
		for name, state := range roots {
			checks["library:"+name] = state
			if state != "ok" && status == "ok" {
				status = "degraded"
			}
		}
	}
	code := http.StatusOK
	if status != "ok" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, map[string]any{"status": status, "checks": checks})
}

func (s *Server) openapi(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(apispec.Spec)
}

func (s *Server) serverInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name": s.ServerName, "product": "vcrx-media-server", "version": s.Version, "api_version": 1,
		"auto_sign_in": s.autoSignIn(info(r).IP),
	})
}

// autoSignIn reports whether a client at ip may sign in without a password.
func (s *Server) autoSignIn(ip string) bool {
	if s.AutoSignInUser == "" {
		return false
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range s.TrustedNetworks {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

type deviceJSON struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Model         string `json:"model"`
	Platform      string `json:"platform"`
	Client        string `json:"client"`
	ClientVersion string `json:"client_version"`
}

func (d deviceJSON) device() auth.Device {
	return auth.Device{ID: clip(d.ID, 128), Name: clip(d.Name, 128), Model: clip(d.Model, 128), Platform: clip(d.Platform, 32),
		Client: clip(d.Client, 64), ClientVersion: clip(d.ClientVersion, 32)}
}

// autoLogin signs a client on a trusted network in as the configured user,
// with its own session so activity still tells devices apart.
func (s *Server) autoLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Device deviceJSON `json:"device"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ip := info(r).IP
	if s.AutoSignInUser == "" {
		writeError(w, http.StatusNotFound, "auto_sign_in_off", "Sign-in without a password is turned off on this server.")
		return
	}
	if !s.autoSignIn(ip) {
		writeError(w, http.StatusForbidden, "not_trusted", "Sign in with a username and password from this network.")
		return
	}
	dev := req.Device.device()
	token, sess, err := s.Auth.SignInAs(r.Context(), s.AutoSignInUser, dev, ip)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		s.Log.Error("auto sign-in user missing or disabled", "user", s.AutoSignInUser)
		writeError(w, http.StatusForbidden, "auto_sign_in_unavailable", "The server's automatic sign-in user does not exist or is disabled.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.Log.Info("login", "user", sess.User.Username, "ip", ip, "device", dev.Name, "client", dev.Client, "method", "trusted_network")
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expires_at": sess.ExpiresAt, "user": toUserJSON(sess.User)})
}

type userJSON struct {
	ID          int64   `json:"id"`
	Username    string  `json:"username"`
	DisplayName *string `json:"display_name"`
	IsAdmin     bool    `json:"is_admin"`
}

func toUserJSON(u auth.User) userJSON {
	out := userJSON{ID: u.ID, Username: u.Username, IsAdmin: u.IsAdmin}
	if u.DisplayName != "" {
		dn := u.DisplayName
		out.DisplayName = &dn
	}
	return out
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string     `json:"username"`
		Password string     `json:"password"`
		Device   deviceJSON `json:"device"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ip := info(r).IP
	for _, key := range []string{auth.IPKey(ip), auth.UserKey(req.Username)} {
		if ok, wait := s.Limiter.Allowed(key); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("Too many failed sign-ins. Try again in %d minutes.", int(wait.Minutes())+1))
			return
		}
	}
	dev := req.Device.device()
	token, sess, err := s.Auth.Login(r.Context(), req.Username, req.Password, dev, ip)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		s.Limiter.Fail(auth.IPKey(ip))
		s.Limiter.Fail(auth.UserKey(req.Username))
		s.Log.Warn("login failed", "ip", ip, "username", clip(req.Username, 64))
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Wrong username or password.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.Limiter.Reset(auth.UserKey(req.Username))
	s.Log.Info("login", "user", sess.User.Username, "ip", ip, "device", dev.Name, "client", dev.Client)
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expires_at": sess.ExpiresAt, "user": toUserJSON(sess.User)})
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if err := s.Auth.Logout(r.Context(), info(r).Session.ID); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toUserJSON(info(r).Session.User))
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	rows, err := s.Catalog.Home(r.Context(), ri.Policy, ri.Session.User.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

func (s *Server) libraries(w http.ResponseWriter, r *http.Request) {
	libs, err := s.Catalog.Libraries(r.Context(), info(r).Policy)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraries": libs})
}

func (s *Server) libraryItems(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "library_id")
	if !ok {
		return
	}
	q := r.URL.Query()
	unwatched, _ := strconv.ParseBool(q.Get("unwatched"))
	lq := catalog.ListQuery{Sort: q.Get("sort"), Order: q.Get("order"), Genre: q.Get("genre"), Year: queryInt(r, "year", 0),
		Resolution: q.Get("resolution"), Unwatched: unwatched, Limit: queryInt(r, "limit", 100), Offset: queryInt(r, "offset", 0)}
	ri := info(r)
	items, total, err := s.Catalog.LibraryItems(r.Context(), ri.Policy, ri.Session.User.ID, id, lq)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "Type something to search for.")
		return
	}
	ri := info(r)
	items, err := s.Catalog.Search(r.Context(), ri.Policy, ri.Session.User.ID, clip(q, 200), queryInt(r, "limit", 50))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) item(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok {
		return
	}
	ri := info(r)
	d, err := s.Catalog.Item(r.Context(), ri.Policy, ri.Session.User.ID, id)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) itemChildren(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok {
		return
	}
	ri := info(r)
	items, err := s.Catalog.Children(r.Context(), ri.Policy, ri.Session.User.ID, id)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) setWatched(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok {
		return
	}
	var req struct {
		Played *bool `json:"played"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Played == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "played is required.")
		return
	}
	ri := info(r)
	if err := s.Catalog.SetWatched(r.Context(), ri.Policy, ri.Session.User.ID, id, *req.Played, db.NowMs()); err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) artwork(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if kind != "poster" && kind != "backdrop" && kind != "thumb" {
		writeError(w, http.StatusNotFound, "not_found", "Unknown artwork kind.")
		return
	}
	ri := info(r)
	// Access is checked through the catalog so artwork never leaks hidden items.
	if sums, err := s.Catalog.Summaries(r.Context(), ri.Policy, ri.Session.User.ID, []int64{id}); err != nil || len(sums) == 0 {
		if err != nil {
			s.internal(w, r, err)
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	path, err := s.Artwork.Get(r.Context(), id, kind, queryInt(r, "width", 0))
	if err != nil {
		if !errors.Is(err, artwork.ErrNoArtwork) {
			s.Log.Warn("artwork", "item", id, "kind", kind, "err", err)
		}
		writeError(w, http.StatusNotFound, "no_artwork", "No artwork.")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, path)
}

func (s *Server) startPlayback(w http.ResponseWriter, r *http.Request) {
	var req playback.StartRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.ItemID <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "item_id is required.")
		return
	}
	ri := info(r)
	sess := ri.Session
	client := playback.Client{UserID: sess.User.ID, Username: sess.User.Username, IsAdmin: sess.User.IsAdmin, AuthSessionID: sess.ID,
		DeviceID: sess.Device.ID, DeviceName: sess.Device.Name, DeviceModel: sess.Device.Model, Platform: sess.Device.Platform,
		ClientName: sess.Device.Client, ClientVersion: sess.Device.ClientVersion, IP: ri.IP}
	out, refusal, err := s.Playback.Start(r.Context(), ri.Policy, client, req)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	if refusal != nil {
		s.Log.Info("playback refused", "user", sess.User.Username, "item", req.ItemID, "code", refusal.Code, "reason", refusal.Message)
		writeError(w, refusal.Status, refusal.Code, refusal.Message)
		return
	}
	s.Log.Info("playback start", "user", sess.User.Username, "item", req.ItemID, "version", out.Version.ID, "decision", out.Decision,
		"reason", out.Reason, "session", out.SessionID, "device", sess.Device.Name)
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) playbackEvent(w http.ResponseWriter, r *http.Request) {
	var ev playback.Event
	if !readJSON(w, r, &ev) {
		return
	}
	err := s.Playback.RecordEvent(r.Context(), info(r).Session.User.ID, r.PathValue("session_id"), ev)
	switch {
	case errors.Is(err, playback.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Unknown playback session.")
	case errors.Is(err, playback.ErrSessionEnded):
		writeError(w, http.StatusConflict, "session_ended", "That playback session has already ended.")
	case err != nil && strings.HasPrefix(err.Error(), "unknown event type"):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case err != nil:
		s.internal(w, r, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// streamVersion serves the file for direct play. http.ServeContent supplies
// Range, If-Range, HEAD and 416 handling; access is re-checked on every request.
func (s *Server) streamVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "version_id")
	if !ok {
		return
	}
	v, root, err := s.Catalog.VersionForPlayback(r.Context(), info(r).Policy, id)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	f, fi, err := fsafe.OpenUnder(root, v.Path)
	if err != nil {
		if errors.Is(err, fsafe.ErrOutsideRoot) {
			s.Log.Error("refused file outside library root", "version", id, "path", v.Path, "root", root)
		} else {
			s.Log.Warn("media file unavailable", "version", id, "err", err)
		}
		writeError(w, http.StatusNotFound, "file_unavailable", "The media file cannot be read right now.")
		return
	}
	defer func() { _ = f.Close() }()
	if m := playbackMime(v.Container); m != "" {
		w.Header().Set("Content-Type", m)
	}
	w.Header().Set("Cache-Control", "private, no-transform")
	http.ServeContent(w, r, filepath.Base(v.Path), fi.ModTime(), f)
}

func playbackMime(c *string) string {
	if c == nil {
		return ""
	}
	return map[string]string{
		"mkv": "video/x-matroska", "webm": "video/webm", "mp4": "video/mp4", "mov": "video/quicktime", "avi": "video/x-msvideo",
		"ts": "video/mp2t", "m2ts": "video/mp2t", "flv": "video/x-flv", "ogg": "video/ogg", "wmv": "video/x-ms-wmv", "mpeg": "video/mpeg",
	}[*c]
}

func (s *Server) subtitle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "stream_id")
	if !ok {
		return
	}
	vtt := r.URL.Query().Get("format") == "vtt"
	var versionID int64
	var index sql.NullInt64
	var codec, ext *string
	var external, imageBased bool
	if err := s.DB.QueryRowContext(r.Context(), `SELECT version_id, stream_index, codec, external_path, is_external, is_image_based
		FROM media_streams WHERE id = ? AND kind = 'subtitle'`, id).Scan(&versionID, &index, &codec, &ext, &external, &imageBased); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	if (external && ext == nil) || (!external && (!vtt || !index.Valid)) {
		// Embedded tracks are only served as WebVTT; the raw ones are in the stream.
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	v, root, err := s.Catalog.VersionForPlayback(r.Context(), info(r).Policy, versionID)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	if vtt && (imageBased || codec == nil || !subtitles.TextCodec(*codec) || s.Subtitles == nil) {
		writeError(w, http.StatusUnprocessableEntity, "not_text", "This subtitle is a picture format and cannot be shown as text.")
		return
	}
	if !external {
		s.embeddedSubtitle(w, r, v, root, index.Int64, id)
		return
	}
	if vtt {
		src, err := fsafe.ResolveUnder(root, *ext)
		if err != nil {
			writeError(w, http.StatusNotFound, "file_unavailable", "The subtitle file cannot be read right now.")
			return
		}
		out, err := s.Subtitles.Sidecar(r.Context(), src)
		if err != nil {
			s.Log.Warn("subtitle conversion failed", "stream", id, "err", err)
			writeError(w, http.StatusUnprocessableEntity, "conversion_failed", "This subtitle file could not be converted.")
			return
		}
		serveVTT(w, r, out)
		return
	}
	_, mime, ok := playback.SubtitleMime(*ext)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "Unsupported subtitle format.")
		return
	}
	f, fi, err := fsafe.OpenUnder(root, *ext)
	if err != nil {
		writeError(w, http.StatusNotFound, "file_unavailable", "The subtitle file cannot be read right now.")
		return
	}
	defer func() { _ = f.Close() }()
	if fi.Size() > 20<<20 {
		writeError(w, http.StatusNotFound, "file_unavailable", "Subtitle file too large.")
		return
	}
	w.Header().Set("Content-Type", mime+"; charset=utf-8")
	http.ServeContent(w, r, filepath.Base(*ext), fi.ModTime(), f)
}

// embeddedSubtitle serves a track from inside the media file as WebVTT. The first
// request starts one pass over the file for every text track and answers 202.
func (s *Server) embeddedSubtitle(w http.ResponseWriter, r *http.Request, v catalog.Version, root string, index, streamID int64) {
	src, err := fsafe.ResolveUnder(root, v.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "file_unavailable", "The media file cannot be read right now.")
		return
	}
	var all []subtitles.Track
	for _, t := range v.SubtitleTracks {
		if !t.IsExternal && !t.IsImageBased && t.Index != nil && t.Codec != nil && subtitles.TextCodec(*t.Codec) {
			all = append(all, subtitles.Track{StreamID: t.ID, Index: *t.Index})
		}
	}
	out, ready, err := s.Subtitles.Embedded(src, subtitles.Track{StreamID: streamID, Index: index}, all)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion_failed", "This subtitle track could not be extracted.")
		return
	}
	if !ready {
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "preparing",
			"message": "Reading the subtitles out of the file. Large files take a few minutes the first time."})
		return
	}
	serveVTT(w, r, out)
}

func serveVTT(w http.ResponseWriter, r *http.Request, path string) {
	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "file_unavailable", "The subtitle file cannot be read right now.")
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusNotFound, "file_unavailable", "The subtitle file cannot be read right now.")
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "subtitles.vtt", fi.ModTime(), f)
}

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

func (s *Server) adminSessions(w http.ResponseWriter, r *http.Request) {
	active, _ := strconv.ParseBool(r.URL.Query().Get("active"))
	sessions, err := s.Admin.PlaybackSessions(r.Context(), active, int64(queryInt(r, "user_id", 0)), queryInt(r, "limit", 50), queryInt(r, "offset", 0))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}
