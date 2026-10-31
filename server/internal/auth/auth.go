// Package auth holds local accounts: argon2id password hashes, opaque bearer
// tokens stored only as SHA-256 hashes, and a login rate limiter.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrInvalidToken       = errors.New("invalid or expired token")
	ErrUsernameTaken      = errors.New("username already exists")
)

// OWASP's argon2id baseline: 19 MiB, 2 iterations, 1 lane.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	saltLen      = 16
)

// HashPassword returns a PHC-format argon2id hash.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// CheckPassword compares in constant time and honours the parameters stored in the hash.
func CheckPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var mem, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &t, &p); err != nil {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) != argonKeyLen {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, mem, p, argonKeyLen)
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash makes a login for an unknown user cost the same as a wrong password.
var dummyHash, _ = HashPassword("vcrx-timing-equalizer")

// NoPassword is the password_hash of a managed household profile, which has
// no password and is reached only by switching to it. No password matches it.
const NoPassword = "!no-password"

type User struct {
	ID          int64
	Username    string
	DisplayName string
	IsAdmin     bool
	Disabled    bool
	CreatedAt   int64
}

type Device struct {
	ID, Name, Model, Platform, Client, ClientVersion string
}

// Session is an authenticated request's identity.
type Session struct {
	ID        int64
	User      User
	Device    Device
	ExpiresAt int64
}

type Store struct {
	DB  *sql.DB
	TTL time.Duration
}

func ValidateUsername(u string) error {
	if u == "" || utf8.RuneCountInString(u) > 64 {
		return errors.New("username must be 1 to 64 characters")
	}
	if strings.TrimSpace(u) != u || strings.ContainsAny(u, "\x00\r\n\t") {
		return errors.New("username may not contain control characters or surrounding spaces")
	}
	return nil
}

func ValidatePassword(p string) error {
	if utf8.RuneCountInString(p) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if len(p) > 1024 {
		return errors.New("password too long")
	}
	return nil
}

// CreateUser adds a local account. Usernames are unique without regard to case.
func (s *Store) CreateUser(ctx context.Context, username, password, displayName string, isAdmin bool) (User, error) {
	if err := ValidateUsername(username); err != nil {
		return User{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	now := db.NowMs()
	var dn any
	if displayName != "" {
		dn = displayName
	}
	// Admins are part of the household from the start, so the profile
	// picker always has someone in it.
	res, err := s.DB.ExecContext(ctx, `INSERT INTO users (username, display_name, password_hash, is_admin, household, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, username, dn, hash, isAdmin, isAdmin, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, ErrUsernameTaken
		}
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Username: username, DisplayName: displayName, IsAdmin: isAdmin, CreatedAt: now}, nil
}

func (s *Store) SetPassword(ctx context.Context, userID int64, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, hash, db.NowMs(), userID); err != nil {
		return err
	}
	// A password change signs out every device.
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id = ?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) UserByName(ctx context.Context, username string) (User, string, error) {
	var u User
	var dn sql.NullString
	var hash string
	err := s.DB.QueryRowContext(ctx, `SELECT id, username, display_name, password_hash, is_admin, disabled, created_at
		FROM users WHERE username = ?`, username).Scan(&u.ID, &u.Username, &dn, &hash, &u.IsAdmin, &u.Disabled, &u.CreatedAt)
	u.DisplayName = dn.String
	return u, hash, err
}

// Login checks credentials and creates a session, returning the raw token once.
func (s *Store) Login(ctx context.Context, username, password string, dev Device, ip string) (string, Session, error) {
	u, hash, err := s.UserByName(ctx, username)
	if errors.Is(err, sql.ErrNoRows) {
		CheckPassword(dummyHash, password)
		return "", Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", Session{}, err
	}
	if hash == NoPassword {
		// A managed profile cannot sign in with a password; answer as slowly
		// as a wrong one, so its name cannot be told apart.
		CheckPassword(dummyHash, password)
		return "", Session{}, ErrInvalidCredentials
	}
	if !CheckPassword(hash, password) || u.Disabled {
		return "", Session{}, ErrInvalidCredentials
	}
	return s.issue(ctx, u, dev, ip)
}

// SignInAs starts a session for a user without a password. Only the trusted
// network sign-in calls it, after checking the client's address.
func (s *Store) SignInAs(ctx context.Context, username string, dev Device, ip string) (string, Session, error) {
	u, _, err := s.UserByName(ctx, username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", Session{}, err
	}
	if u.Disabled {
		return "", Session{}, ErrInvalidCredentials
	}
	return s.issue(ctx, u, dev, ip)
}

func (s *Store) issue(ctx context.Context, u User, dev Device, ip string) (string, Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Session{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	now := time.Now()
	exp := now.Add(s.TTL).UnixMilli()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO auth_sessions
		(user_id, token_hash, device_id, device_name, device_model, platform, client, client_version, ip, created_at, last_seen_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, sum[:], dev.ID, dev.Name, dev.Model, dev.Platform, dev.Client, dev.ClientVersion, ip, now.UnixMilli(), now.UnixMilli(), exp)
	if err != nil {
		return "", Session{}, err
	}
	id, _ := res.LastInsertId()
	return token, Session{ID: id, User: u, Device: dev, ExpiresAt: exp}, nil
}

// Authenticate resolves a bearer token. Sliding expiry: each use within the last
// half of the TTL extends it.
func (s *Store) Authenticate(ctx context.Context, token, ip string) (Session, error) {
	if token == "" || len(token) > 256 {
		return Session{}, ErrInvalidToken
	}
	sum := sha256.Sum256([]byte(token))
	var sess Session
	var dn, devID, devName, devModel, platform, client, clientVer sql.NullString
	var lastSeen int64
	err := s.DB.QueryRowContext(ctx, `SELECT s.id, s.expires_at, s.last_seen_at, s.device_id, s.device_name, s.device_model, s.platform,
			s.client, s.client_version, u.id, u.username, u.display_name, u.is_admin, u.disabled, u.created_at
		FROM auth_sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ?`, sum[:]).Scan(
		&sess.ID, &sess.ExpiresAt, &lastSeen, &devID, &devName, &devModel, &platform, &client, &clientVer,
		&sess.User.ID, &sess.User.Username, &dn, &sess.User.IsAdmin, &sess.User.Disabled, &sess.User.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrInvalidToken
	}
	if err != nil {
		return Session{}, err
	}
	now := time.Now()
	if sess.User.Disabled || now.UnixMilli() >= sess.ExpiresAt {
		return Session{}, ErrInvalidToken
	}
	sess.User.DisplayName = dn.String
	sess.Device = Device{ID: devID.String, Name: devName.String, Model: devModel.String, Platform: platform.String,
		Client: client.String, ClientVersion: clientVer.String}
	if now.UnixMilli()-lastSeen > time.Minute.Milliseconds() {
		exp := sess.ExpiresAt
		if time.Until(time.UnixMilli(exp)) < s.TTL/2 {
			exp = now.Add(s.TTL).UnixMilli()
		}
		_, _ = s.DB.ExecContext(ctx, `UPDATE auth_sessions SET last_seen_at = ?, expires_at = ?, ip = ? WHERE id = ?`,
			now.UnixMilli(), exp, ip, sess.ID)
		sess.ExpiresAt = exp
	}
	return sess, nil
}

func (s *Store) Logout(ctx context.Context, sessionID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM auth_sessions WHERE id = ?`, sessionID)
	return err
}

// PurgeExpired deletes expired sessions.
func (s *Store) PurgeExpired(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM auth_sessions WHERE expires_at <= ?`, db.NowMs())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
