// Package loans lends a tape (milestone 10, item 1). The owner picks a movie
// and prints a card whose QR code is an invite (package invites) scoped to that
// one title for a day to a week. While the loan is out, nobody else can stream
// the owner's copy, the owner included; when it ends, early or on its due date,
// the borrower's device is told "Be kind, rewind." and the title is back on
// the shelf.
//
// The rules live in the one policy path: policy.Load marks a borrower's device
// and the one title it may see, and policy.LoanCond is the block every way of
// playing a file applies. This package writes and ends the loans that those
// read. It streams only, like every invited device: the borrower never gets a
// whole file (milestone 9's no-copy line, enforced by api.streamOnly).
package loans

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/invites"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// How long a loan lasts: three days unless the owner chooses a day to a week.
const (
	MinHours     = 24
	MaxHours     = 7 * 24
	DefaultHours = 72
)

// The cap a borrower plays at unless the owner allows 4K.
const defaultMaxResolution = "1080p"

var (
	ErrNotFound = errors.New("no such loan")
	// ErrOut is a second loan of a copy that is already out.
	ErrOut = errors.New("this tape is already out on loan: return it first")
	// ErrNotLendable is anything but a movie that is on disk.
	ErrNotLendable = errors.New("only a movie that is on disk can be lent")
)

// Loan is one tape lent out, as Activity lists it.
type Loan struct {
	ID       int64  `json:"id"`
	ItemID   *int64 `json:"item_id"`
	TokenID  *int64 `json:"token_id"`
	Title    string `json:"title"`
	Borrower string `json:"borrower"`
	// State is "out", "returned" (early, by an admin or the borrower) or
	// "expired" (it ran to its due date).
	State      string  `json:"state"`
	LentBy     *string `json:"lent_by"`
	CreatedAt  int64   `json:"created_at"`
	DueAt      int64   `json:"due_at"`
	EndedAt    *int64  `json:"ended_at"`
	EndedBy    *string `json:"ended_by"`
	RedeemedAt *int64  `json:"redeemed_at"` // when the card was scanned, if it was
	DeviceName *string `json:"device_name"`
	LastSeenAt *int64  `json:"last_seen_at"`
	Summary    string  `json:"summary"`
}

// Request is what the owner chooses when lending.
type Request struct {
	ItemID   int64
	Borrower string
	Hours    int
	// Allow4K lifts the 1080p cap; AllowTranscode lets the server convert
	// files the borrower's device cannot play as they are.
	Allow4K        bool
	AllowTranscode bool
}

