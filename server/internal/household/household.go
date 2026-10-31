// Package household holds Plex Home style profiles: the people who share the
// server's screens, each with their own watch state, an optional PIN, and for
// children a managed profile limited by content rating. It decides who may
// switch to whom; the api package issues the session.
package household

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

var (
	ErrNotFound = errors.New("no such profile")
	// ErrNotHousehold: the caller is signed in, but not as a household member.
	ErrNotHousehold = errors.New("only household members can switch profiles")
	// ErrPINRequired: the profile has a PIN and none was given.
	ErrPINRequired = errors.New("this profile needs its PIN")
	// ErrCredentialRequired: an admin profile reached from a restricted one
	// needs the admin's PIN or password, whatever the network.
	ErrCredentialRequired = errors.New("this profile needs its PIN or password")
	ErrWrongCredential    = errors.New("wrong PIN or password")
)

// KidRating is the content-rating ceiling a kid's profile gets when none is given.
const KidRating = "PG"

// Profile is a user as the picker sees them.
type Profile struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	IsAdmin     bool   `json:"is_admin"`
	Kid         bool   `json:"kid"`
	Managed     bool   `json:"managed"`
	Household   bool   `json:"household"`
	HasPIN      bool   `json:"has_pin"`
	// Color is the avatar's background when there is no picture.
	Color string `json:"color"`
	// AvatarVersion changes with each upload, so clients can cache the
	// picture by URL; 0 means there is none and the initials show.
	AvatarVersion int64 `json:"avatar_version"`
	// MaxContentRating is the profile's rating ceiling, empty for none.
	MaxContentRating string `json:"max_content_rating"`
	Disabled         bool   `json:"-"`

	pinHash, passwordHash string
}

// palette is the avatar colours, picked by user id so a profile keeps its
// colour. All carry white initials at better than 4.5:1.
var palette = []string{"#c2410c", "#1d4ed8", "#047857", "#7c3aed", "#be123c", "#0e7490", "#a16207", "#4d7c0f"}

func colorFor(id int64) string { return palette[int(id%int64(len(palette)))] }

type Store struct {
	DB *sql.DB
	// AvatarDir holds uploaded pictures, one JPEG per user id.
	AvatarDir string
}

const profileCols = `u.id, u.username, COALESCE(u.display_name, ''), u.is_admin, u.kid, u.managed, u.household, u.disabled,
	COALESCE(u.pin_hash, ''), u.password_hash, COALESCE(u.avatar_updated_at, 0), COALESCE(p.max_content_rating, '')`

const profileFrom = ` FROM users u LEFT JOIN user_policies p ON p.user_id = u.id`

func scanProfile(row interface{ Scan(...any) error }) (Profile, error) {
	var p Profile
	err := row.Scan(&p.ID, &p.Username, &p.DisplayName, &p.IsAdmin, &p.Kid, &p.Managed, &p.Household, &p.Disabled,
		&p.pinHash, &p.passwordHash, &p.AvatarVersion, &p.MaxContentRating)
	p.HasPIN = p.pinHash != ""
	p.Color = colorFor(p.ID)
	return p, err
}

// Members lists the household's enabled profiles for the picker: admins first,
// then by name.
func (s *Store) Members(ctx context.Context) ([]Profile, error) {
	return s.list(ctx, ` WHERE u.household = 1 AND u.disabled = 0`)
}

// All lists every user with their household settings, for admins.
func (s *Store) All(ctx context.Context) ([]Profile, error) { return s.list(ctx, "") }

func (s *Store) list(ctx context.Context, where string) ([]Profile, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+profileCols+profileFrom+where+
		` ORDER BY u.is_admin DESC, COALESCE(u.display_name, u.username) COLLATE NOCASE, u.id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Profile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get reads one user, household member or not.
func (s *Store) Get(ctx context.Context, id int64) (Profile, error) {
	p, err := scanProfile(s.DB.QueryRowContext(ctx, `SELECT `+profileCols+profileFrom+` WHERE u.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return p, err
}

