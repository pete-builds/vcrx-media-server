package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pete-builds/vcrx-media-server/server/internal/federation"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// Friends' servers, the watching side (milestone 9, item 3). Home merges
// what linked peers share, each title badged with where it lives, and this
// server's household browses and plays them here.
//
// Playback proxies: this server asks the peer for a playback session as
// itself (the peer applies its policy for this server: resolution, converting,
// bandwidth), and then relays the stream to the client byte for byte, in
// memory, never to disk. The alternative, handing the client a short-lived
// grant to fetch from the peer directly, needs every client to reach the
// peer: a TV or a phone at home can reach this server, but a friend's server
// is often reachable only from this server (a Tailscale address it holds, a
// LAN route), and NAT traversal is a later milestone (item 5). Proxying also
// keeps the peer's token on this server, and lets the peer meter one
// principal against its bandwidth cap, however many of this household's
// devices are watching.
//
// Every peer answer is bounded and checked before it reaches a client: JSON
// is decoded into this server's own types and cleaned (federation.Item and
// friends), artwork must be an image, streams must be video or audio,
// playlists may name only plain segment files, and none of it is ever served
// as HTML. The paths this server requests are its own, built from ids it
// parsed as numbers; nothing a peer sends becomes a URL this server fetches.

// peerKey is the stream registry key for every open stream from a peer.
func peerKey(id int64) string { return "peer:" + strconv.FormatInt(id, 10) }

// peerSession is one of this household's playback sessions on a peer, as
// this server needs it to relay the stream.
type peerSession struct {
	PeerID, UserID int64
	ID             string // the peer's own session id
	VersionID      int64
	Protocol       string
	Subtitles      map[int64]bool
	touched        time.Time
}

type peerSessionTable struct {
	mu sync.Mutex
	m  map[string]*peerSession
}

func (t *peerSessionTable) put(ps *peerSession) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]*peerSession{}
	}
	now := time.Now()
	for id, old := range t.m {
		// A session nobody has touched in a day is over; and the table has a
		// ceiling, so a client starting sessions in a loop cannot grow it.
		if now.Sub(old.touched) > 24*time.Hour || len(t.m) > 2000 {
			delete(t.m, id)
		}
	}
	ps.touched = now
	t.m[ps.ID] = ps
}

func (t *peerSessionTable) get(peerID, userID int64, id string) (*peerSession, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	ps, ok := t.m[id]
	if !ok || ps.PeerID != peerID || ps.UserID != userID {
		return nil, false
	}
	ps.touched = time.Now()
	return ps, true
}

func (t *peerSessionTable) drop(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, id)
}

func (t *peerSessionTable) dropPeer(peerID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, ps := range t.m {
		if ps.PeerID == peerID {
			delete(t.m, id)
		}
	}
}

// fetchPeerCatalog is the cache's loader: the peer's catalog, checked.
func (s *Server) fetchPeerCatalog(ctx context.Context, p federation.Peer) (federation.Catalog, error) {
	var cat federation.Catalog
	err := s.PeerClient.GetJSON(ctx, federation.Request{Base: p.BaseURL, Token: p.Token, Path: "/api/v1/federation/catalog"},
		federation.MaxCatalogBytes, &cat)
	if err != nil {
		state := peerState(err)
		s.Peers.SetState(context.WithoutCancel(ctx), p.ID, state, describePeerErr(err))
		s.Log.Warn("friend's server catalog", "peer", p.ID, "name", p.Name, "state", state, "err", err)
		return federation.Catalog{}, err
	}
	if dropped := cat.Valid(); dropped > 0 {
		s.Log.Warn("friend's server sent titles this server refused", "peer", p.ID, "dropped", dropped)
	}
	s.Peers.SetState(context.WithoutCancel(ctx), p.ID, federation.StateOnline, "")
	return cat, nil
}

// peerState classifies a failed call to a peer.
func peerState(err error) string {
	var perr *federation.Error
	switch {
	case errors.As(err, &perr) && perr.Unlinked():
		return federation.StateUnlinked
	case errors.As(err, &perr) && perr.Status < 500:
		return federation.StateBadAnswer
	case errors.Is(err, federation.ErrTooLarge), errors.Is(err, federation.ErrBadPath), isDecodeErr(err):
		return federation.StateBadAnswer
	}
	return federation.StateOffline
}

func isDecodeErr(err error) bool {
	return err != nil && (strings.HasPrefix(err.Error(), "json:") || strings.HasPrefix(err.Error(), "invalid character") ||
		strings.Contains(err.Error(), "not JSON") || errors.Is(err, io.ErrUnexpectedEOF))
}

func describePeerErr(err error) string {
	switch peerState(err) {
	case federation.StateUnlinked:
		return "The friend's server no longer accepts this server's link."
	case federation.StateBadAnswer:
		return "The friend's server answered with something this server refused."
	}
	return "The friend's server is not answering."
}

