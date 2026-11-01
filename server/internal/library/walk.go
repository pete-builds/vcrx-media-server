package library

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
)

// videoFile is one video the walk found and kept.
type videoFile struct {
	rel   string // slash-separated, relative to the library root
	abs   string // root joined with rel, the path stored in media_versions
	size  int64
	mtime int64 // unix ms
}

// walker reads folders under one library root. It never follows a symlink
// that resolves outside the root, never descends into a symlinked folder (so
// loops and double counting are impossible), and only ever opens for reading.
type walker struct {
	root string
	kind string
	log  *slog.Logger

	dirs   map[string]*listing
	failed []string // rel folders that could not be read
	videos []*videoFile
	// extras are trailers and other bonus files: in extras folders, which the
	// walk does not descend into as titles, or named "-trailer" and the like.
	// They attach to an item after the titles are written (extras.go).
	extraDirs  []extraDir
	extraFiles []string // rel paths of files named as extras
}

// extraDir is an extras folder, such as Trailers, and the kind of its files.
type extraDir struct{ rel, kind string }

func newWalker(root, kind string, log *slog.Logger) *walker {
	return &walker{root: filepath.Clean(root), kind: kind, log: log, dirs: map[string]*listing{}}
}

func (w *walker) absOf(rel string) string {
	if rel == "" {
		return w.root
	}
	return filepath.Join(w.root, filepath.FromSlash(rel))
}

// read lists one folder, caching the result. Symlinks are kept only when they
// point at a regular file inside the root.
func (w *walker) read(rel string) (*listing, error) {
	if l, ok := w.dirs[rel]; ok {
		return l, nil
	}
	ents, err := os.ReadDir(w.absOf(rel))
	if err != nil {
		return nil, err
	}
	l := &listing{rel: rel}
	for _, e := range ents {
		name := e.Name()
		if e.Type()&fs.ModeSymlink != 0 {
			real, err := fsafe.ResolveUnder(w.root, w.absOf(joinRel(rel, name)))
			if err != nil {
				w.log.Debug("ignoring symlink", "path", joinRel(rel, name), "err", err)
				continue
			}
			fi, err := os.Stat(real)
			if err != nil || !fi.Mode().IsRegular() {
				continue
			}
			l.entries = append(l.entries, dirEntry{name: name})
			continue
		}
		if e.IsDir() {
			l.entries = append(l.entries, dirEntry{name: name, isDir: true})
		} else if e.Type().IsRegular() {
			l.entries = append(l.entries, dirEntry{name: name})
		}
	}
	w.dirs[rel] = l
	return l, nil
}

// listing returns a folder's entries, reading it on demand; nil when it cannot
// be read. Targeted scans use it for the show and season folders above the
// folder being scanned.
func (w *walker) listing(rel string) *listing {
	l, err := w.read(rel)
	if err != nil {
		return nil
	}
	return l
}

// walk descends from start, which must be readable. Unreadable folders below
// it are recorded in failed so their files are never marked missing.
func (w *walker) walk(ctx context.Context, start string) error {
	if _, err := w.read(start); err != nil {
		return err
	}
	stack := []string{start}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		l, err := w.read(rel)
		if err != nil {
			w.log.Warn("folder unreadable, its files will not be marked missing", "path", rel, "err", err)
			w.failed = append(w.failed, rel)
			continue
		}
		var subdirs []string
		for _, e := range l.entries {
			child := joinRel(rel, e.name)
			if e.isDir {
				switch skipDirAt(e.name, w.kind, rel != "") {
				case "":
					subdirs = append(subdirs, child)
				case "extras":
					w.extraDirs = append(w.extraDirs, extraDir{rel: child, kind: ExtrasDirKind(e.name, w.kind, rel != "")})
				}
				continue
			}
			w.addFile(child)
		}
		// Push in reverse so folders are visited in name order.
		for i := len(subdirs) - 1; i >= 0; i-- {
			stack = append(stack, subdirs[i])
		}
	}
	return nil
}

// addFile stats one candidate file and keeps it when it is a video worth
// indexing.
func (w *walker) addFile(rel string) {
	name := path.Base(rel)
	if !IsVideo(name) || strings.HasPrefix(name, ".") {
		return
	}
	fi := w.statFile(rel)
	if fi == nil {
		return
	}
	switch SkipFile(name, fi.Size()) {
	case "":
	case "extras":
		w.extraFiles = append(w.extraFiles, rel)
		return
	default:
		return
	}
	w.videos = append(w.videos, &videoFile{rel: rel, abs: w.absOf(rel), size: fi.Size(), mtime: fi.ModTime().UnixMilli()})
}

// statFile stats a file the walk listed and returns it when it is a regular
// file. One that no longer exists answers nil. Any other error (EIO, ESTALE,
// EACCES on a flaky share) says nothing about whether the file is still there,
// so it goes in failed, like an unreadable folder, and is never marked missing.
func (w *walker) statFile(rel string) os.FileInfo {
	fi, err := os.Stat(w.absOf(rel))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			w.log.Warn("file unreadable, it will not be marked missing", "path", rel, "err", err)
			w.failed = append(w.failed, rel)
		}
		return nil
	}
	if !fi.Mode().IsRegular() {
		return nil
	}
	return fi
}

// underFailed reports whether rel is, or lies in a folder, the walk could not read.
func (w *walker) underFailed(rel string) bool {
	for _, f := range w.failed {
		if f == "" || rel == f || strings.HasPrefix(rel, f+"/") {
			return true
		}
	}
	return false
}
