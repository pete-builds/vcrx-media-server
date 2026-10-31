// Package youtube asks YouTube whether a trailer TMDb lists can still be
// embedded. It uses YouTube's public oEmbed endpoint, which needs no key and
// is sent nothing but the video id.
package youtube

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// DefaultBaseURL is where oEmbed lives.
const DefaultBaseURL = "https://www.youtube.com"

// key is the only shape of video id sent, so nothing else reaches the URL.
var key = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// Client checks videos with oEmbed.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New() *Client {
	return &Client{BaseURL: DefaultBaseURL, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Embeddable reports whether the video can play in another site's player.
// oEmbed answers 200 for one that can, 401 or 403 when its owner turned
// embedding off and 404 when it is gone or private; those are false with no
// error. Anything else, such as no internet, is an error: the caller cannot
// tell, and keeps what it had.
func (c *Client) Embeddable(ctx context.Context, videoID string) (bool, error) {
	if !key.MatchString(videoID) {
		return false, nil
	}
	q := url.Values{"format": {"json"}, "url": {"https://www.youtube.com/watch?v=" + videoID}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/oembed?"+q.Encode(), nil)
	if err != nil {
		return false, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("youtube oembed: HTTP %d", resp.StatusCode)
}
