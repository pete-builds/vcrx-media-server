package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/household"
)

// Household profiles, like Plex Home: "Who's watching?" on a shared screen.
// A device signed in as any household member, or a device on a trusted network
// when the picker is on, may list the household and switch to a profile with
// its PIN (none if it has none). Switching ends the device's old session and
// issues a fresh one, so the token of the profile left behind is gone.

// pickerOpen reports whether a client at ip, with no session, may use the
// picker: the server has it turned on and ip is on a trusted network.
func (s *Server) pickerOpen(ip string) bool {
	return s.ProfilePicker && s.autoSignIn(ip)
}

// pickerGate serves a household route to a client without a valid session.
// A change still needs the client header: without a session there is no
// cookie for the CSRF rule to hang on, and a cross-site form posting here
// would otherwise pick a profile and plant its session cookie.
func (s *Server) pickerGate(w http.ResponseWriter, r *http.Request, rt route) {
	if !s.pickerOpen(info(r).IP) {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Sign in first.")
		return
	}
	if !safeMethod(r.Method) && r.Header.Get(csrfHeader) == "" {
		writeError(w, http.StatusForbidden, "csrf", "Missing the "+csrfHeader+" header.")
		return
	}
	rt.handler(w, r)
}

// caller is the signed-in household member, or nil for the picker. It writes
// the refusal and returns false for anyone signed in outside the household.
func (s *Server) caller(w http.ResponseWriter, r *http.Request) (*household.Profile, bool) {
	ri := info(r)
	if !ri.Authed {
		return nil, true
	}
	p, err := s.Household.Get(r.Context(), ri.Session.User.ID)
	if err != nil {
		s.internal(w, r, err)
		return nil, false
	}
	if !p.Household {
		writeError(w, http.StatusForbidden, "not_household", "Only members of the household can switch profiles.")
		return nil, false
	}
	return &p, true
}

type profileJSON struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	IsAdmin   bool    `json:"is_admin"`
	Kid       bool    `json:"kid"`
	HasPIN    bool    `json:"has_pin"`
	Color     string  `json:"color"`
	AvatarURL *string `json:"avatar_url"`
	// Needs is what switching to this profile asks for from this caller:
	// "none", "pin", or "password" (an admin with no PIN, from a restricted
	// profile or the picker).
	Needs string `json:"needs"`
}

func avatarURL(p household.Profile) *string {
	if p.AvatarVersion == 0 {
		return nil
	}
	u := fmt.Sprintf("/api/v1/profiles/%d/avatar?v=%d", p.ID, p.AvatarVersion)
	return &u
}

func profileName(p household.Profile) string {
	if p.DisplayName != "" {
		return p.DisplayName
	}
	return p.Username
}

func needs(caller *household.Profile, p household.Profile) string {
	switch {
	case household.Needs(caller, p) == nil:
		return "none"
	case p.HasPIN:
		return "pin"
	default:
		return "password"
	}
}

// profiles lists the household for the picker.
func (s *Server) profiles(w http.ResponseWriter, r *http.Request) {
	c, ok := s.caller(w, r)
	if !ok {
		return
	}
	members, err := s.Household.Members(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]profileJSON, 0, len(members))
	var current, focus *int64
	for _, m := range members {
		out = append(out, profileJSON{ID: m.ID, Name: profileName(m), IsAdmin: m.IsAdmin, Kid: m.Kid, HasPIN: m.HasPIN,
			Color: m.Color, AvatarURL: avatarURL(m), Needs: needs(c, m)})
		if c != nil && m.ID == c.ID {
			id := m.ID
			current, focus = &id, &id
		}
		// The picker on a trusted network starts on auto_sign_in_user.
		if c == nil && focus == nil && m.Username == s.AutoSignInUser {
			id := m.ID
			focus = &id
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out, "current_id": current, "focus_id": focus})
}

func (s *Server) profileAvatar(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "user_id")
	if !ok {
		return
	}
	if _, ok := s.caller(w, r); !ok {
		return
	}
	p, err := s.Household.Get(r.Context(), id)
	if err != nil || !p.Household || p.AvatarVersion == 0 {
		writeError(w, http.StatusNotFound, "not_found", "No picture.")
		return
	}
	f, err := os.Open(s.Household.AvatarPath(id))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No picture.")
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "image/jpeg")
	// The URL carries the upload's version, so it can be kept.
	w.Header().Set("Cache-Control", "private, max-age=604800")
	http.ServeContent(w, r, "", time.UnixMilli(p.AvatarVersion), f)
}

