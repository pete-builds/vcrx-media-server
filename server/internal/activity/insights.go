package activity

import (
	"context"
	"net/netip"
	"sort"
	"strings"
)

// Tailscale hands out addresses from the shared CGNAT range and its own IPv6
// prefix. The IPv6 one sits inside fc00::/7, so it is checked first.
var (
	tailscale4 = netip.MustParsePrefix("100.64.0.0/10")
	tailscale6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// Location names the kind of network an address is on: "This server",
// "Tailscale", "Local network" or "Internet". It works offline and does not
// place public addresses on a map; that needs a geolocation database.
// An empty or unreadable address gives "".
func Location(ip string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ""
	}
	a = a.Unmap()
	switch {
	case a.IsLoopback():
		return "This server"
	case tailscale4.Contains(a) || tailscale6.Contains(a):
		return "Tailscale"
	case a.IsPrivate() || a.IsLinkLocalUnicast():
		return "Local network"
	case a.IsGlobalUnicast():
		return "Internet"
	}
	return ""
}

// PlatformName is the kind of device a play came from, as a stats row: the TV
// app, or the web app split by browser ("Web (Chrome)").
func PlatformName(platform, model string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "android_tv":
		return "Google TV"
	case "web":
		if m := strings.TrimSpace(model); m != "" {
			return "Web (" + m + ")"
		}
		return "Web"
	case "":
		return "Unknown"
	}
	return platform
}

// peak finds the most counted plays that overlapped, among those that started
// at or after since. A session still open counts until its last event.
func (s *Service) peak(ctx context.Context, since int64) (int, int64, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT started_at, MAX(started_at, COALESCE(ended_at, last_event_at))
		FROM playback_sessions WHERE started_at >= ? AND decision != 'refused' AND played_ms >= ?`, since, CountedPlayMs)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = rows.Close() }()
	type edge struct {
		at    int64
		delta int
	}
	var edges []edge
	for rows.Next() {
		var start, end int64
		if err := rows.Scan(&start, &end); err != nil {
			return 0, 0, err
		}
		edges = append(edges, edge{start, 1}, edge{end, -1})
	}
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	// At the same instant an end comes before a start: one stream handing over
	// to the next is not two at once.
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].at != edges[j].at {
			return edges[i].at < edges[j].at
		}
		return edges[i].delta < edges[j].delta
	})
	cur, best := 0, 0
	var bestAt int64
	for _, e := range edges {
		cur += e.delta
		if cur > best {
			best, bestAt = cur, e.at
		}
	}
	return best, bestAt, nil
}
