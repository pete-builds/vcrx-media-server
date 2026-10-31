package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/activity"
)

// itemHistory is Tautulli's item page: a title's plays and totals.
func (s *Server) itemHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "item_id")
	if !ok {
		return
	}
	out, err := s.Activity.ItemHistory(r.Context(), id, queryInt(r, "limit", 20), queryInt(r, "offset", 0))
	if errors.Is(err, activity.ErrNoItem) {
		writeError(w, http.StatusNotFound, "not_found", "No movie, show or episode with that id.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// watchExport streams every movie and show with its files and watch state, for
// a cleanup tool deciding what to delete. The closing bracket is written only
// after the last row, so an export cut short never parses as a complete one.
func (s *Server) watchExport(w http.ResponseWriter, r *http.Request) {
	var f activity.ExportFilter
	for name, dst := range map[string]*int64{"library_id": &f.LibraryID} {
		if v := r.URL.Query().Get(name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 {
				writeError(w, http.StatusBadRequest, "bad_request", name+" must be a positive whole number.")
				return
			}
			*dst = n
		}
	}
	if v := r.URL.Query().Get("unplayed_days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 36500 {
			writeError(w, http.StatusBadRequest, "bad_request", "unplayed_days must be a whole number of days, 0 or more.")
			return
		}
		f.UnplayedDays = n
	}
	var bw *bufio.Writer
	var enc *json.Encoder
	count := 0
	err := s.Activity.WatchExport(r.Context(), f, func(t activity.TitleWatch) error {
		if bw == nil {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusOK)
			bw = bufio.NewWriterSize(w, 64<<10)
			enc = json.NewEncoder(bw)
			if _, err := bw.WriteString(`{"generated_at":` + strconv.FormatInt(time.Now().UnixMilli(), 10) + `,"items":[`); err != nil {
				return err
			}
		}
		if count > 0 {
			if err := bw.WriteByte(','); err != nil {
				return err
			}
		}
		count++
		return enc.Encode(t)
	})
	if errors.Is(err, activity.ErrNoLibrary) {
		writeError(w, http.StatusNotFound, "not_found", "No library with that id.")
		return
	}
	if err != nil {
		if bw == nil {
			s.internal(w, r, err)
			return
		}
		// Rows already went out: leave the JSON unterminated rather than end it
		// as if it were complete.
		s.Log.Warn("watch export cut short", "rows", count, "err", err)
		_ = bw.Flush()
		return
	}
	if bw == nil {
		writeJSON(w, http.StatusOK, map[string]any{"generated_at": time.Now().UnixMilli(), "items": []activity.TitleWatch{}, "count": 0})
		return
	}
	_, _ = bw.WriteString(`],"count":` + strconv.Itoa(count) + "}\n")
	_ = bw.Flush()
}
