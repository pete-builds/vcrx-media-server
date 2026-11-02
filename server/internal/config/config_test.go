package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultsAndEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VCRX_CONFIG_DIR", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 3750 || c.Addr() != "0.0.0.0:3750" || c.DBPath() != filepath.Join(dir, "vcrx.db") || c.SessionTTL != 90*24*time.Hour {
		t.Fatalf("defaults: %+v", c)
	}
	t.Setenv("VCRX_PORT", "4000")
	t.Setenv("VCRX_MEDIA_ROOTS", "/media, /srv/more")
	t.Setenv("VCRX_SESSION_TTL_DAYS", "7")
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 4000 || len(c.MediaRoots) != 2 || c.MediaRoots[1] != "/srv/more" || c.SessionTTL != 7*24*time.Hour {
		t.Fatalf("env: %+v", c)
	}
	if c.MaxTranscodes != 2 {
		t.Fatalf("max transcodes default %d", c.MaxTranscodes)
	}
	t.Setenv("VCRX_MAX_TRANSCODES", "0")
	if c, err = Load(); err != nil || c.MaxTranscodes != 0 {
		t.Fatalf("transcoding off: %d %v", c.MaxTranscodes, err)
	}
	t.Setenv("VCRX_MAX_TRANSCODES", "-1")
	if _, err := Load(); err == nil {
		t.Fatal("negative max transcodes accepted")
	}
	t.Setenv("VCRX_MAX_TRANSCODES", "")
	if c.MaxTranscodeHeight != 1080 {
		t.Fatalf("max transcode height default %d", c.MaxTranscodeHeight)
	}
	t.Setenv("VCRX_MAX_TRANSCODE_HEIGHT", "0")
	if c, err = Load(); err != nil || c.MaxTranscodeHeight != 0 {
		t.Fatalf("no transcode cap: %d %v", c.MaxTranscodeHeight, err)
	}
	t.Setenv("VCRX_MAX_TRANSCODE_HEIGHT", "2160")
	if c, err = Load(); err != nil || c.MaxTranscodeHeight != 2160 {
		t.Fatalf("4K transcodes: %d %v", c.MaxTranscodeHeight, err)
	}
	t.Setenv("VCRX_MAX_TRANSCODE_HEIGHT", "-1")
	if _, err := Load(); err == nil {
		t.Fatal("negative max transcode height accepted")
	}
	t.Setenv("VCRX_MAX_TRANSCODE_HEIGHT", "")
	t.Setenv("VCRX_PORT", "seventy")
	if _, err := Load(); err == nil {
		t.Fatal("bad port accepted")
	}
}

func TestYAMLFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VCRX_CONFIG_DIR", dir)
	write := func(s string) {
		if err := os.WriteFile(filepath.Join(dir, "vcrx.yaml"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("server_name: Den\nlibraries:\n  - name: Movies\n    kind: movie\n    path: /media/Movies\n")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerName != "Den" || len(c.Libraries) != 1 || c.Libraries[0].Path != "/media/Movies" {
		t.Fatalf("yaml: %+v", c)
	}
	t.Setenv("VCRX_SERVER_NAME", "Env wins")
	if c, _ = Load(); c.ServerName != "Env wins" {
		t.Fatalf("env did not override yaml: %q", c.ServerName)
	}
	write("")
	if _, err := Load(); err != nil {
		t.Fatalf("empty file: %v", err)
	}
	write("sevrer_name: typo\n")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "sevrer_name") {
		t.Fatalf("unknown key: %v", err)
	}
	write("libraries:\n  - name: Music\n    kind: music\n    path: /media/Music\n")
	if _, err := Load(); err != nil {
		t.Fatalf("a music library refused: %v", err)
	}
	write("libraries:\n  - name: Books\n    kind: book\n    path: /media/Books\n")
	if _, err := Load(); err == nil {
		t.Fatal("unsupported library kind accepted")
	}
	write("media_roots: [relative/path]\n")
	if _, err := Load(); err == nil {
		t.Fatal("relative media root accepted")
	}
}

func TestTMDbKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VCRX_CONFIG_DIR", dir)
	c, err := Load()
	if err != nil || c.TMDbAPIKey != "" || c.TMDbLanguage != "en-US" || c.TMDbCountry != "US" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	keyFile := filepath.Join(dir, "tmdb.key")
	if err := os.WriteFile(keyFile, []byte("  from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VCRX_TMDB_API_KEY_FILE", keyFile)
	if c, err = Load(); err != nil || c.TMDbAPIKey != "from-file" {
		t.Fatalf("key file: %q %v", c.TMDbAPIKey, err)
	}
	t.Setenv("VCRX_TMDB_API_KEY", " from-env ")
	if c, err = Load(); err != nil || c.TMDbAPIKey != "from-env" {
		t.Fatalf("env wins: %q %v", c.TMDbAPIKey, err)
	}
	if want := filepath.Join(dir, "metadata", "tmdb"); c.MetadataDir() != want {
		t.Fatalf("metadata dir %q", c.MetadataDir())
	}
}

func TestTrustedNetworksHaveNoDefault(t *testing.T) {
	t.Setenv("VCRX_CONFIG_DIR", t.TempDir())
	c, err := Load()
	// No default: private ranges include the server's own Docker bridges.
	if err != nil || c.AutoSignInUser != "" || len(c.TrustedNetworks) != 0 {
		t.Fatalf("defaults: %q %v %v", c.AutoSignInUser, c.TrustedNetworks, err)
	}
	t.Setenv("VCRX_AUTO_SIGN_IN_USER", "pete")
	t.Setenv("VCRX_TRUSTED_NETWORKS", "192.168.1.0/24, 100.64.0.0/10")
	t.Setenv("VCRX_ALLOWED_HOSTS", "nix1.tail1234.ts.net")
	if c, err = Load(); err != nil || c.AutoSignInUser != "pete" || len(c.TrustedNetworks) != 2 || len(c.AllowedHosts) != 1 {
		t.Fatalf("env: %q %v %v %v", c.AutoSignInUser, c.TrustedNetworks, c.AllowedHosts, err)
	}
}

// A trusted proxy of 0.0.0.0/0 or ::/0 lets any client name its own address in
// X-Forwarded-For, so the server refuses to start with one.
func TestTrustedProxiesRefuseEveryAddress(t *testing.T) {
	t.Setenv("VCRX_CONFIG_DIR", t.TempDir())
	for _, bad := range []string{"0.0.0.0/0", "::/0", "127.0.0.1, 0.0.0.0/0"} {
		t.Setenv("VCRX_TRUSTED_PROXIES", bad)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "trusts every address") {
			t.Errorf("%q: %v, want a refusal", bad, err)
		}
	}
	t.Setenv("VCRX_TRUSTED_PROXIES", "127.0.0.1/32, ::1")
	if c, err := Load(); err != nil || len(c.TrustedProxies) != 2 {
		t.Fatalf("loopback proxy: %v %v", c.TrustedProxies, err)
	}
}

// YouTube trailers are off unless asked for, and need a TMDb key as well.
func TestTrailersYouTubeIsOptIn(t *testing.T) {
	t.Setenv("VCRX_CONFIG_DIR", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.TrailersYouTube || c.YouTubeTrailers() {
		t.Fatal("YouTube trailers on by default")
	}
	t.Setenv("VCRX_TRAILERS_YOUTUBE", "true")
	if c, _ = Load(); !c.TrailersYouTube || c.YouTubeTrailers() {
		t.Fatalf("without a TMDb key: setting %v, on %v", c.TrailersYouTube, c.YouTubeTrailers())
	}
	t.Setenv("VCRX_TMDB_API_KEY", "fake-tmdb-key")
	if c, _ = Load(); !c.YouTubeTrailers() {
		t.Fatal("trailers_youtube with a key is not on")
	}
}

// Marker detection is on by default, only at night, and an hours value that
// cannot be read stops the server rather than running at any hour.
func TestMarkerSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VCRX_CONFIG_DIR", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.DetectMarkers || c.MarkerHours != "01:00-06:00" || c.MarkerIntroMinutes != 10 || c.MarkerCreditsMinutes != 8 {
		t.Fatalf("defaults: on %v hours %q intro %d credits %d", c.DetectMarkers, c.MarkerHours, c.MarkerIntroMinutes, c.MarkerCreditsMinutes)
	}
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(dir, "vcrx.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("detect_markers: false\nmarker_hours: \"22:00-05:30\"\n")
	if c, err = Load(); err != nil || c.DetectMarkers || c.MarkerHours != "22:00-05:30" {
		t.Fatalf("yaml: %v %v %q", err, c.DetectMarkers, c.MarkerHours)
	}
	for _, bad := range []string{"marker_hours: \"\"\n", "marker_hours: evenings\n", "marker_intro_minutes: 0\n"} {
		write(bad)
		if _, err := Load(); err == nil {
			t.Errorf("%q accepted", strings.TrimSpace(bad))
		}
	}
	write("")
	t.Setenv("VCRX_MARKER_HOURS", "always")
	if c, err = Load(); err != nil || c.MarkerHours != "always" {
		t.Fatalf("env: %v %q", err, c.MarkerHours)
	}
}

func TestMaxTranscodeHeightFromYAML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VCRX_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "vcrx.yaml"), []byte("max_transcode_height: 720\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(); err != nil || c.MaxTranscodeHeight != 720 {
		t.Fatalf("yaml: %d %v", c.MaxTranscodeHeight, err)
	}
}
