// Package config loads settings from environment variables and an optional YAML
// file at $VCRX_CONFIG_DIR/vcrx.yaml. Environment variables win over the file.
// Secrets come from env or from a *_FILE path, never from the repo.
package config

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/pete-builds/vcrx-media-server/server/internal/arr"
	"github.com/pete-builds/vcrx-media-server/server/internal/overlay"
)

// LibrarySpec is a library declared in vcrx.yaml. Libraries are matched by name and
// upserted at startup; libraries missing from the file are left alone.
type LibrarySpec struct {
	Name                string `yaml:"name"`
	Kind                string `yaml:"kind"`
	Path                string `yaml:"path"`
	Grouping            string `yaml:"grouping"`
	ScanIntervalMinutes int    `yaml:"scan_interval_minutes"`
	// Overlays are drawn onto this library's posters (rating, resolution, ribbon).
	Overlays []overlay.Spec `yaml:"overlays"`
}

type Config struct {
	ServerName string `yaml:"server_name"`
	Bind       string `yaml:"bind"`
	Port       int    `yaml:"port"`

	ConfigDir string `yaml:"-"`
	CacheDir  string `yaml:"cache_dir"`
	// MediaRoots are the only directories a library root may sit under.
	MediaRoots []string `yaml:"media_roots"`

	FFprobePath      string `yaml:"ffprobe"`
	FFmpegPath       string `yaml:"ffmpeg"`
	ProbeConcurrency int    `yaml:"probe_concurrency"`
	ScanOnStartup    bool   `yaml:"scan_on_startup"`
	// MaxTranscodes caps video transcodes running at once; each takes one or
	// more CPU cores. 0 turns video transcoding off.
	MaxTranscodes int `yaml:"max_transcodes"`

	LogLevel  string `yaml:"log_level"`
	LogFormat string `yaml:"log_format"`

	SessionTTL time.Duration `yaml:"-"`
	// AdminAllowRemote lets admin routes answer from public addresses. Off by default.
	AdminAllowRemote bool `yaml:"admin_allow_remote"`
	// TrustedProxies are reverse proxies whose X-Forwarded-For is believed.
	TrustedProxies []string `yaml:"trusted_proxies"`
	// AutoSignInUser is signed in without a password for clients on
	// TrustedNetworks. Empty (the default) turns that off.
	AutoSignInUser string `yaml:"auto_sign_in_user"`
	// TrustedNetworks are the networks auto sign-in trusts. There is no default:
	// "every private range" also covers the Docker and Incus bridges on a server
	// host, so a compromised container could sign in. List your own networks.
	TrustedNetworks []string `yaml:"trusted_networks"`
	// AllowedHosts are host names the server answers to besides IP addresses
	// and localhost (for example a Tailscale MagicDNS name). A request for any
	// other name is refused, which stops DNS rebinding: a web page that points
	// its own domain at this server cannot talk to it.
	AllowedHosts []string `yaml:"allowed_hosts"`

	// WebhookURLs receive playback events (play, stop, refused, terminated). A
	// Discord webhook URL gets Discord's message shape. The URLs carry tokens, so
	// they come from VCRX_WEBHOOK_URLS or VCRX_WEBHOOK_URLS_FILE, never the YAML file.
	WebhookURLs []string `yaml:"-"`
	// WebhookEvents limits which events are sent; empty sends all of them.
	WebhookEvents []string `yaml:"webhook_events"`

	// ArrWebhookToken turns on POST /api/v1/hooks/arr for Radarr and Sonarr. It
	// is a secret, so it comes from VCRX_ARR_WEBHOOK_TOKEN or
	// VCRX_ARR_WEBHOOK_TOKEN_FILE, never the YAML file.
	ArrWebhookToken string `yaml:"-"`
	// ArrPathMap rewrites a Radarr or Sonarr path into the same place as VCRX
	// sees it. Without it, a folder is found by its name in the libraries.
	ArrPathMap []arr.PathMap `yaml:"arr_path_map"`

	// IMDbRatings downloads IMDb's public ratings file once a day and stores the
	// rating and vote count for every movie and show with an IMDb id. Off by
	// default: IMDb licenses the file for personal, non-commercial use.
	IMDbRatings bool `yaml:"imdb_ratings"`

	// TMDbAPIKey turns on TMDb enrichment: a v3 API key or a v4 read access token.
	// It comes from VCRX_TMDB_API_KEY or VCRX_TMDB_API_KEY_FILE, never the YAML file.
	TMDbAPIKey   string `yaml:"-"`
	TMDbLanguage string `yaml:"tmdb_language"`
	// TMDbCountry picks the content rating shown, e.g. US for PG-13.
	TMDbCountry string `yaml:"tmdb_country"`

	Libraries []LibrarySpec `yaml:"libraries"`

	// CollectionsDir holds the smart collection YAML files. Empty means
	// $VCRX_CONFIG_DIR/collections. VCRX_COLLECTIONS_DIR overrides it.
	CollectionsDir string `yaml:"collections_dir"`
}

