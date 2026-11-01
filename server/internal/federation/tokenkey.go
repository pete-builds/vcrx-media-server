package federation

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Peer tokens at rest. A friend's server token is the bearer this server
// presents, so unlike every other secret here it cannot be kept as a hash.
// It is sealed with AES-256-GCM under a key kept in its own file in the
// config folder, never in the database, so a copy of the database (the
// nightly backups among them) never carries a usable token. The sealed form
// is bound to the peer's address, so a sealed token moved onto another row
// does not open.

// TokenKeyFile is the key's file name in the config folder.
const TokenKeyFile = "peer-token.key"

// sealedPrefix marks a sealed token. A token as a peer issues it is
// [A-Za-z0-9_-] only, so a stored value with this prefix is never one.
const sealedPrefix = "sealed:v1:"

var (
	// ErrKeyMissing: tokens are sealed, but the key file is not there.
	ErrKeyMissing = errors.New("the peer token key file is missing")
	// ErrKeyInvalid: the key file is there but does not hold a key.
	ErrKeyInvalid = errors.New("the peer token key file does not hold a valid key")
	// ErrTokenUnreadable: the key file holds a key, but not the one the
	// token was sealed with (or the stored value is damaged).
	ErrTokenUnreadable = errors.New("the token cannot be decrypted with the peer token key")
)

// TokenKey seals and opens peer tokens with the key in Path. The key file is
// created, mode 0600, the first time a token has to be sealed; opening a
// token never creates it, so a lost key is reported rather than replaced.
type TokenKey struct {
	Path string

	mu   sync.Mutex
	aead cipher.AEAD
}

// IsSealed reports whether a stored token is in the sealed form.
func IsSealed(stored string) bool { return strings.HasPrefix(stored, sealedPrefix) }

func (k *TokenKey) load(create bool) (cipher.AEAD, error) {
	if k == nil || k.Path == "" {
		return nil, fmt.Errorf("%w: no key file is configured", ErrKeyMissing)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.aead != nil {
		return k.aead, nil
	}
	raw, err := readKey(k.Path)
	if errors.Is(err, fs.ErrNotExist) && create {
		raw, err = createKey(k.Path)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrKeyMissing, k.Path)
	}
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeyInvalid, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// Kept only once it loaded, so a key restored while the server runs is
	// picked up on the next request.
	k.aead = aead
	return aead, nil
}

// readKey reads a key file: base64 of 32 bytes. It refuses anything that is
// not a regular file, a symlink included.
func readKey(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrKeyInvalid, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, 1024))
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("%w: %s", ErrKeyInvalid, path)
	}
	return raw, nil
}

// createKey writes a new random key to path, mode 0600. If another process
// created it first, that key is the one used.
func createKey(path string) ([]byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return readKey(path)
	}
	if err != nil {
		return nil, err
	}
	_, err = f.WriteString(base64.StdEncoding.EncodeToString(raw) + "\n")
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("writing the peer token key: %w", err)
	}
	return raw, nil
}

// Seal returns token in its sealed form, bound to the peer's address. It
// creates the key file if there is none yet.
func (k *TokenKey) Seal(token, baseURL string) (string, error) {
	aead, err := k.load(true)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := aead.Seal(nonce, nonce, []byte(token), []byte(baseURL))
	return sealedPrefix + base64.RawURLEncoding.EncodeToString(out), nil
}

// Open returns the token a stored value holds. A value from before tokens
// were sealed is returned as it is.
func (k *TokenKey) Open(stored, baseURL string) (string, error) {
	if !IsSealed(stored) {
		return stored, nil
	}
	aead, err := k.load(false)
	if err != nil {
		return "", err
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(stored, sealedPrefix))
	if err != nil || len(b) < aead.NonceSize()+aead.Overhead() {
		return "", fmt.Errorf("%w: %s", ErrTokenUnreadable, k.Path)
	}
	n := aead.NonceSize()
	pt, err := aead.Open(nil, b[:n], b[n:], []byte(baseURL))
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrTokenUnreadable, k.Path)
	}
	return string(pt), nil
}
