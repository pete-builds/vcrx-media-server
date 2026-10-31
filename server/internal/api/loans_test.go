package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/loans"
)

// Lend a tape (milestone 10, item 1). Every rule has a test here: the card's
// token opens one title, the loan expires on its own, one loan per copy,
// everyone else (the owner included) is refused while it is out, Return early,
// open streams cut when it ends, and "Be kind, rewind." on the borrower's
// device. The access walk (access_test.go) refuses a lent title on every
// playback route as each kind of principal.

type borrowed struct {
	loanID, tokenID, userID int64
	secret, token           string
}

// lend has the admin lend item; extra overrides the request body.
func (f *fixture) lend(item int64, extra map[string]any) resp {
	f.t.Helper()
	body := map[string]any{"item_id": item, "name": "Mark"}
	for k, v := range extra {
		body[k] = v
	}
	return f.do("POST", "/api/v1/admin/loans", f.adminTok, body)
}

// borrow lends item and redeems the card's invite as the borrower's phone.
func (f *fixture) borrow(item int64, extra map[string]any) borrowed {
	f.t.Helper()
	r := f.lend(item, extra)
	if r.code != 201 {
		f.t.Fatalf("lend: %d %s", r.code, r.body)
	}
	var out struct {
		Loan struct {
			ID      int64
			TokenID int64 `json:"token_id"`
		}
		Invite string
		URL    string
	}
	r.json(f.t, &out)
	if !strings.HasSuffix(out.URL, "/#/invite/"+out.Invite) {
		f.t.Fatalf("the card's link %q does not carry its invite", out.URL)
	}
	rr := f.redeem(out.Invite)
	if rr.code != 200 {
		f.t.Fatalf("redeem the card: %d %s", rr.code, rr.body)
	}
	var in struct {
		Token string
		User  struct{ ID int64 }
		Loan  struct{ Title string }
	}
	rr.json(f.t, &in)
	if in.Loan.Title == "" {
		f.t.Fatalf("redeeming a card did not say which tape: %s", rr.body)
	}
	return borrowed{loanID: out.Loan.ID, tokenID: out.Loan.TokenID, userID: in.User.ID, secret: out.Invite, token: in.Token}
}

