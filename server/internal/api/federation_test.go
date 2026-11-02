package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/federation"
	"github.com/pete-builds/vcrx-media-server/server/internal/household"
	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
)

// Federated libraries (milestone 9, item 3). Two real servers in one process:
// A ("Mark's server") shares, B (Pete's) links to it with A's server invite,
// browses, and plays through its own proxy. Every behaviour and every
// security property of the link has a test here; the access walk
// (access_test.go) also runs as a linked server.

type linkedServer struct {
	deviceID, userID int64
	secret, url      string
	token            string
}

// mintServerInvite has the admin mint a server invite. scope defaults:
// every library, any resolution, converting allowed, 30 days, no cap.
func (f *fixture) mintServerInvite(scope map[string]any) (int64, string, string) {
	f.t.Helper()
	body := map[string]any{"name": "Pete's server", "expires_in_days": 30, "allow_transcode": true,
		"library_ids": []int64{f.libraryID("Movies"), f.libraryID("TV")}}
	for k, v := range scope {
		body[k] = v
	}
	r := f.do("POST", "/api/v1/admin/peers/invites", f.adminTok, body)
	if r.code != 201 {
		f.t.Fatalf("mint server invite: %d %s", r.code, r.body)
	}
	var out struct {
		Device struct {
			ID   int64
			Peer bool
		}
		Invite, URL string
	}
	r.json(f.t, &out)
	if !out.Device.Peer || !strings.HasSuffix(out.URL, "/#/link/"+out.Invite) {
		f.t.Fatalf("server invite: %s", r.body)
	}
	return out.Device.ID, out.Invite, out.URL
}

// linkServer mints a server invite and redeems it as a linked server would,
// without a second server: the walk's "peer" principal.
func (f *fixture) linkServer(scope map[string]any) linkedServer {
	f.t.Helper()
	id, secret, link := f.mintServerInvite(scope)
	r := f.do("POST", "/api/v1/federation/link", "", map[string]any{"invite": secret, "server": map[string]any{"name": "Walker VCRX", "version": "1"}})
	if r.code != 200 {
		f.t.Fatalf("link: %d %s", r.code, r.body)
	}
	var out struct{ Token string }
	r.json(f.t, &out)
	var uid int64
	if err := f.db.QueryRow(`SELECT user_id FROM device_tokens WHERE id = ?`, id).Scan(&uid); err != nil {
		f.t.Fatal(err)
	}
	return linkedServer{deviceID: id, userID: uid, secret: secret, url: link, token: out.Token}
}

// peerPair is two linked servers: B watches A over real HTTP.
type peerPair struct {
	a, b       *fixture
	aSrv, bSrv *httptest.Server
	peerID     int64 // A, as B knows it
	aDevice    int64 // B, as A knows it
	secret     string
	marker     []byte // A's 1080p file, distinct from anything on B
}

const sharedTitle = "Harbor Film"

// newPeerPair links B to A with a server invite of scope. setup runs on B
// before the link, whose catalog fetch starts in the background.
func newPeerPair(t *testing.T, scope map[string]any, setup ...func(b *fixture)) *peerPair {
	t.Helper()
	p := &peerPair{a: newFixture(t), b: newFixture(t)}
	for _, fn := range setup {
		fn(p.b)
	}
	p.a.srv.ServerName = "Mark's VCRX"
	// A's titles and its 1080p file differ from B's, so nothing on B can be
	// mistaken for something that came from A.
	if _, err := p.a.db.Exec(`UPDATE items SET title = ?, sort_title = ?, content_rating = 'PG-13' WHERE id = ?`, sharedTitle, sharedTitle, p.a.movieID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.a.db.Exec(`UPDATE items SET title = 'Quiet Lane', sort_title = 'Quiet Lane', content_rating = 'TV-14' WHERE id = ?`, p.a.showID); err != nil {
		t.Fatal(err)
	}
	p.marker = bytes.Repeat([]byte("A-SIDE-ONLY-MARKER:"), 527)[:10_000]
	var path1080 string
	_ = p.a.db.QueryRow(`SELECT path FROM media_versions WHERE id = ?`, p.a.v1080).Scan(&path1080)
	if err := os.WriteFile(path1080, p.marker, 0o644); err != nil {
		t.Fatal(err)
	}
	p.aSrv = httptest.NewServer(p.a.h)
	t.Cleanup(p.aSrv.Close)
	p.bSrv = httptest.NewServer(p.b.h)
	t.Cleanup(p.bSrv.Close)
	var link string
	p.aDevice, p.secret, link = p.a.mintServerInvite(scope)
	r := p.b.do("POST", "/api/v1/admin/peers", p.b.adminTok, map[string]any{"invite": link, "address": p.aSrv.URL, "name": "Mark's server"})
	if r.code != 201 {
		t.Fatalf("link B to A: %d %s", r.code, r.body)
	}
	var out struct{ Peer struct{ ID int64 } }
	r.json(t, &out)
	p.peerID = out.Peer.ID
	return p
}

// token is A's token as B presents it, opened from B's sealed copy.
func (p *peerPair) token(t *testing.T) string {
	t.Helper()
	peer, err := p.b.srv.Peers.Get(context.Background(), p.peerID)
	if err != nil || peer.Token == "" {
		t.Fatalf("B's peer: %+v %v", peer, err)
	}
	return peer.Token
}

type peerHome struct {
	Peers []struct {
		ID    int64
		Name  string
		State string
		Count int
	}
	Items []struct {
		ID         int64
		Kind       string
		Title      string
		Resolution *string
		PeerID     int64  `json:"peer_id"`
		PeerName   string `json:"peer_name"`
	}
}

func (p *peerPair) home(tok string) (peerHome, resp) {
	p.b.t.Helper()
	var h peerHome
	r := p.b.do("GET", "/api/v1/peers/home", tok, nil)
	if r.code != 200 {
		p.b.t.Fatalf("peers home: %d %s", r.code, r.body)
	}
	r.json(p.b.t, &h)
	return h, r
}

type peerPlay struct {
	SessionID string `json:"session_id"`
	Decision  string
	Version   struct {
		ID         int64
		Resolution *string
	}
	Stream struct {
		URL, Protocol string
		SeekURL       string `json:"seek_url"`
	}
	WebVTT []struct {
		TrackID int64 `json:"track_id"`
		URL     string
	} `json:"webvtt_subtitles"`
}

func (p *peerPair) start(tok string, body map[string]any) (peerPlay, resp) {
	p.b.t.Helper()
	var s peerPlay
	r := p.b.do("POST", fmt.Sprintf("/api/v1/peers/%d/playback/sessions", p.peerID), tok, body)
	if r.code == 201 {
		r.json(p.b.t, &s)
	}
	return s, r
}

// get fetches from B over real HTTP, so streams are streamed.
func (p *peerPair) get(path, tok string, hdr ...string) (*http.Response, error) {
	req, _ := http.NewRequest("GET", p.bSrv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	return http.DefaultClient.Do(req)
}

// filesHolding lists files under dir whose bytes include needle.
func filesHolding(t *testing.T, dir string, needle []byte) []string {
	t.Helper()
	var hits []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable entry holds nothing to find; keep walking
		}
		if d.IsDir() {
			return nil
		}
		if b, err := os.ReadFile(path); err == nil && bytes.Contains(b, needle) {
			hits = append(hits, path)
		}
		return nil
	})
	return hits
}

