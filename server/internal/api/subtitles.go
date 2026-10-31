package api

import (
	"database/sql"
	"net/http"
	"os"
	"path/filepath"

	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/subtitles"
)

func (s *Server) subtitle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "stream_id")
	if !ok {
		return
	}
	vtt := r.URL.Query().Get("format") == "vtt"
	var versionID int64
	var index sql.NullInt64
	var codec, ext *string
	var external, imageBased bool
	if err := s.DB.QueryRowContext(r.Context(), `SELECT version_id, stream_index, codec, external_path, is_external, is_image_based
		FROM media_streams WHERE id = ? AND kind = 'subtitle'`, id).Scan(&versionID, &index, &codec, &ext, &external, &imageBased); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	if (external && ext == nil) || (!external && (!vtt || !index.Valid)) {
		// Embedded tracks are only served as WebVTT; the raw ones are in the stream.
		writeError(w, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	v, root, err := s.Catalog.VersionForPlayback(r.Context(), info(r).Policy, versionID)
	if err != nil {
		s.notFoundOr(w, r, err)
		return
	}
	if vtt && (imageBased || codec == nil || !subtitles.TextCodec(*codec) || s.Subtitles == nil) {
		writeError(w, http.StatusUnprocessableEntity, "not_text", "This subtitle is a picture format and cannot be shown as text.")
		return
	}
	if !external {
		s.embeddedSubtitle(w, r, v, root, index.Int64, id)
		return
	}
	if vtt {
		src, err := fsafe.ResolveUnder(root, *ext)
		if err != nil {
			writeError(w, http.StatusNotFound, "file_unavailable", "The subtitle file cannot be read right now.")
			return
		}
		out, err := s.Subtitles.Sidecar(r.Context(), src)
		if err != nil {
			s.Log.Warn("subtitle conversion failed", "stream", id, "err", err)
			writeError(w, http.StatusUnprocessableEntity, "conversion_failed", "This subtitle file could not be converted.")
			return
		}
		serveVTT(w, r, out)
		return
	}
	_, mime, ok := playback.SubtitleMime(*ext)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "Unsupported subtitle format.")
		return
	}
	f, fi, err := fsafe.OpenUnder(root, *ext)
	if err != nil {
		writeError(w, http.StatusNotFound, "file_unavailable", "The subtitle file cannot be read right now.")
		return
	}
	defer func() { _ = f.Close() }()
	if fi.Size() > 20<<20 {
		writeError(w, http.StatusNotFound, "file_unavailable", "Subtitle file too large.")
		return
	}
	w.Header().Set("Content-Type", mime+"; charset=utf-8")
	http.ServeContent(w, r, filepath.Base(*ext), fi.ModTime(), f)
}

// embeddedSubtitle serves a track from inside the media file as WebVTT. The first
// request starts one pass over the file for every text track and answers 202.
func (s *Server) embeddedSubtitle(w http.ResponseWriter, r *http.Request, v catalog.Version, root string, index, streamID int64) {
	src, err := fsafe.ResolveUnder(root, v.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "file_unavailable", "The media file cannot be read right now.")
		return
	}
	var all []subtitles.Track
	for _, t := range v.SubtitleTracks {
		if !t.IsExternal && !t.IsImageBased && t.Index != nil && t.Codec != nil && subtitles.TextCodec(*t.Codec) {
			all = append(all, subtitles.Track{StreamID: t.ID, Index: *t.Index})
		}
	}
	out, ready, err := s.Subtitles.Embedded(src, subtitles.Track{StreamID: streamID, Index: index}, all)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "conversion_failed", "This subtitle track could not be extracted.")
		return
	}
	if !ready {
		w.Header().Set("Retry-After", "5")
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "preparing",
			"message": "Reading the subtitles out of the file. Large files take a few minutes the first time."})
		return
	}
	serveVTT(w, r, out)
}

func serveVTT(w http.ResponseWriter, r *http.Request, path string) {
	f, err := os.Open(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "file_unavailable", "The subtitle file cannot be read right now.")
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusNotFound, "file_unavailable", "The subtitle file cannot be read right now.")
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "subtitles.vtt", fi.ModTime(), f)
}
