package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/db"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "vcrx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := db.Migrate(context.Background(), conn, "", slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	return &Store{DB: conn, TTL: 24 * time.Hour}
}

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !CheckPassword(h, "correct horse") {
		t.Fatal("right password rejected")
	}
	if CheckPassword(h, "correct horsE") || CheckPassword(h, "") || CheckPassword("garbage", "correct horse") {
		t.Fatal("wrong password accepted")
	}
	h2, _ := HashPassword("correct horse")
	if h == h2 {
		t.Fatal("hashes are not salted")
	}
}

func TestUsernamesAreCaseInsensitive(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.CreateUser(ctx, "Pete", "password1", "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "pete", "password2", "", false); !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("second user differing by case: err = %v, want ErrUsernameTaken", err)
	}
	if _, _, err := s.Login(ctx, "PETE", "password1", Device{Name: "tv"}, "192.168.1.5"); err != nil {
		t.Fatalf("login with different case: %v", err)
	}
}

func TestLoginTokenLifecycle(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	if _, err := s.CreateUser(ctx, "friend", "password1", "", false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Login(ctx, "friend", "nope-nope", Device{}, "ip"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, _, err := s.Login(ctx, "nobody", "password1", Device{}, "ip"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user: %v", err)
	}
	tok, sess, err := s.Login(ctx, "friend", "password1", Device{ID: "d1", Name: "Living room"}, "ip")
	if err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := s.DB.QueryRow(`SELECT token_hash FROM auth_sessions WHERE id = ?`, sess.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if string(stored) == tok {
		t.Fatal("raw token stored")
	}
	got, err := s.Authenticate(ctx, tok, "ip")
	if err != nil || got.User.Username != "friend" || got.Device.Name != "Living room" {
		t.Fatalf("Authenticate = %+v, %v", got, err)
	}
	if _, err := s.Authenticate(ctx, tok+"x", "ip"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("tampered token: %v", err)
	}
	if err := s.Logout(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, tok, "ip"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("token after logout: %v", err)
	}
}

func TestExpiredAndDisabled(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u, _ := s.CreateUser(ctx, "friend", "password1", "", false)
	tok, sess, _ := s.Login(ctx, "friend", "password1", Device{}, "ip")
	if _, err := s.DB.Exec(`UPDATE auth_sessions SET expires_at = 1 WHERE id = ?`, sess.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, tok, "ip"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token: %v", err)
	}
	tok, _, _ = s.Login(ctx, "friend", "password1", Device{}, "ip")
	if _, err := s.DB.Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, tok, "ip"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("disabled user's token: %v", err)
	}
	if _, _, err := s.Login(ctx, "friend", "password1", Device{}, "ip"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled user login: %v", err)
	}
}

func TestPasswordChangeSignsOut(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	u, _ := s.CreateUser(ctx, "friend", "password1", "", false)
	tok, _, _ := s.Login(ctx, "friend", "password1", Device{}, "ip")
	if err := s.SetPassword(ctx, u.ID, "password2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, tok, "ip"); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("old token survived a password change")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	for range 3 {
		if ok, _ := l.Allowed("k"); !ok {
			t.Fatal("blocked too early")
		}
		l.Fail("k")
	}
	if ok, wait := l.Allowed("k"); ok || wait <= 0 {
		t.Fatalf("not blocked after 3 failures: ok=%v wait=%v", ok, wait)
	}
	now = now.Add(61 * time.Second)
	if ok, _ := l.Allowed("k"); !ok {
		t.Fatal("still blocked after the window")
	}
	l.Fail("k")
	l.Reset("k")
	if ok, _ := l.Allowed("k"); !ok {
		t.Fatal("reset did not clear")
	}
}

func TestIPKeyGroupsIPv6ByPrefix(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.20":            "ip:192.168.1.20",
		"2001:db8:1:2::a":         "ip:2001:db8:1:2::/64",
		"2001:db8:1:2:aa:bb:cc:d": "ip:2001:db8:1:2::/64",
		"::ffff:192.168.1.20":     "ip:192.168.1.20",
		"unknown":                 "ip:unknown",
	} {
		if got := IPKey(in); got != want {
			t.Errorf("IPKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTailscaleAddresses(t *testing.T) {
	for ip, want := range map[string]bool{"100.101.102.103": true, "100.128.0.1": false,
		"fd7a:115c:a1e0::1": true, "fd7a:115c:a1e1::1": false, "192.168.1.20": false, "unknown": false} {
		if got := IsTailscale(ip); got != want {
			t.Errorf("IsTailscale(%q) = %v", ip, got)
		}
	}
	if a, b := IPKey("fd7a:115c:a1e0::1"), IPKey("fd7a:115c:a1e0::2"); a == b {
		t.Errorf("two Tailscale peers share the bucket %s", a)
	}
	l := NewLimiter(2, time.Minute)
	for range 2 {
		l.Fail(UserKey("pete"))
		l.Fail(LANUserKey("pete"))
	}
	if ok, _ := l.Allowed(UserKey("pete")); ok {
		t.Error("internet account key not locked at the limit")
	}
	if ok, _ := l.Allowed(LANUserKey("pete")); !ok {
		t.Error("LAN account key locked at the base limit, want LANAccountFactor times it")
	}
}
