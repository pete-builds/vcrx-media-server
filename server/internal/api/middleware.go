package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

type ctxKey int

const reqKey ctxKey = 0

// reqInfo is what the middleware learns about a request.
type reqInfo struct {
	IP      string
	Session auth.Session
	Policy  policy.Policy
	Authed  bool
}

func info(r *http.Request) *reqInfo {
	if v, ok := r.Context().Value(reqKey).(*reqInfo); ok {
		return v
	}
	return &reqInfo{}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": code, "message": msg})
}

// readJSON decodes a bounded request body and rejects unknown fields.
func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", "Invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// clientIP is the TCP peer, or the left-most untrusted hop of X-Forwarded-For
// when the peer is a configured reverse proxy.
func (s *Server) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	if !s.trusted(peer) {
		return peer.String()
	}
	// Every X-Forwarded-For header, in order; the right-most untrusted hop is
	// the client. When no hop parses, the client is unknown, never the proxy
	// itself: answering with the proxy's own (often loopback or private)
	// address would make every client behind it look local.
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	last := ""
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return unknownClient
		}
		a = a.Unmap()
		if !s.trusted(a) {
			return a.String()
		}
		last = a.String()
	}
	if last == "" {
		return unknownClient
	}
	return last
}

// unknownClient stands in for a client address that cannot be known. It
// parses as no address, so it is never LAN and never trusted.
const unknownClient = "unknown"

// checkHost refuses requests for a host name the server does not answer to.
// IP addresses and localhost always pass; other names must be in AllowedHosts.
// This stops DNS rebinding, where a page from another site points its own
// name at this server and then calls it as if same-origin.
func (s *Server) checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			writeError(w, http.StatusMisdirectedRequest, "host_not_allowed",
				"This server does not answer to that name. Open it by its IP address, or add the name to allowed_hosts.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hostAllowed(host string) bool {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.TrimSuffix(strings.ToLower(strings.Trim(h, "[]")), ".")
	if h == "" || h == "localhost" {
		return h == "localhost"
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	for _, a := range s.AllowedHosts {
		if strings.EqualFold(strings.TrimSuffix(a, "."), h) {
			return true
		}
	}
	return false
}

func (s *Server) trusted(a netip.Addr) bool {
	for _, p := range s.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// IsLAN covers loopback, RFC 1918, IPv6 unique local and link-local addresses.
func IsLAN(ip string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast()
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// gate authenticates, authorizes and stores request info before the handler.
func (s *Server) gate(rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ri := &reqInfo{IP: s.clientIP(r)}
		r = r.WithContext(context.WithValue(r.Context(), reqKey, ri))
		if rt.access == public {
			rt.handler(w, r)
			return
		}
		if rt.access == adminOnly && !s.AdminAllowRemote && !IsLAN(ri.IP) {
			writeError(w, http.StatusForbidden, "lan_only", "Admin actions are only available on the local network.")
			return
		}
		tok, viaCookie := sessionToken(r)
		if tok == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Sign in first.")
			return
		}
		if viaCookie && !safeMethod(r.Method) && r.Header.Get(csrfHeader) == "" {
			writeError(w, http.StatusForbidden, "csrf", "Missing the "+csrfHeader+" header.")
			return
		}
		sess, err := s.Auth.Authenticate(r.Context(), tok, ri.IP)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidToken) {
				writeError(w, http.StatusUnauthorized, "unauthorized", "Your session has expired. Sign in again.")
				return
			}
			s.internal(w, r, err)
			return
		}
		if rt.access == adminOnly && !sess.User.IsAdmin {
			writeError(w, http.StatusForbidden, "forbidden", "Only an admin can do that.")
			return
		}
		pol, err := policy.Load(r.Context(), s.DB, sess.User.ID, sess.User.IsAdmin)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		ri.Session, ri.Policy, ri.Authed = sess, pol, true
		rt.handler(w, r)
	})
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal", "Something went wrong on the server.")
}

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

// ReadFrom keeps http.ServeContent on its sendfile fast path.
func (w *statusWriter) ReadFrom(r io.Reader) (int64, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := io.Copy(w.ResponseWriter, r)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logRequests writes one structured line per request. Query strings are left out
// so tokens or search terms never land in logs; health checks log at debug.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		level := s.Log.Info
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || strings.Contains(r.URL.Path, "/artwork/") {
			level = s.Log.Debug
		}
		level("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "bytes", sw.bytes,
			"ms", time.Since(start).Milliseconds(), "ip", s.clientIP(r), "range", r.Header.Get("Range"))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				s.Log.Error("panic", "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal", "Something went wrong on the server.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
