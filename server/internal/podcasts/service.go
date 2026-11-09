package podcasts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Limits on what the server fetches for podcasts.
const (
	maxFeedBytes  = 10 << 20
	maxImageBytes = 5 << 20
	feedTimeout   = 30 * time.Second
	// DefaultMaxEpisodes is how many of a feed's newest episodes are kept.
	DefaultMaxEpisodes = 500
	// HardMaxEpisodes caps podcasts.max_episodes.
	HardMaxEpisodes = 5000
	// DefaultInterval is how often feeds are read again.
	DefaultInterval = 4 * time.Hour
	// episodeImages is how many of a feed's newest episodes have their own
	// image downloaded.
	episodeImages = 20
	// refreshFloor is the least time between two reads of a feed asked for by hand.
	refreshFloor = time.Minute
)

// ErrExplicit is a show marked explicit asked for by a viewer who may not see one.
var ErrExplicit = errors.New("explicit show")

// FeedError is a feed that could not be read, with a reason fit to show the
// user who typed its URL.
type FeedError struct{ Reason string }

func (e *FeedError) Error() string { return e.Reason }

// Service fetches feeds and their images, and keeps subscribed feeds fresh.
type Service struct {
	Store *Store
	Guard Guard
	// Client fetches feeds, images and searches; Stream fetches episode
	// audio, with no overall timeout. Both must come from Guard.Client.
	Client, Stream *http.Client
	// ImageDir holds downloaded show and episode artwork.
	ImageDir    string
	MaxEpisodes int
	Interval    time.Duration
	UserAgent   string
	Log         *slog.Logger
	// Search turns on the Apple directory search; SearchURL is its endpoint
	// (tests point it elsewhere).
	Search    bool
	SearchURL string

	locks sync.Map // feed id or URL to *sync.Mutex
}

