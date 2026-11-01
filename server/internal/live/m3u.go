// Package live is Live TV (milestone 11): channels from the M3U playlists an
// admin lists under live_tv in vcrx.yaml, refreshed on a timer and kept in
// the channels table. VCRX is the player, never the source: it ships no
// playlist and points at none. Playlist and stream URLs never leave the
// server; clients see channel ids, names, numbers and VCRX's own logo route.
package live

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
)

// Entry is one channel as a playlist lists it.
type Entry struct {
	Name   string
	URL    string
	TvgID  string
	Number string
	Group  string
	Logo   string
}

// Key matches the entry to its channel across refreshes: its tvg-id when it
// has one, else a hash of its stream URL.
func (e Entry) Key() string {
	if e.TvgID != "" {
		return "tvg:" + e.TvgID
	}
	sum := sha256.Sum256([]byte(e.URL))
	return "url:" + hex.EncodeToString(sum[:12])
}

// Limits on what a playlist may make the server hold. A public index runs to
// a few thousand channels; anything past these is a mistake or an attack.
const (
	MaxEntries    = 20000
	maxLineBytes  = 64 << 10
	maxFieldBytes = 2048
)

// ErrNotM3U is a body that does not start with #EXTM3U.
var ErrNotM3U = errors.New("not an M3U playlist: it does not start with #EXTM3U")

// Parse reads an extended M3U playlist: #EXTINF lines with tvg-id, tvg-chno,
// tvg-logo and group-title attributes and the channel name after the comma,
// each followed by its stream URL. Only http and https streams are kept.
// Entries past MaxEntries are dropped and reported by truncated.
func Parse(r io.Reader) (entries []Entry, truncated bool, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), maxLineBytes)
	first := true
	var pending *Entry
	var group string // #EXTGRP applies to the entry it sits in
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first {
			line = strings.TrimPrefix(line, "\uFEFF")
			if !strings.HasPrefix(line, "#EXTM3U") {
				return nil, false, ErrNotM3U
			}
			first = false
			continue
		}
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXTINF:"):
			e := parseExtinf(line[len("#EXTINF:"):])
			pending, group = &e, ""
		case strings.HasPrefix(line, "#EXTGRP:"):
			group = clip(strings.TrimSpace(line[len("#EXTGRP:"):]))
		case strings.HasPrefix(line, "#"):
			// #EXTVLCOPT and the like: player hints VCRX does not take from a playlist.
		default:
			if pending == nil {
				continue // a bare URL with no #EXTINF has no name to show
			}
			e := *pending
			pending = nil
			if !streamURL(line) {
				continue
			}
			e.URL = line
			if e.Group == "" {
				e.Group = group
			}
			if e.Name == "" {
				e.Name = e.TvgID
			}
			if e.Name == "" {
				continue
			}
			if len(entries) == MaxEntries {
				return entries, true, nil
			}
			entries = append(entries, e)
		}
	}
	if err := sc.Err(); err != nil {
		return entries, false, err
	}
	if first {
		return nil, false, ErrNotM3U
	}
	return entries, false, nil
}

// parseExtinf reads `-1 tvg-id="x" group-title="News, Local",Channel Name`.
// The name is everything after the first comma outside quotes.
func parseExtinf(s string) Entry {
	var e Entry
	inQuote := false
	split := -1
	for i, c := range s {
		if c == '"' {
			inQuote = !inQuote
		} else if c == ',' && !inQuote {
			split = i
			break
		}
	}
	attrs := s
	if split >= 0 {
		attrs, e.Name = s[:split], clip(strings.TrimSpace(s[split+1:]))
	}
	for k, v := range attributes(attrs) {
		switch strings.ToLower(k) {
		case "tvg-id":
			e.TvgID = v
		case "tvg-chno", "channel-number", "tvg-num":
			e.Number = v
		case "tvg-logo":
			e.Logo = v
		case "group-title":
			e.Group = v
		case "tvg-name":
			if e.Name == "" {
				e.Name = v
			}
		}
	}
	return e
}

// attributes reads key="value" pairs; the duration before them is skipped.
func attributes(s string) map[string]string {
	out := map[string]string{}
	for {
		eq := strings.Index(s, `="`)
		if eq < 0 {
			return out
		}
		key := s[:eq]
		if sp := strings.LastIndexAny(key, " \t"); sp >= 0 {
			key = key[sp+1:]
		}
		rest := s[eq+2:]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			return out
		}
		if key != "" {
			out[key] = clip(strings.TrimSpace(rest[:end]))
		}
		s = rest[end+1:]
	}
}

func streamURL(s string) bool {
	return len(s) <= maxFieldBytes && (strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://"))
}

func clip(s string) string {
	if len(s) <= maxFieldBytes {
		return s
	}
	return s[:maxFieldBytes]
}