// expire moves a loan and its token past their due date, as time would.
func (f *fixture) expire(b borrowed) {
	f.t.Helper()
	past := time.Now().Add(-time.Second).UnixMilli()
	if _, err := f.db.Exec(`UPDATE loans SET due_at = ? WHERE id = ?`, past, b.loanID); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE device_tokens SET expires_at = ? WHERE id = ?`, past, b.tokenID); err != nil {
		f.t.Fatal(err)
	}
}

// addMovie puts another 1080p movie in Movies, with a file on disk.
func (f *fixture) addMovie(title string) (int64, int64) {
	f.t.Helper()
	now := time.Now().UnixMilli()
	lib := f.libraryID("Movies")
	dir := filepath.Join(f.root, title)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	p := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(p, content(10_000), 0o644); err != nil {
		f.t.Fatal(err)
	}
	res, err := f.db.Exec(`INSERT INTO items (library_id, kind, group_key, title, sort_title, year, added_at, updated_at)
		VALUES (?, 'movie', ?, ?, ?, 2005, ?, ?)`, lib, title, title, title, now, now)
	if err != nil {
		f.t.Fatal(err)
	}
	item, _ := res.LastInsertId()
	res, err = f.db.Exec(`INSERT INTO media_versions (item_id, library_id, path, rel_path, size, mtime, container, duration_ms, video_codec,
			video_profile, bit_depth, width, height, resolution, probe_status, added_at, updated_at)
		VALUES (?, ?, ?, ?, 10000, 0, 'mkv', 6000000, 'hevc', 'main 10', 10, 1920, 1080, '1080p', 'ok', ?, ?)`, item, lib, p, title+"/movie.mkv", now, now)
	if err != nil {
		f.t.Fatal(err)
	}
	v, _ := res.LastInsertId()
	return item, v
}

func (f *fixture) tryPlay(tok string, item int64, version *int64) resp {
	f.t.Helper()
	body := map[string]any{"item_id": item, "capabilities": tclCaps()}
	if version != nil {
		body["version_id"] = *version
	}
	return f.do("POST", "/api/v1/playback/sessions", tok, body)
}

func sessionID(t *testing.T, r resp) (string, string) {
	t.Helper()
	var s struct {
		SessionID string `json:"session_id"`
		Stream    struct{ URL string }
	}
	r.json(t, &s)
	if r.code != 201 || s.SessionID == "" {
		t.Fatalf("start: %d %s", r.code, r.body)
	}
	return s.SessionID, s.Stream.URL
}

// The card's token is scoped to one title: that movie's versions (1080p, as
// the owner chose), its subtitles, and nothing else in the library, including
// a guessed id of another title.
func TestLoanTokenSeesOneTitle(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	other, otherV := f.addMovie("Other Film")
	b := f.borrow(f.movieID, nil)

	if r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), b.token, nil); r.code != 200 ||
		strings.Contains(string(r.body), `"4K"`) || !strings.Contains(string(r.body), `"yours":true`) {
		t.Fatalf("the lent movie on the borrower's device: %d %s", r.code, r.body)
	}
	_, url := sessionID(t, f.tryPlay(b.token, f.movieID, nil))
	if sr := f.do("GET", url, b.token, nil, "Range", "bytes=0-99"); sr.code != 206 {
		t.Fatalf("the borrower streaming: %d %s", sr.code, sr.body)
	}
	var subID int64
	_ = f.db.QueryRow(`SELECT id FROM media_streams WHERE version_id = ? AND kind = 'subtitle'`, f.v1080).Scan(&subID)
	if r := f.do("GET", fmt.Sprintf("/api/v1/subtitles/%d", subID), b.token, nil); r.code != 200 {
		t.Errorf("the lent movie's subtitles: %d %s", r.code, r.body)
	}
	// Never 4K unless the owner allows it.
	if r := f.tryPlay(b.token, f.movieID, &f.v4k); r.code != 404 {
		t.Errorf("the 4K version: %d %s", r.code, r.body)
	}

	// Everything else is hidden, a guessed id included.
	for _, p := range []string{fmt.Sprintf("/api/v1/items/%d", other), fmt.Sprintf("/api/v1/items/%d", f.episodeID),
		fmt.Sprintf("/api/v1/items/%d/artwork/poster", other)} {
		if r := f.do("GET", p, b.token, nil); r.code != 404 {
			t.Errorf("%s as the borrower: %d, want 404", p, r.code)
		}
	}
	if r := f.tryPlay(b.token, other, nil); r.code != 404 {
		t.Errorf("playing a guessed title id: %d %s", r.code, r.body)
	}
	if r := f.tryPlay(b.token, f.movieID, &otherV); r.code != 404 {
		t.Errorf("the lent title with another title's version id: %d %s", r.code, r.body)
	}
	for _, p := range []string{"/api/v1/home", "/api/v1/search?q=film", fmt.Sprintf("/api/v1/libraries/%d/items", f.libraryID("Movies"))} {
		r := f.do("GET", p, b.token, nil)
		if r.code != 200 || strings.Contains(string(r.body), "Other Film") || !strings.Contains(string(r.body), "The Film") {
			t.Errorf("%s lists %d %.300s, want the lent film only", p, r.code, r.body)
		}
	}
	if r := f.do("GET", "/api/v1/libraries", b.token, nil); strings.Contains(string(r.body), `"TV"`) {
		t.Errorf("the borrower sees the TV library: %s", r.body)
	}

	// Control: an ordinary invite to the same library sees the other film,
	// so the 404s above are the loan's doing, not a missing fixture.
	d := f.invite(map[string]any{"library_ids": []int64{f.libraryID("Movies")}})
	if r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", other), d.token, nil); r.code != 200 {
		t.Fatalf("control: an invite to Movies cannot see the other film: %d", r.code)
	}
	if r := f.tryPlay(d.token, other, nil); r.code != 201 {
		t.Fatalf("control: an invite to Movies cannot play the other film: %d %s", r.code, r.body)
	}
	// 4K when the owner allows it.
	b4 := f.borrow(other, map[string]any{"allow_4k": true, "name": "Ana"})
	if r := f.do("GET", "/api/v1/me", b4.token, nil); !strings.Contains(string(r.body), `"title":"Other Film"`) {
		t.Errorf("/me does not name the borrowed tape: %s", r.body)
	}
}

// Streaming only: the borrower never gets a whole file, by any route.
func TestLoanIsStreamOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.borrow(f.movieID, map[string]any{"allow_transcode": true})
	if got := wholeFileRoutes(t, f, f.srv.Routes(), b.token); len(got) > 0 {
		t.Fatalf("routes serving the borrower a whole file: %v", got)
	}
	wantError(t, "the bare file URL", f.do("GET", fmt.Sprintf("/api/v1/versions/%d/file", f.v1080), b.token, nil), 403, "stream_only")
}

// A loan lasts 24 hours to 7 days, 72 by default, and ends on its own.
func TestLoanExpiry(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	for _, h := range []int{23, 169} {
		if r := f.lend(f.movieID, map[string]any{"hours": h}); r.code != 400 {
			t.Errorf("a %d hour loan: %d %s", h, r.code, r.body)
		}
	}
	before := time.Now()
	b := f.borrow(f.movieID, nil)
	var due, tokExp, sessExp int64
	_ = f.db.QueryRow(`SELECT due_at FROM loans WHERE id = ?`, b.loanID).Scan(&due)
	_ = f.db.QueryRow(`SELECT expires_at FROM device_tokens WHERE id = ?`, b.tokenID).Scan(&tokExp)
	_ = f.db.QueryRow(`SELECT expires_at FROM auth_sessions WHERE user_id = ?`, b.userID).Scan(&sessExp)
	if want := before.Add(72 * time.Hour).UnixMilli(); due < want || due > want+5000 || tokExp != due || sessExp > due {
		t.Fatalf("a default loan is due %d (want 72 hours, %d), its token %d, its session %d", due, want, tokExp, sessExp)
	}
	if r := f.tryPlay(f.adminTok, f.movieID, nil); r.code != 409 {
		t.Fatalf("the owner while it is out: %d %s", r.code, r.body)
	}
	f.expire(b)
	// Back on the shelf at the due date, before any sweep writes it down.
	if r := f.tryPlay(f.adminTok, f.movieID, nil); r.code != 201 {
		t.Errorf("the owner after the due date: %d %s", r.code, r.body)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), f.friendTok, nil); !strings.Contains(string(r.body), `"loan":null`) {
		t.Errorf("the detail page after the due date still shows a loan: %s", r.body)
	}
	wantError(t, "the borrower after the due date", f.do("GET", "/api/v1/me", b.token, nil), 401, "loan_ended")
	var list struct{ Loans []loans.Loan }
	f.do("GET", "/api/v1/admin/loans", f.adminTok, nil).json(t, &list)
	if len(list.Loans) != 1 || list.Loans[0].State != "expired" || list.Loans[0].EndedAt == nil {
		t.Fatalf("Activity before the sweep: %+v", list.Loans)
	}
	f.srv.CutExpiredDevices(context.Background())
	var reason, by string
	_ = f.db.QueryRow(`SELECT end_reason, ended_by FROM loans WHERE id = ?`, b.loanID).Scan(&reason, &by)
	if reason != "expired" || by != "expiry" {
		t.Errorf("after the sweep: %q by %q", reason, by)
	}
}

// One loan per copy: a second loan of a title that is out is refused, by the
// handler and by the database's unique index, until it comes back.
func TestOneLoanPerCopy(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.borrow(f.movieID, nil)
	wantError(t, "a second loan", f.lend(f.movieID, map[string]any{"name": "Ana"}), 409, "on_loan")
	if _, err := f.db.Exec(`INSERT INTO loans (item_id, title, borrower, created_at, due_at) VALUES (?, 'x', 'y', 1, ?)`,
		f.movieID, time.Now().Add(time.Hour).UnixMilli()); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("the database took a second open loan of one copy: %v", err)
	}
	var tokens int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM device_tokens`).Scan(&tokens)
	if tokens != 1 {
		t.Errorf("a refused loan left an invite behind: %d tokens", tokens)
	}
	// Another title is a different copy.
	other, _ := f.addMovie("Other Film")
	if r := f.lend(other, nil); r.code != 201 {
		t.Errorf("lending a different title: %d %s", r.code, r.body)
	}
	// Only movies on disk.
	wantError(t, "lending a show", f.lend(f.showID, nil), 422, "not_lendable")
	wantError(t, "lending an episode", f.lend(f.episodeID, nil), 422, "not_lendable")
	// Back from an early return, it can go out again.
	if r := f.do("POST", fmt.Sprintf("/api/v1/admin/loans/%d/return", b.loanID), f.adminTok, nil); r.code != 200 {
		t.Fatalf("return: %d %s", r.code, r.body)
	}
	b2 := f.borrow(f.movieID, map[string]any{"name": "Ana"})
	// And once that one runs out, without a sweep.
	f.expire(b2)
	if r := f.lend(f.movieID, map[string]any{"name": "Lee"}); r.code != 201 {
		t.Fatalf("lending after the last loan ran out: %d %s", r.code, r.body)
	}
	var reason string
	_ = f.db.QueryRow(`SELECT end_reason FROM loans WHERE id = ?`, b2.loanID).Scan(&reason)
	if reason != "expired" {
		t.Errorf("the run-out loan reads %q", reason)
	}
}