type Store struct {
	DB      *sql.DB
	Invites *invites.Store
	Now     func() time.Time
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// RewindMessage is what a borrower's device shows once the loan is over.
func RewindMessage(title string) string {
	return "Be kind, rewind. “" + title + "” is back on its owner's shelf."
}

// OnLoanMessage is what anyone else is told when they try to play a lent copy.
func OnLoanMessage(due time.Time) string {
	return "Out on loan, due back " + due.Format("Mon 2 Jan at 15:04") + "."
}

// Lend makes the invite and the loan in one transaction and returns the loan
// and the invite's secret, which is shown once and never kept. Lending ends
// every open stream of the title (the tape is out); their playback session ids
// come back so the caller can cut the connections too.
func (s *Store) Lend(ctx context.Context, by string, req Request) (Loan, string, []string, error) {
	if req.Hours == 0 {
		req.Hours = DefaultHours
	}
	if req.Hours < MinHours || req.Hours > MaxHours {
		return Loan{}, "", nil, fmt.Errorf("a loan lasts %d hours to %d days", MinHours, MaxHours/24)
	}
	var kind, title string
	var lib int64
	var missing sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `SELECT kind, title, library_id, missing_since FROM items WHERE id = ?`, req.ItemID).
		Scan(&kind, &title, &lib, &missing)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (kind != "movie" || missing.Valid)) {
		return Loan{}, "", nil, ErrNotLendable
	}
	if err != nil {
		return Loan{}, "", nil, err
	}
	// One clock reading for both ends, so the loan lasts exactly what was asked.
	start := s.now()
	sc := invites.Scope{LibraryIDs: []int64{lib}, MaxResolution: defaultMaxResolution, AllowTranscode: req.AllowTranscode,
		ExpiresAt: start.Add(time.Duration(req.Hours) * time.Hour).UnixMilli()}
	if req.Allow4K {
		sc.MaxResolution = ""
	}
	if err := s.Invites.Validate(ctx, &sc); err != nil {
		return Loan{}, "", nil, err
	}
	// The borrower must be able to play something, or the card opens an
	// empty shelf: a movie held only in 4K needs 4K allowed.
	vc, vargs := policy.Policy{MaxResolution: sc.MaxResolution}.VersionCond("v")
	var playable int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM media_versions v WHERE v.item_id = ? AND `+vc,
		append([]any{req.ItemID}, vargs...)...).Scan(&playable); err != nil {
		return Loan{}, "", nil, err
	}
	if playable == 0 {
		return Loan{}, "", nil, errors.New("this movie has no copy at 1080p or below: allow 4K to lend it")
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Loan{}, "", nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := start.UnixMilli()
	// A loan of this copy past its due date has ended, whether or not the
	// sweep has written that down yet.
	if _, err := tx.ExecContext(ctx, `UPDATE loans SET ended_at = due_at, end_reason = 'expired', ended_by = 'expiry'
		WHERE item_id = ? AND ended_at IS NULL AND due_at <= ?`, req.ItemID, now); err != nil {
		return Loan{}, "", nil, err
	}
	var out int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM loans WHERE item_id = ? AND ended_at IS NULL`, req.ItemID).Scan(&out); err != nil {
		return Loan{}, "", nil, err
	}
	if out > 0 {
		return Loan{}, "", nil, ErrOut
	}
	tokenID, userID, secret, err := s.Invites.MintIn(ctx, tx, by, req.Borrower, sc)
	if err != nil {
		return Loan{}, "", nil, err
	}
	name, _ := invites.CleanName(req.Borrower)
	res, err := tx.ExecContext(ctx, `INSERT INTO loans (item_id, token_id, borrower_user_id, title, borrower, lent_by, created_at, due_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, req.ItemID, tokenID, userID, title, name, nullStr(by), now, sc.ExpiresAt)
	if err != nil {
		// The unique index is the last word on one loan per copy, should two
		// admins lend the same tape at the same moment.
		if strings.Contains(err.Error(), "UNIQUE") {
			return Loan{}, "", nil, ErrOut
		}
		return Loan{}, "", nil, err
	}
	id, _ := res.LastInsertId()
	cut, err := endOpen(ctx, tx, `item_id = ?`, []any{req.ItemID}, now, OnLoanMessage(time.UnixMilli(sc.ExpiresAt)), "loan")
	if err != nil {
		return Loan{}, "", nil, err
	}
	if err := tx.Commit(); err != nil {
		return Loan{}, "", nil, err
	}
	l, err := s.Get(ctx, id)
	return l, secret, cut, err
}

// Return ends a loan early: the tape is back on the shelf at once. The
// borrower's token is revoked and its sessions signed out, and its open
// streams end with "Be kind, rewind."; their ids come back for the caller to
// cut. Returning a loan that already ended changes nothing.
func (s *Store) Return(ctx context.Context, id int64, by string) ([]string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var tokenID, userID, ended sql.NullInt64
	var title string
	err = tx.QueryRowContext(ctx, `SELECT token_id, borrower_user_id, ended_at, title FROM loans WHERE id = ?`, id).
		Scan(&tokenID, &userID, &ended, &title)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if ended.Valid {
		return nil, nil
	}
	now := s.now().UnixMilli()
	reason := "returned"
	if _, err := tx.ExecContext(ctx, `UPDATE loans SET ended_at = ?, end_reason = ?, ended_by = ? WHERE id = ?`, now, reason, nullStr(by), id); err != nil {
		return nil, err
	}
	if tokenID.Valid {
		if _, err := tx.ExecContext(ctx, `UPDATE device_tokens SET revoked_at = COALESCE(revoked_at, ?), revoked_by = COALESCE(revoked_by, ?)
			WHERE id = ?`, now, nullStr(by), tokenID.Int64); err != nil {
			return nil, err
		}
	}
	var cut []string
	if userID.Valid {
		if _, err := tx.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id = ?`, userID.Int64); err != nil {
			return nil, err
		}
		if cut, err = endOpen(ctx, tx, `user_id = ?`, []any{userID.Int64}, now, RewindMessage(title), by); err != nil {
			return nil, err
		}
	}
	return cut, tx.Commit()
}

