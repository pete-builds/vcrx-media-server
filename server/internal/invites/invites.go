// Package invites makes the invite token the account (milestone 9, item 1).
// An admin mints an invite, a link or a QR code, scoped to some libraries, a
// highest resolution, whether converting is allowed, and an expiry. The device
// that redeems it becomes its own principal, with no username, password or
// email. A TV gets the same principal by pairing: it shows a short code, and an
// admin's phone approves it with a scope (pairing.go).
//
// A device principal is a users row with device_token = 1 and its scope in
// library_grants and user_policies, so the server's one policy path applies to
// it exactly as to an account. It is never an admin and never in the
// household, so it can neither mint, nor reach the profile picker, nor switch
// into a profile.
package invites

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"rsc.io/qr"

	"github.com/pete-builds/vcrx-media-server/server/internal/auth"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

var (
	ErrNotFound = errors.New("no such device")
	// ErrInvalid is any invite that cannot be redeemed: unknown, already
	// used, revoked or expired. One error for all four, so a guesser learns
	// nothing about which secrets once existed.
	ErrInvalid = errors.New("this invite is not valid: it may have been used already, revoked, or expired")
)

// SecretBytes is the invite secret's size: 160 random bits, comfortably above
// the 128 an unguessable bearer secret needs.
const SecretBytes = 20

// Expiry limits: a token lasts at least an hour and at most a year.
const (
	MinLifetime = time.Hour
	MaxLifetime = 366 * 24 * time.Hour
)

var secretEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a fresh invite secret: 32 base32 characters, lowercase so
// it reads well in a link, and accepted in any case with spaces or dashes.
func NewSecret() (string, error) {
	b := make([]byte, SecretBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(secretEncoding.EncodeToString(b)), nil
}

// HashSecret is what the database keeps: the SHA-256 of the secret's bytes.
// A secret that does not decode to SecretBytes bytes has no hash.
func HashSecret(secret string) ([]byte, bool) {
	clean := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(secret)))
	if len(clean) != secretEncoding.EncodedLen(SecretBytes) {
		return nil, false
	}
	raw, err := secretEncoding.DecodeString(clean)
	if err != nil || len(raw) != SecretBytes {
		return nil, false
	}
	sum := sha256.Sum256(raw)
	return sum[:], true
}

// Scope is what a token allows. It never grows after the token is made:
// changing it means revoking the device and inviting it again.
type Scope struct {
	LibraryIDs []int64 `json:"library_ids"`
	// MaxResolution is a policy.Resolutions value, or "" for any.
	MaxResolution string `json:"max_resolution"`
	// AllowTranscode lets the server convert files this device cannot play
	// as they are, 4K included.
	AllowTranscode bool `json:"allow_transcode"`
	// ExpiresAt is when the token stops working, in Unix milliseconds.
	ExpiresAt int64 `json:"expires_at"`
	// BandwidthKbps caps everything a linked server streams at once, in
	// kilobits per second; 0 is no cap. Only server invites carry one.
	BandwidthKbps int64 `json:"bandwidth_kbps"`
}

// Bandwidth cap limits for a linked server, in kilobits per second.
const (
	MinBandwidthKbps = 64
	MaxBandwidthKbps = 10_000_000
)

// Device is one invited device, as the admin's list shows it.
type Device struct {
	ID     int64  `json:"id"`
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Source string `json:"source"`
	// Peer marks a linked friend's server (milestone 9, item 3) rather than
	// a device: it redeemed a server invite, and streams for its household.
	Peer        bool    `json:"peer"`
	State       string  `json:"state"` // pending (not redeemed yet), active, revoked or expired
	Scope       Scope   `json:"scope"`
	Summary     string  `json:"summary"`
	CreatedBy   *string `json:"created_by"`
	CreatedAt   int64   `json:"created_at"`
	RedeemedAt  *int64  `json:"redeemed_at"`
	RevokedAt   *int64  `json:"revoked_at"`
	RevokedBy   *string `json:"revoked_by"`
	LastSeenAt  *int64  `json:"last_seen_at"`
	LastIP      *string `json:"last_ip"`
	DeviceName  *string `json:"device_name"`
	DeviceModel *string `json:"device_model"`
	Platform    *string `json:"platform"`
	Client      *string `json:"client"`
}

