package live

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// SourceSpec is a playlist from vcrx.yaml: an http(s) URL, or an absolute path
// to a file on the server, and its XMLTV guide the same way ("" for none).
type SourceSpec struct {
	Name     string
	Location string
	Guide    string
	// MaxStreams is the playlist's own cap on channels pulled at once; 0 is none.
	MaxStreams int
}

// Refresher reads each playlist when the server starts and then every
// Interval, caches channel logos under LogoDir, and reads each guide after
// its playlist and then every GuideInterval.
type Refresher struct {
	Store         *Store
	Sources       []SourceSpec
	Interval      time.Duration
	GuideInterval time.Duration
	LogoDir       string
	Log           *slog.Logger
	// Playlists fetches playlist URLs. The admin chose them, so they may be on
	// the LAN (a tuner or a playlist proxy there). Nil uses a client with a timeout.
	Playlists *http.Client
	// Logos fetches logo URLs, which come from inside a playlist the admin
	// may not have written: public addresses only (PublicOnlyClient).
	Logos *http.Client

	ids  map[string]int64
	maxs map[int64]int
}

// SourceMax is a source's max_streams, 0 for none.
func (r *Refresher) SourceMax(id int64) int { return r.maxs[id] }

// Limits on what a refresh downloads.
const (
	maxPlaylistBytes = 32 << 20
	maxLogoBytes     = 1 << 20
)

// DefaultInterval is how often playlists are read when vcrx.yaml does not say.
const DefaultInterval = 12 * time.Hour

// Register records the sources from vcrx.yaml, matched by name.
func (r *Refresher) Register(ctx context.Context) error {
	r.ids, r.maxs = map[string]int64{}, map[int64]int{}
	for _, src := range r.Sources {
		id, err := r.Store.EnsureSource(ctx, src.Name, src.Location, src.Guide)
		if err != nil {
			return fmt.Errorf("live_tv source %q: %w", src.Name, err)
		}
		r.ids[src.Name], r.maxs[id] = id, src.MaxStreams
	}
	return nil
}

// Start registers the sources, then refreshes them now and every Interval
// until ctx ends.
func (r *Refresher) Start(ctx context.Context) error {
	if err := r.Register(ctx); err != nil {
		return err
	}
	if len(r.Sources) == 0 {
		return nil
	}
	every, guides := r.Interval, r.GuideInterval
	if every <= 0 {
		every = DefaultInterval
	}
	if guides <= 0 {
		guides = DefaultGuideInterval
	}
	go func() {
		playlists, guideTick := time.NewTicker(every), time.NewTicker(guides)
		defer playlists.Stop()
		defer guideTick.Stop()
		// The first guides wait for the first playlists: they match its channels.
		r.RefreshAll(ctx)
		r.RefreshGuides(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-playlists.C:
				r.RefreshAll(ctx)
			case <-guideTick.C:
				r.RefreshGuides(ctx)
			}
		}
	}()
	return nil
}

// RefreshAll refreshes every source's playlist in turn.
func (r *Refresher) RefreshAll(ctx context.Context) {
	for _, src := range r.Sources {
		if err := r.Refresh(ctx, r.ids[src.Name]); err != nil && ctx.Err() == nil {
			r.log().Warn("live tv: playlist refresh failed", "source", src.Name, "err", err)
		}
	}
}

// RefreshGuides refreshes every source's guide in turn.
func (r *Refresher) RefreshGuides(ctx context.Context) {
	for _, src := range r.Sources {
		if src.Guide == "" {
			continue
		}
		if err := r.RefreshGuide(ctx, r.ids[src.Name]); err != nil && ctx.Err() == nil {
			r.log().Warn("live tv: guide refresh failed", "source", src.Name, "err", err)
		}
	}
}

