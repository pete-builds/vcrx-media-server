package markers

import (
	"fmt"
	"strings"
	"time"
)

// Hours is a daily window of local time, "01:00-06:00", that may wrap past
// midnight ("22:00-06:00"). "always" is the whole day.
type Hours struct {
	always     bool
	start, end int // minutes after midnight; end is exclusive
}

// ParseHours reads "HH:MM-HH:MM" or "always".
func ParseHours(s string) (Hours, error) {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "always") {
		return Hours{always: true}, nil
	}
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		return Hours{}, fmt.Errorf("hours %q: want HH:MM-HH:MM or always", s)
	}
	start, err1 := clockMinutes(a)
	end, err2 := clockMinutes(b)
	if err1 != nil || err2 != nil || start == end {
		return Hours{}, fmt.Errorf("hours %q: want HH:MM-HH:MM (24 hour clock, start and end differ) or always", s)
	}
	return Hours{start: start, end: end}, nil
}

func clockMinutes(s string) (int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if strings.TrimSpace(s) == "24:00" {
		return 24 * 60, nil
	}
	if err != nil {
		return 0, err
	}
	return t.Hour()*60 + t.Minute(), nil
}

// Contains reports whether t, in its own location, is inside the window.
func (h Hours) Contains(t time.Time) bool {
	if h.always {
		return true
	}
	m := t.Hour()*60 + t.Minute()
	if h.start < h.end {
		return m >= h.start && m < h.end
	}
	return m >= h.start || m < h.end
}

func (h Hours) String() string {
	if h.always {
		return "always"
	}
	return fmt.Sprintf("%02d:%02d-%02d:%02d", h.start/60, h.start%60, h.end/60, h.end%60)
}
