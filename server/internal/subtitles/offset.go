package subtitles

import (
	"fmt"
	"strconv"
	"strings"
)

// MaxOffsetMs bounds a subtitle delay: ten minutes either way covers a wrong
// edition's cold open; anything more is a different file.
const MaxOffsetMs = 10 * 60 * 1000

// ShiftVTT moves every cue in a WebVTT file by offsetMs: positive shows the
// subtitles later, negative earlier. A cue pushed to before zero is dropped
// when it would also end there, and otherwise starts at zero. Header, NOTE
// and STYLE blocks pass through untouched, as does a timing line that does
// not parse.
func ShiftVTT(src []byte, offsetMs int64) []byte {
	if offsetMs == 0 {
		return src
	}
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	blocks := strings.Split(text, "\n\n")
	out := make([]string, 0, len(blocks))
	for _, b := range blocks {
		lines := strings.Split(b, "\n")
		timing := -1
		for i, l := range lines {
			if strings.Contains(l, "-->") {
				timing = i
				break
			}
		}
		// The header block can never hold a cue, even if its text has an arrow.
		if timing < 0 || strings.HasPrefix(strings.TrimLeft(b, "\n\ufeff"), "WEBVTT") {
			out = append(out, b)
			continue
		}
		shifted, keep, ok := shiftTiming(lines[timing], offsetMs)
		if !ok {
			out = append(out, b)
			continue
		}
		if !keep {
			continue
		}
		lines[timing] = shifted
		out = append(out, strings.Join(lines, "\n"))
	}
	res := strings.Join(out, "\n\n")
	if !strings.HasSuffix(res, "\n") {
		res += "\n"
	}
	return []byte(res)
}

// shiftTiming rewrites "start --> end settings". keep is false when the cue
// ends at or before zero after the shift.
func shiftTiming(line string, offsetMs int64) (string, bool, bool) {
	left, right, found := strings.Cut(line, "-->")
	if !found {
		return line, true, false
	}
	start, ok := parseTimestamp(strings.TrimSpace(left))
	if !ok {
		return line, true, false
	}
	right = strings.TrimLeft(right, " \t")
	endTok, settings, _ := strings.Cut(right, " ")
	end, ok := parseTimestamp(strings.TrimSpace(endTok))
	if !ok {
		return line, true, false
	}
	start, end = start+offsetMs, end+offsetMs
	if end <= 0 {
		return "", false, true
	}
	start = max(0, start)
	s := formatTimestamp(start) + " --> " + formatTimestamp(end)
	if settings = strings.TrimSpace(settings); settings != "" {
		s += " " + settings
	}
	return s, true, true
}

// parseTimestamp reads hh:mm:ss.ttt or mm:ss.ttt (hours may run past two
// digits) into milliseconds.
func parseTimestamp(s string) (int64, bool) {
	main, frac, ok := strings.Cut(s, ".")
	if !ok || len(frac) != 3 {
		return 0, false
	}
	ms, err := strconv.ParseInt(frac, 10, 64)
	if err != nil || ms < 0 {
		return 0, false
	}
	parts := strings.Split(main, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var total int64
	for i, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 || (i > 0 || len(parts) == 2) && (len(p) != 2 || n > 59) {
			return 0, false
		}
		total = total*60 + n
	}
	return total*1000 + ms, true
}

func formatTimestamp(ms int64) string {
	h := ms / 3_600_000
	m := ms / 60_000 % 60
	sec := ms / 1000 % 60
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, sec, ms%1000)
}
