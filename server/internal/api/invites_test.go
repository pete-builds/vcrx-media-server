package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/invites"
)

// Invited devices (milestone 9, item 1). Every rule and security property of
// the invite token has a test here; the access walk (access_test.go) runs the
// whole policy walk as an invited device too.

type invited struct {
	id, userID int64
	secret     string
	token      string
}

// mint has the admin mint an invite. scope defaults: every library, no
// resolution limit, no converting, 30 days.
func (f *fixture) mint(scope map[string]any) (int64, string, resp) {
	f.t.Helper()
	body := map[string]any{"name": "Mark's TV", "expires_in_days": 30}
	if _, ok := scope["library_ids"]; !ok {
		var movies, tv int64
		_ = f.db.QueryRow(`SELECT id FROM libraries WHERE name = 'Movies'`).Scan(&movies)
		_ = f.db.QueryRow(`SELECT id FROM libraries WHERE name = 'TV'`).Scan(&tv)
		body["library_ids"] = []int64{movies, tv}
	}
	for k, v := range scope {
		body[k] = v
	}
	r := f.do("POST", "/api/v1/admin/devices", f.adminTok, body)
	if r.code != 201 {
		f.t.Fatalf("mint: %d %s", r.code, r.body)
	}
	var out struct {
		Device struct{ ID int64 }
		Invite string
	}
	r.json(f.t, &out)
	return out.Device.ID, out.Invite, r
}

func (f *fixture) redeem(secret string, hdr ...string) resp {
	f.t.Helper()
	return f.do("POST", "/api/v1/invites/redeem", "", map[string]any{"invite": secret,
		"device": map[string]string{"id": "phone-1", "name": "Pixel", "platform": "web", "client": "VCRX Web"}}, hdr...)
}

// invite mints and redeems: the device's principal and its session token.
func (f *fixture) invite(scope map[string]any) invited {
	f.t.Helper()
	id, secret, _ := f.mint(scope)
	r := f.redeem(secret)
	if r.code != 200 {
		f.t.Fatalf("redeem: %d %s", r.code, r.body)
	}
	var out struct {
		Token string
		User  struct{ ID int64 }
	}
	r.json(f.t, &out)
	return invited{id: id, userID: out.User.ID, secret: secret, token: out.Token}
}

