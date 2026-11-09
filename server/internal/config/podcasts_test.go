package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Podcasts are off unless vcrx.yaml turns them on, search is off unless
// asked for separately, and the loopback allowance is empty by default.
func TestPodcastsConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VCRX_CONFIG_DIR", dir)
	c, err := Load()
	if err != nil || c.Podcasts.Enabled || c.Podcasts.Search || len(c.Podcasts.AllowHosts) != 0 || c.PodcastRefreshInterval() != 4*time.Hour {
		t.Fatalf("defaults: %+v %v", c.Podcasts, err)
	}
	yaml := "podcasts:\n  enabled: true\n  refresh_hours: 2\n  max_episodes: 50\n  search: true\n  allow_hosts: [\"127.0.0.1:3881\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "vcrx.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = Load()
	if err != nil || !c.Podcasts.Enabled || !c.Podcasts.Search || c.Podcasts.MaxEpisodes != 50 || c.PodcastRefreshInterval() != 2*time.Hour ||
		len(c.Podcasts.AllowHosts) != 1 {
		t.Fatalf("set: %+v %v", c.Podcasts, err)
	}
	for bad, want := range map[string]string{
		"podcasts:\n  refresh_hours: 500\n":           "refresh_hours",
		"podcasts:\n  max_episodes: 99999\n":          "max_episodes",
		"podcasts:\n  allow_hosts: [\"http://x/\"]\n": "allow_hosts",
		"podcasts:\n  enable: true\n":                 "enable",
	} {
		if err := os.WriteFile(filepath.Join(dir, "vcrx.yaml"), []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want an error naming %s", bad, err, want)
		}
	}
}
