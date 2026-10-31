package library

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A subtitle downloaded from OpenSubtitles is an external track that is not
// beside the media. Scans rewrite the sidecar rows whenever the files beside
// a video change; they must leave the downloaded one alone.
func TestScanKeepsDownloadedSubtitles(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", map[string]string{
		"Lantern Bay (2004)/Lantern Bay (2004).mkv":    "",
		"Lantern Bay (2004)/Lantern Bay (2004).en.srt": "",
	})
	e.scan(Options{})
	ver := int64(e.count(`SELECT id FROM media_versions`))
	res, err := e.db.Exec(`INSERT INTO downloaded_subtitles (version_id, provider, provider_file_id, language, rel_path, created_at)
		VALUES (?, 'opensubtitles', 8000001, 'fre', '1/1.fre.srt', ?)`, ver, time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	did, _ := res.LastInsertId()
	if _, err := e.db.Exec(`INSERT INTO media_streams (version_id, kind, codec, language, is_external, external_path, downloaded_id)
		VALUES (?, 'subtitle', 'subrip', 'fre', 1, '1/1.fre.srt', ?)`, ver, did); err != nil {
		t.Fatal(err)
	}
	downloaded := func() int {
		return e.count(`SELECT COUNT(*) FROM media_streams WHERE downloaded_id IS NOT NULL`)
	}

	// A sidecar added, then one removed: both rewrite the sidecar rows.
	e.write(map[string]string{"Lantern Bay (2004)/Lantern Bay (2004).de.srt": ""})
	e.scan(Options{})
	if n := e.count(`SELECT COUNT(*) FROM media_streams WHERE is_external = 1 AND downloaded_id IS NULL`); n != 2 || downloaded() != 1 {
		t.Fatalf("after adding a sidecar: %d sidecars, %d downloaded", n, downloaded())
	}
	if err := os.Remove(filepath.Join(e.root, "Lantern Bay (2004)", "Lantern Bay (2004).en.srt")); err != nil {
		t.Fatal(err)
	}
	e.scan(Options{})
	if n := e.count(`SELECT COUNT(*) FROM media_streams WHERE is_external = 1 AND downloaded_id IS NULL`); n != 1 || downloaded() != 1 {
		t.Fatalf("after removing a sidecar: %d sidecars, %d downloaded", n, downloaded())
	}
	// And a rescan with nothing changed does not see it as a change.
	if run := e.scan(Options{}); run.Changed != 0 {
		t.Fatalf("an unchanged folder counted as changed: %+v", run)
	}
}

func TestLanguageCodes(t *testing.T) {
	for in, want := range map[string][2]string{
		"en": {"eng", "en"}, "eng": {"eng", "en"}, "English": {"eng", "en"}, "pt-BR": {"por", "pt"},
		"ger": {"ger", "de"}, "deu": {"ger", "de"}, "nor": {"nor", "no"}, "heb": {"heb", "he"}, "hin": {"hin", "hi"},
		"xx": {"", ""}, "": {"", ""},
	} {
		if got := LanguageCode(in); got != want[0] {
			t.Errorf("LanguageCode(%q) = %q, want %q", in, got, want[0])
		}
		if got := ISO6391(in); got != want[1] {
			t.Errorf("ISO6391(%q) = %q, want %q", in, got, want[1])
		}
	}
}
