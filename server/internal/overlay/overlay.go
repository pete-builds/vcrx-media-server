// Package overlay draws badges onto posters, the way Kometa overlays Plex
// posters: a rating badge, a resolution and HDR badge, and a corner ribbon.
// The server bakes them into the poster image it serves, so every client shows
// them without knowing they exist. Badges use VCRX's own lettering rather than
// third-party logos.
package overlay

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"strconv"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/f64"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"

	"github.com/pete-builds/vcrx-media-server/server/internal/policy"
)

// Spec is one overlay as configured for a library.
type Spec struct {
	// Type is rating, resolution or ribbon.
	Type string `yaml:"type"`
	// Position is top_left, top_right, bottom_left or bottom_right. Ribbons
	// always sit in a corner; the default is bottom_right.
	Position string `yaml:"position"`
	// Sources, for rating and ribbon: rating sources in order of preference
	// (imdb, tmdb). The first one the item has is used.
	Sources []string `yaml:"sources"`
	// Resolutions, for resolution: which resolutions get a badge (4K, 1080p,
	// 720p, 576p, 480p, SD). Empty means 4K only, like Pete's Kometa setup.
	Resolutions []string `yaml:"resolutions"`
	// MinRating and MinVotes, for ribbon: the item qualifies when its rating
	// from the first available source reaches both. Defaults: 8.0 and 25000
	// votes for IMDb (votes are ignored for sources that have none).
	MinRating float64 `yaml:"min_rating"`
	MinVotes  int     `yaml:"min_votes"`
	// Text, for ribbon. Default "TOP RATED".
	Text string `yaml:"text"`
}

func (s Spec) position(def string) string {
	switch s.Position {
	case "top_left", "top_right", "bottom_left", "bottom_right":
		return s.Position
	}
	return def
}

func (s Spec) sources() []string {
	if len(s.Sources) > 0 {
		return s.Sources
	}
	return []string{"imdb", "tmdb"}
}

// Validate reports a spec the renderer would ignore.
func (s Spec) Validate() error {
	switch s.Type {
	case "rating", "resolution", "ribbon":
	default:
		return fmt.Errorf("overlay type %q: want rating, resolution or ribbon", s.Type)
	}
	if s.Position != "" && s.position("") == "" {
		return fmt.Errorf("overlay position %q: want top_left, top_right, bottom_left or bottom_right", s.Position)
	}
	for _, src := range s.Sources {
		if src != "imdb" && src != "tmdb" && src != "nfo" {
			return fmt.Errorf("overlay source %q: want imdb, tmdb or nfo", src)
		}
	}
	return nil
}

// Facts are what the badges are drawn from.
type Facts struct {
	Ratings    map[string]float64
	Votes      map[string]int64
	Resolution string // of the best version: 4K, 1080p, ...
	HDR        string // dolby_vision, hdr10, hdr10+, hlg or empty
	DVWithHDR  bool   // Dolby Vision with an HDR10 base layer
}

// Badge is one resolved overlay, ready to draw. Its fields feed the cache key.
type Badge struct {
	Kind, Pos, Text, Sub string
}

// Badges resolves specs against an item's facts. An overlay with nothing to
// show (no rating, a resolution not chosen) yields no badge.
func Badges(specs []Spec, f Facts) []Badge {
	var out []Badge
	for _, s := range specs {
		switch s.Type {
		case "rating":
			for _, src := range s.sources() {
				if v, ok := f.Ratings[src]; ok && v > 0 {
					out = append(out, Badge{Kind: "rating", Pos: s.position("top_left"), Text: strconv.FormatFloat(v, 'f', 1, 64), Sub: sourceLabel(src)})
					break
				}
			}
		case "resolution":
			want := s.Resolutions
			if len(want) == 0 {
				want = []string{"4K"}
			}
			if f.Resolution == "" || !contains(want, f.Resolution) {
				continue
			}
			sub := ""
			switch {
			case f.HDR == "dolby_vision" && f.DVWithHDR:
				sub = "DV HDR"
			case f.HDR == "dolby_vision":
				sub = "DV"
			case f.HDR == "hdr10+":
				sub = "HDR10+"
			case f.HDR != "":
				sub = "HDR"
			}
			out = append(out, Badge{Kind: "resolution", Pos: s.position("bottom_right"), Text: f.Resolution, Sub: sub})
		case "ribbon":
			minR, minV := s.MinRating, int64(s.MinVotes)
			if minR == 0 {
				minR = 8.0
			}
			for _, src := range s.sources() {
				v, ok := f.Ratings[src]
				if !ok {
					continue
				}
				votes, hasVotes := f.Votes[src]
				if src == "imdb" && minV == 0 {
					minV = 25000
				}
				if v >= minR && (!hasVotes || votes >= minV) {
					text := s.Text
					if text == "" {
						text = "TOP RATED"
					}
					out = append(out, Badge{Kind: "ribbon", Pos: s.position("bottom_right"), Text: strings.ToUpper(text)})
				}
				break
			}
		}
	}
	return out
}