// peerVisible applies the caller's own policy to a peer's title. A peer
// title carries none of this server's labels, so an allow list hides every
// one; a content-rating ceiling admits the ratings it admits locally, and
// hides titles with no known rating, as it does locally.
func peerVisible(pol policy.Policy, rating *string) bool {
	if pol.IsAdmin {
		return true
	}
	if len(pol.AllowLabels) > 0 {
		return false
	}
	if pol.MaxContentRating == "" {
		return true
	}
	if rating == nil {
		return false
	}
	r := policy.ContentRatingRank(*rating)
	return r >= 0 && r <= policy.ContentRatingRank(pol.MaxContentRating)
}

func unrestricted(pol policy.Policy) bool {
	return pol.IsAdmin || (pol.MaxContentRating == "" && len(pol.AllowLabels) == 0)
}

type peerJSON struct {
	ID    int64   `json:"id"`
	Name  string  `json:"name"`
	State string  `json:"state"`
	Error *string `json:"error"`
	Count int     `json:"count"`
}

type peerRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type peerItemJSON struct {
	federation.Item
	PeerID   int64  `json:"peer_id"`
	PeerName string `json:"peer_name"`
}

// peersHome is Home's friends' row: every linked peer's newest titles the
// caller may see, merged and newest first, with each peer's state. A peer
// that is offline or slow is reported as such, never waited on for long.
func (s *Server) peersHome(w http.ResponseWriter, r *http.Request) {
	if s.Peers == nil {
		peerWrite(w, http.StatusOK, map[string]any{"peers": []peerJSON{}, "items": []peerItemJSON{}})
		return
	}
	peers, err := s.Peers.List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	pol := info(r).Policy
	out := []peerJSON{}
	items := []peerItemJSON{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range peers {
		pj := peerJSON{ID: p.ID, Name: p.Name, State: p.State}
		if p.State == federation.StateUnlinked {
			out = append(out, pj)
			continue
		}
		if p.State == federation.StateTokenUnreadable {
			pj.Error = p.LastError
			out = append(out, pj)
			continue
		}
		wg.Add(1)
		go func(p federation.Peer, pj peerJSON) {
			defer wg.Done()
			st := s.PeerCache.Get(p, 2*time.Second)
			var mine []peerItemJSON
			switch {
			case st.Pending:
				pj.State = "checking"
			case st.Err != nil:
				pj.State = peerState(st.Err)
				msg := describePeerErr(st.Err)
				pj.Error = &msg
			default:
				pj.State = federation.StateOnline
				for _, it := range st.Catalog.Items {
					if peerVisible(pol, it.ContentRating) {
						mine = append(mine, peerItemJSON{Item: it, PeerID: p.ID, PeerName: p.Name})
					}
				}
				pj.Count = len(mine)
			}
			mu.Lock()
			defer mu.Unlock()
			out = append(out, pj)
			items = append(items, mine...)
		}(p, pj)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].AddedAt != items[j].AddedAt {
			return items[i].AddedAt > items[j].AddedAt
		}
		return items[i].Title < items[j].Title
	})
	if len(items) > 60 {
		items = items[:60]
	}
	peerWrite(w, http.StatusOK, map[string]any{"peers": out, "items": items})
}

// peerWrite is writeJSON for anything carrying a peer's text: the body is
// JSON with HTML-significant characters escaped, and the browser is told not
// to guess otherwise.
func peerWrite(w http.ResponseWriter, status int, v any) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeJSON(w, status, v)
}

// peerFor loads the peer named in the path.
func (s *Server) peerFor(w http.ResponseWriter, r *http.Request) (federation.Peer, bool) {
	if s.Peers == nil {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return federation.Peer{}, false
	}
	id, ok := pathID(w, r, "peer_id")
	if !ok {
		return federation.Peer{}, false
	}
	p, err := s.Peers.Get(r.Context(), id)
	if errors.Is(err, federation.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No such friend's server.")
		return p, false
	}
	if err != nil {
		s.internal(w, r, err)
		return p, false
	}
	if p.State == federation.StateUnlinked {
		writeError(w, http.StatusBadGateway, "peer_unlinked", p.Name+" has ended the link with this server.")
		return p, false
	}
	if p.State == federation.StateTokenUnreadable {
		s.Log.Error("friend's server link token cannot be read", "peer", p.ID, "name", p.Name, "err", p.TokenErr)
		writeError(w, http.StatusBadGateway, "peer_token_unreadable",
			"The link to "+p.Name+" cannot be used: this server cannot read its link token. An admin can restore the key file or link it again.")
		return p, false
	}
	return p, true
}

