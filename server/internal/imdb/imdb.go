// Package imdb keeps IMDb ratings for the library, read from IMDb's public
// ratings file (title.ratings.tsv.gz, refreshed daily), which is where Kometa's
// IMDb ratings come from too. Only titles the library has an IMDb id for are
// kept. IMDb licenses the file for personal and non-commercial use, so the sync
// is off unless the server's owner turns it on.
package imdb

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DatasetURL is IMDb's ratings file: tconst, averageRating, numVotes.
const DatasetURL = "https://datasets.imdbws.com/title.ratings.tsv.gz"

// maxDownload bounds the compressed file; it is about 8 MB today.
const maxDownload = 200 << 20

const settingKey = "imdb_ratings_last_modified"

type Syncer struct {
	DB     *sql.DB
	URL    string
	Client *http.Client
	Log    *slog.Logger
	Now    func() time.Time
}

func (s *Syncer) url() string {
	if s.URL != "" {
		return s.URL
	}
	return DatasetURL
}

// Result says what a sync did.
type Result struct {
	NotModified bool
	Wanted      int // titles in the library with an IMDb id
	Updated     int // ratings written
	Removed     int // ratings dropped because IMDb no longer lists the title
}

// Sync downloads the file unless IMDb says it has not changed since the last
// sync, and writes a rating for every library title it lists.
func (s *Syncer) Sync(ctx context.Context, force bool) (Result, error) {
	var res Result
	wanted, err := s.wanted(ctx)
	if err != nil {
		return res, err
	}
	res.Wanted = len(wanted)
	if len(wanted) == 0 {
		return res, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url(), nil)
	if err != nil {
		return res, err
	}
	req.Header.Set("User-Agent", "vcrx-media-server")
	if !force {
		var last string
		if err := s.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, settingKey).Scan(&last); err == nil && last != "" {
			req.Header.Set("If-Modified-Since", last)
		}
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return res, fmt.Errorf("imdb: download: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified {
		res.NotModified = true
		return res, nil
	}
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("imdb: download: HTTP %d", resp.StatusCode)
	}
	found, err := parse(io.LimitReader(resp.Body, maxDownload), wanted)
	if err != nil {
		return res, err
	}
	// A file that matches nothing at all is more likely broken than true.
	if len(found) == 0 {
		return res, errors.New("imdb: the ratings file matched no title in the library; keeping the old ratings")
	}
	res.Updated, res.Removed, err = s.store(ctx, wanted, found)
	if err != nil {
		return res, err
	}
	if lm := resp.Header.Get("Last-Modified"); lm != "" {
		_, _ = s.DB.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, settingKey, lm, s.now().UnixMilli())
	}
	return res, nil
}

func (s *Syncer) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// wanted maps each IMDb id in the library to its item ids (a title can sit in
// more than one library).
func (s *Syncer) wanted(ctx context.Context) (map[string][]int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, imdb_id FROM items WHERE imdb_id IS NOT NULL AND imdb_id != ''
		AND kind IN ('movie', 'show', 'episode')`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]int64{}
	for rows.Next() {
		var id int64
		var tt string
		if err := rows.Scan(&id, &tt); err != nil {
			return nil, err
		}
		tt = strings.TrimSpace(tt)
		if strings.HasPrefix(tt, "tt") {
			out[tt] = append(out[tt], id)
		}
	}
	return out, rows.Err()
}

type rating struct {
	value float64
	votes int64
}

func parse(r io.Reader, wanted map[string][]int64) (map[string]rating, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("imdb: not a gzip file: %w", err)
	}
	defer func() { _ = gz.Close() }()
	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	out := map[string]rating{}
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			if !strings.HasPrefix(line, "tconst\t") {
				return nil, fmt.Errorf("imdb: unexpected header %q", line)
			}
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		if _, ok := wanted[line[:tab]]; !ok {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 3 {
			continue
		}
		v, err1 := strconv.ParseFloat(f[1], 64)
		n, err2 := strconv.ParseInt(f[2], 10, 64)
		if err1 != nil || err2 != nil || v < 0 || v > 10 {
			continue
		}
		out[f[0]] = rating{value: v, votes: n}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("imdb: read: %w", err)
	}
	return out, nil
}

func (s *Syncer) store(ctx context.Context, wanted map[string][]int64, found map[string]rating) (updated, removed int, err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	for tt, ids := range wanted {
		r, ok := found[tt]
		for _, id := range ids {
			if !ok {
				res, err := tx.ExecContext(ctx, `DELETE FROM item_ratings WHERE item_id = ? AND source = 'imdb'`, id)
				if err != nil {
					return 0, 0, err
				}
				n, _ := res.RowsAffected()
				removed += int(n)
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO item_ratings (item_id, source, value, votes) VALUES (?, 'imdb', ?, ?)
				ON CONFLICT (item_id, source) DO UPDATE SET value = excluded.value, votes = excluded.votes`, id, r.value, r.votes); err != nil {
				return 0, 0, err
			}
			updated++
		}
	}
	return updated, removed, tx.Commit()
}

// Run syncs after a short delay and then once a day until ctx ends.
func (s *Syncer) Run(ctx context.Context) {
	log := s.Log
	if log == nil {
		log = slog.Default()
	}
	t := time.NewTimer(3 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		res, err := s.Sync(ctx, false)
		switch {
		case err != nil:
			log.Warn("imdb ratings sync failed", "err", err)
		case res.NotModified:
			log.Info("imdb ratings unchanged since the last sync")
		default:
			log.Info("imdb ratings synced", "titles", res.Wanted, "updated", res.Updated, "removed", res.Removed)
		}
		t.Reset(24 * time.Hour)
	}
}