type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// CleanName trims a device name and checks its length.
func CleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 64 || strings.ContainsAny(name, "\x00\r\n\t") {
		return "", errors.New("give the device a name of 1 to 64 characters")
	}
	return name, nil
}

// Validate checks a scope before anything is written: at least one library,
// every one of them real, a known resolution, and an expiry from an hour to a
// year away.
func (s *Store) Validate(ctx context.Context, sc *Scope) error {
	if len(sc.LibraryIDs) == 0 {
		return errors.New("choose at least one library")
	}
	seen := map[int64]bool{}
	ids := sc.LibraryIDs[:0:0]
	for _, id := range sc.LibraryIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		var n int
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM libraries WHERE id = ?`, id).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("library %d does not exist", id)
		}
		ids = append(ids, id)
	}
	sc.LibraryIDs = ids
	if sc.MaxResolution != "" && policy.ResolutionRank(sc.MaxResolution) < 0 {
		return fmt.Errorf("unknown resolution %q", sc.MaxResolution)
	}
	if sc.MaxResolution != "" {
		sc.MaxResolution = policy.Resolutions[policy.ResolutionRank(sc.MaxResolution)]
	}
	if sc.BandwidthKbps != 0 && (sc.BandwidthKbps < MinBandwidthKbps || sc.BandwidthKbps > MaxBandwidthKbps) {
		return fmt.Errorf("a bandwidth cap is between %d kbit/s and %d Gbit/s, or none", MinBandwidthKbps, MaxBandwidthKbps/1_000_000)
	}
	now := s.now()
	if sc.ExpiresAt < now.Add(MinLifetime).UnixMilli()-1000 || sc.ExpiresAt > now.Add(MaxLifetime).UnixMilli() {
		return errors.New("an invite expires between an hour and a year from now")
	}
	return nil
}

// create writes the principal, its scope and its token row in one transaction.
func (s *Store) create(ctx context.Context, createdBy, name, source string, sc Scope, inviteHash []byte, dev auth.Device, peer bool) (int64, int64, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	tokenID, userID, err := s.createIn(ctx, tx, createdBy, name, source, sc, inviteHash, dev, peer)
	if err != nil {
		return 0, 0, err
	}
	return tokenID, userID, tx.Commit()
}

// MintIn makes an invite inside the caller's transaction, for a caller that
// writes more alongside it (a lent tape writes its loan in the same commit).
// The scope must have passed Validate. It returns the token id, the device's
// user id and the secret, which is not kept.
func (s *Store) MintIn(ctx context.Context, tx *sql.Tx, createdBy, name string, sc Scope) (int64, int64, string, error) {
	name, err := CleanName(name)
	if err != nil {
		return 0, 0, "", err
	}
	secret, err := NewSecret()
	if err != nil {
		return 0, 0, "", err
	}
	hash, _ := HashSecret(secret)
	tokenID, userID, err := s.createIn(ctx, tx, createdBy, name, "invite", sc, hash, auth.Device{}, false)
	return tokenID, userID, secret, err
}

func (s *Store) createIn(ctx context.Context, tx *sql.Tx, createdBy, name, source string, sc Scope, inviteHash []byte, dev auth.Device, peer bool) (int64, int64, error) {
	now := s.now().UnixMilli()
	tag := make([]byte, 6)
	if _, err := rand.Read(tag); err != nil {
		return 0, 0, err
	}
	// The username is only a unique handle; people see the name. No password
	// matches NoPassword, the principal is never an admin and never in the
	// household, so the only way in is this token.
	res, err := tx.ExecContext(ctx, `INSERT INTO users (username, display_name, password_hash, is_admin, household, managed, kid,
			device_token, created_at, updated_at) VALUES (?, ?, ?, 0, 0, 0, 0, 1, ?, ?)`,
		"device-"+hex.EncodeToString(tag), name, auth.NoPassword, now, now)
	if err != nil {
		return 0, 0, err
	}
	userID, _ := res.LastInsertId()
	for _, lib := range sc.LibraryIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO library_grants (user_id, library_id) VALUES (?, ?)`, userID, lib); err != nil {
			return 0, 0, err
		}
	}
	var maxRes any
	if sc.MaxResolution != "" {
		maxRes = sc.MaxResolution
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_policies (user_id, max_resolution, allow_transcode, allow_4k_transcode, updated_at)
		VALUES (?, ?, ?, ?, ?)`, userID, maxRes, sc.AllowTranscode, sc.AllowTranscode, now); err != nil {
		return 0, 0, err
	}
	var redeemed any
	if source == "pairing" {
		redeemed = now
	}
	var hash any
	if inviteHash != nil {
		hash = inviteHash
	}
	var kbps any
	if peer && sc.BandwidthKbps > 0 {
		kbps = sc.BandwidthKbps
	}
	res, err = tx.ExecContext(ctx, `INSERT INTO device_tokens (user_id, name, created_by, source, invite_hash, expires_at, redeemed_at,
			device_name, device_model, platform, client, created_at, peer, bandwidth_kbps)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, userID, name, nullStr(createdBy), source, hash, sc.ExpiresAt, redeemed,
		nullStr(dev.Name), nullStr(dev.Model), nullStr(dev.Platform), nullStr(dev.Client), now, peer, kbps)
	if err != nil {
		return 0, 0, err
	}
	tokenID, _ := res.LastInsertId()
	return tokenID, userID, nil
}

