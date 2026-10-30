package library

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/probe"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestManagerSerializesScans(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	e.pr.gate = make(chan struct{})
	m := NewManager(e.sc, quiet)
	ctx, cancel := context.WithCancel(context.Background())
	id, err := m.Start(ctx, e.lib.ID, Options{Trigger: "manual"})
	if err != nil || id == 0 {
		t.Fatalf("Start = %d, %v", id, err)
	}
	// The request context ending must not stop the scan.
	cancel()
	if !m.Running(e.lib.ID) {
		t.Fatal("not running")
	}
	if _, err := m.Start(context.Background(), e.lib.ID, Options{}); !errors.Is(err, ErrScanRunning) {
		t.Fatalf("second Start = %v, want ErrScanRunning", err)
	}
	if _, err := m.RunNow(context.Background(), e.lib.ID, Options{}); !errors.Is(err, ErrScanRunning) {
		t.Fatalf("RunNow during a scan = %v", err)
	}
	// Another process (a fresh manager, as the CLI is) is refused by the run row.
	if _, err := NewManager(e.sc, quiet).RunNow(context.Background(), e.lib.ID, Options{}); !errors.Is(err, ErrScanRunning) {
		t.Fatalf("second process = %v, want ErrScanRunning", err)
	}
	close(e.pr.gate)
	waitFor(t, "the scan to finish", func() bool { return !m.Running(e.lib.ID) })
	if got := e.str(`SELECT status FROM scan_runs WHERE id = ?`, id); got != "ok" {
		t.Fatalf("run %d status = %q", id, got)
	}
	run, err := m.RunNow(context.Background(), e.lib.ID, Options{})
	if err != nil || run.Trigger != "cli" || run.Status != StatusOK {
		t.Fatalf("RunNow = %+v, %v", run, err)
	}
	if _, err := m.Start(context.Background(), 999, Options{}); !errors.Is(err, ErrNoLibrary) {
		t.Fatalf("unknown library = %v", err)
	}
	if _, err := m.Start(context.Background(), e.lib.ID, Options{Trigger: "bogus"}); err == nil || m.Running(e.lib.ID) {
		t.Fatalf("bad trigger = %v", err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestManagerShutdownAbortsScan(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	e.pr.gate = make(chan struct{})
	m := NewManager(e.sc, quiet)
	id, err := m.Start(context.Background(), e.lib.ID, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.str(`SELECT status FROM scan_runs WHERE id = ?`, id); got != "aborted" {
		t.Fatalf("status = %q, want aborted", got)
	}
	// A cancelled scan never marks anything missing.
	if n := e.count(`SELECT COUNT(*) FROM media_versions WHERE missing_since IS NOT NULL`); n != 0 {
		t.Fatalf("%d versions marked missing by a cancelled scan", n)
	}
}

func TestManagerSchedule(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	other := addLibrary(t, e.db, "Off", KindMovie, e.root+"-none", "folder")
	if _, err := e.db.Exec(`UPDATE libraries SET scan_interval_minutes = 0 WHERE id = ?`, other.ID); err != nil {
		t.Fatal(err)
	}
	m := NewManager(e.sc, quiet)
	m.InitialDelay, m.Stagger, m.Tick = 0, 0, 20*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		m.Schedule(ctx)
		close(done)
	}()
	waitFor(t, "the startup scan", func() bool {
		return e.count(`SELECT COUNT(*) FROM scan_runs WHERE library_id = ? AND status = 'ok'`, e.lib.ID) == 1
	})
	cancel()
	<-done
	if got := e.str(`SELECT trigger FROM scan_runs WHERE library_id = ?`, e.lib.ID); got != "startup" {
		t.Fatalf("trigger = %q", got)
	}
	// The next pass is an hour away, and interval 0 means never.
	if n := e.count(`SELECT COUNT(*) FROM scan_runs`); n != 1 {
		t.Fatalf("%d runs, want 1", n)
	}
}

func TestStaleRunDoesNotBlock(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	old := e.clock.now().Add(-2 * staleRunAfter).UnixMilli()
	if _, err := e.db.Exec(`INSERT INTO scan_runs (library_id, trigger, status, started_at) VALUES (?, 'manual', 'running', ?)`, e.lib.ID, old); err != nil {
		t.Fatal(err)
	}
	e.scan(Options{})
}

func TestCancelledScanChangesNothingMissing(t *testing.T) {
	e := newEnv(t, KindMovie, "folder", movieTree)
	e.scan(Options{})
	e.remove("His Girl Friday (1940)")
	e.write(map[string]string{"Nosferatu (1922)/Nosferatu (1922).mkv": ""})
	e.pr.gate = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Run)
	go func() {
		run, _ := e.sc.Scan(ctx, e.lib, Options{})
		done <- run
	}()
	waitFor(t, "the run row", func() bool { return e.count(`SELECT COUNT(*) FROM scan_runs WHERE status = 'running'`) == 1 })
	cancel()
	run := <-done
	if run.Status != StatusAborted {
		t.Fatalf("run = %+v", run)
	}
	if n := e.count(`SELECT COUNT(*) FROM media_versions WHERE missing_since IS NOT NULL`); n != 0 {
		t.Fatal("a cancelled scan marked files missing")
	}
}

func TestUnreadableFolderKeepsItsFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores folder permissions")
	}
	e := newEnv(t, KindMovie, "folder", movieTree)
	e.scan(Options{})
	dir := e.path("Nosferatu.1922.2160p.UHD.BluRay.x265-SAMPLEGRP")
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	run := e.scan(Options{})
	if run.Missing != 0 || !strings.Contains(run.Error, "could not be read") {
		t.Fatalf("run = %+v", run)
	}
}