func fixtureDir(f *fixture) string { return filepath.Dir(filepath.Dir(f.root)) }

func TestFederationLinkBrowseAndPlay(t *testing.T) {
	t.Parallel()
	p := newPeerPair(t, nil)
	// Both admins see the link, and B's list never carries A's token.
	token := p.token(t)
	r := p.b.do("GET", "/api/v1/admin/peers", p.b.adminTok, nil)
	if r.code != 200 || !strings.Contains(string(r.body), `"name":"Mark's server"`) || !strings.Contains(string(r.body), `"remote_name":"Mark's VCRX"`) ||
		strings.Contains(string(r.body), token) {
		t.Fatalf("B's peers: %d %s", r.code, r.body)
	}
	r = p.a.do("GET", "/api/v1/admin/peers", p.a.adminTok, nil)
	var aList struct {
		LinkedServers []struct {
			ID         int64
			Peer       bool
			State      string
			DeviceName *string `json:"device_name"`
		} `json:"linked_servers"`
	}
	r.json(t, &aList)
	if len(aList.LinkedServers) != 1 || !aList.LinkedServers[0].Peer || aList.LinkedServers[0].State != "active" ||
		aList.LinkedServers[0].DeviceName == nil || *aList.LinkedServers[0].DeviceName != "Test" {
		t.Fatalf("A's linked servers: %s", r.body)
	}
	// A server invite works once.
	again := p.a.do("POST", "/api/v1/federation/link", "", map[string]any{"invite": p.secret})
	wantError(t, "the server invite again", again, 401, "invalid_invite")

	// Home: A's titles, badged with where they live, 4K included (A's scope
	// for B allows any resolution).
	h, _ := p.home(p.b.adminTok)
	if len(h.Peers) != 1 || h.Peers[0].State != "online" || h.Peers[0].Name != "Mark's server" {
		t.Fatalf("peers: %+v", h.Peers)
	}
	found := false
	for _, it := range h.Items {
		if it.Title == sharedTitle {
			found = true
			if it.Resolution == nil || *it.Resolution != "4K" || it.PeerID != p.peerID || it.PeerName != "Mark's server" || it.ID != p.a.movieID {
				t.Fatalf("badge data for %s: %+v", sharedTitle, it)
			}
		}
		if it.Title == "The Film" {
			t.Fatalf("B's own film came back as a peer title: %+v", it)
		}
	}
	if !found {
		t.Fatalf("%s is not on Home: %+v", sharedTitle, h.Items)
	}
	// The title's page and a show's seasons.
	r = p.b.do("GET", fmt.Sprintf("/api/v1/peers/%d/items/%d", p.peerID, p.a.movieID), p.b.adminTok, nil)
	if r.code != 200 || !strings.Contains(string(r.body), sharedTitle) || strings.Contains(string(r.body), "film.1080p.mkv") {
		t.Fatalf("peer item: %d %s", r.code, r.body)
	}
	r = p.b.do("GET", fmt.Sprintf("/api/v1/peers/%d/items/%d/children", p.peerID, p.a.showID), p.b.adminTok, nil)
	if r.code != 200 || !strings.Contains(string(r.body), "Season 1") {
		t.Fatalf("peer children: %d %s", r.code, r.body)
	}

	// Play: B proxies A's stream, byte for byte, with ranges.
	s, r := p.start(p.b.adminTok, map[string]any{"item_id": p.a.movieID, "version_id": p.a.v1080, "capabilities": tclCaps()})
	if r.code != 201 || s.Stream.Protocol != "progressive" ||
		s.Stream.URL != fmt.Sprintf("/api/v1/peers/%d/playback/sessions/%s/file", p.peerID, s.SessionID) {
		t.Fatalf("start: %d %s", r.code, r.body)
	}
	resp, err := p.get(s.Stream.URL, p.b.adminTok, "Range", "bytes=100-199")
	if err != nil {
		t.Fatal(err)
	}
	part, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 206 || !bytes.Equal(part, p.marker[100:200]) || resp.Header.Get("Content-Range") != "bytes 100-199/10000" ||
		resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("range through B: %d %v %q", resp.StatusCode, resp.Header, part)
	}
	resp, err = p.get(s.Stream.URL, p.b.adminTok)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(all, p.marker) {
		t.Fatalf("whole stream through B: %d %d bytes", resp.StatusCode, len(all))
	}
	// A recorded the stream as B's, under B's principal.
	var kind string
	var tokenID int64
	if err := p.a.db.QueryRow(`SELECT principal_kind, token_id FROM playback_sessions WHERE id = ?`, s.SessionID).Scan(&kind, &tokenID); err != nil ||
		kind != "token" || tokenID != p.aDevice {
		t.Fatalf("A's session row: %v %s %d", err, kind, tokenID)
	}
	// Another of B's users cannot use the admin's session.
	if r := p.b.do("GET", s.Stream.URL, p.b.friendTok, nil); r.code != 403 && r.code != 404 {
		t.Fatalf("B's friend on the admin's peer session: %d", r.code)
	}
	if r := p.b.do("POST", fmt.Sprintf("/api/v1/peers/%d/playback/sessions/%s/events", p.peerID, s.SessionID), p.b.adminTok,
		map[string]any{"type": "stop", "position_ms": 5000}); r.code != 204 {
		t.Fatalf("stop: %d %s", r.code, r.body)
	}
	var ended *int64
	_ = p.a.db.QueryRow(`SELECT ended_at FROM playback_sessions WHERE id = ?`, s.SessionID).Scan(&ended)
	if ended == nil {
		t.Fatal("A's session did not end on stop")
	}
	if r := p.b.do("GET", s.Stream.URL, p.b.adminTok, nil); r.code != 404 {
		t.Fatalf("stream after stop: %d", r.code)
	}

	// Nothing from A was ever written to B's disk: B's whole directory
	// (database, caches, media) holds no byte of A's file. Control: A's
	// directory does.
	if hits := filesHolding(t, fixtureDir(p.b), p.marker[:200]); len(hits) > 0 {
		t.Fatalf("A's file landed on B's disk: %v", hits)
	}
	if hits := filesHolding(t, fixtureDir(p.a), p.marker[:200]); len(hits) != 1 {
		t.Fatalf("control: A's own file not found on A: %v", hits)
	}
}