func sourceLabel(src string) string {
	switch src {
	case "imdb":
		return "IMDb"
	case "tmdb":
		return "TMDb"
	}
	return strings.ToUpper(src)
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// styleVersion goes into every signature. Change it whenever the drawing
// changes (sizes, colours, fonts), or cached posters keep the old look.
const styleVersion = "style-2"

// Signature is a short key for a set of badges, so a cached poster is redrawn
// when a rating moves or a 4K version arrives. Empty means no badges.
func Signature(bs []Badge) string {
	if len(bs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(styleVersion)
	for _, x := range bs {
		fmt.Fprintf(&b, "|%s,%s,%s,%s", x.Kind, x.Pos, x.Text, x.Sub)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:6])
}

// Service looks up facts and draws badges for items in libraries that have
// overlays configured.
type Service struct {
	DB *sql.DB
	// Specs are the overlays per library id.
	Specs map[int64][]Spec
	// Font is the display face's TTF bytes (Rajdhani Bold).
	Font []byte

	once  sync.Once
	fnt   *opentype.Font
	fntOK bool
}

// Plan returns the badges for an item's poster and their signature, or nil
// when its library has no overlays or nothing applies. The resolution badge
// reads only the versions pol may play.
func (s *Service) Plan(ctx context.Context, itemID int64, pol policy.Policy) ([]Badge, string, error) {
	if s == nil || len(s.Specs) == 0 {
		return nil, "", nil
	}
	var lib int64
	var kind string
	if err := s.DB.QueryRowContext(ctx, `SELECT library_id, kind FROM items WHERE id = ?`, itemID).Scan(&lib, &kind); err != nil {
		return nil, "", err
	}
	specs := s.Specs[lib]
	if len(specs) == 0 || (kind != "movie" && kind != "show") {
		return nil, "", nil
	}
	f, err := s.facts(ctx, itemID, kind, pol)
	if err != nil {
		return nil, "", err
	}
	bs := Badges(specs, f)
	return bs, Signature(bs), nil
}

func (s *Service) facts(ctx context.Context, itemID int64, kind string, pol policy.Policy) (Facts, error) {
	f := Facts{Ratings: map[string]float64{}, Votes: map[string]int64{}}
	if err := s.ratings(ctx, itemID, &f); err != nil {
		return f, err
	}
	if kind != "movie" {
		return f, nil
	}
	// The best version decides the resolution badge, as Kometa reads Plex's
	// best media; but only among the versions this viewer may play.
	var res, hdr sql.NullString
	var compat sql.NullInt64
	vc, vargs := pol.VersionCond("v")
	err := s.DB.QueryRowContext(ctx, `SELECT v.resolution, v.hdr_type, v.dv_compat_id FROM media_versions v
		WHERE v.item_id = ? AND `+vc+` ORDER BY COALESCE(v.height, 0) DESC,
		CASE v.hdr_type WHEN 'dolby_vision' THEN 0 WHEN 'hdr10+' THEN 1 WHEN 'hdr10' THEN 2 WHEN 'hlg' THEN 3 ELSE 4 END LIMIT 1`,
		append([]any{itemID}, vargs...)...).Scan(&res, &hdr, &compat)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return f, err
	}
	f.Resolution, f.HDR = res.String, hdr.String
	// Compatibility id 1 is an HDR10 base layer (profile 8.1); 6 is profile 8.6.
	f.DVWithHDR = compat.Valid && (compat.Int64 == 1 || compat.Int64 == 6)
	return f, nil
}

