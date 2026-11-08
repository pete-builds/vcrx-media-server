package web

import (
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Pete, 2026-11-04: "Change live TV to say IP TV." The channels VCRX plays come
// from M3U playlists, never an antenna, so every label a viewer reads says
// IPTV. Config keys (`live_tv`), the grant (`allow_live_tv`) and the API paths
// (/api/v1/live/...) keep their names so existing installs keep working; only
// words on screen changed. This fails if the old name comes back in the web app.
var oldLiveName = regexp.MustCompile(`(?i)\blive[ -]?tv\b`)

// wireNames keep the old words on purpose: the /me field, the policy field and
// the variable holding its checkbox. They are blanked before matching.
var wireNames = regexp.MustCompile(`\b(allow_live_tv|live_tv|liveTv)\b`)

func liveTVLabels(name, src string) []string {
	var bad []string
	for i, line := range strings.Split(src, "\n") {
		if oldLiveName.MatchString(wireNames.ReplaceAllString(line, "")) {
			bad = append(bad, name+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
		}
	}
	return bad
}

func TestNoLiveTVLabels(t *testing.T) {
	var bad []string
	err := fs.WalkDir(Static, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !strings.HasSuffix(path, ".js") && !strings.HasSuffix(path, ".html") && !strings.HasSuffix(path, ".css") {
			return nil
		}
		b, err := fs.ReadFile(Static, path)
		if err != nil {
			return err
		}
		bad = append(bad, liveTVLabels(path, string(b))...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) > 0 {
		t.Errorf("the web app says Live TV; it is IPTV:\n%s", strings.Join(bad, "\n"))
	}
}

// The control: every spelling of the old label is caught, and the wire names
// beside the new label are not.
func TestNoLiveTVLabelsControl(t *testing.T) {
	src := strings.Join([]string{
		`h("a", { href: "#/live" }, "Live TV")`,
		`title: "Live tv"`,
		`"May watch LiveTV"`,
		`"live-tv"`,
		`if (state.me && state.me.live_tv) links.push("IPTV")`,
		`checked: p.allow_live_tv, liveTv`,
	}, "\n")
	if got := liveTVLabels("control", src); len(got) != 4 {
		t.Fatalf("want the four old labels caught, got %d: %v", len(got), got)
	}
}
