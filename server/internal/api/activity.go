package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/pete-builds/vcrx-media-server/server/internal/activity"
	"github.com/pete-builds/vcrx-media-server/server/internal/notify"
)

// streamRegistry tracks open file responses by playback session, so stopping a
// session from Activity also cuts off the bytes already flowing to the device.
type streamRegistry struct {
	mu   sync.Mutex
	open map[string]map[*context.CancelFunc]bool
	// converted is each session's one converted (fMP4) stream. Direct play
	// may have several responses open at once (a browser's range requests);
	// a converted stream holds a conversion slot, so a session gets one.
	converted map[string]*context.CancelFunc
}

// addConverted registers a session's converted stream, stopping the one it
// replaces, and reports whether there was one. A seek, a retry or a second
// tab then never needs a second slot.
func (g *streamRegistry) addConverted(session string, cancel *context.CancelFunc) bool {
	g.mu.Lock()
	if g.converted == nil {
		g.converted = map[string]*context.CancelFunc{}
	}
	prev := g.converted[session]
	g.converted[session] = cancel
	g.mu.Unlock()
	if prev != nil {
		(*prev)()
	}
	g.add(session, cancel)
	return prev != nil
}

func (g *streamRegistry) add(session string, cancel *context.CancelFunc) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.open == nil {
		g.open = map[string]map[*context.CancelFunc]bool{}
	}
	if g.open[session] == nil {
		g.open[session] = map[*context.CancelFunc]bool{}
	}
	g.open[session][cancel] = true
}

func (g *streamRegistry) remove(session string, cancel *context.CancelFunc) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.open[session], cancel)
	if len(g.open[session]) == 0 {
		delete(g.open, session)
	}
	if g.converted[session] == cancel {
		delete(g.converted, session)
	}
}

// cancel stops every open response for a session and reports how many there were.
func (g *streamRegistry) cancel(session string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for c := range g.open[session] {
		(*c)()
		n++
	}
	return n
}

// ctxReader stops a ServeContent copy once its context is cancelled;
// ServeContent itself never looks at the request context.
type ctxReader struct {
	ctx context.Context
	rs  io.ReadSeeker
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.rs.Read(p)
}

func (c ctxReader) Seek(off int64, whence int) (int64, error) { return c.rs.Seek(off, whence) }

func (s *Server) activityNow(w http.ResponseWriter, r *http.Request) {
	out, err := s.Activity.NowPlaying(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) activityHistory(w http.ResponseWriter, r *http.Request) {
	refused, _ := strconv.ParseBool(r.URL.Query().Get("include_refused"))
	out, err := s.Activity.History(r.Context(), activity.HistoryFilter{
		UserID: int64(queryInt(r, "user_id", 0)), ItemID: int64(queryInt(r, "item_id", 0)), IncludeRefused: refused,
		Limit: queryInt(r, "limit", 50), Offset: queryInt(r, "offset", 0),
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) activityStats(w http.ResponseWriter, r *http.Request) {
	out, err := s.Activity.Stats(r.Context(), queryInt(r, "days", 30))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) activityUsers(w http.ResponseWriter, r *http.Request) {
	out, err := s.Activity.Users(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *Server) terminateSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message string `json:"message"`
	}
	if r.ContentLength != 0 && !readJSON(w, r, &req) {
		return
	}
	id := r.PathValue("session_id")
	by := info(r).Session.User.Username
	sess, err := s.Activity.Terminate(r.Context(), id, by, clip(strings.TrimSpace(req.Message), 200))
	if errors.Is(err, activity.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "No open playback session with that id.")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	cut := s.streams.cancel(id)
	if s.HLS != nil && s.HLS.Stop(id) {
		cut++
	}
	s.Log.Info("playback terminated", "session", id, "by", by, "open_streams", cut)
	if s.Webhooks != nil {
		s.Webhooks.Send(notify.Notice{Event: notify.Terminated, SessionID: id, Username: deref(sess.Username),
			Title: deref(sess.ItemTitle), Device: deref(sess.DeviceName), Decision: sess.Decision, Message: deref(sess.TerminatedMessage),
			PositionMs: sess.PositionMs, PlayedMs: sess.PlayedMs})
	}
	writeJSON(w, http.StatusOK, sess)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
