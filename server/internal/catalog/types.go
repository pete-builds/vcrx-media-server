// Package catalog answers the browse queries clients make: libraries, item pages,
// details, children, search and home rows. Every query is filtered by the
// caller's policy; the JSON shapes match server/api/openapi.yaml.
package catalog

type Library struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	ItemCount int    `json:"item_count"`
}

type WatchState struct {
	PositionMs    int64  `json:"position_ms"`
	DurationMs    *int64 `json:"duration_ms"`
	Played        bool   `json:"played"`
	PlayCount     int    `json:"play_count"`
	LastPlayedAt  *int64 `json:"last_played_at"`
	UnplayedCount *int   `json:"unplayed_count"`
}

type ItemSummary struct {
	ID               int64      `json:"id"`
	Kind             string     `json:"kind"`
	LibraryID        int64      `json:"library_id"`
	Title            string     `json:"title"`
	Year             *int64     `json:"year"`
	ParentID         *int64     `json:"parent_id"`
	ShowID           *int64     `json:"show_id"`
	ShowTitle        *string    `json:"show_title"`
	SeasonNumber     *int64     `json:"season_number"`
	EpisodeNumber    *int64     `json:"episode_number"`
	EpisodeNumberEnd *int64     `json:"episode_number_end"`
	RuntimeMs        *int64     `json:"runtime_ms"`
	Resolution       *string    `json:"resolution"`
	ChildCount       *int       `json:"child_count"`
	HasPoster        bool       `json:"has_poster"`
	HasBackdrop      bool       `json:"has_backdrop"`
	HasThumb         bool       `json:"has_thumb"`
	AddedAt          int64      `json:"added_at"`
	Watch            WatchState `json:"watch"`
}

type Track struct {
	ID           int64   `json:"id"`
	Kind         string  `json:"kind"`
	Index        *int64  `json:"index"`
	Codec        *string `json:"codec"`
	Language     *string `json:"language"`
	Title        *string `json:"title"`
	Channels     *int64  `json:"channels"`
	IsDefault    bool    `json:"is_default"`
	IsForced     bool    `json:"is_forced"`
	IsExternal   bool    `json:"is_external"`
	IsImageBased bool    `json:"is_image_based"`
}

type Version struct {
	ID             int64   `json:"id"`
	Label          *string `json:"label"`
	FileName       string  `json:"file_name"`
	Container      *string `json:"container"`
	Size           int64   `json:"size"`
	Bitrate        *int64  `json:"bitrate"`
	DurationMs     *int64  `json:"duration_ms"`
	VideoCodec     *string `json:"video_codec"`
	VideoProfile   *string `json:"video_profile"`
	BitDepth       *int64  `json:"bit_depth"`
	Width          *int64  `json:"width"`
	Height         *int64  `json:"height"`
	Resolution     *string `json:"resolution"`
	HDRType        *string `json:"hdr_type"`
	AudioTracks    []Track `json:"audio_tracks"`
	SubtitleTracks []Track `json:"subtitle_tracks"`

	// Server-side only.
	VideoLevel  *int64   `json:"-"`
	FrameRate   *float64 `json:"-"`
	DVProfile   *int64   `json:"-"`
	DVCompatID  *int64   `json:"-"`
	ProbeStatus string   `json:"-"`
	ItemID      int64    `json:"-"`
	LibraryID   int64    `json:"-"`
	Path        string   `json:"-"`
	VideoTracks []Track  `json:"-"`
}

type ItemDetail struct {
	ItemSummary
	Overview      *string            `json:"overview"`
	Tagline       *string            `json:"tagline"`
	ContentRating *string            `json:"content_rating"`
	Genres        []string           `json:"genres"`
	Directors     []string           `json:"directors"`
	Studios       []string           `json:"studios"`
	Cast          []CastMember       `json:"cast"`
	Ratings       map[string]float64 `json:"ratings"`
	Versions      []Version          `json:"versions"`
	NextEpisode   *ItemSummary       `json:"next_episode"`
	Collections   []CollectionRef    `json:"collections"`
}

// CastMember is one actor in billing order, with the part they play when known.
type CastMember struct {
	Name string  `json:"name"`
	Role *string `json:"role"`
}

type HomeRow struct {
	ID        string        `json:"id"`
	Title     string        `json:"title"`
	LibraryID *int64        `json:"library_id"`
	Items     []ItemSummary `json:"items"`
}

type ListQuery struct {
	Sort       string
	Order      string
	Genre      string
	Year       int
	Decade     int // e.g. 1990 for 1990 to 1999
	Resolution string
	Unwatched  bool
	Limit      int
	Offset     int
}
