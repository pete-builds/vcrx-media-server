// Package tmdb is a small client for The Movie Database API v3: search, movie
// and TV details, seasons, and image downloads. It is used only when the
// operator supplies their own API key.
package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultBaseURL  = "https://api.themoviedb.org/3"
	DefaultImageURL = "https://image.tmdb.org/t/p"
	maxBody         = 4 << 20
	// MaxImage caps a downloaded image; TMDb originals at the sizes used here are far smaller.
	MaxImage = 15 << 20
)

var (
	// ErrUnauthorized means TMDb rejected the key; every later call would fail too.
	ErrUnauthorized = errors.New("TMDb rejected the API key")
	ErrNotFound     = errors.New("not found on TMDb")
	// imagePath is the only shape of file_path TMDb returns. Anything else is refused,
	// so a response can never steer a download to another path or host.
	imagePath = regexp.MustCompile(`^/[A-Za-z0-9_-]+\.(jpg|jpeg|png)$`)
)

type Client struct {
	// Key is a v3 API key or a v4 read access token (a JWT); both work.
	Key      string
	Language string // e.g. en-US
	BaseURL  string
	ImageURL string
	HTTP     *http.Client
	// Interval spaces requests apart. TMDb allows roughly 40 a second; the default
	// of 100ms keeps a first backfill polite.
	Interval time.Duration

	mu   sync.Mutex
	last time.Time
}

func New(key, language string) *Client {
	return &Client{Key: strings.TrimSpace(key), Language: language, BaseURL: DefaultBaseURL, ImageURL: DefaultImageURL,
		HTTP: &http.Client{Timeout: 30 * time.Second}, Interval: 100 * time.Millisecond}
}

func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	next := c.last.Add(c.Interval)
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	c.last = next
	c.mu.Unlock()
	t := time.NewTimer(time.Until(next))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// bearer reports whether Key is a v4 read access token rather than a v3 key.
func (c *Client) bearer() bool { return strings.Count(c.Key, ".") == 2 && len(c.Key) > 60 }

// do fetches u, retrying a 429 or 5xx up to three times.
func (c *Client) do(ctx context.Context, u string, auth bool, limit int64) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if err := c.wait(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		if auth && c.bearer() {
			req.Header.Set("Authorization", "Bearer "+c.Key)
		}
		req.Header.Set("Accept", "application/json")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		_ = resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			return nil, ErrUnauthorized
		case resp.StatusCode == http.StatusNotFound:
			return nil, ErrNotFound
		case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
			lastErr = fmt.Errorf("TMDb answered %d", resp.StatusCode)
			backoff := time.Duration(attempt+1) * time.Second
			if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 && s <= 30 {
				backoff = time.Duration(s) * time.Second
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			continue
		case resp.StatusCode != http.StatusOK:
			return nil, fmt.Errorf("TMDb answered %d", resp.StatusCode)
		}
		if rerr != nil {
			return nil, rerr
		}
		if int64(len(body)) > limit {
			return nil, errors.New("TMDb response too large")
		}
		return body, nil
	}
	return nil, lastErr
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	if c.Language != "" {
		q.Set("language", c.Language)
	}
	if !c.bearer() {
		q.Set("api_key", c.Key)
	}
	body, err := c.do(ctx, c.BaseURL+path+"?"+q.Encode(), true, maxBody)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// Image downloads an image at a TMDb size such as w780. The path must be a bare
// TMDb file_path.
func (c *Client) Image(ctx context.Context, size, path string) ([]byte, error) {
	if !imagePath.MatchString(path) {
		return nil, fmt.Errorf("unexpected TMDb image path %q", path)
	}
	return c.do(ctx, c.ImageURL+"/"+size+path, false, MaxImage)
}

// ValidImagePath reports whether p is a file_path Image would accept.
func ValidImagePath(p string) bool { return imagePath.MatchString(p) }

type Named struct {
	Name string `json:"name"`
}

type Cast struct {
	Name      string `json:"name"`
	Character string `json:"character"`
	Order     int    `json:"order"`
}

type Crew struct {
	Name       string `json:"name"`
	Job        string `json:"job"`
	Department string `json:"department"`
}

type Credits struct {
	Cast []Cast `json:"cast"`
	Crew []Crew `json:"crew"`
}

