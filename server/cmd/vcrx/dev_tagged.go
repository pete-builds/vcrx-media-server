//go:build vcrxdev

package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// Development builds only (go build -tags vcrxdev, which scripts/dev-server.sh
// uses). Neither the Dockerfile nor CI's release image passes the tag, so none
// of this exists in a server anyone runs for real.

// devHomeDate pins the date Home's seasonal rows and daily rotation go by to
// VCRX_DEV_HOME_DATE (YYYY-MM-DD, local), so the web smoke test sees the
// Halloween row whatever day it runs.
func devHomeDate(log *slog.Logger) func() time.Time {
	v := os.Getenv("VCRX_DEV_HOME_DATE")
	if v == "" {
		return nil
	}
	d, err := time.ParseInLocation("2006-01-02", v, time.Local)
	if err != nil {
		log.Error("VCRX_DEV_HOME_DATE is not YYYY-MM-DD; using the real date", "value", v)
		return nil
	}
	log.Warn("development build: Home's date is pinned", "date", v)
	noon := d.Add(12 * time.Hour)
	return func() time.Time { return noon }
}

// devCommand runs "vcrx dev-seed": a play history for the sample library, so
// Home's Trending, Most Watched and Because You Watched rows have something
// to show. It writes once; a second run changes nothing.
func devCommand(name string, _ []string) (bool, error) {
	if name != "dev-seed" {
		return false, nil
	}
	ctx := context.Background()
	a, err := open(ctx)
	if err != nil {
		return true, err
	}
	defer func() { _ = a.db.Close() }()
	return true, devSeed(ctx, a.db, time.Now())
}

func devSeed(ctx context.Context, conn *sql.DB, now time.Time) error {
	var done int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings WHERE key = 'dev_seed'`).Scan(&done); err != nil || done > 0 {
		return err
	}
	var uid int64
	var uname string
	if err := conn.QueryRowContext(ctx, `SELECT id, username FROM users ORDER BY id LIMIT 1`).Scan(&uid, &uname); err != nil {
		return fmt.Errorf("dev-seed needs a user: %w", err)
	}
	find := func(title string) (int64, string, bool) {
		var id int64
		var kind string
		err := conn.QueryRowContext(ctx, `SELECT id, kind FROM items WHERE title = ? AND kind IN ('movie', 'episode') ORDER BY id LIMIT 1`, title).Scan(&id, &kind)
		return id, kind, err == nil
	}
	day := 24 * time.Hour
	plays := []struct {
		title string
		n     int
		ago   time.Duration
	}{
		{"Harbor Lights", 5, 1 * day}, {"Hollow Pines", 4, 2 * day}, {"Paper Trail", 3, 3 * day},
		{"Grave Weather", 3, 4 * day}, {"Starlight Diner", 2, 5 * day}, {"The Lantern Hour", 2, 6 * day},
		{"Copper Valley", 6, 60 * day}, {"Iron Meridian", 4, 90 * day}, {"Twin Rivers", 2, 120 * day},
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	n := 0
	for _, p := range plays {
		id, kind, ok := find(p.title)
		if !ok {
			continue
		}
		for k := range p.n {
			at := now.Add(-p.ago - time.Duration(k)*time.Hour).UnixMilli()
			if _, err := tx.ExecContext(ctx, `INSERT INTO playback_sessions (id, user_id, item_id, username, item_title, item_kind,
					decision, decision_reason, state, played_ms, duration_ms, last_position_ms, started_at, last_event_at, ended_at)
				VALUES (?, ?, ?, ?, ?, ?, 'direct_play', 'dev-seed', 'stopped', 2400000, 2400000, 2300000, ?, ?, ?)`,
				"dev-seed-"+strconv.Itoa(n), uid, id, uname, p.title, kind, at, at+2400000, at+2400000); err != nil {
				return err
			}
			n++
		}
	}
	// Finished yesterday, for Because You Watched; finished long ago, for Rediscover.
	for title, ago := range map[string]time.Duration{"Harbor Lights": day, "Paper Trail": 400 * day} {
		id, _, ok := find(title)
		if !ok {
			continue
		}
		at := now.Add(-ago).UnixMilli()
		if _, err := tx.ExecContext(ctx, `INSERT INTO watch_state (user_id, item_id, position_ms, played, play_count, last_played_at, updated_at)
			VALUES (?, ?, 0, 1, 1, ?, ?) ON CONFLICT (user_id, item_id) DO NOTHING`, uid, id, at, at); err != nil {
			return err
		}
	}
	if n == 0 {
		return errors.New("dev-seed found none of the sample titles; scan the libraries first")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES ('dev_seed', ?, ?)`, strconv.Itoa(n), now.UnixMilli()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	fmt.Printf("dev-seed: %d plays\n", n)
	return nil
}