// ForToken is the loan an invited device came from, if it came from one.
func (s *Store) ForToken(ctx context.Context, tokenID int64) (Loan, bool, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM loans WHERE token_id = ?`, tokenID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Loan{}, false, nil
	}
	if err != nil {
		return Loan{}, false, err
	}
	l, err := s.Get(ctx, id)
	return l, err == nil, err
}

// SweepExpired writes down the loans that reached their due date and ends
// their borrowers' open streams with "Be kind, rewind.", returning those
// playback session ids so the caller cuts the connections. The block on the
// owner's copy already lifted at the due date (policy.LoanCond compares it);
// this stops a film the borrower was still watching.
func (s *Store) SweepExpired(ctx context.Context) ([]string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := s.now().UnixMilli()
	list, err := dueLoans(ctx, tx, now)
	if err != nil {
		return nil, err
	}
	var cut []string
	for _, d := range list {
		if _, err := tx.ExecContext(ctx, `UPDATE loans SET ended_at = ?, end_reason = 'expired', ended_by = 'expiry' WHERE id = ?`, d.due, d.id); err != nil {
			return nil, err
		}
		if !d.user.Valid {
			continue
		}
		ids, err := endOpen(ctx, tx, `user_id = ?`, []any{d.user.Int64}, now, RewindMessage(d.title), "expiry")
		if err != nil {
			return nil, err
		}
		cut = append(cut, ids...)
	}
	return cut, tx.Commit()
}

type dueLoan struct {
	id, due int64
	user    sql.NullInt64
	title   string
}

// dueLoans are the loans still marked out whose due date has passed.
func dueLoans(ctx context.Context, tx *sql.Tx, now int64) ([]dueLoan, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, borrower_user_id, title, due_at FROM loans WHERE ended_at IS NULL AND due_at <= ?`, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var list []dueLoan
	for rows.Next() {
		var d dueLoan
		if err := rows.Scan(&d.id, &d.user, &d.title, &d.due); err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// RememberSession notes a borrower's sign-in session, so that after the loan
// its next request can be answered with "Be kind, rewind." It does nothing for
// a device that is not a borrower.
func (s *Store) RememberSession(ctx context.Context, tokenID int64, sessionToken string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT OR IGNORE INTO loan_sessions (token_hash, loan_id) SELECT ?, id FROM loans WHERE token_id = ?`,
		hashToken(sessionToken), tokenID)
	return err
}

// Ended is the loan behind a borrower's session token once that loan is over:
// returned, or past its due date. It is false for any other token, and for a
// loan still out.
func (s *Store) Ended(ctx context.Context, sessionToken string) (Loan, bool, error) {
	if sessionToken == "" || len(sessionToken) > 256 {
		return Loan{}, false, nil
	}
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT l.id FROM loan_sessions ls JOIN loans l ON l.id = ls.loan_id
		WHERE ls.token_hash = ? AND (l.ended_at IS NOT NULL OR l.due_at <= ?)`, hashToken(sessionToken), s.now().UnixMilli()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Loan{}, false, nil
	}
	if err != nil {
		return Loan{}, false, err
	}
	l, err := s.Get(ctx, id)
	return l, err == nil, err
}

const loanCols = `l.id, l.item_id, l.token_id, l.title, l.borrower, l.lent_by, l.created_at, l.due_at, l.ended_at, l.end_reason, l.ended_by,
	dt.redeemed_at, dt.device_name, dt.last_seen_at, p.max_resolution, COALESCE(p.allow_transcode, 0)`

const loanFrom = ` FROM loans l LEFT JOIN device_tokens dt ON dt.id = l.token_id LEFT JOIN user_policies p ON p.user_id = l.borrower_user_id`

func (s *Store) scan(rows *sql.Rows) ([]Loan, error) {
	defer func() { _ = rows.Close() }()
	now := s.now().UnixMilli()
	out := []Loan{}
	for rows.Next() {
		var l Loan
		var item, token, ended, redeemed, seen sql.NullInt64
		var lentBy, reason, endedBy, device, maxRes sql.NullString
		var transcode bool
		if err := rows.Scan(&l.ID, &item, &token, &l.Title, &l.Borrower, &lentBy, &l.CreatedAt, &l.DueAt, &ended, &reason, &endedBy,
			&redeemed, &device, &seen, &maxRes, &transcode); err != nil {
			return nil, err
		}
		l.ItemID, l.TokenID, l.EndedAt, l.RedeemedAt, l.LastSeenAt = i64(item), i64(token), i64(ended), i64(redeemed), i64(seen)
		l.LentBy, l.EndedBy, l.DeviceName = str(lentBy), str(endedBy), str(device)
		switch {
		case reason.Valid:
			l.State = reason.String
		case now >= l.DueAt:
			// Due, and the sweep has not written it down yet.
			l.State, l.EndedAt = "expired", &l.DueAt
		default:
			l.State = "out"
		}
		l.Summary = "Up to " + maxRes.String
		if !maxRes.Valid {
			l.Summary = "Any resolution, 4K included"
		}
		if transcode {
			l.Summary += ", may convert."
		} else {
			l.Summary += ", direct play only."
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// List returns loans, newest first.
func (s *Store) List(ctx context.Context, limit int) ([]Loan, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+loanCols+loanFrom+` ORDER BY l.created_at DESC, l.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return s.scan(rows)
}

func (s *Store) Get(ctx context.Context, id int64) (Loan, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+loanCols+loanFrom+` WHERE l.id = ?`, id)
	if err != nil {
		return Loan{}, err
	}
	list, err := s.scan(rows)
	if err != nil {
		return Loan{}, err
	}
	if len(list) == 0 {
		return Loan{}, ErrNotFound
	}
	return list[0], nil
}

// endOpen ends the open playback sessions matching where with a message for
// the viewer, and returns their ids.
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
