package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
)

// First-run setup: a new server has no account, and the first person to open
// it from the local network makes the admin, in the web app, instead of
// typing `vcrx user add` into the server's terminal. It is open only while no
// account of any kind exists and only to a client on the local network
// (IsLAN, judged by the same proxy-aware address as admin routes), so a
// server exposed to the internet before setup cannot be claimed from outside.
// remote_admin does not widen it: setup from afar is what the CLI is for.

// setupNeeded reports whether a client at ip should be offered first-run setup.
func (s *Server) setupNeeded(ctx context.Context, ip string) bool {
	if s.setupDone.Load() || !IsLAN(ip) {
		return false
	}
	n, err := s.Auth.CountUsers(ctx)
	if err != nil {
		s.Log.Warn("setup check", "err", err)
		return false
	}
	if n > 0 {
		s.setupDone.Store(true)
		return false
	}
	return true
}

// setup creates the first admin and signs this client in as them.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username    string     `json:"username"`
		Password    string     `json:"password"`
		DisplayName string     `json:"display_name"`
		Device      deviceJSON `json:"device"`
		Cookie      bool       `json:"cookie"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	// Always the client header, not only when a cookie is asked for: setup
	// needs no credential, so a page on any site a LAN browser visits could
	// otherwise post a CORS "simple" request here from that browser's LAN
	// address and make an admin with a password it chose. A cross-site page
	// cannot add a custom header without a preflight this server never
	// grants, and browsers that send Sec-Fetch-Site are refused outright.
	if r.Header.Get(csrfHeader) == "" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		writeError(w, http.StatusForbidden, "csrf", "Set up from the VCRX web app.")
		return
	}
	ip := info(r).IP
	if !IsLAN(ip) {
		writeError(w, http.StatusForbidden, "lan_only", "Set up a new server from its local network.")
		return
	}
	// One setup at a time: each hashes a password (19 MiB of argon2id) before
	// its insert, and after the first wins the rest answer 409 from the flag.
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if s.setupDone.Load() {
		writeError(w, http.StatusConflict, "setup_done", "This server is already set up. Sign in instead.")
		return
	}
	if err := auth.ValidateUsername(req.Username); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := auth.ValidatePassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	u, err := s.Auth.CreateFirstAdmin(r.Context(), req.Username, req.Password, clip(req.DisplayName, 128))
	if errors.Is(err, auth.ErrSetupDone) {
		s.setupDone.Store(true)
		writeError(w, http.StatusConflict, "setup_done", "This server is already set up. Sign in instead.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.setupDone.Store(true)
	dev := req.Device.device()
	token, sess, err := s.Auth.SignInAs(r.Context(), u.Username, dev, ip)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.Log.Info("first admin created", "user", u.Username, "ip", ip, "device", dev.Name, "client", dev.Client)
	if req.Cookie {
		s.setSessionCookie(w, r, token)
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expires_at": sess.ExpiresAt, "user": toUserJSON(sess.User)})
}
