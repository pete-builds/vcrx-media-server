package federation

import (
	"context"
	"database/sql"
	"errors"
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
)

// Peer is a friend's server this one has linked to. Token is never sent to
// clients.
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
}

// Store keeps the linked peers.
type Store struct {
	DB  *sql.DB
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
	res, err := s.DB.ExecContext(ctx, `INSERT INTO peers (name, base_url, remote_name, token, expires_at, created_by, created_at,
			last_checked_at, last_ok_at, state) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.BaseURL, p.RemoteName, p.Token, p.ExpiresAt, p.CreatedBy, s.now(), s.now(), s.now(), StateOnline)
	if err != nil {
		return Peer{}, err
	}
	id, _ := res.LastInsertId()
	return s.Get(ctx, id)
}

const peerCols = `id, name, base_url, remote_name, token, expires_at, created_by, created_at, last_checked_at, last_ok_at, state, last_error`

func scanPeer(row interface{ Scan(...any) error }) (Peer, error) {
	var p Peer
	err := row.Scan(&p.ID, &p.Name, &p.BaseURL, &p.RemoteName, &p.Token, &p.ExpiresAt, &p.CreatedBy, &p.CreatedAt, &p.LastCheckedAt,
		&p.LastOKAt, &p.State, &p.LastError)
	return p, err
}

// Get returns one peer.
func (s *Store) Get(ctx context.Context, id int64) (Peer, error) {
	p, err := scanPeer(s.DB.QueryRowContext(ctx, `SELECT `+peerCols+` FROM peers WHERE id = ?`, id))
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
		p, err := scanPeer(rows)
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
