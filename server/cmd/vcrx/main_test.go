package main

import (
	"os"
	"strings"
	"testing"
)

func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old; _ = r.Close() })
}

// Run over ssh without -t, the CLI sees an empty stdin. It must say so instead of
// reporting a too-short password (seen on 2026-10-30).
func TestReadPasswordEmptyStdin(t *testing.T) {
	withStdin(t, "")
	if _, err := readPassword("Password: "); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadPasswordPiped(t *testing.T) {
	withStdin(t, "correct horse\r\n")
	pw, err := readPassword("Password: ")
	if err != nil || pw != "correct horse" {
		t.Fatalf("got %q, %v", pw, err)
	}
}
