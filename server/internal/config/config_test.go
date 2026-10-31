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