// peerError answers for a failed call to a peer, and records its state. A
// peer's own refusal (not found, not allowed to convert) passes through with
// its code and cleaned message; anything else is this server's own words.
func (s *Server) peerError(w http.ResponseWriter, r *http.Request, p federation.Peer, err error) {
	if errors.Is(err, context.Canceled) && r.Context().Err() != nil {
		return
	}
	state := peerState(err)
	var perr *federation.Error
	if state == federation.StateUnlinked {
		s.Peers.SetState(r.Context(), p.ID, state, describePeerErr(err))
		s.PeerCache.Forget(p.ID)
		writeError(w, http.StatusBadGateway, "peer_unlinked", p.Name+" has ended the link with this server.")
		return
	}
	if errors.As(err, &perr) {
		switch perr.Status {
		case http.StatusNotFound, http.StatusGone, http.StatusForbidden, http.StatusConflict, http.StatusUnprocessableEntity,
			http.StatusTooManyRequests, http.StatusServiceUnavailable, http.StatusBadRequest, http.StatusRequestedRangeNotSatisfiable:
			writeError(w, perr.Status, perr.Code, perr.Message)
			return
		}
	}
	if state == federation.StateOffline {
		s.Peers.SetState(r.Context(), p.ID, state, describePeerErr(err))
		s.Log.Warn("friend's server not answering", "peer", p.ID, "path", r.URL.Path, "err", err)
		writeError(w, http.StatusBadGateway, "peer_offline", p.Name+" is not answering right now.")
		return
	}
	s.Log.Warn("friend's server answered badly", "peer", p.ID, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusBadGateway, "peer_bad_answer", p.Name+" answered with something this server refused.")
}

func (s *Server) peerRequest(p federation.Peer, method, path string, q url.Values, body any) federation.Request {
	return federation.Request{Base: p.BaseURL, Token: p.Token, Method: method, Path: path, Query: q, Body: body}
}

// peerDetail loads a peer title's page, checked.
func (s *Server) peerDetail(ctx context.Context, p federation.Peer, id int64) (federation.Detail, error) {
	var d federation.Detail
	if err := s.PeerClient.GetJSON(ctx, s.peerRequest(p, "", fmt.Sprintf("/api/v1/items/%d", id), nil, nil), federation.MaxJSONBytes, &d); err != nil {
		return d, err
	}
	if !d.Valid() || d.ID != id {
		return d, fmt.Errorf("item %d: %w", id, federation.ErrTooLarge)
	}
	return d, nil
}

// errPeerHidden is a peer title the caller's own policy hides.
var errPeerHidden = errors.New("hidden by the caller's policy")

// peerAllowed applies the caller's policy to a peer title, judging a season
// or an episode by its show's rating as well as its own, as locally.
func (s *Server) peerAllowed(ctx context.Context, p federation.Peer, pol policy.Policy, d federation.Detail) error {
	if unrestricted(pol) {
		return nil
	}
	// A season or an episode with no rating of its own is judged by its show
	// below; anything else by its own rating.
	inherits := d.ContentRating == nil && d.Kind != "movie" && d.Kind != "show"
	if !inherits && !peerVisible(pol, d.ContentRating) {
		return errPeerHidden
	}
	if d.Kind == "season" || d.Kind == "episode" {
		show := d.ShowID
		if show == nil && d.Kind == "season" {
			show = d.ParentID
		}
		if show == nil {
			return errPeerHidden
		}
		sd, err := s.peerDetail(ctx, p, *show)
		if err != nil {
			return err
		}
		if !peerVisible(pol, sd.ContentRating) {
			return errPeerHidden
		}
	}
	return nil
}

// peerItemFor loads a title for the caller, or answers the error itself.
func (s *Server) peerItemFor(w http.ResponseWriter, r *http.Request, p federation.Peer) (federation.Detail, bool) {
	id, ok := pathID(w, r, "item_id")
	if !ok {
		return federation.Detail{}, false
	}
	d, err := s.peerDetail(r.Context(), p, id)
	if err == nil {
		err = s.peerAllowed(r.Context(), p, info(r).Policy, d)
	}
	if errors.Is(err, errPeerHidden) {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return d, false
	}
	if err != nil {
		s.peerError(w, r, p, err)
		return d, false
	}
	return d, true
}

// allowedVersions drops versions above the caller's own resolution ceiling.
func allowedVersions(pol policy.Policy, vs []federation.Version) []federation.Version {
	if pol.IsAdmin || pol.MaxResolution == "" {
		return vs
	}
	limit := policy.ResolutionRank(pol.MaxResolution)
	out := []federation.Version{}
	for _, v := range vs {
		if v.Resolution != nil && policy.ResolutionRank(*v.Resolution) <= limit {
			out = append(out, v)
		}
	}
	return out
}

