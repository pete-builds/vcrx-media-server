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