// Refresh reads one source's playlist into the channels table and fetches
// any logos not cached yet. A failure is recorded on the source and leaves
// its channels as they were.
func (r *Refresher) Refresh(ctx context.Context, sourceID int64) error {
	loc, err := r.Store.SourceURL(ctx, sourceID)
	if err != nil {
		return err
	}
	entries, truncated, err := r.read(ctx, loc)
	if err != nil {
		_ = r.Store.Failed(ctx, sourceID, err.Error())
		return err
	}
	if err := r.Store.Sync(ctx, sourceID, entries); err != nil {
		return err
	}
	if truncated {
		_ = r.Store.Failed(ctx, sourceID, fmt.Sprintf("the playlist lists more than %d channels; only the first %d were kept", MaxEntries, MaxEntries))
	}
	r.fetchLogos(ctx, sourceID)
	return nil
}

func (r *Refresher) read(ctx context.Context, loc string) ([]Entry, bool, error) {
	body, err := r.open(ctx, loc, "playlist")
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = body.Close() }()
	return Parse(io.LimitReader(body, maxPlaylistBytes))
}

func (r *Refresher) fetchLogos(ctx context.Context, sourceID int64) {
	if r.Logos == nil || r.LogoDir == "" {
		return
	}
	work, err := r.Store.logosToFetch(ctx, sourceID)
	if err != nil {
		r.log().Warn("live tv: listing logos", "err", err)
		return
	}
	if err := os.MkdirAll(r.LogoDir, 0o750); err != nil {
		r.log().Warn("live tv: logo folder", "err", err)
		return
	}
	failed := 0
	for _, w := range work {
		if ctx.Err() != nil {
			return
		}
		file, err := r.fetchLogo(ctx, w.URL)
		if err != nil {
			failed++
			continue
		}
		if err := r.Store.setLogo(ctx, w.ID, w.URL, file); err != nil {
			r.log().Warn("live tv: saving logo", "err", err)
			return
		}
	}
	if failed > 0 {
		r.log().Info("live tv: some channel logos could not be fetched", "source_id", sourceID, "failed", failed, "of", len(work))
	}
}

// logoTypes are the image types a logo may be, and the extension it is saved with.
var logoTypes = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp", "image/gif": ".gif"}

// fetchLogo downloads one logo into LogoDir under a name made from its URL,
// so two channels with one logo share the file.
func (r *Refresher) fetchLogo(ctx context.Context, loc string) (string, error) {
	if !strings.HasPrefix(loc, "http://") && !strings.HasPrefix(loc, "https://") {
		return "", errors.New("logo is not http(s)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
	if err != nil {
		return "", err
	}
	resp, err := r.Logos.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("logo answered %d", resp.StatusCode)
	}
	mt, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	ext, ok := logoTypes[mt]
	if !ok {
		return "", fmt.Errorf("logo is %q, not an image", mt)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLogoBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > maxLogoBytes {
		return "", errors.New("logo is larger than 1 MB")
	}
	sum := sha256.Sum256([]byte(loc))
	name := hex.EncodeToString(sum[:16]) + ext
	tmp, err := os.CreateTemp(r.LogoDir, ".logo-*")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(body)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		return "", errors.Join(werr, cerr)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(r.LogoDir, name)); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return name, nil
}

func (r *Refresher) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// redact keeps a failed request's reason without its URL, which may carry
// the account's credentials.
func redact(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}
	return "the request failed"
}

// PublicOnlyClient fetches from public internet addresses only: never
// loopback, private, link-local, carrier-grade NAT, multicast or unspecified,
// checked on the address every connection actually dials, after DNS. Redirects
// are refused. A logo URL comes from inside a playlist, so without this a
// public playlist could make the server call into the LAN.
func PublicOnlyClient(timeout time.Duration) *http.Client {
	d := &net.Dialer{Timeout: timeout, Control: func(_, address string, _ syscall.RawConn) error {
		return checkPublic(address)
	}}
	tr := &http.Transport{DialContext: d.DialContext, Proxy: nil, TLSHandshakeTimeout: timeout, ResponseHeaderTimeout: timeout,
		MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second}
	return &http.Client{Transport: tr, Timeout: 2 * timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

func checkPublic(address string) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return err
	}
	a := ap.Addr().Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || cgnat.Contains(a) {
		return fmt.Errorf("%s is not a public address", a)
	}
	return nil
}
