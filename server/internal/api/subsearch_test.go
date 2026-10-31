package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/opensubtitles/ostest"
	"github.com/pete-builds/vcrx-media-server/server/internal/subsearch"
)

// withSubSearch turns subtitle search on against the OpenSubtitles stand-in.
func (f *fixture) withSubSearch(user, pass string) (*ostest.Server, string) {
	f.t.Helper()
	fake := ostest.New(f.t)
	dir := filepath.Join(f.t.TempDir(), "config", "subtitles")
	f.srv.SubSearch = subsearch.New(f.db, fake.Client(user, pass), dir, []string{"en"}, f.srv.Log)
	return fake, dir
}

const remote = "203.0.113.9:4000"

func TestSubtitleSearchRoutes(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	search := func(tok string, version int64) resp {
		return f.do("GET", fmt.Sprintf("/api/v1/versions/%d/subtitles/search?languages=en", version), tok, nil)
	}
	download := func(tok string, version, file int64, hdr ...string) resp {
		return f.do("POST", fmt.Sprintf("/api/v1/versions/%d/subtitles/download", version), tok, map[string]any{"file_id": file}, hdr...)
	}
	start := func(tok string) map[string]any {
		r := f.do("POST", "/api/v1/playback/sessions", tok, map[string]any{"item_id": f.movieID, "version_id": f.v1080, "capabilities": tclCaps()})
		if r.code != 201 {
			t.Fatalf("start: %d %s", r.code, r.body)
		}
		var out map[string]any
		r.json(t, &out)
		return out
	}

	// Off: no key configured.
	if r := search(f.friendTok, f.v1080); r.code != 404 || !strings.Contains(string(r.body), "search_off") {
		t.Fatalf("search while off: %d %s", r.code, r.body)
	}
	if s := start(f.friendTok)["subtitle_search"].(map[string]any); s["enabled"] != false || s["can_download"] != false {
		t.Fatalf("session while off: %v", s)
	}

	fake, dir := f.withSubSearch("viewer", "secret")

	// Search: anyone who may play the version, nobody else.
	if r := search(f.strangerTok, f.v1080); r.code != 404 {
		t.Fatalf("stranger search: %d", r.code)
	}
	if r := search(f.friendTok, f.v4k); r.code != 404 {
		t.Fatalf("friend search on the hidden 4K version: %d", r.code)
	}
	r := search(f.friendTok, f.v1080)
	var res struct {
		Results     []subsearch.Hit
		CanDownload bool `json:"can_download"`
		Quota       any
	}
	if r.code != 200 {
		t.Fatalf("friend search: %d %s", r.code, r.body)
	}
	r.json(t, &res)
	if len(res.Results) != 1 || res.Results[0].FileID != 8000009 || res.CanDownload || res.Quota != nil {
		t.Fatalf("friend search: %s", r.body)
	}
	if s := start(f.friendTok)["subtitle_search"].(map[string]any); s["enabled"] != true || s["can_download"] != false {
		t.Fatalf("friend session: %v", s)
	}

	// Download: admins, and users whose policy allows it.
	if r := download(f.friendTok, f.v1080, 8000009); r.code != 403 {
		t.Fatalf("friend download without the flag: %d %s", r.code, r.body)
	}
	if r := download(f.strangerTok, f.v1080, 8000009); r.code != 404 {
		t.Fatalf("stranger download: %d", r.code)
	}
	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/users/%d/policy", f.userID("friend")), f.adminTok, map[string]any{
		"max_resolution": "1080p", "allow_transcode": true, "allow_subtitle_download": true}); r.code != 204 {
		t.Fatalf("set policy: %d %s", r.code, r.body)
	}
	if s := start(f.friendTok)["subtitle_search"].(map[string]any); s["can_download"] != true {
		t.Fatalf("friend session with the flag: %v", s)
	}
	r = download(f.friendTok, f.v1080, 8000009)
	if r.code != 201 {
		t.Fatalf("friend download: %d %s", r.code, r.body)
	}
	var got struct {
		Subtitle subsearch.Downloaded
		WebVTT   struct {
			TrackID    int64 `json:"track_id"`
			URL        string
			Downloaded bool
		}
		Quota any
	}
	r.json(t, &got)
	sid := got.Subtitle.StreamID
	if sid == 0 || got.WebVTT.TrackID != sid || !got.WebVTT.Downloaded || got.WebVTT.URL != fmt.Sprintf("/api/v1/subtitles/%d?format=vtt", sid) || got.Quota != nil {
		t.Fatalf("download answer: %s", r.body)
	}
	if r := download(f.friendTok, f.v1080, 8000009); r.code != 200 {
		t.Fatalf("second download: %d %s", r.code, r.body)
	}
	if r := download(f.adminTok, f.v1080, 4242); r.code != 409 {
		t.Fatalf("a file no search offered: %d %s", r.code, r.body)
	}

	// Every client sees it: in the item's tracks and the TV's side-loaded list.
	sess := start(f.friendTok)
	subs := sess["subtitles"].([]any)
	found := false
	for _, s := range subs {
		m := s.(map[string]any)
		if int64(m["track_id"].(float64)) == sid && m["downloaded"] == true && m["mime_type"] == "application/x-subrip" {
			found = true
		}
	}
	if !found {
		t.Fatalf("downloaded track missing from the side-loaded list: %v", subs)
	}
	raw := f.do("GET", fmt.Sprintf("/api/v1/subtitles/%d", sid), f.friendTok, nil)
	if raw.code != 200 || string(raw.body) != ostest.Content {
		t.Fatalf("raw download: %d %q", raw.code, raw.body)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/subtitles/%d", sid), f.strangerTok, nil); r.code != 404 {
		t.Fatalf("stranger raw: %d", r.code)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, fmt.Sprint(f.v1080))); len(entries) != 1 {
		t.Fatalf("data dir: %v", entries)
	}

	// The admin list: admins on the LAN only, with the quota.
	list := fmt.Sprintf("/api/v1/admin/items/%d/subtitles", f.movieID)
	if r := f.do("GET", list, f.friendTok, nil); r.code != 403 {
		t.Fatalf("friend list: %d", r.code)
	}
	if r := f.do("GET", list, f.adminTok, nil, "RemoteAddr", remote); r.code != 403 {
		t.Fatalf("remote admin list: %d", r.code)
	}
	r = f.do("GET", list, f.adminTok, nil)
	var adm struct {
		Subtitles []subsearch.Listed
		Quota     struct {
			Known     bool
			Remaining int
		}
	}
	r.json(t, &adm)
	if r.code != 200 || len(adm.Subtitles) != 1 || adm.Subtitles[0].StreamID != sid || adm.Subtitles[0].DownloadedBy == nil ||
		*adm.Subtitles[0].DownloadedBy != "friend" || !adm.Quota.Known || adm.Quota.Remaining != 19 {
		t.Fatalf("admin list: %d %s", r.code, r.body)
	}

	// Delete: admins on the LAN only, and only downloaded subtitles.
	del := fmt.Sprintf("/api/v1/admin/subtitles/%d", sid)
	if r := f.do("DELETE", del, f.friendTok, nil); r.code != 403 {
		t.Fatalf("friend delete: %d", r.code)
	}
	if r := f.do("DELETE", del, f.adminTok, nil, "RemoteAddr", remote); r.code != 403 {
		t.Fatalf("remote admin delete: %d", r.code)
	}
	var sidecar int64
	_ = f.db.QueryRow(`SELECT id FROM media_streams WHERE version_id = ? AND is_external = 1 AND downloaded_id IS NULL`, f.v1080).Scan(&sidecar)
	if r := f.do("DELETE", fmt.Sprintf("/api/v1/admin/subtitles/%d", sidecar), f.adminTok, nil); r.code != 404 {
		t.Fatalf("delete a sidecar: %d", r.code)
	}
	if r := f.do("DELETE", del, f.adminTok, nil); r.code != 204 {
		t.Fatalf("admin delete: %d %s", r.code, r.body)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/subtitles/%d", sid), f.adminTok, nil); r.code != 404 {
		t.Fatalf("deleted subtitle still served: %d", r.code)
	}
	if fake.Count("/api/v1/download") != 1 {
		t.Fatalf("%d provider downloads, want 1", fake.Count("/api/v1/download"))
	}
}