func (s *Server) peerItem(w http.ResponseWriter, r *http.Request) {
	p, ok := s.peerFor(w, r)
	if !ok {
		return
	}
	d, ok := s.peerItemFor(w, r, p)
	if !ok {
		return
	}
	d.Versions = allowedVersions(info(r).Policy, d.Versions)
	if n := d.NextEpisode; n != nil && n.ContentRating != nil && !peerVisible(info(r).Policy, n.ContentRating) {
		// An episode stricter than its show is hidden on its own rating.
		d.NextEpisode = nil
	}
	// The page's own shape, as a local title's, plus where it lives.
	peerWrite(w, http.StatusOK, struct {
		federation.Detail
		Peer peerRef `json:"peer"`
	}{d, peerRef{p.ID, p.Name}})
}

func (s *Server) peerChildren(w http.ResponseWriter, r *http.Request) {
	p, ok := s.peerFor(w, r)
	if !ok {
		return
	}
	d, ok := s.peerItemFor(w, r, p)
	if !ok {
		return
	}
	var out struct {
		Items []federation.Item `json:"items"`
	}
	if err := s.PeerClient.GetJSON(r.Context(), s.peerRequest(p, "", fmt.Sprintf("/api/v1/items/%d/children", d.ID), nil, nil),
		federation.MaxJSONBytes, &out); err != nil {
		s.peerError(w, r, p, err)
		return
	}
	pol := info(r).Policy
	items := []federation.Item{}
	for _, it := range out.Items {
		// A child stricter than its show is hidden on its own rating.
		if len(items) < 2000 && it.Valid() && (it.ContentRating == nil || peerVisible(pol, it.ContentRating)) {
			items = append(items, it)
		}
	}
	peerWrite(w, http.StatusOK, map[string]any{"items": items})
}

// imageTypes are the only things served as a peer's artwork.
var imageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true}

