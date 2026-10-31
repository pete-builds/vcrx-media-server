package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// OpenSubtitles is off unless a key is set; the key and password are secrets
// that never come from the YAML file.
func TestOpenSubtitlesSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VCRX_CONFIG_DIR", dir)
	c, err := Load()
	if err != nil || c.OpenSubtitlesAPIKey != "" || c.OpenSubtitlesPassword != "" || c.OpenSubtitlesStub || c.SubtitleAutoFetch ||
		strings.Join(c.SubtitleLanguages, ",") != "en" {
		t.Fatalf("defaults: %+v %v", c, err)
	}
	if want := filepath.Join(dir, "subtitles"); c.SubtitleDownloadDir() != want {
		t.Fatalf("download dir %q, want it under the config dir", c.SubtitleDownloadDir())
	}
	pw := filepath.Join(dir, "os.pw")
	if err := os.WriteFile(pw, []byte(" hunter2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VCRX_OPENSUBTITLES_API_KEY", " key ")
	t.Setenv("VCRX_OPENSUBTITLES_USERNAME", "viewer")
	t.Setenv("VCRX_OPENSUBTITLES_PASSWORD_FILE", pw)
	t.Setenv("VCRX_SUBTITLE_LANGUAGES", "en, es")
	t.Setenv("VCRX_SUBTITLE_AUTO_FETCH", "true")
	c, err = Load()
	if err != nil || c.OpenSubtitlesAPIKey != "key" || c.OpenSubtitlesUsername != "viewer" || c.OpenSubtitlesPassword != "hunter2" ||
		strings.Join(c.SubtitleLanguages, ",") != "en,es" || !c.SubtitleAutoFetch {
		t.Fatalf("env: %+v %v", c, err)
	}
	// The secrets are not YAML keys at all: a file naming one is refused.
	if err := os.WriteFile(filepath.Join(dir, "vcrx.yaml"), []byte("opensubtitles_password: oops\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("a password in vcrx.yaml was accepted")
	}
}