// NewService builds a service whose every fetch goes through g.
func NewService(store *Store, g Guard, imageDir string) *Service {
	return &Service{Store: store, Guard: g, Client: g.Client(15 * time.Second), Stream: g.Client(20 * time.Second), ImageDir: imageDir,
		MaxEpisodes: DefaultMaxEpisodes, Interval: DefaultInterval, UserAgent: "VCRX podcasts", SearchURL: "https://itunes.apple.com/search"}
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) lock(key string) func() {
	m, _ := s.locks.LoadOrStore(key, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Service) maxEpisodes() int {
	if s.MaxEpisodes > 0 {
		return s.MaxEpisodes
	}
	return DefaultMaxEpisodes
}

// Subscribe subscribes the viewer to the feed at raw, fetching it first when
// the server does not have it yet. created is false for a subscription the
// viewer already had.
func (s *Service) Subscribe(ctx context.Context, v Viewer, raw string) (Podcast, bool, error) {
	u, err := s.Guard.ParseURL(raw)
	if err != nil {
		return Podcast{}, false, err
	}
	key := u.String()
	unlock := s.lock("url:" + key)
	defer unlock()
	id, known, err := s.Store.FeedByURL(ctx, key)
	if err != nil {
		return Podcast{}, false, err
	}
	if !known {
		f, etag, lm, _, err := s.fetchFeed(ctx, Target{URL: key})
		if err != nil {
			return Podcast{}, false, err
		}
		if v.NoExplicit && f.Explicit {
			return Podcast{}, false, ErrExplicit
		}
		if id, err = s.Store.Save(ctx, key, f, etag, lm, s.maxEpisodes()); err != nil {
			return Podcast{}, false, err
		}
		s.FetchImages(ctx, id)
	} else if v.NoExplicit {
		ex, err := s.Store.Explicit(ctx, id)
		if err != nil {
			return Podcast{}, false, err
		}
		if ex {
			return Podcast{}, false, ErrExplicit
		}
	}
	created, err := s.Store.Subscribe(ctx, v.UserID, id)
	if err != nil {
		return Podcast{}, false, err
	}
	p, err := s.Store.Show(ctx, v, id)
	return p, created, err
}

// Unsubscribe ends the viewer's subscription, and deletes the artwork of a
// show nobody subscribes to any more.
func (s *Service) Unsubscribe(ctx context.Context, v Viewer, id int64) error {
	files, err := s.Store.Unsubscribe(ctx, v.UserID, id)
	if err != nil {
		return err
	}
	for _, f := range files {
		if filepath.Base(f) == f {
			_ = os.Remove(filepath.Join(s.ImageDir, f))
		}
	}
	return nil
}

// Refresh reads a stored feed again. force skips the conditional request
// headers; a feed checked under a minute ago is left alone unless force.
func (s *Service) Refresh(ctx context.Context, id int64, byHand bool) error {
	unlock := s.lock(fmt.Sprint("id:", id))
	defer unlock()
	t, err := s.Store.Target(ctx, id)
	if err != nil {
		return err
	}
	if byHand && time.Since(time.UnixMilli(t.CheckedAt)) < refreshFloor {
		return nil
	}
	f, etag, lm, notModified, err := s.fetchFeed(ctx, t)
	if err != nil {
		_ = s.Store.Failed(ctx, id, err.Error())
		return err
	}
	if notModified {
		return s.Store.NotModified(ctx, id)
	}
	if _, err := s.Store.Save(ctx, t.URL, f, etag, lm, s.maxEpisodes()); err != nil {
		return err
	}
	s.FetchImages(ctx, id)
	return nil
}

// Run reads every subscribed feed that is due, now and then every few
// minutes, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	every := s.Interval
	if every <= 0 {
		every = DefaultInterval
	}
	tick := time.NewTicker(10 * time.Minute)
	defer tick.Stop()
	for {
		due, err := s.Store.Due(ctx, time.Now().Add(-every))
		if err != nil && ctx.Err() == nil {
			s.log().Warn("podcasts: listing feeds to refresh", "err", err)
		}
		for _, t := range due {
			if ctx.Err() != nil {
				return
			}
			rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			if err := s.Refresh(rctx, t.ID, false); err != nil && ctx.Err() == nil {
				s.log().Info("podcasts: feed refresh failed", "podcast_id", t.ID, "err", err)
			}
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// fetchFeed reads a feed with a conditional GET when it has validators.
func (s *Service) fetchFeed(ctx context.Context, t Target) (*Feed, string, string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, feedTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.URL, nil)
	if err != nil {
		return nil, "", "", false, &FeedError{"The feed URL is not valid."}
	}
	req.Header.Set("User-Agent", s.UserAgent)
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.1")
	if t.ETag != "" {
		req.Header.Set("If-None-Match", t.ETag)
	}
	if t.LastModified != "" {
		req.Header.Set("If-Modified-Since", t.LastModified)
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		if Refused(err) {
			return nil, "", "", false, &FeedError{"The server will not fetch that address: podcasts come from public internet addresses only."}
		}
		return nil, "", "", false, &FeedError{"The feed could not be reached: " + reason(err)}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified && (t.ETag != "" || t.LastModified != "") {
		return nil, "", "", true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", "", false, &FeedError{fmt.Sprintf("The feed answered %d %s.", resp.StatusCode, http.StatusText(resp.StatusCode))}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes+1))
	if err != nil {
		return nil, "", "", false, &FeedError{"The feed could not be read: " + reason(err)}
	}
	if len(body) > maxFeedBytes {
		return nil, "", "", false, &FeedError{"The feed is larger than 10 MB."}
	}
	f, err := Parse(bytesReader(body), s.maxEpisodes())
	if err != nil {
		return nil, "", "", false, &FeedError{"That address is not a podcast's RSS feed."}
	}
	return f, clip(resp.Header.Get("ETag"), 256), clip(resp.Header.Get("Last-Modified"), 64), false, nil
}

// reason is a failed request's cause without its URL.
func reason(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "it took too long to answer."
	}
	return clip(err.Error(), 200)
}

var imageTypes = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/jpg": ".jpg", "image/webp": ".webp", "image/gif": ".gif"}

// ImageTypes are the content types of saved artwork, by extension.
var ImageTypes = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".webp": "image/webp", ".gif": "image/gif"}

// FetchImages downloads the show's artwork and its newest episodes' own,
// whichever are not downloaded yet. Failures leave a show without artwork.
func (s *Service) FetchImages(ctx context.Context, podcastID int64) {
	if s.ImageDir == "" {
		return
	}
	work, err := s.Store.imagesToFetch(ctx, podcastID, episodeImages)
	if err != nil || len(work) == 0 {
		return
	}
	if err := os.MkdirAll(s.ImageDir, 0o750); err != nil {
		s.log().Warn("podcasts: artwork folder", "err", err)
		return
	}
	done := map[string]string{}
	for _, w := range work {
		if ctx.Err() != nil {
			return
		}
		name, ok := done[w.url]
		if !ok {
			name, err = s.fetchImage(ctx, w.url)
			if err != nil {
				s.log().Debug("podcasts: artwork not fetched", "podcast_id", podcastID, "err", err)
				done[w.url] = ""
				continue
			}
			done[w.url] = name
		}
		if name != "" {
			if err := s.Store.setImage(ctx, w, name); err != nil {
				return
			}
		}
	}
}

func (s *Service) fetchImage(ctx context.Context, loc string) (string, error) {
	u, err := url.Parse(loc)
	if err != nil {
		return "", err
	}
	if err := s.Guard.CheckURL(u); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", s.UserAgent)
	resp, err := s.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("artwork answered %d", resp.StatusCode)
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	ext, ok := imageTypes[mt]
	if !ok {
		return "", fmt.Errorf("artwork is %q, not an image", mt)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxImageBytes {
		return "", errors.New("artwork is larger than 5 MB")
	}
	sum := sha256.Sum256([]byte(loc))
	name := hex.EncodeToString(sum[:16]) + ext
	tmp, err := os.CreateTemp(s.ImageDir, ".art-*")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(body)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return "", errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.ImageDir, name)); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return name, nil
}

// ArtworkPath is where a saved artwork file is, and its content type; false
// for a name the service did not write.
func (s *Service) ArtworkPath(name string) (string, string, bool) {
	ctype, ok := ImageTypes[filepath.Ext(name)]
	if !ok || filepath.Base(name) != name || name == "" || name[0] == '.' {
		return "", "", false
	}
	return filepath.Join(s.ImageDir, name), ctype, true
}

// OpenStream asks the episode's host for its audio, passing a Range header
// on. The caller copies the answer and closes its body.
func (s *Service) OpenStream(ctx context.Context, loc, method, rangeHeader string) (*http.Response, error) {
	u, err := url.Parse(loc)
	if err != nil {
		return nil, err
	}
	if err := s.Guard.CheckURL(u); err != nil {
		return nil, err
	}
	if method != http.MethodHead {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, loc, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.UserAgent)
	if rangeHeader != "" && len(rangeHeader) < 200 {
		req.Header.Set("Range", rangeHeader)
	}
	return s.Stream.Do(req)
}