// pinBuckets are what a PIN attempt is charged to: the client's address, and
// the profile in the client's scope (on the LAN, or away including Tailscale),
// so failures from away never lock a profile at home. The address bucket is
// tight; the profile's LAN bucket is shared by every device at home and so
// wider (household.PIN*).
func pinBuckets(ip string, id int64) []household.Bucket {
	if pinScopeLAN(ip) {
		return []household.Bucket{household.AddressBucket(auth.IPKey(ip)), household.LANProfileBucket(id)}
	}
	return []household.Bucket{household.AddressBucket(auth.IPKey(ip)), household.AwayProfileBucket(id)}
}

// passwordKeys are the sign-in limiter buckets a password offered at a switch
// counts against, the same as a sign-in's: the address, and the account in the
// client's scope. Wrong PINs never touch them, so an admin locked out of their
// PIN can still switch with their password.
func passwordKeys(ip, username string) []string {
	if pinScopeLAN(ip) {
		return []string{auth.IPKey(ip), auth.LANUserKey(username)}
	}
	return []string{auth.IPKey(ip), auth.UserKey(username)}
}

// pinScopeLAN is IsLAN without Tailscale, the scope rule the sign-in limiter
// uses: a tailnet peer may be anywhere.
func pinScopeLAN(ip string) bool {
	return IsLAN(ip) && !auth.IsTailscale(ip)
}

