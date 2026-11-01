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
	"github.com/pete-builds/vcrx-media-server/server/internal/markers"
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

// LiveTVSpec is the live_tv section of vcrx.yaml.
type LiveTVSpec struct {
	Sources []LiveSourceSpec `yaml:"sources"`
	// RefreshHours is how often every playlist is read again (12 by default).
	RefreshHours int `yaml:"refresh_hours"`
	// GuideRefreshHours is how often every guide is read again (6 by default).
	GuideRefreshHours int `yaml:"guide_refresh_hours"`
	// MaxStreams caps channels being watched at once across all playlists (4
	// by default); each is one pull, shared by everyone watching it.
	MaxStreams int `yaml:"max_streams"`
}

// LiveSourceSpec is one M3U playlist: m3u is an http(s) URL or an absolute
// path to a playlist file. A URL that carries an account's credentials goes
// in a file of its own instead (m3u_url_file), the way secrets do, so
// vcrx.yaml never holds it.
type LiveSourceSpec struct {
	Name       string `yaml:"name"`
	M3U        string `yaml:"m3u"`
	M3UURLFile string `yaml:"m3u_url_file"`
	// XMLTV is the playlist's guide, optional: an http(s) URL or an absolute
	// path, gzipped or not; xmltv_url_file holds a URL with credentials. A
	// playlist's own x-tvg-url is never followed: someone else wrote it.
	XMLTV        string `yaml:"xmltv"`
	XMLTVURLFile string `yaml:"xmltv_url_file"`
	// MaxStreams is how many of this playlist's channels may be pulled at
	// once (a provider's connection limit); 0 is no limit of its own.
	MaxStreams int `yaml:"max_streams"`
	// Location is m3u, or the URL read from m3u_url_file; GuideLocation is
	// the same for the guide.
	Location      string `yaml:"-"`
	GuideLocation string `yaml:"-"`
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
	// AutoSignInPicker shows trusted-network clients the "Who's watching?"
	// profile picker instead of signing them in as AutoSignInUser, who is then
	// only the profile the picker starts on. Off (sign straight in) by default.
	AutoSignInPicker bool `yaml:"auto_sign_in_picker"`
	// AllowedHosts are host names the server answers to besides IP addresses
	// and localhost (for example a Tailscale MagicDNS name). A request for any
	// other name is refused, which stops DNS rebinding: a web page that points
	// its own domain at this server cannot talk to it.
	AllowedHosts []string `yaml:"allowed_hosts"`
	// PeerAllowLoopback lets this server link to a friend's server on
	// 127.0.0.1 or ::1. For running two servers on one machine in
	// development only: a production peer on loopback is this server itself.
	PeerAllowLoopback bool `yaml:"peer_allow_loopback"`

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
	// TMDbHomeLists adds Home rows from TMDb's trending and popular lists,
	// limited to titles in the library. Off by default; needs TMDbAPIKey. The
	// lists refresh every six hours and the last good copy is kept offline.
	TMDbHomeLists bool `yaml:"tmdb_home_lists"`
	// TrailersYouTube looks up each title's official YouTube trailer on TMDb and
	// offers it when there is no local trailer file. Off by default: it needs the
	// internet to play, and VCRX works without it. Needs a TMDb key.
	TrailersYouTube bool `yaml:"trailers_youtube"`
	// PrerollDir is a folder of short clips (mp4, m4v, mkv, mov, webm). A
	// profile that turns on Before a movie > Pre-roll sees one of them, picked
	// at random, before each movie. Empty: no pre-roll.
	PrerollDir string `yaml:"preroll_dir"`

	// DetectMarkers finds intros and end credits in TV episodes from their
	// audio, for Skip Intro and Skip Credits. On by default. It runs at low
	// priority, one season at a time, only during MarkerHours.
	DetectMarkers bool `yaml:"detect_markers"`
	// MarkerHours is when detection may run, local time (set TZ): "01:00-06:00"
	// by default, or "always".
	MarkerHours string `yaml:"marker_hours"`
	// MarkerIntroMinutes and MarkerCreditsMinutes are how much of the start
	// and of the end of each episode are compared (10 and 8).
	MarkerIntroMinutes   int `yaml:"marker_intro_minutes"`
	MarkerCreditsMinutes int `yaml:"marker_credits_minutes"`
	// OpenSubtitlesAPIKey turns on subtitle search (opensubtitles.com, REST API
	// v1). Downloads also need OpenSubtitlesUsername and OpenSubtitlesPassword,
	// because the daily quota belongs to an account. The key and password are
	// secrets: VCRX_OPENSUBTITLES_API_KEY and VCRX_OPENSUBTITLES_PASSWORD, or
	// their *_FILE forms, never the YAML file.
	OpenSubtitlesAPIKey   string `yaml:"-"`
	OpenSubtitlesUsername string `yaml:"opensubtitles_username"`
	OpenSubtitlesPassword string `yaml:"-"`
	// OpenSubtitlesStub answers searches with invented results and never
	// touches the network. For the dev server and its browser test only.
	OpenSubtitlesStub bool `yaml:"-"`
	// SubtitleLanguages are the languages searched when a client names none,
	// and the ones the automatic fetch looks for. Default: English.
	SubtitleLanguages []string `yaml:"subtitle_languages"`
	// SubtitleAutoFetch downloads the preferred languages for files added from
	// now on that have no subtitle in them, once per file. Off by default.
	SubtitleAutoFetch bool `yaml:"subtitle_auto_fetch"`

	Libraries []LibrarySpec `yaml:"libraries"`

	// LiveTV is Live TV's playlists (milestone 11). VCRX ships and suggests
	// none: it plays what the admin lists here.
	LiveTV LiveTVSpec `yaml:"live_tv"`

	// CollectionsDir holds the smart collection YAML files. Empty means
	// $VCRX_CONFIG_DIR/collections. VCRX_COLLECTIONS_DIR overrides it.
	CollectionsDir string `yaml:"collections_dir"`
}

