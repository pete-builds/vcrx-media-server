// Package admin holds the management operations shared by the admin API and the
// vcrx CLI: libraries, users, grants, policies, scans and activity history.
package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pete-builds/vcrx-media-server/server/internal/config"
	"github.com/pete-builds/vcrx-media-server/server/internal/db"
	"github.com/pete-builds/vcrx-media-server/server/internal/fsafe"
	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

var (
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("already exists")
	ErrScanRunning = errors.New("a scan of this library is already running")
)

type Admin struct {
	DB         *sql.DB
	MediaRoots []string
}

type ScanRun struct {
	ID           int64   `json:"id"`
	LibraryID    int64   `json:"library_id"`
	Trigger      string  `json:"trigger"`
	PathFilter   *string `json:"path_filter"`
	Status       string  `json:"status"`
	Error        *string `json:"error"`
	FilesSeen    int     `json:"files_seen"`
	FilesAdded   int     `json:"files_added"`
	FilesChanged int     `json:"files_changed"`
	FilesMissing int     `json:"files_missing"`
	FilesFailed  int     `json:"files_failed"`
	StartedAt    int64   `json:"started_at"`
	FinishedAt   *int64  `json:"finished_at"`
}

type Library struct {
	ID                  int64    `json:"id"`
	Name                string   `json:"name"`
	Kind                string   `json:"kind"`
	ItemCount           int      `json:"item_count"`
	RootPath            string   `json:"root_path"`
	Grouping            string   `json:"grouping"`
	ScanIntervalMinutes int      `json:"scan_interval_minutes"`
	LastScan            *ScanRun `json:"last_scan"`
}

type LibraryInput struct {
	Name                string `json:"name"`
	Kind                string `json:"kind"`
	RootPath            string `json:"root_path"`
	Grouping            string `json:"grouping"`
	ScanIntervalMinutes int    `json:"scan_interval_minutes"`
}

// ValidateRoot requires an existing directory under one of the configured media
// roots, symlinks resolved.
func (a *Admin) ValidateRoot(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("root path %q must be absolute", root)
	}
	root = filepath.Clean(root)
	for _, mr := range a.MediaRoots {
		if _, err := fsafe.ResolveUnder(mr, root); err == nil {
			fi, err := os.Stat(root)
			if err != nil {
				return "", fmt.Errorf("root path %s: %w", root, err)
			}
			if !fi.IsDir() {
				return "", fmt.Errorf("root path %s is not a directory", root)
			}
			return root, nil
		}
	}
	return "", fmt.Errorf("root path %s is not under a configured media root (%s)", root, strings.Join(a.MediaRoots, ", "))
}

func (in *LibraryInput) normalize() error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 100 {
		return errors.New("library name must be 1 to 100 characters")
	}
	if in.Kind != "movie" && in.Kind != "show" {
		return errors.New("kind must be movie or show")
	}
	if in.Grouping == "" {
		in.Grouping = "folder"
	}
	if in.Grouping != "folder" && in.Grouping != "title_year" {
		return errors.New("grouping must be folder or title_year")
	}
	if in.ScanIntervalMinutes == 0 {
		in.ScanIntervalMinutes = 60
	}
	if in.ScanIntervalMinutes < 5 {
		return errors.New("scan interval must be at least 5 minutes")
	}
	return nil
}