func (s *Server) peerArtwork(w http.ResponseWriter, r *http.Request) {
	p, ok := s.peerFor(w, r)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if kind != "poster" && kind != "backdrop" && kind != "thumb" {
		writeError(w, http.StatusNotFound, "not_found", "Unknown artwork kind.")
		return
	}
	id, ok := pathID(w, r, "item_id")
	if !ok {
		return
	}
	pol := info(r).Policy
	if !unrestricted(pol) && !s.peerListed(p, pol, id) {
		// Not a title from the shared catalog this caller sees: check it.
		if _, ok := s.peerItemFor(w, r, p); !ok {
			return
		}
	}
	q := url.Values{}
	if width := queryInt(r, "width", 0); width > 0 {
		q.Set("width", strconv.Itoa(min(width, 1920)))
	}
	if r.URL.Query().Get("overlays") == "0" {
		q.Set("overlays", "0")
	}
	resp, err := s.PeerClient.Do(r.Context(), s.PeerClient.JSON, s.peerRequest(p, "", fmt.Sprintf("/api/v1/items/%d/artwork/%s", id, kind), q, nil))
	if err != nil {
		s.peerError(w, r, p, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		s.peerError(w, r, p, federation.ReadError(resp))
		return
	}
	b, err := federation.ReadLimited(resp.Body, federation.MaxArtworkBytes)
	if err != nil {
		s.peerError(w, r, p, err)
		return
	}
	// The bytes decide, not the peer's Content-Type: only an image is
	// served, and as that image type, never as anything a browser renders.
	ct := http.DetectContentType(b)
	if !imageTypes[ct] {
		s.Log.Warn("friend's server sent artwork that is not an image", "peer", p.ID, "item", id, "type", ct)
		writeError(w, http.StatusBadGateway, "peer_bad_answer", p.Name+" sent artwork that is not an image.")
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	_, _ = w.Write(b)
}

// peerListed reports whether id is a title in the peer's cached catalog
// that the caller's policy shows.
func (s *Server) peerListed(p federation.Peer, pol policy.Policy, id int64) bool {
	st := s.PeerCache.Get(p, 0)
	for _, it := range st.Catalog.Items {
		if it.ID == id {
			return peerVisible(pol, it.ContentRating)
		}
	}
	return false
}

var (
	remoteSession = regexp.MustCompile(`^[0-9a-f]{32}$`)
	hlsRunName    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	segmentName   = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9_.-]{0,63}$`)
	decisions     = map[string]bool{playback.DirectPlay: true, playback.Remux: true, playback.AudioTranscode: true, playback.Transcode: true}
)

type peerStreamJSON struct {
	URL      string  `json:"url"`
	Protocol string  `json:"protocol"`
	MimeType *string `json:"mime_type"`
	SeekURL  string  `json:"seek_url,omitempty"`
}

type peerSubJSON struct {
	TrackID  int64   `json:"track_id"`
	URL      string  `json:"url"`
	Codec    string  `json:"codec"`
	MimeType string  `json:"mime_type"`
	Language *string `json:"language"`
	Title    *string `json:"title"`
	IsForced bool    `json:"is_forced"`
	Embedded bool    `json:"embedded"`
}

// peerStartPlayback asks the peer for a playback session as this server.
// The peer decides, under its policy for this server, what plays and how;
// this server decides only what its own caller may ask for.
func (s *Server) peerStartPlayback(w http.ResponseWriter, r *http.Request) {
	p, ok := s.peerFor(w, r)
	if !ok {
		return
	}
	var req playback.StartRequest
	if !readJSON(w, r, &req) {
		return
	}
	if req.ItemID <= 0 {
		writeError(w, http.StatusBadRequest, "bad_request", "item_id is required.")
		return
	}
	pol := info(r).Policy
	if !unrestricted(pol) || (!pol.IsAdmin && pol.MaxResolution != "") {
		d, err := s.peerDetail(r.Context(), p, req.ItemID)
		if err == nil {
			err = s.peerAllowed(r.Context(), p, pol, d)
		}
		if errors.Is(err, errPeerHidden) {
			writeError(w, http.StatusNotFound, "not_found", "Not found.")
			return
		}
		if err != nil {
			s.peerError(w, r, p, err)
			return
		}
		// The caller's own resolution ceiling picks among the versions the
		// peer offers: the best one at or under it.
		if d.Kind == "movie" || d.Kind == "episode" {
			vs := allowedVersions(pol, d.Versions)
			if len(vs) == 0 {
				writeError(w, http.StatusNotFound, "not_found", "Not found.")
				return
			}
			if req.VersionID == nil {
				best := vs[0]
				for _, v := range vs[1:] {
					if res(v) > res(best) {
						best = v
					}
				}
				req.VersionID = &best.ID
			} else if !hasVersion(vs, *req.VersionID) {
				writeError(w, http.StatusNotFound, "not_found", "Not found.")
				return
			}
		}
	}
	if req.StartPositionMs == nil {
		// The peer's resume point is the whole server's, not this person's.
		zero := int64(0)
		req.StartPositionMs = &zero
	}
	var out playback.Session
	if err := s.PeerClient.GetJSON(r.Context(), s.peerRequest(p, http.MethodPost, "/api/v1/playback/sessions", nil, req), 256<<10, &out); err != nil {
		s.peerError(w, r, p, err)
		return
	}
	if !remoteSession.MatchString(out.SessionID) || out.Version.ID <= 0 || !decisions[out.Decision] ||
		(out.Stream.Protocol != "progressive" && out.Stream.Protocol != "fmp4" && out.Stream.Protocol != "hls") {
		s.peerError(w, r, p, fmt.Errorf("playback session: %w", federation.ErrTooLarge))
		return
	}
	ps := &peerSession{PeerID: p.ID, UserID: info(r).Session.User.ID, ID: out.SessionID, VersionID: out.Version.ID,
		Protocol: out.Stream.Protocol, Subtitles: map[int64]bool{}}
	base := fmt.Sprintf("/api/v1/peers/%d/playback/sessions/%s", p.ID, out.SessionID)
	stream := peerStreamJSON{Protocol: out.Stream.Protocol}
	switch out.Stream.Protocol {
	case "progressive":
		stream.URL = base + "/file"
		if out.Stream.MimeType != nil && strings.HasPrefix(*out.Stream.MimeType, "video/") && len(*out.Stream.MimeType) < 40 {
			stream.MimeType = out.Stream.MimeType
		}
	case "fmp4":
		mp4 := "video/mp4"
		stream.URL, stream.SeekURL, stream.MimeType = base+"/stream.mp4", base+"/seek", &mp4
	case "hls":
		m3u8 := "application/vnd.apple.mpegurl"
		stream.URL, stream.SeekURL, stream.MimeType = base+"/seek", base+"/seek", &m3u8
	}
	subs := func(list []playback.SubtitleSource, vtt bool) []peerSubJSON {
		out := []peerSubJSON{}
		for _, t := range list {
			if t.TrackID <= 0 || len(out) >= 64 {
				continue
			}
			ps.Subtitles[t.TrackID] = true
			u := base + "/subtitles/" + strconv.FormatInt(t.TrackID, 10)
			codec, mimeType := "subrip", "application/x-subrip"
			if vtt {
				u += "?format=vtt"
				codec, mimeType = "webvtt", "text/vtt"
			}
			lang, title := t.Language, t.Title
			out = append(out, peerSubJSON{TrackID: t.TrackID, URL: u, Codec: codec, MimeType: mimeType, IsForced: t.IsForced, Embedded: t.Embedded,
				Language: cleanStr(lang, 16), Title: cleanStr(title, 100)})
		}
		return out
	}
	version := federation.Version{ID: out.Version.ID, Label: out.Version.Label, Resolution: out.Version.Resolution,
		VideoCodec: out.Version.VideoCodec, HDRType: out.Version.HDRType, DurationMs: out.Version.DurationMs,
		Width: out.Version.Width, Height: out.Version.Height}
	for _, t := range out.Version.AudioTracks {
		version.AudioTracks = append(version.AudioTracks, federation.Track{ID: t.ID, Index: t.Index, Codec: t.Codec, Language: t.Language,
			Title: t.Title, Channels: t.Channels, IsDefault: t.IsDefault, IsForced: t.IsForced})
	}
	version.Valid()
	reason, _ := federation.Clean(out.Reason, 300)
	body := map[string]any{
		"session_id": out.SessionID, "decision": out.Decision, "reason": reason, "item_id": req.ItemID, "version": version,
		"stream": stream, "subtitles": subs(out.Subtitles, false), "webvtt_subtitles": subs(out.WebVTTSubtitles, true),
		"resume_position_ms": max(0, out.ResumePositionMs), "heartbeat_interval_ms": min(max(out.HeartbeatIntervalMs, 5000), 120000),
		"peer": peerRef{p.ID, p.Name},
	}
	s.peerSessions.put(ps)
	s.Log.Info("playback start from a friend's server", "user", info(r).Session.User.Username, "peer", p.ID, "item", req.ItemID,
		"session", out.SessionID, "decision", out.Decision)
	peerWrite(w, http.StatusCreated, body)
}

func res(v federation.Version) int {
	if v.Resolution == nil {
		return -1
	}
	return policy.ResolutionRank(*v.Resolution)
}

func hasVersion(vs []federation.Version, id int64) bool {
	for _, v := range vs {
		if v.ID == id {
			return true
		}
	}
	return false
}

func cleanStr(p *string, n int) *string {
	if p == nil {
		return nil
	}
	if c, ok := federation.Clean(*p, n); ok {
		return &c
	}
	return nil
}

// peerSessionFor loads the peer and one of the caller's own sessions on it.
func (s *Server) peerSessionFor(w http.ResponseWriter, r *http.Request) (federation.Peer, *peerSession, bool) {
	p, ok := s.peerFor(w, r)
	if !ok {
		return p, nil, false
	}
	ps, ok := s.peerSessions.get(p.ID, info(r).Session.User.ID, r.PathValue("session_id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "Unknown playback session.")
		return p, nil, false
	}
	return p, ps, true
}

func (s *Server) peerPlaybackEvent(w http.ResponseWriter, r *http.Request) {
	p, ps, ok := s.peerSessionFor(w, r)
	if !ok {
		return
	}
	var ev playback.Event
	if !readJSON(w, r, &ev) {
		return
	}
	if ev.Message != nil {
		ev.Message = cleanStr(ev.Message, 300)
	}
	err := s.PeerClient.GetJSON(r.Context(), s.peerRequest(p, http.MethodPost, "/api/v1/playback/sessions/"+ps.ID+"/events", nil, ev), 16<<10, nil)
	if ev.Type == "stop" || ev.Type == "error" {
		s.streams.cancel(peerKey(p.ID) + ":" + ps.ID)
		if err == nil {
			s.peerSessions.drop(ps.ID)
		}
	}
	if err != nil {
		s.peerError(w, r, p, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

var (
	seekHLS  = regexp.MustCompile(`^/api/v1/playback/sessions/([0-9a-f]{32})/hls/([A-Za-z0-9_-]{1,32})/index\.m3u8$`)
	seekFMP4 = regexp.MustCompile(`^/api/v1/playback/sessions/([0-9a-f]{32})/stream\.mp4\?start_ms=([0-9]{1,12})$`)
)

// peerSeek asks the peer where a converted stream starts, and answers with
// this server's own URL for it.
func (s *Server) peerSeek(w http.ResponseWriter, r *http.Request) {
	p, ps, ok := s.peerSessionFor(w, r)
	if !ok {
		return
	}
	q := url.Values{"position_ms": {strconv.Itoa(max(0, queryInt(r, "position_ms", 0)))}}
	if a := r.URL.Query().Get("audio"); a != "" {
		n, err := strconv.Atoi(a)
		if err != nil || n < 0 || n > 1000 {
			writeError(w, http.StatusBadRequest, "bad_request", "No such audio track.")
			return
		}
		q.Set("audio", strconv.Itoa(n))
	}
	var out struct {
		StartMs int64  `json:"start_ms"`
		URL     string `json:"url"`
	}
	if err := s.PeerClient.GetJSON(r.Context(), s.peerRequest(p, "", "/api/v1/playback/sessions/"+ps.ID+"/seek", q, nil), 16<<10, &out); err != nil {
		s.peerError(w, r, p, err)
		return
	}
	base := fmt.Sprintf("/api/v1/peers/%d/playback/sessions/%s", p.ID, ps.ID)
	var u string
	if m := seekHLS.FindStringSubmatch(out.URL); m != nil && m[1] == ps.ID && ps.Protocol == "hls" {
		u = base + "/hls/" + m[2] + "/index.m3u8"
	} else if m := seekFMP4.FindStringSubmatch(out.URL); m != nil && m[1] == ps.ID && ps.Protocol == "fmp4" {
		u = base + "/stream.mp4?start_ms=" + m[2]
	} else {
		s.peerError(w, r, p, fmt.Errorf("seek url: %w", federation.ErrBadPath))
		return
	}
	peerWrite(w, http.StatusOK, map[string]any{"start_ms": max(0, out.StartMs), "url": u})
}

func (s *Server) peerFile(w http.ResponseWriter, r *http.Request) {
	p, ps, ok := s.peerSessionFor(w, r)
	if !ok {
		return
	}
	if ps.Protocol != "progressive" {
		writeError(w, http.StatusNotFound, "not_found", "This session plays a converted stream; ask its seek URL.")
		return
	}
	s.relay(w, r, p, ps, fmt.Sprintf("/api/v1/versions/%d/file", ps.VersionID), url.Values{"session": {ps.ID}}, mediaTypes)
}

func (s *Server) peerConverted(w http.ResponseWriter, r *http.Request) {
	p, ps, ok := s.peerSessionFor(w, r)
	if !ok {
		return
	}
	if ps.Protocol != "fmp4" {
		writeError(w, http.StatusNotFound, "not_found", "This session does not play a fragmented MP4 stream.")
		return
	}
	q := url.Values{}
	for _, k := range []string{"start_ms", "audio"} {
		if v := r.URL.Query().Get(k); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "bad_request", "Invalid "+k+".")
				return
			}
			q.Set(k, strconv.FormatInt(n, 10))
		}
	}
	s.relay(w, r, p, ps, "/api/v1/playback/sessions/"+ps.ID+"/stream.mp4", q, mediaTypes)
}

func (s *Server) peerHLSPlaylist(w http.ResponseWriter, r *http.Request) {
	p, ps, ok := s.peerSessionFor(w, r)
	if !ok {
		return
	}
	run := r.PathValue("run")
	if ps.Protocol != "hls" || !hlsRunName.MatchString(run) {
		writeError(w, http.StatusNotFound, "not_found", "No such stream.")
		return
	}
	resp, err := s.PeerClient.Do(r.Context(), s.PeerClient.JSON, s.peerRequest(p, http.MethodGet, "/api/v1/playback/sessions/"+ps.ID+"/hls/"+run+"/index.m3u8", nil, nil))
	if err != nil {
		s.peerError(w, r, p, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		s.peerError(w, r, p, federation.ReadError(resp))
		return
	}
	b, err := federation.ReadLimited(resp.Body, federation.MaxPlaylistBytes)
	if err == nil && (!playlistTypes[mediaType(resp.Header.Get("Content-Type"))] || !validPlaylist(b)) {
		err = fmt.Errorf("playlist: %w", federation.ErrBadPath)
	}
	if err != nil {
		s.peerError(w, r, p, err)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(b)
}

var playlistTypes = map[string]bool{"application/vnd.apple.mpegurl": true, "application/x-mpegurl": true, "audio/mpegurl": true}

var playlistURI = regexp.MustCompile(`URI="([^"]*)"`)

