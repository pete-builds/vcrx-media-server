package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/markers"
)

type fakeMarkerJob struct {
	reruns  []int64
	applied []string
}

func (f *fakeMarkerJob) Rerun(ctx context.Context, itemID int64) (int, error) {
	f.reruns = append(f.reruns, itemID)
	return 1, nil
}

func (f *fakeMarkerJob) ApplyToSeason(ctx context.Context, episodeID int64, kind string) error {
	f.applied = append(f.applied, fmt.Sprintf("%d/%s", episodeID, kind))
	return nil
}

type markerJSON struct {
	Kind       string  `json:"kind"`
	StartMs    int64   `json:"start_ms"`
	EndMs      int64   `json:"end_ms"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
	AutoSkip   *bool   `json:"auto_skip"`
}

func (f *fixture) detect(t *testing.T, kind string, start, end int64, confidence float64) {
	t.Helper()
	if _, err := f.db.Exec(`INSERT INTO item_markers (item_id, kind, start_ms, end_ms, confidence, source, updated_at)
		VALUES (?, ?, ?, ?, ?, 'detected', 0)`, f.episodeID, kind, start, end, confidence); err != nil {
		t.Fatal(err)
	}
}

// Markers come with the episode and with the playback session, to anyone who
// may see the episode, and to nobody else.
func TestMarkersFollowTheEpisodesPolicy(t *testing.T) {
	f := newFixture(t)
	f.detect(t, "intro", 60_000, 90_000, 0.9)
	f.detect(t, "credits", 5_800_000, 6_000_000, 0.85)

	var item struct{ Markers []markerJSON }
	r := f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.episodeID), f.friendTok, nil)
	r.json(t, &item)
	if r.code != 200 || len(item.Markers) != 2 || item.Markers[0].Kind != "intro" || item.Markers[0].EndMs != 90_000 {
		t.Fatalf("episode markers: %d %s", r.code, r.body)
	}
	// A movie has none, as an empty list rather than null.
	var movie struct{ Markers []markerJSON }
	f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.movieID), f.adminTok, nil).json(t, &movie)
	if movie.Markers == nil || len(movie.Markers) != 0 {
		t.Errorf("movie markers %v", movie.Markers)
	}

	var sess struct {
		Markers     []markerJSON
		SkipMarkers string `json:"skip_markers"`
	}
	r = f.do("POST", "/api/v1/playback/sessions", f.friendTok, map[string]any{"item_id": f.episodeID, "capabilities": tclCaps()})
	r.json(t, &sess)
	if r.code != 201 || len(sess.Markers) != 2 || sess.SkipMarkers != "button" || sess.Markers[0].AutoSkip == nil || *sess.Markers[0].AutoSkip {
		t.Fatalf("session markers: %d %s", r.code, r.body)
	}

	// The stranger has no grant on TV: no details, no session, so no markers.
	for _, p := range []string{fmt.Sprintf("/api/v1/items/%d", f.episodeID), fmt.Sprintf("/api/v1/items/%d", f.seasonID)} {
		if r := f.do("GET", p, f.strangerTok, nil); r.code != 404 {
			t.Errorf("stranger reads %s: %d %s", p, r.code, r.body)
		}
	}
	// On main a stranger can still start the session (#35 refuses it); the
	// markers must not come with it either way.
	var leak struct{ Markers []markerJSON }
	r = f.do("POST", "/api/v1/playback/sessions", f.strangerTok, map[string]any{"item_id": f.episodeID, "capabilities": tclCaps()})
	if r.code == 201 {
		r.json(t, &leak)
	}
	if len(leak.Markers) != 0 {
		t.Errorf("stranger's session carries the episode's markers: %s", r.body)
	}
	// Nor can a friend change them.
	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/items/%d/markers/intro", f.episodeID), f.friendTok, map[string]any{"start_ms": 0, "end_ms": 5000}); r.code != 403 {
		t.Errorf("friend sets a marker: %d", r.code)
	}
	if r := f.do("GET", "/api/v1/admin/markers", f.friendTok, nil); r.code != 403 {
		t.Errorf("friend reads detection status: %d", r.code)
	}
}

// Skip automatically acts only on a confident intro; credits always wait for
// a press. The choice is the user's own and lives on the server.
func TestSkipAutomaticallyOnlyWhenSure(t *testing.T) {
	f := newFixture(t)
	var got struct {
		SkipMarkers string `json:"skip_markers"`
	}
	f.do("GET", "/api/v1/me/playback-settings", f.friendTok, nil).json(t, &got)
	if got.SkipMarkers != "button" {
		t.Fatalf("default %q", got.SkipMarkers)
	}
	if r := f.do("PUT", "/api/v1/me/playback-settings", f.friendTok, map[string]string{"skip_markers": "sometimes"}); r.code != 400 {
		t.Errorf("bad mode: %d", r.code)
	}
	if r := f.do("PUT", "/api/v1/me/playback-settings", f.friendTok, map[string]string{"skip_markers": "auto"}); r.code != 200 {
		t.Fatalf("set auto: %d %s", r.code, r.body)
	}
	// The admin's own choice is untouched.
	f.do("GET", "/api/v1/me/playback-settings", f.adminTok, nil).json(t, &got)
	if got.SkipMarkers != "button" {
		t.Errorf("admin's mode changed to %q", got.SkipMarkers)
	}
	start := func() map[string]bool {
		var sess struct {
			Markers     []markerJSON
			SkipMarkers string `json:"skip_markers"`
		}
		f.do("POST", "/api/v1/playback/sessions", f.friendTok, map[string]any{"item_id": f.episodeID, "capabilities": tclCaps()}).json(t, &sess)
		out := map[string]bool{}
		for _, m := range sess.Markers {
			out[m.Kind] = *m.AutoSkip
		}
		return out
	}
	f.detect(t, "intro", 60_000, 90_000, 0.9)
	f.detect(t, "credits", 5_800_000, 6_000_000, 1)
	if a := start(); !a["intro"] || a["credits"] {
		t.Errorf("confident intro and credits: auto %v, want the intro only", a)
	}
	if _, err := f.db.Exec(`UPDATE item_markers SET confidence = 0.5 WHERE kind = 'intro'`); err != nil {
		t.Fatal(err)
	}
	if a := start(); a["intro"] {
		t.Errorf("an unsure intro is skipped automatically")
	}
}

func TestAdminSetsAndClearsMarkers(t *testing.T) {
	f := newFixture(t)
	f.detect(t, "intro", 60_000, 90_000, 0.9)
	path := fmt.Sprintf("/api/v1/admin/items/%d/markers/", f.episodeID)
	var out struct{ Markers []markerJSON }
	r := f.do("PUT", path+"intro", f.adminTok, map[string]any{"start_ms": 61_000, "end_ms": 91_500})
	r.json(t, &out)
	if r.code != 200 || len(out.Markers) != 1 || out.Markers[0].Source != "manual" || out.Markers[0].StartMs != 61_000 {
		t.Fatalf("set: %d %s", r.code, r.body)
	}
	for name, body := range map[string]map[string]any{
		"backwards":    {"start_ms": 9000, "end_ms": 8000},
		"past the end": {"start_ms": 0, "end_ms": 9_000_000},
		"missing end":  {"start_ms": 0},
	} {
		if r := f.do("PUT", path+"intro", f.adminTok, body); r.code != 400 {
			t.Errorf("%s: %d %s", name, r.code, r.body)
		}
	}
	if r := f.do("PUT", path+"recap", f.adminTok, map[string]any{"start_ms": 0, "end_ms": 1000}); r.code != 400 {
		t.Errorf("unknown kind: %d", r.code)
	}
	if r := f.do("PUT", fmt.Sprintf("/api/v1/admin/items/%d/markers/intro", f.showID), f.adminTok, map[string]any{"start_ms": 0, "end_ms": 1000}); r.code != 404 {
		t.Errorf("marker on a show: %d", r.code)
	}
	r = f.do("DELETE", path+"intro", f.adminTok, nil)
	r.json(t, &out)
	if r.code != 200 || len(out.Markers) != 0 {
		t.Fatalf("clear: %d %s", r.code, r.body)
	}
	// Cleared means cleared for viewers too.
	var item struct{ Markers []markerJSON }
	f.do("GET", fmt.Sprintf("/api/v1/items/%d", f.episodeID), f.friendTok, nil).json(t, &item)
	if len(item.Markers) != 0 {
		t.Errorf("cleared marker still served: %+v", item.Markers)
	}

	// Detection off: re-running answers 409; on, it queues.
	if r := f.do("POST", fmt.Sprintf("/api/v1/admin/items/%d/markers/detect", f.showID), f.adminTok, nil); r.code != 409 {
		t.Errorf("rerun with detection off: %d", r.code)
	}
	job := &fakeMarkerJob{}
	f.srv.Markers = job
	f.h = f.srv.Handler()
	if r := f.do("POST", fmt.Sprintf("/api/v1/admin/items/%d/markers/detect", f.showID), f.adminTok, nil); r.code != 202 {
		t.Errorf("rerun: %d %s", r.code, r.body)
	}
	if r := f.do("POST", path+"credits/apply-to-season", f.adminTok, nil); r.code != 202 {
		t.Errorf("apply to season: %d %s", r.code, r.body)
	}
	if fmt.Sprint(job.reruns, job.applied) != fmt.Sprintf("[%d] [%d/credits]", f.showID, f.episodeID) {
		t.Errorf("job got %v %v", job.reruns, job.applied)
	}

	var st struct {
		Enabled       bool `json:"enabled"`
		EpisodesTotal int  `json:"episodes_total"`
		EpisodesDone  int  `json:"episodes_done"`
		Shows         []struct {
			Title string `json:"title"`
		} `json:"shows"`
	}
	r = f.do("GET", "/api/v1/admin/markers", f.adminTok, nil)
	r.json(t, &st)
	if r.code != 200 || !st.Enabled || st.EpisodesTotal != 1 || st.EpisodesDone != 0 || len(st.Shows) != 1 {
		t.Errorf("status: %d %s", r.code, r.body)
	}
	_ = markers.ModeAuto
}