// While a title is out, nobody but the borrower plays the owner's copy: a
// friend, another invited device, and the admin who owns it. It stays on the
// shelf, saying when it is due back. Streams of it open at lending time stop.
func TestLentTitleRefusesEveryoneElse(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	other := f.invite(map[string]any{"library_ids": []int64{f.libraryID("Movies")}, "name": "Another phone"})
	who := map[string]string{"the owner (admin)": f.adminTok, "a friend": f.friendTok, "another invited device": other.token}
	// Control: before the loan every one of them plays it.
	for name, tok := range who {
		if r := f.tryPlay(tok, f.movieID, &f.v1080); r.code != 201 {
			t.Fatalf("control: %s before the loan: %d %s", name, r.code, r.body)
		}
	}
	friendSession, friendURL := sessionID(t, f.tryPlay(f.friendTok, f.movieID, &f.v1080))
	b := f.borrow(f.movieID, nil)
	var stopped string
	_ = f.db.QueryRow(`SELECT terminated_message FROM playback_sessions WHERE id = ?`, friendSession).Scan(&stopped)
	if !strings.HasPrefix(stopped, "Out on loan, due back ") {
		t.Errorf("a stream open when the tape went out ended with %q", stopped)
	}
	if r := f.do("GET", friendURL, f.friendTok, nil, "Range", "bytes=0-99"); r.code == 206 || r.code == 200 {
		t.Errorf("the friend's open session still streams: %d", r.code)
	}
	for name, tok := range who {
		r := f.tryPlay(tok, f.movieID, nil)
		wantError(t, name, r, 409, "on_loan")
		if !strings.Contains(string(r.body), "Out on loan, due back") {
			t.Errorf("%s was told %s", name, r.body)
		}
		// Nor by a file URL, a converted stream or a subtitle.
		if fr := f.do("GET", fmt.Sprintf("/api/v1/versions/%d/file", f.v1080), tok, nil); fr.code != 404 && fr.code != 403 {
			t.Errorf("%s fetching the file: %d", name, fr.code)
		}
		d := f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), tok, nil)
		if d.code != 200 || !strings.Contains(string(d.body), `"loan":{`) || strings.Contains(string(d.body), `"yours":true`) {
			t.Errorf("%s: the detail page should show it out on loan: %d %.400s", name, d.code, d.body)
		}
		if strings.Contains(string(d.body), `"borrower":"Mark"`) != (name == "the owner (admin)") {
			t.Errorf("%s: who has it is for admins only: %.400s", name, d.body)
		}
	}
	var subID int64
	_ = f.db.QueryRow(`SELECT id FROM media_streams WHERE version_id = ? AND kind = 'subtitle'`, f.v1080).Scan(&subID)
	if r := f.do("GET", fmt.Sprintf("/api/v1/subtitles/%d", subID), f.friendTok, nil); r.code != 404 {
		t.Errorf("the friend reading the lent copy's subtitles: %d", r.code)
	}
	// The borrower still plays it, and the refusals are in Activity.
	if r := f.tryPlay(b.token, f.movieID, nil); r.code != 201 {
		t.Errorf("the borrower: %d %s", r.code, r.body)
	}
	var refused int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM playback_sessions WHERE item_id = ? AND decision = 'refused' AND decision_reason LIKE 'Out on loan%'`,
		f.movieID).Scan(&refused)
	if refused != len(who) {
		t.Errorf("%d refusals recorded, want %d", refused, len(who))
	}
	// Other titles are untouched.
	if r := f.tryPlay(f.friendTok, f.episodeID, nil); r.code != 201 {
		t.Errorf("another title while this one is out: %d %s", r.code, r.body)
	}
}

// Return early: the admin brings the tape back. The title is on the shelf at
// once, the borrower's open stream stops mid-file and its next request says
// "Be kind, rewind." Revoking the borrower's device from Devices, or the
// borrower signing out, returns it the same way.
func TestReturnEarly(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.borrow(f.movieID, nil)
	var path string
	_ = f.db.QueryRow(`SELECT path FROM media_versions WHERE id = ?`, f.v1080).Scan(&path)
	const size = 256 << 20
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE media_versions SET size = ? WHERE id = ?`, size, f.v1080); err != nil {
		t.Fatal(err)
	}
	sid, url := sessionID(t, f.tryPlay(b.token, f.movieID, nil))
	srv := httptest.NewServer(f.h)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+url, nil)
	req.Header.Set("Authorization", "Bearer "+b.token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if _, err := io.ReadFull(res.Body, make([]byte, 1<<20)); err != nil {
		t.Fatal(err)
	}
	wantError(t, "a friend before the return", f.tryPlay(f.friendTok, f.movieID, nil), 409, "on_loan")
	var out struct {
		Loan           loans.Loan
		StreamsStopped int `json:"streams_stopped"`
	}
	r := f.do("POST", fmt.Sprintf("/api/v1/admin/loans/%d/return", b.loanID), f.adminTok, nil)
	r.json(t, &out)
	if r.code != 200 || out.StreamsStopped != 1 || out.Loan.State != "returned" || out.Loan.EndedBy == nil || *out.Loan.EndedBy != "pete" {
		t.Fatalf("return early: %d %s", r.code, r.body)
	}
	n, _ := io.Copy(io.Discard, res.Body)
	if n+(1<<20) >= size {
		t.Fatalf("the borrower's open stream ran to the end (%d bytes) after the return", n+(1<<20))
	}
	if r := f.tryPlay(f.friendTok, f.movieID, nil); r.code != 201 {
		t.Errorf("a friend after the return: %d %s", r.code, r.body)
	}
	if r := f.tryPlay(f.adminTok, f.movieID, nil); r.code != 201 {
		t.Errorf("the owner after the return: %d %s", r.code, r.body)
	}
	wantError(t, "the borrower after the return", f.do("GET", "/api/v1/me", b.token, nil), 401, "loan_ended")
	var term string
	_ = f.db.QueryRow(`SELECT terminated_message FROM playback_sessions WHERE id = ?`, sid).Scan(&term)
	if !strings.HasPrefix(term, "Be kind, rewind.") {
		t.Errorf("the borrower's session ended with %q", term)
	}
	// Returning twice changes nothing; an unknown loan is a 404.
	if r := f.do("POST", fmt.Sprintf("/api/v1/admin/loans/%d/return", b.loanID), f.adminTok, nil); r.code != 200 {
		t.Errorf("returning twice: %d", r.code)
	}
	wantError(t, "an unknown loan", f.do("POST", "/api/v1/admin/loans/999/return", f.adminTok, nil), 404, "not_found")

	// Revoking a borrower's device from Settings > Devices returns the tape.
	b2 := f.borrow(f.movieID, map[string]any{"name": "Ana"})
	if r := f.do("DELETE", fmt.Sprintf("/api/v1/admin/devices/%d", b2.tokenID), f.adminTok, nil); r.code != 200 {
		t.Fatalf("revoke the borrower: %d %s", r.code, r.body)
	}
	if r := f.tryPlay(f.friendTok, f.movieID, nil); r.code != 201 {
		t.Errorf("after revoking the borrower the copy is still held: %d %s", r.code, r.body)
	}
	// So does the borrower signing out.
	b3 := f.borrow(f.movieID, map[string]any{"name": "Lee"})
	if r := f.do("POST", "/api/v1/auth/logout", b3.token, nil); r.code != 204 {
		t.Fatalf("the borrower signing out: %d", r.code)
	}
	var reason string
	_ = f.db.QueryRow(`SELECT end_reason FROM loans WHERE id = ?`, b3.loanID).Scan(&reason)
	if reason != "returned" {
		t.Errorf("signing out left the loan %q", reason)
	}
}