// validPlaylist admits a playlist whose every URI is a plain segment name in
// the same directory: the client fetches those from this server, relative
// to the playlist, so a peer cannot point a client anywhere else.
func validPlaylist(b []byte) bool {
	if !utf8.Valid(b) || !bytes.HasPrefix(b, []byte("#EXTM3U")) {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "#"):
			uris := playlistURI.FindAllStringSubmatch(line, -1)
			if strings.Contains(line, "URI=") && (len(uris) != 1 || !strings.HasPrefix(line, "#EXT-X-MAP:")) {
				return false
			}
			for _, u := range uris {
				if !segmentName.MatchString(u[1]) {
					return false
				}
			}
		case !segmentName.MatchString(line):
			return false
		}
	}
	return true
}

func (s *Server) peerHLSSegment(w http.ResponseWriter, r *http.Request) {
	p, ps, ok := s.peerSessionFor(w, r)
	if !ok {
		return
	}
	run, seg := r.PathValue("run"), r.PathValue("segment")
	if ps.Protocol != "hls" || !hlsRunName.MatchString(run) || !segmentName.MatchString(seg) {
		writeError(w, http.StatusNotFound, "not_found", "No such segment.")
		return
	}
	s.relay(w, r, p, ps, "/api/v1/playback/sessions/"+ps.ID+"/hls/"+run+"/"+seg, nil, mediaTypes)
}