type Movie struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Overview      string  `json:"overview"`
	Tagline       string  `json:"tagline"`
	ReleaseDate   string  `json:"release_date"`
	Runtime       int     `json:"runtime"`
	IMDbID        string  `json:"imdb_id"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
	VoteAverage   float64 `json:"vote_average"`
	VoteCount     int     `json:"vote_count"`
	Genres        []Named `json:"genres"`
	Companies     []Named `json:"production_companies"`
	Countries     []Named `json:"production_countries"`
	Collection    *struct {
		ID int64 `json:"id"`
	} `json:"belongs_to_collection"`
	Credits      Credits `json:"credits"`
	ReleaseDates struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Dates   []struct {
				Certification string `json:"certification"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
}

// Certification is the movie's rating in a country, e.g. "PG-13" for US.
func (m *Movie) Certification(country string) string {
	for _, r := range m.ReleaseDates.Results {
		if r.Country != country {
			continue
		}
		for _, d := range r.Dates {
			if d.Certification != "" {
				return d.Certification
			}
		}
	}
	return ""
}

type SeasonRef struct {
	SeasonNumber int    `json:"season_number"`
	PosterPath   string `json:"poster_path"`
	EpisodeCount int    `json:"episode_count"`
}

type Show struct {
	ID           int64       `json:"id"`
	Name         string      `json:"name"`
	OriginalName string      `json:"original_name"`
	Overview     string      `json:"overview"`
	Tagline      string      `json:"tagline"`
	FirstAirDate string      `json:"first_air_date"`
	PosterPath   string      `json:"poster_path"`
	BackdropPath string      `json:"backdrop_path"`
	VoteAverage  float64     `json:"vote_average"`
	VoteCount    int         `json:"vote_count"`
	Genres       []Named     `json:"genres"`
	Networks     []Named     `json:"networks"`
	Seasons      []SeasonRef `json:"seasons"`
	Credits      Credits     `json:"credits"`
	ExternalIDs  struct {
		IMDbID string `json:"imdb_id"`
	} `json:"external_ids"`
	ContentRatings struct {
		Results []struct {
			Country string `json:"iso_3166_1"`
			Rating  string `json:"rating"`
		} `json:"results"`
	} `json:"content_ratings"`
}

func (s *Show) Certification(country string) string {
	for _, r := range s.ContentRatings.Results {
		if r.Country == country && r.Rating != "" {
			return r.Rating
		}
	}
	return ""
}

type Episode struct {
	EpisodeNumber int     `json:"episode_number"`
	Name          string  `json:"name"`
	Overview      string  `json:"overview"`
	AirDate       string  `json:"air_date"`
	StillPath     string  `json:"still_path"`
	VoteAverage   float64 `json:"vote_average"`
	VoteCount     int     `json:"vote_count"`
}

type Season struct {
	SeasonNumber int       `json:"season_number"`
	Overview     string    `json:"overview"`
	PosterPath   string    `json:"poster_path"`
	Episodes     []Episode `json:"episodes"`
}

// Result is one search hit, movie or show.
type Result struct {
	ID            int64    `json:"id"`
	Title         string   `json:"title"`
	OriginalTitle string   `json:"original_title"`
	Name          string   `json:"name"`
	OriginalName  string   `json:"original_name"`
	ReleaseDate   string   `json:"release_date"`
	FirstAirDate  string   `json:"first_air_date"`
	Popularity    float64  `json:"popularity"`
	VoteCount     int      `json:"vote_count"`
	OriginCountry []string `json:"origin_country"`
	Overview      string   `json:"overview"`
	// MediaType is "movie" or "tv" in mixed lists such as trending.
	MediaType string `json:"media_type"`
}