func defaults() Config {
	return Config{
		ServerName:       "VCRX",
		Bind:             "0.0.0.0",
		Port:             3750,
		ConfigDir:        "/config",
		CacheDir:         "/cache",
		MediaRoots:       []string{"/media"},
		FFprobePath:      "ffprobe",
		FFmpegPath:       "ffmpeg",
		ProbeConcurrency: 4,
		MaxTranscodes:    2,
		ScanOnStartup:    true,
		LogLevel:         "info",
		LogFormat:        "json",
		SessionTTL:       90 * 24 * time.Hour,
		TMDbLanguage:     "en-US",
		TMDbCountry:      "US",
	}
}

// Load reads vcrx.yaml (if present) then the environment.
func Load() (Config, error) {
	c := defaults()
	if v := os.Getenv("VCRX_CONFIG_DIR"); v != "" {
		c.ConfigDir = v
	}
	file := filepath.Join(c.ConfigDir, "vcrx.yaml")
	if v := os.Getenv("VCRX_CONFIG_FILE"); v != "" {
		file = v
	}
	if b, err := os.ReadFile(file); err == nil { //nolint:gosec // the operator chooses this path
		dec := yaml.NewDecoder(strings.NewReader(string(b)))
		dec.KnownFields(true)
		if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
			return c, fmt.Errorf("%s: %w", file, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return c, fmt.Errorf("read %s: %w", file, err)
	}

	str := func(key string, dst *string) {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			*dst = v
		}
	}
	var errs []error
	integer := func(key string, dst *int) {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", key, err))
				return
			}
			*dst = n
		}
	}
	boolean := func(key string, dst *bool) {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", key, err))
				return
			}
			*dst = b
		}
	}
	list := func(key string, dst *[]string) {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			*dst = splitList(v)
		}
	}
	str("VCRX_SERVER_NAME", &c.ServerName)
	str("VCRX_BIND", &c.Bind)
	integer("VCRX_PORT", &c.Port)
	str("VCRX_CACHE_DIR", &c.CacheDir)
	list("VCRX_MEDIA_ROOTS", &c.MediaRoots)
	str("VCRX_FFPROBE", &c.FFprobePath)
	str("VCRX_FFMPEG", &c.FFmpegPath)
	integer("VCRX_PROBE_CONCURRENCY", &c.ProbeConcurrency)
	integer("VCRX_MAX_TRANSCODES", &c.MaxTranscodes)
	boolean("VCRX_SCAN_ON_STARTUP", &c.ScanOnStartup)
	str("VCRX_LOG_LEVEL", &c.LogLevel)
	str("VCRX_LOG_FORMAT", &c.LogFormat)
	boolean("VCRX_ADMIN_ALLOW_REMOTE", &c.AdminAllowRemote)
	boolean("VCRX_IMDB_RATINGS", &c.IMDbRatings)
	list("VCRX_TRUSTED_PROXIES", &c.TrustedProxies)
	str("VCRX_AUTO_SIGN_IN_USER", &c.AutoSignInUser)
	list("VCRX_TRUSTED_NETWORKS", &c.TrustedNetworks)
	list("VCRX_ALLOWED_HOSTS", &c.AllowedHosts)
	str("VCRX_COLLECTIONS_DIR", &c.CollectionsDir)
	str("VCRX_TMDB_LANGUAGE", &c.TMDbLanguage)
	str("VCRX_TMDB_COUNTRY", &c.TMDbCountry)
	str("VCRX_TMDB_API_KEY", &c.TMDbAPIKey)
	if v := os.Getenv("VCRX_TMDB_API_KEY_FILE"); v != "" && c.TMDbAPIKey == "" {
		b, err := os.ReadFile(v) //nolint:gosec // the operator chooses this path
		if err != nil {
			errs = append(errs, fmt.Errorf("VCRX_TMDB_API_KEY_FILE: %w", err))
		} else {
			c.TMDbAPIKey = strings.TrimSpace(string(b))
		}
	}
	c.TMDbAPIKey = strings.TrimSpace(c.TMDbAPIKey)
	list("VCRX_WEBHOOK_URLS", &c.WebhookURLs)
	if v := os.Getenv("VCRX_WEBHOOK_URLS_FILE"); v != "" && len(c.WebhookURLs) == 0 {
		b, err := os.ReadFile(v) //nolint:gosec // the operator chooses this path
		if err != nil {
			errs = append(errs, fmt.Errorf("VCRX_WEBHOOK_URLS_FILE: %w", err))
		} else {
			c.WebhookURLs = splitList(strings.ReplaceAll(string(b), "\n", ","))
		}
	}
	list("VCRX_WEBHOOK_EVENTS", &c.WebhookEvents)
	str("VCRX_ARR_WEBHOOK_TOKEN", &c.ArrWebhookToken)
	if v := os.Getenv("VCRX_ARR_WEBHOOK_TOKEN_FILE"); v != "" && c.ArrWebhookToken == "" {
		b, err := os.ReadFile(v) //nolint:gosec // the operator chooses this path
		if err != nil {
			errs = append(errs, fmt.Errorf("VCRX_ARR_WEBHOOK_TOKEN_FILE: %w", err))
		} else {
			c.ArrWebhookToken = strings.TrimSpace(string(b))
		}
	}
	if t := c.ArrWebhookToken; t != "" && len(t) < 16 {
		errs = append(errs, errors.New("VCRX_ARR_WEBHOOK_TOKEN must be at least 16 characters"))
	}
	ttlDays := int(c.SessionTTL / (24 * time.Hour))
	integer("VCRX_SESSION_TTL_DAYS", &ttlDays)
	c.SessionTTL = time.Duration(ttlDays) * 24 * time.Hour

	if err := c.validate(); err != nil {
		errs = append(errs, err)
	}
	return c, errors.Join(errs...)
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (c *Config) validate() error {
	var errs []error
	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, fmt.Errorf("port %d out of range", c.Port))
	}
	if c.ProbeConcurrency < 1 {
		c.ProbeConcurrency = 1
	}
	if c.MaxTranscodes < 0 {
		errs = append(errs, fmt.Errorf("max_transcodes %d must be 0 (off) or more", c.MaxTranscodes))
	}
	if c.SessionTTL < time.Hour {
		errs = append(errs, errors.New("session TTL must be at least one day"))
	}
	for i, r := range c.MediaRoots {
		if !filepath.IsAbs(r) {
			errs = append(errs, fmt.Errorf("media root %q must be absolute", r))
		}
		c.MediaRoots[i] = filepath.Clean(r)
	}
	for _, l := range c.Libraries {
		if l.Name == "" || l.Path == "" {
			errs = append(errs, errors.New("every library in vcrx.yaml needs a name and a path"))
		}
		if l.Kind != "movie" && l.Kind != "show" && l.Kind != "music" {
			errs = append(errs, fmt.Errorf("library %q: kind must be movie, show or music", l.Name))
		}
		if l.Grouping != "" && l.Grouping != "folder" && l.Grouping != "title_year" {
			errs = append(errs, fmt.Errorf("library %q: grouping must be folder or title_year", l.Name))
		}
		for _, o := range l.Overlays {
			if err := o.Validate(); err != nil {
				errs = append(errs, fmt.Errorf("library %q: %w", l.Name, err))
			}
		}
	}
	return errors.Join(errs...)
}

