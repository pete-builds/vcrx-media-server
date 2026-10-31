package api

import (
	"bytes"
	"fmt"
	"image"
	pngenc "image/png"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/household"
)

// householdFixture is the API fixture with profiles on: pete (admin, in the
// household from the start), Robin (a kid's profile, no PIN) and Sam (a
// managed profile with the PIN 2468). The Film is rated PG; Night Film, R.
type householdFixture struct {
	*fixture
	robin, sam int64
	rMovie     int64
}

func newHouseholdFixture(t *testing.T) *householdFixture {
	t.Helper()
	f := newFixture(t)
	f.srv.Household = &household.Store{DB: f.db, AvatarDir: filepath.Join(t.TempDir(), "avatars")}
	f.srv.PINs = household.NewLimiter(household.PINWindow)
	f.h = f.srv.Handler()
	hf := &householdFixture{fixture: f}
	if _, err := f.db.Exec(`UPDATE items SET content_rating = 'PG' WHERE id = ?`, f.movieID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	res, err := f.db.Exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, year, content_rating, added_at, updated_at)
		SELECT library_id, 'movie', 'night|2003', 'Night Film', 'Night Film', 2003, 'R', ?, ? FROM items WHERE id = ?`, now, now, f.movieID)
	if err != nil {
		t.Fatal(err)
	}
	hf.rMovie, _ = res.LastInsertId()
	if _, err := f.db.Exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, container, duration_ms,
			video_codec, width, height, resolution, probe_status, added_at, updated_at)
		SELECT ?, library_id, path || '.night', rel_path || '.night', size, 0, 'mkv', duration_ms, 'h264', 1920, 1080, '1080p', 'ok', ?, ?
		FROM media_versions WHERE id = ?`, hf.rMovie, now, now, f.v1080); err != nil {
		t.Fatal(err)
	}
	create := func(body map[string]any) int64 {
		r := f.do("POST", "/api/v1/admin/profiles", f.adminTok, body)
		if r.code != 201 {
			t.Fatalf("create profile %v: %d %s", body, r.code, r.body)
		}
		var p struct{ ID int64 }
		r.json(t, &p)
		return p.ID
	}
	hf.robin = create(map[string]any{"name": "Robin", "kid": true})
	hf.sam = create(map[string]any{"name": "Sam", "pin": "2468"})
	return hf
}

func (f *householdFixture) switchTo(token string, id int64, body map[string]any, hdr ...string) resp {
	f.t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	body["user_id"] = id
	return f.do("POST", "/api/v1/auth/switch", token, body, hdr...)
}

func tokenOf(t *testing.T, r resp) string {
	t.Helper()
	if r.code != 200 {
		t.Fatalf("switch: %d %s", r.code, r.body)
	}
	var out struct {
		Token string
		User  struct{ Username string }
	}
	r.json(t, &out)
	return out.Token
}

func wantError(t *testing.T, what string, r resp, code int, errCode string) {
	t.Helper()
	if r.code != code || !strings.Contains(string(r.body), `"error":"`+errCode+`"`) {
		t.Fatalf("%s: %d %s, want %d %s", what, r.code, r.body, code, errCode)
	}
}

