package overlay

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestBadgesFollowPetesKometaSetup(t *testing.T) {
	movies := []Spec{
		{Type: "rating", Position: "top_left", Sources: []string{"imdb"}},
		{Type: "resolution", Position: "bottom_right"}, // 4K only by default
	}
	f := Facts{Ratings: map[string]float64{"imdb": 7.86, "tmdb": 7.1}, Resolution: "4K", HDR: "dolby_vision", DVWithHDR: true}
	bs := Badges(movies, f)
	if len(bs) != 2 || bs[0] != (Badge{"rating", "top_left", "7.9", "IMDb"}) || bs[1] != (Badge{"resolution", "bottom_right", "4K", "DV HDR"}) {
		t.Fatalf("4K DV movie: %+v", bs)
	}
	// 1080p gets no resolution badge; no IMDb rating means no rating badge
	// (sources lists imdb only, as in Kometa's rating1: audience with imdb).
	bs = Badges(movies, Facts{Ratings: map[string]float64{"tmdb": 7.1}, Resolution: "1080p"})
	if len(bs) != 0 {
		t.Fatalf("1080p without IMDb: %+v", bs)
	}
	// Default sources fall back to TMDb.
	bs = Badges([]Spec{{Type: "rating"}}, Facts{Ratings: map[string]float64{"tmdb": 6.0}})
	if len(bs) != 1 || bs[0].Sub != "TMDb" || bs[0].Pos != "top_left" {
		t.Fatalf("fallback: %+v", bs)
	}
	for hdr, want := range map[string]string{"hdr10": "HDR", "hdr10+": "HDR10+", "dolby_vision": "DV", "": ""} {
		bs = Badges([]Spec{{Type: "resolution"}}, Facts{Resolution: "4K", HDR: hdr})
		if bs[0].Sub != want {
			t.Errorf("%q: sub %q, want %q", hdr, bs[0].Sub, want)
		}
	}
}

func TestRibbonNeedsRatingAndVotes(t *testing.T) {
	tv := []Spec{{Type: "ribbon"}}
	cases := []struct {
		f    Facts
		want bool
	}{
		{Facts{Ratings: map[string]float64{"imdb": 8.6}, Votes: map[string]int64{"imdb": 300000}}, true},
		{Facts{Ratings: map[string]float64{"imdb": 8.6}, Votes: map[string]int64{"imdb": 900}}, false}, // too few votes
		{Facts{Ratings: map[string]float64{"imdb": 7.4}, Votes: map[string]int64{"imdb": 300000}}, false},
		// The first source the item has decides; TMDb has no vote count here.
		{Facts{Ratings: map[string]float64{"tmdb": 8.2}}, true},
	}
	for i, c := range cases {
		got := len(Badges(tv, c.f)) == 1
		if got != c.want {
			t.Errorf("case %d: ribbon=%v, want %v", i, got, c.want)
		}
	}
	if bs := Badges([]Spec{{Type: "ribbon", Text: "Fan favourite", MinRating: 9}}, Facts{Ratings: map[string]float64{"tmdb": 9.1}}); bs[0].Text != "FAN FAVOURITE" {
		t.Fatalf("custom text: %+v", bs)
	}
}

func TestSignatureChangesWithFacts(t *testing.T) {
	sp := []Spec{{Type: "rating"}}
	a := Signature(Badges(sp, Facts{Ratings: map[string]float64{"imdb": 7.1}}))
	b := Signature(Badges(sp, Facts{Ratings: map[string]float64{"imdb": 7.2}}))
	if a == "" || a == b {
		t.Fatalf("signatures %q %q", a, b)
	}
	if Signature(nil) != "" {
		t.Fatal("no badges must mean no signature, so the clean poster is shared")
	}
}

func TestDrawPaintsTheChosenCorners(t *testing.T) {
	fontBytes, err := os.ReadFile(filepath.Join("..", "..", "web", "static", "fonts", "rajdhani_bold.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{Font: fontBytes}
	img := image.NewRGBA(image.Rect(0, 0, 600, 900))
	bg := color.RGBA{0x30, 0x60, 0x90, 0xff}
	draw.Draw(img, img.Bounds(), &image.Uniform{bg}, image.Point{}, draw.Src)
	bs := []Badge{{"rating", "top_left", "8.4", "IMDb"}, {"resolution", "bottom_right", "4K", "DV HDR"}, {"ribbon", "bottom_left", "TOP RATED", ""}}
	if err := s.Draw(img, bs); err != nil {
		t.Fatal(err)
	}
	changed := func(x0, y0, x1, y1 int) bool {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				if img.RGBAAt(x, y) != bg {
					return true
				}
			}
		}
		return false
	}
	if !changed(18, 18, 120, 70) {
		t.Error("top-left rating badge missing")
	}
	if !changed(480, 830, 582, 882) {
		t.Error("bottom-right resolution badge missing")
	}
	// The band's centre sits 150 design pixels in from the corner on the diagonal.
	if !changed(70, 790, 110, 830) {
		t.Error("bottom-left ribbon missing")
	}
	if changed(250, 350, 350, 550) || changed(480, 18, 582, 70) {
		t.Error("paint outside the badges")
	}
	if out := os.Getenv("VCRX_OVERLAY_PNG"); out != "" {
		fh, _ := os.Create(out)
		_ = png.Encode(fh, img)
		_ = fh.Close()
	}
}
