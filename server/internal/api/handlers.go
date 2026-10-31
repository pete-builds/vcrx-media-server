package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	apispec "github.com/pete-builds/vcrx-media-server/server/api"
	"github.com/pete-builds/vcrx-media-server/server/internal/admin"
	"github.com/pete-builds/vcrx-media-server/server/internal/artwork"
	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
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
		// On a trusted network, show "Who's watching?" instead of auto sign-in.
		"profile_picker": s.pickerOpen(info(r).IP),
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
		Cookie bool       `json:"cookie"`
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
	if s.ProfilePicker {
		// The picker decides who is watching; POST /api/v1/auth/switch signs in.
		writeError(w, http.StatusConflict, "choose_profile", "Choose who is watching: this server shows a profile picker on this network.")
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
	if req.Cookie {
		s.setSessionCookie(w, r, token)
	}
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
		Cookie   bool       `json:"cookie"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ip := info(r).IP
	for _, key := range loginKeys(ip, req.Username) {
		if ok, wait := s.Limiter.Allowed(key); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("Too many failed sign-ins. Try again in %d minutes.", int(wait.Minutes())+1))
			return
		}
	}
	dev := req.Device.device()
	token, sess, err := s.Auth.Login(r.Context(), req.Username, req.Password, dev, ip)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		for _, key := range loginKeys(ip, req.Username) {
			s.Limiter.Fail(key)
		}
		s.Log.Warn("login failed", "ip", ip, "username", clip(req.Username, 64))
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Wrong username or password.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if !limiterLAN(ip) {
		// Only a sign-in from off the LAN clears the account's internet
		// count. LAN sign-ins clear nothing: a device guessing on the LAN
		// must not get a fresh allowance each time Pete signs in.
		s.Limiter.Reset(auth.UserKey(req.Username))
	}
	s.Log.Info("login", "user", sess.User.Username, "ip", ip, "device", dev.Name, "client", dev.Client)
	if req.Cookie {
		s.setSessionCookie(w, r, token)
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expires_at": sess.ExpiresAt, "user": toUserJSON(sess.User)})
}

// loginKeys are the limiter buckets a sign-in counts against: the client's
// address, and the account in the client's scope. The account buckets stop
// guessing spread over many addresses. They are split by where the client is:
// the internet scope (10 per window) can be filled by anyone on the internet
// failing as "pete", so it must never apply at home; the LAN scope (50 per
// window) stops a guest or IoT device rotating through a /24, and nothing off
// the LAN can touch it.
func loginKeys(ip, username string) []string {
	if limiterLAN(ip) {
		return []string{auth.IPKey(ip), auth.LANUserKey(username)}
	}
	return []string{auth.IPKey(ip), auth.UserKey(username)}
}

// limiterLAN is IsLAN without Tailscale, for sign-in limits only. A tailnet
// peer may be anywhere, so its failures count with the internet's. Admin and
// webhook gating keep IsLAN, which admits Tailscale IPv6 (a unique local
// address), so Pete keeps reaching admin over Tailscale.
func limiterLAN(ip string) bool {
	return IsLAN(ip) && !auth.IsTailscale(ip)
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
	clearSessionCookie(w, r)
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
		Decade:     queryInt(r, "decade", 0),
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

// clearProgress drops the caller's resume position so the item leaves Continue
// Watching. It does not mark the item played.
func (s *Server) clearProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok {
		return
	}
	ri := info(r)
	if err := s.Catalog.ClearProgress(r.Context(), ri.Policy, ri.Session.User.ID, id, db.NowMs()); err != nil {
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
	path, err := s.Artwork.Get(r.Context(), ri.Policy, id, kind, queryInt(r, "width", 0), r.URL.Query().Get("overlays") != "0")
	if err != nil {
		if !errors.Is(err, artwork.ErrNoArtwork) {
			s.Log.Warn("artwork", "item", id, "kind", kind, "err", err)
		}
		writeError(w, http.StatusNotFound, "no_artwork", "No artwork.")
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	// path is a cache file the artwork service built from a hash, never from the request.
	http.ServeFile(w, r, path) //nolint:gosec // see above
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
	ri := info(r)
	// Progress lands in the item's watch state, so the caller must still be
	// allowed the session's version, as for its streams.
	if ss, err := s.Playback.ForStream(r.Context(), ri.Session.User.ID, r.PathValue("session_id")); err == nil {
		if ok, err := s.Catalog.CanPlay(r.Context(), ri.Policy, ss.VersionID); err != nil || !ok {
			if err != nil {
				s.internal(w, r, err)
				return
			}
			writeError(w, http.StatusNotFound, "not_found", "Unknown playback session.")
			return
		}
	}
	err := s.Playback.RecordEvent(r.Context(), ri.Session.User.ID, r.PathValue("session_id"), ev)
	if err == nil && (ev.Type == "stop" || ev.Type == "error") {
		// The client is done with this session, so any stream still open for it
		// goes too. Otherwise a reader that keeps its connection open after
		// stopping holds a conversion slot until it disconnects, and with only
		// two transcode slots that refuses everyone else.
		s.streams.cancel(r.PathValue("session_id"))
	}
	var term *playback.TerminatedError
	switch {
	case errors.Is(err, playback.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, "not_found", "Unknown playback session.")
	case errors.As(err, &term):
		writeError(w, http.StatusGone, "session_terminated", term.Message)
	case errors.Is(err, playback.ErrSessionEnded):
		writeError(w, http.StatusConflict, "session_ended", "That playback session has already ended.")
	case err != nil && strings.HasPrefix(err.Error(), "unknown event type"):
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	case err != nil:
		s.internal(w, r, err)
	default:
		if s.HLS != nil && (ev.Type == "stop" || ev.Type == "error") {
			// The session is over: its HLS stream stops and its files go now.
			s.HLS.Stop(r.PathValue("session_id"))
		}
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
	session := r.URL.Query().Get("session")
	if session != "" {
		if msg, ok := s.Playback.Terminated(r.Context(), session); ok {
			writeError(w, http.StatusGone, "session_terminated", msg)
			return
		}
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
	var body io.ReadSeeker = f
	if session != "" {
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		s.streams.add(session, &cancel)
		defer s.streams.remove(session, &cancel)
		body = ctxReader{ctx: ctx, rs: f}
	}
	http.ServeContent(w, r, filepath.Base(v.Path), fi.ModTime(), body)
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
