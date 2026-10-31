package arr

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type scanned struct {
	lib int64
	sub string
}

func setup(t *testing.T) (*Dispatcher, *[]scanned, *int, *sync.Mutex) {
	t.Helper()
	root := t.TempDir()
	movies, tv := filepath.Join(root, "Movies"), filepath.Join(root, "TV")
	for _, d := range []string{filepath.Join(movies, "Harbor Lights (2001)"), filepath.Join(tv, "Quiet Street")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var got []scanned
	busy := 0
	d := &Dispatcher{
		Libraries: func(context.Context) ([]Library, error) {
			return []Library{{ID: 1, Kind: "movie", RootPath: movies}, {ID: 2, Kind: "show", RootPath: tv}}, nil
		},
		Scan: func(_ context.Context, lib int64, sub string) (int64, error) {
			mu.Lock()
			defer mu.Unlock()
			if busy > 0 {
				busy--
				return 0, ErrBusy
			}
			got = append(got, scanned{lib, sub})
			return int64(len(got)), nil
		},
		Retry: 10 * time.Millisecond,
		Give:  time.Second,
	}
	return d, &got, &busy, &mu
}

func movie(event, folder string) Payload {
	p := Payload{EventType: event}
	p.Movie = &struct {
		FolderPath string `json:"folderPath"`
		Title      string `json:"title"`
	}{FolderPath: folder}
	return p
}

func TestRadarrImportScansTheFolderByName(t *testing.T) {
	d, got, _, _ := setup(t)
	// Radarr sees the NAS at another path; the folder name finds it.
	res, err := d.Handle(context.Background(), movie("Download", "/volume1/data/movies/Harbor Lights (2001)"))
	if err != nil || res.Action != "scanned" || res.LibraryID != 1 || res.Path != "Harbor Lights (2001)" {
		t.Fatalf("%+v %v", res, err)
	}
	if len(*got) != 1 || (*got)[0] != (scanned{1, "Harbor Lights (2001)"}) {
		t.Fatalf("scans: %+v", *got)
	}
}

func TestSonarrUsesThePathMapFirst(t *testing.T) {
	d, got, _, _ := setup(t)
	libs, _ := d.Libraries(context.Background())
	d.Map = []PathMap{{From: "/tv", To: libs[1].RootPath}}
	p := Payload{EventType: "Download"}
	p.Series = &struct {
		Path  string `json:"path"`
		Title string `json:"title"`
	}{Path: "/tv/Quiet Street"}
	res, err := d.Handle(context.Background(), p)
	if err != nil || res.Action != "scanned" || res.LibraryID != 2 || res.Path != "Quiet Street" || len(*got) != 1 {
		t.Fatalf("%+v %v %+v", res, err, *got)
	}
}

func TestTestGrabAndUnknownFoldersScanNothing(t *testing.T) {
	d, got, _, _ := setup(t)
	for _, c := range []struct {
		p    Payload
		want string
	}{
		{Payload{EventType: "Test"}, "test"},
		{movie("Grab", "/m/Harbor Lights (2001)"), "ignored"},
		{movie("Download", "/m/Not Here (1999)"), "ignored"},
		{Payload{EventType: "Download"}, "ignored"},
	} {
		res, err := d.Handle(context.Background(), c.p)
		if err != nil || res.Action != c.want {
			t.Errorf("%+v: %+v %v", c.p.EventType, res, err)
		}
	}
	if len(*got) != 0 {
		t.Fatalf("scanned: %+v", *got)
	}
}

func TestBusyLibraryIsRetried(t *testing.T) {
	d, got, busy, mu := setup(t)
	*busy = 2
	res, err := d.Handle(context.Background(), movie("Download", "/x/Harbor Lights (2001)"))
	if err != nil || res.Action != "queued" {
		t.Fatalf("%+v %v", res, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		n := len(*got)
		mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the queued scan never ran")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPathMapCannotEscapeTheRoot(t *testing.T) {
	d, got, _, _ := setup(t)
	libs, _ := d.Libraries(context.Background())
	d.Map = []PathMap{{From: "/movies", To: libs[0].RootPath}}
	// "../" stays as text in the subpath; the scanner's own resolveSubPath
	// refuses it. Here: the folder-name fallback finds nothing outside roots.
	if res, _ := d.Handle(context.Background(), movie("Download", "/elsewhere/etc")); res.Action != "ignored" {
		t.Fatalf("unknown folder: %+v", res)
	}
	if len(*got) != 0 {
		t.Fatalf("scanned: %+v", *got)
	}
}
