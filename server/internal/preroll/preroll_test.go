package preroll

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
)

func TestFolderListsOnlyClipsAndOpensOnlyThose(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "Pre-Roll")
	write := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("clip"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"Retro Countdown.mp4", "B Static.MKV", ".hidden.mp4", "notes.txt", "poster.jpg"} {
		write(filepath.Join(dir, n))
	}
	write(filepath.Join(dir, "Nested", "deep.mp4"))
	// A clip-looking link out of the folder, to a file beside it.
	write(filepath.Join(root, "secret.mp4"))
	if err := os.Symlink(filepath.Join(root, "secret.mp4"), filepath.Join(dir, "escape.mp4")); err != nil {
		t.Fatal(err)
	}
	f := Folder{Dir: dir}

	if got := strings.Join(f.Clips(), ","); got != "B Static.MKV,Retro Countdown.mp4,escape.mp4" {
		t.Fatalf("clips = %s", got)
	}
	file, _, err := f.Open("Retro Countdown.mp4")
	if err != nil {
		t.Fatalf("open a clip: %v", err)
	}
	_ = file.Close()
	for _, bad := range []string{"notes.txt", ".hidden.mp4", "Nested/deep.mp4", "../secret.mp4", "Nested", "", "missing.mp4"} {
		if _, _, err := f.Open(bad); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Open(%q) = %v, want not found", bad, err)
		}
	}
	if _, _, err := f.Open("escape.mp4"); !errors.Is(err, fsafe.ErrOutsideRoot) {
		t.Errorf("a link out of the folder opened: %v", err)
	}
	if p := f.Pick(); !f.Has(p) {
		t.Errorf("Pick() = %q, not a clip", p)
	}
	if Title("Retro Countdown.mp4") != "Retro Countdown" {
		t.Errorf("title = %q", Title("Retro Countdown.mp4"))
	}

	// No folder set, or one that is gone: no clips, nothing opens.
	for _, empty := range []Folder{{}, {Dir: filepath.Join(root, "gone")}} {
		if len(empty.Clips()) != 0 || empty.Pick() != "" || empty.Has("Retro Countdown.mp4") {
			t.Errorf("%+v has clips", empty)
		}
	}
}
