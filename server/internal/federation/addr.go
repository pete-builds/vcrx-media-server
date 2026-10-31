// Package federation links friends' servers (milestone 9, item 3). Each friend
// runs their own VCRX; an admin mints a server invite (an invites token whose
// principal is a whole server, marked as a peer), and the other admin pastes
// it in to link. From then on this server browses the peer's catalog and
// proxies its streams for its own household. Nothing is ever copied: the peer
// streams, and no file from a peer is written to this server's disk.
//
// This package is the watching side's half: the peer's address and the guard
// on every connection to it (addr.go), the HTTP client that talks only to that
// address (client.go), the linked peers (store.go), and the validated, cached
// catalog (catalog.go). The serving side is the ordinary API under the peer's
// own policy; see package api.
package federation

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

var (
	// ErrBadAddress is a peer address that is not a plain http or https
	// origin: a path, user info, a query, or another scheme.
	ErrBadAddress = errors.New("a friend's server address is http:// or https:// with a host and an optional port, nothing else")
	// ErrBlockedAddress is an address this server never connects to for a
	// peer: loopback, link-local (cloud metadata lives there), multicast or
	// unspecified.
	ErrBlockedAddress = errors.New("that address is not a friend's server: loopback, link-local, multicast and unspecified addresses are refused")
	// ErrBadInvite is a pasted invite that is not a server invite link.
	ErrBadInvite = errors.New("that is not a server invite link: it looks like http://192.168.1.20:3750/#/link/ followed by 32 letters and digits")
)

// Guard decides which addresses a peer connection may reach. It is checked
// twice: on the address an admin enters, and on the IP address every
// connection actually dials, after DNS, so a name that resolves (or later
// rebinds) to a blocked address is refused too.
type Guard struct {
	// AllowLoopback admits 127.0.0.0/8 and ::1. Only for development and
	// tests, where both servers run on one machine: in production a peer on
	// loopback is this server itself, whose LAN-only routes trust loopback.
	AllowLoopback bool
}

// CheckIP refuses the addresses no friend's server can have. Private LAN
// ranges and Tailscale's 100.64.0.0/10 and fd7a:115c:a1e0::/48 are allowed:
// that is where friends' servers are (NAT traversal is a later milestone).
func (g Guard) CheckIP(a netip.Addr) error {
	a = a.Unmap()
	switch {
	case !a.IsValid(), a.IsUnspecified(), a.IsMulticast(), a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast(),
		a.IsInterfaceLocalMulticast(), a == netip.AddrFrom4([4]byte{255, 255, 255, 255}):
		return ErrBlockedAddress
	case a.Is4() && a.As4()[0] == 0: // 0.0.0.0/8, "this network"
		return ErrBlockedAddress
	case a.IsLoopback() && !g.AllowLoopback:
		return ErrBlockedAddress
	}
	return nil
}

// ParseBase reads a peer address as an admin typed it: an origin such as
// http://192.168.1.20:3750, or a bare host and port, which means http. It
// returns the normalised origin (scheme://host[:port], no trailing slash).
// Anything past the origin is refused rather than dropped, so what is stored
// is exactly what the admin saw.
func (g Guard) ParseBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 300 {
		return "", ErrBadAddress
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ErrBadAddress
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ErrBadAddress
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return "", ErrBadAddress
	}
	host := u.Hostname()
	if host == "" || strings.ContainsAny(host, "%@\\/ ") {
		return "", ErrBadAddress
	}
	if port := u.Port(); port != "" {
		if !portNumber.MatchString(port) {
			return "", ErrBadAddress
		}
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if err := g.CheckIP(a); err != nil {
			return "", err
		}
	} else if !hostName.MatchString(host) {
		return "", ErrBadAddress
	} else if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		// Resolves to loopback without DNS; refused here as well as at dial.
		if !g.AllowLoopback {
			return "", ErrBlockedAddress
		}
	}
	return u.Scheme + "://" + u.Host, nil
}

var portNumber = regexp.MustCompile(`^[0-9]{1,5}$`)

// hostName is a DNS name: letters, digits, hyphens and dots.
var hostName = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*\.?$`)

// linkFragment is the server invite in a link: #/link/<secret>.
var linkFragment = regexp.MustCompile(`^/link/([A-Za-z2-7-]{16,64})$`)

// ParseInvite splits a pasted server invite link into the peer's origin and
// the invite secret. A different address may be given to override the link's
// own (the friend's Tailscale address, say); either way it passes ParseBase.
func (g Guard) ParseInvite(link, address string) (base, secret string, err error) {
	link = strings.TrimSpace(link)
	i := strings.Index(link, "#")
	if i < 0 || len(link) > 400 {
		return "", "", ErrBadInvite
	}
	m := linkFragment.FindStringSubmatch(link[i+1:])
	if m == nil {
		return "", "", ErrBadInvite
	}
	origin := link[:i]
	if strings.TrimSpace(address) != "" {
		origin = address
	}
	base, err = g.ParseBase(origin)
	if err != nil {
		return "", "", err
	}
	return base, m[1], nil
}

// dialCheck is the net.Dialer Control hook: the address about to be
// connected, after DNS, must pass CheckIP.
func (g Guard) dialCheck(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("peer dial %q: %w", address, ErrBlockedAddress)
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("peer dial %q: %w", address, ErrBlockedAddress)
	}
	if err := g.CheckIP(a); err != nil {
		return fmt.Errorf("peer dial %s: %w", a, err)
	}
	return nil
}
