package markers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Kinds of marker.
const (
	Intro   = "intro"
	Credits = "credits"
)

// Sources. A marker in the file's NFO beats a chapter title, which beats
// detection; an admin's own marker (or clearing one) beats everything and is
// never replaced.
const (
	SourceDetected = "detected"
	SourceChapter  = "chapter"
	SourceNFO      = "nfo"
	SourceManual   = "manual"
)

// ErrBadMarker is a marker an admin cannot set: unknown kind, or times that
// do not make a stretch inside the file.
var ErrBadMarker = errors.New("bad marker")

// Marker is one stretch of an episode a player may offer to skip.
type Marker struct {
	Kind    string `json:"kind"`
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	// Confidence is 1 for markers from a file or an admin, and 0 to 1 for
	// detected ones.
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
}

// ValidKind reports whether k is intro or credits.
func ValidKind(k string) bool { return k == Intro || k == Credits }

// ForItem lists an item's markers, intro first. It never returns nil, so the
// API always sends a list. The caller has already checked the user may see
// the item.
func ForItem(ctx context.Context, db *sql.DB, itemID int64) ([]Marker, error) {
	rows, err := db.QueryContext(ctx, `SELECT kind, start_ms, end_ms, confidence, source FROM item_markers
		WHERE item_id = ? AND cleared = 0 ORDER BY kind = 'credits', start_ms`, itemID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Marker{}
	for rows.Next() {
		var m Marker
		if err := rows.Scan(&m.Kind, &m.StartMs, &m.EndMs, &m.Confidence, &m.Source); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetManual stores an admin's marker, which detection never replaces. The
// item must be a movie or an episode; the times must fit its file when its
// length is known.
func SetManual(ctx context.Context, db *sql.DB, itemID int64, kind string, startMs, endMs int64) error {
	if !ValidKind(kind) {
		return fmt.Errorf("%w: kind must be intro or credits", ErrBadMarker)
	}
	if startMs < 0 || endMs <= startMs {
		return fmt.Errorf("%w: end_ms must be after start_ms, and start_ms at least 0", ErrBadMarker)
	}
	dur, err := playableDuration(ctx, db, itemID)
	if err != nil {
		return err
	}
	if dur > 0 && endMs > dur+1000 {
		return fmt.Errorf("%w: end_ms is past the end of the file (%d ms)", ErrBadMarker, dur)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO item_markers (item_id, kind, start_ms, end_ms, confidence, source, cleared, updated_at)
		VALUES (?, ?, ?, ?, 1, 'manual', 0, ?)
		ON CONFLICT (item_id, kind) DO UPDATE SET start_ms = excluded.start_ms, end_ms = excluded.end_ms,
			confidence = 1, source = 'manual', cleared = 0, updated_at = excluded.updated_at`,
		itemID, kind, startMs, endMs, time.Now().UnixMilli())
	return err
}

// Clear removes a marker for good: an admin's "there is no intro here", which
// detection respects.
func Clear(ctx context.Context, db *sql.DB, itemID int64, kind string) error {
	if !ValidKind(kind) {
		return fmt.Errorf("%w: kind must be intro or credits", ErrBadMarker)
	}
	if _, err := playableDuration(ctx, db, itemID); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `INSERT INTO item_markers (item_id, kind, start_ms, end_ms, confidence, source, cleared, updated_at)
		VALUES (?, ?, 0, 0, 1, 'manual', 1, ?)
		ON CONFLICT (item_id, kind) DO UPDATE SET start_ms = 0, end_ms = 0, confidence = 1, source = 'manual', cleared = 1,
			updated_at = excluded.updated_at`, itemID, kind, time.Now().UnixMilli())
	return err
}

// ErrNotPlayable is an item that is not a movie or an episode, or does not exist.
var ErrNotPlayable = errors.New("not a movie or an episode")

// playableDuration is the longest version's length, 0 when unknown.
func playableDuration(ctx context.Context, db *sql.DB, itemID int64) (int64, error) {
	var kind string
	var dur sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT i.kind, (SELECT MAX(duration_ms) FROM media_versions v WHERE v.item_id = i.id)
		FROM items i WHERE i.id = ?`, itemID).Scan(&kind, &dur)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && kind != "movie" && kind != "episode") {
		return 0, ErrNotPlayable
	}
	return dur.Int64, err
}

// apply records what a pass worked out for one kind of one episode: m (the
// best of NFO, chapter and detection), or nothing (nil), which removes a
// marker an earlier pass stored. An admin's marker is never touched.
func apply(ctx context.Context, tx *sql.Tx, itemID int64, kind string, m *Marker, now int64) error {
	var cur string
	err := tx.QueryRowContext(ctx, `SELECT source FROM item_markers WHERE item_id = ? AND kind = ?`, itemID, kind).Scan(&cur)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		cur = ""
	case err != nil:
		return err
	}
	if cur == SourceManual {
		return nil
	}
	if m == nil {
		if cur == "" {
			return nil
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM item_markers WHERE item_id = ? AND kind = ? AND source != 'manual'`, itemID, kind)
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO item_markers (item_id, kind, start_ms, end_ms, confidence, source, cleared, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?)
		ON CONFLICT (item_id, kind) DO UPDATE SET start_ms = excluded.start_ms, end_ms = excluded.end_ms,
			confidence = excluded.confidence, source = excluded.source, cleared = 0, updated_at = excluded.updated_at`,
		itemID, kind, m.StartMs, m.EndMs, m.Confidence, m.Source, now)
	return err
}
