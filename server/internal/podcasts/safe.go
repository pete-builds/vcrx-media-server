// Package podcasts is podcast subscriptions: RSS feeds an account user adds by
// URL, kept once per server, read again every few hours, and played through
// the server so clients never contact a podcast's host.
//
// A feed URL is typed by a user, not the admin, so everything this package
// fetches goes through Guard: http or https only, public addresses only,
// checked on the address each connection really dials (after DNS, so a name
// that resolves to the LAN, or one that rebinds, is refused too), at most a few
// redirects, each checked the same way, and bounded sizes and times.
package podcasts

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// ErrRefused is a URL or address the guard will not fetch.
var ErrRefused = errors.New("refused")

// maxRedirects is how many redirects a fetch follows.
const maxRedirects = 5

// Guard decides where the server may connect on a user's behalf.
type Guard struct {
	// AllowHosts are host:port pairs (or bare hosts, any port) that skip the
	// public address check. For development and tests only: scripts/dev-server.sh
	// serves its sample feed on loopback. Empty in production.
	AllowHosts []string
}

var refusedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64 can reach IPv4 private space
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"), // 6to4 embeds an IPv4 address
	netip.MustParsePrefix("fec0::/10"), // deprecated site-local
}

// CheckIP refuses every address that is not public unicast: loopback,
// private, link-local, carrier-grade NAT, multicast, unspecified, and the
// translation prefixes that can lead back into them.
func CheckIP(a netip.Addr) error {
	a = a.Unmap()
	if !a.IsValid() || !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() ||
		a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() {
		return fmt.Errorf("%w: %s is not a public address", ErrRefused, a)
	}
	for _, p := range refusedPrefixes {
		if p.Contains(a) {
			return fmt.Errorf("%w: %s is not a public address", ErrRefused, a)
		}
	}
	return nil
}

func (g Guard) allowed(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	for _, h := range g.AllowHosts {
		if strings.EqualFold(h, hostport) || strings.EqualFold(h, host) {
			return true
		}
	}
	return false
}

// CheckURL refuses a URL that is not http(s), carries credentials, or names
// an IP address that is not public. Names are checked again at dial time.
func (g Guard) CheckURL(u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: only http and https URLs", ErrRefused)
	}
	if u.Host == "" || u.User != nil {
		return fmt.Errorf("%w: the URL needs a host and no user name", ErrRefused)
	}
	if g.allowed(u.Host) {
		return nil
	}
	if a, err := netip.ParseAddr(strings.Trim(u.Hostname(), "[]")); err == nil {
		return CheckIP(a)
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || !strings.Contains(host, ".") {
		return fmt.Errorf("%w: %s is not a public host name", ErrRefused, host)
	}
	return nil
}

// ParseURL parses and checks a feed URL a user typed.
func (g Guard) ParseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2048 {
		return nil, fmt.Errorf("%w: the URL is too long", ErrRefused)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: not a URL", ErrRefused)
	}
	u.Fragment = ""
	if err := g.CheckURL(u); err != nil {
		return nil, err
	}
	return u, nil
}

// dialCheck runs on the address each connection is about to dial.
func (g Guard) dialCheck(hostport string, dialed string) error {
	if g.allowed(hostport) {
		return nil
	}
	ap, err := netip.ParseAddrPort(dialed)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRefused, err)
	}
	return CheckIP(ap.Addr())
}

// Client is an HTTP client whose every connection passes the guard. timeout
// bounds connecting and the response headers; callers bound the body.
func (g Guard) Client(timeout time.Duration) *http.Client {
	tr := &http.Transport{
		Proxy:                 nil, // an environment proxy would dial for us, unchecked
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          8,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		d := &net.Dialer{Timeout: timeout, Control: func(_, dialed string, _ syscall.RawConn) error {
			return g.dialCheck(addr, dialed)
		}}
		return d.DialContext(ctx, network, addr)
	}
	return &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("%w: more than %d redirects", ErrRefused, maxRedirects)
		}
		return g.CheckURL(req.URL)
	}}
}

// Refused reports whether err came from the guard.
func Refused(err error) bool { return errors.Is(err, ErrRefused) }