func TestProfilesListAndSwitch(t *testing.T) {
	f := newHouseholdFixture(t)
	var list struct {
		Profiles []struct {
			ID    int64
			Name  string
			Needs string
		}
		CurrentID *int64 `json:"current_id"`
	}
	f.do("GET", "/api/v1/profiles", f.adminTok, nil).json(t, &list)
	needs := map[string]string{}
	for _, p := range list.Profiles {
		needs[p.Name] = p.Needs
	}
	if len(list.Profiles) != 3 || needs["pete"] != "none" || needs["Robin"] != "none" || needs["Sam"] != "pin" ||
		list.CurrentID == nil || list.Profiles[0].Name != "pete" {
		t.Fatalf("profiles as pete: %+v needs %v", list, needs)
	}
	// A friend outside the household sees no household and cannot switch.
	wantError(t, "friend lists", f.do("GET", "/api/v1/profiles", f.friendTok, nil), 403, "not_household")
	wantError(t, "friend switches", f.switchTo(f.friendTok, f.robin, nil), 403, "not_household")
	wantError(t, "switch to a non-member", f.switchTo(f.adminTok, mustUserID(t, f.fixture, "friend"), nil), 404, "not_found")
	wantError(t, "no session, picker off", f.do("GET", "/api/v1/profiles", "", nil), 401, "unauthorized")

	// Sam has a PIN: none, then a wrong one, are refused.
	wantError(t, "no PIN", f.switchTo(f.adminTok, f.sam, nil), 401, "pin_required")
	wantError(t, "wrong PIN", f.switchTo(f.adminTok, f.sam, map[string]any{"pin": "1357"}), 401, "wrong_pin")
	samTok := tokenOf(t, f.switchTo(f.adminTok, f.sam, map[string]any{"pin": "2468"}))
	var me struct{ Username string }
	f.do("GET", "/api/v1/me", samTok, nil).json(t, &me)
	if me.Username != "Sam" {
		t.Fatalf("after switching: signed in as %q", me.Username)
	}
	// The device left pete's profile: that token is gone.
	if r := f.do("GET", "/api/v1/me", f.adminTok, nil); r.code != 401 {
		t.Fatalf("the old session still works: %d", r.code)
	}
	// Sam to Robin needs nothing; Robin back to pete needs pete's credential
	// even though pete has no PIN.
	robinTok := tokenOf(t, f.switchTo(samTok, f.robin, nil))
	petesID := mustUserID(t, f.fixture, "pete")
	wantError(t, "kid to admin, nothing offered", f.switchTo(robinTok, petesID, nil), 401, "credential_required")
	wantError(t, "kid to admin, wrong password", f.switchTo(robinTok, petesID, map[string]any{"password": "guess-guess"}), 401, "wrong_pin")
	peteTok := tokenOf(t, f.switchTo(robinTok, petesID, map[string]any{"password": "adminpass1"}))
	// Admin to admin's own profile, and to a kid, need nothing.
	robinTok = tokenOf(t, f.switchTo(peteTok, f.robin, nil))
	if r := f.do("GET", "/api/v1/admin/users", robinTok, nil); r.code != 403 {
		t.Fatalf("a kid's session reached an admin route: %d", r.code)
	}
}

func mustUserID(t *testing.T, f *fixture, username string) int64 {
	t.Helper()
	var id int64
	if err := f.db.QueryRow(`SELECT id FROM users WHERE username = ?`, username).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Wrong PINs are limited per device (5) and per profile in the client's scope
// (25 shared by every device at home, 5 away), and the right PIN gives its
// own slot back. One device guessing locks only itself; the right PIN still
// works on every other device until the whole home's allowance is spent.
func TestPINRateLimit(t *testing.T) {
	f := newHouseholdFixture(t)
	tok := f.adminTok
	wrong := func(addr string) resp {
		return f.switchTo(tok, f.sam, map[string]any{"pin": "0000"}, "RemoteAddr", addr+":4000")
	}
	right := func(addr string) resp {
		return f.switchTo(tok, f.sam, map[string]any{"pin": "2468"}, "RemoteAddr", addr+":4000")
	}
	// The right PIN refunds its slot: four wrong, the right one, then a fifth
	// wrong one is still checked rather than refused.
	for range 4 {
		wantError(t, "wrong PIN", wrong("192.168.1.10"), 401, "wrong_pin")
	}
	tok = tokenOf(t, right("192.168.1.10"))
	tok = tokenOf(t, f.switchTo(tok, mustUserID(t, f.fixture, "pete"), map[string]any{"password": "adminpass1"}))
	wantError(t, "fifth wrong PIN after a right one", wrong("192.168.1.10"), 401, "wrong_pin")
	r := right("192.168.1.10")
	if r.code != 429 || r.hdr.Get("Retry-After") == "" {
		t.Fatalf("right PIN on a device with five wrong: %d %s", r.code, r.body)
	}
	// Another device at home is not locked by the first one.
	tok = tokenOf(t, right("192.168.1.11"))
	tok = tokenOf(t, f.switchTo(tok, mustUserID(t, f.fixture, "pete"), map[string]any{"password": "adminpass1"}))
	// Four more devices use up the home's 25 for the profile.
	for d := 12; d < 16; d++ {
		for range 5 {
			wantError(t, "filling the LAN bucket", wrong(fmt.Sprintf("192.168.1.%d", d)), 401, "wrong_pin")
		}
	}
	wantError(t, "fresh LAN device, home allowance spent", right("192.168.1.30"), 429, "rate_limited")
	// From away the profile's away bucket is untouched.
	tok = tokenOf(t, right("203.0.113.9"))
	// An admin setting a new PIN clears the profile's lock.
	tok = tokenOf(t, f.switchTo(tok, mustUserID(t, f.fixture, "pete"), map[string]any{"password": "adminpass1"}))
	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/profiles/%d/pin", f.sam), tok, map[string]any{"pin": "97531"}); r.code != 204 {
		t.Fatalf("set PIN: %d %s", r.code, r.body)
	}
	tokenOf(t, f.switchTo(tok, f.sam, map[string]any{"pin": "97531"}, "RemoteAddr", "192.168.1.31:4000"))
}

