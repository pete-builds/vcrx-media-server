// Command vcrx is VCRX Media Server. With no arguments it serves; the other
// subcommands manage the database, users and libraries, for use with
// `docker compose exec vcrx vcrx <command>`.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/pete-builds/vcrx-media-server/server/internal/activity"
	"github.com/pete-builds/vcrx-media-server/server/internal/admin"
	"github.com/pete-builds/vcrx-media-server/server/internal/api"
	"github.com/pete-builds/vcrx-media-server/server/internal/artwork"
	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/catalog"
	"github.com/pete-builds/vcrx-media-server/server/internal/config"
	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/enrich"
	"github.com/pete-builds/vcrx-media-server/server/internal/library"
	"github.com/pete-builds/vcrx-media-server/server/internal/notify"
	"github.com/pete-builds/vcrx-media-server/server/internal/playback"
	"github.com/pete-builds/vcrx-media-server/server/internal/probe"
	"github.com/pete-builds/vcrx-media-server/server/internal/subtitles"
	"github.com/pete-builds/vcrx-media-server/server/internal/tmdb"
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
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
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
	cat := &catalog.Catalog{DB: a.db}
	authStore := &auth.Store{DB: a.db, TTL: cfg.SessionTTL}
	pb := &playback.Service{DB: a.db, Catalog: cat}
	hooks := notify.New(cfg.WebhookURLs, cfg.WebhookEvents, cfg.ServerName, log.With("component", "webhooks"))
	if len(cfg.WebhookURLs) > 0 {
		pb.Notify = hooks.Send
		log.Info("webhooks on", "count", len(cfg.WebhookURLs), "events", cfg.WebhookEvents)
	}

	proxies, err := prefixes(cfg.TrustedProxies)
	if err != nil {
		return fmt.Errorf("trusted proxies: %w", err)
	}
	trustedNets, err := prefixes(cfg.TrustedNetworks)
	if err != nil {
		return fmt.Errorf("trusted networks: %w", err)
	}
	if cfg.AutoSignInUser != "" {
		log.Info("sign-in without a password is on for trusted networks", "user", cfg.AutoSignInUser, "networks", cfg.TrustedNetworks)
	}

	art := artwork.New(a.db, cfg.ArtworkCacheDir(), cfg.FFmpegPath)
	art.MetadataDir = cfg.MetadataDir()
	srv := &api.Server{
		DB: a.db, Log: log.With("component", "http"), Version: version, ServerName: cfg.ServerName,
		Auth: authStore, Limiter: auth.NewLimiter(10, 15*time.Minute), Catalog: cat, Playback: pb,
		Artwork: art, Admin: adm, Scans: scanAdapter{scans},
		Activity: &activity.Service{DB: a.db}, Webhooks: hooks, WebDir: os.Getenv("VCRX_WEB_DIR"),
		Subtitles:        subtitles.New(cfg.SubtitleCacheDir(), cfg.FFmpegPath, log.With("component", "subtitles")),
		LibraryRoots:     func(ctx context.Context) (map[string]string, error) { return rootStates(ctx, adm) },
		AdminAllowRemote: cfg.AdminAllowRemote, TrustedProxies: proxies,
		AutoSignInUser: cfg.AutoSignInUser, TrustedNetworks: trustedNets,
	}
	if users, err := authStore.CountUsers(ctx); err == nil && users == 0 {
		log.Warn("no users yet: create the admin with `docker compose exec vcrx vcrx user add -username <name> -admin`")
	}

	if cfg.ScanOnStartup {
		go scans.Schedule(ctx)
	}
	go housekeeping(ctx, log, pb, authStore)
	if cfg.TMDbAPIKey != "" {
		enr := newEnricher(a)
		enr.Busy = scans.Running
		go enr.Run(ctx, 2*time.Minute, 15*time.Minute)
	} else {
		log.Info("TMDb enrichment is off: set VCRX_TMDB_API_KEY for posters and plots")
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

func housekeeping(ctx context.Context, log *slog.Logger, pb *playback.Service, as *auth.Store) {
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
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
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
	kind := fs.String("kind", "", "movie or show")
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

func newEnricher(a *app) *enrich.Enricher {
	return &enrich.Enricher{DB: a.db, TMDb: tmdb.New(a.cfg.TMDbAPIKey, a.cfg.TMDbLanguage), Dir: a.cfg.MetadataDir(),
		Country: a.cfg.TMDbCountry, Log: a.log.With("component", "tmdb")}
}

func metadataCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("metadata: want refresh or status")
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
		if st.Unmatched > 0 {
			fmt.Println("run `vcrx metadata status` to see what was skipped and why")
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
