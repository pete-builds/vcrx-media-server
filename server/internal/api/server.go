// Package api is the HTTP surface: the versioned REST API under /api/v1, health
// checks, and the middleware that authenticates, logs and gates every request.
package api

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/activity"
	"github.com/pete-builds/vcrx-media-server/server/internal/admin"
	"github.com/pete-builds/vcrx-media-server/server/internal/arr"
	"github.com/pete-builds/vcrx-media-server/server/internal/artwork"
	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/enrich"
	"github.com/pete-builds/vcrx-media-server/server/internal/notify"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
	"github.com/pete-builds/vcrx-media-server/server/internal/subtitles"
)

// ScanStarter starts a scan in the background and returns its scan_runs id. It
// returns admin.ErrScanRunning when that library is already being scanned.
type ScanStarter interface {
	StartScan(ctx context.Context, libraryID int64, subPath, trigger string) (int64, error)
}

type Server struct {
	DB         *sql.DB
	Log        *slog.Logger
	Version    string
	ServerName string
	Auth       *auth.Store
	Limiter    *auth.Limiter
	Catalog    *catalog.Catalog
	Playback   *playback.Service
	Artwork    *artwork.Service
	Subtitles  *subtitles.Service
	Remux      *remux.Service
	// Arr turns Radarr and Sonarr webhooks into scans; ArrToken guards it.
	Arr      *arr.Dispatcher
	ArrToken string
	// Metadata, when TMDb is set up, backs Fix Match.
	Metadata *enrich.Enricher
	Activity *activity.Service
	Webhooks *notify.Webhooks
	Admin    *admin.Admin
	Scans    ScanStarter
	// LibraryRoots lists roots for the readiness check.
	LibraryRoots func(ctx context.Context) (map[string]string, error)

	AdminAllowRemote bool
	TrustedProxies   []netip.Prefix
	// AutoSignInUser, when set, is signed in without a password for clients on
	// TrustedNetworks (POST /api/v1/auth/auto).
	AutoSignInUser  string
	TrustedNetworks []netip.Prefix
	// AllowedHosts are names, besides IP addresses and localhost, that a
	// request's Host may carry. Anything else is refused (DNS rebinding).
	AllowedHosts []string

	// WebDir, when set, serves the web app from disk instead of the copy built
	// into the binary. For development only.
	WebDir string

	streams streamRegistry
}

type access int

const (
	public access = iota
	signedIn
	adminOnly
)

type route struct {
	Method, Path string
	access       access
	handler      func(http.ResponseWriter, *http.Request)
}

// Routes is the full route table. The OpenAPI drift test reads it.
func (s *Server) Routes() []route {
	return []route{
		{"GET", "/healthz", public, s.healthz},
		{"GET", "/readyz", public, s.readyz},
		{"GET", "/api/v1/openapi.yaml", public, s.openapi},
		{"GET", "/api/v1/server", public, s.serverInfo},
		{"POST", "/api/v1/auth/login", public, s.login},
		{"POST", "/api/v1/auth/auto", public, s.autoLogin},
		{"POST", "/api/v1/hooks/arr", public, s.arrHook},
		{"POST", "/api/v1/auth/logout", signedIn, s.logout},
		{"GET", "/api/v1/me", signedIn, s.me},
		{"GET", "/api/v1/home", signedIn, s.home},
		{"GET", "/api/v1/libraries", signedIn, s.libraries},
		{"GET", "/api/v1/libraries/{library_id}/items", signedIn, s.libraryItems},
		{"GET", "/api/v1/libraries/{library_id}/collections", signedIn, s.libraryCollections},
		{"GET", "/api/v1/libraries/{library_id}/filters", signedIn, s.libraryFilters},
		{"GET", "/api/v1/collections/{collection_id}", signedIn, s.collection},
		{"GET", "/api/v1/search", signedIn, s.search},
		{"GET", "/api/v1/items/{item_id}", signedIn, s.item},
		{"GET", "/api/v1/items/{item_id}/children", signedIn, s.itemChildren},
		{"PUT", "/api/v1/items/{item_id}/watched", signedIn, s.setWatched},
		{"GET", "/api/v1/items/{item_id}/artwork/{kind}", signedIn, s.artwork},
		{"POST", "/api/v1/playback/sessions", signedIn, s.startPlayback},
		{"POST", "/api/v1/playback/sessions/{session_id}/events", signedIn, s.playbackEvent},
		{"GET", "/api/v1/playback/sessions/{session_id}/seek", signedIn, s.convertedSeek},
		{"GET", "/api/v1/playback/sessions/{session_id}/stream.mp4", signedIn, s.convertedStream},
		{"HEAD", "/api/v1/playback/sessions/{session_id}/stream.mp4", signedIn, s.convertedStream},
		{"GET", "/api/v1/versions/{version_id}/file", signedIn, s.streamVersion},
		{"HEAD", "/api/v1/versions/{version_id}/file", signedIn, s.streamVersion},
		{"GET", "/api/v1/subtitles/{stream_id}", signedIn, s.subtitle},
		{"GET", "/api/v1/admin/libraries", adminOnly, s.adminLibraries},
		{"POST", "/api/v1/admin/libraries", adminOnly, s.adminCreateLibrary},
		{"POST", "/api/v1/admin/libraries/{library_id}/scan", adminOnly, s.adminScan},
		{"GET", "/api/v1/admin/scans", adminOnly, s.adminScans},
		{"GET", "/api/v1/admin/users", adminOnly, s.adminUsers},
		{"POST", "/api/v1/admin/users", adminOnly, s.adminCreateUser},
		{"PUT", "/api/v1/admin/users/{user_id}/grants", adminOnly, s.adminSetGrants},
		{"PUT", "/api/v1/admin/users/{user_id}/policy", adminOnly, s.adminSetPolicy},
		{"GET", "/api/v1/admin/labels", adminOnly, s.adminLabels},
		{"GET", "/api/v1/admin/activity/sessions", adminOnly, s.adminSessions},
		{"GET", "/api/v1/admin/metadata/unmatched", adminOnly, s.unmatched},
		{"GET", "/api/v1/admin/items/{item_id}/match", adminOnly, s.matchSearch},
		{"PUT", "/api/v1/admin/items/{item_id}/match", adminOnly, s.matchSet},
		{"GET", "/api/v1/admin/activity/now", adminOnly, s.activityNow},
		{"GET", "/api/v1/admin/activity/history", adminOnly, s.activityHistory},
		{"GET", "/api/v1/admin/activity/stats", adminOnly, s.activityStats},
		{"GET", "/api/v1/admin/activity/users", adminOnly, s.activityUsers},
		{"POST", "/api/v1/admin/activity/sessions/{session_id}/terminate", adminOnly, s.terminateSession},
	}
}

// Handler builds the mux with middleware applied.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.Routes() {
		mux.Handle(rt.Method+" "+rt.Path, s.gate(rt))
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "No such API route.")
	})
	mux.Handle("/", s.webApp())
	return s.recoverer(s.logRequests(s.checkHost(mux)))
}

// HTTPServer wraps Handler with timeouts that suit long range requests: no write
// timeout, since a stream may run for hours, but bounded header reads.
func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
}
