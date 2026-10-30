package fsafe

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWithin(t *testing.T) {
	cases := []struct {
		root, path string
		want       bool
	}{
		{"/media/Movies", "/media/Movies", true},
		{"/media/Movies", "/media/Movies/a/b.mkv", true},
		{"/media/Movies", "/media/Movies2/x.mkv", false},
		{"/media/Movies", "/media/TV/x.mkv", false},
		{"/media/Movies", "/media", false},
		{"/media/Movies", "/media/Movies/../TV/x.mkv", false},
		{"/media/Movies", "/media/Movies/..foo/x.mkv", true},
	}
	for _, c := range cases {
		if got := Within(c.root, filepath.Clean(c.path)); got != c.want {
			t.Errorf("Within(%q, %q) = %v, want %v", c.root, c.path, got, c.want)
		}
	}
}

func setup(t *testing.T) (root, outside string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "Movies")
	outside = filepath.Join(base, "secret")
	for _, d := range []string{filepath.Join(root, "Film (2001)"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p string) {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "Film (2001)", "film.mkv"))
	write(filepath.Join(outside, "passwd"))
	return root, outside
}

func TestResolveUnderRejectsEscapes(t *testing.T) {
	root, outside := setup(t)
	// A symlink inside the library pointing out of it, file and directory forms.
	if err := os.Symlink(filepath.Join(outside, "passwd"), filepath.Join(root, "link.mkv")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"../secret/passwd",
		filepath.Join(root, "..", "secret", "passwd"),
		filepath.Join(outside, "passwd"),
		"link.mkv",
		"linkdir/passwd",
		filepath.Join(root, "Film (2001)", "..", "..", "secret", "passwd"),
	} {
		if _, err := ResolveUnder(root, target); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("ResolveUnder(%q) err = %v, want ErrOutsideRoot", target, err)
		}
		if f, _, err := OpenUnder(root, target); err == nil {
			_ = f.Close()
			t.Errorf("OpenUnder(%q) opened a file outside the root", target)
		}
	}
}

func TestResolveUnderAllowsInside(t *testing.T) {
	root, _ := setup(t)
	if err := os.Symlink(filepath.Join(root, "Film (2001)", "film.mkv"), filepath.Join(root, "alias.mkv")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"Film (2001)/film.mkv", filepath.Join(root, "Film (2001)", "film.mkv"), "alias.mkv"} {
		p, err := ResolveUnder(root, target)
		if err != nil {
			t.Fatalf("ResolveUnder(%q): %v", target, err)
		}
		if filepath.Base(p) != "film.mkv" {
			t.Errorf("ResolveUnder(%q) = %q", target, p)
		}
	}
}

func TestResolveUnderSymlinkedRoot(t *testing.T) {
	root, _ := setup(t)
	alias := filepath.Join(filepath.Dir(root), "MoviesAlias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveUnder(alias, filepath.Join(alias, "Film (2001)", "film.mkv")); err != nil {
		t.Fatalf("file under a symlinked root: %v", err)
	}
}

func TestOpenUnderRefusesDirectories(t *testing.T) {
	root, _ := setup(t)
	if f, _, err := OpenUnder(root, "Film (2001)"); err == nil {
		_ = f.Close()
		t.Fatal("opened a directory")
	}
}