func (r Result) titles() []string {
	var out []string
	for _, t := range []string{r.Title, r.OriginalTitle, r.Name, r.OriginalName} {
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Year is the release or first air year, 0 when TMDb has none.
func (r Result) Year() int { return r.year() }

// DisplayTitle is a film's title or a show's name.
func (r Result) DisplayTitle() string {
	if r.Title != "" {
		return r.Title
	}
	return r.Name
}

func (r Result) year() int {
	d := r.ReleaseDate
	if d == "" {
		d = r.FirstAirDate
	}
	if len(d) < 4 {
		return 0
	}
	y, _ := strconv.Atoi(d[:4])
	return y
}

func (c *Client) Movie(ctx context.Context, id int64) (*Movie, error) {
	var m Movie
	q := url.Values{"append_to_response": {"credits,release_dates"}}
	if err := c.get(ctx, "/movie/"+strconv.FormatInt(id, 10), q, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// CollectionInfo is a TMDb collection (a franchise such as a film series).
type CollectionInfo struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Collection returns a collection's name, for franchise collections.
func (c *Client) Collection(ctx context.Context, id int64) (*CollectionInfo, error) {
	var out CollectionInfo
	if err := c.get(ctx, "/collection/"+strconv.FormatInt(id, 10), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Show(ctx context.Context, id int64) (*Show, error) {
	var s Show
	q := url.Values{"append_to_response": {"credits,content_ratings,external_ids"}}
	if err := c.get(ctx, "/tv/"+strconv.FormatInt(id, 10), q, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (c *Client) Season(ctx context.Context, showID int64, number int) (*Season, error) {
	var s Season
	if err := c.get(ctx, "/tv/"+strconv.FormatInt(showID, 10)+"/season/"+strconv.Itoa(number), nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Search searches movies (kind "movie") or shows (kind "show"), optionally for a year.
func (c *Client) Search(ctx context.Context, kind, query string, year int) ([]Result, error) {
	path, yearKey := "/search/movie", "year"
	if kind == "show" {
		path, yearKey = "/search/tv", "first_air_date_year"
	}
	q := url.Values{"query": {query}, "include_adult": {"false"}}
	if year > 0 {
		q.Set(yearKey, strconv.Itoa(year))
	}
	var out struct {
		Results []Result `json:"results"`
	}
	if err := c.get(ctx, path, q, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

// FindIMDb resolves an IMDb id (tt...) to a TMDb id of the given kind.
func (c *Client) FindIMDb(ctx context.Context, kind, imdbID string) (int64, error) {
	var out struct {
		Movies []Result `json:"movie_results"`
		Shows  []Result `json:"tv_results"`
	}
	if err := c.get(ctx, "/find/"+url.PathEscape(imdbID), url.Values{"external_source": {"imdb_id"}}, &out); err != nil {
		return 0, err
	}
	res := out.Movies
	if kind == "show" {
		res = out.Shows
	}
	if len(res) != 1 {
		return 0, ErrNotFound
	}
	return res[0].ID, nil
}

// ListEntry is one title in a TMDb list: kind "movie" or "show" and its id.
type ListEntry struct {
	Kind string
	ID   int64
}

// Trending is TMDb's trending movies and shows this week, in its order.
func (c *Client) Trending(ctx context.Context) ([]ListEntry, error) {
	return c.list(ctx, "/trending/all/week", "")
}

// Popular is TMDb's popular movies then popular shows.
func (c *Client) Popular(ctx context.Context) ([]ListEntry, error) {
	movies, err := c.list(ctx, "/movie/popular", "movie")
	if err != nil {
		return nil, err
	}
	shows, err := c.list(ctx, "/tv/popular", "show")
	if err != nil {
		return nil, err
	}
	// Interleave, so neither kind crowds the other out of a short row.
	out := make([]ListEntry, 0, len(movies)+len(shows))
	for i := 0; i < max(len(movies), len(shows)); i++ {
		if i < len(movies) {
			out = append(out, movies[i])
		}
		if i < len(shows) {
			out = append(out, shows[i])
		}
	}
	return out, nil
}

// list reads the first three pages of a list. kind is the list's single kind,
// or empty when each result says its own media_type.
func (c *Client) list(ctx context.Context, path, kind string) ([]ListEntry, error) {
	var out []ListEntry
	for page := 1; page <= 3; page++ {
		var resp struct {
			Results    []Result `json:"results"`
			TotalPages int      `json:"total_pages"`
		}
		if err := c.get(ctx, path, url.Values{"page": {strconv.Itoa(page)}}, &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Results {
			k := kind
			switch r.MediaType {
			case "movie":
				k = "movie"
			case "tv":
				k = "show"
			}
			if k != "" && r.ID > 0 {
				out = append(out, ListEntry{Kind: k, ID: r.ID})
			}
		}
		if page >= resp.TotalPages {
			break
		}
	}
	return out, nil
}
