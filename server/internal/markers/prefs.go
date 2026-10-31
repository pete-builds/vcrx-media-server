package markers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// What a person wants done with intros. Credits always get a button and are
// never skipped without a press: a scene after the credits that detection
// missed must not be skipped for anyone.
const (
	ModeButton = "button"
	ModeAuto   = "auto"
	ModeOff    = "off"
)

// AutoSkipConfidence is the least confidence at which "skip automatically"
// acts. Below it the button shows instead. Markers from a file or an admin
// have confidence 1; a theme heard in every neighbouring episode scores
// about 0.8 to 0.9; one heard in half of them about 0.45.
const AutoSkipConfidence = 0.75

// ShowIntroConfidence is the least confidence at which a detected intro gets
// a button at all. A theme heard in only half the neighbouring episodes is as
// likely a studio logo as an intro (a 0.42 on Adolescence was one), so below
// this the player gets nothing. Credits keep their button at any confidence:
// a press is needed, and the fallback from black or silence scores 0.3.
const ShowIntroConfidence = 0.5

// ValidMode reports whether m is button, auto or off.
func ValidMode(m string) bool { return m == ModeButton || m == ModeAuto || m == ModeOff }

// SkipMode is the user's choice; button when unset.
func SkipMode(ctx context.Context, db *sql.DB, userID int64) (string, error) {
	var m string
	err := db.QueryRowContext(ctx, `SELECT skip_markers FROM users WHERE id = ?`, userID).Scan(&m)
	if errors.Is(err, sql.ErrNoRows) {
		return ModeButton, nil
	}
	return m, err
}

// SetSkipMode stores the user's choice.
func SetSkipMode(ctx context.Context, db *sql.DB, userID int64, mode string) error {
	if !ValidMode(mode) {
		return fmt.Errorf("%w: skip_markers must be button, auto or off", ErrBadMarker)
	}
	_, err := db.ExecContext(ctx, `UPDATE users SET skip_markers = ? WHERE id = ?`, mode, userID)
	return err
}

// PlayerMarker is a marker as a player receives it: AutoSkip says to skip it
// without asking.
type PlayerMarker struct {
	Marker
	AutoSkip bool `json:"auto_skip"`
}

// ForPlayer marks the markers a player may skip on its own under mode: only
// intros, only with "skip automatically" chosen, only when sure. It drops an
// intro below ShowIntroConfidence.
func ForPlayer(ms []Marker, mode string) []PlayerMarker {
	out := make([]PlayerMarker, 0, len(ms))
	for _, m := range ms {
		if m.Kind == Intro && m.Confidence < ShowIntroConfidence {
			continue
		}
		out = append(out, PlayerMarker{Marker: m, AutoSkip: mode == ModeAuto && m.Kind == Intro && m.Confidence >= AutoSkipConfidence})
	}
	return out
}
