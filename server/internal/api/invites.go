package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/activity"
	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/invites"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
)

// Invited devices (milestone 9, item 1): the invite is the account. An admin
// mints a link or QR code with a scope, or approves a TV's pairing code from
// a phone; the device becomes its own principal under the same policy path as
// everyone else. See package invites for the model and the pairing flow.

// scopeJSON is the scope form: what the device sees and for how long.
type scopeJSON struct {
	Name           string  `json:"name"`
	LibraryIDs     []int64 `json:"library_ids"`
	MaxResolution  string  `json:"max_resolution"`
	AllowTranscode bool    `json:"allow_transcode"`
	ExpiresInDays  int     `json:"expires_in_days"`
}

func (j scopeJSON) scope() (invites.Scope, error) {
	if j.ExpiresInDays < 1 || j.ExpiresInDays > 365 {
		return invites.Scope{}, errors.New("an invite lasts 1 to 365 days")
	}
	return invites.Scope{LibraryIDs: j.LibraryIDs, MaxResolution: j.MaxResolution, AllowTranscode: j.AllowTranscode,
		ExpiresAt: time.Now().Add(time.Duration(j.ExpiresInDays) * 24 * time.Hour).UnixMilli()}, nil
}

// origin is this server as the client reached it, for links a phone opens.
// The Host has passed checkHost, so it is an address or an allowed name.
func (s *Server) origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	} else if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		if a, err := netip.ParseAddr(host); err == nil && s.trusted(a.Unmap()) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
			scheme = "https"
		}
	}
	return scheme + "://" + r.Host
}

// limited answers 429 when key is locked out.
func limited(w http.ResponseWriter, l *auth.Limiter, key, what string) bool {
	if ok, wait := l.Allowed(key); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("Too many %s. Try again in %d minutes.", what, int(wait.Minutes())+1))
		return true
	}
	return false
}

func inviteKey(ip string) string { return "invite:" + auth.IPKey(ip) }

// redeemInvite spends an invite and signs the device in as its principal.
func (s *Server) redeemInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Invite string     `json:"invite"`
		Device deviceJSON `json:"device"`
		Cookie bool       `json:"cookie"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Cookie && r.Header.Get(csrfHeader) == "" {
		// The answer plants a session cookie: as for the picker, a page on
		// another site must not be able to post an invite here and sign
		// this browser in as someone else's device.
		writeError(w, http.StatusForbidden, "csrf", "Missing the "+csrfHeader+" header.")
		return
	}
	ip := info(r).IP
	if limited(w, s.Limiter, inviteKey(ip), "invalid invites") {
		return
	}
	dev := req.Device.device()
	d, err := s.Invites.Redeem(r.Context(), clip(req.Invite, 128), dev, ip)
	if errors.Is(err, invites.ErrInvalid) {
		s.Limiter.Fail(inviteKey(ip))
		s.Log.Warn("invite refused", "ip", ip)
		writeError(w, http.StatusUnauthorized, "invalid_invite", "This invite is not valid. It may have been used already, revoked, or expired. Ask for a new one.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	token, sess, err := s.Auth.SignInDevice(r.Context(), d.UserID, d.ID, d.Scope.ExpiresAt, dev, ip)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.Log.Info("login", "user", sess.User.Username, "device_token", d.ID, "name", d.Name, "ip", ip, "device", dev.Name,
		"client", dev.Client, "method", "invite")
	// A lent tape's device is told "Be kind, rewind." when the loan is over,
	// which needs its session known to the loan.
	var loan *meLoan
	if s.Loans != nil {
		if err := s.Loans.RememberSession(r.Context(), d.ID, token); err != nil {
			s.internal(w, r, err)
			return
		}
		if loan, err = s.borrowerLoan(r.Context(), d.ID); err != nil {
			s.internal(w, r, err)
			return
		}
	}
	if req.Cookie {
		s.setSessionCookie(w, r, token)
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expires_at": sess.ExpiresAt, "user": toUserJSON(sess.User), "device": d,
		"loan": loan})
}

func pairKey(kind, ip string) string { return "pair-" + kind + ":" + auth.IPKey(ip) }

// pairStart is a TV asking to pair. Every start counts against the TV's
// address, so nobody can churn through user codes or fill the table.
func (s *Server) pairStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Device deviceJSON `json:"device"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ip := info(r).IP
	if limited(w, s.PairLimiter, pairKey("start", ip), "pairing codes from this address") {
		return
	}
	s.PairLimiter.Fail(pairKey("start", ip))
	deviceCode, p, err := s.Pairings.Start(req.Device.device(), ip)
	if errors.Is(err, invites.ErrTooManyPairings) {
		writeError(w, http.StatusServiceUnavailable, "too_many_pairings", err.Error())
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	link := s.origin(r) + "/#/pair/" + p.UserCode
	png, err := invites.QRPNG(link)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.Log.Info("pairing started", "ip", ip, "device", p.Device.Name)
	writeJSON(w, http.StatusOK, map[string]any{
		"device_code": deviceCode, "user_code": invites.FormatCode(p.UserCode), "verification_url": link,
		"qr_png":     base64.StdEncoding.EncodeToString(png),
		"expires_in": int(invites.PairingTTL.Seconds()), "interval": int(invites.PollInterval.Seconds()),
	})
}

