package api

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/preroll"
)

// beforeClip is one thing to play before a movie, in order. URL plays it
// directly (the household's own devices); nothing it plays touches watch state.
type beforeClip struct {
	Kind  string `json:"kind"` // "trailer" or "preroll"
	Title string `json:"title"`
	URL   string `json:"url"`
	// A trailer's own movie, so a client can say what it is for.
	Trailer *catalog.BeforeTrailer `json:"trailer,omitempty"`
}

// beforeMovie lists what the caller's Before a movie settings play ahead of
// a movie: trailers for other unwatched titles they may see, then a pre-roll.
// Empty for anything but a movie, and for an invited device, which plays only
// inside a playback session and never a bare file.
func (s *Server) beforeMovie(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok {
		return
	}
	ri := info(r)
	ctx := r.Context()
	d, err := s.Catalog.Item(ctx, ri.Policy, ri.Session.User.ID, id)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	clips := []beforeClip{}
	if d.Kind != "movie" || ri.Session.TokenID != 0 {
		writeJSON(w, http.StatusOK, map[string]any{"clips": clips})
		return
	}
	set, err := s.readPlaybackSettings(ctx, ri.Session.User.ID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	trailers, err := s.Catalog.BeforeTrailers(ctx, ri.Policy, ri.Session.User.ID, id, set.BeforeMovieTrailers)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	for i := range trailers {
		t := trailers[i]
		clips = append(clips, beforeClip{Kind: "trailer", Title: t.ItemTitle, URL: "/api/v1/extras/" + strconv.FormatInt(t.ExtraID, 10) + "/file", Trailer: &t})
	}
	if set.BeforeMoviePreroll {
		if name := s.Prerolls.Pick(); name != "" {
			clips = append(clips, beforeClip{Kind: "preroll", Title: preroll.Title(name), URL: "/api/v1/preroll?name=" + url.QueryEscape(name)})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"clips": clips})
}

// prerollFile serves one clip from preroll_dir by its listed name. The clips
// are the admin's own bumpers, not library titles, but an invited device still
// gets no bare file from any route (TestNoTokenRouteServesAWholeFile).
func (s *Server) prerollFile(w http.ResponseWriter, r *http.Request) {
	if !s.streamOnly(w, r, "", func(playback.StreamSession) bool { return false }) {
		return
	}
	f, fi, err := s.Prerolls.Open(r.URL.Query().Get("name"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "No pre-roll clip by that name.")
		return
	}
	defer func() { _ = f.Close() }()
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}
