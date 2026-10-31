package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Response size limits. A peer is a friend's server, but its answers are
// treated like any other input from the network: bounded before they are
// read, validated after.
const (
	MaxCatalogBytes  = 4 << 20
	MaxJSONBytes     = 1 << 20
	MaxArtworkBytes  = 5 << 20
	MaxPlaylistBytes = 1 << 20
	MaxSubtitleBytes = 5 << 20
)

// ErrTooLarge is an answer over its limit.
var ErrTooLarge = errors.New("the friend's server sent more than this server accepts")

// ErrBadPath is a request path the client refuses to send: only the fixed
// API paths this package builds are ever requested.
var ErrBadPath = errors.New("refused to request that path from a friend's server")

// Error is a peer's error answer, with its code and message checked.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("friend's server answered %d %s", e.Status, e.Code)
}

// Unlinked reports whether the peer no longer accepts this server's token:
// its admin revoked the link, or it expired.
func (e *Error) Unlinked() bool { return e.Status == http.StatusUnauthorized }

// Client talks to linked peers, and only to them. It never follows a
// redirect, never uses a proxy from the environment, dials only addresses
// that pass the Guard after DNS, and requests only paths under /api/v1/
// made of plain characters, so a peer's address cannot be bent into a fetch
// of anything else.
type Client struct {
	Guard Guard
	// JSON is for small API calls, with an overall deadline; Streams has
	// none, since a film streams for hours, but bounded header waits.
	JSON, Streams *http.Client
	// UserAgent names this server to the peer.
	UserAgent string
}

// NewClient builds a Client with the guard wired into its dialer.
func NewClient(g Guard, userAgent string) *Client {
	dial := &net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error { return g.dialCheck(address) }}
	transport := func() *http.Transport {
		return &http.Transport{
			Proxy:                  nil, // never an environment proxy: the peer's address is the only destination
			DialContext:            dial.DialContext,
			TLSHandshakeTimeout:    5 * time.Second,
			ResponseHeaderTimeout:  15 * time.Second,
			MaxResponseHeaderBytes: 64 << 10,
			MaxIdleConnsPerHost:    8,
			IdleConnTimeout:        90 * time.Second,
			DisableCompression:     true, // byte ranges must stay byte ranges
		}
	}
	noRedirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		Guard:     g,
		JSON:      &http.Client{Transport: transport(), Timeout: 8 * time.Second, CheckRedirect: noRedirect},
		Streams:   &http.Client{Transport: transport(), CheckRedirect: noRedirect},
		UserAgent: userAgent,
	}
}

// safePath is every path the client may request: /api/v1/ then plain path
// characters, with no dot segments, no encoding, and no query or fragment
// (queries go in separately, encoded).
var safePath = regexp.MustCompile(`^/api/v1/[A-Za-z0-9_\-/.]+$`)

// URL joins a peer's stored origin with a path this package built. base is
// parsed again, so a row edited by hand cannot smuggle a path in either.
func (c *Client) URL(base, path string, q url.Values) (string, error) {
	if !safePath.MatchString(path) || strings.Contains(path, "//") || strings.Contains(path, "/.") {
		return "", ErrBadPath
	}
	origin, err := c.Guard.ParseBase(base)
	if err != nil || origin != base {
		return "", ErrBadAddress
	}
	u := origin + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u, nil
}

// Request is one call to a peer.
type Request struct {
	Base, Token, Method, Path string
	Query                     url.Values
	Body                      any
	// Header carries the few request headers that pass through to a peer:
	// Range and If-Range for streams. Nothing else is copied.
	Header http.Header
}

// Do sends a request and returns the peer's response, unread. The caller
// closes its body. Streams use it directly; JSON calls use GetJSON.
func (c *Client) Do(ctx context.Context, hc *http.Client, rq Request) (*http.Response, error) {
	u, err := c.URL(rq.Base, rq.Path, rq.Query)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if rq.Body != nil {
		b, err := json.Marshal(rq.Body)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	method := rq.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if rq.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if rq.Token != "" {
		req.Header.Set("Authorization", "Bearer "+rq.Token)
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept-Encoding", "identity")
	for _, h := range []string{"Range", "If-Range"} {
		if v := rq.Header.Get(h); v != "" && len(v) < 200 {
			req.Header.Set(h, v)
		}
	}
	return hc.Do(req)
}

// GetJSON sends a request and decodes a 2xx JSON answer of at most limit
// bytes into dst. A non-2xx answer comes back as *Error.
func (c *Client) GetJSON(ctx context.Context, rq Request, limit int64, dst any) error {
	resp, err := c.Do(ctx, c.JSON, rq)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ReadError(resp)
	}
	if resp.StatusCode == http.StatusNoContent || dst == nil {
		return nil
	}
	if !strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "application/json") {
		return fmt.Errorf("friend's server answered %s, not JSON", clipType(resp.Header.Get("Content-Type")))
	}
	b, err := ReadLimited(resp.Body, limit)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

// ReadLimited reads at most limit bytes, and fails rather than truncates.
func ReadLimited(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, ErrTooLarge
	}
	return b, nil
}

var errorCode = regexp.MustCompile(`^[a-z_]{1,40}$`)

// ReadError turns a peer's error answer into *Error. The code must look like
// one of VCRX's codes and the message is cleaned; anything else is replaced,
// so a peer cannot put arbitrary text in front of this server's users.
func ReadError(resp *http.Response) error {
	out := &Error{Status: resp.StatusCode, Code: "peer_error", Message: "The friend's server could not do that."}
	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if b, err := ReadLimited(resp.Body, 16<<10); err == nil && json.Unmarshal(b, &body) == nil {
		if errorCode.MatchString(body.Error) {
			out.Code = body.Error
		}
		if m, ok := Clean(body.Message, 300); ok {
			out.Message = m
		}
	}
	return out
}

// Clean makes a peer's text safe to keep: valid UTF-8, no control characters,
// trimmed, at most max characters. ok is false when nothing is left.
func Clean(s string, max int) (string, bool) {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0x2028 || r == 0x2029 || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			// Control characters, line separators and bidirectional overrides,
			// which can make a title read as something it is not.
			if r == '\n' || r == '\t' {
				r = ' '
			} else {
				continue
			}
		}
		if n >= max {
			break
		}
		b.WriteRune(r)
		n++
	}
	out := strings.TrimSpace(b.String())
	return out, out != ""
}

func clipType(t string) string {
	t, _ = Clean(t, 60)
	if t == "" {
		return "nothing"
	}
	return t
}