// A's policy for B decides what B gets, exactly as for a local user: no 4K,
// no converting, only the Movies library.
func TestFederationPeerScopeAppliesOnTheServingSide(t *testing.T) {
	t.Parallel()
	p := newPeerPair(t, map[string]any{"max_resolution": "1080p", "allow_transcode": false,
		"library_ids": []int64{newFixtureLibraryID(t, "Movies")}})
	h, _ := p.home(p.b.adminTok)
	if len(h.Items) != 1 || h.Items[0].Title != sharedTitle || h.Items[0].Resolution == nil || *h.Items[0].Resolution != "1080p" {
		t.Fatalf("home under a 1080p, Movies-only scope: %+v", h.Items)
	}
	r := p.b.do("GET", fmt.Sprintf("/api/v1/peers/%d/items/%d", p.peerID, p.a.movieID), p.b.adminTok, nil)
	if r.code != 200 || strings.Contains(string(r.body), `"4K"`) {
		t.Fatalf("the 4K version leaked through B: %s", r.body)
	}
	if r := p.b.do("GET", fmt.Sprintf("/api/v1/peers/%d/items/%d", p.peerID, p.a.showID), p.b.adminTok, nil); r.code != 404 {
		t.Fatalf("a show from an unshared library: %d %s", r.code, r.body)
	}
	if _, r := p.start(p.b.adminTok, map[string]any{"item_id": p.a.movieID, "version_id": p.a.v4k, "capabilities": tclCaps()}); r.code != 404 {
		t.Fatalf("4K through B: %d %s", r.code, r.body)
	}
	// A browser needs the E-AC-3 converted: A refuses, in its own words.
	_, r = p.start(p.b.adminTok, map[string]any{"item_id": p.a.movieID, "capabilities": browserCaps(true)})
	wantError(t, "converting under a direct-play-only scope", r, 403, "playback_refused")
	// Control: the TV direct plays the 1080p copy.
	s, r := p.start(p.b.adminTok, map[string]any{"item_id": p.a.movieID, "capabilities": tclCaps()})
	if r.code != 201 || s.Version.ID != p.a.v1080 {
		t.Fatalf("direct play of the 1080p copy: %d %s", r.code, r.body)
	}
}

// newFixtureLibraryID is a library's id in a fresh fixture: both fixtures in
// a pair insert the same libraries in the same order.
func newFixtureLibraryID(t *testing.T, name string) int64 {
	t.Helper()
	return map[string]int64{"Movies": 1, "TV": 2}[name]
}

// Revoking from either side cuts an open stream at once, and the next
// request fails.
func TestFederationRevokeMidStream(t *testing.T) {
	t.Parallel()
	for _, side := range []string{"the serving side revokes", "the watching side unlinks"} {
		t.Run(side, func(t *testing.T) {
			// A slow cap keeps the stream open long enough to cut it.
			p := newPeerPair(t, map[string]any{"bandwidth_kbps": 64})
			s, r := p.start(p.b.adminTok, map[string]any{"item_id": p.a.movieID, "version_id": p.a.v1080, "capabilities": tclCaps()})
			if r.code != 201 {
				t.Fatalf("start: %d %s", r.code, r.body)
			}
			resp, err := p.get(s.Stream.URL, p.b.adminTok)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			head := make([]byte, 1000)
			if _, err := io.ReadFull(resp.Body, head); err != nil || !bytes.Equal(head, p.marker[:1000]) {
				t.Fatalf("first bytes: %v", err)
			}
			began := time.Now()
			if side == "the serving side revokes" {
				r = p.a.do("DELETE", fmt.Sprintf("/api/v1/admin/devices/%d", p.aDevice), p.a.adminTok, nil)
			} else {
				r = p.b.do("DELETE", fmt.Sprintf("/api/v1/admin/peers/%d", p.peerID), p.b.adminTok, nil)
			}
			rest, err := io.ReadAll(resp.Body)
			if err == nil || len(head)+len(rest) >= len(p.marker) {
				t.Fatalf("the stream was not cut: %d of %d bytes, err %v", len(head)+len(rest), len(p.marker), err)
			}
			if took := time.Since(began); took > 800*time.Millisecond {
				t.Fatalf("the cut took %v; the rest of the film at the cap would take about a second", took)
			}
			want := []string{`"streams_stopped":1`}
			if side != "the serving side revokes" {
				want = append(want, `"peer_notified":true`)
			}
			for _, w := range want {
				if r.code != 200 || !strings.Contains(string(r.body), w) {
					t.Fatalf("%s: %d %s, want %s", side, r.code, r.body, w)
				}
			}
			// Either way A's side of the link is revoked, and B gets nothing more.
			var revoked *int64
			_ = p.a.db.QueryRow(`SELECT revoked_at FROM device_tokens WHERE id = ?`, p.aDevice).Scan(&revoked)
			if revoked == nil {
				t.Fatal("A's token for B is not revoked")
			}
			if side == "the serving side revokes" {
				wantError(t, "B's next request", p.b.do("GET", fmt.Sprintf("/api/v1/peers/%d/items/%d", p.peerID, p.a.movieID), p.b.adminTok, nil), 502, "peer_unlinked")
				h, _ := p.home(p.b.adminTok)
				if len(h.Items) != 0 || len(h.Peers) != 1 || h.Peers[0].State != "unlinked" {
					t.Fatalf("Home after the revoke: %+v", h)
				}
			} else {
				if r := p.b.do("GET", fmt.Sprintf("/api/v1/peers/%d/items/%d", p.peerID, p.a.movieID), p.b.adminTok, nil); r.code != 404 {
					t.Fatalf("B's next request after unlinking: %d", r.code)
				}
			}
		})
	}
}

