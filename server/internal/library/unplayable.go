package library

import (
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Reasons a title is on disk but cannot be played, matching the reason field
// of scan_runs.unplayable. A real library is full of these: in one measured on
// 2026-11-02, about one movie in eight was a disc image, a disc folder, a RAR
// set or a stalled download, and every scan reported "0 failed".
// Leaving them out is right; leaving them out in silence is not.
const (
	UnplayableDiscImage  = "disc_image"  // .iso or .img
	UnplayableDiscFolder = "disc_folder" // a folder holding BDMV or VIDEO_TS
	UnplayableArchive    = "archive"     // RAR or 7z volumes and no video beside them
	UnplayableIncomplete = "incomplete"  // a .part download or an _UNPACK_/_FAILED_ job folder
)

// Unplayable is one title the walk found but cannot index. Path is relative to
// the library root: the file for an image or a partial download, the folder
// for the rest.
type Unplayable struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// unplayableMax caps the list a run keeps; the count is never capped.
const unplayableMax = 500

// unplayableMinSize keeps stray small files out of the report: a disc image or
// a stalled feature download is hundreds of megabytes at least.
const unplayableMinSize = 100 << 20

// archiveRe matches the volumes of a RAR or 7z set in every naming a download
// client leaves: x.rar, x.part01.rar, x.r00, x.001, x.7z, and the x.rar.2 a
// client writes when the same job runs twice.
var archiveRe = regexp.MustCompile(`(?i)\.(rar|7z|r\d{2}|\d{3})(\.\d+)?$`)

// UnplayableFile reports why a file holds a title that cannot be played, or ""
// when it is not one. Archive volumes answer UnplayableArchive whatever their
// size; the walk reports them per folder, and only where no video sits beside
// them, since a finished unpack often leaves its archives behind.
func UnplayableFile(name string, size int64) string {
	if strings.HasPrefix(name, ".") {
		return ""
	}
	l := strings.ToLower(name)
	if archiveRe.MatchString(l) {
		return UnplayableArchive
	}
	if size < unplayableMinSize {
		return ""
	}
	switch path.Ext(l) {
	case ".iso", ".img":
		return UnplayableDiscImage
	case ".part", ".!qb", ".crdownload":
		return UnplayableIncomplete
	}
	return ""
}

// discDir reports whether a folder is a Blu-ray or DVD structure, which plays
// only as a disc.
func discDir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bdmv", "video_ts":
		return true
	}
	return false
}

// addUnplayable notes a file holding a title that cannot be played, and
// reports whether it is an archive volume, which the walk settles per folder.
// Only names that could qualify are statted, so a folder of NFOs and posters
// on a NAS costs no extra round trips.
func (w *walker) addUnplayable(rel string) (archive bool) {
	name := path.Base(rel)
	if r := UnplayableFile(name, 0); r == UnplayableArchive {
		return true
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".iso", ".img", ".part", ".!qb", ".crdownload":
	default:
		return false
	}
	fi := w.statFile(rel)
	if fi == nil {
		return false
	}
	if r := UnplayableFile(name, fi.Size()); r != "" {
		w.noteUnplayable(rel, r)
	}
	return false
}

// noteArchives reports the folders holding archive volumes and no video,
// unless a video was found below one: an unpack into a subfolder leaves its
// archives behind, and that title plays.
func (w *walker) noteArchives(dirs []string) {
	for _, d := range dirs {
		played := false
		for _, v := range w.videos {
			if d == "" || strings.HasPrefix(v.rel, d+"/") {
				played = true
				break
			}
		}
		if !played {
			w.noteUnplayable(d, UnplayableArchive)
		}
	}
}

// noteUnplayable records one title once per path.
func (w *walker) noteUnplayable(rel, reason string) {
	if w.unplayableSeen == nil {
		w.unplayableSeen = map[string]bool{}
	}
	if rel == "" {
		rel = "."
	}
	if w.unplayableSeen[rel] {
		return
	}
	w.unplayableSeen[rel] = true
	w.unplayable = append(w.unplayable, Unplayable{Path: rel, Reason: reason})
}

// unplayableJSON renders a run's list for scan_runs.unplayable, sorted by path
// and capped at unplayableMax.
func unplayableJSON(list []Unplayable) string {
	out := slices.Clone(list)
	slices.SortFunc(out, func(a, b Unplayable) int { return strings.Compare(a.Path, b.Path) })
	if len(out) > unplayableMax {
		out = out[:unplayableMax]
	}
	if out == nil {
		out = []Unplayable{}
	}
	b, _ := json.Marshal(out)
	return string(b)
}
