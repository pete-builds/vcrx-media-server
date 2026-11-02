package federation

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
)

// A token shaped as a peer issues one, distinctive enough to search for.
const testToken = "PeerTokenAbcdefghijklmnopqrstuvwxyz_0123456789-XYZ"

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := db.Migrate(context.Background(), conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	return &Store{DB: conn, Key: &TokenKey{Path: filepath.Join(dir, TokenKeyFile)}}, dir
}

func addTestPeer(t *testing.T, s *Store) Peer {
	t.Helper()
	p, err := s.Add(context.Background(), Peer{Name: "Mark's server", BaseURL: "http://192.168.1.20:3750", Token: testToken, ExpiresAt: 1 << 50})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func storedToken(t *testing.T, s *Store, id int64) string {
	t.Helper()
	var tok string
	if err := s.DB.QueryRow(`SELECT token FROM peers WHERE id = ?`, id).Scan(&tok); err != nil {
		t.Fatal(err)
	}
	return tok
}

// The database holds only the sealed token; reading it back gives the token.
func TestPeerTokenIsSealedAtRest(t *testing.T) {
	s, dir := newTestStore(t)
	p := addTestPeer(t, s)
	stored := storedToken(t, s, p.ID)
	if stored == testToken || strings.Contains(stored, testToken) || !IsSealed(stored) {
		t.Fatalf("stored token is not sealed: %q", stored)
	}
	if p.Token != testToken {
		t.Fatalf("Add returned token %q", p.Token)
	}
	got, err := s.Get(context.Background(), p.ID)
	if err != nil || got.Token != testToken || got.State == StateTokenUnreadable {
		t.Fatalf("Get: %+v %v", got, err)
	}
	list, err := s.List(context.Background())
	if err != nil || len(list) != 1 || list[0].Token != testToken {
		t.Fatalf("List: %+v %v", list, err)
	}
	// The key file was made on first need, readable by this user only, and
	// the key itself is nowhere in the database.
	fi, err := os.Stat(filepath.Join(dir, TokenKeyFile))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", fi, err)
	}
	key, _ := os.ReadFile(filepath.Join(dir, TokenKeyFile))
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(key)))
	if err != nil || len(raw) != 32 {
		t.Fatalf("key file content: %v", err)
	}
	dbFile, _ := os.ReadFile(filepath.Join(dir, "vcrx.db"))
	if bytes.Contains(dbFile, raw) || bytes.Contains(dbFile, bytes.TrimSpace(key)) {
		t.Fatal("the database file holds the key")
	}
	// Each seal is fresh, so two links with one token do not match.
	again, _ := s.Key.Seal(testToken, p.BaseURL)
	if again == stored {
		t.Fatal("sealing twice gave the same value")
	}
}

// A backup made by the server's own backup code carries no usable token,
// and the key file is never copied into the backup folder.
func TestBackupCarriesNoPeerToken(t *testing.T) {
	s, dir := newTestStore(t)
	addTestPeer(t, s)
	backups := filepath.Join(dir, "backups")
	path, err := db.Backup(context.Background(), s.DB, backups, "nightly")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(testToken)) {
		t.Fatal("the backup holds the peer token in the clear")
	}
	// Control: the same byte search finds a value the backup does hold.
	if !bytes.Contains(b, []byte("Mark's server")) {
		t.Fatal("control: the byte search cannot find the peer's name in the backup")
	}
	entries, _ := os.ReadDir(backups)
	for _, e := range entries {
		if e.Name() == TokenKeyFile || !strings.HasSuffix(e.Name(), ".db") {
			t.Fatalf("backup folder holds %s", e.Name())
		}
	}
}