// TestNoWritesInSource is the standing guard for "never write to source
// media": the scanner packages may not call anything that creates, changes or
// removes a file.
func TestNoWritesInSource(t *testing.T) {
	forbidden := map[string]bool{
		"Create": true, "OpenFile": true, "WriteFile": true, "Remove": true, "RemoveAll": true, "Rename": true,
		"Mkdir": true, "MkdirAll": true, "MkdirTemp": true, "CreateTemp": true, "Chmod": true, "Chown": true, "Lchown": true,
		"Chtimes": true, "Truncate": true, "Symlink": true, "Link": true,
	}
	for _, dir := range []string{".", "../probe", "../metadata"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		checked := 0
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			checked++
			file, err := parser.ParseFile(gotoken.NewFileSet(), f, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && (pkg.Name == "os" || pkg.Name == "ioutil") && forbidden[sel.Sel.Name] {
					t.Errorf("%s calls %s.%s; the scanner must only read media", f, pkg.Name, sel.Sel.Name)
				}
				return true
			})
		}
		if checked == 0 {
			t.Fatalf("no sources found in %s", dir)
		}
	}
}

// TestRealFFprobe scans tiny files made by ffmpeg with the real prober.
func TestRealFFprobe(t *testing.T) {
	ffmpeg, err1 := exec.LookPath("ffmpeg")
	_, err2 := exec.LookPath("ffprobe")
	if err1 != nil || err2 != nil {
		t.Skip("ffmpeg or ffprobe not on PATH; the fake prober covers the logic")
	}
	e := newEnv(t, KindMovie, "title_year", map[string]string{
		"Nosferatu (1922)/Nosferatu (1922).en.srt": "1\n00:00:00,000 --> 00:00:01,000\nHello.\n",
	})
	makeClip := func(rel, size, extra string) {
		t.Helper()
		p := e.path(rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=" + size + ":rate=24:duration=0.2" + extra,
			"-f", "lavfi", "-i", "sine=duration=0.2", "-c:v", "mpeg4", "-c:a", "ac3", "-metadata:s:a:0", "language=eng", p}
		if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v\n%s", err, out)
		}
	}
	makeClip("Nosferatu (1922)/Nosferatu (1922).mkv", "1920x800", "")
	makeClip("Nosferatu.1922.2160p.HDR-VCRXTEST/nosferatu.2160p.mp4", "3840x1600", ",setparams=color_trc=smpte2084:color_primaries=bt2020:colorspace=bt2020nc")
	e.write(map[string]string{"Broken (2000)/Broken (2000).mkv": "this is not a video"})
	e.sc.Prober = &probe.FFprobe{Timeout: 30 * time.Second}
	run := e.scan(Options{})
	if run.Added != 3 || run.Failed != 1 {
		t.Fatalf("run = %+v", run)
	}
	got := e.strs(`SELECT i.title || ' ' || v.container || ' ' || v.resolution || ' ' || IFNULL(v.hdr_type, 'sdr') || ' ' || v.label
		FROM media_versions v JOIN items i ON i.id = v.item_id WHERE v.probe_status = 'ok' ORDER BY v.width`)
	want := []string{"Nosferatu mkv 1080p sdr 1080p", "Nosferatu mp4 4K hdr10 4K HDR"}
	if !slices.Equal(got, want) {
		t.Fatalf("versions = %q, want %q", got, want)
	}
	streams := e.strs(`SELECT s.kind || ':' || s.codec || ':' || IFNULL(s.language, '') || ':' || s.is_external FROM media_streams s
		JOIN media_versions v ON v.id = s.version_id WHERE v.rel_path = 'Nosferatu (1922)/Nosferatu (1922).mkv' ORDER BY s.is_external, s.stream_index`)
	if !slices.Equal(streams, []string{"video:mpeg4::0", "audio:ac3:eng:0", "subtitle:subrip:eng:1"}) {
		t.Fatalf("streams = %q", streams)
	}
	if got := e.str(`SELECT probe_status FROM media_versions WHERE rel_path LIKE 'Broken%'`); got != "error" {
		t.Fatalf("broken file status = %q", got)
	}
}