// Mint makes an invite and returns its device row and the secret. The secret
// is not kept: this is the only time anyone sees it.
func (s *Store) Mint(ctx context.Context, createdBy, name string, sc Scope) (Device, string, error) {
	sc.BandwidthKbps = 0
	return s.mint(ctx, createdBy, name, sc, false)
}

// MintPeer makes a server invite: the same single-use, hashed invite, whose
// principal is a friend's whole server rather than one device. It may carry a
// bandwidth cap.
func (s *Store) MintPeer(ctx context.Context, createdBy, name string, sc Scope) (Device, string, error) {
	return s.mint(ctx, createdBy, name, sc, true)
}

func (s *Store) mint(ctx context.Context, createdBy, name string, sc Scope, peer bool) (Device, string, error) {
	name, err := CleanName(name)
	if err != nil {
		return Device{}, "", err
	}
	if err := s.Validate(ctx, &sc); err != nil {
		return Device{}, "", err
	}
	secret, err := NewSecret()
	if err != nil {
		return Device{}, "", err
	}
	hash, _ := HashSecret(secret)
	id, _, err := s.create(ctx, createdBy, name, "invite", sc, hash, auth.Device{}, peer)
	if err != nil {
		return Device{}, "", err
	}
	d, err := s.Get(ctx, id)
	return d, secret, err
}

// Approve makes the principal for a TV a phone approved. It has no invite:
// the pairing's device code is how the TV collects its session.
func (s *Store) Approve(ctx context.Context, createdBy, name string, sc Scope, dev auth.Device) (Device, error) {
	name, err := CleanName(name)
	if err != nil {
		return Device{}, err
	}
	if err := s.Validate(ctx, &sc); err != nil {
		return Device{}, err
	}
	sc.BandwidthKbps = 0
	id, _, err := s.create(ctx, createdBy, name, "pairing", sc, nil, dev, false)
	if err != nil {
		return Device{}, err
	}
	return s.Get(ctx, id)
}

// Redeem spends an invite: one UPDATE that only an unused, unrevoked,
// unexpired invite passes, so two devices racing with the same link cannot
// both get in, and a replayed link fails. A server invite is not a device's:
// it redeems only through RedeemPeer, and a device invite never through that.
func (s *Store) Redeem(ctx context.Context, secret string, dev auth.Device, ip string) (Device, error) {
	return s.redeem(ctx, secret, dev, ip, false)
}

// RedeemPeer spends a server invite for the friend's server that presents it.
func (s *Store) RedeemPeer(ctx context.Context, secret string, dev auth.Device, ip string) (Device, error) {
	return s.redeem(ctx, secret, dev, ip, true)
}

