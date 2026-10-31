//go:build !vcrxdev

package main

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// The dev server's fixed Home date and its play seeding must not exist in a
// normal build: setting the variable there changes nothing.
func TestDevOverridesAreNotInNormalBuilds(t *testing.T) {
	t.Setenv("VCRX_DEV_HOME_DATE", "2026-10-31")
	if f := devHomeDate(slog.New(slog.NewTextHandler(io.Discard, nil))); f != nil {
		t.Fatalf("a normal build pinned Home's date to %v", f())
	}
	if ok, _ := devCommand("dev-seed", nil); ok {
		t.Fatal("a normal build has the dev-seed command")
	}
}

// The release image is built without the vcrxdev tag; this fails the day
// someone adds it to the Dockerfile.
func TestReleaseImageHasNoDevTag(t *testing.T) {
	b, err := os.ReadFile("../../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "vcrxdev") {
		t.Fatal("the Dockerfile builds with the vcrxdev tag, which turns on development-only overrides")
	}
	if !strings.Contains(string(b), "go build") {
		t.Fatal("the Dockerfile no longer has its go build line; update this test")
	}
}