// A kid failing an admin's PIN on the shared TV cannot keep the admin out: the
// admin's password counts against the sign-in limiter, not the PIN buckets.
func TestAdminPasswordGetsPastAFullPINBucket(t *testing.T) {
	f := newHouseholdFixture(t)
	pete := mustUserID(t, f.fixture, "pete")
	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/profiles/%d/pin", pete), f.adminTok, map[string]any{"pin": "8080"}); r.code != 204 {
		t.Fatal(r.code)
	}
	tv := []string{"RemoteAddr", "192.168.1.50:4000"}
	kid := tokenOf(t, f.switchTo(f.adminTok, f.robin, nil, tv...))
	for range 5 {
		wantError(t, "kid guesses", f.switchTo(kid, pete, map[string]any{"pin": "1111"}, tv...), 401, "wrong_pin")
	}
	wantError(t, "the right PIN on the TV", f.switchTo(kid, pete, map[string]any{"pin": "8080"}, tv...), 429, "rate_limited")
	tokenOf(t, f.switchTo(kid, pete, map[string]any{"password": "adminpass1"}, tv...))
}

// Concurrent wrong PINs: the limit holds before the hash, so no more PINs are
// hashed than the buckets allow, however many arrive at once.
func TestPINLimitHoldsUnderConcurrency(t *testing.T) {
	f := newHouseholdFixture(t)
	burst := func(addr func(i int) string, n int) int64 {
		before := household.HashChecks()
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				f.switchTo(f.adminTok, f.sam, map[string]any{"pin": "0000"}, "RemoteAddr", addr(i))
			}()
		}
		wg.Wait()
		return household.HashChecks() - before
	}
	if n := burst(func(int) string { return "192.168.1.60:4000" }, 24); n != household.PINAddressLimit {
		t.Fatalf("24 concurrent wrong PINs from one device hashed %d, want %d", n, household.PINAddressLimit)
	}
	// Wrong passwords for an admin, from a kid's session on one device: the
	// sign-in limiter's address bucket (10 in this fixture) holds too.
	pete := mustUserID(t, f.fixture, "pete")
	kid := tokenOf(t, f.switchTo(f.adminTok, f.robin, nil, "RemoteAddr", "192.168.1.61:4000"))
	before := household.HashChecks()
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.switchTo(kid, pete, map[string]any{"password": "guess-guess"}, "RemoteAddr", "192.168.1.62:4000")
		}()
	}
	wg.Wait()
	if n := household.HashChecks() - before; n != 10 {
		t.Fatalf("24 concurrent wrong passwords from one device hashed %d, want 10", n)
	}
	f.adminTok = tokenOf(t, f.switchTo(kid, pete, map[string]any{"password": "adminpass1"}, "RemoteAddr", "192.168.1.63:4000"))
	// From many devices the profile's LAN bucket caps the total (5 used above).
	if n := burst(func(i int) string { return fmt.Sprintf("192.168.1.%d:4000", 100+i/2) }, 48); n != household.PINLANProfileLimit-household.PINAddressLimit {
		t.Fatalf("48 concurrent wrong PINs from 24 devices hashed %d, want %d", n, household.PINLANProfileLimit-household.PINAddressLimit)
	}
}

