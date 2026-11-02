package web

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Pete, 2026-11-01: "the green buttons on vcrx are too intense. don't use full
// green buttons." A button takes --accent-tint with a green ring instead (see
// brand/README.md). Solid green stays only on small marks, listed here; any
// other rule that fills with the full green fails, so a new button cannot
// bring it back. A new mark is added to this list on purpose, never a control.
var solidGreenMarks = map[string]bool{
	".nav-link.active::after":            true, // the active tab's underline
	".load-cursor":                       true, // the blinking loading cursor
	".badge":                             true, // unwatched count on a poster
	".progress > i":                      true,
	".resume-bar .bar > i":               true,
	".session .bar > i":                  true,
	".rank .meter > i":                   true,
	".live-bar span":                     true,
	".pin-dots i.on":                     true, // PIN digits entered so far
	".live-badge":                        true, // LIVE on a live recording's cover
	".track.is-playing .track-no::after": true, // the playing track's pulse
}

var (
	cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	cssRule    = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)
	solidGreen = regexp.MustCompile(`(?i)background(?:-color)?\s*:\s*(?:var\(--accent(?:-soft)?\)|#008f11|#1fae30)\s*(?:!important\s*)?(?:;|$)`)
)

func solidGreenRules(t *testing.T, name, css string) []string {
	t.Helper()
	var bad []string
	for _, m := range cssRule.FindAllStringSubmatch(cssComment.ReplaceAllString(css, ""), -1) {
		if !solidGreen.MatchString(m[2]) {
			continue
		}
		for _, sel := range strings.Split(m[1], ",") {
			sel = strings.Join(strings.Fields(sel), " ")
			if !solidGreenMarks[sel] {
				bad = append(bad, name+": "+sel)
			}
		}
	}
	return bad
}

func TestNoSolidGreenButtons(t *testing.T) {
	var bad []string
	files := 0
	err := fs.WalkDir(Static, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".css") {
			return err
		}
		b, err := fs.ReadFile(Static, path)
		if err != nil {
			return err
		}
		files++
		bad = append(bad, solidGreenRules(t, path, string(b))...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("no stylesheets found, so this check proves nothing")
	}
	sort.Strings(bad)
	if len(bad) > 0 {
		t.Errorf("solid green fill on something that is not a listed mark (use --accent-tint with a ring for a button):\n  %s", strings.Join(bad, "\n  "))
	}
}

// The control: the check must catch the old primary button and its hover.
func TestNoSolidGreenButtonsControl(t *testing.T) {
	old := `.btn-primary { background: var(--accent); color: #000; }
@media (hover: hover) { .btn-primary:hover, .x { background-color: #008F11 } }
.progress > i { background: var(--accent); }
.btn-ok { background: var(--accent-tint); }`
	got := solidGreenRules(t, "control.css", old)
	want := []string{"control.css: .btn-primary", "control.css: .btn-primary:hover", "control.css: .x"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("control: got %q, want %q", got, want)
	}
}