func defaults() Config {
	return Config{
		ServerName:        "VCRX",
		Bind:              "0.0.0.0",
		Port:              3750,
		ConfigDir:         "/config",
		CacheDir:          "/cache",
		MediaRoots:        []string{"/media"},
		FFprobePath:       "ffprobe",
		FFmpegPath:        "ffmpeg",
		ProbeConcurrency:  4,
		MaxTranscodes:     2,
		ScanOnStartup:     true,
		LogLevel:          "info",
		LogFormat:         "json",
		SessionTTL:        90 * 24 * time.Hour,
		TMDbLanguage:      "en-US",
		TMDbCountry:       "US",
		SubtitleLanguages: []string{"en"},

		DetectMarkers:        true,
		MarkerHours:          "01:00-06:00",
		MarkerIntroMinutes:   10,
		MarkerCreditsMinutes: 8,
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
	boolean("VCRX_TMDB_HOME_LISTS", &c.TMDbHomeLists)
	boolean("VCRX_TRAILERS_YOUTUBE", &c.TrailersYouTube)
	list("VCRX_TRUSTED_PROXIES", &c.TrustedProxies)
	str("VCRX_AUTO_SIGN_IN_USER", &c.AutoSignInUser)
	list("VCRX_TRUSTED_NETWORKS", &c.TrustedNetworks)
	boolean("VCRX_AUTO_SIGN_IN_PICKER", &c.AutoSignInPicker)
	boolean("VCRX_PEER_ALLOW_LOOPBACK", &c.PeerAllowLoopback)
	list("VCRX_ALLOWED_HOSTS", &c.AllowedHosts)
	str("VCRX_COLLECTIONS_DIR", &c.CollectionsDir)
	boolean("VCRX_DETECT_MARKERS", &c.DetectMarkers)
	str("VCRX_MARKER_HOURS", &c.MarkerHours)
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
	secret := func(key string, dst *string) {
		str(key, dst)
		if v := os.Getenv(key + "_FILE"); v != "" && *dst == "" {
			b, err := os.ReadFile(v) //nolint:gosec // the operator chooses this path
			if err != nil {
				errs = append(errs, fmt.Errorf("%s_FILE: %w", key, err))
			} else {
				*dst = strings.TrimSpace(string(b))
			}
		}
		*dst = strings.TrimSpace(*dst)
	}
	secret("VCRX_OPENSUBTITLES_API_KEY", &c.OpenSubtitlesAPIKey)
	secret("VCRX_OPENSUBTITLES_PASSWORD", &c.OpenSubtitlesPassword)
	str("VCRX_OPENSUBTITLES_USERNAME", &c.OpenSubtitlesUsername)
	boolean("VCRX_OPENSUBTITLES_STUB", &c.OpenSubtitlesStub)
	list("VCRX_SUBTITLE_LANGUAGES", &c.SubtitleLanguages)
	boolean("VCRX_SUBTITLE_AUTO_FETCH", &c.SubtitleAutoFetch)

	for i := range c.LiveTV.Sources {
		src := &c.LiveTV.Sources[i]
		src.Location = strings.TrimSpace(src.M3U)
		if src.M3UURLFile != "" && src.Location == "" {
			b, err := os.ReadFile(src.M3UURLFile) //nolint:gosec // the operator chooses this path
			if err != nil {
				errs = append(errs, fmt.Errorf("live_tv source %q: m3u_url_file: %w", src.Name, err))
			} else {
				src.Location = strings.TrimSpace(string(b))
			}
		}
		src.GuideLocation = strings.TrimSpace(src.XMLTV)
		if src.XMLTVURLFile != "" && src.GuideLocation == "" {
			b, err := os.ReadFile(src.XMLTVURLFile) //nolint:gosec // the operator chooses this path
			if err != nil {
				errs = append(errs, fmt.Errorf("live_tv source %q: xmltv_url_file: %w", src.Name, err))
			} else {
				src.GuideLocation = strings.TrimSpace(string(b))
			}
		}
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
	if _, err := markers.ParseHours(c.MarkerHours); err != nil {
		errs = append(errs, fmt.Errorf("marker_hours: %w", err))
	}
	if c.MarkerIntroMinutes < 1 || c.MarkerIntroMinutes > 30 || c.MarkerCreditsMinutes < 1 || c.MarkerCreditsMinutes > 30 {
		errs = append(errs, errors.New("marker_intro_minutes and marker_credits_minutes must be 1 to 30"))
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
	if h := c.LiveTV.RefreshHours; h < 0 || h > 168 {
		errs = append(errs, fmt.Errorf("live_tv.refresh_hours %d must be 1 to 168, or 0 for the default", h))
	}
	if n := c.LiveTV.MaxStreams; n < 0 || n > 64 {
		errs = append(errs, fmt.Errorf("live_tv.max_streams %d must be 1 to 64, or 0 for the default", n))
	}
	if h := c.LiveTV.GuideRefreshHours; h < 0 || h > 168 {
		errs = append(errs, fmt.Errorf("live_tv.guide_refresh_hours %d must be 1 to 168, or 0 for the default", h))
	}
	seen := map[string]bool{}
	for _, s := range c.LiveTV.Sources {
		switch {
		case strings.TrimSpace(s.Name) == "":
			errs = append(errs, errors.New("every live_tv source needs a name"))
		case seen[s.Name]:
			errs = append(errs, fmt.Errorf("live_tv source %q is listed twice", s.Name))
		case s.M3U != "" && s.M3UURLFile != "":
			errs = append(errs, fmt.Errorf("live_tv source %q: give m3u or m3u_url_file, not both", s.Name))
		case s.M3U == "" && s.M3UURLFile == "":
			errs = append(errs, fmt.Errorf("live_tv source %q needs m3u or m3u_url_file", s.Name))
		case s.Location != "" && !strings.HasPrefix(s.Location, "http://") && !strings.HasPrefix(s.Location, "https://") && !filepath.IsAbs(s.Location):
			// Say which key, never the value: it may be a URL with credentials.
			errs = append(errs, fmt.Errorf("live_tv source %q: the playlist must be an http(s) URL or an absolute file path", s.Name))
		case s.MaxStreams < 0:
			errs = append(errs, fmt.Errorf("live_tv source %q: max_streams cannot be negative", s.Name))
		case s.XMLTV != "" && s.XMLTVURLFile != "":
			errs = append(errs, fmt.Errorf("live_tv source %q: give xmltv or xmltv_url_file, not both", s.Name))
		case s.GuideLocation != "" && !strings.HasPrefix(s.GuideLocation, "http://") && !strings.HasPrefix(s.GuideLocation, "https://") &&
			!filepath.IsAbs(s.GuideLocation):
			errs = append(errs, fmt.Errorf("live_tv source %q: the guide must be an http(s) URL or an absolute file path", s.Name))
		}
		seen[s.Name] = true
	}
	return errors.Join(errs...)
}

// LiveRefreshInterval is how often Live TV playlists are read again.
func (c Config) LiveRefreshInterval() time.Duration {
	if c.LiveTV.RefreshHours > 0 {
		return time.Duration(c.LiveTV.RefreshHours) * time.Hour
	}
	return 12 * time.Hour
}

// LiveGuideInterval is how often Live TV guides are read again.
func (c Config) LiveGuideInterval() time.Duration {
	if c.LiveTV.GuideRefreshHours > 0 {
		return time.Duration(c.LiveTV.GuideRefreshHours) * time.Hour
	}
	return 6 * time.Hour
}

// LiveMaxStreams caps channels watched at once across all playlists.
func (c Config) LiveMaxStreams() int {
	if c.LiveTV.MaxStreams > 0 {
		return c.LiveTV.MaxStreams
	}
	return 4
}

// LiveStreamDir holds the live HLS segments while channels are watched.
func (c Config) LiveStreamDir() string { return filepath.Join(c.CacheDir, "live") }

// LogoCacheDir holds Live TV channel logos.
func (c Config) LogoCacheDir() string { return filepath.Join(c.CacheDir, "channel-logos") }

// YouTubeTrailers reports whether YouTube trailers are on: asked for, and a
// TMDb key to look them up with.
func (c Config) YouTubeTrailers() bool { return c.TrailersYouTube && c.TMDbAPIKey != "" }

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

// AvatarDir holds household profile pictures. It is data, not cache: an
// upload cannot be rebuilt.
func (c Config) AvatarDir() string { return filepath.Join(c.ConfigDir, "avatars") }

func (c Config) ArtworkCacheDir() string { return filepath.Join(c.CacheDir, "artwork") }

// SubtitleCacheDir holds WebVTT copies of text subtitles for browsers.
func (c Config) SubtitleCacheDir() string { return filepath.Join(c.CacheDir, "subtitles") }

// HLSDir holds the segments of HLS streams while they play. Each session's
// files are deleted when it ends, and the whole directory's leftovers at
// startup.
func (c Config) HLSDir() string { return filepath.Join(c.CacheDir, "hls") }

// SubtitleDownloadDir holds subtitles downloaded from OpenSubtitles. It is
// under the config dir, beside the database that lists them, and never under
// a library: the media mounts are read-only.
func (c Config) SubtitleDownloadDir() string { return filepath.Join(c.ConfigDir, "subtitles") }

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