func (s *Server) peerSubtitle(w http.ResponseWriter, r *http.Request) {
	p, ps, ok := s.peerSessionFor(w, r)
	if !ok {
		return
	}
	id, ok := pathID(w, r, "stream_id")
	if !ok {
		return
	}
	if !ps.Subtitles[id] {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	q := url.Values{}
	vtt := r.URL.Query().Get("format") == "vtt"
	if vtt {
		q.Set("format", "vtt")
	}
	resp, err := s.PeerClient.Do(r.Context(), s.PeerClient.JSON, s.peerRequest(p, http.MethodGet, "/api/v1/subtitles/"+strconv.FormatInt(id, 10), q, nil))
	if err != nil {
		s.peerError(w, r, p, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusAccepted:
		// The peer is reading an embedded track out of its file.
		retry := 5
		if n, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && n > 0 && n < 60 {
			retry = n
		}
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		peerWrite(w, http.StatusAccepted, map[string]any{"status": "preparing"})
		return
	default:
		s.peerError(w, r, p, federation.ReadError(resp))
		return
	}
	b, err := federation.ReadLimited(resp.Body, federation.MaxSubtitleBytes)
	if err != nil {
		s.peerError(w, r, p, err)
		return
	}
	if !utf8.Valid(b) {
		writeError(w, http.StatusBadGateway, "peer_bad_answer", p.Name+" sent subtitles that are not text.")
		return
	}
	// WebVTT is what players read; anything else is served as plain text,
	// which a browser shows and never runs.
	ct := "text/plain; charset=utf-8"
	if vtt && bytes.HasPrefix(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), []byte("WEBVTT")) {
		ct = "text/vtt; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(b)
}

// mediaTypes are what a relayed stream may be.
func mediaTypes(t string) bool {
	return strings.HasPrefix(t, "video/") || strings.HasPrefix(t, "audio/") || t == "application/octet-stream"
}

func mediaType(ct string) string {
	t, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return ""
	}
	return strings.ToLower(t)
}

var contentRange = regexp.MustCompile(`^bytes ([0-9]{1,15}-[0-9]{1,15}|\*)/[0-9]{1,15}$`)

// relay streams one of the caller's sessions from the peer to the client, in
// memory: what arrives is written straight out, nothing is kept. Range
// requests pass through so the client seeks as it would locally. An unlink
// on this side or a revoke on the peer's cuts it at once.
func (s *Server) relay(w http.ResponseWriter, r *http.Request, p federation.Peer, ps *peerSession, path string, q url.Values, allowed func(string) bool) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	for _, k := range []string{peerKey(p.ID), peerKey(p.ID) + ":" + ps.ID} {
		s.streams.add(k, &cancel)
		defer s.streams.remove(k, &cancel)
	}
	resp, err := s.PeerClient.Do(ctx, s.PeerClient.Streams, federation.Request{Base: p.BaseURL, Token: p.Token, Method: r.Method,
		Path: path, Query: q, Header: r.Header})
	if err != nil {
		s.peerError(w, r, p, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
	case http.StatusRequestedRangeNotSatisfiable:
		if cr := resp.Header.Get("Content-Range"); contentRange.MatchString(cr) {
			w.Header().Set("Content-Range", cr)
		}
		writeError(w, http.StatusRequestedRangeNotSatisfiable, "bad_range", "That range is outside the file.")
		return
	default:
		s.peerError(w, r, p, federation.ReadError(resp))
		return
	}
	ct := mediaType(resp.Header.Get("Content-Type"))
	if !allowed(ct) {
		s.Log.Warn("friend's server sent a stream that is not media", "peer", p.ID, "type", ct)
		writeError(w, http.StatusBadGateway, "peer_bad_answer", p.Name+" sent something that is not a video.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if cl := resp.Header.Get("Content-Length"); cl != "" && resp.ContentLength >= 0 {
		h.Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	if cr := resp.Header.Get("Content-Range"); contentRange.MatchString(cr) {
		h.Set("Content-Range", cr)
	}
	if ar := resp.Header.Get("Accept-Ranges"); ar == "bytes" || ar == "none" {
		h.Set("Accept-Ranges", ar)
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	var upstream error
	err = streamUntilCancelled(ctx, w, func() error {
		buf := make([]byte, 64<<10)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return werr
				}
				_ = http.NewResponseController(w).Flush()
			}
			if errors.Is(rerr, io.EOF) {
				return nil
			}
			if rerr != nil {
				upstream = rerr
				return rerr
			}
		}
	})
	if upstream != nil || (err != nil && ctx.Err() != nil && r.Context().Err() == nil) {
		// The peer cut the stream (a revoke there) or this server did (an
		// unlink here). Abort the response rather than end it cleanly, so
		// the client sees a cut stream, not a short film.
		s.Log.Info("relayed stream cut", "peer", p.ID, "session", ps.ID, "err", err)
		panic(http.ErrAbortHandler)
	}
}