func (f *fixture) userID(name string) int64 {
	var id int64
	if err := f.db.QueryRow(`SELECT id FROM users WHERE username = ?`, name).Scan(&id); err != nil {
		f.t.Fatal(err)
	}
	return id
}

// OpenSubtitles being down, out of quota or unconfigured never breaks
// anything else, and each says what to do.
func TestSubtitleProviderFailures(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	fake, _ := f.withSubSearch("viewer", "secret")
	path := fmt.Sprintf("/api/v1/versions/%d/subtitles/", f.v1080)

	fake.Remaining = 0
	if r := f.do("GET", path+"search", f.adminTok, nil); r.code != 200 {
		t.Fatalf("search: %d %s", r.code, r.body)
	}
	if r := f.do("POST", path+"download", f.adminTok, map[string]any{"file_id": 8000009}); r.code != 429 ||
		!strings.Contains(string(r.body), "quota_exhausted") {
		t.Fatalf("quota: %d %s", r.code, r.body)
	}

	fake.Close()
	if r := f.do("GET", path+"search?languages=fr", f.adminTok, nil); r.code != 503 || !strings.Contains(string(r.body), "provider_unavailable") {
		t.Fatalf("offline search: %d %s", r.code, r.body)
	}
	if r := f.do("POST", "/api/v1/playback/sessions", f.adminTok, map[string]any{"item_id": f.movieID, "capabilities": tclCaps()}); r.code != 201 {
		t.Fatalf("playback while offline: %d %s", r.code, r.body)
	}
	if r := f.do("GET", fmt.Sprintf("/api/v1/admin/items/%d/subtitles", f.movieID), f.adminTok, nil); r.code != 200 {
		t.Fatalf("admin list while offline: %d %s", r.code, r.body)
	}
	if r := f.do("GET", path+"search?languages=xx", f.adminTok, nil); r.code != 400 {
		t.Fatalf("unknown language: %d %s", r.code, r.body)
	}

	// A key but no account: search works, downloads say why not.
	g := newFixture(t)
	g.withSubSearch("", "")
	if r := g.do("GET", path+"search", g.adminTok, nil); r.code != 200 {
		t.Fatalf("search without account: %d %s", r.code, r.body)
	}
	if r := g.do("POST", path+"download", g.adminTok, map[string]any{"file_id": 8000009}); r.code != 403 ||
		!strings.Contains(string(r.body), "download_off") {
		t.Fatalf("download without account: %d %s", r.code, r.body)
	}
}

