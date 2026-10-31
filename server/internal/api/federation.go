package api

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/federation"
	"github.com/pete-builds/vcrx-media-server/server/internal/invites"
)

// Federated libraries (milestone 9, item 3). Each friend runs their own VCRX.
// To share with a friend's server, an admin mints a server invite: an invite
// token like a device's (single use, stored as a hash, expiring, revocable)
// whose principal is the friend's whole server. The friend's admin pastes the
// link in, and their server redeems it here for a session token.
//
// From then on the linked server is a principal of this one, under its scope
// exactly as a local user is under theirs: its libraries, its highest
// resolution, whether it may convert, and its bandwidth cap. It browses with
// GET /api/v1/federation/catalog and plays through the ordinary playback
// routes, which apply its policy and, as for any invited device, reach a file
// only inside one of its own playback sessions. It streams; it never copies.
//
// Revoking works from either side and at once: this server's admin revokes it
// like any device (its sessions go and its open streams are cut), and the
// friend's admin unlinking tells this server to do the same.
//
// The other half, this server watching its friends' servers, is peers.go.

// federationLink is a friend's server redeeming a server invite. It answers
// with the session token that server will present from now on: the token is
// kept here only as a hash, like every session.
func (s *Server) federationLink(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Invite string `json:"invite"`
		Server struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"server"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ip := info(r).IP
	if limited(w, s.Limiter, inviteKey(ip), "invalid invites") {
		return
	}
	name, _ := federation.Clean(req.Server.Name, 64)
	version, _ := federation.Clean(req.Server.Version, 32)
	dev := auth.Device{Name: name, Model: "VCRX server", Platform: "server", Client: "VCRX", ClientVersion: version}
	d, err := s.Invites.RedeemPeer(r.Context(), clip(req.Invite, 128), dev, ip)
	if errors.Is(err, invites.ErrInvalid) {
		s.Limiter.Fail(inviteKey(ip))
		s.Log.Warn("server invite refused", "ip", ip)
		writeError(w, http.StatusUnauthorized, "invalid_invite", "This server invite is not valid. It may have been used already, revoked, or expired. Ask for a new one.")
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
	s.Log.Info("server linked", "device_token", d.ID, "name", d.Name, "remote", name, "ip", ip)
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expires_at": sess.ExpiresAt, "link_expires_at": d.Scope.ExpiresAt,
		"server": map[string]any{"name": s.ServerName, "version": s.Version}, "summary": d.Summary})
}

// federationItem is a title in the catalog a linked server reads: the summary
// every client gets, plus the content rating its household's policies need.
type federationItem struct {
	catalog.ItemSummary
	ContentRating *string `json:"content_rating"`
}