// The serving side's per-peer bandwidth cap covers every stream the linked
// server has open at once.
func TestFederationBandwidthCap(t *testing.T) {
	t.Parallel()
	p := newPeerPair(t, map[string]any{"bandwidth_kbps": 64}) // 8,000 bytes a second
	play := func() string {
		s, r := p.start(p.b.adminTok, map[string]any{"item_id": p.a.movieID, "version_id": p.a.v1080, "capabilities": tclCaps()})
		if r.code != 201 {
			t.Fatalf("start: %d %s", r.code, r.body)
		}
		return s.Stream.URL
	}
	read := func(url string) {
		resp, err := p.get(url, p.b.adminTok)
		if err != nil {
			t.Error(err)
			return
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if !bytes.Equal(b, p.marker) {
			t.Errorf("stream: %d bytes", len(b))
		}
	}
	u := play()
	began := time.Now()
	read(u)
	if took := time.Since(began); took < time.Second {
		t.Fatalf("10,000 bytes under a 64 kbit/s cap took %v, want over a second", took)
	}
	// Two at once share the cap: twice the bytes, twice the time.
	u1, u2 := play(), play()
	began = time.Now()
	var wg sync.WaitGroup
	for _, u := range []string{u1, u2} {
		wg.Add(1)
		go func() { defer wg.Done(); read(u) }()
	}
	wg.Wait()
	if took := time.Since(began); took < 2*time.Second {
		t.Fatalf("two streams under one cap took %v, want over two seconds", took)
	}
	// Control: without the cap the same stream is immediate.
	if _, err := p.a.db.Exec(`UPDATE device_tokens SET bandwidth_kbps = NULL WHERE id = ?`, p.aDevice); err != nil {
		t.Fatal(err)
	}
	u = play()
	began = time.Now()
	read(u)
	if took := time.Since(began); took > 500*time.Millisecond {
		t.Fatalf("uncapped, the stream took %v", took)
	}
}

// A friend's server that is down or hangs shows as such, and never breaks
// Home: the other peer's titles and the local rows still come back, fast.
func TestFederationOfflinePeerNeverBreaksHome(t *testing.T) {
	t.Parallel()
	p := newPeerPair(t, nil, func(b *fixture) {
		// Longer than Home's own two-second wait, so that wait is what bounds it.
		b.srv.PeerClient.JSON.Timeout = 10 * time.Second
		b.srv.PeerCache.TTL = 0 // always ask again, as after the real TTL
	})
	// A second friend whose server accepts the link, then never answers.
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/federation/link" {
			linkAnswer(w, "Hanging VCRX")
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	// Cleanups run last first: release the hanging handler, then close.
	t.Cleanup(hang.Close)
	t.Cleanup(func() { close(release) })
	if r := p.b.do("POST", "/api/v1/admin/peers", p.b.adminTok, map[string]any{"invite": "http://192.168.1.9:3750/#/link/" + strings.Repeat("a", 32),
		"address": hang.URL, "name": "Slow server"}); r.code != 201 {
		t.Fatalf("link the hanging server: %d %s", r.code, r.body)
	}
	began := time.Now()
	h, _ := p.home(p.b.adminTok)
	if took := time.Since(began); took > 3*time.Second {
		t.Fatalf("Home waited %v on a hanging peer", took)
	}
	states := map[string]string{}
	for _, pj := range h.Peers {
		states[pj.Name] = pj.State
	}
	if states["Mark's server"] != "online" || states["Slow server"] == "online" || states["Slow server"] == "" {
		t.Fatalf("peer states: %v", states)
	}
	if len(h.Items) == 0 {
		t.Fatal("the hanging peer hid the online peer's titles")
	}
	// A goes down: it shows as offline, and playing from it fails cleanly.
	p.aSrv.Close()
	began = time.Now()
	h, _ = p.home(p.b.adminTok)
	for _, pj := range h.Peers {
		states[pj.Name] = pj.State
	}
	if states["Mark's server"] != "offline" || len(h.Items) != 0 || time.Since(began) > 3*time.Second {
		t.Fatalf("after A went down: %v, %d items, %v", states, len(h.Items), time.Since(began))
	}
	_, r := p.start(p.b.adminTok, map[string]any{"item_id": p.a.movieID, "capabilities": tclCaps()})
	wantError(t, "playing from an offline peer", r, 502, "peer_offline")
	// The local Home is untouched.
	if r := p.b.do("GET", "/api/v1/home", p.b.adminTok, nil); r.code != 200 || !strings.Contains(string(r.body), `"rows"`) {
		t.Fatalf("local Home with a peer down: %d", r.code)
	}
}

func linkAnswer(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"token": strings.Repeat("t", 43), "link_expires_at": time.Now().Add(time.Hour).UnixMilli(),
		"server": map[string]any{"name": name}})
}

// The no-copy line (PLAN.md milestone 9): no route a linked server can reach
// serves it a whole file for storage. It streams inside its own playback
// sessions and nowhere else.
func TestNoPeerRouteServesAWholeFile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	l := f.linkServer(map[string]any{"allow_transcode": true})
	if got := wholeFileRoutes(t, f, f.srv.Routes(), l.token); len(got) > 0 {
		t.Fatalf("routes serving a linked server a whole file: %v", got)
	}
	// Control: a peer-facing download route, with a download disposition,
	// is caught by name...
	serve := func(disposition bool) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			var path string
			_ = f.db.QueryRow(`SELECT path FROM media_versions WHERE id = ?`, r.PathValue("version_id")).Scan(&path)
			if disposition {
				w.Header().Set("Content-Disposition", `attachment; filename="film.mkv"`)
			}
			fh, err := os.Open(path)
			if err != nil {
				return
			}
			defer func() { _ = fh.Close() }()
			http.ServeContent(w, r, "", time.Time{}, fh)
		}
	}
	dl := route{"GET", "/api/v1/federation/versions/{version_id}/download", peerOnly, serve(true)}
	if got := wholeFileRoutes(t, f, append(f.srv.Routes(), dl), l.token); len(got) != 1 || got[0] != "GET /api/v1/federation/versions/{version_id}/download" {
		t.Fatalf("the control download route was not caught: %v", got)
	}
	// ...and so is one that only answers ranges, outside any playback
	// session, when a range covers the whole file.
	rng := route{"GET", "/api/v1/federation/versions/{version_id}/bytes", peerOnly, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			writeError(w, http.StatusBadRequest, "range_required", "Ask for a range.")
			return
		}
		serve(false)(w, r)
	}}
	if got := wholeFileRoutes(t, f, append(f.srv.Routes(), rng), l.token); len(got) != 1 || got[0] != "GET /api/v1/federation/versions/{version_id}/bytes" {
		t.Fatalf("the control range route was not caught: %v", got)
	}
}

// Peer tokens are hashed: the serving side keeps only SHA-256 of the server
// invite and of the session token it hands back.
func TestFederationTokensAreHashed(t *testing.T) {
	t.Parallel()
	p := newPeerPair(t, nil)
	token := p.token(t)
	for what, secret := range map[string]string{"the server invite": p.secret, "the session token": token} {
		if dbContains(t, p.a, strings.ToLower(secret)) {
			t.Errorf("A's database holds %s in the clear", what)
		}
	}
	raw, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(p.secret))
	want := sha256.Sum256(raw)
	var got []byte
	_ = p.a.db.QueryRow(`SELECT invite_hash FROM device_tokens WHERE id = ?`, p.aDevice).Scan(&got)
	if !bytes.Equal(got, want[:]) {
		t.Fatal("A does not keep the invite's SHA-256")
	}
	tsum := sha256.Sum256([]byte(token))
	var n int
	_ = p.a.db.QueryRow(`SELECT COUNT(*) FROM auth_sessions WHERE token_hash = ?`, tsum[:]).Scan(&n)
	if n != 1 {
		t.Fatal("A does not keep the session token's SHA-256")
	}
	// The watching side has to present the token, so it cannot hash it, but
	// it keeps it sealed with a key file outside the database.
	if dbContains(t, p.b, strings.ToLower(token)) {
		t.Error("B's database holds the token in the clear")
	}
	// Control: the scan finds what B does store, the sealed form.
	var stored string
	if err := p.b.db.QueryRow(`SELECT token FROM peers WHERE id = ?`, p.peerID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !federation.IsSealed(stored) || !dbContains(t, p.b, strings.ToLower(stored)) {
		t.Fatalf("control: the database scan cannot find B's stored token %q", stored)
	}
}

// A linked server is never an admin: every admin route refuses it, even with
// its users row flipped to admin, and it cannot redeem its invite as a
// device or a device invite as a server.
func TestLinkedServerIsNeverAnAdmin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	l := f.linkServer(nil)
	check := func(when string) {
		for _, rt := range f.srv.Routes() {
			if rt.access != adminOnly {
				continue
			}
			path := routeParam.ReplaceAllString(rt.Path, "1")
			if r := f.do(rt.Method, path, l.token, map[string]any{}); r.code != http.StatusForbidden {
				t.Errorf("%s: %s %s answered the linked server %d %.100s", when, rt.Method, rt.Path, r.code, r.body)
			}
		}
	}
	check("as itself")
	if _, err := f.db.Exec(`UPDATE users SET is_admin = 1, household = 1 WHERE id = ?`, l.userID); err != nil {
		t.Fatal(err)
	}
	check("with its users row made an admin")
	// Control: the admin reaches the same routes.
	if r := f.do("GET", "/api/v1/admin/peers", f.adminTok, nil); r.code != 200 {
		t.Fatalf("control: the admin: %d", r.code)
	}
	// A server invite is not a device invite, and the other way round.
	_, secret, _ := f.mintServerInvite(nil)
	wantError(t, "a server invite redeemed as a device", f.redeem(secret), 401, "invalid_invite")
	_, devSecret, _ := f.mint(nil)
	wantError(t, "a device invite redeemed as a server", f.do("POST", "/api/v1/federation/link", "", map[string]any{"invite": devSecret}), 401, "invalid_invite")
	// An invited device is not a linked server.
	d := f.invite(nil)
	wantError(t, "a device on a peer route", f.do("GET", "/api/v1/federation/catalog", d.token, nil), 403, "peers_only")
}