func (c Config) Addr() string { return fmt.Sprintf("%s:%d", c.Bind, c.Port) }

func (c Config) DBPath() string { return filepath.Join(c.ConfigDir, "vcrx.db") }

// SmartCollectionsDir is where smart collection definitions are read from.
func (c Config) SmartCollectionsDir() string {
	if c.CollectionsDir != "" {
		return c.CollectionsDir
	}
	return filepath.Join(c.ConfigDir, "collections")
}

func (c Config) BackupDir() string { return filepath.Join(c.ConfigDir, "backups") }

func (c Config) ArtworkCacheDir() string { return filepath.Join(c.CacheDir, "artwork") }

// SubtitleCacheDir holds WebVTT copies of text subtitles for browsers.
func (c Config) SubtitleCacheDir() string { return filepath.Join(c.CacheDir, "subtitles") }

// HLSDir holds the segments of HLS streams while they play. Each session's
// files are deleted when it ends, and the whole directory's leftovers at
// startup.
func (c Config) HLSDir() string { return filepath.Join(c.CacheDir, "hls") }

// MetadataDir holds images downloaded from TMDb. It sits under the config dir,
// not the cache, because downloading them again for a whole library is slow.
func (c Config) MetadataDir() string { return filepath.Join(c.ConfigDir, "metadata", "tmdb") }

// Logger builds the process logger from LogLevel and LogFormat.
func (c Config) Logger() *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(c.LogLevel)); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if c.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, opts))
}
