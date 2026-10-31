package federation

import (
	"context"
	"sync"
	"time"

	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// MaxCatalogItems is the most titles kept from one peer's catalog.
const MaxCatalogItems = 5000

// Item is a peer's title as this server keeps and serves it: only these
// fields, each checked. Whatever else a peer sends is dropped.
type Item struct {
	ID            int64   `json:"id"`
	Kind          string  `json:"kind"`
	Title         string  `json:"title"`
	Year          *int64  `json:"year"`
	ParentID      *int64  `json:"parent_id"`
	ShowID        *int64  `json:"show_id"`
	ShowTitle     *string `json:"show_title"`
	SeasonNumber  *int64  `json:"season_number"`
	EpisodeNumber *int64  `json:"episode_number"`
	RuntimeMs     *int64  `json:"runtime_ms"`
	Resolution    *string `json:"resolution"`
	ContentRating *string `json:"content_rating"`
	ChildCount    *int    `json:"child_count"`
	HasPoster     bool    `json:"has_poster"`
	HasBackdrop   bool    `json:"has_backdrop"`
	HasThumb      bool    `json:"has_thumb"`
	AddedAt       int64   `json:"added_at"`
}

var kinds = map[string]bool{"movie": true, "show": true, "season": true, "episode": true}

// Valid checks and cleans an item in place. An item that fails is dropped,
// not the whole answer, so one bad title does not hide a friend's library.
func (it *Item) Valid() bool {
	if it.ID <= 0 || !kinds[it.Kind] || it.AddedAt < 0 {
		return false
	}
	var ok bool
	if it.Title, ok = Clean(it.Title, 200); !ok {
		return false
	}
	if it.Year != nil && (*it.Year < 1870 || *it.Year > 2200) {
		it.Year = nil
	}
	for _, id := range []**int64{&it.ParentID, &it.ShowID} {
		if *id != nil && **id <= 0 {
			*id = nil
		}
	}
	for _, n := range []**int64{&it.SeasonNumber, &it.EpisodeNumber} {
		if *n != nil && (**n < 0 || **n > 100000) {
			*n = nil
		}
	}
	if it.RuntimeMs != nil && (*it.RuntimeMs < 0 || *it.RuntimeMs > 7*24*3600*1000) {
		it.RuntimeMs = nil
	}
	if it.ChildCount != nil && (*it.ChildCount < 0 || *it.ChildCount > 100000) {
		it.ChildCount = nil
	}
	it.ShowTitle = cleanPtr(it.ShowTitle, 200)
	it.ContentRating = cleanPtr(it.ContentRating, 16)
	// Resolutions are VCRX's own names or nothing.
	if it.Resolution != nil {
		if r := policy.ResolutionRank(*it.Resolution); r >= 0 {
			v := policy.Resolutions[r]
			it.Resolution = &v
		} else {
			it.Resolution = nil
		}
	}
	return true
}

func cleanPtr(p *string, max int) *string {
	if p == nil {
		return nil
	}
	if s, ok := Clean(*p, max); ok {
		return &s
	}
	return nil
}

// Track is an audio or subtitle track of a peer's version.
type Track struct {
	ID        int64   `json:"id"`
	Index     *int64  `json:"index"`
	Codec     *string `json:"codec"`
	Language  *string `json:"language"`
	Title     *string `json:"title"`
	Channels  *int64  `json:"channels"`
	IsDefault bool    `json:"is_default"`
	IsForced  bool    `json:"is_forced"`
}

func (t *Track) valid() bool {
	if t.ID <= 0 {
		return false
	}
	if t.Index != nil && (*t.Index < 0 || *t.Index > 1000) {
		t.Index = nil
	}
	if t.Channels != nil && (*t.Channels < 0 || *t.Channels > 64) {
		t.Channels = nil
	}
	t.Codec, t.Language, t.Title = cleanPtr(t.Codec, 32), cleanPtr(t.Language, 16), cleanPtr(t.Title, 100)
	return true
}

// Version is one of a peer title's files, as far as this server's clients
// need to know it: no file name, no path.
type Version struct {
	ID          int64   `json:"id"`
	Label       *string `json:"label"`
	Resolution  *string `json:"resolution"`
	VideoCodec  *string `json:"video_codec"`
	HDRType     *string `json:"hdr_type"`
	DurationMs  *int64  `json:"duration_ms"`
	Width       *int64  `json:"width"`
	Height      *int64  `json:"height"`
	AudioTracks []Track `json:"audio_tracks"`
}

// Valid checks and cleans a version in place.
func (v *Version) Valid() bool {
	if v.ID <= 0 {
		return false
	}
	it := Item{ID: 1, Kind: "movie", Title: "x", Resolution: v.Resolution}
	it.Valid()
	v.Resolution = it.Resolution
	v.Label, v.VideoCodec, v.HDRType = cleanPtr(v.Label, 60), cleanPtr(v.VideoCodec, 32), cleanPtr(v.HDRType, 32)
	if v.DurationMs != nil && (*v.DurationMs < 0 || *v.DurationMs > 7*24*3600*1000) {
		v.DurationMs = nil
	}
	for _, n := range []**int64{&v.Width, &v.Height} {
		if *n != nil && (**n <= 0 || **n > 20000) {
			*n = nil
		}
	}
	tracks := v.AudioTracks[:0:0]
	for _, t := range v.AudioTracks {
		if len(tracks) < 32 && t.valid() {
			tracks = append(tracks, t)
		}
	}
	v.AudioTracks = tracks
	return true
}

// Detail is a peer's title page.
type Detail struct {
	Item
	Overview    *string   `json:"overview"`
	Tagline     *string   `json:"tagline"`
	Genres      []string  `json:"genres"`
	Versions    []Version `json:"versions"`
	NextEpisode *Item     `json:"next_episode"`
}

// Valid checks and cleans a detail in place.
func (d *Detail) Valid() bool {
	if !d.Item.Valid() {
		return false
	}
	d.Overview, d.Tagline = cleanPtr(d.Overview, 4000), cleanPtr(d.Tagline, 300)
	genres := []string{}
	for _, g := range d.Genres {
		if c, ok := Clean(g, 40); ok && len(genres) < 12 {
			genres = append(genres, c)
		}
	}
	d.Genres = genres
	versions := []Version{}
	for _, v := range d.Versions {
		if len(versions) < 16 && v.Valid() {
			versions = append(versions, v)
		}
	}
	d.Versions = versions
	if d.NextEpisode != nil && !d.NextEpisode.Valid() {
		d.NextEpisode = nil
	}
	return true
}

// Catalog is what a peer shares with this server: its name and its titles.
type Catalog struct {
	Server struct {
		Name string `json:"name"`
	} `json:"server"`
	Items []Item `json:"items"`
}

// Valid keeps the items that pass and reports how many were dropped.
func (c *Catalog) Valid() (dropped int) {
	c.Server.Name, _ = Clean(c.Server.Name, 64)
	items := make([]Item, 0, min(len(c.Items), MaxCatalogItems))
	for _, it := range c.Items {
		if (it.Kind == "movie" || it.Kind == "show") && len(items) < MaxCatalogItems && it.Valid() {
			items = append(items, it)
		} else {
			dropped++
		}
	}
	c.Items = items
	return dropped
}

// Cache holds each peer's catalog for TTL. Home reads it and never waits
// longer than its own short deadline: a peer that is offline or slow shows as
// such, and the rest of Home, local and other peers, is unaffected.
type Cache struct {
	TTL time.Duration
	// Fetch loads one peer's catalog. It runs in the background with its
	// own deadline, never on a request's.
	Fetch func(ctx context.Context, p Peer) (Catalog, error)

	mu      sync.Mutex
	entries map[int64]*entry
}

type entry struct {
	cat     Catalog
	err     error
	fetched time.Time
	running chan struct{}
}

// Status is a peer's catalog as Home gets it.
type Status struct {
	Catalog Catalog
	Err     error
	// Pending: no answer yet from a first fetch still running.
	Pending bool
}

// Get returns peer p's catalog, starting a refresh when it is older than
// TTL. It waits at most wait for a refresh, then answers with what it has.
func (c *Cache) Get(p Peer, wait time.Duration) Status {
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[int64]*entry{}
	}
	e := c.entries[p.ID]
	if e == nil {
		e = &entry{}
		c.entries[p.ID] = e
	}
	fresh := !e.fetched.IsZero() && time.Since(e.fetched) < c.ttl(e)
	if !fresh && e.running == nil {
		done := make(chan struct{})
		e.running = done
		go c.refresh(p, e, done)
	}
	running, have := e.running, !e.fetched.IsZero()
	c.mu.Unlock()
	if running != nil {
		// A stale answer is worth a short wait for a fresh one, so a peer
		// that went down shows as down; past the wait, what there is.
		select {
		case <-running:
		case <-time.After(wait):
			if !have {
				return Status{Pending: true}
			}
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return Status{Catalog: e.cat, Err: e.err}
}

// ttl is shorter after a failure, so a peer that comes back shows up soon.
func (c *Cache) ttl(e *entry) time.Duration {
	if e.err != nil {
		return min(c.TTL, time.Minute)
	}
	return c.TTL
}

func (c *Cache) refresh(p Peer, e *entry, done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cat, err := c.Fetch(ctx, p)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries[p.ID] == e {
		e.cat, e.err, e.fetched = cat, err, time.Now()
		if err != nil {
			e.cat = Catalog{}
		}
	}
	e.running = nil
	close(done)
}

// Forget drops a peer's catalog: it was unlinked.
func (c *Cache) Forget(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, id)
}
