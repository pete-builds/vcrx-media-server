// Package fsafe keeps every file the server touches inside a configured root. All
// media access goes through ResolveUnder, which evaluates symlinks before checking
// containment, so neither "../" segments nor a symlink pointing out of the library
// can reach another file.
package fsafe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideRoot means a path resolved to somewhere outside its root.
var ErrOutsideRoot = errors.New("path escapes its root")

// afterResolve, when a test sets it, runs between OpenUnder's checks and its
// open, where a path on a share could change underneath it.
var afterResolve func()

// Within reports whether path is root or lies beneath it. Both must be clean and
// absolute; no filesystem access happens here.
func Within(root, path string) bool {
	if root == path {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// ResolveUnder returns the real path of target with symlinks evaluated, and fails
// unless it is inside the real path of root. target may be absolute or relative
// to root. The file must exist.
func ResolveUnder(root, target string) (string, error) {
	if root == "" || !filepath.IsAbs(root) {
		return "", fmt.Errorf("root %q must be absolute", root)
	}
	realRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target = filepath.Clean(target)
	// A lexical check first, so obviously hostile input never reaches the filesystem.
	if !Within(filepath.Clean(root), target) && !Within(realRoot, target) {
		return "", ErrOutsideRoot
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	if !Within(realRoot, realTarget) {
		return "", ErrOutsideRoot
	}
	return realTarget, nil
}

// OpenUnder opens target read-only after ResolveUnder succeeds. It refuses
// anything that is not a regular file.
func OpenUnder(root, target string) (*os.File, os.FileInfo, error) {
	p, err := ResolveUnder(root, target)
	if err != nil {
		return nil, nil, err
	}
	// Checked before opening too: opening a FIFO for reading blocks until a
	// writer appears, so a FIFO named like a sidecar would hang the caller.
	if fi, err := os.Stat(p); err != nil {
		return nil, nil, err
	} else if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s: not a regular file", p)
	}
	if afterResolve != nil {
		afterResolve()
	}
	// Open through an os.Root on the real root: if a folder on the way was
	// swapped for a symlink out of the library after the checks above (anyone
	// who can write to the share could try), the open fails instead of
	// following it.
	realRoot, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return nil, nil, err
	}
	rel, err := filepath.Rel(realRoot, p)
	if err != nil {
		return nil, nil, err
	}
	r, err := os.OpenRoot(realRoot)
	if err != nil {
		return nil, nil, err
	}
	f, err := r.Open(rel)
	_ = r.Close() // the opened file stays valid
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, fmt.Errorf("%s: not a regular file", p)
	}
	return f, fi, nil
}
