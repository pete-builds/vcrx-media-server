package markers

import (
	"testing"
	"time"
)

func TestHours(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 2, h, m, 0, 0, time.Local) }
	night, err := ParseHours("01:00-06:00")
	if err != nil {
		t.Fatal(err)
	}
	wrap, _ := ParseHours("22:30-02:00")
	always, _ := ParseHours("always")
	cases := []struct {
		h    Hours
		t    time.Time
		want bool
	}{
		{night, at(0, 59), false}, {night, at(1, 0), true}, {night, at(5, 59), true}, {night, at(6, 0), false}, {night, at(20, 0), false},
		{wrap, at(22, 29), false}, {wrap, at(23, 0), true}, {wrap, at(1, 59), true}, {wrap, at(2, 0), false},
		{always, at(14, 0), true},
	}
	for _, c := range cases {
		if got := c.h.Contains(c.t); got != c.want {
			t.Errorf("%s contains %s: %v", c.h, c.t.Format("15:04"), got)
		}
	}
	for _, bad := range []string{"", "1-6", "01:00", "01:00-01:00", "25:00-03:00"} {
		if _, err := ParseHours(bad); err == nil {
			t.Errorf("ParseHours(%q) accepted", bad)
		}
	}
	if night.String() != "01:00-06:00" {
		t.Errorf("String %q", night.String())
	}
}
