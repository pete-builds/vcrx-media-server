// Package arr turns Radarr and Sonarr webhooks into targeted scans, so an
// import shows up in VCRX within seconds instead of at the next scheduled scan.
// It reads the payloads both send for their Webhook connection unchanged.
package arr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Payload is the part of a Radarr or Sonarr webhook this reads.
type Payload struct {
	EventType string `json:"eventType"`
	Movie     *struct {
		FolderPath string `json:"folderPath"`
		Title      string `json:"title"`
	} `json:"movie"`
	Series *struct {
		Path  string `json:"path"`
		Title string `json:"title"`
	} `json:"series"`
}

// PathMap rewrites the start of a path as Radarr or Sonarr sees it into the
// same place as VCRX sees it, for when the two mount the NAS differently.
type PathMap struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// Library is what the dispatcher needs to know about a library.
type Library struct {
	ID       int64
	Kind     string // movie or show
	RootPath string
}

// ErrBusy means the library is being scanned now; the caller retries later.
var ErrBusy = errors.New("library busy")

type Dispatcher struct {
	Libraries func(ctx context.Context) ([]Library, error)
	// Scan starts a scan of one folder under a library root; it returns ErrBusy
	// while that library is already being scanned.
	Scan  func(ctx context.Context, libraryID int64, subPath string) (int64, error)
	Map   []PathMap
	Log   *slog.Logger
	Retry time.Duration // between attempts on a busy library; zero is 20 s
	Give  time.Duration // how long to keep trying; zero is 15 min
}

// Result says what a webhook led to.
type Result struct {
	Action    string `json:"action"` // scanned, queued, ignored, test
	LibraryID int64  `json:"library_id,omitempty"`
	Path      string `json:"path,omitempty"`
	ScanID    int64  `json:"scan_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// scanEvents change files on disk; everything else (grabs, health, tests of
// other kinds) leaves the library as it is.
var scanEvents = map[string]bool{
	"Download": true, "Rename": true, "MovieDelete": true, "MovieFileDelete": true,
	"SeriesDelete": true, "EpisodeFileDelete": true, "SeriesAdd": false, "MovieAdded": false,
}

// Handle finds the library folder a webhook is about and scans it.
func (d *Dispatcher) Handle(ctx context.Context, p Payload) (Result, error) {
	if p.EventType == "Test" {
		return Result{Action: "test"}, nil
	}
	if !scanEvents[p.EventType] {
		return Result{Action: "ignored", Reason: "event " + p.EventType + " changes no files"}, nil
	}
	kind, folder := "", ""
	switch {
	case p.Movie != nil && p.Movie.FolderPath != "":
		kind, folder = "movie", p.Movie.FolderPath
	case p.Series != nil && p.Series.Path != "":
		kind, folder = "show", p.Series.Path
	default:
		return Result{Action: "ignored", Reason: "no movie or series folder in the payload"}, nil
	}
	libs, err := d.Libraries(ctx)
	if err != nil {
		return Result{}, err
	}
	deleted := strings.HasSuffix(p.EventType, "Delete")
	lib, sub, ok := d.locate(libs, kind, folder)
	if !ok && deleted {
		// A deleted folder cannot be found by its name any more; with one
		// library of the kind there is no doubt which it was in.
		lib, ok = onlyLibrary(libs, kind)
	}
	if !ok {
		return Result{Action: "ignored", Reason: fmt.Sprintf("no %s library holds %q", kind, path.Base(filepath.ToSlash(folder)))}, nil
	}
	if deleted {
		// A targeted scan needs its folder to exist and to still hold a video,
		// and a delete can take away either. Scanning the parent (for a movie
		// or a series, the library root) marks what went missing, and a full
		// scan is still guarded by the half rule.
		sub = parentOf(sub)
	}
	res := Result{LibraryID: lib.ID, Path: sub}
	id, err := d.Scan(ctx, lib.ID, sub)
	switch {
	case errors.Is(err, ErrBusy):
		res.Action = "queued"
		// The retry outlives the webhook request, so it keeps the request's
		// values but not its cancellation.
		go d.retry(context.WithoutCancel(ctx), lib.ID, sub)
		return res, nil
	case err != nil:
		return res, err
	}
	res.Action, res.ScanID = "scanned", id
	return res, nil
}

// locate maps the arr's folder to a library and a folder relative to its
// root: first through the configured path maps, then by the folder's name, a
// first-level folder of a library of the right kind.
func (d *Dispatcher) locate(libs []Library, kind, folder string) (Library, string, bool) {
	folder = filepath.ToSlash(strings.TrimRight(folder, `/\`))
	for _, m := range d.Map {
		from := strings.TrimRight(filepath.ToSlash(m.From), "/")
		if from == "" || (folder != from && !strings.HasPrefix(folder, from+"/")) {
			continue
		}
		mapped := strings.TrimRight(filepath.ToSlash(m.To), "/") + strings.TrimPrefix(folder, from)
		for _, l := range libs {
			root := strings.TrimRight(filepath.ToSlash(l.RootPath), "/")
			if l.Kind == kind && strings.HasPrefix(mapped, root+"/") {
				return l, strings.TrimPrefix(mapped, root+"/"), true
			}
		}
	}
	name := path.Base(folder)
	if name == "." || name == ".." || name == "/" || name == "" {
		return Library{}, "", false
	}
	for _, l := range libs {
		if l.Kind != kind {
			continue
		}
		if fi, err := os.Stat(filepath.Join(l.RootPath, name)); err == nil && fi.IsDir() {
			return l, name, true
		}
	}
	return Library{}, "", false
}

// onlyLibrary is the one library of kind, when there is exactly one.
func onlyLibrary(libs []Library, kind string) (Library, bool) {
	var found []Library
	for _, l := range libs {
		if l.Kind == kind {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		return Library{}, false
	}
	return found[0], true
}

// parentOf is the slash-separated parent of a library-relative path, or ""
// (the library root) for a top-level folder.
func parentOf(sub string) string {
	p := path.Dir(sub)
	if p == "." || p == "/" {
		return ""
	}
	return p
}

func (d *Dispatcher) retry(ctx context.Context, libraryID int64, sub string) {
	every, give := d.Retry, d.Give
	if every <= 0 {
		every = 20 * time.Second
	}
	if give <= 0 {
		give = 15 * time.Minute
	}
	deadline := time.Now().Add(give)
	for time.Now().Before(deadline) {
		time.Sleep(every)
		_, err := d.Scan(ctx, libraryID, sub)
		if !errors.Is(err, ErrBusy) {
			if err != nil && d.Log != nil {
				d.Log.Warn("webhook scan failed", "library", libraryID, "path", sub, "err", err)
			}
			return
		}
	}
	if d.Log != nil {
		d.Log.Warn("webhook scan gave up: library stayed busy; the scheduled scan will pick it up", "library", libraryID, "path", sub)
	}
}