func (s *Service) ratings(ctx context.Context, itemID int64, f *Facts) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT source, value, votes FROM item_ratings WHERE item_id = ?`, itemID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var src string
		var v float64
		var votes sql.NullInt64
		if err := rows.Scan(&src, &v, &votes); err != nil {
			return err
		}
		f.Ratings[src] = v
		if votes.Valid {
			f.Votes[src] = votes.Int64
		}
	}
	return rows.Err()
}

func (s *Service) face(size float64) (font.Face, error) {
	s.once.Do(func() {
		if f, err := opentype.Parse(s.Font); err == nil {
			s.fnt, s.fntOK = f, true
		}
	})
	if !s.fntOK {
		return nil, fmt.Errorf("overlay font did not parse")
	}
	return opentype.NewFace(s.fnt, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
}

// Poster adapts Plan and Draw to the artwork service.
func (s *Service) Poster(ctx context.Context, itemID int64, pol policy.Policy) (string, func(*image.RGBA) error, error) {
	bs, sig, err := s.Plan(ctx, itemID, pol)
	if err != nil || sig == "" {
		return "", nil, err
	}
	return sig, func(img *image.RGBA) error { return s.Draw(img, bs) }, nil
}

// Draw paints badges onto img in place. Sizes follow the poster's width, on a
// 1000-pixel-wide design canvas like Kometa's.
func (s *Service) Draw(img *image.RGBA, bs []Badge) error {
	w := float64(img.Bounds().Dx())
	// Badges are drawn 35% larger than the design canvas so they stay
	// readable at the size a TV shows a poster row.
	k := w / 1000 * 1.35
	margin := 26 * k
	for _, b := range bs {
		var err error
		switch b.Kind {
		case "ribbon":
			// Ribbons are already large; they keep the design size.
			err = s.ribbon(img, b, w/1000)
		default:
			err = s.pill(img, b, k, margin)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

var (
	ink    = color.RGBA{0xf2, 0xf2, 0xf4, 0xff}
	muted  = color.RGBA{0xb4, 0xb4, 0xbc, 0xff}
	orange = color.RGBA{0xff, 0x5f, 0x00, 0xff}
	gold   = color.RGBA{0xf5, 0xc5, 0x18, 0xff}
	panel  = color.NRGBA{0x08, 0x08, 0x0a, 0xd8}
	black  = color.RGBA{0, 0, 0, 0xff}
)

// pill draws a rounded dark badge: a star and score with the source for a
// rating, or the resolution with an orange HDR tag.
func (s *Service) pill(img *image.RGBA, b Badge, k, margin float64) error {
	h := 92 * k
	big, err := s.face(64 * k)
	if err != nil {
		return err
	}
	small, err := s.face(34 * k)
	if err != nil {
		return err
	}
	pad := 24 * k
	gap := 14 * k
	mainW := measure(big, b.Text)
	subW := 0.0
	if b.Sub != "" {
		subW = measure(small, b.Sub)
	}
	star := 0.0
	if b.Kind == "rating" {
		star = 54 * k
	}
	tagPad := 0.0
	if b.Kind == "resolution" && b.Sub != "" {
		tagPad = 14 * k // the HDR tag sits in its own orange chip
	}
	width := pad + star + mainW + pad
	if b.Sub != "" {
		width += gap + subW + 2*tagPad
	}
	x, y := corner(img.Bounds(), b.Pos, width, h, margin)
	roundRect(img, x, y, width, h, 16*k, panel)
	cx := x + pad
	base := y + h/2 + 22*k // baseline for the big text
	if b.Kind == "rating" {
		drawStar(img, cx+star/2-4*k, y+h/2, 24*k, gold)
		cx += star
	}
	text(img, big, cx, base, b.Text, ink)
	cx += mainW + gap
	if b.Sub != "" {
		if b.Kind == "resolution" {
			roundRect(img, cx, y+18*k, subW+2*tagPad, h-36*k, 8*k, orange)
			text(img, small, cx+tagPad, y+h/2+12*k, b.Sub, black)
		} else {
			text(img, small, cx, y+h/2+12*k, b.Sub, muted)
		}
	}
	return nil
}

// ribbon draws a diagonal orange band across a corner.
func (s *Service) ribbon(img *image.RGBA, b Badge, k float64) error {
	face, err := s.face(40 * k)
	if err != nil {
		return err
	}
	bandH := 70 * k
	bandW := 520 * k
	band := image.NewRGBA(image.Rect(0, 0, int(bandW), int(bandH)))
	draw.Draw(band, band.Bounds(), &image.Uniform{orange}, image.Point{}, draw.Src)
	tw := measure(face, b.Text)
	text(band, face, (bandW-tw)/2, bandH/2+14*k, b.Text, black)

	// Rotate 45° about the band's centre and place that centre on the corner's
	// diagonal, so the band clips cleanly against both edges.
	r := img.Bounds()
	d := 150 * k // distance from the corner along each axis
	var cx, cy, ang float64
	switch b.Pos {
	case "top_left":
		cx, cy, ang = float64(r.Min.X)+d, float64(r.Min.Y)+d, -math.Pi/4
	case "top_right":
		cx, cy, ang = float64(r.Max.X)-d, float64(r.Min.Y)+d, math.Pi/4
	case "bottom_left":
		cx, cy, ang = float64(r.Min.X)+d, float64(r.Max.Y)-d, math.Pi/4
	default:
		cx, cy, ang = float64(r.Max.X)-d, float64(r.Max.Y)-d, -math.Pi/4
	}
	cos, sin := math.Cos(ang), math.Sin(ang)
	bx, by := bandW/2, bandH/2
	// Maps band coordinates to poster coordinates.
	m := f64.Aff3{cos, -sin, cx - cos*bx + sin*by, sin, cos, cy - sin*bx - cos*by}
	xdraw.BiLinear.Transform(img, m, band, band.Bounds(), xdraw.Over, nil)
	return nil
}

func corner(r image.Rectangle, pos string, w, h, margin float64) (float64, float64) {
	x, y := float64(r.Min.X)+margin, float64(r.Min.Y)+margin
	if strings.HasSuffix(pos, "right") {
		x = float64(r.Max.X) - margin - w
	}
	if strings.HasPrefix(pos, "bottom") {
		y = float64(r.Max.Y) - margin - h
	}
	return x, y
}

func measure(face font.Face, s string) float64 {
	return float64(font.MeasureString(face, s)) / 64
}

func text(img draw.Image, face font.Face, x, baseline float64, s string, c color.Color) {
	d := font.Drawer{Dst: img, Src: &image.Uniform{c}, Face: face,
		Dot: fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(baseline * 64)}}
	d.DrawString(s)
}

func roundRect(img *image.RGBA, x, y, w, h, r float64, c color.Color) {
	b := img.Bounds()
	z := vector.NewRasterizer(b.Dx(), b.Dy())
	ox, oy := float32(x-float64(b.Min.X)), float32(y-float64(b.Min.Y))
	fw, fh, fr := float32(w), float32(h), float32(math.Min(r, math.Min(w, h)/2))
	z.MoveTo(ox+fr, oy)
	z.LineTo(ox+fw-fr, oy)
	z.QuadTo(ox+fw, oy, ox+fw, oy+fr)
	z.LineTo(ox+fw, oy+fh-fr)
	z.QuadTo(ox+fw, oy+fh, ox+fw-fr, oy+fh)
	z.LineTo(ox+fr, oy+fh)
	z.QuadTo(ox, oy+fh, ox, oy+fh-fr)
	z.LineTo(ox, oy+fr)
	z.QuadTo(ox, oy, ox+fr, oy)
	z.ClosePath()
	z.Draw(img, b, &image.Uniform{c}, b.Min)
}

func drawStar(img *image.RGBA, cx, cy, r float64, c color.Color) {
	b := img.Bounds()
	z := vector.NewRasterizer(b.Dx(), b.Dy())
	for i := 0; i < 10; i++ {
		rad := r
		if i%2 == 1 {
			rad = r * 0.45
		}
		a := -math.Pi/2 + float64(i)*math.Pi/5
		px := float32(cx + rad*math.Cos(a) - float64(b.Min.X))
		py := float32(cy + rad*math.Sin(a) - float64(b.Min.Y))
		if i == 0 {
			z.MoveTo(px, py)
		} else {
			z.LineTo(px, py)
		}
	}
	z.ClosePath()
	z.Draw(img, b, &image.Uniform{c}, b.Min)
}