// Each profile has its own watched state and Continue Watching.
func TestWatchStateIsPerProfile(t *testing.T) {
	f := newHouseholdFixture(t)
	path := fmt.Sprintf("/api/v1/items/%d/watched", f.movieID)
	if r := f.do("PUT", path, f.adminTok, map[string]any{"played": true}); r.code != 204 {
		t.Fatalf("pete marks watched: %d %s", r.code, r.body)
	}
	played := func(tok string) bool {
		var item struct{ Watch struct{ Played bool } }
		r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), tok, nil)
		if r.code != 200 {
			t.Fatalf("item: %d %s", r.code, r.body)
		}
		r.json(t, &item)
		return item.Watch.Played
	}
	if !played(f.adminTok) {
		t.Fatal("control: pete's own watched mark is missing")
	}
	robinTok := tokenOf(t, f.switchTo(f.adminTok, f.robin, nil))
	if played(robinTok) {
		t.Fatal("Robin sees pete's watched mark")
	}
	if r := f.do("PUT", path, robinTok, map[string]any{"played": true}); r.code != 204 {
		t.Fatal(r.code)
	}
	if r := f.do("PUT", path, robinTok, map[string]any{"played": false}); r.code != 204 {
		t.Fatal(r.code)
	}
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM watch_state WHERE item_id = ? AND played = 1`, f.movieID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("Robin's unmark touched pete's row: %d played rows, %v", n, err)
	}
}

// A kid's profile sees nothing above its ceiling: not in lists, not by search,
// not by id. The admin sees both films.
func TestKidProfileCeiling(t *testing.T) {
	f := newHouseholdFixture(t)
	lib := func(tok string) string {
		var libs struct{ Libraries []struct{ ID int64 } }
		f.do("GET", "/api/v1/libraries", tok, nil).json(t, &libs)
		var all strings.Builder
		for _, l := range libs.Libraries {
			all.Write(f.do("GET", fmt.Sprintf("/api/v1/libraries/%d/items", l.ID), tok, nil).body)
		}
		return all.String()
	}
	if got := lib(f.adminTok); !strings.Contains(got, "Night Film") || !strings.Contains(got, "The Film") {
		t.Fatalf("control: the admin does not see both films: %s", got)
	}
	robinTok := tokenOf(t, f.switchTo(f.adminTok, f.robin, nil))
	got := lib(robinTok)
	if strings.Contains(got, "Night Film") || !strings.Contains(got, "The Film") {
		t.Fatalf("Robin (PG) library: %s", got)
	}
	if r := f.do("GET", "/api/v1/search?q=film", robinTok, nil); strings.Contains(string(r.body), "Night Film") || !strings.Contains(string(r.body), "The Film") {
		t.Fatalf("Robin search: %s", r.body)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.rMovie), robinTok, nil); r.code != 404 {
		t.Fatalf("Robin opens the R film by id: %d", r.code)
	}
	// Raising the ceiling to R shows it.
	pete := tokenOf(t, f.switchTo(robinTok, mustUserID(t, f.fixture, "pete"), map[string]any{"password": "adminpass1"}))
	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/profiles/%d", f.robin), pete, map[string]any{"max_content_rating": "R"}); r.code != 204 {
		t.Fatalf("raise ceiling: %d %s", r.code, r.body)
	}
	robinTok = tokenOf(t, f.switchTo(pete, f.robin, nil))
	if r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.rMovie), robinTok, nil); r.code != 200 {
		t.Fatalf("Robin at R opens the R film: %d", r.code)
	}
}

func (f *householdFixture) picker(on bool) {
	p, _ := netip.ParsePrefix("192.168.1.0/24")
	f.srv.TrustedNetworks = []netip.Prefix{p}
	f.srv.AutoSignInUser = "pete"
	f.srv.ProfilePicker = on
	f.h = f.srv.Handler()
}

// With auto_sign_in_picker, a trusted network lands on the picker rather than
// on auto_sign_in_user, and the picker counts as restricted: an admin needs a
// credential there. Without it, today's automatic sign-in is unchanged.
func TestAutoSignInLandsOnThePicker(t *testing.T) {
	f := newHouseholdFixture(t)
	home := []string{"RemoteAddr", "192.168.1.30:5000", csrfHeader, "tv"}
	dev := map[string]any{"device": map[string]string{"id": "tv-1", "name": "Living room", "platform": "android_tv", "client": "vcrx-tv", "client_version": "1"}}

	f.picker(false)
	if r := f.do("GET", "/api/v1/server", "", nil, home...); !strings.Contains(string(r.body), `"profile_picker":false`) {
		t.Fatalf("server info, picker off: %s", r.body)
	}
	if r := f.do("POST", "/api/v1/auth/auto", "", dev, home...); r.code != 200 {
		t.Fatalf("picker off: auto sign-in %d %s", r.code, r.body)
	}
	wantError(t, "picker off, no session", f.do("GET", "/api/v1/profiles", "", nil, home...), 401, "unauthorized")

	f.picker(true)
	if r := f.do("GET", "/api/v1/server", "", nil, home...); !strings.Contains(string(r.body), `"profile_picker":true`) {
		t.Fatalf("server info, picker on: %s", r.body)
	}
	wantError(t, "auto sign-in with the picker on", f.do("POST", "/api/v1/auth/auto", "", dev, home...), 409, "choose_profile")
	var list struct {
		Profiles []struct {
			Name, Needs string
		}
		CurrentID *int64 `json:"current_id"`
		FocusID   *int64 `json:"focus_id"`
	}
	r := f.do("GET", "/api/v1/profiles", "", nil, home...)
	if r.code != 200 {
		t.Fatalf("picker list: %d %s", r.code, r.body)
	}
	r.json(t, &list)
	if len(list.Profiles) != 3 || list.CurrentID != nil || list.FocusID == nil || *list.FocusID != mustUserID(t, f.fixture, "pete") ||
		list.Profiles[0].Needs != "password" {
		t.Fatalf("picker list: %+v", list)
	}
	// Off the trusted network there is no picker.
	wantError(t, "picker from outside", f.do("GET", "/api/v1/profiles", "", nil, "RemoteAddr", "192.168.0.30:5000"), 401, "unauthorized")
	wantError(t, "switch from outside", f.switchTo("", f.robin, nil, "RemoteAddr", "192.168.0.30:5000", csrfHeader, "tv"), 401, "unauthorized")
	// A kid's profile needs nothing; the admin needs a credential.
	robinTok := tokenOf(t, f.switchTo("", f.robin, dev, home...))
	if robinTok == "" {
		t.Fatal("no token")
	}
	wantError(t, "picker to admin", f.switchTo("", mustUserID(t, f.fixture, "pete"), nil, home...), 401, "credential_required")
	tokenOf(t, f.switchTo("", mustUserID(t, f.fixture, "pete"), map[string]any{"password": "adminpass1"}, home...))
	// A stale cookie does not hide the picker.
	if r := f.do("GET", "/api/v1/profiles", "", nil, append(home, "Cookie", cookieName+"=expired-token")...); r.code != 200 {
		t.Fatalf("picker with a stale cookie: %d %s", r.code, r.body)
	}
}

// The switch route keeps the CSRF rule: a cookie session must send the client
// header, and so must a picker request with no session at all, since a
// cross-site form could otherwise plant a profile's session cookie.
func TestSwitchCSRF(t *testing.T) {
	f := newHouseholdFixture(t)
	f.picker(true)
	r := f.do("POST", "/api/v1/auth/login", "", map[string]any{"username": "pete", "password": "adminpass1", "cookie": true})
	cookie := strings.SplitN(r.hdr.Get("Set-Cookie"), ";", 2)[0]
	call := func(cookie string, hdr ...string) *httptest.ResponseRecorder {
		t.Helper()
		body := fmt.Sprintf(`{"user_id": %d, "cookie": true}`, f.robin)
		req := httptest.NewRequest("POST", "/api/v1/auth/switch", strings.NewReader(body))
		req.RemoteAddr = "192.168.1.40:5555"
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		for i := 0; i+1 < len(hdr); i += 2 {
			req.Header.Set(hdr[i], hdr[i+1])
		}
		w := httptest.NewRecorder()
		f.h.ServeHTTP(w, req)
		return w
	}
	if w := call(cookie); w.Code != 403 || !strings.Contains(w.Body.String(), "csrf") {
		t.Fatalf("cookie switch without the header: %d %s", w.Code, w.Body.String())
	}
	if w := call(""); w.Code != 403 || !strings.Contains(w.Body.String(), "csrf") {
		t.Fatalf("picker switch without the header: %d %s", w.Code, w.Body.String())
	}
	w := call(cookie, csrfHeader, "web")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Set-Cookie"), cookieName+"=") {
		t.Fatalf("cookie switch with the header: %d %s", w.Code, w.Body.String())
	}
	// Bearer clients need no header.
	if r := f.switchTo(f.adminTok, f.robin, nil); r.code != 200 {
		t.Fatalf("bearer switch: %d %s", r.code, r.body)
	}
}

func TestAdminProfilesAndAvatar(t *testing.T) {
	f := newHouseholdFixture(t)
	// Friends join the household and become kids through the admin API.
	friend := mustUserID(t, f.fixture, "friend")
	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/profiles/%d", friend), f.adminTok, map[string]any{"household": true}); r.code != 204 {
		t.Fatalf("join: %d %s", r.code, r.body)
	}
	if r := f.do("GET", "/api/v1/profiles", f.friendTok, nil); r.code != 200 {
		t.Fatalf("new member lists: %d %s", r.code, r.body)
	}
	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/profiles/%d", mustUserID(t, f.fixture, "pete")), f.adminTok, map[string]any{"kid": true}); r.code != 400 {
		t.Fatalf("admin as kid: %d", r.code)
	}
	for _, bad := range []string{"12", "abcd", "123456789"} {
		if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/profiles/%d/pin", f.robin), f.adminTok, map[string]any{"pin": bad}); r.code != 400 {
			t.Fatalf("PIN %q: %d", bad, r.code)
		}
	}
	if r := f.do("DELETE", fmt.Sprintf("/api/v1/admin/profiles/%d/pin", f.sam), f.adminTok, nil); r.code != 204 {
		t.Fatalf("clear PIN: %d", r.code)
	}
	tokenOf(t, f.switchTo(f.adminTok, f.sam, nil))
	// Non-admins cannot manage profiles.
	if r := f.do("GET", "/api/v1/admin/profiles", f.friendTok, nil); r.code != 403 {
		t.Fatalf("friend lists admin profiles: %d", r.code)
	}
	// No picture yet: 404, the client draws initials.
	adminTok := f.login("pete", "adminpass1")
	if r := f.do("GET", fmt.Sprintf("/api/v1/profiles/%d/avatar", f.robin), adminTok, nil); r.code != 404 {
		t.Fatalf("no avatar: %d", r.code)
	}
	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/profiles/%d/avatar", f.robin), adminTok, "not an image"); r.code != 400 {
		t.Fatalf("bad avatar: %d %s", r.code, r.body)
	}
	var png bytes.Buffer
	if err := pngenc.Encode(&png, image.NewGray(image.Rect(0, 0, 40, 30))); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/v1/admin/profiles/%d/avatar", f.robin), &png)
	req.RemoteAddr = "192.168.1.20:4000"
	req.Header.Set("Authorization", "Bearer "+adminTok)
	req.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var list struct {
		Profiles []struct {
			ID        int64
			AvatarURL *string `json:"avatar_url"`
		}
	}
	f.do("GET", "/api/v1/profiles", adminTok, nil).json(t, &list)
	var url string
	for _, p := range list.Profiles {
		if p.ID == f.robin && p.AvatarURL != nil {
			url = *p.AvatarURL
		}
	}
	if !strings.HasPrefix(url, fmt.Sprintf("/api/v1/profiles/%d/avatar?v=", f.robin)) {
		t.Fatalf("avatar url %q", url)
	}
	if r := f.do("GET", url, adminTok, nil); r.code != 200 || r.hdr.Get("Content-Type") != "image/jpeg" {
		t.Fatalf("avatar: %d %s", r.code, r.hdr.Get("Content-Type"))
	}
	// Outside the household, no pictures.
	if r := f.do("GET", url, f.strangerTok, nil); r.code != 403 {
		t.Fatalf("stranger reads an avatar: %d", r.code)
	}
}

// The skip-markers choice (#42) is per user, so it follows the active profile
// across switches.
func TestPlaybackSettingsFollowTheProfile(t *testing.T) {
	f := newHouseholdFixture(t)
	mode := func(tok string) string {
		var s struct {
			SkipMarkers string `json:"skip_markers"`
		}
		f.do("GET", "/api/v1/me/playback-settings", tok, nil).json(t, &s)
		return s.SkipMarkers
	}
	if r := f.do("PUT", "/api/v1/me/playback-settings", f.adminTok, map[string]any{"skip_markers": "auto"}); r.code != 200 {
		t.Fatalf("pete sets auto: %d %s", r.code, r.body)
	}
	robin := tokenOf(t, f.switchTo(f.adminTok, f.robin, nil))
	if got := mode(robin); got == "auto" {
		t.Fatal("Robin inherited pete's skip setting")
	}
	if r := f.do("PUT", "/api/v1/me/playback-settings", robin, map[string]any{"skip_markers": "off"}); r.code != 200 {
		t.Fatalf("Robin sets off: %d", r.code)
	}
	pete := tokenOf(t, f.switchTo(robin, mustUserID(t, f.fixture, "pete"), map[string]any{"password": "adminpass1"}))
	if got := mode(pete); got != "auto" {
		t.Fatalf("pete's setting after switching back: %q, want auto", got)
	}
}