func (f *fixture) libraryID(name string) int64 {
	f.t.Helper()
	var id int64
	if err := f.db.QueryRow(`SELECT id FROM libraries WHERE name = ?`, name).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// dbContains reports whether any text or blob in the database holds needle.
func dbContains(t *testing.T, f *fixture, needle string) bool {
	t.Helper()
	var tables []string
	queryRows(t, f, `SELECT name FROM sqlite_master WHERE type = 'table'`, func(vals []any) {
		tables = append(tables, fmt.Sprint(vals[0]))
	})
	found := false
	for _, tbl := range tables {
		queryRows(t, f, `SELECT * FROM "`+tbl+`"`, func(vals []any) {
			for _, v := range vals {
				switch x := v.(type) {
				case string:
					found = found || strings.Contains(strings.ToLower(x), needle)
				case []byte:
					found = found || bytes.Contains(bytes.ToLower(x), []byte(needle))
				}
			}
		})
	}
	return found
}

// queryRows calls each with every row's values.
func queryRows(t *testing.T, f *fixture, q string, each func([]any)) {
	t.Helper()
	rows, err := f.db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols, _ := rows.Columns()
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		each(vals)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// The secret: random, at least 128 bits, stored only as a hash, shown once.
func TestInviteSecretIsRandomHashedAndShownOnce(t *testing.T) {
	f := newFixture(t)
	id, secret, r := f.mint(nil)
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil || len(raw)*8 < 128 {
		t.Fatalf("secret %q: %d bits (%v), want at least 128", secret, len(raw)*8, err)
	}
	_, other, _ := f.mint(nil)
	if other == secret {
		t.Fatal("two invites got the same secret")
	}
	// The mint answer carries the secret, its link and a QR code of the link.
	var out struct {
		URL   string `json:"url"`
		QRPNG string `json:"qr_png"`
	}
	r.json(t, &out)
	if out.URL != "http://example.com/#/invite/"+secret {
		t.Errorf("invite link %q", out.URL)
	}
	if b, err := base64.StdEncoding.DecodeString(out.QRPNG); err != nil {
		t.Errorf("qr_png is not base64: %v", err)
	} else if _, err := png.Decode(bytes.NewReader(b)); err != nil {
		t.Errorf("qr_png is not a PNG: %v", err)
	}
	// Stored: the SHA-256 of its bytes, and nothing that holds the secret.
	var stored []byte
	if err := f.db.QueryRow(`SELECT invite_hash FROM device_tokens WHERE id = ?`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); !bytes.Equal(stored, sum[:]) {
		t.Errorf("stored hash %x, want sha256 of the secret", stored)
	}
	if dbContains(t, f, strings.ToLower(secret)) || dbContains(t, f, string(raw)) {
		t.Error("the database holds the invite secret")
	}
	// Shown once: the device list never carries it.
	if l := f.do("GET", "/api/v1/admin/devices", f.adminTok, nil); l.code != 200 || bytes.Contains(bytes.ToLower(l.body), []byte(secret)) {
		t.Errorf("device list: %d, holds the secret %v", l.code, bytes.Contains(bytes.ToLower(l.body), []byte(secret)))
	}
}

// A redeemed invite link is single-use: a replay fails, and the first device
// keeps working.
func TestInviteRedeemIsSingleUse(t *testing.T) {
	f := newFixture(t)
	d := f.invite(nil)
	var me struct {
		Kind        string `json:"kind"`
		DisplayName string `json:"display_name"`
		IsAdmin     bool   `json:"is_admin"`
	}
	r := f.do("GET", "/api/v1/me", d.token, nil)
	r.json(t, &me)
	if r.code != 200 || me.Kind != "token" || me.DisplayName != "Mark's TV" || me.IsAdmin {
		t.Fatalf("me as the device: %d %s", r.code, r.body)
	}
	wantError(t, "replayed invite", f.redeem(d.secret), 401, "invalid_invite")
	// Case, spaces and dashes do not make a used invite new.
	wantError(t, "replayed invite, reformatted", f.redeem(strings.ToUpper(d.secret[:8])+"-"+d.secret[8:]), 401, "invalid_invite")
	if r := f.do("GET", "/api/v1/me", d.token, nil); r.code != 200 {
		t.Fatalf("the first device after a replay: %d", r.code)
	}
	wantError(t, "made-up invite", f.redeem(strings.Repeat("a", 32)), 401, "invalid_invite")
	var redeemed int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM device_tokens WHERE redeemed_at IS NOT NULL`).Scan(&redeemed)
	if redeemed != 1 {
		t.Fatalf("%d redeemed tokens, want 1", redeemed)
	}
}

// Wrong invites count against the address, like wrong passwords.
func TestInviteGuessingIsRateLimited(t *testing.T) {
	f := newFixture(t)
	_, secret, _ := f.mint(nil)
	for i := range 10 {
		wantError(t, fmt.Sprintf("guess %d", i), f.redeem(fmt.Sprintf("%032d", i)), 401, "invalid_invite")
	}
	wantError(t, "the right invite after ten wrong ones", f.redeem(secret), 429, "rate_limited")
	if r := f.redeem(secret, "RemoteAddr", "192.168.1.77:4000"); r.code != 200 {
		t.Fatalf("another address: %d %s", r.code, r.body)
	}
}

// A token never widens past its scope: libraries, resolution, converting.
// And its scope cannot be widened afterwards through the user routes.
func TestInviteScopeIsEnforcedAndCannotBeWidened(t *testing.T) {
	f := newFixture(t)
	movies := f.libraryID("Movies")
	d := f.invite(map[string]any{"library_ids": []int64{movies}, "max_resolution": "1080p", "allow_transcode": false})
	var libs struct{ Libraries []struct{ Name string } }
	f.do("GET", "/api/v1/libraries", d.token, nil).json(t, &libs)
	if len(libs.Libraries) != 1 || libs.Libraries[0].Name != "Movies" {
		t.Errorf("libraries: %+v, want Movies only", libs)
	}
	wantError(t, "a show outside the scope", f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.showID), d.token, nil), 404, "not_found")
	wantError(t, "the 4K version above the scope",
		f.do("POST", "/api/v1/playback/sessions", d.token, map[string]any{"item_id": f.movieID, "version_id": f.v4k, "capabilities": tclCaps()}), 404, "not_found")
	if r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), d.token, nil); r.code != 200 || bytes.Contains(r.body, []byte("2160p")) {
		t.Errorf("the film: %d, lists the 4K file %v", r.code, bytes.Contains(r.body, []byte("2160p")))
	}
	// The 1080p file has eac3 audio, which a browser cannot play: converting
	// is outside the scope, so it is refused rather than converted.
	r := f.do("POST", "/api/v1/playback/sessions", d.token, map[string]any{"item_id": f.movieID, "capabilities": browserCaps(true)})
	wantError(t, "converting outside the scope", r, 403, "playback_refused")
	// Direct play inside the scope works.
	if r := f.do("POST", "/api/v1/playback/sessions", d.token, map[string]any{"item_id": f.movieID, "capabilities": tclCaps()}); r.code != 201 {
		t.Errorf("direct play inside the scope: %d %s", r.code, r.body)
	}
	// The admin's user routes refuse to widen it, and the device is not a user.
	tv := f.libraryID("TV")
	wantError(t, "widen grants", f.do("PUT", fmt.Sprintf("/api/v1/admin/users/%d/grants", d.userID), f.adminTok, map[string]any{"library_ids": []int64{movies, tv}}), 404, "not_found")
	wantError(t, "widen policy", f.do("PUT", fmt.Sprintf("/api/v1/admin/users/%d/policy", d.userID), f.adminTok, map[string]any{"allow_transcode": true}), 404, "not_found")
	if r := f.do("GET", "/api/v1/admin/users", f.adminTok, nil); bytes.Contains(r.body, []byte("Mark's TV")) || bytes.Contains(r.body, []byte("device-")) {
		t.Errorf("an invited device is listed as a user: %s", r.body)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.showID), d.token, nil); r.code != 404 {
		t.Errorf("after the refused widening, the show: %d", r.code)
	}
	// A scope must name a real library, a known resolution and a sane expiry.
	for _, bad := range []map[string]any{
		{"name": "x", "library_ids": []int64{}, "expires_in_days": 30},
		{"name": "x", "library_ids": []int64{999}, "expires_in_days": 30},
		{"name": "x", "library_ids": []int64{movies}, "max_resolution": "1440p", "expires_in_days": 30},
		{"name": "x", "library_ids": []int64{movies}, "expires_in_days": 0},
		{"name": "x", "library_ids": []int64{movies}, "expires_in_days": 400},
		{"name": "", "library_ids": []int64{movies}, "expires_in_days": 30},
	} {
		wantError(t, fmt.Sprintf("scope %v", bad), f.do("POST", "/api/v1/admin/devices", f.adminTok, bad), 400, "bad_request")
	}
}

// Expiry is enforced by the server on every request, and a session never
// outlives its token.
func TestInviteExpiryIsEnforcedServerSide(t *testing.T) {
	f := newFixture(t)
	d := f.invite(map[string]any{"expires_in_days": 1})
	var sessExp, tokExp int64
	_ = f.db.QueryRow(`SELECT expires_at FROM auth_sessions WHERE user_id = ?`, d.userID).Scan(&sessExp)
	_ = f.db.QueryRow(`SELECT expires_at FROM device_tokens WHERE id = ?`, d.id).Scan(&tokExp)
	if sessExp > tokExp {
		t.Errorf("session expires %d, after its token %d", sessExp, tokExp)
	}
	if r := f.do("GET", "/api/v1/me", d.token, nil); r.code != 200 {
		t.Fatalf("before expiry: %d", r.code)
	}
	// Only the token expires; the session row still says it is good.
	past := time.Now().Add(-time.Second).UnixMilli()
	if _, err := f.db.Exec(`UPDATE device_tokens SET expires_at = ? WHERE id = ?`, past, d.id); err != nil {
		t.Fatal(err)
	}
	wantError(t, "after expiry", f.do("GET", "/api/v1/me", d.token, nil), 401, "unauthorized")
	// An invite never redeemed cannot be redeemed after it expires.
	id2, secret2, _ := f.mint(nil)
	if _, err := f.db.Exec(`UPDATE device_tokens SET expires_at = ? WHERE id = ?`, past, id2); err != nil {
		t.Fatal(err)
	}
	wantError(t, "redeem after expiry", f.redeem(secret2), 401, "invalid_invite")
	var list struct {
		Devices []struct {
			ID    int64
			State string
		}
	}
	f.do("GET", "/api/v1/admin/devices", f.adminTok, nil).json(t, &list)
	for _, x := range list.Devices {
		if x.State != "expired" {
			t.Errorf("device %d: %s, want expired", x.ID, x.State)
		}
	}
}

// An expired device's stream that is already open stops within the minute.
func TestExpiredDeviceStreamIsCut(t *testing.T) {
	f := newFixture(t)
	d := f.invite(nil)
	r := f.do("POST", "/api/v1/playback/sessions", d.token, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": tclCaps()})
	var s struct {
		SessionID string `json:"session_id"`
	}
	r.json(t, &s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.srv.streams.add(s.SessionID, &cancel)
	if n := f.srv.CutExpiredDevices(context.Background()); n != 0 {
		t.Fatalf("cut %d streams before expiry", n)
	}
	if _, err := f.db.Exec(`UPDATE device_tokens SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Second).UnixMilli(), d.id); err != nil {
		t.Fatal(err)
	}
	if n := f.srv.CutExpiredDevices(context.Background()); n != 1 || ctx.Err() == nil {
		t.Fatalf("after expiry: cut %d streams, context %v", n, ctx.Err())
	}
	var msg string
	_ = f.db.QueryRow(`SELECT terminated_message FROM playback_sessions WHERE id = ?`, s.SessionID).Scan(&msg)
	if !strings.Contains(msg, "expired") {
		t.Errorf("session message %q", msg)
	}
}

