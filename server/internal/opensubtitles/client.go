// Package opensubtitles is a small client for the OpenSubtitles REST API (v1,
// api.opensubtitles.com): search, download and the account's download quota.
//
// Searching needs only an API key. Downloading also needs an account, because
// the quota (20 a day on a free account) is counted per user. The client
// spaces its own requests, stops asking for a while after a 429, and turns
// every network failure into ErrUnavailable, so a server with no internet
// connection answers "not now" instead of failing.
package opensubtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the public API. Login may name another host for VIP
// accounts; the client follows that only to an opensubtitles.com host.
const DefaultBaseURL = "https://api.opensubtitles.com/api/v1"

var (
	// ErrNotConfigured means no API key is set: the feature is off.
	ErrNotConfigured = errors.New("opensubtitles: no API key")
	// ErrNoAccount means downloads were asked for without a username and password.
	ErrNoAccount = errors.New("opensubtitles: downloads need a username and password")
	// ErrUnavailable means the service could not be reached or failed on its side.
	ErrUnavailable = errors.New("opensubtitles: service unavailable")
)

// RateLimitError means OpenSubtitles asked us to slow down. Until is when the
// client will try again; calls before then fail at once without a request.
type RateLimitError struct{ Until time.Time }

func (e *RateLimitError) Error() string {
	return "opensubtitles: rate limited until " + e.Until.UTC().Format(time.RFC3339)
}

// QuotaError means the account's daily download quota is used up.
type QuotaError struct{ Quota Quota }

func (e *QuotaError) Error() string {
	if e.Quota.ResetAt.IsZero() {
		return "opensubtitles: download quota used up"
	}
	return "opensubtitles: download quota used up until " + e.Quota.ResetAt.UTC().Format(time.RFC3339)
}

