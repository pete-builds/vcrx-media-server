package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Smart collections live beside vcrx.yaml unless the file or env moves them.
func TestSmartCollectionsDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("VCRX_CONFIG_DIR", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.SmartCollectionsDir(); got != filepath.Join(dir, "collections") {
		t.Fatalf("default: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "vcrx.yaml"), []byte("collections_dir: /srv/vcrx-collections\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err = Load(); err != nil || c.SmartCollectionsDir() != "/srv/vcrx-collections" {
		t.Fatalf("from file: %q %v", c.SmartCollectionsDir(), err)
	}
	t.Setenv("VCRX_COLLECTIONS_DIR", "/data/collections")
	if c, err = Load(); err != nil || c.SmartCollectionsDir() != "/data/collections" {
		t.Fatalf("env: %q %v", c.SmartCollectionsDir(), err)
	}
}
