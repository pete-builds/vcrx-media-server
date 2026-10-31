package household

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

func newStore(t *testing.T) (*Store, *auth.Store) {
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
	now := time.Now().UnixMilli()
	for _, name := range []string{"Movies", "TV"} {
		if _, err := conn.Exec(`INSERT INTO libraries (name, kind, root_path, created_at, updated_at) VALUES (?, 'movie', ?, ?, ?)`,
			name, "/media/"+name, now, now); err != nil {
			t.Fatal(err)
		}
	}
	return &Store{DB: conn, AvatarDir: filepath.Join(dir, "avatars")}, &auth.Store{DB: conn, TTL: time.Hour}
}

func TestValidatePIN(t *testing.T) {
	for _, ok := range []string{"0000", "1234", "98765432"} {
		if err := ValidatePIN(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	// Arabic-Indic digits are digits to unicode.IsDigit, not to a TV's PIN pad.
	for _, bad := range []string{"", "123", "123456789", "12a4", "12 34", "١٢٣٤", "-123"} {
		if err := ValidatePIN(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A PIN is stored as an argon2id hash, never as the digits, and clearing it
// leaves no hash behind.
func TestPINIsHashed(t *testing.T) {
	s, a := newStore(t)
	ctx := context.Background()
	u, err := a.CreateUser(ctx, "pete", "adminpass1", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetPIN(ctx, u.ID, "2468"); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.DB.QueryRow(`SELECT pin_hash FROM users WHERE id = ?`, u.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, "$argon2id$") || strings.Contains(stored, "2468") {
		t.Fatalf("stored PIN %q is not an argon2id hash", stored)
	}
	p, err := s.Get(ctx, u.ID)
	if err != nil || !p.HasPIN {
		t.Fatalf("has_pin after set: %+v %v", p, err)
	}
	if err := s.SetPIN(ctx, u.ID, "12ab"); err == nil {
		t.Fatal("a PIN with letters was stored")
	}
	if err := s.SetPIN(ctx, u.ID, ""); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Get(ctx, u.ID); p.HasPIN {
		t.Fatal("PIN still set after clearing")
	}
	if err := s.SetPIN(ctx, 999, "1234"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestCheckSwitch(t *testing.T) {
	s, a := newStore(t)
	ctx := context.Background()
	admin, _ := a.CreateUser(ctx, "pete", "adminpass1", "", true)
	friend, _ := a.CreateUser(ctx, "friend", "friendpass1", "", false)
	kid, err := s.Create(ctx, NewProfile{Name: "Robin", Kid: true})
	if err != nil {
		t.Fatal(err)
	}
	teen, err := s.Create(ctx, NewProfile{Name: "Sam", PIN: "2468"})
	if err != nil {
		t.Fatal(err)
	}
	get := func(id int64) *Profile {
		p, err := s.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return &p
	}
	pete, robin, sam, fr := get(admin.ID), get(kid.ID), get(teen.ID), get(friend.ID)
	cases := []struct {
		name           string
		caller, target *Profile
		cred           Credential
		want           error
	}{
		{"admin to a kid with no PIN", pete, robin, Credential{}, nil},
		{"admin to a profile with a PIN, none given", pete, sam, Credential{}, ErrPINRequired},
		{"admin to a profile with a PIN, wrong", pete, sam, Credential{PIN: "1357"}, ErrWrongCredential},
		{"admin to a profile with a PIN, right", pete, sam, Credential{PIN: "2468"}, nil},
		{"a managed profile has no password to offer", pete, sam, Credential{Password: auth.NoPassword}, ErrWrongCredential},
		{"kid to an admin with no PIN needs a credential", robin, pete, Credential{}, ErrCredentialRequired},
		{"the picker to an admin with no PIN needs a credential", nil, pete, Credential{}, ErrCredentialRequired},
		{"kid to an admin with the admin's password", robin, pete, Credential{Password: "adminpass1"}, nil},
		{"kid to an admin with a wrong password", robin, pete, Credential{Password: "guess-guess"}, ErrWrongCredential},
		{"kid to a kid with no PIN", robin, robin, Credential{}, nil},
		{"the picker to a kid with no PIN", nil, robin, Credential{}, nil},
		{"a friend outside the household", fr, robin, Credential{}, ErrNotHousehold},
		{"to someone outside the household", pete, fr, Credential{}, ErrNotFound},
	}
	for _, c := range cases {
		if got := CheckSwitch(c.caller, *c.target, c.cred); !errors.Is(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// With a PIN set, an admin reached from a kid takes the PIN too.
	if err := s.SetPIN(ctx, pete.ID, "8080"); err != nil {
		t.Fatal(err)
	}
	pete = get(admin.ID)
	if err := CheckSwitch(robin, *pete, Credential{}); !errors.Is(err, ErrPINRequired) {
		t.Fatalf("kid to admin with a PIN, none given: %v", err)
	}
	if err := CheckSwitch(robin, *pete, Credential{PIN: "8080"}); err != nil {
		t.Fatalf("kid to admin with the PIN: %v", err)
	}
	// A disabled profile cannot be switched to.
	if _, err := s.DB.Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, robin.ID); err != nil {
		t.Fatal(err)
	}
	if err := CheckSwitch(pete, *get(kid.ID), Credential{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled target: %v", err)
	}
}

// A kid's profile is managed, part of the household, sees every library, has
// a PG ceiling by default, and cannot be signed into with a password.
func TestCreateKidProfile(t *testing.T) {
	s, a := newStore(t)
	ctx := context.Background()
	kid, err := s.Create(ctx, NewProfile{Name: "Robin", Kid: true})
	if err != nil {
		t.Fatal(err)
	}
	if !kid.Kid || !kid.Managed || !kid.Household || kid.IsAdmin || kid.MaxContentRating != KidRating || kid.HasPIN {
		t.Fatalf("kid profile: %+v", kid)
	}
	pol, err := policy.Load(ctx, s.DB, kid.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if pol.MaxContentRating != "PG" || len(pol.LibraryIDs) != 2 {
		t.Fatalf("kid policy: %+v", pol)
	}
	for _, pw := range []string{"", auth.NoPassword, "anything-at-all"} {
		if _, _, err := a.Login(ctx, "Robin", pw, auth.Device{}, "192.168.1.20"); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("password sign-in to a managed profile with %q: %v", pw, err)
		}
	}
	if _, err := s.Create(ctx, NewProfile{Name: "robin"}); !errors.Is(err, auth.ErrUsernameTaken) {
		t.Fatalf("duplicate name, other case: %v", err)
	}
	if _, err := s.Create(ctx, NewProfile{Name: "Ash", MaxContentRating: "TV-MA"}); err == nil {
		t.Fatal("a ceiling outside the movie ratings was accepted")
	}
	none, err := s.Create(ctx, NewProfile{Name: "Ash", LibraryIDs: []int64{}})
	if err != nil {
		t.Fatal(err)
	}
	if pol, _ := policy.Load(ctx, s.DB, none.ID, false); len(pol.LibraryIDs) != 0 || pol.MaxContentRating != "" {
		t.Fatalf("explicitly no libraries: %+v", pol)
	}
}

func TestApply(t *testing.T) {
	s, a := newStore(t)
	ctx := context.Background()
	admin, _ := a.CreateUser(ctx, "pete", "adminpass1", "", true)
	friend, _ := a.CreateUser(ctx, "friend", "friendpass1", "", false)
	yes := true
	if err := s.Apply(ctx, admin.ID, Update{Kid: &yes}); err == nil {
		t.Fatal("an admin became a kid")
	}
	if err := s.Apply(ctx, friend.ID, Update{Household: &yes, Kid: &yes}); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Get(ctx, friend.ID)
	if !p.Household || !p.Kid || p.MaxContentRating != KidRating {
		t.Fatalf("after joining as a kid: %+v", p)
	}
	r := "PG-13"
	if err := s.Apply(ctx, friend.ID, Update{MaxContentRating: &r}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Get(ctx, friend.ID); p.MaxContentRating != "PG-13" {
		t.Fatalf("ceiling: %q", p.MaxContentRating)
	}
	if err := s.Apply(ctx, 999, Update{Household: &yes}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

// An upload is cropped to a square, scaled to 256 px and re-encoded as JPEG,
// which drops whatever else the file carried.
func TestAvatar(t *testing.T) {
	s, a := newStore(t)
	ctx := context.Background()
	u, _ := a.CreateUser(ctx, "pete", "adminpass1", "", true)
	src := image.NewRGBA(image.Rect(0, 0, 600, 300))
	for y := range 300 {
		for x := range 600 {
			c := color.RGBA{R: 200, A: 255}
			if x >= 150 && x < 450 {
				c = color.RGBA{B: 200, A: 255} // the centre square is blue
			}
			src.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	marker := []byte("GPS 12.34 56.78 secret-marker")
	upload := append(buf.Bytes(), marker...) // trailing bytes, as metadata would ride along
	if err := s.SaveAvatar(ctx, u.ID, upload); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(s.AvatarPath(u.ID))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(saved, marker) {
		t.Fatal("the stored picture kept the upload's extra bytes")
	}
	img, err := jpeg.Decode(bytes.NewReader(saved))
	if err != nil {
		t.Fatalf("stored picture is not a JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != AvatarSize || b.Dy() != AvatarSize {
		t.Fatalf("stored picture is %v", b)
	}
	if r, _, b, _ := img.At(128, 128).RGBA(); b>>8 < 150 || r>>8 > 60 {
		t.Fatalf("centre is not the source's centre: r=%d b=%d", r>>8, b>>8)
	}
	if p, _ := s.Get(ctx, u.ID); p.AvatarVersion == 0 {
		t.Fatal("avatar version not set")
	}
	if err := s.SaveAvatar(ctx, u.ID, []byte("<svg onload=alert(1)>")); err == nil {
		t.Fatal("a non-image was stored")
	}
	if err := s.DeleteAvatar(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.AvatarPath(u.ID)); !os.IsNotExist(err) {
		t.Fatalf("picture still on disk: %v", err)
	}
	if p, _ := s.Get(ctx, u.ID); p.AvatarVersion != 0 {
		t.Fatal("avatar version kept after delete")
	}
}
