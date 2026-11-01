package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Live TV plays only the playlists listed here. A provider's URL carries the
// account's credentials, so it can live in a file of its own, and no error
// message ever repeats it.
func TestLiveTVSources(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VCRX_CONFIG_DIR", dir)
	c, err := Load()
	if err != nil || len(c.LiveTV.Sources) != 0 || c.LiveRefreshInterval() != 12*time.Hour {
		t.Fatalf("defaults: %+v %v", c.LiveTV, err)
	}
	secret := filepath.Join(dir, "provider.url")
	if err := os.WriteFile(secret, []byte(" https://provider.example/get.php?username=me&password=s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	yaml := "live_tv:\n  refresh_hours: 6\n  sources:\n" +
		"    - name: Free\n      m3u: https://playlists.example/free.m3u\n" +
		"    - name: Provider\n      m3u_url_file: " + secret + "\n" +
		"    - name: Local file\n      m3u: /config/channels.m3u\n"
	if err := os.WriteFile(filepath.Join(dir, "vcrx.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	got := c.LiveTV.Sources
	if len(got) != 3 || got[0].Location != "https://playlists.example/free.m3u" ||
		got[1].Location != "https://provider.example/get.php?username=me&password=s3cret" || got[2].Location != "/config/channels.m3u" ||
		c.LiveRefreshInterval() != 6*time.Hour {
		t.Fatalf("sources %+v, interval %v", got, c.LiveRefreshInterval())
	}

	for name, body := range map[string]string{
		"no name":     "live_tv:\n  sources:\n    - m3u: https://a.example/x.m3u\n",
		"twice":       "live_tv:\n  sources:\n    - {name: A, m3u: https://a.example/x.m3u}\n    - {name: A, m3u: https://b.example/y.m3u}\n",
		"both":        "live_tv:\n  sources:\n    - {name: A, m3u: https://a.example/x.m3u, m3u_url_file: " + secret + "}\n",
		"neither":     "live_tv:\n  sources:\n    - {name: A}\n",
		"relative":    "live_tv:\n  sources:\n    - {name: A, m3u: channels.m3u}\n",
		"rtsp":        "live_tv:\n  sources:\n    - {name: A, m3u: rtsp://user:hunter2@cam.example/live}\n",
		"refresh":     "live_tv:\n  refresh_hours: 500\n",
		"unknown key": "live_tv:\n  channels: []\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, "vcrx.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Load()
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: the error repeats the URL's password: %v", name, err)
		}
	}
}
