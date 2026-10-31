// Package api is the HTTP surface: the versioned REST API under /api/v1, health
// checks, and the middleware that authenticates, logs and gates every request.
package api

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/activity"
	"github.com/pete-builds/vcrx-media-server/server/internal/admin"
	"github.com/pete-builds/vcrx-media-server/server/internal/arr"
	"github.com/pete-builds/vcrx-media-server/server/internal/artwork"
	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/collections"
	"github.com/pete-builds/vcrx-media-server/server/internal/enrich"
	"github.com/pete-builds/vcrx-media-server/server/internal/federation"
	"github.com/pete-builds/vcrx-media-server/server/internal/household"
	"github.com/pete-builds/vcrx-media-server/server/internal/invites"
	"github.com/pete-builds/vcrx-media-server/server/internal/loans"
	"github.com/pete-builds/vcrx-media-server/server/internal/markers"
	"github.com/pete-builds/vcrx-media-server/server/internal/newsletter"
	"github.com/pete-builds/vcrx-media-server/server/internal/notify"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/preroll"
	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
	"github.com/pete-builds/vcrx-media-server/server/internal/subsearch"
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
	// HLS writes converted streams as HLS for clients that report
	// hls_stream (Safari). Nil turns it off.
	HLS *remux.HLS
	// SubSearch finds and downloads missing subtitles; nil or without a
	// provider, search is off.
	SubSearch *subsearch.Service
	// Arr turns Radarr and Sonarr webhooks into scans; ArrToken guards it.
	Arr      *arr.Dispatcher
	ArrToken string
	// Metadata, when TMDb is set up, backs Fix Match.
	Metadata *enrich.Enricher
	Activity *activity.Service
	Webhooks *notify.Webhooks
	Admin    *admin.Admin
	Scans    ScanStarter
	// Markers re-runs and copies intro and credits detection; nil when it
	// is off. MarkerHours is its window, for the admin status page.
	Markers     MarkerJob
	MarkerHours markers.Hours
	// LibraryRoots lists roots for the readiness check.
	LibraryRoots func(ctx context.Context) (map[string]string, error)

	// SmartCollections reports the YAML collection definitions and their builds.
	SmartCollections *collections.SmartBuilder

	// Newsletter is the weekly recently-added digest, set up in Settings.
	Newsletter *newsletter.Service

	// Music serves music libraries (music.go).
	Music *Music

	AdminAllowRemote bool
	TrustedProxies   []netip.Prefix
	// AutoSignInUser, when set, is signed in without a password for clients on
	// TrustedNetworks (POST /api/v1/auth/auto).
	AutoSignInUser  string
	TrustedNetworks []netip.Prefix
	// ProfilePicker sends trusted-network clients to the "Who's watching?"
	// picker instead of signing them in as AutoSignInUser.
	ProfilePicker bool
	// Household backs profile switching; PINs counts PIN attempts. Passwords
	// offered at a switch count against Limiter, as sign-ins do.
	Household *household.Store
	PINs      *household.Limiter
	// switchLocks serializes password checks per profile (*sync.Mutex by id),
	// so the sign-in limiter's check and its count cannot be raced.
	switchLocks sync.Map
	// Invites are invited devices (the invite is the account); Pairings are
	// TVs waiting for a phone to approve them. PairLimiter counts pairing
	// starts, polls with unknown codes, and wrong codes typed by an admin.
	Invites     *invites.Store
	Pairings    *invites.Pairings
	PairLimiter *auth.Limiter
	// Loans are lent tapes: one movie, on a printed card, for a day to a week.
	Loans *loans.Store
	// AllowedHosts are names, besides IP addresses and localhost, that a
	// request's Host may carry. Anything else is refused (DNS rebinding).
	AllowedHosts []string

	// WebDir, when set, serves the web app from disk instead of the copy built
	// into the binary. For development only.
	WebDir string

	// TrailersYouTube is the trailers_youtube setting with a TMDb key present:
	// details may offer a YouTube trailer, and the web app's CSP allows that
	// one embed. Off, the CSP allows no frames at all.
	TrailersYouTube bool

	// Prerolls is preroll_dir: clips a profile may see before a movie.
	Prerolls preroll.Folder

	// Friends' servers (milestone 9, item 3), set up by SetUpFederation.
	// Peers are the servers this one has linked to; PeerClient talks to them
	// and to nothing else; PeerCache keeps their catalogs for Home.
	Peers      *federation.Store
	PeerClient *federation.Client
	PeerCache  *federation.Cache

	streams streamRegistry
	// buckets hold each linked server's bandwidth cap, shared by all its
	// streams; peerSessions are this server's own users' streams from peers.
	buckets      bandwidthBuckets
	peerSessions peerSessionTable
}