func (s *Store) redeem(ctx context.Context, secret string, dev auth.Device, ip string, peer bool) (Device, error) {
	hash, ok := HashSecret(secret)
	if !ok {
		return Device{}, ErrInvalid
	}
	now := s.now().UnixMilli()
	var id int64
	err := s.DB.QueryRowContext(ctx, `UPDATE device_tokens SET redeemed_at = ?, last_seen_at = ?, last_ip = ?,
			device_name = ?, device_model = ?, platform = ?, client = ?
		WHERE invite_hash = ? AND redeemed_at IS NULL AND revoked_at IS NULL AND expires_at > ? AND peer = ?
		RETURNING id`, now, now, nullStr(ip), nullStr(dev.Name), nullStr(dev.Model), nullStr(dev.Platform), nullStr(dev.Client),
		hash, now, peer).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrInvalid
	}
	if err != nil {
		return Device{}, err
	}
	return s.Get(ctx, id)
}

const deviceCols = `dt.id, dt.user_id, dt.name, dt.source, dt.created_by, dt.created_at, dt.expires_at, dt.redeemed_at, dt.revoked_at,
	dt.revoked_by, dt.last_seen_at, dt.last_ip, dt.device_name, dt.device_model, dt.platform, dt.client,
	p.max_resolution, COALESCE(p.allow_transcode, 0), dt.peer, COALESCE(dt.bandwidth_kbps, 0)`

const deviceFrom = ` FROM device_tokens dt LEFT JOIN user_policies p ON p.user_id = dt.user_id`