// fakePeer is a malicious friend's server: it links like VCRX, then answers
// with whatever the test sets.
type fakePeer struct {
	srv      *httptest.Server
	mu       sync.Mutex
	handlers map[string]http.HandlerFunc
	hosts    []string
}

func newFakePeer(t *testing.T) *fakePeer {
	fp := &fakePeer{handlers: map[string]http.HandlerFunc{}}
	fp.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fp.mu.Lock()
		fp.hosts = append(fp.hosts, r.Host)
		h := fp.handlers[r.Method+" "+r.URL.Path]
		fp.mu.Unlock()
		if r.URL.Path == "/api/v1/federation/link" {
			linkAnswer(w, "Evil <b>VCRX</b>\u202e")
			return
		}
		if h == nil {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(fp.srv.Close)
	return fp
}

func (fp *fakePeer) on(route string, h http.HandlerFunc) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.handlers[route] = h
}

func jsonAnswer(v any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
}

// counter is an internal service a peer would like this server to call.
func counter(t *testing.T) (*httptest.Server, *atomic.Int64) {
	var hits atomic.Int64
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("internal secret"))
	}))
	t.Cleanup(s.Close)
	return s, &hits
}

func linkFake(t *testing.T, b *fixture, fp *fakePeer) int64 {
	t.Helper()
	r := b.do("POST", "/api/v1/admin/peers", b.adminTok, map[string]any{"invite": "http://192.168.1.9:3750/#/link/" + strings.Repeat("b", 32),
		"address": fp.srv.URL})
	if r.code != 201 {
		t.Fatalf("link the fake peer: %d %s", r.code, r.body)
	}
	var out struct {
		Peer struct {
			ID   int64
			Name string
		}
	}
	r.json(t, &out)
	if out.Peer.Name != "Evil <b>VCRX</b>" {
		t.Fatalf("the peer's name was not cleaned: %q", out.Peer.Name)
	}
	return out.Peer.ID
}