// federationCatalog lists the movies and shows a linked server may see,
// newest first, under its own policy: the same queries as Home and the
// library grids, so it sees exactly what its scope allows and nothing else.
func (s *Server) federationCatalog(w http.ResponseWriter, r *http.Request) {
	ri := info(r)
	libs, err := s.Catalog.Libraries(r.Context(), ri.Policy)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	items := []federationItem{}
	for _, l := range libs {
		list, _, err := s.Catalog.LibraryItems(r.Context(), ri.Policy, ri.Session.User.ID, l.ID,
			catalog.ListQuery{Sort: "added", Order: "desc", Limit: 500})
		if err != nil {
			s.internal(w, r, err)
			return
		}
		for _, it := range list {
			var rating sql.NullString
			if err := s.DB.QueryRowContext(r.Context(), `SELECT content_rating FROM items WHERE id = ?`, it.ID).Scan(&rating); err != nil {
				s.internal(w, r, err)
				return
			}
			fi := federationItem{ItemSummary: it}
			if rating.Valid && rating.String != "" {
				fi.ContentRating = &rating.String
			}
			items = append(items, fi)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": map[string]any{"name": s.ServerName, "version": s.Version}, "items": items})
}

// federationUnlink is a linked server ending its own link: its admin
// unlinked this server. The token is revoked as if this server's admin had.
func (s *Server) federationUnlink(w http.ResponseWriter, r *http.Request) {
	if _, err := s.revokeDevice(r.Context(), info(r).Session.TokenID, "the linked server itself"); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// adminPeers lists both directions: the friends' servers this one watches,
// and the friends' servers linked to this one.
func (s *Server) adminPeers(w http.ResponseWriter, r *http.Request) {
	if !s.federationOn(w) {
		return
	}
	peers, err := s.Peers.List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	devices, err := s.Invites.List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	linked := []invites.Device{}
	for _, d := range devices {
		if d.Peer {
			linked = append(linked, d)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"peers": peers, "linked_servers": linked})
}

func (s *Server) federationOn(w http.ResponseWriter) bool {
	if s.Peers == nil || s.PeerClient == nil {
		writeError(w, http.StatusNotFound, "federation_off", "Linking with friends' servers is turned off on this server.")
		return false
	}
	return true
}

var peerToken = regexp.MustCompile(`^[A-Za-z0-9_-]{20,256}$`)

// adminLinkPeer pastes in a friend's server invite: this server redeems it at
// the friend's server and keeps the token it gets back.
func (s *Server) adminLinkPeer(w http.ResponseWriter, r *http.Request) {
	if !s.federationOn(w) {
		return
	}
	var req struct {
		Invite  string `json:"invite"`
		Address string `json:"address"`
		Name    string `json:"name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	base, secret, err := s.PeerClient.Guard.ParseInvite(req.Invite, req.Address)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_address", err.Error())
		return
	}
	var out struct {
		Token         string `json:"token"`
		LinkExpiresAt int64  `json:"link_expires_at"`
		Server        struct {
			Name string `json:"name"`
		} `json:"server"`
	}
	body := map[string]any{"invite": secret, "server": map[string]any{"name": s.ServerName, "version": s.Version}}
	err = s.PeerClient.GetJSON(r.Context(), federation.Request{Base: base, Method: http.MethodPost, Path: "/api/v1/federation/link", Body: body},
		64<<10, &out)
	var perr *federation.Error
	switch {
	case errors.As(err, &perr) && (perr.Status == http.StatusUnauthorized || perr.Status == http.StatusTooManyRequests):
		writeError(w, http.StatusBadRequest, "invalid_invite", "That server did not accept the invite. It may have been used already, revoked, or expired: ask your friend for a new one.")
		return
	case err != nil:
		// Not the peer's own words: an address that is not a VCRX server
		// must not be able to put text in front of the admin.
		s.Log.Warn("linking a friend's server failed", "address", base, "err", err)
		writeError(w, http.StatusBadGateway, "peer_unreachable", "Could not link to a VCRX server at "+base+". Check the address and that the server is running and reachable from here.")
		return
	}
	if !peerToken.MatchString(out.Token) || out.LinkExpiresAt <= time.Now().UnixMilli() {
		writeError(w, http.StatusBadGateway, "peer_bad_answer", "The server at "+base+" answered, but not as a VCRX server does.")
		return
	}
	remote, _ := federation.Clean(out.Server.Name, 64)
	name, _ := federation.Clean(req.Name, 64)
	if name == "" {
		name = remote
	}
	if name == "" {
		name = strings.TrimPrefix(strings.TrimPrefix(base, "http://"), "https://")
	}
	by := info(r).Session.User.Username
	p := federation.Peer{Name: name, BaseURL: base, Token: out.Token, ExpiresAt: out.LinkExpiresAt, CreatedBy: &by}
	if remote != "" {
		p.RemoteName = &remote
	}
	p, err = s.Peers.Add(r.Context(), p)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.Log.Info("linked to a friend's server", "peer", p.ID, "name", p.Name, "address", p.BaseURL, "by", by)
	// Start loading its catalog now, so Home has it by the time anyone looks.
	s.PeerCache.Get(p, 0)
	writeJSON(w, http.StatusCreated, map[string]any{"peer": p})
}

// adminUnlinkPeer ends a link from this side: open streams from that server
// stop now, its token is forgotten, and the friend's server is told so it
// revokes the token on its side too. It is unlinked here even if the friend's
// server cannot be reached.
func (s *Server) adminUnlinkPeer(w http.ResponseWriter, r *http.Request) {
	if !s.federationOn(w) {
		return
	}
	id, ok := pathID(w, r, "peer_id")
	if !ok {
		return
	}
	p, err := s.Peers.Get(r.Context(), id)
	if errors.Is(err, federation.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No such friend's server.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if err := s.Peers.Delete(r.Context(), id); err != nil {
		s.internal(w, r, err)
		return
	}
	s.PeerCache.Forget(id)
	cut := s.streams.cancel(peerKey(id))
	s.peerSessions.dropPeer(id)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	told := s.PeerClient.GetJSON(ctx, federation.Request{Base: p.BaseURL, Token: p.Token, Method: http.MethodDelete,
		Path: "/api/v1/federation/link"}, 16<<10, nil) == nil
	s.Log.Info("unlinked a friend's server", "peer", id, "name", p.Name, "streams_stopped", cut, "peer_told", told,
		"by", info(r).Session.User.Username)
	writeJSON(w, http.StatusOK, map[string]any{"streams_stopped": cut, "peer_notified": told})
}

// adminMintServerInvite makes a server invite for a friend's server. Like a
// device invite its secret is shown once and kept only as a hash. The link
// carries this server's address as the admin reached it, or the address
// given (a Tailscale address, say), which is where the friend's server will
// connect.
func (s *Server) adminMintServerInvite(w http.ResponseWriter, r *http.Request) {
	if !s.federationOn(w) {
		return
	}
	var req struct {
		scopeJSON
		BandwidthKbps int64  `json:"bandwidth_kbps"`
		Address       string `json:"address"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	sc, err := req.scope()
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	sc.BandwidthKbps = req.BandwidthKbps
	origin := s.origin(r)
	if strings.TrimSpace(req.Address) != "" {
		if origin, err = s.PeerClient.Guard.ParseBase(req.Address); err != nil {
			writeError(w, http.StatusBadRequest, "bad_address", err.Error())
			return
		}
	}
	by := info(r).Session.User.Username
	d, secret, err := s.Invites.MintPeer(r.Context(), by, req.Name, sc)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	link := origin + "/#/link/" + secret
	png, err := invites.QRPNG(link)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.Log.Info("server invite minted", "by", by, "device_token", d.ID, "name", d.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"device": d, "invite": secret, "url": link,
		"qr_png": base64.StdEncoding.EncodeToString(png)})
}

// inHousehold reports whether a session is one of this server's household:
// an admin or a household profile, never an invited device or linked server.
func (s *Server) inHousehold(ctx context.Context, sess auth.Session) (bool, error) {
	if sess.TokenID != 0 {
		return false, nil
	}
	var h bool
	err := s.DB.QueryRowContext(ctx, `SELECT household FROM users WHERE id = ?`, sess.User.ID).Scan(&h)
	return h, err
}