// lockSwitch serializes password checks for one profile and returns the unlock.
func (s *Server) lockSwitch(id int64) func() {
	m, _ := s.switchLocks.LoadOrStore(id, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Server) tooMany(w http.ResponseWriter, wait time.Duration, what string) {
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	writeError(w, http.StatusTooManyRequests, "rate_limited",
		fmt.Sprintf("Too many wrong %s for this profile. Try again in %d minutes.", what, int(wait.Minutes())+1))
}

func (s *Server) switchProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserID   int64      `json:"user_id"`
		PIN      string     `json:"pin"`
		Password string     `json:"password"`
		Device   deviceJSON `json:"device"`
		Cookie   bool       `json:"cookie"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	c, ok := s.caller(w, r)
	if !ok {
		return
	}
	ri := info(r)
	target, err := s.Household.Get(r.Context(), req.UserID)
	if errors.Is(err, household.ErrNotFound) || (err == nil && (!target.Household || target.Disabled)) {
		writeError(w, http.StatusNotFound, "not_found", "No such profile.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	cred := household.Credential{PIN: clip(req.PIN, 64), Password: clip(req.Password, 1024)}
	if cred.Password != "" {
		cred.PIN = "" // one credential per attempt, each with its own limiter
	}
	needed := household.Needs(c, target) != nil
	var charged []household.Bucket
	switch {
	case !needed:
	case cred.PIN != "":
		// Counted before the hash, in one step with the check (see Limiter.Take).
		charged = pinBuckets(ri.IP, target.ID)
		if ok, wait := s.PINs.Take(charged...); !ok {
			s.tooMany(w, wait, "PINs")
			return
		}
	case cred.Password != "":
		defer s.lockSwitch(target.ID)()
		for _, key := range passwordKeys(ri.IP, target.Username) {
			if ok, wait := s.Limiter.Allowed(key); !ok {
				s.tooMany(w, wait, "passwords")
				return
			}
		}
	}
	switch err := household.CheckSwitch(c, target, cred); {
	case errors.Is(err, household.ErrPINRequired):
		writeError(w, http.StatusUnauthorized, "pin_required", "Enter the PIN for "+profileName(target)+".")
		return
	case errors.Is(err, household.ErrCredentialRequired):
		writeError(w, http.StatusUnauthorized, "credential_required", "Enter the PIN or password for "+profileName(target)+".")
		return
	case errors.Is(err, household.ErrWrongCredential):
		if cred.Password != "" {
			for _, key := range passwordKeys(ri.IP, target.Username) {
				s.Limiter.Fail(key)
			}
		} // a PIN attempt was counted before the hash check
		s.Log.Warn("profile switch refused", "ip", ri.IP, "profile", target.Username)
		writeError(w, http.StatusUnauthorized, "wrong_pin", "That PIN is not right.")
		return
	case err != nil:
		writeError(w, http.StatusForbidden, "not_household", err.Error())
		return
	}
	dev := req.Device.device()
	if dev.ID == "" && ri.Authed {
		dev = ri.Session.Device
	}
	token, sess, err := s.Auth.SignInAs(r.Context(), target.Username, dev, ri.IP)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if ri.Authed {
		// The device leaves the old profile: its token must not linger and
		// let anyone back in without that profile's PIN.
		if err := s.Auth.Logout(r.Context(), ri.Session.ID); err != nil {
			s.Log.Warn("profile switch: old session not ended", "err", err)
		}
	}
	if charged != nil {
		// The right PIN gives its own slot back; from away it also clears the
		// away bucket, as an away sign-in clears the account's internet count.
		s.PINs.Refund(charged...)
		if !pinScopeLAN(ri.IP) {
			s.PINs.Reset(household.AwayProfileBucket(target.ID))
		}
	}
	if needed && cred.Password != "" && !pinScopeLAN(ri.IP) {
		s.Limiter.Reset(auth.UserKey(target.Username))
	}
	from := ""
	if c != nil {
		from = c.Username
	}
	s.Log.Info("login", "user", sess.User.Username, "ip", ri.IP, "device", dev.Name, "client", dev.Client,
		"method", "profile_switch", "from", from)
	if req.Cookie {
		s.setSessionCookie(w, r, token)
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expires_at": sess.ExpiresAt, "user": toUserJSON(sess.User)})
}

// Admin: who is in the household, PINs, kids' profiles and pictures.

type adminProfileJSON struct {
	profileJSON
	Username         string `json:"username"`
	Household        bool   `json:"household"`
	Managed          bool   `json:"managed"`
	Disabled         bool   `json:"disabled"`
	MaxContentRating string `json:"max_content_rating"`
}

func toAdminProfile(p household.Profile) adminProfileJSON {
	return adminProfileJSON{
		profileJSON: profileJSON{ID: p.ID, Name: profileName(p), IsAdmin: p.IsAdmin, Kid: p.Kid, HasPIN: p.HasPIN, Color: p.Color,
			AvatarURL: avatarURL(p), Needs: needs(nil, p)},
		Username: p.Username, Household: p.Household, Managed: p.Managed, Disabled: p.Disabled, MaxContentRating: p.MaxContentRating,
	}
}

func (s *Server) adminProfiles(w http.ResponseWriter, r *http.Request) {
	all, err := s.Household.All(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]adminProfileJSON, 0, len(all))
	for _, p := range all {
		out = append(out, toAdminProfile(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": out})
}

func (s *Server) adminCreateProfile(w http.ResponseWriter, r *http.Request) {
	var req household.NewProfile
	if !readJSON(w, r, &req) {
		return
	}
	p, err := s.Household.Create(r.Context(), req)
	if errors.Is(err, auth.ErrUsernameTaken) {
		writeError(w, http.StatusConflict, "conflict", "That name is taken.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toAdminProfile(p))
}

// profileErr answers for a household store error: 404 for an unknown user,
// 400 with the reason otherwise.
func (s *Server) profileErr(w http.ResponseWriter, err error) {
	if errors.Is(err, household.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	writeError(w, http.StatusBadRequest, "bad_request", err.Error())
}

func (s *Server) adminUpdateProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "user_id")
	if !ok {
		return
	}
	var req household.Update
	if !readJSON(w, r, &req) {
		return
	}
	if err := s.Household.Apply(r.Context(), id, req); err != nil {
		s.profileErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminSetPIN(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "user_id")
	if !ok {
		return
	}
	var req struct {
		PIN string `json:"pin"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if err := household.ValidatePIN(req.PIN); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	s.setPIN(w, r, id, req.PIN)
}

func (s *Server) adminClearPIN(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r, "user_id"); ok {
		s.setPIN(w, r, id, "")
	}
}

func (s *Server) setPIN(w http.ResponseWriter, r *http.Request, id int64, pin string) {
	if err := s.Household.SetPIN(r.Context(), id, pin); err != nil {
		s.profileErr(w, err)
		return
	}
	// A new PIN starts with a clean slate in both scopes.
	s.PINs.Reset(household.LANProfileBucket(id))
	s.PINs.Reset(household.AwayProfileBucket(id))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminSetAvatar(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "user_id")
	if !ok {
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, household.MaxAvatarBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "Pictures are limited to 10 MB.")
		return
	}
	if err := s.Household.SaveAvatar(r.Context(), id, data); err != nil {
		s.profileErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminDeleteAvatar(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "user_id")
	if !ok {
		return
	}
	if err := s.Household.DeleteAvatar(r.Context(), id); err != nil {
		s.profileErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