func jpegBytes(t *testing.T) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 6)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// What a malicious peer sends is bounded, checked, and never served as HTML.
func TestMaliciousPeerAnswersAreBoundedAndChecked(t *testing.T) {
	t.Parallel()
	b := newFixture(t)
	fp := newFakePeer(t)
	internal, hits := counter(t)
	id := linkFake(t, b, fp)
	long := strings.Repeat("x", 10_000)
	fp.on("GET /api/v1/federation/catalog", jsonAnswer(map[string]any{"server": map[string]any{"name": "Evil"}, "items": []map[string]any{
		{"id": 1, "kind": "movie", "title": `<img src=x onerror=alert(1)>`, "added_at": 5, "has_poster": true,
			"poster_url": internal.URL + "/steal", "resolution": "4k"},
		{"id": 2, "kind": "movie", "title": "Bad\u0000Title\u202e", "added_at": 4, "resolution": "<script>"},
		{"id": 3, "kind": "movie", "title": long, "added_at": 3},
		{"id": -1, "kind": "movie", "title": "Negative", "added_at": 2},
		{"id": 4, "kind": "admin", "title": "Wrong kind", "added_at": 2},
		{"id": 5, "kind": "movie", "title": "   ", "added_at": 2},
		{"id": 6, "kind": "episode", "title": "Not a top-level title", "added_at": 2},
	}}))
	// Linking warmed the cache before this catalog existed; on a fast runner
	// that 404 is cached as a bad answer.
	b.srv.PeerCache.Forget(id)
	_, r := homeOf(t, b)
	body := string(r.body)
	if strings.Contains(body, "<img") || !strings.Contains(body, `\u003cimg src=x onerror=alert(1)\u003e`) ||
		r.hdr.Get("Content-Type") != "application/json" || r.hdr.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("the peer's markup reached a client unescaped: %v %s", r.hdr, body)
	}
	var h peerHome
	r.json(t, &h)
	titles := map[int64]string{}
	for _, it := range h.Items {
		titles[it.ID] = it.Title
		if it.ID == 1 && (it.Resolution == nil || *it.Resolution != "4K") {
			t.Errorf("resolution not normalised: %v", it.Resolution)
		}
		if it.ID == 2 && it.Resolution != nil {
			t.Errorf("an unknown resolution passed: %q", *it.Resolution)
		}
	}
	if len(titles) != 3 || titles[2] != "BadTitle" || len([]rune(titles[3])) != 200 || strings.Contains(body, "poster_url") {
		t.Fatalf("validated catalog: %v", titles)
	}

	// Artwork: only images, at most 5 MiB, as the image type the bytes are.
	fp.on("GET /api/v1/items/1/artwork/poster", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("<html><script>alert(document.cookie)</script></html>"))
	})
	fp.on("GET /api/v1/items/2/artwork/poster", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(append(jpegBytes(t), make([]byte, federation.MaxArtworkBytes)...))
	})
	good := jpegBytes(t)
	fp.on("GET /api/v1/items/3/artwork/poster", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(good)
	})
	wantError(t, "HTML posing as artwork", b.do("GET", fmt.Sprintf("/api/v1/peers/%d/items/1/artwork/poster", id), b.adminTok, nil), 502, "peer_bad_answer")
	wantError(t, "artwork over 5 MiB", b.do("GET", fmt.Sprintf("/api/v1/peers/%d/items/2/artwork/poster", id), b.adminTok, nil), 502, "peer_bad_answer")
	// Control: a real JPEG passes, as a JPEG, whatever the peer called it.
	r = b.do("GET", fmt.Sprintf("/api/v1/peers/%d/items/3/artwork/poster", id), b.adminTok, nil)
	if r.code != 200 || r.hdr.Get("Content-Type") != "image/jpeg" || r.hdr.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(r.hdr.Get("Content-Security-Policy"), "sandbox") || !bytes.Equal(r.body, good) {
		t.Fatalf("control artwork: %d %v", r.code, r.hdr)
	}

	// A catalog over 4 MiB is refused whole: the peer shows as a bad answer.
	fp.on("GET /api/v1/federation/catalog", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[`))
		item := []byte(`{"id":1,"kind":"movie","title":"` + strings.Repeat("y", 1000) + `","added_at":1},`)
		for range federation.MaxCatalogBytes/len(item) + 10 {
			_, _ = w.Write(item)
		}
		_, _ = w.Write([]byte(`{"id":2,"kind":"movie","title":"z","added_at":1}]}`))
	})
	b.srv.PeerCache.Forget(id)
	h, _ = homeOf(t, b)
	if len(h.Items) != 0 || len(h.Peers) != 1 || h.Peers[0].State != "bad_answer" {
		t.Fatalf("an oversized catalog: %+v", h)
	}

	// Playback: the peer's URLs are never used. Its stream URL and subtitle
	// URL point at an internal service; this server builds its own paths
	// and streams only what is video.
	fp.on("POST /api/v1/playback/sessions", jsonAnswer(map[string]any{
		"session_id": strings.Repeat("ab", 16), "decision": "direct_play", "reason": "<b>ok</b>", "item_id": 1,
		"version":          map[string]any{"id": 7, "file_name": "/srv/peer-media/film.mkv", "duration_ms": 1000},
		"stream":           map[string]any{"url": internal.URL + "/steal", "protocol": "progressive", "mime_type": "text/html"},
		"webvtt_subtitles": []map[string]any{{"track_id": 9, "url": internal.URL + "/subs"}},
	}))
	fp.on("GET /api/v1/versions/7/file", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<script>alert(1)</script>"))
	})
	fp.on("GET /api/v1/subtitles/9", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<script>alert(1)</script>"))
	})
	r = b.do("POST", fmt.Sprintf("/api/v1/peers/%d/playback/sessions", id), b.adminTok, map[string]any{"item_id": 1, "capabilities": tclCaps()})
	var s peerPlay
	r.json(t, &s)
	if r.code != 201 || strings.Contains(string(r.body), internal.URL) || strings.Contains(string(r.body), "/srv/peer-media/") || strings.Contains(string(r.body), "text/html") ||
		s.Stream.URL != fmt.Sprintf("/api/v1/peers/%d/playback/sessions/%s/file", id, s.SessionID) {
		t.Fatalf("start through a malicious peer: %d %s", r.code, r.body)
	}
	wantError(t, "a stream that is HTML", b.do("GET", s.Stream.URL, b.adminTok, nil), 502, "peer_bad_answer")
	r = b.do("GET", s.WebVTT[0].URL, b.adminTok, nil)
	if r.code != 200 || r.hdr.Get("Content-Type") != "text/plain; charset=utf-8" || r.hdr.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("subtitles that are HTML: %d %v", r.code, r.hdr)
	}
	if hits.Load() != 0 {
		t.Fatalf("this server fetched a URL the peer gave it: %d hits", hits.Load())
	}
}

func homeOf(t *testing.T, b *fixture) (peerHome, resp) {
	t.Helper()
	var h peerHome
	r := b.do("GET", "/api/v1/peers/home", b.adminTok, nil)
	if r.code != 200 {
		t.Fatalf("peers home: %d %s", r.code, r.body)
	}
	r.json(t, &h)
	return h, r
}

// A playlist from a peer may name only plain segment files beside it.
func TestPeerPlaylistsNameOnlyTheirOwnSegments(t *testing.T) {
	t.Parallel()
	for pl, want := range map[string]bool{
		"#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:2.0,\nseg-00001.m4s\n#EXT-X-ENDLIST\n": true,
		"#EXTM3U\n#EXTINF:2.0,\nhttp://192.168.1.1/admin\n":                                   false,
		"#EXTM3U\n#EXTINF:2.0,\n../../admin/users\n":                                          false,
		"#EXTM3U\n#EXTINF:2.0,\n/api/v1/admin/users\n":                                        false,
		"#EXTM3U\n#EXT-X-MAP:URI=\"https://evil.example/init.mp4\"\n":                         false,
		"#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key\"\n":                                    false,
		"<html>\n": false,
	} {
		if got := validPlaylist([]byte(pl)); got != want {
			t.Errorf("validPlaylist(%q) = %v, want %v", pl, got, want)
		}
	}
}

// SSRF: an address an admin enters, or a peer's invite carries, cannot make
// this server fetch anything but that peer's own API.
func TestPeerAddressCannotReachInternalURLs(t *testing.T) {
	t.Parallel()
	b := newFixture(t)
	b.srv.PeerClient = federation.NewClient(federation.Guard{AllowLoopback: false}, "test")
	internal, hits := counter(t)
	secret := strings.Repeat("c", 32)
	_, port, _ := strings.Cut(strings.TrimPrefix(internal.URL, "http://127.0.0.1"), ":")
	for _, c := range []struct{ invite, address string }{
		{internal.URL + "/#/link/" + secret, ""},                           // loopback: this server itself
		{"http://localhost:" + port + "/#/link/" + secret, ""},             // loopback by name
		{"http://169.254.169.254/#/link/" + secret, ""},                    // cloud metadata
		{"http://[fe80::1]:3750/#/link/" + secret, ""},                     // link-local
		{"http://0.0.0.0:" + port + "/#/link/" + secret, ""},               // unspecified
		{"http://192.168.1.9@127.0.0.1:" + port + "/#/link/" + secret, ""}, // user info hiding the real host
		{"http://192.168.1.9:3750/api/v1/admin/#/link/" + secret, ""},      // a path
		{"file:///etc/passwd#/link/" + secret, ""},                         // another scheme
		{"http://192.168.1.9:3750/#/link/" + secret, internal.URL},         // the override address
	} {
		r := b.do("POST", "/api/v1/admin/peers", b.adminTok, map[string]any{"invite": c.invite, "address": c.address})
		if r.code == 201 {
			t.Errorf("linked to %q / %q", c.invite, c.address)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("the internal service was reached %d times", hits.Load())
	}
	// Control: with loopback allowed (development), the same internal
	// address is reached, which shows the guard is what stopped it. (The
	// check on the address actually dialled, after DNS, is in package
	// federation: TestDialGuardChecksTheResolvedAddress.)
	b.srv.PeerClient = federation.NewClient(federation.Guard{AllowLoopback: true}, "test")
	b.do("POST", "/api/v1/admin/peers", b.adminTok, map[string]any{"invite": internal.URL + "/#/link/" + secret})
	if hits.Load() == 0 {
		t.Fatal("control: the internal service was not reached with loopback allowed")
	}

	// A linked peer cannot redirect this server anywhere: redirects are
	// never followed.
	hits.Store(0)
	fp := newFakePeer(t)
	id := linkFake(t, b, fp)
	fp.on("GET /api/v1/federation/catalog", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL+"/catalog", http.StatusFound)
	})
	fp.on("GET /api/v1/items/1", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL+"/item", http.StatusTemporaryRedirect)
	})
	b.srv.PeerCache.Forget(id) // drop the catalog linking warmed before these routes
	h, _ := homeOf(t, b)
	if len(h.Items) != 0 || h.Peers[0].State == "online" {
		t.Fatalf("a redirecting peer: %+v", h)
	}
	if r := b.do("GET", fmt.Sprintf("/api/v1/peers/%d/items/1", id), b.adminTok, nil); r.code != 502 {
		t.Fatalf("a redirected item: %d %s", r.code, r.body)
	}
	// Ids from the client are numbers and names are plain, so no path can
	// be bent toward another route on the peer.
	for _, path := range []string{
		fmt.Sprintf("/api/v1/peers/%d/items/1%%2F..%%2F..%%2Fadmin%%2Fusers", id),
		fmt.Sprintf("/api/v1/peers/%d/items/1/artwork/..%%2F..%%2Fadmin", id),
	} {
		if r := b.do("GET", path, b.adminTok, nil); r.code != 404 {
			t.Errorf("%s: %d", path, r.code)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("the internal service was reached through a peer %d times", hits.Load())
	}
	// Every request reached the fake peer by the address entered.
	fp.mu.Lock()
	defer fp.mu.Unlock()
	for _, host := range fp.hosts {
		if "http://"+host != fp.srv.URL {
			t.Fatalf("a request went to the peer as host %q", host)
		}
	}
}

// DNS rebinding: the routes a linked server calls refuse a Host this server
// does not answer to, as every route does.
func TestPeerRoutesCheckTheHost(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	l := f.linkServer(nil)
	for _, c := range []struct{ method, path, tok string }{
		{"POST", "/api/v1/federation/link", ""},
		{"GET", "/api/v1/federation/catalog", l.token},
		{"DELETE", "/api/v1/federation/link", l.token},
		{"POST", "/api/v1/playback/sessions", l.token},
		{"GET", fmt.Sprintf("/api/v1/versions/%d/file", f.v1080), l.token},
		{"GET", "/api/v1/peers/home", f.adminTok},
	} {
		wantError(t, c.method+" "+c.path+" for a rebound name", f.do(c.method, c.path, c.tok, nil, "Host", "rebind.attacker.example"), 421, "host_not_allowed")
		// Control: by its address the same call gets past the check.
		if r := f.do(c.method, c.path, c.tok, nil, "Host", "192.168.1.20:3750"); r.code == 421 {
			t.Errorf("%s %s by address: 421", c.method, c.path)
		}
	}
}

// This server's own policy applies to what its household sees of a peer: a
// profile with a PG-13 ceiling is refused a peer's R-rated film everywhere,
// and a 1080p profile plays the 1080p copy.
func TestPeerTitlesFollowTheCallersPolicy(t *testing.T) {
	t.Parallel()
	p := newPeerPair(t, nil)
	if _, err := p.a.db.Exec(`UPDATE items SET content_rating = 'R' WHERE id IN (?, ?)`, p.a.movieID, p.a.showID); err != nil {
		t.Fatal(err)
	}
	p.b.srv.PeerCache.Forget(p.peerID)
	b := p.b
	b.srv.Household = &household.Store{DB: b.db, AvatarDir: filepath.Join(t.TempDir(), "avatars")}
	b.srv.PINs = household.NewLimiter(household.PINWindow)
	profile := func(name string, pol map[string]any) string {
		r := b.do("POST", "/api/v1/admin/profiles", b.adminTok, map[string]any{"name": name})
		var pr struct{ ID int64 }
		r.json(t, &pr)
		if r.code != 201 {
			t.Fatalf("profile: %d %s", r.code, r.body)
		}
		if r := b.do("PUT", fmt.Sprintf("/api/v1/admin/users/%d/policy", pr.ID), b.adminTok, pol); r.code != 204 {
			t.Fatalf("policy: %d %s", r.code, r.body)
		}
		r = b.do("POST", "/api/v1/auth/switch", b.login("pete", "adminpass1"), map[string]any{"user_id": pr.ID})
		var out struct{ Token string }
		r.json(t, &out)
		return out.Token
	}
	kid := profile("Kid", map[string]any{"max_content_rating": "PG-13", "allow_transcode": true})
	base := fmt.Sprintf("/api/v1/peers/%d", p.peerID)
	probes := []struct{ method, path string }{
		{"GET", fmt.Sprintf("%s/items/%d", base, p.a.movieID)},
		{"GET", fmt.Sprintf("%s/items/%d/children", base, p.a.showID)},
		{"GET", fmt.Sprintf("%s/items/%d", base, p.a.seasonID)},
		{"GET", fmt.Sprintf("%s/items/%d", base, p.a.episodeID)},
		{"GET", fmt.Sprintf("%s/items/%d/artwork/poster", base, p.a.movieID)},
		{"POST", base + "/playback/sessions"},
	}
	body := map[string]any{"item_id": p.a.movieID, "capabilities": tclCaps()}
	// Control: the admin reaches each of them (artwork aside: A has none,
	// and answers so after the policy).
	for _, pr := range probes {
		r := b.do(pr.method, pr.path, b.adminTok, body)
		if strings.Contains(pr.path, "artwork") {
			if r.code != 404 || !strings.Contains(string(r.body), "no_artwork") {
				t.Errorf("control %s: %d %s", pr.path, r.code, r.body)
			}
			continue
		}
		if r.code != 200 && r.code != 201 {
			t.Errorf("control %s %s: %d %s", pr.method, pr.path, r.code, r.body)
		}
	}
	h, _ := p.home(b.adminTok)
	if len(h.Items) != 2 {
		t.Fatalf("control: the admin's Home: %+v", h.Items)
	}
	for _, pr := range probes {
		r := b.do(pr.method, pr.path, kid, body)
		if r.code != 404 || strings.Contains(string(r.body), "no_artwork") {
			t.Errorf("the PG-13 profile on %s %s: %d %s", pr.method, pr.path, r.code, r.body)
		}
	}
	if h, _ := p.home(kid); len(h.Items) != 0 {
		t.Fatalf("the PG-13 profile's Home lists R titles: %+v", h.Items)
	}
	// Session routes are the caller's own: the kid cannot use the admin's.
	s, _ := p.start(b.adminTok, map[string]any{"item_id": p.a.movieID, "version_id": p.a.v1080, "capabilities": tclCaps()})
	for _, path := range []string{"/file", "/stream.mp4", "/seek", "/hls/1/index.m3u8", "/hls/1/seg-00001.m4s", "/subtitles/1"} {
		if r := b.do("GET", fmt.Sprintf("%s/playback/sessions/%s%s", base, s.SessionID, path), kid, nil); r.code != 404 {
			t.Errorf("the kid on the admin's session %s: %d", path, r.code)
		}
	}
	if r := b.do("POST", fmt.Sprintf("%s/playback/sessions/%s/events", base, s.SessionID), kid, map[string]any{"type": "stop"}); r.code != 404 {
		t.Errorf("the kid stopping the admin's session: %d", r.code)
	}
	// A 1080p profile gets the 1080p copy, and is refused the 4K by id.
	hd := profile("HD", map[string]any{"max_resolution": "1080p", "allow_transcode": true})
	got, r := p.start(hd, map[string]any{"item_id": p.a.movieID, "capabilities": tclCaps()})
	if r.code != 201 || got.Version.ID != p.a.v1080 {
		t.Fatalf("the 1080p profile: %d %s", r.code, r.body)
	}
	if _, r := p.start(hd, map[string]any{"item_id": p.a.movieID, "version_id": p.a.v4k, "capabilities": tclCaps()}); r.code != 404 {
		t.Fatalf("the 1080p profile asking for 4K: %d %s", r.code, r.body)
	}
}

// Converted streams and HLS play through the proxy, under the peer's policy.
func TestFederationConvertedAndHLSThroughTheProxy(t *testing.T) {
	t.Parallel()
	ff, err1 := osexec.LookPath("ffmpeg")
	fpb, err2 := osexec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg or ffprobe not on PATH")
	}
	p := newPeerPair(t, nil)
	withHLS(t, p.a, remux.New(ff, fpb, 2, nil))
	var epPath string
	if err := p.a.db.QueryRow(`SELECT path FROM media_versions WHERE id = ?`, p.a.episodeVerID).Scan(&epPath); err != nil {
		t.Fatal(err)
	}
	if msg, err := osexec.Command(ff, "-nostdin", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24:duration=8",
		"-f", "lavfi", "-i", "sine=duration=8", "-c:v", "libx264", "-g", "48", "-sc_threshold", "0", "-pix_fmt", "yuv420p",
		"-c:a", "ac3", "-y", epPath+".tmp.mkv").CombinedOutput(); err != nil {
		t.Skipf("fixture needs libx264 and ac3: %v %s", err, msg)
	}
	if err := os.Rename(epPath+".tmp.mkv", epPath); err != nil {
		t.Fatal(err)
	}
	if _, err := p.a.db.Exec(`UPDATE media_versions SET video_codec = 'h264', video_profile = 'high', bit_depth = 8, duration_ms = 8000 WHERE id = ?`, p.a.episodeVerID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.a.db.Exec(`INSERT INTO media_streams (version_id, stream_index, kind, codec, channels, is_default) VALUES (?, 1, 'audio', 'ac3', 2, 1)`, p.a.episodeVerID); err != nil {
		t.Fatal(err)
	}
	// Fragmented MP4: seek, then the stream, through B.
	s, r := p.start(p.b.adminTok, map[string]any{"item_id": p.a.episodeID, "capabilities": browserCaps(true)})
	if r.code != 201 || s.Decision != "audio_transcode" || s.Stream.Protocol != "fmp4" {
		t.Fatalf("converted start: %d %s", r.code, r.body)
	}
	var seek struct {
		StartMs int64 `json:"start_ms"`
		URL     string
	}
	r = p.b.do("GET", s.Stream.SeekURL+"?position_ms=0", p.b.adminTok, nil)
	r.json(t, &seek)
	if r.code != 200 || !strings.HasPrefix(seek.URL, fmt.Sprintf("/api/v1/peers/%d/playback/sessions/%s/stream.mp4?start_ms=", p.peerID, s.SessionID)) {
		t.Fatalf("seek: %d %s", r.code, r.body)
	}
	resp, err := p.get(seek.URL, p.b.adminTok)
	if err != nil {
		t.Fatal(err)
	}
	mp4, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "video/mp4" || !bytes.Contains(mp4[:min(len(mp4), 64)], []byte("ftyp")) {
		t.Fatalf("converted stream through B: %d %v %d bytes", resp.StatusCode, resp.Header, len(mp4))
	}
	// HLS: the playlist and its segments through B.
	s, r = p.start(p.b.adminTok, map[string]any{"item_id": p.a.episodeID, "capabilities": safariCaps()})
	if r.code != 201 || s.Stream.Protocol != "hls" {
		t.Fatalf("HLS start: %d %s", r.code, r.body)
	}
	r = p.b.do("GET", s.Stream.SeekURL+"?position_ms=0", p.b.adminTok, nil)
	r.json(t, &seek)
	if r.code != 200 || !strings.HasSuffix(seek.URL, "/index.m3u8") || !strings.HasPrefix(seek.URL, fmt.Sprintf("/api/v1/peers/%d/", p.peerID)) {
		t.Fatalf("HLS seek: %d %s", r.code, r.body)
	}
	var pl string
	for range 200 {
		r = p.b.do("GET", seek.URL, p.b.adminTok, nil)
		if r.code != 200 || r.hdr.Get("Content-Type") != "application/vnd.apple.mpegurl" {
			t.Fatalf("playlist through B: %d %s", r.code, r.body)
		}
		if pl = string(r.body); strings.Contains(pl, "#EXT-X-ENDLIST") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	uris, _ := segmentsOf(t, pl)
	if len(uris) < 2 {
		t.Fatalf("playlist: %s", pl)
	}
	dir := strings.TrimSuffix(seek.URL, "index.m3u8")
	for _, u := range uris {
		r := p.b.do("GET", dir+u, p.b.adminTok, nil)
		if r.code != 200 || r.hdr.Get("Content-Type") != "video/mp4" || len(r.body) == 0 {
			t.Fatalf("segment %s through B: %d %s", u, r.code, r.body)
		}
	}
	// The same policy on the peer: once converting is off for B, A refuses.
	if _, err := p.a.db.Exec(`UPDATE user_policies SET allow_transcode = 0, allow_4k_transcode = 0 WHERE user_id = (SELECT user_id FROM device_tokens WHERE id = ?)`, p.aDevice); err != nil {
		t.Fatal(err)
	}
	_, r = p.start(p.b.adminTok, map[string]any{"item_id": p.a.episodeID, "capabilities": safariCaps()})
	wantError(t, "HLS once converting is off for B", r, 403, "playback_refused")
}

// A peer token is sealed with a key file outside the database. Without that
// file the link is kept and shown as unusable, every peer route says why,
// and the admin can still unlink it; nothing is dropped and nothing crashes.
func TestFederationMissingTokenKeyKeepsTheLink(t *testing.T) {
	t.Parallel()
	p := newPeerPair(t, nil)
	keyPath := filepath.Join(fixtureDir(p.b), federation.TokenKeyFile)
	fi, err := os.Stat(keyPath)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", fi, err)
	}
	// The database is restored somewhere the key file never went.
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	p.b.srv.Peers.Key = &federation.TokenKey{Path: keyPath}

	r := p.b.do("GET", "/api/v1/admin/peers", p.b.adminTok, nil)
	var list struct {
		Peers []struct {
			ID        int64
			State     string
			LastError *string `json:"last_error"`
		}
	}
	r.json(t, &list)
	if r.code != 200 || len(list.Peers) != 1 || list.Peers[0].State != "token_unreadable" || list.Peers[0].LastError == nil ||
		!strings.Contains(*list.Peers[0].LastError, federation.TokenKeyFile) {
		t.Fatalf("B's peers without the key: %d %s", r.code, r.body)
	}
	h, _ := p.home(p.b.adminTok)
	if len(h.Peers) != 1 || h.Peers[0].State != "token_unreadable" {
		t.Fatalf("Home without the key: %+v", h.Peers)
	}
	r = p.b.do("GET", fmt.Sprintf("/api/v1/peers/%d/items/1", p.peerID), p.b.adminTok, nil)
	wantError(t, "a peer route without the key", r, 502, "peer_token_unreadable")
	var n int
	_ = p.b.db.QueryRow(`SELECT COUNT(*) FROM peers WHERE token LIKE 'sealed:v1:%'`).Scan(&n)
	if n != 1 {
		t.Fatalf("the sealed row is gone or changed: %d", n)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("reading peers created a new key file: %v", err)
	}
	// The admin can still unlink it; there is no token to tell A with.
	r = p.b.do("DELETE", fmt.Sprintf("/api/v1/admin/peers/%d", p.peerID), p.b.adminTok, nil)
	if r.code != 200 || !strings.Contains(string(r.body), `"peer_notified":false`) {
		t.Fatalf("unlink without the key: %d %s", r.code, r.body)
	}
}
