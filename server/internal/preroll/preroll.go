// Package preroll lists the clips in the admin's pre-roll folder (preroll_dir)
// and opens one by name. The folder is read on every call, so a clip added or
// removed shows up without a restart; it holds a handful of files.
package preroll

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
)

// clipExt are the containers a pre-roll may use.
var clipExt = map[string]bool{".mp4": true, ".m4v": true, ".mkv": true, ".mov": true, ".webm": true}

// Folder is the pre-roll folder. A zero Folder (no Dir) has no clips.
type Folder struct {
	Dir string
}

// Clips lists the clip file names, sorted. Hidden files, folders and other
// files are left out; a missing or unreadable folder has none.
func (f Folder) Clips() []string {
	if f.Dir == "" {
		return nil
	}
	entries, err := os.ReadDir(f.Dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !clipExt[strings.ToLower(filepath.Ext(name))] {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Pick is one clip at random, or "" when there are none.
func (f Folder) Pick() string {
	clips := f.Clips()
	if len(clips) == 0 {
		return ""
	}
	return clips[rand.IntN(len(clips))]
}

// Has reports whether name is one of the clips. Only a listed name is ever
// opened, so a path, "..", or a file of another kind is never served.
func (f Folder) Has(name string) bool {
	for _, c := range f.Clips() {
		if c == name {
			return true
		}
	}
	return false
}

// Open opens a listed clip inside the folder, refusing anything that resolves
// outside it (a symlink pointing elsewhere).
func (f Folder) Open(name string) (*os.File, os.FileInfo, error) {
	if !f.Has(name) {
		return nil, nil, os.ErrNotExist
	}
	return fsafe.OpenUnder(f.Dir, filepath.Join(f.Dir, name))
}

// Title is a clip's name without its extension, for display.
func Title(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}