// Quota is what the account may still download. Known is false until the
// first download or account lookup.
type Quota struct {
	Known     bool      `json:"known"`
	Remaining int       `json:"remaining"`
	Allowed   int       `json:"allowed"`
	ResetAt   time.Time `json:"reset_at"`
	Level     string    `json:"level,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
}

// Query is one search. Ids are numbers without the "tt" prefix. For an
// episode, the show's ids go in Parent* with Season and Episode.
type Query struct {
	MovieHash    string
	IMDbID       int64
	TMDbID       int64
	ParentIMDbID int64
	ParentTMDbID int64
	Season       int
	Episode      int
	Title        string
	Year         int
	// Type is "movie" or "episode"; empty searches both.
	Type string
	// Languages are ISO 639-1 codes, lowercase ("en", "pt-br").
	Languages []string
}

// Result is one downloadable subtitle file.
type Result struct {
	FileID            int64   `json:"file_id"`
	FileName          string  `json:"file_name"`
	Language          string  `json:"language"`
	Release           string  `json:"release"`
	HearingImpaired   bool    `json:"hearing_impaired"`
	HashMatch         bool    `json:"hash_match"`
	Trusted           bool    `json:"trusted"`
	DownloadCount     int     `json:"download_count"`
	Uploader          string  `json:"uploader"`
	MachineTranslated bool    `json:"machine_translated"`
	FPS               float64 `json:"fps"`
}

// File is a downloaded subtitle and the quota left after it.
type File struct {
	Name    string
	Content []byte
	Quota   Quota
}

// MaxFileSize bounds a downloaded subtitle. Real ones are well under 1 MB.
const MaxFileSize = 5 << 20

type Client struct {
	APIKey   string
	Username string
	Password string
	// BaseURL defaults to DefaultBaseURL. Tests point it at a stand-in.
	BaseURL string
	// UserAgent names the app, which OpenSubtitles requires ("VCRX v1.2.3").
	UserAgent string
	HTTP      *http.Client
	// MinInterval spaces requests; zero means 250 ms, under the API's
	// documented limit of a few requests a second.
	MinInterval time.Duration
	Now         func() time.Time

	mu           sync.Mutex
	next         time.Time
	blockedUntil time.Time
	token        string
	apiBase      string
	tokenAt      time.Time
	quota        Quota
}

// New returns a client, or nil when apiKey is empty (the feature is off).
func New(apiKey, username, password, userAgent string) *Client {
	if strings.TrimSpace(apiKey) == "" {
		return nil
	}
	return &Client{APIKey: strings.TrimSpace(apiKey), Username: username, Password: password, UserAgent: userAgent}
}

// CanDownload reports whether an account is configured.
func (c *Client) CanDownload() bool {
	return c != nil && c.Username != "" && c.Password != ""
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (c *Client) base() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.apiBase != "" {
		return c.apiBase
	}
	if c.BaseURL != "" {
		return strings.TrimSuffix(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

// wait spaces requests and refuses while a 429 is in force.
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	now := c.now()
	if now.Before(c.blockedUntil) {
		until := c.blockedUntil
		c.mu.Unlock()
		return &RateLimitError{Until: until}
	}
	gap := c.MinInterval
	if gap <= 0 {
		gap = 250 * time.Millisecond
	}
	at := c.next
	if at.Before(now) {
		at = now
	}
	c.next = at.Add(gap)
	c.mu.Unlock()
	if d := at.Sub(now); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

type apiError struct {
	Status  int
	Message string
	Body    []byte
}

func (e *apiError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("opensubtitles: %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("opensubtitles: HTTP %d", e.Status)
}

// do sends one request and decodes a JSON answer into out. auth adds the
// login token, logging in first and once more after a 401.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any, auth bool) error {
	if c == nil || c.APIKey == "" {
		return ErrNotConfigured
	}
	for attempt := 0; ; attempt++ {
		tok := ""
		if auth {
			var err error
			if tok, err = c.login(ctx); err != nil {
				return err
			}
		}
		status, data, hdr, err := c.send(ctx, method, c.base()+path, query, body, tok)
		if err != nil {
			return err
		}
		if status == http.StatusUnauthorized && auth && attempt == 0 {
			c.mu.Lock()
			c.token = ""
			c.mu.Unlock()
			continue
		}
		return c.decode(status, data, hdr, out)
	}
}

func (c *Client) send(ctx context.Context, method, u string, query url.Values, body any, tok string) (int, []byte, http.Header, error) {
	if err := c.wait(ctx); err != nil {
		return 0, nil, nil, err
	}
	if len(query) > 0 {
		// The API redirects any query whose parameters are not sorted and
		// lowercase, which costs a request; url.Values.Encode sorts by key.
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, nil, nil, err
	}
	req.Header.Set("Api-Key", c.APIKey)
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	res, err := c.http().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, nil, ctx.Err()
		}
		return 0, nil, nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return res.StatusCode, data, res.Header, nil
}

func (c *Client) userAgent() string {
	if c.UserAgent != "" {
		return c.UserAgent
	}
	return "VCRX v0"
}

func (c *Client) decode(status int, data []byte, hdr http.Header, out any) error {
	switch {
	case status == http.StatusTooManyRequests:
		until := c.now().Add(retryAfter(hdr))
		c.mu.Lock()
		if until.After(c.blockedUntil) {
			c.blockedUntil = until
		}
		c.mu.Unlock()
		return &RateLimitError{Until: until}
	case status >= 500:
		return fmt.Errorf("%w: HTTP %d", ErrUnavailable, status)
	case status >= 400:
		var e struct {
			Message string `json:"message"`
			Errors  []string
		}
		_ = json.Unmarshal(data, &e)
		if e.Message == "" && len(e.Errors) > 0 {
			e.Message = strings.Join(e.Errors, "; ")
		}
		return &apiError{Status: status, Message: e.Message, Body: data}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("opensubtitles: unreadable answer: %w", err)
	}
	return nil
}

// retryAfter reads how long to back off from a 429: Retry-After, then the
// ratelimit-reset header, else ten seconds.
func retryAfter(h http.Header) time.Duration {
	for _, k := range []string{"Retry-After", "Ratelimit-Reset", "X-Ratelimit-Reset"} {
		if n, err := strconv.Atoi(strings.TrimSpace(h.Get(k))); err == nil && n > 0 && n < 3600 {
			return time.Duration(n) * time.Second
		}
	}
	return 10 * time.Second
}

// login returns a token, logging in when there is none or it is a day old.
func (c *Client) login(ctx context.Context) (string, error) {
	if !c.CanDownload() {
		return "", ErrNoAccount
	}
	c.mu.Lock()
	if c.token != "" && c.now().Sub(c.tokenAt) < 23*time.Hour {
		t := c.token
		c.mu.Unlock()
		return t, nil
	}
	c.apiBase = ""
	c.mu.Unlock()
	var res struct {
		Token   string `json:"token"`
		BaseURL string `json:"base_url"`
		User    struct {
			AllowedDownloads   int    `json:"allowed_downloads"`
			RemainingDownloads *int   `json:"remaining_downloads"`
			Level              string `json:"level"`
		} `json:"user"`
	}
	status, data, hdr, err := c.send(ctx, http.MethodPost, c.base()+"/login", nil,
		map[string]string{"username": c.Username, "password": c.Password}, "")
	if err != nil {
		return "", err
	}
	if status == http.StatusUnauthorized {
		return "", &apiError{Status: status, Message: "the OpenSubtitles username or password is wrong"}
	}
	if err := c.decode(status, data, hdr, &res); err != nil {
		return "", err
	}
	if res.Token == "" {
		return "", errors.New("opensubtitles: login returned no token")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token, c.tokenAt = res.Token, c.now()
	if c.BaseURL == "" || c.BaseURL == DefaultBaseURL {
		// A configured BaseURL (a test stand-in, a proxy) is kept as given.
		c.apiBase = vipBase(res.BaseURL)
	}
	if res.User.RemainingDownloads != nil {
		c.quota = Quota{Known: true, Remaining: *res.User.RemainingDownloads, Allowed: res.User.AllowedDownloads,
			Level: res.User.Level, CheckedAt: c.now(), ResetAt: c.quota.ResetAt}
	}
	return c.token, nil
}

// vipBase turns login's base_url into an API base, but only for an
// opensubtitles.com host: an answer must never send the API key elsewhere.
func vipBase(host string) string {
	host = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://"), "/")
	host = strings.TrimSuffix(host, "/api/v1")
	if host == "" || strings.ContainsAny(host, "/@?#:") {
		return ""
	}
	if host != "api.opensubtitles.com" && !strings.HasSuffix(host, ".opensubtitles.com") {
		return ""
	}
	return "https://" + host + "/api/v1"
}

type searchAnswer struct {
	Data []struct {
		Attributes struct {
			Language          string  `json:"language"`
			DownloadCount     int     `json:"download_count"`
			HearingImpaired   bool    `json:"hearing_impaired"`
			FromTrusted       bool    `json:"from_trusted"`
			AITranslated      bool    `json:"ai_translated"`
			MachineTranslated bool    `json:"machine_translated"`
			MoviehashMatch    bool    `json:"moviehash_match"`
			Release           string  `json:"release"`
			FPS               float64 `json:"fps"`
			Uploader          struct {
				Name string `json:"name"`
				Rank string `json:"rank"`
			} `json:"uploader"`
			Files []struct {
				FileID   int64  `json:"file_id"`
				FileName string `json:"file_name"`
			} `json:"files"`
		} `json:"attributes"`
	} `json:"data"`
}

// Search runs one query and returns the first page of results, one entry per
// subtitle (multi-file, multi-CD subtitles are skipped: they cannot be one track).
func (c *Client) Search(ctx context.Context, q Query) ([]Result, error) {
	v := url.Values{}
	set := func(k string, n int64) {
		if n > 0 {
			v.Set(k, strconv.FormatInt(n, 10))
		}
	}
	if q.MovieHash != "" {
		v.Set("moviehash", strings.ToLower(q.MovieHash))
	}
	set("imdb_id", q.IMDbID)
	set("tmdb_id", q.TMDbID)
	set("parent_imdb_id", q.ParentIMDbID)
	set("parent_tmdb_id", q.ParentTMDbID)
	set("season_number", int64(q.Season))
	set("episode_number", int64(q.Episode))
	set("year", int64(q.Year))
	if q.Title != "" {
		v.Set("query", strings.ToLower(strings.TrimSpace(q.Title)))
	}
	if q.Type != "" {
		v.Set("type", q.Type)
	}
	if len(q.Languages) > 0 {
		langs := make([]string, 0, len(q.Languages))
		for _, l := range q.Languages {
			langs = append(langs, strings.ToLower(l))
		}
		sort.Strings(langs)
		v.Set("languages", strings.Join(langs, ","))
	}
	// Machine translations are rarely watchable; leave them out.
	v.Set("ai_translated", "exclude")
	v.Set("machine_translated", "exclude")
	var ans searchAnswer
	if err := c.do(ctx, http.MethodGet, "/subtitles", v, nil, &ans, false); err != nil {
		return nil, err
	}
	out := make([]Result, 0, len(ans.Data))
	for _, d := range ans.Data {
		a := d.Attributes
		if len(a.Files) != 1 || a.Files[0].FileID <= 0 {
			continue
		}
		rank := strings.ToLower(a.Uploader.Rank)
		out = append(out, Result{
			FileID: a.Files[0].FileID, FileName: a.Files[0].FileName, Language: strings.ToLower(a.Language),
			Release: a.Release, HearingImpaired: a.HearingImpaired, HashMatch: a.MoviehashMatch,
			Trusted:       a.FromTrusted || strings.Contains(rank, "trusted") || strings.Contains(rank, "admin"),
			DownloadCount: a.DownloadCount, Uploader: a.Uploader.Name,
			MachineTranslated: a.AITranslated || a.MachineTranslated, FPS: a.FPS,
		})
	}
	return out, nil
}

type downloadAnswer struct {
	Link         string `json:"link"`
	FileName     string `json:"file_name"`
	Requests     int    `json:"requests"`
	Remaining    *int   `json:"remaining"`
	ResetTimeUTC string `json:"reset_time_utc"`
	Message      string `json:"message"`
}

// Download asks for a file and fetches it. It spends one download from the
// account's quota; a used-up quota is a *QuotaError.
func (c *Client) Download(ctx context.Context, fileID int64) (File, error) {
	if c == nil || c.APIKey == "" {
		return File{}, ErrNotConfigured
	}
	var ans downloadAnswer
	err := c.do(ctx, http.MethodPost, "/download", nil, map[string]any{"file_id": fileID, "sub_format": "srt"}, &ans, true)
	var ae *apiError
	if errors.As(err, &ae) && (ae.Status == http.StatusNotAcceptable || ae.Status == 407) {
		// 406 (and 407 in some clients' reading of the docs) is the daily quota.
		var body downloadAnswer
		_ = json.Unmarshal(ae.Body, &body)
		q := c.noteQuota(0, 0, body.ResetTimeUTC)
		return File{}, &QuotaError{Quota: q}
	}
	if err != nil {
		return File{}, err
	}
	q := c.LastQuota()
	if ans.Remaining != nil {
		q = c.noteQuota(*ans.Remaining, ans.Requests+*ans.Remaining, ans.ResetTimeUTC)
	}
	if ans.Link == "" {
		if ans.Remaining != nil && *ans.Remaining <= 0 {
			return File{}, &QuotaError{Quota: q}
		}
		return File{}, errors.New("opensubtitles: download returned no link")
	}
	content, err := c.fetch(ctx, ans.Link)
	if err != nil {
		return File{}, err
	}
	return File{Name: ans.FileName, Content: content, Quota: q}, nil
}

func (c *Client) noteQuota(remaining, allowed int, reset string) Quota {
	c.mu.Lock()
	defer c.mu.Unlock()
	q := Quota{Known: true, Remaining: max(0, remaining), Allowed: allowed, Level: c.quota.Level, CheckedAt: c.now()}
	if allowed == 0 {
		q.Allowed = c.quota.Allowed
	}
	if t, err := time.Parse(time.RFC3339, reset); err == nil {
		q.ResetAt = t
	} else if !c.quota.ResetAt.IsZero() && c.quota.ResetAt.After(c.now()) {
		q.ResetAt = c.quota.ResetAt
	}
	c.quota = q
	return q
}

// LinkError means /download answered, so a download was already counted
// against the quota, but the file behind its link could not be fetched.
// Callers must remember the attempt: asking again spends another download.
// Transient (network, 5xx) also matches ErrUnavailable.
type LinkError struct {
	Status    int
	Transient bool
	Err       error
}

func (e *LinkError) Error() string {
	return "opensubtitles: download link failed after the quota was spent: " + e.Err.Error()
}

func (e *LinkError) Unwrap() []error {
	if e.Transient {
		return []error{e.Err, ErrUnavailable}
	}
	return []error{e.Err}
}

// fetch downloads the file behind a download link. Every failure is a
// *LinkError, because the quota is already spent by now.
func (c *Client) fetch(ctx context.Context, link string) ([]byte, error) {
	b, status, err := c.fetchLink(ctx, link)
	if err != nil {
		var rl *RateLimitError
		transient := status >= 500 || (status == 0 && (errors.Is(err, ErrUnavailable) || errors.As(err, &rl) ||
			errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)))
		return nil, &LinkError{Status: status, Transient: transient, Err: err}
	}
	return b, nil
}

// fetchLink fetches a link on another host, so the API key and token are not
// sent with it. status is 0 when no answer came.
func (c *Client) fetchLink(ctx context.Context, link string) ([]byte, int, error) {
	u, err := url.Parse(link)
	if err != nil || u.Host == "" || !c.linkScheme(u.Scheme) {
		return nil, 0, fmt.Errorf("refusing download link %q", link)
	}
	if err := c.wait(ctx); err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", c.userAgent())
	res, err := c.http().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, res.StatusCode, fmt.Errorf("download link answered %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxFileSize+1))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if len(b) > MaxFileSize {
		return nil, res.StatusCode, errors.New("subtitle file larger than 5 MB")
	}
	return b, res.StatusCode, nil
}

// linkScheme accepts https, and plain http only when the API itself is plain
// http (a test stand-in).
func (c *Client) linkScheme(scheme string) bool {
	return scheme == "https" || scheme == "http" && strings.HasPrefix(c.base(), "http://")
}

// LastQuota is the quota as last reported, without a request.
func (c *Client) LastQuota() Quota {
	if c == nil {
		return Quota{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.quota
}

// Quota asks the account's remaining downloads, at most every five minutes.
func (c *Client) Quota(ctx context.Context) (Quota, error) {
	if c == nil || c.APIKey == "" {
		return Quota{}, ErrNotConfigured
	}
	if !c.CanDownload() {
		return Quota{}, ErrNoAccount
	}
	if q := c.LastQuota(); q.Known && c.now().Sub(q.CheckedAt) < 5*time.Minute {
		return q, nil
	}
	var ans struct {
		Data struct {
			AllowedDownloads   int    `json:"allowed_downloads"`
			RemainingDownloads int    `json:"remaining_downloads"`
			Level              string `json:"level"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/infos/user", nil, nil, &ans, true); err != nil {
		return c.LastQuota(), err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	reset := c.quota.ResetAt
	if !reset.After(c.now()) {
		reset = time.Time{}
	}
	c.quota = Quota{Known: true, Remaining: max(0, ans.Data.RemainingDownloads), Allowed: ans.Data.AllowedDownloads,
		Level: ans.Data.Level, CheckedAt: c.now(), ResetAt: reset}
	return c.quota, nil
}