// A token stored before sealing still works, is sealed by SealLegacy, and
// works after; nothing else about the row changes.
func TestLegacyPlaintextTokensAreSealed(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	res, err := s.DB.Exec(`INSERT INTO peers (name, base_url, token, expires_at, created_at, state) VALUES (?, ?, ?, ?, ?, ?)`,
		"Old link", "http://10.0.0.5:3750", testToken, int64(1)<<50, 1, StateOnline)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if p, err := s.Get(ctx, id); err != nil || p.Token != testToken {
		t.Fatalf("legacy row before sealing: %+v %v", p, err)
	}
	n, err := s.SealLegacy(ctx)
	if err != nil || n != 1 {
		t.Fatalf("SealLegacy: %d %v", n, err)
	}
	stored := storedToken(t, s, id)
	if !IsSealed(stored) || strings.Contains(stored, testToken) {
		t.Fatalf("legacy token not sealed: %q", stored)
	}
	p, err := s.Get(ctx, id)
	if err != nil || p.Token != testToken || p.Name != "Old link" || p.State != StateOnline {
		t.Fatalf("legacy row after sealing: %+v %v", p, err)
	}
	if n, err := s.SealLegacy(ctx); err != nil || n != 0 {
		t.Fatalf("SealLegacy again: %d %v", n, err)
	}
}

// Without the key, or with another key, sealed tokens are reported unusable,
// with a reason; the rows are kept unchanged and no new key appears.
func TestMissingOrWrongKeyKeepsRowsAndSaysWhy(t *testing.T) {
	s, dir := newTestStore(t)
	ctx := context.Background()
	p := addTestPeer(t, s)
	keyPath := filepath.Join(dir, TokenKeyFile)
	stored := storedToken(t, s, p.ID)
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}
	s.Key = &TokenKey{Path: keyPath}
	check := func(what string, want error) {
		t.Helper()
		list, err := s.List(ctx)
		if err != nil || len(list) != 1 {
			t.Fatalf("%s: List: %+v %v", what, list, err)
		}
		got := list[0]
		if got.Token != "" || got.State != StateTokenUnreadable || !errors.Is(got.TokenErr, want) || got.LastError == nil ||
			!strings.Contains(*got.LastError, TokenKeyFile) {
			t.Fatalf("%s: %+v", what, got)
		}
		if one, err := s.Get(ctx, p.ID); err != nil || one.State != StateTokenUnreadable {
			t.Fatalf("%s: Get: %+v %v", what, one, err)
		}
		if storedToken(t, s, p.ID) != stored {
			t.Fatalf("%s: the stored token changed", what)
		}
	}
	check("missing key", ErrKeyMissing)
	if n, err := s.SealLegacy(ctx); err != nil || n != 0 {
		t.Fatalf("SealLegacy without the key: %d %v", n, err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("reading made a new key file: %v", err)
	}

	other := make([]byte, 32)
	_, _ = rand.Read(other)
	if err := os.WriteFile(keyPath, []byte(base64.StdEncoding.EncodeToString(other)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Key = &TokenKey{Path: keyPath}
	check("wrong key", ErrTokenUnreadable)

	if err := os.WriteFile(keyPath, []byte("not a key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Key = &TokenKey{Path: keyPath}
	check("damaged key file", ErrKeyInvalid)
}

// A sealed token opens only for the address it was sealed with, so a row
// whose address was changed in the database does not send it elsewhere.
func TestSealedTokenIsBoundToItsAddress(t *testing.T) {
	s, _ := newTestStore(t)
	p := addTestPeer(t, s)
	if _, err := s.DB.Exec(`UPDATE peers SET base_url = 'http://203.0.113.9:3750' WHERE id = ?`, p.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(context.Background(), p.ID)
	if err != nil || got.Token != "" || !errors.Is(got.TokenErr, ErrTokenUnreadable) {
		t.Fatalf("moved row: %+v %v", got, err)
	}
}

// A symlinked key file is refused rather than followed.
func TestSymlinkedKeyFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "elsewhere")
	if err := os.WriteFile(target, []byte(base64.StdEncoding.EncodeToString(make([]byte, 32))), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, TokenKeyFile)
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks here")
	}
	if _, err := (&TokenKey{Path: link}).Seal(testToken, "http://192.168.1.20:3750"); !errors.Is(err, ErrKeyInvalid) {
		t.Fatalf("symlinked key: %v", err)
	}
}