// SetUpFederation turns on linking with friends' servers.
func (s *Server) SetUpFederation(g federation.Guard) {
	s.Peers = &federation.Store{DB: s.DB}
	s.PeerClient = federation.NewClient(g, "VCRX/"+s.Version)
	s.PeerCache = &federation.Cache{TTL: 5 * time.Minute, Fetch: s.fetchPeerCatalog}
}

type access int

const (
	public access = iota
	signedIn
	adminOnly
	// householdOnly routes serve the profile picker: a household member's session,
	// or no session at all from a trusted network when the picker is on.
	householdOnly
	// peerOnly routes answer only a linked friend's server: its catalog and
	// its own unlinking. Everything else a peer does goes through the
	// ordinary signed-in routes, under its policy like anyone's.
	peerOnly
	// friendsServers routes show this server's household what linked
	// friends' servers share: never to an invited device, a linked server or
	// an account outside the household, so a link is never passed on.
	friendsServers
)

type route struct {
	Method, Path string
	access       access
	handler      func(http.ResponseWriter, *http.Request)
}

// Routes is the full route table. The OpenAPI drift test reads it.
func (s *Server) Routes() []route {
	return append([]route{
		{"GET", "/healthz", public, s.healthz},
		{"GET", "/readyz", public, s.readyz},
		{"GET", "/api/v1/openapi.yaml", public, s.openapi},
		{"GET", "/api/v1/server", public, s.serverInfo},
		{"POST", "/api/v1/auth/login", public, s.login},
		{"POST", "/api/v1/auth/auto", public, s.autoLogin},
		{"GET", "/api/v1/profiles", householdOnly, s.profiles},
		{"GET", "/api/v1/profiles/{user_id}/avatar", householdOnly, s.profileAvatar},
		{"POST", "/api/v1/auth/switch", householdOnly, s.switchProfile},
		{"POST", "/api/v1/hooks/arr", public, s.arrHook},
		{"POST", "/api/v1/invites/redeem", public, s.redeemInvite},
		{"POST", "/api/v1/pair", public, s.pairStart},
		{"POST", "/api/v1/pair/poll", public, s.pairPoll},
		// Federation, the serving side: a friend's server links with a
		// server invite, reads what its scope shares, and may unlink itself.
		{"POST", "/api/v1/federation/link", public, s.federationLink},
		{"GET", "/api/v1/federation/catalog", peerOnly, s.federationCatalog},
		{"DELETE", "/api/v1/federation/link", peerOnly, s.federationUnlink},
		{"POST", "/api/v1/auth/logout", signedIn, s.logout},
		{"GET", "/api/v1/me", signedIn, s.me},
		{"GET", "/api/v1/me/playback-settings", signedIn, s.playbackSettings},
		{"PUT", "/api/v1/me/playback-settings", signedIn, s.setPlaybackSettings},
		{"GET", "/api/v1/home", signedIn, s.home},
		{"GET", "/api/v1/rows/{row_id}", signedIn, s.homeRow},
		{"GET", "/api/v1/layout/home", signedIn, s.homeLayout},
		{"PUT", "/api/v1/layout/home", signedIn, s.setHomeLayout},
		{"DELETE", "/api/v1/layout/home", signedIn, s.resetHomeLayout},
		{"GET", "/api/v1/libraries", signedIn, s.libraries},
		{"GET", "/api/v1/libraries/{library_id}/items", signedIn, s.libraryItems},
		{"GET", "/api/v1/libraries/{library_id}/collections", signedIn, s.libraryCollections},
		{"GET", "/api/v1/libraries/{library_id}/filters", signedIn, s.libraryFilters},
		{"GET", "/api/v1/collections/{collection_id}", signedIn, s.collection},
		{"GET", "/api/v1/search", signedIn, s.search},
		{"GET", "/api/v1/items/{item_id}", signedIn, s.item},
		{"GET", "/api/v1/items/{item_id}/children", signedIn, s.itemChildren},
		{"GET", "/api/v1/items/{item_id}/before", signedIn, s.beforeMovie},
		{"GET", "/api/v1/preroll", signedIn, s.prerollFile},
		{"HEAD", "/api/v1/preroll", signedIn, s.prerollFile},
		{"PUT", "/api/v1/items/{item_id}/watched", signedIn, s.setWatched},
		{"DELETE", "/api/v1/items/{item_id}/progress", signedIn, s.clearProgress},
		{"GET", "/api/v1/items/{item_id}/artwork/{kind}", signedIn, s.artwork},
		{"POST", "/api/v1/playback/sessions", signedIn, s.startPlayback},
		{"POST", "/api/v1/playback/sessions/{session_id}/events", signedIn, s.playbackEvent},
		{"GET", "/api/v1/playback/sessions/{session_id}/seek", signedIn, s.convertedSeek},
		{"GET", "/api/v1/playback/sessions/{session_id}/stream.mp4", signedIn, s.convertedStream},
		{"HEAD", "/api/v1/playback/sessions/{session_id}/stream.mp4", signedIn, s.convertedStream},
		{"GET", "/api/v1/playback/sessions/{session_id}/hls/{run}/index.m3u8", signedIn, s.hlsPlaylist},
		{"HEAD", "/api/v1/playback/sessions/{session_id}/hls/{run}/index.m3u8", signedIn, s.hlsPlaylist},
		{"GET", "/api/v1/playback/sessions/{session_id}/hls/{run}/{segment}", signedIn, s.hlsSegment},
		{"GET", "/api/v1/versions/{version_id}/file", signedIn, s.streamVersion},
		{"HEAD", "/api/v1/versions/{version_id}/file", signedIn, s.streamVersion},
		{"GET", "/api/v1/extras/{extra_id}/file", signedIn, s.streamExtra},
		{"HEAD", "/api/v1/extras/{extra_id}/file", signedIn, s.streamExtra},
		{"GET", "/api/v1/subtitles/{stream_id}", signedIn, s.subtitle},
		// Federation, the watching side: friends' servers' titles on Home,
		// their pages, and their streams, proxied (peers.go).
		{"GET", "/api/v1/peers/home", friendsServers, s.peersHome},
		{"GET", "/api/v1/peers/{peer_id}/items/{item_id}", friendsServers, s.peerItem},
		{"GET", "/api/v1/peers/{peer_id}/items/{item_id}/children", friendsServers, s.peerChildren},
		{"GET", "/api/v1/peers/{peer_id}/items/{item_id}/artwork/{kind}", friendsServers, s.peerArtwork},
		{"POST", "/api/v1/peers/{peer_id}/playback/sessions", friendsServers, s.peerStartPlayback},
		{"POST", "/api/v1/peers/{peer_id}/playback/sessions/{session_id}/events", friendsServers, s.peerPlaybackEvent},
		{"GET", "/api/v1/peers/{peer_id}/playback/sessions/{session_id}/seek", friendsServers, s.peerSeek},
		{"GET", "/api/v1/peers/{peer_id}/playback/sessions/{session_id}/file", friendsServers, s.peerFile},
		{"HEAD", "/api/v1/peers/{peer_id}/playback/sessions/{session_id}/file", friendsServers, s.peerFile},
		{"GET", "/api/v1/peers/{peer_id}/playback/sessions/{session_id}/stream.mp4", friendsServers, s.peerConverted},
		{"HEAD", "/api/v1/peers/{peer_id}/playback/sessions/{session_id}/stream.mp4", friendsServers, s.peerConverted},
		{"GET", "/api/v1/peers/{peer_id}/playback/sessions/{session_id}/hls/{run}/index.m3u8", friendsServers, s.peerHLSPlaylist},
		{"HEAD", "/api/v1/peers/{peer_id}/playback/sessions/{session_id}/hls/{run}/index.m3u8", friendsServers, s.peerHLSPlaylist},
		{"GET", "/api/v1/peers/{peer_id}/playback/sessions/{session_id}/hls/{run}/{segment}", friendsServers, s.peerHLSSegment},
		{"GET", "/api/v1/peers/{peer_id}/playback/sessions/{session_id}/subtitles/{stream_id}", friendsServers, s.peerSubtitle},
		{"GET", "/api/v1/admin/profiles", adminOnly, s.adminProfiles},
		{"POST", "/api/v1/admin/profiles", adminOnly, s.adminCreateProfile},
		{"PUT", "/api/v1/admin/profiles/{user_id}", adminOnly, s.adminUpdateProfile},
		{"PUT", "/api/v1/admin/profiles/{user_id}/pin", adminOnly, s.adminSetPIN},
		{"DELETE", "/api/v1/admin/profiles/{user_id}/pin", adminOnly, s.adminClearPIN},
		{"PUT", "/api/v1/admin/profiles/{user_id}/avatar", adminOnly, s.adminSetAvatar},
		{"DELETE", "/api/v1/admin/profiles/{user_id}/avatar", adminOnly, s.adminDeleteAvatar},
		// Invited devices: mint, list, revoke, and approve a TV's pairing code.
		{"GET", "/api/v1/admin/devices", adminOnly, s.adminDevices},
		{"POST", "/api/v1/admin/devices", adminOnly, s.adminMintInvite},
		{"DELETE", "/api/v1/admin/devices/{device_id}", adminOnly, s.adminRevokeDevice},
		{"GET", "/api/v1/admin/pairings/{code}", adminOnly, s.adminPairing},
		{"POST", "/api/v1/admin/pairings/{code}/approve", adminOnly, s.adminApprovePairing},
		{"DELETE", "/api/v1/admin/pairings/{code}", adminOnly, s.adminDenyPairing},
		// Lend a tape: one movie, on a printed card, and return it early.
		{"GET", "/api/v1/admin/loans", adminOnly, s.adminLoans},
		{"POST", "/api/v1/admin/loans", adminOnly, s.adminLend},
		{"POST", "/api/v1/admin/loans/{loan_id}/return", adminOnly, s.adminReturnLoan},
		// Friends' servers: link to one with its invite, unlink, and mint a
		// server invite for a friend's server to link to this one.
		{"GET", "/api/v1/admin/peers", adminOnly, s.adminPeers},
		{"POST", "/api/v1/admin/peers", adminOnly, s.adminLinkPeer},
		{"DELETE", "/api/v1/admin/peers/{peer_id}", adminOnly, s.adminUnlinkPeer},
		{"POST", "/api/v1/admin/peers/invites", adminOnly, s.adminMintServerInvite},
		{"GET", "/api/v1/versions/{version_id}/subtitles/search", signedIn, s.searchSubtitles},
		{"POST", "/api/v1/versions/{version_id}/subtitles/download", signedIn, s.downloadSubtitle},
		{"PUT", "/api/v1/versions/{version_id}/subtitle-offset", signedIn, s.setSubtitleOffset},
		{"GET", "/api/v1/speedtest", signedIn, s.speedTest},
		{"GET", "/api/v1/admin/libraries", adminOnly, s.adminLibraries},
		{"POST", "/api/v1/admin/libraries", adminOnly, s.adminCreateLibrary},
		{"POST", "/api/v1/admin/libraries/{library_id}/scan", adminOnly, s.adminScan},
		{"GET", "/api/v1/admin/scans", adminOnly, s.adminScans},
		{"GET", "/api/v1/admin/collections", adminOnly, s.adminSmartCollections},
		{"GET", "/api/v1/admin/users", adminOnly, s.adminUsers},
		{"POST", "/api/v1/admin/users", adminOnly, s.adminCreateUser},
		{"PUT", "/api/v1/admin/users/{user_id}/grants", adminOnly, s.adminSetGrants},
		{"PUT", "/api/v1/admin/users/{user_id}/policy", adminOnly, s.adminSetPolicy},
		{"GET", "/api/v1/admin/layout/home", adminOnly, s.adminHomeLayout},
		{"PUT", "/api/v1/admin/layout/home", adminOnly, s.adminSetHomeLayout},
		{"DELETE", "/api/v1/admin/layout/home", adminOnly, s.adminResetHomeLayout},
		{"GET", "/api/v1/admin/labels", adminOnly, s.adminLabels},
		{"GET", "/api/v1/admin/activity/sessions", adminOnly, s.adminSessions},
		{"GET", "/api/v1/admin/metadata/unmatched", adminOnly, s.unmatched},
		{"GET", "/api/v1/admin/items/{item_id}/match", adminOnly, s.matchSearch},
		{"PUT", "/api/v1/admin/items/{item_id}/match", adminOnly, s.matchSet},
		{"GET", "/api/v1/admin/items/{item_id}/history", adminOnly, s.itemHistory},
		{"GET", "/api/v1/admin/watch/export", adminOnly, s.watchExport},
		{"POST", "/api/v1/admin/items/{item_id}/merge", adminOnly, s.adminMergeItems},
		{"POST", "/api/v1/admin/versions/{version_id}/split", adminOnly, s.adminSplitVersion},
		{"GET", "/api/v1/admin/markers", adminOnly, s.adminMarkerStatus},
		{"POST", "/api/v1/admin/items/{item_id}/markers/detect", adminOnly, s.adminDetectMarkers},
		{"PUT", "/api/v1/admin/items/{item_id}/markers/{kind}", adminOnly, s.adminSetMarker},
		{"DELETE", "/api/v1/admin/items/{item_id}/markers/{kind}", adminOnly, s.adminClearMarker},
		{"POST", "/api/v1/admin/items/{item_id}/markers/{kind}/apply-to-season", adminOnly, s.adminApplyMarkerToSeason},
		{"GET", "/api/v1/admin/activity/now", adminOnly, s.activityNow},
		{"GET", "/api/v1/admin/activity/history", adminOnly, s.activityHistory},
		{"GET", "/api/v1/admin/activity/stats", adminOnly, s.activityStats},
		{"GET", "/api/v1/admin/activity/users", adminOnly, s.activityUsers},
		{"POST", "/api/v1/admin/activity/sessions/{session_id}/terminate", adminOnly, s.terminateSession},
		// The weekly newsletter.
		{"GET", "/api/v1/admin/newsletter", adminOnly, s.newsletterStatus},
		{"PUT", "/api/v1/admin/newsletter", adminOnly, s.newsletterSave},
		{"POST", "/api/v1/admin/newsletter/preview", adminOnly, s.newsletterPreview},
		{"GET", "/api/v1/admin/items/{item_id}/subtitles", adminOnly, s.adminItemSubtitles},
		{"DELETE", "/api/v1/admin/subtitles/{stream_id}", adminOnly, s.adminDeleteSubtitle},
	}, s.musicRoutes()...)
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