// ValidatePIN accepts 4 to 8 ASCII digits.
func ValidatePIN(pin string) error {
	if len(pin) < 4 || len(pin) > 8 {
		return errors.New("a PIN is 4 to 8 digits")
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			return errors.New("a PIN is digits only")
		}
	}
	return nil
}

// SetPIN stores a PIN, hashed like a password. An empty pin clears it.
func (s *Store) SetPIN(ctx context.Context, id int64, pin string) error {
	var hash any
	if pin != "" {
		if err := ValidatePIN(pin); err != nil {
			return err
		}
		h, err := auth.HashPassword(pin)
		if err != nil {
			return err
		}
		hash = h
	}
	return s.update(ctx, id, `pin_hash = ?`, hash)
}

// Update is an admin's change to a user's household settings. Nil leaves a
// field as it is.
type Update struct {
	Household *bool `json:"household"`
	Kid       *bool `json:"kid"`
	// MaxContentRating sets the rating ceiling; "" removes it.
	MaxContentRating *string `json:"max_content_rating"`
}

// Apply changes a user's household settings. Admins cannot be kids. Turning a
// profile into a kid's gives it the default rating ceiling unless one is set.
func (s *Store) Apply(ctx context.Context, id int64, u Update) error {
	p, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if u.Kid != nil && *u.Kid && p.IsAdmin {
		return errors.New("an admin cannot be a kid's profile")
	}
	if u.MaxContentRating != nil {
		if err := validRating(*u.MaxContentRating); err != nil {
			return err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	now := db.NowMs()
	if u.Household != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET household = ?, updated_at = ? WHERE id = ?`, *u.Household, now, id); err != nil {
			return err
		}
	}
	if u.Kid != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE users SET kid = ?, updated_at = ? WHERE id = ?`, *u.Kid, now, id); err != nil {
			return err
		}
	}
	rating := u.MaxContentRating
	if rating == nil && u.Kid != nil && *u.Kid && p.MaxContentRating == "" {
		r := KidRating
		rating = &r
	}
	if rating != nil {
		if err := setRating(ctx, tx, id, *rating); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// validRating accepts "" (no ceiling) or one of policy.ContentRatings.
func validRating(r string) error {
	if r == "" {
		return nil
	}
	for _, c := range policy.ContentRatings {
		if c == r {
			return nil
		}
	}
	return fmt.Errorf("max_content_rating must be one of %s", strings.Join(policy.ContentRatings, ", "))
}

// setRating changes only the rating ceiling of the user's policy, creating the
// row with the defaults when there is none.
func setRating(ctx context.Context, tx *sql.Tx, id int64, rating string) error {
	var v any
	if rating != "" {
		v = rating
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO user_policies (user_id, max_content_rating, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET max_content_rating = excluded.max_content_rating, updated_at = excluded.updated_at`,
		id, v, db.NowMs())
	return err
}

// NewProfile is a managed profile an admin creates: no password, part of the
// household, never an admin.
type NewProfile struct {
	Name string `json:"name"`
	Kid  bool   `json:"kid"`
	// MaxContentRating defaults to KidRating for a kid.
	MaxContentRating string `json:"max_content_rating"`
	PIN              string `json:"pin"`
	// LibraryIDs are the libraries it may see; nil means every library, since
	// the rating ceiling is what limits a kid.
	LibraryIDs []int64 `json:"library_ids"`
}

// Create adds a managed profile. Its username is its name, so it shows in
// Activity like anyone else's.
func (s *Store) Create(ctx context.Context, n NewProfile) (Profile, error) {
	n.Name = strings.TrimSpace(n.Name)
	if err := auth.ValidateUsername(n.Name); err != nil {
		return Profile{}, err
	}
	if utf8.RuneCountInString(n.Name) > 32 {
		return Profile{}, errors.New("a profile name is at most 32 characters")
	}
	if n.Kid && n.MaxContentRating == "" {
		n.MaxContentRating = KidRating
	}
	if err := validRating(n.MaxContentRating); err != nil {
		return Profile{}, err
	}
	var pinHash any
	if n.PIN != "" {
		if err := ValidatePIN(n.PIN); err != nil {
			return Profile{}, err
		}
		h, err := auth.HashPassword(n.PIN)
		if err != nil {
			return Profile{}, err
		}
		pinHash = h
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Profile{}, err
	}
	defer func() { _ = tx.Rollback() }()
	now := db.NowMs()
	// The name is the username; a display name would only repeat it.
	res, err := tx.ExecContext(ctx, `INSERT INTO users (username, display_name, password_hash, is_admin, household, managed, kid, pin_hash, created_at, updated_at)
		VALUES (?, NULL, ?, 0, 1, 1, ?, ?, ?, ?)`, n.Name, auth.NoPassword, n.Kid, pinHash, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Profile{}, auth.ErrUsernameTaken
		}
		return Profile{}, err
	}
	id, _ := res.LastInsertId()
	if n.MaxContentRating != "" {
		if err := setRating(ctx, tx, id, n.MaxContentRating); err != nil {
			return Profile{}, err
		}
	}
	grant := `INSERT INTO library_grants (user_id, library_id) SELECT ?, id FROM libraries`
	args := []any{id}
	if n.LibraryIDs != nil {
		if len(n.LibraryIDs) == 0 {
			grant = ""
		} else {
			grant += ` WHERE id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(n.LibraryIDs)), ",") + `)`
			for _, l := range n.LibraryIDs {
				args = append(args, l)
			}
		}
	}
	if grant != "" {
		if _, err := tx.ExecContext(ctx, grant, args...); err != nil {
			return Profile{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Profile{}, err
	}
	return s.Get(ctx, id)
}

func (s *Store) update(ctx context.Context, id int64, set string, arg any) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET `+set+`, updated_at = ? WHERE id = ?`, arg, db.NowMs(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Credential is what a switch offers: a PIN, a password, or neither.
type Credential struct {
	PIN      string
	Password string
}

// Restricted reports whether a caller counts as restricted when switching:
// anyone who is not an admin, and nobody at all (the picker before anyone has
// been chosen).
func Restricted(caller *Profile) bool { return caller == nil || !caller.IsAdmin }

// Needs reports what switching from caller (nil: the picker on a trusted
// network, nobody signed in) to target requires.
//
//   - A target with a PIN needs it (or its password, for a forgotten PIN).
//   - An admin target reached from a restricted caller needs the admin's PIN
//     or password even with no PIN set, on any network: a kid's profile on a
//     trusted network must not become an admin's for free.
//   - Anything else needs nothing.
func Needs(caller *Profile, target Profile) error {
	switch {
	case target.HasPIN:
		return ErrPINRequired
	case target.IsAdmin && Restricted(caller):
		return ErrCredentialRequired
	}
	return nil
}

// CheckSwitch decides whether caller may switch to target with cred. It
// returns nil when the switch is allowed, the Needs error when no credential
// was offered, and ErrWrongCredential when the one offered does not match.
// Only a PIN or password that was actually needed is checked, and a managed
// profile has no password to offer.
func CheckSwitch(caller *Profile, target Profile, cred Credential) error {
	if !target.Household || target.Disabled {
		return ErrNotFound
	}
	if caller != nil && !caller.Household {
		return ErrNotHousehold
	}
	need := Needs(caller, target)
	if need == nil {
		return nil
	}
	if cred.PIN == "" && cred.Password == "" {
		return need
	}
	if cred.PIN != "" && target.HasPIN {
		hashChecks.Add(1)
		if auth.CheckPassword(target.pinHash, cred.PIN) {
			return nil
		}
	}
	if cred.Password != "" && target.passwordHash != auth.NoPassword {
		hashChecks.Add(1)
		if auth.CheckPassword(target.passwordHash, cred.Password) {
			return nil
		}
	}
	return ErrWrongCredential
}

// hashChecks counts PIN and password hashes compared by CheckSwitch, so a
// test can prove the limiter stops guesses before the hash, not after.
var hashChecks atomic.Int64

// HashChecks is how many PINs and passwords CheckSwitch has hashed.
func HashChecks() int64 { return hashChecks.Load() }
