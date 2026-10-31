// Package ostest is an httptest stand-in for the OpenSubtitles REST API,
// answering from fixture JSON in the documented shapes. Tests use it so that
// nothing ever calls the real API: Client returns a client whose transport
// refuses every host but the stand-in.
package ostest

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/opensubtitles"
)

//go:embed testdata/*.json
var fixtures embed.FS

// Fixture returns one testdata file.
func Fixture(name string) []byte {
	b, err := fixtures.ReadFile("testdata/" + name)
	if err != nil {
		panic(err)
	}
	return b
}

// Request is what the stand-in saw.
type Request struct {
	Method, Path string
	RawQuery     string
	APIKey, Auth string
	UserAgent    string
}

// Content is the subtitle every download link serves.
const Content = "1\n00:00:01,000 --> 00:00:03,000\nFixture line one\n\n2\n00:00:04,000 --> 00:00:06,000\nFixture line two\n"

type Server struct {
	*httptest.Server
	t *testing.T

	mu sync.Mutex
	// Remaining is the download quota; at 0 a download answers 406.
	Remaining int
	// DownloadName, when set, replaces file_name in the download answer.
	DownloadName string
	// Down makes every API call answer 503; RateLimit makes them answer 429.
	Down, RateLimit bool
	// NoIDResults makes id searches find nothing, so the title stage runs.
	NoIDResults bool
	// LoginCount and the request log are for assertions.
	LoginCount int
	requests   []Request
	// tokens issued; the first is expired once ExpireFirstToken is set.
	ExpireFirstToken bool
}

func New(t *testing.T) *Server {
	s := &Server{t: t, Remaining: 20}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// BaseURL is the API base to give a Client.
func (s *Server) BaseURL() string { return s.URL + "/api/v1" }

// Client returns an opensubtitles client pointed at the stand-in. Its HTTP
// transport fails any request to another host, so a wrong base URL can never
// reach the real API from a test.
func (s *Server) Client(user, pass string) *opensubtitles.Client {
	c := opensubtitles.New("fixture-api-key", user, pass, "VCRX vtest")
	c.BaseURL = s.BaseURL()
	c.MinInterval = 1
	host := strings.TrimPrefix(s.URL, "http://")
	inner := s.Server.Client().Transport
	c.HTTP = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != host {
			return nil, errors.New("ostest: refused a request to " + r.URL.Host)
		}
		return inner.RoundTrip(r)
	})}
	return c
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Requests returns the requests seen so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Count returns how many requests went to a path.
func (s *Server) Count(path string) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Path == path {
			n++
		}
	}
	return n
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery,
		APIKey: r.Header.Get("Api-Key"), Auth: r.Header.Get("Authorization"), UserAgent: r.Header.Get("User-Agent")})
	down, limited := s.Down, s.RateLimit
	s.mu.Unlock()

	if strings.HasPrefix(r.URL.Path, "/files/") {
		w.Header().Set("Content-Type", "application/x-subrip")
		_, _ = w.Write([]byte(Content))
		return
	}
	switch {
	case down:
		http.Error(w, "upstream down", http.StatusServiceUnavailable)
		return
	case limited:
		w.Header().Set("Retry-After", "30")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"message": "Throttle limit reached. Retry later."})
		return
	case r.Header.Get("Api-Key") != "fixture-api-key":
		writeJSON(w, http.StatusForbidden, map[string]any{"message": "You cannot consume this service"})
		return
	}
	switch r.Method + " " + r.URL.Path {
	case "POST /api/v1/login":
		var body struct{ Username, Password string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Username != "viewer" || body.Password != "secret" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "Error, invalid username/password", "status": 401})
			return
		}
		s.mu.Lock()
		s.LoginCount++
		n := s.LoginCount
		s.mu.Unlock()
		var m map[string]any
		_ = json.Unmarshal(Fixture("login.json"), &m)
		m["token"] = fmt.Sprintf("fixture-token-%d", n)
		writeJSON(w, http.StatusOK, m)
	case "GET /api/v1/subtitles":
		q := r.URL.Query()
		switch {
		case q.Get("moviehash") != "":
			serveRaw(w, Fixture("search_hash.json"))
		case q.Get("imdb_id") != "" || q.Get("tmdb_id") != "" || q.Get("parent_imdb_id") != "" || q.Get("parent_tmdb_id") != "":
			if s.NoIDResults {
				serveRaw(w, Fixture("search_empty.json"))
				return
			}
			serveRaw(w, Fixture("search_id.json"))
		case q.Get("query") != "":
			serveRaw(w, Fixture("search_title.json"))
		default:
			writeJSON(w, http.StatusBadRequest, map[string]any{"message": "missing search parameters"})
		}
	case "POST /api/v1/download":
		if !s.authorised(w, r) {
			return
		}
		var body struct {
			FileID int64 `json:"file_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		if s.Remaining <= 0 {
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotAcceptable)
			_, _ = w.Write(Fixture("download_quota.json"))
			return
		}
		s.Remaining--
		left, name := s.Remaining, s.DownloadName
		s.mu.Unlock()
		var m map[string]any
		_ = json.Unmarshal(Fixture("download.json"), &m)
		m["link"] = s.URL + "/files/" + url.PathEscape(fmt.Sprint(body.FileID)) + ".srt"
		m["remaining"] = left
		m["requests"] = 20 - left
		if name != "" {
			m["file_name"] = name
		}
		writeJSON(w, http.StatusOK, m)
	case "GET /api/v1/infos/user":
		if !s.authorised(w, r) {
			return
		}
		var m map[string]any
		_ = json.Unmarshal(Fixture("infos_user.json"), &m)
		s.mu.Lock()
		m["data"].(map[string]any)["remaining_downloads"] = s.Remaining
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, m)
	default:
		writeJSON(w, http.StatusNotFound, map[string]any{"message": "no such route"})
	}
}

func (s *Server) authorised(w http.ResponseWriter, r *http.Request) bool {
	s.mu.Lock()
	expired := s.ExpireFirstToken && r.Header.Get("Authorization") == "Bearer fixture-token-1"
	s.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer fixture-token-") || expired {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"message": "invalid token"})
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func serveRaw(w http.ResponseWriter, b []byte) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}