func (s *Store) scan(ctx context.Context, rows *sql.Rows) ([]Device, error) {
	defer func() { _ = rows.Close() }()
	out := []Device{}
	now := s.now().UnixMilli()
	for rows.Next() {
		var d Device
		var createdBy, revokedBy, lastIP, dname, dmodel, platform, client, maxRes sql.NullString
		var redeemed, revoked, seen sql.NullInt64
		if err := rows.Scan(&d.ID, &d.UserID, &d.Name, &d.Source, &createdBy, &d.CreatedAt, &d.Scope.ExpiresAt, &redeemed, &revoked,
			&revokedBy, &seen, &lastIP, &dname, &dmodel, &platform, &client, &maxRes, &d.Scope.AllowTranscode, &d.Peer,
			&d.Scope.BandwidthKbps); err != nil {
			return nil, err
		}
		d.CreatedBy, d.RevokedBy, d.LastIP = str(createdBy), str(revokedBy), str(lastIP)
		d.DeviceName, d.DeviceModel, d.Platform, d.Client = str(dname), str(dmodel), str(platform), str(client)
		d.RedeemedAt, d.RevokedAt, d.LastSeenAt = i64(redeemed), i64(revoked), i64(seen)
		d.Scope.MaxResolution = maxRes.String
		switch {
		case revoked.Valid:
			d.State = "revoked"
		case now >= d.Scope.ExpiresAt:
			d.State = "expired"
		case !redeemed.Valid:
			d.State = "pending"
		default:
			d.State = "active"
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		libs, err := policy.Load(ctx, s.DB, out[i].UserID, false)
		if err != nil {
			return nil, err
		}
		out[i].Scope.LibraryIDs = append([]int64{}, libs.LibraryIDs...)
		// A lent tape's device sees one title, whatever its library says.
		var loanTitle string
		if err := s.DB.QueryRowContext(ctx, `SELECT title FROM loans WHERE token_id = ?`, out[i].ID).Scan(&loanTitle); err != nil &&
			!errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		out[i].Summary, err = s.summary(ctx, out[i].Scope, loanTitle)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// summary says what a scope allows in one line. loanTitle names the one
// title a lent tape's device may see, when it is one.
func (s *Store) summary(ctx context.Context, sc Scope, loanTitle string) (string, error) {
	var names []string
	for _, id := range sc.LibraryIDs {
		var n string
		if err := s.DB.QueryRowContext(ctx, `SELECT name FROM libraries WHERE id = ?`, id).Scan(&n); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return "", err
		}
		names = append(names, n)
	}
	out := "Sees " + joinAnd(names)
	if len(names) == 0 {
		out = "Sees nothing"
	}
	if loanTitle != "" {
		out = "Borrowing “" + loanTitle + "” only"
	}
	if sc.MaxResolution != "" {
		out += ", up to " + sc.MaxResolution
	}
	if sc.AllowTranscode {
		out += ", may convert"
	} else {
		out += ", direct play only"
	}
	if sc.BandwidthKbps > 0 {
		out += ", at most " + FormatKbps(sc.BandwidthKbps)
	}
	return out + ".", nil
}

// FormatKbps writes a bandwidth cap for people: "8 Mbit/s", "512 kbit/s".
func FormatKbps(k int64) string {
	if k >= 1000 && k%100 == 0 {
		if k%1000 == 0 {
			return fmt.Sprintf("%d Mbit/s", k/1000)
		}
		return fmt.Sprintf("%.1f Mbit/s", float64(k)/1000)
	}
	return fmt.Sprintf("%d kbit/s", k)
}

func joinAnd(xs []string) string {
	switch len(xs) {
	case 0:
		return ""
	case 1:
		return xs[0]
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

// List returns every device, newest first.
func (s *Store) List(ctx context.Context) ([]Device, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+deviceCols+deviceFrom+` ORDER BY dt.created_at DESC, dt.id DESC`)
	if err != nil {
		return nil, err
	}
	return s.scan(ctx, rows)
}

func (s *Store) Get(ctx context.Context, id int64) (Device, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+deviceCols+deviceFrom+` WHERE dt.id = ?`, id)
	if err != nil {
		return Device{}, err
	}
	list, err := s.scan(ctx, rows)
	if err != nil {
		return Device{}, err
	}
	if len(list) == 0 {
		return Device{}, ErrNotFound
	}
	return list[0], nil
}

// Revoke ends a device for good: the token is marked revoked, its sessions
// are deleted, and its open playback sessions are ended with a message. It
// returns those playback session ids, so the caller can cut their open streams
// now rather than at the next request. Revoking twice is harmless.
func (s *Store) Revoke(ctx context.Context, id int64, by string) ([]string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var userID int64
	if err := tx.QueryRowContext(ctx, `SELECT user_id FROM device_tokens WHERE id = ?`, id).Scan(&userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	now := s.now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE device_tokens SET revoked_at = COALESCE(revoked_at, ?), revoked_by = COALESCE(revoked_by, ?)
		WHERE id = ?`, now, nullStr(by), id); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	open, err := endOpen(ctx, tx, `user_id = ?`, []any{userID}, now, "This device's invite was revoked.", by)
	if err != nil {
		return nil, err
	}
	return open, tx.Commit()
}

// EndExpired ends the open playback sessions of every device whose token has
// expired, and returns them, so a stream that began before the expiry stops
// at it instead of running to the end of the film.
func (s *Store) EndExpired(ctx context.Context) ([]string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now().UnixMilli()
	open, err := endOpen(ctx, tx, `user_id IN (SELECT user_id FROM device_tokens WHERE expires_at <= ?)`, []any{now}, now,
		"This device's invite has expired.", "expiry")
	if err != nil {
		return nil, err
	}
	return open, tx.Commit()
}

func endOpen(ctx context.Context, tx *sql.Tx, where string, args []any, now int64, msg, by string) ([]string, error) {
	ids, err := openSessions(ctx, tx, where, args)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE playback_sessions SET state = 'stopped', ended_at = ?, last_event_at = ?,
			terminated_message = ?, terminated_by = ? WHERE id = ? AND ended_at IS NULL`, now, now, msg, nullStr(by), id); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func openSessions(ctx context.Context, tx *sql.Tx, where string, args []any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM playback_sessions WHERE ended_at IS NULL AND `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// QRPNG draws text as a QR code, as PNG bytes.
func QRPNG(text string) ([]byte, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	c.Scale = 6
	return c.PNG(), nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func str(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	return &n.String
}

func i64(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}
