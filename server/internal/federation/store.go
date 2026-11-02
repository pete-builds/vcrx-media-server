package federation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is a peer id this server has no link for.
var ErrNotFound = errors.New("no such friend's server")

// Peer states, as the admin's list and Home show them.
const (
	StateUnknown = "unknown"
	StateOnline  = "online"
	StateOffline = "offline"
	// StateBadAnswer: the peer answered, but with something this server
	// refuses (too large, not JSON, the wrong shape).
	StateBadAnswer = "bad_answer"
	// StateUnlinked: the peer no longer accepts this server's token. Its
	// admin revoked the link, or it expired.
	StateUnlinked = "unlinked"
	// StateTokenUnreadable: this server cannot open its own sealed token for
	// the peer (the key file is missing or is not the one it was sealed
	// with). The row is kept; the link is unusable until the key is restored
	// or the peer is unlinked and linked again. It is never stored, only
	// reported.
	StateTokenUnreadable = "token_unreadable"
)

// Peer is a friend's server this one has linked to. Token is never sent to
// clients. The store keeps it sealed (tokenkey.go); a Peer always carries it
// opened, or empty with State StateTokenUnreadable and TokenErr saying why.
type Peer struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	BaseURL       string  `json:"address"`
	RemoteName    *string `json:"remote_name"`
	Token         string  `json:"-"`
	ExpiresAt     int64   `json:"expires_at"`
	CreatedBy     *string `json:"created_by"`
	CreatedAt     int64   `json:"created_at"`
	LastCheckedAt *int64  `json:"last_checked_at"`
	LastOKAt      *int64  `json:"last_ok_at"`
	State         string  `json:"state"`
	LastError     *string `json:"last_error"`
	TokenErr      error   `json:"-"`
}

// Store keeps the linked peers. Key seals their tokens at rest.
type Store struct {
	DB  *sql.DB
	Key *TokenKey
	Now func() time.Time
}

func (s *Store) now() int64 {
	if s.Now != nil {
		return s.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

// Add records a link after the peer accepted this server's invite.
func (s *Store) Add(ctx context.Context, p Peer) (Peer, error) {
	sealed, err := s.Key.Seal(p.Token, p.BaseURL)
	if err != nil {
		return Peer{}, fmt.Errorf("sealing the friend's server token: %w", err)
	}
	res, err := s.DB.ExecContext(ctx, `INSERT INTO peers (name, base_url, remote_name, token, expires_at, created_by, created_at,
			last_checked_at, last_ok_at, state) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.BaseURL, p.RemoteName, sealed, p.ExpiresAt, p.CreatedBy, s.now(), s.now(), s.now(), StateOnline)
	if err != nil {
		return Peer{}, err
	}
	id, _ := res.LastInsertId()
	return s.Get(ctx, id)
}

const peerCols = `id, name, base_url, remote_name, token, expires_at, created_by, created_at, last_checked_at, last_ok_at, state, last_error`

func (s *Store) scanPeer(row interface{ Scan(...any) error }) (Peer, error) {
	var p Peer
	err := row.Scan(&p.ID, &p.Name, &p.BaseURL, &p.RemoteName, &p.Token, &p.ExpiresAt, &p.CreatedBy, &p.CreatedAt, &p.LastCheckedAt,
		&p.LastOKAt, &p.State, &p.LastError)
	if err != nil {
		return p, err
	}
	tok, terr := s.Key.Open(p.Token, p.BaseURL)
	if terr != nil {
		msg := TokenProblem(terr)
		p.Token, p.State, p.LastError, p.TokenErr = "", StateTokenUnreadable, &msg, terr
		return p, nil
	}
	p.Token = tok
	return p, nil
}

// TokenProblem says, for an admin, why a peer's token cannot be opened and
// what to do about it.
func TokenProblem(err error) string {
	if errors.Is(err, ErrKeyMissing) {
		return "This server's key file for friends' server links (" + TokenKeyFile + " in the config folder) is missing, " +
			"so this link cannot be used. Restore the file, or unlink this server and link it again."
	}
	return "This link's token cannot be decrypted with this server's key file for friends' server links (" + TokenKeyFile +
		" in the config folder). Restore the key it was sealed with, or unlink this server and link it again."
}

// SealLegacy seals every token stored before tokens were sealed, and returns
// how many it sealed. Each row is rewritten only if its token is unchanged,
// so nothing is lost to a concurrent change; one that fails is left as it
// is, still usable, and reported.
func (s *Store) SealLegacy(ctx context.Context) (int, error) {
	todo, err := s.legacyTokens(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, l := range todo {
		sealed, err := s.Key.Seal(l.tok, l.base)
		if err != nil {
			errs = append(errs, fmt.Errorf("peer %d: %w", l.id, err))
			continue
		}
		res, err := s.DB.ExecContext(ctx, `UPDATE peers SET token = ? WHERE id = ? AND token = ?`, sealed, l.id, l.tok)
		if err != nil {
			errs = append(errs, fmt.Errorf("peer %d: %w", l.id, err))
			continue
		}
		if c, _ := res.RowsAffected(); c == 1 {
			n++
		}
	}
	return n, errors.Join(errs...)
}

type legacyToken struct {
	id        int64
	base, tok string
}

// legacyTokens are the rows whose token is not sealed yet.
func (s *Store) legacyTokens(ctx context.Context) ([]legacyToken, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, base_url, token FROM peers WHERE token NOT LIKE ?`, sealedPrefix+"%")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []legacyToken
	for rows.Next() {
		var l legacyToken
		if err := rows.Scan(&l.id, &l.base, &l.tok); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Get returns one peer.
func (s *Store) Get(ctx context.Context, id int64) (Peer, error) {
	p, err := s.scanPeer(s.DB.QueryRowContext(ctx, `SELECT `+peerCols+` FROM peers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Peer{}, ErrNotFound
	}
	return p, err
}

// List returns every linked peer, oldest link first.
func (s *Store) List(ctx context.Context) ([]Peer, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+peerCols+` FROM peers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Peer{}
	for rows.Next() {
		p, err := s.scanPeer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Delete forgets a link, token and all.
func (s *Store) Delete(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM peers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetState records how the peer answered last.
func (s *Store) SetState(ctx context.Context, id int64, state, lastErr string) {
	now := s.now()
	var ok, msg any
	if state == StateOnline {
		ok = now
	}
	if lastErr != "" {
		msg = lastErr
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE peers SET state = ?, last_error = ?, last_checked_at = ?, last_ok_at = COALESCE(?, last_ok_at)
		WHERE id = ?`, state, msg, now, ok, id)
}
