package markers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChapterTitles(t *testing.T) {
	t.Parallel()
	got := fromChapters([]Chapter{
		{"Chapter 1", 0, 60_000},
		{" Opening ", 60_000, 150_000},
		{"Intro", 150_000, 200_000}, // a second intro is ignored
		{"End Credits", 1_300_000, 1_380_000},
		{"Credits", 1_000, 500}, // backwards
	})
	if m := got[Intro]; m.StartMs != 60_000 || m.EndMs != 150_000 || m.Source != SourceChapter {
		t.Errorf("intro %+v", m)
	}
	if m := got[Credits]; m.StartMs != 1_300_000 || m.EndMs != 1_380_000 {
		t.Errorf("credits %+v", m)
	}
	if len(fromChapters([]Chapter{{"Chapter 2", 0, 9}, {"Recap", 0, 60_000}})) != 0 {
		t.Error("plain chapter names read as markers")
	}
}

func TestNFOMarkers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	media := filepath.Join(dir, "Show - S01E01.mkv")
	if m, err := fromNFO(dir, media); err != nil || len(m) != 0 {
		t.Fatalf("no NFO: %v %v", m, err)
	}
	nfo := `<?xml version="1.0"?><episodedetails><title>Pilot</title>
		<Marker type="intro" startTimeOffset="12000" endTimeOffset="62000"/>
		<marker type="credits" starttimeoffset="1300000" endtimeoffset="1380000"></marker>
		<Marker type="commercial" startTimeOffset="1" endTimeOffset="2"/>
		<Marker type="intro" startTimeOffset="900000" endTimeOffset="800000"/>
	</episodedetails>`
	if err := os.WriteFile(filepath.Join(dir, "Show - S01E01.nfo"), []byte(nfo), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := fromNFO(dir, media)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 || m[Intro].StartMs != 12_000 || m[Intro].EndMs != 62_000 || m[Credits].StartMs != 1_300_000 || m[Intro].Source != SourceNFO {
		t.Errorf("markers %+v", m)
	}
}