// pairPoll is the TV asking whether a phone approved it yet.
func (s *Server) pairPoll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceCode string `json:"device_code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ip := info(r).IP
	if limited(w, s.PairLimiter, pairKey("poll", ip), "unknown pairing codes") {
		return
	}
	var token string
	var sess auth.Session
	status, err := s.Pairings.Poll(clip(req.DeviceCode, 64), func(p invites.Pairing) error {
		d, err := s.Invites.Get(r.Context(), p.TokenID)
		if err != nil {
			return err
		}
		if d.State != "active" {
			// Revoked between approval and the TV's poll.
			return invites.ErrPairingNotFound
		}
		token, sess, err = s.Auth.SignInDevice(r.Context(), d.UserID, d.ID, d.Scope.ExpiresAt, p.Device, ip)
		return err
	})
	switch {
	case errors.Is(err, invites.ErrPairingNotFound):
		s.PairLimiter.Fail(pairKey("poll", ip))
		writeError(w, http.StatusNotFound, "pairing_not_found", "This pairing code has expired or was turned down. Start again for a new code.")
	case err != nil:
		s.internal(w, r, err)
	case status == invites.Pending:
		writeJSON(w, http.StatusOK, map[string]any{"status": status, "interval": int(invites.PollInterval.Seconds())})
	default:
		s.Log.Info("login", "user", sess.User.Username, "device_token", sess.TokenID, "ip", ip, "method", "pairing")
		writeJSON(w, http.StatusOK, map[string]any{"status": status, "token": token, "expires_at": sess.ExpiresAt, "user": toUserJSON(sess.User)})
	}
}

func (s *Server) adminDevices(w http.ResponseWriter, r *http.Request) {
	list, err := s.Invites.List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": list})
}

// adminMintInvite makes an invite. Its secret is in this answer and nowhere
// else: the server keeps only a hash.
func (s *Server) adminMintInvite(w http.ResponseWriter, r *http.Request) {
	var req scopeJSON
	if !readJSON(w, r, &req) {
		return
	}
	sc, err := req.scope()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	by := info(r).Session.User.Username
	d, secret, err := s.Invites.Mint(r.Context(), by, req.Name, sc)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	link := s.origin(r) + "/#/invite/" + secret
	png, err := invites.QRPNG(link)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.Log.Info("invite minted", "by", by, "device_token", d.ID, "name", d.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"device": d, "invite": secret, "url": link,
		"qr_png": base64.StdEncoding.EncodeToString(png)})
}

func (s *Server) adminRevokeDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "device_id")
	if !ok {
		return
	}
	by := info(r).Session.User.Username
	cut, err := s.revokeDevice(r.Context(), id, by)
	if errors.Is(err, invites.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No such device.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	d, err := s.Invites.Get(r.Context(), id)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"device": d, "streams_stopped": cut})
}

// revokeDevice revokes a token and cuts its open streams at once, through the
// same stream registry Activity's Stop uses. Its sessions are already gone, so
// the device's very next request is refused too.
func (s *Server) revokeDevice(ctx context.Context, id int64, by string) (int, error) {
	// A lent tape's device is the loan: revoking it (or the borrower signing
	// out) brings the tape back, rather than leaving the owner's copy held
	// by a device that can no longer play it.
	if s.Loans != nil {
		l, ok, err := s.Loans.ForToken(ctx, id)
		if err != nil {
			return 0, err
		}
		if ok && l.State == "out" {
			return s.returnLoan(ctx, l.ID, by)
		}
	}
	open, err := s.Invites.Revoke(ctx, id, by)
	if err != nil {
		return 0, err
	}
	cut := s.cutStreams(open)
	s.Log.Info("device revoked", "device_token", id, "by", by, "open_streams", cut)
	return cut, nil
}

func (s *Server) cutStreams(sessions []string) int {
	n := 0
	for _, id := range sessions {
		n += s.streams.cancel(id)
		if s.HLS != nil && s.HLS.Stop(id) {
			n++
		}
	}
	return n
}

// CutExpiredDevices stops the open streams of devices whose token has expired:
// the next request is refused anyway, and this ends a film already playing.
// The server runs it every minute.
func (s *Server) CutExpiredDevices(ctx context.Context) int {
	// Lent tapes first, so a borrower's film ends with "Be kind, rewind."
	// rather than the plain expired-invite message.
	n := 0
	if s.Loans != nil {
		due, err := s.Loans.SweepExpired(ctx)
		if err != nil {
			s.Log.Warn("end expired loans", "err", err)
		} else if len(due) > 0 {
			n = s.cutStreams(due)
			s.Log.Info("expired loans' streams stopped", "sessions", len(due), "open_streams", n)
		}
	}
	open, err := s.Invites.EndExpired(ctx)
	if err != nil {
		s.Log.Warn("end expired devices' streams", "err", err)
		return n
	}
	if len(open) == 0 {
		return n
	}
	cut := s.cutStreams(open)
	s.Log.Info("expired devices' streams stopped", "sessions", len(open), "open_streams", cut)
	return n + cut
}

// streamOnly keeps invited devices to streaming (milestone 9's no-copy line):
// they reach a file only inside one of their own open playback sessions for
// it, never as a whole file to keep. plays says whether a session plays the
// file asked for (a version, or an extra such as a trailer). Accounts and
// profiles are the household's own devices, which may download.
func (s *Server) streamOnly(w http.ResponseWriter, r *http.Request, session string, plays func(playback.StreamSession) bool) bool {
	sess := info(r).Session
	if sess.TokenID == 0 {
		return true
	}
	if session != "" {
		ss, err := s.Playback.ForStream(r.Context(), sess.User.ID, session)
		if err == nil && plays(ss) && !ss.Ended {
			return true
		}
		if err != nil && !errors.Is(err, playback.ErrSessionNotFound) {
			s.internal(w, r, err)
			return false
		}
	}
	writeError(w, http.StatusForbidden, "stream_only", "An invited device can play this, but not download it. Start it from the player.")
	return false
}

type pairingJSON struct {
	UserCode  string  `json:"user_code"`
	Name      *string `json:"device_name"`
	Model     *string `json:"device_model"`
	Platform  *string `json:"platform"`
	Client    *string `json:"client"`
	IP        string  `json:"ip"`
	Location  string  `json:"location"`
	CreatedAt int64   `json:"created_at"`
	ExpiresAt int64   `json:"expires_at"`
}

func opt(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// pairingFor finds a waiting pairing for an admin. Wrong codes count against
// the admin's address, so a code cannot be found by trying them all.
func (s *Server) pairingFor(w http.ResponseWriter, r *http.Request) (string, bool) {
	ip := info(r).IP
	if limited(w, s.PairLimiter, pairKey("code", ip), "wrong pairing codes") {
		return "", false
	}
	code := invites.NormalizeCode(clip(r.PathValue("code"), 32))
	if _, err := s.Pairings.Lookup(code); err != nil {
		s.PairLimiter.Fail(pairKey("code", ip))
		writeError(w, http.StatusNotFound, "pairing_not_found", "No TV is waiting with that code. Codes last ten minutes and work once.")
		return "", false
	}
	return code, true
}

func (s *Server) adminPairing(w http.ResponseWriter, r *http.Request) {
	code, ok := s.pairingFor(w, r)
	if !ok {
		return
	}
	p, err := s.Pairings.Lookup(code)
	if err != nil {
		writeError(w, http.StatusNotFound, "pairing_not_found", "No TV is waiting with that code.")
		return
	}
	writeJSON(w, http.StatusOK, pairingJSON{UserCode: invites.FormatCode(p.UserCode), Name: opt(p.Device.Name), Model: opt(p.Device.Model),
		Platform: opt(p.Device.Platform), Client: opt(p.Device.Client), IP: p.IP, Location: activity.Location(p.IP),
		CreatedAt: p.CreatedAt.UnixMilli(), ExpiresAt: p.ExpiresAt.UnixMilli()})
}

func (s *Server) adminApprovePairing(w http.ResponseWriter, r *http.Request) {
	var req scopeJSON
	if !readJSON(w, r, &req) {
		return
	}
	code, ok := s.pairingFor(w, r)
	if !ok {
		return
	}
	sc, err := req.scope()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	by := info(r).Session.User.Username
	var d invites.Device
	err = s.Pairings.Approve(code, func(p invites.Pairing) (int64, error) {
		name := req.Name
		if strings.TrimSpace(name) == "" {
			name = p.Device.Name
		}
		var err error
		d, err = s.Invites.Approve(r.Context(), by, name, sc, p.Device)
		return d.ID, err
	})
	switch {
	case errors.Is(err, invites.ErrPairingNotFound):
		writeError(w, http.StatusNotFound, "pairing_not_found", "No TV is waiting with that code.")
	case err != nil:
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
	default:
		s.Log.Info("pairing approved", "by", by, "device_token", d.ID, "name", d.Name)
		writeJSON(w, http.StatusCreated, map[string]any{"device": d})
	}
}

func (s *Server) adminDenyPairing(w http.ResponseWriter, r *http.Request) {
	code, ok := s.pairingFor(w, r)
	if !ok {
		return
	}
	if err := s.Pairings.Deny(code); err != nil {
		writeError(w, http.StatusNotFound, "pairing_not_found", "No TV is waiting with that code.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
