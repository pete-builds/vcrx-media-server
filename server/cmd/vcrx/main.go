// Command vcrx is VCRX Media Server. With no arguments it serves; the other
// subcommands manage the database, users and libraries, for use with
// `docker compose exec vcrx vcrx <command>`.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/pete-builds/vcrx-media-server/server/internal/activity"
	"github.com/pete-builds/vcrx-media-server/server/internal/admin"
	"github.com/pete-builds/vcrx-media-server/server/internal/api"
	"github.com/pete-builds/vcrx-media-server/server/internal/arr"
	"github.com/pete-builds/vcrx-media-server/server/internal/artwork"
	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/collections"
	"github.com/pete-builds/vcrx-media-server/server/internal/config"
	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/enrich"
	"github.com/pete-builds/vcrx-media-server/server/internal/federation"
	"github.com/pete-builds/vcrx-media-server/server/internal/homelists"
	"github.com/pete-builds/vcrx-media-server/server/internal/household"
	"github.com/pete-builds/vcrx-media-server/server/internal/imdb"
	"github.com/pete-builds/vcrx-media-server/server/internal/invites"
	"github.com/pete-builds/vcrx-media-server/server/internal/library"
	"github.com/pete-builds/vcrx-media-server/server/internal/live"
	"github.com/pete-builds/vcrx-media-server/server/internal/loans"
	"github.com/pete-builds/vcrx-media-server/server/internal/markers"
	"github.com/pete-builds/vcrx-media-server/server/internal/music"
	"github.com/pete-builds/vcrx-media-server/server/internal/newsletter"
	"github.com/pete-builds/vcrx-media-server/server/internal/notify"
	"github.com/pete-builds/vcrx-media-server/server/internal/opensubtitles"
	"github.com/pete-builds/vcrx-media-server/server/internal/overlay"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/preroll"
	"github.com/pete-builds/vcrx-media-server/server/internal/probe"
	"github.com/pete-builds/vcrx-media-server/server/internal/remux"
	"github.com/pete-builds/vcrx-media-server/server/internal/subsearch"
	"github.com/pete-builds/vcrx-media-server/server/internal/subtitles"
	"github.com/pete-builds/vcrx-media-server/server/internal/tmdb"
	"github.com/pete-builds/vcrx-media-server/server/internal/youtube"
	"github.com/pete-builds/vcrx-media-server/server/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `vcrx: VCRX Media Server

Usage:
  vcrx [serve]                         run the server
  vcrx migrate                         back up the database, apply migrations, exit
  vcrx version                         print the version
  vcrx healthcheck                     exit 0 if the local server answers /healthz
  vcrx user add -username NAME [-admin] [-display-name NAME]
                                       create a user; the password is read from the
                                       terminal, or from stdin when piped
  vcrx user grant -username NAME -libraries Movies,TV
  vcrx user passwd -username NAME
  vcrx user list
  vcrx library add -name NAME -kind movie|show -path /media/Movies [-grouping folder|title_year]
  vcrx library list
  vcrx library scan -name NAME [-path sub/folder] [-force]
  vcrx metadata refresh [-all]         fetch posters and plots from TMDb now; -all
                                       re-checks items already done
  vcrx metadata status                 matched and unmatched counts, and why each
                                       unmatched title was skipped
  vcrx watch export [-library NAME] [-unplayed-days N]
                                       every movie and show with its files, size and
                                       who watched it when, one JSON object per line;
                                       -unplayed-days keeps titles nobody played in
                                       N days, never-played ones included
  vcrx markers detect -dry-run DIR     detect intros and credits in every season
                                       folder under DIR and print them; stores nothing

