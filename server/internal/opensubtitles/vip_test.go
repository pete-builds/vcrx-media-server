package opensubtitles

import "testing"

// Login names the API host to use next; only an opensubtitles.com host is
// followed, so a tampered answer cannot send the API key elsewhere.
func TestVIPBaseOnlyFollowsOpenSubtitlesHosts(t *testing.T) {
	for in, want := range map[string]string{
		"vip-api.opensubtitles.com":          "https://vip-api.opensubtitles.com/api/v1",
		"api.opensubtitles.com":              "https://api.opensubtitles.com/api/v1",
		"https://vip-api.opensubtitles.com/": "https://vip-api.opensubtitles.com/api/v1",
		"evil.example":                       "",
		"opensubtitles.com.evil.example":     "",
		"api.opensubtitles.com@evil.example": "",
		"api.opensubtitles.com:8443":         "",
		"":                                   "",
	} {
		if got := vipBase(in); got != want {
			t.Errorf("vipBase(%q) = %q, want %q", in, got, want)
		}
	}
}
