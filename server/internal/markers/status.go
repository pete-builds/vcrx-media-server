package markers

import (
	"context"
	"database/sql"
)

// Status is detection's progress over the whole TV library, for admins.
type Status struct {
	SeasonsTotal  int          `json:"seasons_total"`
	SeasonsDone   int          `json:"seasons_done"`
	EpisodesTotal int          `json:"episodes_total"`
	EpisodesDone  int          `json:"episodes_done"`
	Intros        int          `json:"intros"`
	Credits       int          `json:"credits"`
	Errors        int          `json:"errors"`
	Shows         []ShowStatus `json:"shows"`
	RecentErrors  []ErrorEntry `json:"recent_errors"`
}

// ShowStatus is one show's progress. A season is done when every episode
// with a playable file has been looked at in its current form.
type ShowStatus struct {
	ShowID        int64  `json:"show_id"`
	Title         string `json:"title"`
	SeasonsTotal  int    `json:"seasons_total"`
	SeasonsDone   int    `json:"seasons_done"`
	EpisodesTotal int    `json:"episodes_total"`
	EpisodesDone  int    `json:"episodes_done"`
	Intros        int    `json:"intros"`
	Credits       int    `json:"credits"`
	Errors        int    `json:"errors"`
}

// ErrorEntry is an episode whose file could not be read.
type ErrorEntry struct {
	ItemID        int64  `json:"item_id"`
	ShowTitle     string `json:"show_title"`
	SeasonNumber  *int64 `json:"season_number"`
	EpisodeNumber *int64 `json:"episode_number"`
	Error         string `json:"error"`
	CheckedAt     int64  `json:"checked_at"`
}

// Progress reports detection's progress, show by show, with the latest
// errors.
func Progress(ctx context.Context, db *sql.DB) (Status, error) {
	st := Status{Shows: []ShowStatus{}, RecentErrors: []ErrorEntry{}}
	rows, err := db.QueryContext(ctx, `
		WITH eps AS (
			SELECT sh.id AS show_id, sh.title AS title, e.parent_id AS season,
				CASE WHEN NOT `+dueCond+` THEN 1 ELSE 0 END AS done,
				CASE WHEN m.status = 'error' AND NOT `+dueCond+` THEN 1 ELSE 0 END AS failed,
				EXISTS (SELECT 1 FROM item_markers k WHERE k.item_id = e.id AND k.kind = 'intro' AND k.cleared = 0) AS intro,
				EXISTS (SELECT 1 FROM item_markers k WHERE k.item_id = e.id AND k.kind = 'credits' AND k.cleared = 0) AS credits
			FROM items e `+fileJoin+`
			JOIN items se ON se.id = e.parent_id JOIN items sh ON sh.id = se.parent_id
			WHERE e.kind = 'episode' AND e.missing_since IS NULL),
		seasons AS (SELECT show_id, season, MIN(done) AS done FROM eps GROUP BY show_id, season)
		SELECT e.show_id, e.title, COUNT(*), SUM(e.done), SUM(e.intro), SUM(e.credits), SUM(e.failed),
			(SELECT COUNT(*) FROM seasons s WHERE s.show_id = e.show_id),
			(SELECT COUNT(*) FROM seasons s WHERE s.show_id = e.show_id AND s.done = 1)
		FROM eps e GROUP BY e.show_id, e.title ORDER BY e.title COLLATE NOCASE`)
	if err != nil {
		return st, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var s ShowStatus
		if err := rows.Scan(&s.ShowID, &s.Title, &s.EpisodesTotal, &s.EpisodesDone, &s.Intros, &s.Credits, &s.Errors,
			&s.SeasonsTotal, &s.SeasonsDone); err != nil {
			return st, err
		}
		st.Shows = append(st.Shows, s)
		st.SeasonsTotal += s.SeasonsTotal
		st.SeasonsDone += s.SeasonsDone
		st.EpisodesTotal += s.EpisodesTotal
		st.EpisodesDone += s.EpisodesDone
		st.Intros += s.Intros
		st.Credits += s.Credits
		st.Errors += s.Errors
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	erows, err := db.QueryContext(ctx, `SELECT m.item_id, sh.title, se.season_number, e.episode_number, IFNULL(m.error, ''), m.checked_at
		FROM marker_scans m JOIN items e ON e.id = m.item_id JOIN items se ON se.id = e.parent_id JOIN items sh ON sh.id = se.parent_id
		WHERE m.status = 'error' ORDER BY m.checked_at DESC LIMIT 20`)
	if err != nil {
		return st, err
	}
	defer func() { _ = erows.Close() }()
	for erows.Next() {
		var e ErrorEntry
		var sn, en sql.NullInt64
		if err := erows.Scan(&e.ItemID, &e.ShowTitle, &sn, &en, &e.Error, &e.CheckedAt); err != nil {
			return st, err
		}
		if sn.Valid {
			e.SeasonNumber = &sn.Int64
		}
		if en.Valid {
			e.EpisodeNumber = &en.Int64
		}
		st.RecentErrors = append(st.RecentErrors, e)
	}
	return st, erows.Err()
}