// When a loan runs out, a stream the borrower has open stops within the
// minute, ending with "Be kind, rewind."
func TestLoanExpiryCutsOpenStreams(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.borrow(f.movieID, nil)
	sid, _ := sessionID(t, f.tryPlay(b.token, f.movieID, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.srv.streams.add(sid, &cancel)
	if n := f.srv.CutExpiredDevices(context.Background()); n != 0 || ctx.Err() != nil {
		t.Fatalf("cut %d streams before the due date", n)
	}
	f.expire(b)
	if n := f.srv.CutExpiredDevices(context.Background()); n != 1 || ctx.Err() == nil {
		t.Fatalf("after the due date: cut %d streams, context %v", n, ctx.Err())
	}
	var msg string
	_ = f.db.QueryRow(`SELECT terminated_message FROM playback_sessions WHERE id = ?`, sid).Scan(&msg)
	if msg != loans.RewindMessage("The Film") {
		t.Errorf("the borrower's session ended with %q", msg)
	}
}

// "Be kind, rewind.": once the loan is over the borrower's device is told so,
// with the title, on any signed-in route, even after the expired session is
// purged; another refused device is not.
func TestBeKindRewind(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	b := f.borrow(f.movieID, nil)
	if r := f.do("GET", "/api/v1/me", b.token, nil); r.code != 200 || !strings.Contains(string(r.body), `"loan":{`) {
		t.Fatalf("the borrower while it is out: %d %s", r.code, r.body)
	}
	f.expire(b)
	for _, p := range []string{"/api/v1/me", "/api/v1/home", fmt.Sprintf("/api/v1/items/%d", f.movieID)} {
		r := f.do("GET", p, b.token, nil)
		wantError(t, p, r, 401, "loan_ended")
		if !strings.Contains(string(r.body), "Be kind, rewind.") || !strings.Contains(string(r.body), `"title":"The Film"`) {
			t.Errorf("%s: %s", p, r.body)
		}
	}
	if _, err := f.srv.Auth.PurgeExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantError(t, "after the session is purged", f.do("GET", "/api/v1/me", b.token, nil), 401, "loan_ended")
	// Control: an ordinary device whose invite ran out, and a made-up token,
	// get the plain answer.
	d := f.invite(nil)
	if _, err := f.db.Exec(`UPDATE device_tokens SET expires_at = ? WHERE id = ?`, time.Now().Add(-time.Second).UnixMilli(), d.id); err != nil {
		t.Fatal(err)
	}
	wantError(t, "an expired invite", f.do("GET", "/api/v1/me", d.token, nil), 401, "unauthorized")
	wantError(t, "a made-up token", f.do("GET", "/api/v1/me", "not-a-token", nil), 401, "unauthorized")
}

// Activity lists every loan: who, when, and how it ended.
func TestLoansInActivity(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	other, _ := f.addMovie("Other Film")
	b := f.borrow(f.movieID, nil)
	f.borrow(other, map[string]any{"name": "Ana", "hours": 24})
	f.do("POST", fmt.Sprintf("/api/v1/admin/loans/%d/return", b.loanID), f.adminTok, nil)
	var list struct{ Loans []loans.Loan }
	r := f.do("GET", "/api/v1/admin/loans", f.adminTok, nil)
	r.json(t, &list)
	if len(list.Loans) != 2 {
		t.Fatalf("loans: %s", r.body)
	}
	byTitle := map[string]loans.Loan{}
	for _, l := range list.Loans {
		byTitle[l.Title] = l
	}
	film, ana := byTitle["The Film"], byTitle["Other Film"]
	if film.Borrower != "Mark" || film.State != "returned" || film.LentBy == nil || *film.LentBy != "pete" || film.RedeemedAt == nil || film.EndedAt == nil {
		t.Errorf("the returned loan: %+v", film)
	}
	if ana.Borrower != "Ana" || ana.State != "out" || ana.DueAt-ana.CreatedAt != 24*3600*1000 || !strings.Contains(ana.Summary, "1080p") {
		t.Errorf("the loan still out: %+v", ana)
	}
	// Devices names a borrower's device by its tape.
	var devs struct {
		Devices []struct{ Summary string }
	}
	f.do("GET", "/api/v1/admin/devices", f.adminTok, nil).json(t, &devs)
	found := false
	for _, d := range devs.Devices {
		found = found || strings.HasPrefix(d.Summary, "Borrowing “Other Film” only")
	}
	if !found {
		t.Errorf("Devices does not say what a borrower borrows: %+v", devs.Devices)
	}
}

// A lent movie's trailer goes with the tape: its borrower lists and plays it,
// everyone else (the owner included) neither lists nor plays it until it comes
// back, and a trailer playing when the tape went out stops.
func TestLoanCoversExtras(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
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
	playExtra := func(tok string) resp {
		return f.do("POST", "/api/v1/playback/sessions", tok, map[string]any{"extra_id": extraID, "capabilities": tclCaps()})
	}
	lists := func(tok string) bool {
		r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), tok, nil)
		return strings.Contains(string(r.body), `"title":"Teaser"`)
	}
	// Control: before the loan everyone lists and plays it.
	if !lists(f.friendTok) || !lists(f.adminTok) {
		t.Fatal("control: the trailer is not listed before the loan")
	}
	friendSession, _ := sessionID(t, playExtra(f.friendTok))
	b := f.borrow(f.movieID, nil)
	var stopped string
	_ = f.db.QueryRow(`SELECT terminated_message FROM playback_sessions WHERE id = ?`, friendSession).Scan(&stopped)
	if !strings.HasPrefix(stopped, "Out on loan") {
		t.Errorf("a trailer playing when the tape went out ended with %q", stopped)
	}
	for name, tok := range map[string]string{"the owner (admin)": f.adminTok, "a friend": f.friendTok} {
		if lists(tok) {
			t.Errorf("%s still lists the lent movie's trailer", name)
		}
		wantError(t, name+" playing the trailer", playExtra(tok), 409, "on_loan")
		if r := f.do("GET", fmt.Sprintf("/api/v1/extras/%d/file", extraID), tok, nil); r.code != 404 {
			t.Errorf("%s fetching the trailer file: %d", name, r.code)
		}
	}
	if !lists(b.token) {
		t.Error("the borrower does not list the trailer")
	}
	_, url := sessionID(t, playExtra(b.token))
	if r := f.do("GET", url, b.token, nil, "Range", "bytes=0-99"); r.code != 206 {
		t.Errorf("the borrower streaming the trailer: %d %s", r.code, r.body)
	}
	f.do("POST", fmt.Sprintf("/api/v1/admin/loans/%d/return", b.loanID), f.adminTok, nil)
	if !lists(f.friendTok) {
		t.Error("the trailer is not back after the return")
	}
	if r := playExtra(f.friendTok); r.code != 201 {
		t.Errorf("a friend playing the trailer after the return: %d %s", r.code, r.body)
	}
}