func (a *Admin) CreateLibrary(ctx context.Context, in LibraryInput) (Library, error) {
	if err := in.normalize(); err != nil {
		return Library{}, err
	}
	root, err := a.ValidateRoot(in.RootPath)
	if err != nil {
		return Library{}, err
	}
	now := db.NowMs()
	res, err := a.DB.ExecContext(ctx, `INSERT INTO libraries (name, kind, root_path, grouping, scan_interval_minutes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, in.Name, in.Kind, root, in.Grouping, in.ScanIntervalMinutes, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Library{}, ErrConflict
		}
		return Library{}, err
	}
	id, _ := res.LastInsertId()
	return a.Library(ctx, id)
}

// SyncConfigLibraries upserts the libraries declared in vcrx.yaml by name. The
// kind of an existing library never changes from config, since that would orphan
// its items.
func (a *Admin) SyncConfigLibraries(ctx context.Context, specs []config.LibrarySpec) error {
	var errs []error
	for _, sp := range specs {
		in := LibraryInput{Name: sp.Name, Kind: sp.Kind, RootPath: sp.Path, Grouping: sp.Grouping, ScanIntervalMinutes: sp.ScanIntervalMinutes}
		if err := in.normalize(); err != nil {
			errs = append(errs, fmt.Errorf("library %q: %w", sp.Name, err))
			continue
		}
		var id int64
		var kind string
		err := a.DB.QueryRowContext(ctx, `SELECT id, kind FROM libraries WHERE name = ?`, in.Name).Scan(&id, &kind)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := a.CreateLibrary(ctx, in); err != nil {
				errs = append(errs, fmt.Errorf("library %q: %w", sp.Name, err))
			}
		case err != nil:
			errs = append(errs, err)
		default:
			if kind != in.Kind {
				errs = append(errs, fmt.Errorf("library %q is a %s library; vcrx.yaml says %s, which is ignored", in.Name, kind, in.Kind))
			}
			root, err := a.ValidateRoot(in.RootPath)
			if err != nil {
				errs = append(errs, fmt.Errorf("library %q: %w", sp.Name, err))
				continue
			}
			if _, err := a.DB.ExecContext(ctx, `UPDATE libraries SET root_path = ?, grouping = ?, scan_interval_minutes = ?, updated_at = ? WHERE id = ?`,
				root, in.Grouping, in.ScanIntervalMinutes, db.NowMs(), id); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (a *Admin) Library(ctx context.Context, id int64) (Library, error) {
	libs, err := a.libraries(ctx, "WHERE id = ?", id)
	if err != nil {
		return Library{}, err
	}
	if len(libs) == 0 {
		return Library{}, ErrNotFound
	}
	return libs[0], nil
}

func (a *Admin) Libraries(ctx context.Context) ([]Library, error) {
	return a.libraries(ctx, "")
}

func (a *Admin) libraries(ctx context.Context, where string, args ...any) ([]Library, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT id, name, kind, root_path, grouping, scan_interval_minutes,
			(SELECT COUNT(*) FROM items i WHERE i.library_id = libraries.id AND i.parent_id IS NULL AND i.missing_since IS NULL)
		FROM libraries `+where+` ORDER BY name COLLATE NOCASE`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	libs := []Library{}
	for rows.Next() {
		var l Library
		if err := rows.Scan(&l.ID, &l.Name, &l.Kind, &l.RootPath, &l.Grouping, &l.ScanIntervalMinutes, &l.ItemCount); err != nil {
			return nil, err
		}
		libs = append(libs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range libs {
		scans, err := a.Scans(ctx, libs[i].ID, 1)
		if err != nil {
			return nil, err
		}
		if len(scans) > 0 {
			libs[i].LastScan = &scans[0]
		}
	}
	return libs, nil
}

// Scans lists scan runs, newest first; libraryID 0 means all libraries.
func (a *Admin) Scans(ctx context.Context, libraryID int64, limit int) ([]ScanRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	q := `SELECT id, library_id, trigger, path_filter, status, error, files_seen, files_added, files_changed, files_missing,
			files_failed, started_at, finished_at FROM scan_runs`
	var args []any
	if libraryID > 0 {
		q += ` WHERE library_id = ?`
		args = append(args, libraryID)
	}
	q += ` ORDER BY started_at DESC, id DESC LIMIT ?`
	rows, err := a.DB.QueryContext(ctx, q, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []ScanRun{}
	for rows.Next() {
		var s ScanRun
		var pf, e sql.NullString
		var fin sql.NullInt64
		if err := rows.Scan(&s.ID, &s.LibraryID, &s.Trigger, &pf, &s.Status, &e, &s.FilesSeen, &s.FilesAdded, &s.FilesChanged,
			&s.FilesMissing, &s.FilesFailed, &s.StartedAt, &fin); err != nil {
			return nil, err
		}
		if pf.Valid {
			s.PathFilter = &pf.String
		}
		if e.Valid {
			s.Error = &e.String
		}
		if fin.Valid {
			s.FinishedAt = &fin.Int64
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (a *Admin) Scan(ctx context.Context, id int64) (ScanRun, error) {
	var s ScanRun
	var pf, e sql.NullString
	var fin sql.NullInt64
	err := a.DB.QueryRowContext(ctx, `SELECT id, library_id, trigger, path_filter, status, error, files_seen, files_added, files_changed,
			files_missing, files_failed, started_at, finished_at FROM scan_runs WHERE id = ?`, id).Scan(&s.ID, &s.LibraryID, &s.Trigger,
		&pf, &s.Status, &e, &s.FilesSeen, &s.FilesAdded, &s.FilesChanged, &s.FilesMissing, &s.FilesFailed, &s.StartedAt, &fin)
	if errors.Is(err, sql.ErrNoRows) {
		return s, ErrNotFound
	}
	if pf.Valid {
		s.PathFilter = &pf.String
	}
	if e.Valid {
		s.Error = &e.String
	}
	if fin.Valid {
		s.FinishedAt = &fin.Int64
	}
	return s, err
}

type UserPolicy struct {
	MaxResolution    *string  `json:"max_resolution"`
	MaxContentRating *string  `json:"max_content_rating"`
	AllowLabels      []string `json:"allow_labels"`
	DenyLabels       []string `json:"deny_labels"`
	AllowTranscode   bool     `json:"allow_transcode"`
	Allow4KTranscode bool     `json:"allow_4k_transcode"`
}

type User struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	DisplayName *string    `json:"display_name"`
	IsAdmin     bool       `json:"is_admin"`
	Disabled    bool       `json:"disabled"`
	LibraryIDs  []int64    `json:"library_ids"`
	Policy      UserPolicy `json:"policy"`
	CreatedAt   int64      `json:"created_at"`
}

func (a *Admin) Users(ctx context.Context) ([]User, error) {
	rows, err := a.DB.QueryContext(ctx, `SELECT id, username, display_name, is_admin, disabled, created_at FROM users ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	users := []User{}
	for rows.Next() {
		var u User
		var dn sql.NullString
		if err := rows.Scan(&u.ID, &u.Username, &dn, &u.IsAdmin, &u.Disabled, &u.CreatedAt); err != nil {
			return nil, err
		}
		if dn.Valid {
			u.DisplayName = &dn.String
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range users {
		p, err := policy.Load(ctx, a.DB, users[i].ID, users[i].IsAdmin)
		if err != nil {
			return nil, err
		}
		users[i].LibraryIDs = append([]int64{}, p.LibraryIDs...)
		users[i].Policy = toUserPolicy(p)
	}
	return users, nil
}

func (a *Admin) User(ctx context.Context, id int64) (User, error) {
	users, err := a.Users(ctx)
	if err != nil {
		return User{}, err
	}
	for _, u := range users {
		if u.ID == id {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func toUserPolicy(p policy.Policy) UserPolicy {
	up := UserPolicy{AllowLabels: nonNil(p.AllowLabels), DenyLabels: nonNil(p.DenyLabels), AllowTranscode: p.AllowTranscode, Allow4KTranscode: p.Allow4KTranscode}
	if p.MaxResolution != "" {
		up.MaxResolution = &p.MaxResolution
	}
	if p.MaxContentRating != "" {
		up.MaxContentRating = &p.MaxContentRating
	}
	return up
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (a *Admin) userExists(ctx context.Context, id int64) error {
	var n int
	if err := a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = ?`, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetGrants replaces the libraries a user may see.
func (a *Admin) SetGrants(ctx context.Context, userID int64, libraryIDs []int64) error {
	if err := a.userExists(ctx, userID); err != nil {
		return err
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM library_grants WHERE user_id = ?`, userID); err != nil {
		return err
	}
	for _, lid := range libraryIDs {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO library_grants (user_id, library_id) VALUES (?, ?)`, userID, lid); err != nil {
			if strings.Contains(err.Error(), "FOREIGN KEY") {
				return fmt.Errorf("library %d does not exist", lid)
			}
			return err
		}
	}
	return tx.Commit()
}

func (a *Admin) SetPolicy(ctx context.Context, userID int64, p UserPolicy) error {
	if err := a.userExists(ctx, userID); err != nil {
		return err
	}
	if p.MaxResolution != nil && policy.ResolutionRank(*p.MaxResolution) < 0 {
		return fmt.Errorf("max_resolution must be one of %s", strings.Join(policy.Resolutions, ", "))
	}
	allow, _ := json.Marshal(nonNil(p.AllowLabels))
	deny, _ := json.Marshal(nonNil(p.DenyLabels))
	_, err := a.DB.ExecContext(ctx, `INSERT INTO user_policies (user_id, max_resolution, max_content_rating, allow_labels, deny_labels,
			allow_transcode, allow_4k_transcode, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET max_resolution = excluded.max_resolution, max_content_rating = excluded.max_content_rating,
			allow_labels = excluded.allow_labels, deny_labels = excluded.deny_labels, allow_transcode = excluded.allow_transcode,
			allow_4k_transcode = excluded.allow_4k_transcode, updated_at = excluded.updated_at`,
		userID, p.MaxResolution, p.MaxContentRating, string(allow), string(deny), p.AllowTranscode, p.Allow4KTranscode, db.NowMs())
	return err
}

type ActivitySession struct {
	ID              string  `json:"id"`
	UserID          *int64  `json:"user_id"`
	Username        *string `json:"username"`
	ItemID          *int64  `json:"item_id"`
	ItemTitle       *string `json:"item_title"`
	VersionID       *int64  `json:"version_id"`
	VersionSummary  *string `json:"version_summary"`
	DeviceName      *string `json:"device_name"`
	DeviceModel     *string `json:"device_model"`
	Client          *string `json:"client"`
	ClientVersion   *string `json:"client_version"`
	IP              *string `json:"ip"`
	Decision        string  `json:"decision"`
	DecisionReason  string  `json:"decision_reason"`
	State           string  `json:"state"`
	StartPositionMs int64   `json:"start_position_ms"`
	LastPositionMs  int64   `json:"last_position_ms"`
	DurationMs      *int64  `json:"duration_ms"`
	PlayedMs        int64   `json:"played_ms"`
	StartedAt       int64   `json:"started_at"`
	LastEventAt     int64   `json:"last_event_at"`
	EndedAt         *int64  `json:"ended_at"`
}

func (a *Admin) PlaybackSessions(ctx context.Context, active bool, userID int64, limit, offset int) ([]ActivitySession, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var where []string
	var args []any
	if active {
		where = append(where, "ended_at IS NULL")
	}
	if userID > 0 {
		where = append(where, "user_id = ?")
		args = append(args, userID)
	}
	q := `SELECT id, user_id, username, item_id, item_title, version_id, version_summary, device_name, device_model, client,
			client_version, ip, decision, decision_reason, state, start_position_ms, last_position_ms, duration_ms, played_ms,
			started_at, last_event_at, ended_at FROM playback_sessions`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY started_at DESC LIMIT ? OFFSET ?"
	rows, err := a.DB.QueryContext(ctx, q, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []ActivitySession{}
	for rows.Next() {
		var s ActivitySession
		var uid, iid, vid, dur, ended sql.NullInt64
		var uname, title, vsum, dname, dmodel, client, cver, ip sql.NullString
		if err := rows.Scan(&s.ID, &uid, &uname, &iid, &title, &vid, &vsum, &dname, &dmodel, &client, &cver, &ip, &s.Decision,
			&s.DecisionReason, &s.State, &s.StartPositionMs, &s.LastPositionMs, &dur, &s.PlayedMs, &s.StartedAt, &s.LastEventAt, &ended); err != nil {
			return nil, err
		}
		s.UserID, s.ItemID, s.VersionID, s.DurationMs, s.EndedAt = i64(uid), i64(iid), i64(vid), i64(dur), i64(ended)
		s.Username, s.ItemTitle, s.VersionSummary, s.DeviceName = str(uname), str(title), str(vsum), str(dname)
		s.DeviceModel, s.Client, s.ClientVersion, s.IP = str(dmodel), str(client), str(cver), str(ip)
		out = append(out, s)
	}
	return out, rows.Err()
}

func i64(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	return &n.Int64
}

func str(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}
