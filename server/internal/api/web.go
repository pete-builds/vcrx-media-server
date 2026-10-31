package api

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/web"
)

// cookieName carries the session token for the web app. Browsers cannot add an
// Authorization header to <video>, <track> or <img> requests, so the web app
// signs in with a cookie instead of keeping the token in script.
const cookieName = "vcrx_session"

// csrfHeader must accompany every cookie-authenticated request that changes
// something. A cross-site form cannot set it, and a cross-site fetch that sets
// it needs a CORS preflight this server never approves.
const csrfHeader = "X-VCRX-Client"

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	// VCRX serves plain HTTP on the LAN, where a Secure cookie would never come
	// back; Secure follows the connection instead.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // see above
		Name: cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil, MaxAge: int((400 * 24 * time.Hour).Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure follows the connection, as in setSessionCookie
		Name: cookieName, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: -1,
	})
}

// sessionToken returns the bearer token, or else the session cookie. cookie is
// true when the cookie was used.
func sessionToken(r *http.Request) (tok string, cookie bool) {
	if t := bearer(r); t != "" {
		return t, false
	}
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value, true
	}
	return "", false
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// webApp serves the browser client for every path outside /api/ and the health
// checks. Unknown paths without a file extension get index.html so a reload
// on a deep link still works; a missing file is a 404.
func (s *Server) webApp() http.Handler {
	sub, err := fs.Sub(web.Static, "static")
	if err != nil {
		panic(err)
	}
	if s.WebDir != "" {
		// Development: serve the files from disk so an edit shows on reload.
		sub = os.DirFS(s.WebDir)
	}
	tags := etags(sub)
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !safeMethod(r.Method) {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed.")
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", s.contentSecurityPolicy())
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" || strings.HasSuffix(p, "/") {
			p += "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			// A missing file (a script, style, image or font) is a 404. Answering
			// it with the page instead hides a broken build behind a 200: the
			// browser only reports a MIME error, and a probe sees success.
			if path.Ext(p) != "" {
				http.Error(w, "Not found.", http.StatusNotFound)
				return
			}
			r = r.Clone(r.Context())
			r.URL.Path = "/"
			p = "index.html"
		}
		// Embedded files carry no modification time, so revalidate by content
		// hash: a deploy is picked up on the next load, and an unchanged file
		// costs a 304. On disk (development) the hash is recomputed per request.
		tag := tags[p]
		if s.WebDir != "" {
			tag = etag(sub, p)
		}
		if tag != "" {
			h.Set("ETag", tag)
		}
		h.Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	})
}

// youTubeEmbed is the only frame the web app ever shows: a YouTube trailer in
// YouTube's no-cookie player, and only when the admin turned trailers_youtube on.
const youTubeEmbed = "https://www.youtube-nocookie.com"

// contentSecurityPolicy keeps the web app to its own origin. With YouTube
// trailers on, it adds exactly one frame source (which also stops same-origin
// frames, unused anyway); off, it is the policy VCRX always sent.
func (s *Server) contentSecurityPolicy() string {
	frames := ""
	if s.TrailersYouTube {
		frames = "frame-src " + youTubeEmbed + "; "
	}
	return "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; " +
		"style-src 'self'; script-src 'self'; connect-src 'self'; font-src 'self'; object-src 'none'; " +
		frames + "base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
}

func etags(fsys fs.FS) map[string]string {
	out := map[string]string{}
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out[p] = etag(fsys, p)
		}
		return nil
	})
	return out
}

func etag(fsys fs.FS, p string) string {
	b, err := fs.ReadFile(fsys, p)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:10]) + `"`
}