// Revoking a device takes effect on its next request and on a stream it has
// open, over a real connection, through the stream registry.
func TestRevokeCutsTheNextRequestAndAnOpenStream(t *testing.T) {
	f := newFixture(t)
	d := f.invite(nil)
	other := f.invite(map[string]any{"name": "Another device"})
	// A file big enough that it cannot all sit in socket buffers.
	var path string
	_ = f.db.QueryRow(`SELECT path FROM media_versions WHERE id = ?`, f.v1080).Scan(&path)
	const size = 256 << 20
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE media_versions SET size = ? WHERE id = ?`, size, f.v1080); err != nil {
		t.Fatal(err)
	}
	r := f.do("POST", "/api/v1/playback/sessions", d.token, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": tclCaps()})
	var s struct {
		SessionID string `json:"session_id"`
		Stream    struct{ URL string }
	}
	r.json(t, &s)
	if r.code != 201 || !strings.Contains(s.Stream.URL, "session="+s.SessionID) {
		t.Fatalf("start: %d %s", r.code, r.body)
	}
	srv := httptest.NewServer(f.h)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+s.Stream.URL, nil)
	req.Header.Set("Authorization", "Bearer "+d.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		t.Fatalf("stream: %d", res.StatusCode)
	}
	if _, err := io.ReadFull(res.Body, make([]byte, 1<<20)); err != nil {
		t.Fatal(err)
	}
	var out struct {
		StreamsStopped int `json:"streams_stopped"`
	}
	rv := f.do("DELETE", fmt.Sprintf("/api/v1/admin/devices/%d", d.id), f.adminTok, nil)
	rv.json(t, &out)
	if rv.code != 200 || out.StreamsStopped != 1 {
		t.Fatalf("revoke: %d %s", rv.code, rv.body)
	}
	n, _ := io.Copy(io.Discard, res.Body)
	if n+(1<<20) >= size {
		t.Fatalf("the open stream ran to the end (%d bytes) after the revoke", n+(1<<20))
	}
	wantError(t, "the next request", f.do("GET", "/api/v1/me", d.token, nil), 401, "unauthorized")
	var term string
	_ = f.db.QueryRow(`SELECT terminated_message FROM playback_sessions WHERE id = ?`, s.SessionID).Scan(&term)
	if !strings.Contains(term, "revoked") {
		t.Errorf("session message %q", term)
	}
	// Each device on its own: the other one is untouched.
	if r := f.do("GET", "/api/v1/me", other.token, nil); r.code != 200 {
		t.Errorf("the other device after the revoke: %d", r.code)
	}
	// Signing out on the device gives the invite up.
	if r := f.do("POST", "/api/v1/auth/logout", other.token, nil); r.code != 204 {
		t.Fatalf("logout: %d", r.code)
	}
	var revokedBy string
	_ = f.db.QueryRow(`SELECT revoked_by FROM device_tokens WHERE id = ?`, other.id).Scan(&revokedBy)
	if revokedBy != "the device itself" {
		t.Errorf("after signing out, revoked_by %q", revokedBy)
	}
}

// Rule 1 and 3: a device signed in by token is its own principal. It never
// sees the profile picker, even on the trusted network with the picker on,
// can never switch into a profile (a kid's included), and cannot be added to
// the household.
func TestInvitedDeviceNeverReachesThePicker(t *testing.T) {
	f := newHouseholdFixture(t)
	f.picker(true)
	d := f.invite(nil)
	home := []string{"RemoteAddr", "192.168.1.30:5000", csrfHeader, "tv"}
	// Control: with no session at all, this network gets the picker.
	if r := f.do("GET", "/api/v1/profiles", "", nil, home...); r.code != 200 {
		t.Fatalf("the picker without a session: %d %s", r.code, r.body)
	}
	wantError(t, "the picker with a token", f.do("GET", "/api/v1/profiles", d.token, nil, home...), 403, "not_household")
	wantError(t, "a picture with a token", f.do("GET", fmt.Sprintf("/api/v1/profiles/%d/avatar", f.robin), d.token, nil, home...), 403, "not_household")
	for _, target := range []int64{f.robin, f.sam, mustUserID(t, f.fixture, "pete")} {
		wantError(t, fmt.Sprintf("switch to %d", target), f.switchTo(d.token, target, map[string]any{"pin": "2468", "password": "adminpass1"}, home...), 403, "not_household")
	}
	// The token's own cookie, too.
	_, secret, _ := f.mint(nil)
	rc := f.do("POST", "/api/v1/invites/redeem", "", map[string]any{"invite": secret, "cookie": true}, home...)
	cookie := strings.SplitN(rc.hdr.Get("Set-Cookie"), ";", 2)[0]
	if rc.code != 200 || !strings.HasPrefix(cookie, cookieName+"=") {
		t.Fatalf("redeem with a cookie: %d %s", rc.code, rc.body)
	}
	wantError(t, "the picker with a token cookie", f.do("GET", "/api/v1/profiles", "", nil, append(home, "Cookie", cookie)...), 403, "not_household")
	// The admin cannot make the device a household member.
	wantError(t, "add the device to the household", f.do("PUT", fmt.Sprintf("/api/v1/admin/profiles/%d", d.userID), f.adminTok, map[string]any{"household": true}), 404, "not_found")
	var list struct{ Profiles []struct{ ID int64 } }
	f.do("GET", "/api/v1/admin/profiles", f.adminTok, nil).json(t, &list)
	for _, p := range list.Profiles {
		if p.ID == d.userID {
			t.Error("the invited device is listed as a profile")
		}
	}
	if r := f.do("GET", "/api/v1/profiles", d.token, nil); r.code == 200 {
		t.Error("still reaches the picker")
	}
}

// Rule 2: only admins mint. Accounts, household profiles (kids' above all) and
// invited devices cannot mint an invite, approve a pairing, or list devices.
func TestOnlyAdminsMint(t *testing.T) {
	f := newHouseholdFixture(t)
	d := f.invite(nil)
	kid := tokenOf(t, f.switchTo(f.login("pete", "adminpass1"), f.robin, nil))
	sam := tokenOf(t, f.switchTo(f.login("pete", "adminpass1"), f.sam, map[string]any{"pin": "2468"}))
	_, p := f.startPairing(t)
	body := map[string]any{"name": "x", "library_ids": []int64{f.libraryID("Movies")}, "expires_in_days": 1}
	for who, tok := range map[string]string{"friend": f.friendTok, "kid": kid, "profile": sam, "device": d.token} {
		wantError(t, who+" mints", f.do("POST", "/api/v1/admin/devices", tok, body), 403, "forbidden")
		wantError(t, who+" approves", f.do("POST", "/api/v1/admin/pairings/"+p.UserCode+"/approve", tok, body), 403, "forbidden")
		wantError(t, who+" lists", f.do("GET", "/api/v1/admin/devices", tok, nil), 403, "forbidden")
		wantError(t, who+" revokes", f.do("DELETE", fmt.Sprintf("/api/v1/admin/devices/%d", d.id), tok, nil), 403, "forbidden")
	}
	// Control: the admin can.
	if r := f.do("POST", "/api/v1/admin/devices", f.adminTok, body); r.code != 201 {
		t.Fatalf("admin mints: %d %s", r.code, r.body)
	}
}

type pairStart struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	QRPNG           string `json:"qr_png"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

func (f *fixture) startPairing(t *testing.T, hdr ...string) (resp, pairStart) {
	t.Helper()
	r := f.do("POST", "/api/v1/pair", "", map[string]any{"device": map[string]string{"id": "tv-9", "name": "Den TV", "model": "Chromecast",
		"platform": "android_tv", "client": "vcrx-tv"}}, hdr...)
	var p pairStart
	if r.code == 200 {
		r.json(t, &p)
	}
	return r, p
}

func (f *fixture) poll(code string, hdr ...string) resp {
	f.t.Helper()
	return f.do("POST", "/api/v1/pair/poll", "", map[string]any{"device_code": code}, hdr...)
}

// TV pairing: the TV shows a code, the admin's phone approves it with a
// scope, and the TV's next poll gets its session, once.
func TestPairingFlow(t *testing.T) {
	f := newFixture(t)
	r, p := f.startPairing(t)
	if r.code != 200 || len(invites.NormalizeCode(p.UserCode)) != 8 || p.ExpiresIn != 600 || p.Interval != 5 {
		t.Fatalf("start: %d %s", r.code, r.body)
	}
	if p.VerificationURL != "http://example.com/#/pair/"+invites.NormalizeCode(p.UserCode) {
		t.Errorf("verification url %q", p.VerificationURL)
	}
	if b, err := base64.StdEncoding.DecodeString(p.QRPNG); err != nil {
		t.Error(err)
	} else if _, err := png.Decode(bytes.NewReader(b)); err != nil {
		t.Errorf("qr: %v", err)
	}
	if r := f.poll(p.DeviceCode); r.code != 200 || !bytes.Contains(r.body, []byte(`"status":"pending"`)) {
		t.Fatalf("poll before approval: %d %s", r.code, r.body)
	}
	// The phone sees which TV is asking, typed in any case.
	var seen struct {
		Name *string `json:"device_name"`
		IP   string
	}
	lr := f.do("GET", "/api/v1/admin/pairings/"+strings.ToLower(p.UserCode), f.adminTok, nil)
	lr.json(t, &seen)
	if lr.code != 200 || seen.Name == nil || *seen.Name != "Den TV" || seen.IP != "192.168.1.50" {
		t.Fatalf("lookup: %d %s", lr.code, lr.body)
	}
	movies := f.libraryID("Movies")
	scope := map[string]any{"library_ids": []int64{movies}, "max_resolution": "1080p", "expires_in_days": 7}
	if r := f.do("POST", "/api/v1/admin/pairings/"+p.UserCode+"/approve", f.adminTok, scope); r.code != 201 || !bytes.Contains(r.body, []byte(`"name":"Den TV"`)) {
		t.Fatalf("approve: %d %s", r.code, r.body)
	}
	// Single use: the code cannot be approved again.
	wantError(t, "approve twice", f.do("POST", "/api/v1/admin/pairings/"+p.UserCode+"/approve", f.adminTok, scope), 404, "pairing_not_found")
	pr := f.poll(p.DeviceCode)
	var got struct {
		Status string
		Token  string
	}
	pr.json(t, &got)
	if pr.code != 200 || got.Status != "approved" || got.Token == "" {
		t.Fatalf("poll after approval: %d %s", pr.code, pr.body)
	}
	var me struct{ Kind string }
	f.do("GET", "/api/v1/me", got.Token, nil).json(t, &me)
	if me.Kind != "token" {
		t.Errorf("the TV signed in as %q, want a token principal", me.Kind)
	}
	var libs struct{ Libraries []struct{ Name string } }
	f.do("GET", "/api/v1/libraries", got.Token, nil).json(t, &libs)
	if len(libs.Libraries) != 1 {
		t.Errorf("the TV's scope: %+v", libs)
	}
	// Single use: the device code gives one session, never a second.
	wantError(t, "poll after collecting", f.poll(p.DeviceCode), 404, "pairing_not_found")
	// Denied: the TV's next poll learns the code is gone.
	_, p2 := f.startPairing(t)
	if r := f.do("DELETE", "/api/v1/admin/pairings/"+p2.UserCode, f.adminTok, nil); r.code != 204 {
		t.Fatalf("deny: %d", r.code)
	}
	wantError(t, "poll after deny", f.poll(p2.DeviceCode), 404, "pairing_not_found")
}

// The pairing code is short-lived and rate limited.
func TestPairingCodeIsShortLivedAndRateLimited(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	f.srv.Pairings.Now = func() time.Time { return now }
	_, p := f.startPairing(t)
	now = now.Add(invites.PairingTTL - time.Second)
	if r := f.do("GET", "/api/v1/admin/pairings/"+p.UserCode, f.adminTok, nil); r.code != 200 {
		t.Fatalf("just before expiry: %d", r.code)
	}
	now = now.Add(2 * time.Second)
	wantError(t, "lookup after ten minutes", f.do("GET", "/api/v1/admin/pairings/"+p.UserCode, f.adminTok, nil), 404, "pairing_not_found")
	wantError(t, "poll after ten minutes", f.poll(p.DeviceCode), 404, "pairing_not_found")

	// Ten wrong codes typed by an admin lock that address out, right code or
	// not: the lookup after expiry above was the first.
	_, p3 := f.startPairing(t)
	for i := range 9 {
		wantError(t, fmt.Sprintf("wrong code %d", i), f.do("GET", "/api/v1/admin/pairings/BBBBBBB"+string("BCDFGHJKLM"[i]), f.adminTok, nil), 404, "pairing_not_found")
	}
	wantError(t, "the right code after ten wrong ones", f.do("GET", "/api/v1/admin/pairings/"+p3.UserCode, f.adminTok, nil), 429, "rate_limited")
	wantError(t, "approve after ten wrong ones", f.do("POST", "/api/v1/admin/pairings/"+p3.UserCode+"/approve", f.adminTok,
		map[string]any{"library_ids": []int64{f.libraryID("Movies")}, "expires_in_days": 1}), 429, "rate_limited")

	// Ten pairing starts per address per window.
	g := newFixture(t)
	away := []string{"RemoteAddr", "192.168.1.60:5000"}
	for i := range 10 {
		if r, _ := g.startPairing(t, away...); r.code != 200 {
			t.Fatalf("start %d: %d", i, r.code)
		}
	}
	if r, _ := g.startPairing(t, away...); r.code != 429 {
		t.Fatalf("the eleventh start: %d, want 429", r.code)
	}
	// Ten polls with unknown codes per address.
	for range 10 {
		g.poll(base64.RawURLEncoding.EncodeToString(make([]byte, 32)), "RemoteAddr", "192.168.1.61:5000")
	}
	_, p4 := g.startPairing(t)
	wantError(t, "a real poll after ten unknown ones", g.poll(p4.DeviceCode, "RemoteAddr", "192.168.1.61:5000"), 429, "rate_limited")
}

// Every new route is behind the DNS-rebinding Host check.
func TestInviteRoutesCheckTheHost(t *testing.T) {
	f := newFixture(t)
	for _, rt := range f.srv.Routes() {
		if !strings.Contains(rt.Path, "/invites") && !strings.Contains(rt.Path, "/pair") && !strings.Contains(rt.Path, "/devices") {
			continue
		}
		path := routeParam.ReplaceAllString(rt.Path, "1")
		if r := f.do(rt.Method, path, f.adminTok, map[string]any{}, "Host", "attacker.example"); r.code != http.StatusMisdirectedRequest {
			t.Errorf("%s %s with a foreign Host: %d, want 421", rt.Method, rt.Path, r.code)
		}
		// Control: the same call by IP gets past the check.
		if r := f.do(rt.Method, path, f.adminTok, map[string]any{}, "Host", "192.168.1.10:8096"); r.code == http.StatusMisdirectedRequest {
			t.Errorf("%s %s by IP: 421", rt.Method, rt.Path)
		}
	}
}

// Redeeming into a cookie is a cookie write: it needs the client header, and
// a refused attempt leaves the invite unspent.
func TestRedeemCookieNeedsTheClientHeader(t *testing.T) {
	f := newFixture(t)
	_, secret, _ := f.mint(nil)
	body := map[string]any{"invite": secret, "cookie": true}
	r := f.do("POST", "/api/v1/invites/redeem", "", body)
	wantError(t, "cookie redeem without the header", r, 403, "csrf")
	if r.hdr.Get("Set-Cookie") != "" {
		t.Error("a refused redeem set a cookie")
	}
	r = f.do("POST", "/api/v1/invites/redeem", "", body, csrfHeader, "web")
	if r.code != 200 || !strings.HasPrefix(r.hdr.Get("Set-Cookie"), cookieName+"=") {
		t.Fatalf("cookie redeem with the header: %d %s", r.code, r.body)
	}
	// The cookie session follows the cookie rules too.
	cookie := strings.SplitN(r.hdr.Get("Set-Cookie"), ";", 2)[0]
	wantError(t, "a cookie write without the header", f.do("PUT", fmt.Sprintf("/api/v1/items/%d/watched", f.movieID), "", map[string]any{"played": true}, "Cookie", cookie), 403, "csrf")
}

// Rule 5: every playback row records who played, and Pete can revoke a
// friend's device from Activity.
func TestPlaybackRecordsThePrincipal(t *testing.T) {
	f := newHouseholdFixture(t)
	d := f.invite(map[string]any{"name": "Mark's TV"})
	sam := tokenOf(t, f.switchTo(f.login("pete", "adminpass1"), f.sam, map[string]any{"pin": "2468"}))
	play := func(tok string) string {
		t.Helper()
		r := f.do("POST", "/api/v1/playback/sessions", tok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": tclCaps()})
		var s struct {
			SessionID string `json:"session_id"`
		}
		r.json(t, &s)
		if r.code != 201 {
			t.Fatalf("play: %d %s", r.code, r.body)
		}
		for _, ev := range []map[string]any{{"type": "start", "position_ms": 0}, {"type": "progress", "position_ms": 60000}} {
			if r := f.do("POST", "/api/v1/playback/sessions/"+s.SessionID+"/events", tok, ev); r.code != 204 {
				t.Fatalf("event: %d %s", r.code, r.body)
			}
		}
		return s.SessionID
	}
	want := map[string][2]any{play(f.friendTok): {"user", nil}, play(sam): {"profile", nil}, play(d.token): {"token", d.id}}
	type row struct {
		ID            string `json:"id"`
		Username      string `json:"username"`
		PrincipalKind string `json:"principal_kind"`
		TokenID       *int64 `json:"token_id"`
		TokenActive   bool   `json:"token_active"`
	}
	var now struct{ Sessions []row }
	f.do("GET", "/api/v1/admin/activity/now", f.adminTok, nil).json(t, &now)
	if len(now.Sessions) != 3 {
		t.Fatalf("now playing: %+v", now)
	}
	for _, x := range now.Sessions {
		w := want[x.ID]
		var tok any
		if x.TokenID != nil {
			tok = *x.TokenID
		}
		if x.PrincipalKind != w[0] || fmt.Sprint(tok) != fmt.Sprint(w[1]) {
			t.Errorf("session %s by %s: %s %v, want %v %v", x.ID, x.Username, x.PrincipalKind, tok, w[0], w[1])
		}
		if x.PrincipalKind == "token" && (x.Username != "Mark's TV" || !x.TokenActive) {
			t.Errorf("the device's row: %+v", x)
		}
	}
	// Revoke from Activity: the row's token id is the device to revoke. Its
	// stream leaves Now Playing and its history row says it was revoked.
	if r := f.do("DELETE", fmt.Sprintf("/api/v1/admin/devices/%d", d.id), f.adminTok, nil); r.code != 200 {
		t.Fatalf("revoke: %d", r.code)
	}
	f.do("GET", "/api/v1/admin/activity/now", f.adminTok, nil).json(t, &now)
	if len(now.Sessions) != 2 {
		t.Errorf("now playing after the revoke: %+v", now)
	}
	var hist struct {
		Sessions []struct {
			row
			TerminatedMessage string `json:"terminated_message"`
		}
	}
	f.do("GET", "/api/v1/admin/activity/history", f.adminTok, nil).json(t, &hist)
	if len(hist.Sessions) != 1 || hist.Sessions[0].PrincipalKind != "token" || hist.Sessions[0].TokenActive ||
		!strings.Contains(hist.Sessions[0].TerminatedMessage, "revoked") {
		t.Errorf("history after the revoke: %+v", hist)
	}
}

// The design constraint (PLAN.md milestone 9): stream to invited devices,
// never hand them a whole file to keep. This walks every GET and HEAD route
// as an invited device, outside any playback session, with and without a
// Range covering the whole file, and fails if any of them serves a media file
// whole or offers it as a download. A route added later is walked too.
func TestNoTokenRouteServesAWholeFile(t *testing.T) {
	f := newFixture(t)
	d := f.invite(map[string]any{"allow_transcode": true})
	// A local trailer beside the film: extras are files too.
	trailer := filepath.Join(f.root, "Film 2001 1080p", "Trailers", "Teaser.mkv")
	if err := os.MkdirAll(filepath.Dir(trailer), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trailer, content(4000), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := f.db.Exec(`INSERT INTO item_extras (item_id, library_id, kind, title, path, rel_path, size, mtime, container, duration_ms,
			video_codec, resolution, audio_streams, probe_status, added_at, updated_at)
		VALUES (?, (SELECT library_id FROM items WHERE id = ?), 'trailer', 'Teaser', ?, 'Film 2001 1080p/Trailers/Teaser.mkv', 4000, 0, 'mkv',
			90000, 'hevc', '1080p', '[{"index":1,"codec":"eac3","channels":2,"default":true}]', 'ok', 0, 0)`, f.movieID, f.movieID, trailer)
	if err != nil {
		t.Fatal(err)
	}
	extraID, _ := res.LastInsertId()
	// Control: the household's own account gets the trailer whole, so the
	// walk below would see it if a route handed it to the device.
	if r := f.do("GET", fmt.Sprintf("/api/v1/extras/%d/file", extraID), f.friendTok, nil); r.code != 200 || !bytes.Equal(r.body, content(4000)) {
		t.Fatalf("a friend's account, whole trailer: %d", r.code)
	}
	if got := wholeFileRoutes(t, f, f.srv.Routes(), d.token); len(got) > 0 {
		t.Fatalf("routes serving an invited device a whole file: %v", got)
	}
	// Control: a download route added to the table is caught by name.
	dl := route{"GET", "/api/v1/versions/{version_id}/download", signedIn, func(w http.ResponseWriter, r *http.Request) {
		var p string
		_ = f.db.QueryRow(`SELECT path FROM media_versions WHERE id = ?`, r.PathValue("version_id")).Scan(&p)
		w.Header().Set("Content-Disposition", "attachment")
		http.ServeFile(w, r, p)
	}}
	if got := wholeFileRoutes(t, f, append(f.srv.Routes(), dl), d.token); len(got) != 1 || got[0] != "GET /api/v1/versions/{version_id}/download" {
		t.Fatalf("the control download route was not caught: %v", got)
	}
	// Control: the household's own devices may still download.
	if r := f.do("GET", fmt.Sprintf("/api/v1/versions/%d/file", f.v1080), f.friendTok, nil); r.code != 200 || !bytes.Equal(r.body, content(10_000)) {
		t.Fatalf("a friend's account, whole file: %d", r.code)
	}
	// And inside its own playback session the device streams.
	r := f.do("POST", "/api/v1/playback/sessions", d.token, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": tclCaps()})
	var s struct {
		SessionID string `json:"session_id"`
		Stream    struct{ URL string }
	}
	r.json(t, &s)
	if sr := f.do("GET", s.Stream.URL, d.token, nil, "Range", "bytes=0-99"); sr.code != 206 {
		t.Fatalf("streaming in its session: %d %s", sr.code, sr.body)
	}
	// A trailer plays inside its own session too, which records the device.
	xr := f.do("POST", "/api/v1/playback/sessions", d.token, map[string]any{"extra_id": extraID, "capabilities": tclCaps()})
	var xs struct {
		SessionID string `json:"session_id"`
		Stream    struct{ URL string }
	}
	xr.json(t, &xs)
	if sr := f.do("GET", xs.Stream.URL, d.token, nil, "Range", "bytes=0-99"); xr.code != 201 || sr.code != 206 {
		t.Fatalf("the trailer in its session: start %d %s, stream %d", xr.code, xr.body, sr.code)
	}
	var kind string
	var tokenID int64
	_ = f.db.QueryRow(`SELECT principal_kind, COALESCE(token_id, 0) FROM playback_sessions WHERE id = ?`, xs.SessionID).Scan(&kind, &tokenID)
	if kind != "token" || tokenID != d.id {
		t.Errorf("the trailer's playback row: %s %d, want token %d", kind, tokenID, d.id)
	}
	// Someone else's session, or one that ended, does not count.
	fr := f.do("POST", "/api/v1/playback/sessions", f.friendTok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": tclCaps()})
	var fs struct {
		SessionID string `json:"session_id"`
	}
	fr.json(t, &fs)
	wantError(t, "a friend's session", f.do("GET", fmt.Sprintf("/api/v1/versions/%d/file?session=%s", f.v1080, fs.SessionID), d.token, nil), 403, "stream_only")
	f.do("POST", "/api/v1/playback/sessions/"+s.SessionID+"/events", d.token, map[string]any{"type": "stop", "position_ms": 0})
	wantError(t, "an ended session", f.do("GET", s.Stream.URL, d.token, nil), 403, "stream_only")
}

// wholeFileRoutes calls every GET and HEAD route as tok and names those that
// answer with a whole media file or a download disposition.
func wholeFileRoutes(t *testing.T, f *fixture, routes []route, tok string) []string {
	t.Helper()
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.Handle(rt.Method+" "+rt.Path, f.srv.gate(rt))
	}
	h := f.srv.checkHost(mux)
	var sub int64
	_ = f.db.QueryRow(`SELECT id FROM media_streams WHERE kind = 'subtitle' LIMIT 1`).Scan(&sub)
	// An open session of the device's own, so session routes are reached too.
	r := f.do("POST", "/api/v1/playback/sessions", tok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": tclCaps()})
	var s struct {
		SessionID string `json:"session_id"`
	}
	r.json(t, &s)
	var extra int64
	_ = f.db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM item_extras`).Scan(&extra)
	fill := map[string]string{"version_id": fmt.Sprint(f.v1080), "item_id": fmt.Sprint(f.movieID), "kind": "poster",
		"stream_id": fmt.Sprint(sub), "session_id": s.SessionID, "library_id": "1", "run": "1", "segment": "seg-00001.m4s",
		"extra_id": fmt.Sprint(extra)}
	// Every media file the library holds, versions and extras alike.
	var wholes [][]byte
	queryRows(t, f, `SELECT path FROM media_versions UNION ALL SELECT path FROM item_extras`, func(vals []any) {
		if b, err := os.ReadFile(fmt.Sprint(vals[0])); err == nil {
			wholes = append(wholes, b)
		}
	})
	var bad []string
	for _, rt := range routes {
		if rt.Method != "GET" && rt.Method != "HEAD" {
			continue
		}
		path := routeParam.ReplaceAllStringFunc(rt.Path, func(p string) string {
			if v, ok := fill[strings.Trim(p, "{}")]; ok {
				return v
			}
			return "1"
		})
		for _, rng := range []string{"", "bytes=0-", "bytes=0-9999"} {
			req := httptest.NewRequest(rt.Method, path, nil)
			req.RemoteAddr = "192.168.1.50:40000"
			req.Header.Set("Authorization", "Bearer "+tok)
			if rng != "" {
				req.Header.Set("Range", rng)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			ok := rec.Code == 200 || rec.Code == 206
			full := false
			for _, whole := range wholes {
				full = full || bytes.Equal(rec.Body.Bytes(), whole) || (rt.Method == "HEAD" && rec.Header().Get("Content-Length") == fmt.Sprint(len(whole)))
			}
			if ok && (full || strings.Contains(strings.ToLower(rec.Header().Get("Content-Disposition")), "attachment")) {
				bad = append(bad, rt.Method+" "+rt.Path)
				break
			}
		}
	}
	return bad
}