func TestSubtitleOffsetShiftsServedCues(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	vtt := filepath.Join(f.root, "Film 2001 1080p", "film.1080p.fr.vtt")
	if err := os.WriteFile(vtt, []byte("WEBVTT\n\n1\n00:00:01.000 --> 00:00:03.000\nBonjour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := f.db.Exec(`INSERT INTO media_streams (version_id, kind, codec, language, is_external, external_path) VALUES (?, 'subtitle', 'webvtt', 'fre', 1, ?)`, f.v1080, vtt)
	if err != nil {
		t.Fatal(err)
	}
	sid, _ := res.LastInsertId()
	url := fmt.Sprintf("/api/v1/subtitles/%d?format=vtt", sid)
	offset := fmt.Sprintf("/api/v1/versions/%d/subtitle-offset", f.v1080)

	before := f.do("GET", url, f.friendTok, nil)
	if before.code != 200 || !strings.Contains(string(before.body), "00:00:01.000 --> 00:00:03.000") {
		t.Fatalf("unshifted: %d %s", before.code, before.body)
	}
	if cc := before.hdr.Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Fatalf("Cache-Control %q: a cached copy would outlive a timing change", cc)
	}
	if r := f.do("PUT", offset, f.friendTok, map[string]any{"offset_ms": 1500}); r.code != 200 {
		t.Fatalf("set offset: %d %s", r.code, r.body)
	}
	after := f.do("GET", url, f.friendTok, nil)
	if !strings.Contains(string(after.body), "00:00:02.500 --> 00:00:04.500") {
		t.Fatalf("shifted: %s", after.body)
	}
	// The old copy is not reused; the new one is.
	if r := f.do("GET", url, f.friendTok, nil, "If-None-Match", before.hdr.Get("ETag")); r.code != 200 {
		t.Fatalf("revalidating the unshifted copy: %d", r.code)
	}
	if r := f.do("GET", url, f.friendTok, nil, "If-None-Match", after.hdr.Get("ETag")); r.code != 304 {
		t.Fatalf("revalidating the current copy: %d", r.code)
	}
	// Per user: the admin's cues are unchanged.
	if r := f.do("GET", url, f.adminTok, nil); !strings.Contains(string(r.body), "00:00:01.000 --> 00:00:03.000") {
		t.Fatalf("admin sees the friend's offset: %s", r.body)
	}
	// Earlier than the start: the cue is cut off at zero, or dropped.
	f.do("PUT", offset, f.friendTok, map[string]any{"offset_ms": -2000})
	if r := f.do("GET", url, f.friendTok, nil); !strings.Contains(string(r.body), "00:00:00.000 --> 00:00:01.000") {
		t.Fatalf("negative: %s", r.body)
	}
	f.do("PUT", offset, f.friendTok, map[string]any{"offset_ms": -3000})
	if r := f.do("GET", url, f.friendTok, nil); strings.Contains(string(r.body), "Bonjour") {
		t.Fatalf("a cue ending before zero was served: %s", r.body)
	}
	// The raw file the TV side-loads is never shifted; the TV gets the offset instead.
	if r := f.do("GET", fmt.Sprintf("/api/v1/subtitles/%d", sid), f.friendTok, nil); !strings.Contains(string(r.body), "00:00:01.000") {
		t.Fatalf("raw file shifted: %s", r.body)
	}
	r := f.do("POST", "/api/v1/playback/sessions", f.friendTok, map[string]any{"item_id": f.movieID, "capabilities": tclCaps()})
	var sess struct {
		Offset int64 `json:"subtitle_offset_ms"`
	}
	r.json(t, &sess)
	if r.code != 201 || sess.Offset != -3000 {
		t.Fatalf("session offset: %d %s", r.code, r.body)
	}

	for _, bad := range []map[string]any{{"offset_ms": 600_001}, {"offset_ms": -600_001}, {}} {
		if r := f.do("PUT", offset, f.friendTok, bad); r.code != 400 {
			t.Fatalf("offset %v: %d", bad, r.code)
		}
	}
	if r := f.do("PUT", offset, f.strangerTok, map[string]any{"offset_ms": 100}); r.code != 404 {
		t.Fatalf("stranger offset: %d", r.code)
	}
	if r := f.do("PUT", fmt.Sprintf("/api/v1/versions/%d/subtitle-offset", f.v4k), f.friendTok, map[string]any{"offset_ms": 100}); r.code != 404 {
		t.Fatalf("offset on a hidden version: %d", r.code)
	}
	if r := f.do("PUT", offset, f.friendTok, map[string]any{"offset_ms": 0}); r.code != 200 {
		t.Fatalf("reset: %d", r.code)
	}
	var rows int
	_ = f.db.QueryRow(`SELECT COUNT(*) FROM subtitle_offsets`).Scan(&rows)
	if rows != 0 {
		t.Fatalf("%d offset rows after a reset", rows)
	}
}