Settings come from VCRX_* environment variables and /config/vcrx.yaml; see
deploy/.env.example.
`

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "migrate":
		err = migrateOnly()
	case "version", "-version", "--version":
		fmt.Println(version)
	case "healthcheck":
		err = healthcheck()
	case "user":
		err = userCmd(args)
	case "library":
		err = libraryCmd(args)
	case "metadata":
		err = metadataCmd(args)
	case "markers":
		err = markersCmd(args)
	case "watch":
		err = watchCmd(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		var ok bool
		if ok, err = devCommand(cmd, args); !ok {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "vcrx:", err)
		os.Exit(1)
	}
}

type app struct {
	cfg config.Config
	log *slog.Logger
	db  *sql.DB
}

func open(ctx context.Context) (*app, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	log := cfg.Logger()
	conn, err := db.Open(cfg.DBPath())
	if err != nil {
		return nil, err
	}
	if _, err := db.Migrate(ctx, conn, cfg.BackupDir(), log); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &app{cfg: cfg, log: log, db: conn}, nil
}

// healthcheck is the container HEALTHCHECK: the image has no curl.
func healthcheck() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	host := cfg.Bind
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, strconv.Itoa(cfg.Port)) + "/healthz")
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}
	return nil
}

func migrateOnly() error {
	ctx := context.Background()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := cfg.Logger()
	conn, err := db.Open(cfg.DBPath())
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	before, err := db.CurrentVersion(ctx, conn)
	if err != nil {
		return err
	}
	n, err := db.Migrate(ctx, conn, cfg.BackupDir(), log)
	if err != nil {
		return err
	}
	fmt.Printf("schema version %d -> %d (%d applied)\n", before, before+n, n)
	return nil
}

// canTranscode reports whether video transcoding is on: allowed by the
// config and possible with this ffmpeg, which needs libx264. It logs which.
func canTranscode(ctx context.Context, rmx *remux.Service, limit int, log *slog.Logger) bool {
	if limit == 0 {
		log.Info("video transcoding is off (max_transcodes is 0): clients that cannot decode a file's video are refused")
		return false
	}
	f, err := rmx.Detect(ctx)
	switch {
	case err != nil:
		log.Error("video transcoding is off: cannot ask ffmpeg for its encoders", "ffmpeg", rmx.FFmpeg, "err", err)
		return false
	case !f.X264:
		log.Warn("video transcoding is off: this ffmpeg has no libx264 encoder", "ffmpeg", rmx.FFmpeg)
		return false
	case !f.ToneMap:
		log.Info("video transcoding is on, without HDR tone mapping: this ffmpeg has no zscale filter, so HDR sent to an SDR browser looks washed out",
			"max_transcodes", limit)
	default:
		log.Info("video transcoding is on, with HDR tone mapping", "max_transcodes", limit)
	}
	return true
}

// scanAdapter lets the API start scans without depending on the library package.
type scanAdapter struct{ m *library.Manager }

func (a scanAdapter) StartScan(ctx context.Context, libraryID int64, subPath, trigger string) (int64, error) {
	id, err := a.m.Start(ctx, libraryID, library.Options{Trigger: trigger, SubPath: subPath})
	if errors.Is(err, library.ErrScanRunning) {
		return 0, admin.ErrScanRunning
	}
	return id, err
}

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = a.db.Close() }()
	cfg, log := a.cfg, a.log
	log.Info("starting", "version", version, "addr", cfg.Addr(), "config", cfg.ConfigDir, "cache", cfg.CacheDir,
		"media_roots", cfg.MediaRoots, "schema", db.LatestVersion())

	adm := &admin.Admin{DB: a.db, MediaRoots: cfg.MediaRoots}
	if err := adm.SyncConfigLibraries(ctx, cfg.Libraries); err != nil {
		// A bad library entry should not stop the server from serving the others.
		log.Error("vcrx.yaml libraries", "err", err)
	}
	// A scan cannot survive a restart; close out any the last process left open.
	if _, err := a.db.ExecContext(ctx, `UPDATE scan_runs SET status = 'aborted', error = 'server restarted during the scan', finished_at = ?
		WHERE status = 'running'`, db.NowMs()); err != nil {
		return err
	}

	prober := &probe.FFprobe{Path: cfg.FFprobePath}
	if v, err := prober.Version(ctx); err != nil {
		// Without ffprobe every file fails to probe; say so once, loudly.
		log.Error("ffprobe unavailable: files will be listed but not inspected", "path", cfg.FFprobePath, "err", err)
	} else {
		log.Info("ffprobe", "version", v)
	}
	scanner := &library.Scanner{DB: a.db, Prober: prober, Log: log.With("component", "scanner"), ProbeConcurrency: cfg.ProbeConcurrency}
	scans := library.NewManager(scanner, log.With("component", "scans"))
	seasons, err := catalog.LoadSeasons(cfg.ConfigDir)
	if err != nil {
		return err
	}
	cat := &catalog.Catalog{DB: a.db, Seasons: seasons, Today: devHomeDate(log), TrailersYouTube: cfg.YouTubeTrailers()}
	authStore := &auth.Store{DB: a.db, TTL: cfg.SessionTTL}
	// Converted streams that copy the video convert at most the audio, a small
	// fraction of a core each; four at once is well within nix1's budget.
	// Video transcodes cost whole cores and have their own, smaller limit.
	rmx := remux.New(cfg.FFmpegPath, cfg.FFprobePath, 4, log.With("component", "remux"))
	rmx.SetMaxTranscodes(cfg.MaxTranscodes)
	pb := &playback.Service{DB: a.db, Catalog: cat, CanRemux: true, CanTranscode: canTranscode(ctx, rmx, cfg.MaxTranscodes, log)}
	// Safari plays converted streams only as HLS, written to disk per session
	// under the same slot limits.
	hls, err := remux.NewHLS(rmx, cfg.HLSDir(), log.With("component", "hls"))
	if err != nil {
		log.Error("HLS streams are off, so Safari cannot play files that need converting", "err", err)
	} else {
		pb.CanHLS = true
		defer hls.Close()
		go hls.Run(ctx)
	}
	hooks := notify.New(cfg.WebhookURLs, cfg.WebhookEvents, cfg.ServerName, log.With("component", "webhooks"))
	if len(cfg.WebhookURLs) > 0 {
		pb.Notify = hooks.Send
		scanner.OnAdded = func(ctx context.Context, lib library.Library, run library.Run, idsAbove int64) {
			titles, err := library.AddedTitles(ctx, a.db, lib.ID, idsAbove)
			if err != nil || len(titles) == 0 {
				return
			}
			hooks.Send(notify.Notice{Event: notify.Added, Library: lib.Name, Titles: titles, Message: notify.AddedSummary(titles)})
		}
		log.Info("webhooks on", "count", len(cfg.WebhookURLs), "events", cfg.WebhookEvents)
	}

	subs := subtitleSearch(ctx, a, version)
	if cfg.SubtitleAutoFetch && subs.CanDownload() {
		prev := scanner.OnAdded
		scanner.OnAdded = func(ctx context.Context, lib library.Library, run library.Run, idsAbove int64) {
			if prev != nil {
				prev(ctx, lib, run, idsAbove)
			}
			subs.Kick()
		}
	}

	proxies, err := prefixes(cfg.TrustedProxies)
	if err != nil {
		return fmt.Errorf("trusted proxies: %w", err)
	}
	trustedNets, err := prefixes(cfg.TrustedNetworks)
	if err != nil {
		return fmt.Errorf("trusted networks: %w", err)
	}
	switch {
	case cfg.AutoSignInUser != "" && len(trustedNets) == 0:
		log.Warn("auto_sign_in_user is set but trusted_networks is empty, so nobody is signed in without a password; list your LAN and Tailscale networks")
	case cfg.AutoSignInUser != "" && cfg.AutoSignInPicker:
		log.Info("trusted networks get the profile picker", "starts_on", cfg.AutoSignInUser, "networks", cfg.TrustedNetworks)
	case cfg.AutoSignInUser != "":
		log.Info("sign-in without a password is on for trusted networks", "user", cfg.AutoSignInUser, "networks", cfg.TrustedNetworks)
	}

	art := artwork.New(a.db, cfg.ArtworkCacheDir(), cfg.FFmpegPath)
	art.MetadataDir = cfg.MetadataDir()
	if ov, n, err := overlays(ctx, adm, cfg.Libraries, a.db); err != nil {
		log.Error("overlays off", "err", err)
	} else if n > 0 {
		art.Overlays = ov
		log.Info("poster overlays on", "libraries", n)
	}
	if cfg.IMDbRatings {
		go (&imdb.Syncer{DB: a.db, Log: log.With("component", "imdb")}).Run(ctx)
	}
	go collectionBuilder(a).Run(ctx, 30*time.Minute)
	smart := smartBuilder(a)
	scanner.OnFinished = func(library.Library, library.Run) { smart.Trigger() }
	go smart.Run(ctx, 15*time.Minute)
	markerHours, _ := markers.ParseHours(cfg.MarkerHours) // validated by config.Load
	var markerJob *markers.Job
	if cfg.DetectMarkers {
		markerJob = &markers.Job{DB: a.db, Decoder: markers.Decoder{FFmpeg: cfg.FFmpegPath, FFprobe: cfg.FFprobePath},
			CacheDir: cfg.CacheDir, Log: log.With("component", "markers"), Hours: markerHours,
			IntroWindow: time.Duration(cfg.MarkerIntroMinutes) * time.Minute, CreditsWindow: time.Duration(cfg.MarkerCreditsMinutes) * time.Minute,
			Busy: scans.Running, InitialDelay: time.Minute}
		// A scan that adds episodes starts a pass at once (still only inside the hours).
		added := scanner.OnAdded
		scanner.OnAdded = func(ctx context.Context, lib library.Library, run library.Run, idsAbove int64) {
			if added != nil {
				added(ctx, lib, run, idsAbove)
			}
			if lib.Kind == "show" {
				markerJob.Wake()
			}
		}
		go markerJob.Run(ctx)
		log.Info("intro and credits detection on", "hours", markerHours.String())
	} else {
		log.Info("intro and credits detection is off (detect_markers: false)")
	}
	srv := &api.Server{
		DB: a.db, Log: log.With("component", "http"), Version: version, ServerName: cfg.ServerName,
		Auth: authStore, Limiter: auth.NewLimiter(10, 15*time.Minute), Catalog: cat, Playback: pb,
		Artwork: art, Admin: adm, Scans: scanAdapter{scans},
		Activity: &activity.Service{DB: a.db}, Webhooks: hooks, Remux: rmx, HLS: hls, WebDir: os.Getenv("VCRX_WEB_DIR"),
		Subtitles:        subtitles.New(cfg.SubtitleCacheDir(), cfg.FFmpegPath, log.With("component", "subtitles")),
		SubSearch:        subs,
		LibraryRoots:     func(ctx context.Context) (map[string]string, error) { return rootStates(ctx, adm) },
		AdminAllowRemote: cfg.AdminAllowRemote, TrustedProxies: proxies,
		AutoSignInUser: cfg.AutoSignInUser, TrustedNetworks: trustedNets, AllowedHosts: cfg.AllowedHosts,
		TrailersYouTube: cfg.YouTubeTrailers(), Prerolls: preroll.Folder{Dir: cfg.PrerollDir},
	}
	srv.SmartCollections = smart
	// Music libraries: listings, covers, and tracks played directly or
	// converted to AAC or Opus through the same ffmpeg as video.
	if err := rmx.DetectAudio(ctx); err != nil {
		log.Warn("could not ask ffmpeg about libopus; music converts to AAC only", "err", err)
	}
	mcat := &music.Catalog{DB: a.db}
	srv.Music = &api.Music{Catalog: mcat, Player: &music.Player{DB: a.db, Catalog: mcat, Remux: rmx},
		Covers: &music.Covers{Catalog: mcat, FFmpeg: cfg.FFmpegPath, CacheDir: filepath.Join(cfg.ArtworkCacheDir(), "music")}}
	// The weekly newsletter checks once a minute whether its time has come; it
	// does nothing until switched on in Settings.
	srv.Newsletter = &newsletter.Service{DB: a.db, Hooks: hooks, Log: log.With("component", "newsletter")}
	go srv.Newsletter.Run(ctx)
	// Live TV: the admin's M3U playlists, read now and every refresh_hours.
	// Logos come only from public addresses (a playlist names them).
	liveStore := &live.Store{DB: a.db}
	var sources []live.SourceSpec
	for _, src := range cfg.LiveTV.Sources {
		sources = append(sources, live.SourceSpec{Name: src.Name, Location: src.Location, Guide: src.GuideLocation, MaxStreams: src.MaxStreams})
	}
	refresher := &live.Refresher{Store: liveStore, Sources: sources, Interval: cfg.LiveRefreshInterval(), GuideInterval: cfg.LiveGuideInterval(),
		LogoDir: cfg.LogoCacheDir(),
		Log:     log.With("component", "live"), Logos: live.PublicOnlyClient(10 * time.Second)}
	if err := refresher.Start(ctx); err != nil {
		return err
	}
	streams := &live.Streams{FFmpeg: cfg.FFmpegPath, Root: cfg.LiveStreamDir(), Log: log.With("component", "live"), MaxStreams: cfg.LiveMaxStreams()}
	if err := os.MkdirAll(streams.Root, 0o700); err != nil {
		return fmt.Errorf("live stream directory: %w", err)
	}
	go streams.Run(ctx)
	srv.Live = &api.Live{Store: liveStore, Refresher: refresher, LogoDir: cfg.LogoCacheDir(), Streams: streams}
	srv.MarkerHours = markerHours
	if markerJob != nil {
		srv.Markers = markerJob
	}
	// Profiles: PIN attempts have their own, stricter limiter (household.PIN*).
	srv.Household = &household.Store{DB: a.db, AvatarDir: cfg.AvatarDir()}
	srv.PINs, srv.ProfilePicker = household.NewLimiter(household.PINWindow), cfg.AutoSignInPicker
	// Invited devices, and TVs pairing by QR code: ten pairing codes, ten
	// polls with an unknown code, and ten wrong codes typed by an admin, per
	// address per window.
	srv.Invites, srv.Pairings, srv.PairLimiter = &invites.Store{DB: a.db}, &invites.Pairings{}, auth.NewLimiter(10, 15*time.Minute)
	// Lent tapes; their expiry is swept with the devices' every minute.
	srv.Loans = &loans.Store{DB: a.db, Invites: srv.Invites}
	// Friends' servers: link with a server invite, stream from each other,
	// never copy (milestone 9, item 3).
	srv.SetUpFederation(federation.Guard{AllowLoopback: cfg.PeerAllowLoopback})
	if cfg.PeerAllowLoopback {
		log.Warn("peer_allow_loopback is on: friends' servers may be linked on this machine's loopback address; for development only")
	}
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				srv.CutExpiredDevices(ctx)
			}
		}
	}()
	if users, err := authStore.CountUsers(ctx); err == nil && users == 0 {
		log.Warn("no users yet: create the admin with `docker compose exec vcrx vcrx user add -username <name> -admin`")
	}

	if cfg.ScanOnStartup {
		go scans.Schedule(ctx)
	}
	go housekeeping(ctx, log, pb, authStore, a.db, cfg.BackupDir())
	if cfg.TMDbAPIKey != "" {
		enr := newEnricher(a)
		enr.Busy = scans.Running
		// Merge copies first, then rebuild: stamps are worked out on the
		// merged movie, not on a copy that is about to disappear.
		enr.AfterPass = func(ctx context.Context) {
			rep, err := library.MergeByTMDb(ctx, a.db, 0)
			if err != nil {
				log.Warn("merging copies with the same TMDb id", "err", err)
			}
			rep.Log(log)
			if rep.Merged > 0 {
				smart.Trigger()
			}
		}
		enr.Changed = smart.Trigger
		srv.Metadata = enr
		go enr.Run(ctx, 2*time.Minute, 15*time.Minute)
	} else {
		log.Info("TMDb enrichment is off: set VCRX_TMDB_API_KEY for posters and plots")
	}
	switch {
	case cfg.TMDbHomeLists && cfg.TMDbAPIKey == "":
		log.Warn("tmdb_home_lists is on but there is no TMDb key, so Home has no TMDb rows")
	case cfg.TMDbHomeLists:
		lists := &homelists.Refresher{DB: a.db, TMDb: tmdb.New(cfg.TMDbAPIKey, cfg.TMDbLanguage), Log: log.With("component", "homelists")}
		go lists.Run(ctx, 6*time.Hour)
	}
	switch {
	case cfg.YouTubeTrailers():
		log.Info("YouTube trailers on: titles without a local trailer offer TMDb's official one")
	case cfg.TrailersYouTube:
		log.Warn("trailers_youtube is on but there is no TMDb key to look trailers up with; set VCRX_TMDB_API_KEY")
	}

	if cfg.ArrWebhookToken != "" {
		srv.ArrToken = cfg.ArrWebhookToken
		srv.Arr = &arr.Dispatcher{
			Map: cfg.ArrPathMap, Log: log.With("component", "arr"),
			Libraries: func(ctx context.Context) ([]arr.Library, error) {
				libs, err := adm.Libraries(ctx)
				out := make([]arr.Library, 0, len(libs))
				for _, l := range libs {
					out = append(out, arr.Library{ID: l.ID, Kind: l.Kind, RootPath: l.RootPath})
				}
				return out, err
			},
			Scan: func(ctx context.Context, libraryID int64, sub string) (int64, error) {
				id, err := scanAdapter{scans}.StartScan(context.WithoutCancel(ctx), libraryID, sub, "webhook")
				if errors.Is(err, admin.ErrScanRunning) {
					return 0, arr.ErrBusy
				}
				return id, err
			},
		}
		log.Info("Radarr and Sonarr webhooks on", "path_maps", len(cfg.ArrPathMap))
	}

	hs := srv.HTTPServer(cfg.Addr())
	errc := make(chan error, 1)
	go func() { errc <- hs.ListenAndServe() }()
	log.Info("listening", "addr", cfg.Addr())
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	herr := hs.Shutdown(sctx)
	if err := scans.Shutdown(sctx); err != nil {
		log.Warn("scans did not stop cleanly", "err", err)
	}
	return herr
}

// subtitleSearch sets up OpenSubtitles search, off without an API key, and
// starts the automatic fetch when it is turned on and downloads can work.
func subtitleSearch(ctx context.Context, a *app, version string) *subsearch.Service {
	cfg, log := a.cfg, a.log
	var provider subsearch.Provider
	switch {
	case cfg.OpenSubtitlesStub:
		provider = opensubtitles.NewStub()
		log.Warn("subtitle search uses the offline stub (VCRX_OPENSUBTITLES_STUB): results are invented, for development only")
	case cfg.OpenSubtitlesAPIKey != "":
		provider = opensubtitles.New(cfg.OpenSubtitlesAPIKey, cfg.OpenSubtitlesUsername, cfg.OpenSubtitlesPassword, "VCRX v"+version)
	}
	subs := subsearch.New(a.db, provider, cfg.SubtitleDownloadDir(), cfg.SubtitleLanguages, log.With("component", "subsearch"))
	switch {
	case provider == nil:
		log.Info("subtitle search is off: set VCRX_OPENSUBTITLES_API_KEY to search OpenSubtitles")
		return subs
	case !subs.CanDownload():
		log.Warn("subtitle search is on but downloads are off: set VCRX_OPENSUBTITLES_USERNAME and VCRX_OPENSUBTITLES_PASSWORD")
	default:
		log.Info("subtitle search on", "languages", subs.Languages, "auto_fetch", cfg.SubtitleAutoFetch)
	}
	if cfg.SubtitleAutoFetch && subs.CanDownload() {
		if err := subs.InitAuto(ctx); err != nil {
			log.Error("automatic subtitle fetch off", "err", err)
			return subs
		}
		go subs.RunAuto(ctx, 30*time.Minute)
	}
	return subs
}

// rootStates answers the readiness check: each library root must list, quickly.
func rootStates(ctx context.Context, adm *admin.Admin) (map[string]string, error) {
	libs, err := adm.Libraries(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, l := range libs {
		res := make(chan string, 1)
		go func(root string) {
			f, err := os.Open(root)
			if err != nil {
				res <- "unreachable: " + err.Error()
				return
			}
			defer func() { _ = f.Close() }()
			if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
				res <- "unreadable: " + err.Error()
				return
			}
			res <- "ok"
		}(l.RootPath)
		select {
		case s := <-res:
			out[l.Name] = s
		case <-ctx.Done():
			out[l.Name] = "timed out"
		}
	}
	return out, nil
}

// nightlyBackups is how many daily database snapshots are kept in /config/backups.
const nightlyBackups = 7

func housekeeping(ctx context.Context, log *slog.Logger, pb *playback.Service, as *auth.Store, conn *sql.DB, backupDir string) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for i := 0; ; i++ {
		if n, err := pb.SweepStale(ctx); err != nil {
			log.Warn("sweep stale playback sessions", "err", err)
		} else if n > 0 {
			log.Info("closed stale playback sessions", "count", n)
		}
		if i%60 == 0 {
			if _, err := as.PurgeExpired(ctx); err != nil {
				log.Warn("purge expired sessions", "err", err)
			}
		}
		// Ten minutes after start, then hourly, take the day's snapshot of the
		// database (watch history, resume points, matches) if there is none
		// yet, keeping a week. Going by the date rather than uptime means
		// restarts neither stack copies from one day nor skip days.
		if i%60 == 10 {
			maybeNightlyBackup(ctx, log, conn, backupDir, time.Now())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// nightlyDue reports whether dir lacks a nightly backup from now's UTC date
// (backup names carry a UTC timestamp, so they sort and compare by date).
func nightlyDue(dir string, now time.Time) bool {
	matches, err := filepath.Glob(filepath.Join(dir, "vcrx-nightly-*.db"))
	if err != nil || len(matches) == 0 {
		return true
	}
	sort.Strings(matches)
	newest := strings.TrimPrefix(filepath.Base(matches[len(matches)-1]), "vcrx-nightly-")
	return newest < now.UTC().Format("20060102")
}

func maybeNightlyBackup(ctx context.Context, log *slog.Logger, conn *sql.DB, dir string, now time.Time) {
	if !nightlyDue(dir, now) {
		return
	}
	path, err := db.Backup(ctx, conn, dir, "nightly")
	if err != nil {
		log.Warn("nightly database backup failed", "err", err)
		return
	}
	removed, err := db.Prune(dir, "nightly", nightlyBackups)
	if err != nil {
		log.Warn("pruning old database backups failed", "err", err)
	}
	log.Info("database backed up", "path", path, "pruned", removed)
}

func readPassword(prompt string) (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, prompt)
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		fmt.Fprint(os.Stderr, "Again: ")
		b2, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		if string(b) != string(b2) {
			return "", errors.New("passwords do not match")
		}
		return string(b), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		// The usual cause is a command run without a terminal (ssh without -t,
		// docker compose exec -T), which hands the CLI an empty stdin.
		return "", errors.New("no password received: run this in an interactive terminal (ssh -t, docker compose exec without -T), or pipe the password on stdin")
	}
	return line, nil
}

func userCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("user: want add, grant, passwd or list")
	}
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("user "+sub, flag.ContinueOnError)
	username := fs.String("username", "", "username")
	isAdmin := fs.Bool("admin", false, "make the user an admin")
	displayName := fs.String("display-name", "", "display name")
	libs := fs.String("libraries", "", "comma separated library names")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	a, err := open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = a.db.Close() }()
	store := &auth.Store{DB: a.db, TTL: a.cfg.SessionTTL}
	adm := &admin.Admin{DB: a.db, MediaRoots: a.cfg.MediaRoots}
	switch sub {
	case "add":
		pw, err := readPassword("Password for " + *username + ": ")
		if err != nil {
			return err
		}
		u, err := store.CreateUser(ctx, *username, pw, *displayName, *isAdmin)
		if err != nil {
			return err
		}
		role := "user"
		if u.IsAdmin {
			role = "admin (sees every library)"
		}
		fmt.Printf("created %s %q (id %d)\n", role, u.Username, u.ID)
		if !u.IsAdmin {
			fmt.Println("grant libraries with: vcrx user grant -username " + u.Username + " -libraries Movies,TV")
		}
	case "passwd":
		u, _, err := store.UserByName(ctx, *username)
		if err != nil {
			return fmt.Errorf("user %q: %w", *username, err)
		}
		pw, err := readPassword("New password for " + u.Username + ": ")
		if err != nil {
			return err
		}
		if err := store.SetPassword(ctx, u.ID, pw); err != nil {
			return err
		}
		fmt.Println("password changed; every device was signed out")
	case "grant":
		u, _, err := store.UserByName(ctx, *username)
		if err != nil {
			return fmt.Errorf("user %q: %w", *username, err)
		}
		all, err := adm.Libraries(ctx)
		if err != nil {
			return err
		}
		var ids []int64
		for _, name := range strings.Split(*libs, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			found := false
			for _, l := range all {
				if strings.EqualFold(l.Name, name) {
					ids, found = append(ids, l.ID), true
				}
			}
			if !found {
				return fmt.Errorf("no library named %q", name)
			}
		}
		if err := adm.SetGrants(ctx, u.ID, ids); err != nil {
			return err
		}
		fmt.Printf("%s may now see %d libraries\n", u.Username, len(ids))
	case "list":
		users, err := adm.Users(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "ID\tUSERNAME\tADMIN\tLIBRARIES")
		for _, u := range users {
			_, _ = fmt.Fprintf(tw, "%d\t%s\t%v\t%v\n", u.ID, u.Username, u.IsAdmin, u.LibraryIDs)
		}
		return tw.Flush()
	default:
		return fmt.Errorf("user: unknown command %q", sub)
	}
	return nil
}

func libraryCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("library: want add, list or scan")
	}
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("library "+sub, flag.ContinueOnError)
	name := fs.String("name", "", "library name")
	kind := fs.String("kind", "", "movie, show or music")
	path := fs.String("path", "", "root path inside the container (add), or folder under the root (scan)")
	grouping := fs.String("grouping", "folder", "folder or title_year")
	force := fs.Bool("force", false, "scan even if most files appear to be missing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	a, err := open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = a.db.Close() }()
	adm := &admin.Admin{DB: a.db, MediaRoots: a.cfg.MediaRoots}
	switch sub {
	case "add":
		l, err := adm.CreateLibrary(ctx, admin.LibraryInput{Name: *name, Kind: *kind, RootPath: *path, Grouping: *grouping})
		if err != nil {
			return err
		}
		fmt.Printf("created %s library %q at %s (id %d); the running server scans it within a few minutes, or run: vcrx library scan -name %q\n",
			l.Kind, l.Name, l.RootPath, l.ID, l.Name)
	case "list":
		libs, err := adm.Libraries(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "ID\tNAME\tKIND\tROOT\tITEMS\tLAST SCAN")
		for _, l := range libs {
			last := "never"
			if l.LastScan != nil {
				last = fmt.Sprintf("%s %s (%d seen, %d added, %d failed)", l.LastScan.Status,
					time.UnixMilli(l.LastScan.StartedAt).Format(time.DateTime), l.LastScan.FilesSeen, l.LastScan.FilesAdded, l.LastScan.FilesFailed)
			}
			_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%s\n", l.ID, l.Name, l.Kind, l.RootPath, l.ItemCount, last)
		}
		return tw.Flush()
	case "scan":
		libs, err := adm.Libraries(ctx)
		if err != nil {
			return err
		}
		var lib *admin.Library
		for i := range libs {
			if strings.EqualFold(libs[i].Name, *name) {
				lib = &libs[i]
			}
		}
		if lib == nil {
			return fmt.Errorf("no library named %q", *name)
		}
		if lib.LastScan != nil && lib.LastScan.Status == "running" {
			return fmt.Errorf("library %q is already being scanned (run %d)", lib.Name, lib.LastScan.ID)
		}
		scanner := &library.Scanner{DB: a.db, Prober: &probe.FFprobe{Path: a.cfg.FFprobePath}, Log: a.log, ProbeConcurrency: a.cfg.ProbeConcurrency}
		run, err := library.NewManager(scanner, a.log).RunNow(ctx, lib.ID, library.Options{Trigger: "cli", SubPath: *path, Force: *force})
		fmt.Printf("scan %d: %s, %d seen, %d added, %d changed, %d missing, %d failed\n", run.ID, run.Status, run.Seen, run.Added,
			run.Changed, run.Missing, run.Failed)
		if run.Error != "" {
			fmt.Println("error:", run.Error)
		}
		return err
	default:
		return fmt.Errorf("library: unknown command %q", sub)
	}
	return nil
}

func watchCmd(args []string) error {
	if len(args) == 0 || args[0] != "export" {
		return errors.New("watch: want export")
	}
	fs := flag.NewFlagSet("watch export", flag.ContinueOnError)
	name := fs.String("library", "", "only this library")
	days := fs.Int("unplayed-days", 0, "only titles nobody played in this many days, never-played ones included")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *days < 0 {
		return errors.New("watch export: -unplayed-days must be 0 or more")
	}
	ctx := context.Background()
	a, err := open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = a.db.Close() }()
	return watchExport(ctx, a.db, os.Stdout, *name, *days)
}

// watchExport writes the watch state export as JSON lines, the same rows as
// GET /api/v1/admin/watch/export.
func watchExport(ctx context.Context, conn *sql.DB, out io.Writer, library string, unplayedDays int) error {
	f := activity.ExportFilter{UnplayedDays: unplayedDays}
	if library != "" {
		libs, err := (&admin.Admin{DB: conn}).Libraries(ctx)
		if err != nil {
			return err
		}
		for _, l := range libs {
			if strings.EqualFold(l.Name, library) {
				f.LibraryID = l.ID
			}
		}
		if f.LibraryID == 0 {
			return fmt.Errorf("no library named %q", library)
		}
	}
	bw := bufio.NewWriter(out)
	enc := json.NewEncoder(bw)
	if err := (&activity.Service{DB: conn}).WatchExport(ctx, f, func(t activity.TitleWatch) error { return enc.Encode(t) }); err != nil {
		return err
	}
	return bw.Flush()
}

func newEnricher(a *app) *enrich.Enricher {
	e := &enrich.Enricher{DB: a.db, TMDb: tmdb.New(a.cfg.TMDbAPIKey, a.cfg.TMDbLanguage), Dir: a.cfg.MetadataDir(),
		Country: a.cfg.TMDbCountry, Log: a.log.With("component", "tmdb"), Trailers: a.cfg.YouTubeTrailers()}
	if e.Trailers {
		e.Embeddable = youtube.New().Embeddable
	}
	return e
}

func metadataCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("metadata: want refresh, status, imdb or collections")
	}
	sub, args := args[0], args[1:]
	fs := flag.NewFlagSet("metadata "+sub, flag.ContinueOnError)
	all := fs.Bool("all", false, "re-check every item, not only new and due ones")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a, err := open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = a.db.Close() }()
	enr := newEnricher(a)
	switch sub {
	case "collections":
		res, err := collectionBuilder(a).Build(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("collections: %d franchises, %d newly named, %d removed, %d waiting for a name\n", res.Collections, res.Named, res.Removed, res.Waiting)
		return printSmart(ctx, smartBuilder(a))
	case "imdb":
		res, err := (&imdb.Syncer{DB: a.db, Log: a.log}).Sync(ctx, true)
		if err != nil {
			return err
		}
		fmt.Printf("imdb: %d titles with an IMDb id, %d ratings written, %d removed\n", res.Wanted, res.Updated, res.Removed)
		return nil
	case "refresh":
		if a.cfg.TMDbAPIKey == "" {
			return errors.New("set VCRX_TMDB_API_KEY (or VCRX_TMDB_API_KEY_FILE) first")
		}
		if *all {
			if err := enr.ResetChecks(ctx); err != nil {
				return err
			}
		}
		st, err := enr.Drain(ctx)
		fmt.Printf("tmdb: %d matched, %d unmatched, %d errors, %d episodes filled\n", st.Matched, st.Unmatched, st.Errors, st.Episodes)
		if err == nil && enr.Trailers {
			var ts enrich.TrailerStats
			ts, err = enr.RefreshTrailers(ctx)
			fmt.Printf("trailers: %d found on YouTube, %d without one, %d lookups failed, %d passed over (no longer plays embedded)\n", ts.Found, ts.None, ts.Errors, ts.Skipped)
		}
		if st.Unmatched > 0 {
			fmt.Println("run `vcrx metadata status` to see what was skipped and why")
		}
		if err != nil {
			return err
		}
		rep, err := library.MergeByTMDb(ctx, a.db, 0)
		if rep.Merged > 0 {
			fmt.Printf("merged %d copies into movies with the same TMDb id\n", rep.Merged)
		}
		for _, why := range rep.Apart {
			fmt.Println("left apart:", why)
		}
		return err
	case "status":
		st, err := enr.Status(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "KIND\tMATCHED\tUNMATCHED\tERROR\tPENDING")
		for _, k := range []string{"movie", "show", "episode"} {
			_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\n", k, st.Counts[k+" matched"], st.Counts[k+" unmatched"], st.Counts[k+" error"],
				st.Counts[k+" pending"])
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if len(st.Unmatched) > 0 {
			fmt.Println()
			tw = tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
			_, _ = fmt.Fprintln(tw, "ID\tKIND\tTITLE\tYEAR\tWHY")
			for _, u := range st.Unmatched {
				_, _ = fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%s\n", u.ID, u.Kind, u.Title, u.Year, u.Reason)
			}
			return tw.Flush()
		}
		return nil
	default:
		return fmt.Errorf("metadata: unknown command %q", sub)
	}
}

// prefixes parses CIDR ranges; a bare address becomes a single-address range.
func prefixes(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, p := range list {
		pfx, err := netip.ParsePrefix(p)
		if err != nil {
			addr, aerr := netip.ParseAddr(p)
			if aerr != nil {
				return nil, fmt.Errorf("%q: %w", p, err)
			}
			pfx = netip.PrefixFrom(addr, addr.BitLen())
		}
		out = append(out, pfx.Masked())
	}
	return out, nil
}

// overlays builds the poster overlay service from the libraries in vcrx.yaml
// that list overlays, matched to their database ids by name.
func overlays(ctx context.Context, adm *admin.Admin, specs []config.LibrarySpec, conn *sql.DB) (*overlay.Service, int, error) {
	byName := map[string][]overlay.Spec{}
	for _, l := range specs {
		if len(l.Overlays) > 0 {
			byName[strings.ToLower(l.Name)] = l.Overlays
		}
	}
	if len(byName) == 0 {
		return nil, 0, nil
	}
	libs, err := adm.Libraries(ctx)
	if err != nil {
		return nil, 0, err
	}
	fontBytes, err := fs.ReadFile(web.Static, "static/fonts/rajdhani_bold.ttf")
	if err != nil {
		return nil, 0, err
	}
	ov := &overlay.Service{DB: conn, Specs: map[int64][]overlay.Spec{}, Font: fontBytes}
	for _, l := range libs {
		if sp := byName[strings.ToLower(l.Name)]; sp != nil {
			ov.Specs[l.ID] = sp
		}
	}
	return ov, len(ov.Specs), nil
}

// collectionBuilder builds franchise collections; names come from TMDb when
// a key is set, and franchises wait for one otherwise.
func collectionBuilder(a *app) *collections.Builder {
	b := &collections.Builder{DB: a.db, Log: a.log.With("component", "collections")}
	if a.cfg.TMDbAPIKey != "" {
		client := tmdb.New(a.cfg.TMDbAPIKey, a.cfg.TMDbLanguage)
		b.Names = func(ctx context.Context, id int64) (string, error) {
			c, err := client.Collection(ctx, id)
			if err != nil {
				return "", err
			}
			return c.Name, nil
		}
	}
	return b
}

// smartBuilder builds the smart collections defined in YAML files.
func smartBuilder(a *app) *collections.SmartBuilder {
	return &collections.SmartBuilder{DB: a.db, Dir: a.cfg.SmartCollectionsDir(), Log: a.log.With("component", "smart-collections")}
}

// printSmart builds the smart collections once and says what happened, with
// every refused file and failed definition, so a typo is found at the shell.
func printSmart(ctx context.Context, b *collections.SmartBuilder) error {
	rep, err := b.Build(ctx)
	if err != nil {
		return err
	}
	if rep.Error != "" {
		fmt.Printf("smart collections: %s (nothing changed)\n", rep.Error)
	}
	for _, f := range rep.Files {
		if f.Error != "" {
			fmt.Printf("smart collections: refused %s\n", f.Error)
		}
	}
	failed := 0
	for _, c := range rep.Collections {
		if c.Error != "" {
			failed++
			fmt.Printf("smart collections: %s\n", c.Error)
		}
	}
	fmt.Printf("smart collections: %d from %d files, %d failed, labels +%d -%d, %d removed\n",
		len(rep.Collections), len(rep.Files), failed, rep.LabelsAdded, rep.LabelsRemoved, rep.Removed)
	return nil
}

// markersCmd checks detection against a real library without touching the
// database: `vcrx markers detect -dry-run /media/TV/Some Show`.
func markersCmd(args []string) error {
	if len(args) == 0 || args[0] != "detect" {
		return errors.New("usage: vcrx markers detect -dry-run DIR")
	}
	fs := flag.NewFlagSet("markers detect", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "print what detection finds; store nothing (required)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if !*dry || fs.NArg() != 1 {
		return errors.New("usage: vcrx markers detect -dry-run DIR (the server stores markers itself; this only prints)")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	j := &markers.Job{Decoder: markers.Decoder{FFmpeg: cfg.FFmpegPath, FFprobe: cfg.FFprobePath}, Log: cfg.Logger(),
		IntroWindow: time.Duration(cfg.MarkerIntroMinutes) * time.Minute, CreditsWindow: time.Duration(cfg.MarkerCreditsMinutes) * time.Minute}
	return j.DryRun(ctx, fs.Arg(0), os.Stdout)
}
