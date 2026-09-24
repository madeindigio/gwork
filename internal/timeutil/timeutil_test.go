package timeutil

import (
	"errors"
	"testing"
	"time"
)

var (
	madrid = time.FixedZone("CEST", 2*3600)
	now    = time.Date(2026, 9, 24, 15, 30, 0, 0, madrid)
)

func TestParse(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{"now", now},
		{"today", time.Date(2026, 9, 24, 0, 0, 0, 0, madrid)},
		{"Tomorrow", time.Date(2026, 9, 25, 0, 0, 0, 0, madrid)},
		{"yesterday", time.Date(2026, 9, 23, 0, 0, 0, 0, madrid)},
		{"2026-09-01", time.Date(2026, 9, 1, 0, 0, 0, 0, madrid)},
		{"2026-09-01T10:00:00Z", time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)},
		{"2026-09-01T10:00:00+02:00", time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)},
		{"+3d", now.Add(72 * time.Hour)},
		{"-7d", now.Add(-7 * 24 * time.Hour)},
		{"7d", now.Add(-7 * 24 * time.Hour)},
		{"24h", now.Add(-24 * time.Hour)},
		{"+90m", now.Add(90 * time.Minute)},
		{"2w", now.Add(-14 * 24 * time.Hour)},
	}
	for _, c := range cases {
		got, err := Parse(c.in, now)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("Parse(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{"", "  ", "soon", "d", "+d", "3x", "1.5d", "2026-13-01", "7dd", "99999999999999999999d"} {
		if _, err := Parse(in, now); err == nil {
			t.Errorf("Parse(%q) expected error", in)
		}
	}
	if _, err := Parse("", now); !errors.Is(err, ErrEmpty) {
		t.Errorf("expected ErrEmpty, got %v", err)
	}
}

func TestParseBoundEnd(t *testing.T) {
	got, err := ParseBound("2026-09-30", now, true)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 1, 0, 0, 0, 0, madrid); !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	got, _ = ParseBound("today", now, true)
	if want := time.Date(2026, 9, 25, 0, 0, 0, 0, madrid); !got.Equal(want) {
		t.Fatalf("today end: got %v want %v", got, want)
	}
	// Non day-granular values are unaffected by end.
	got, _ = ParseBound("+1h", now, true)
	if !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("got %v", got)
	}
}

func TestParseWindow(t *testing.T) {
	w, err := ParseWindow("", "", now, "today", "+7d")
	if err != nil {
		t.Fatal(err)
	}
	if !w.From.Equal(time.Date(2026, 9, 24, 0, 0, 0, 0, madrid)) || !w.To.Equal(now.Add(7*24*time.Hour)) {
		t.Fatalf("unexpected window %+v", w)
	}

	w, err = ParseWindow("today", "today", now, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if w.To.Sub(w.From) != 24*time.Hour {
		t.Fatalf("today..today should span one day, got %+v", w)
	}

	w, err = ParseWindow("", "", now, "7d", "")
	if err != nil {
		t.Fatal(err)
	}
	if !w.To.IsZero() || !w.From.Equal(now.Add(-7*24*time.Hour)) {
		t.Fatalf("unexpected window %+v", w)
	}
	if !w.Contains(now) || w.Contains(now.Add(-8*24*time.Hour)) {
		t.Fatal("Contains mismatch")
	}

	if _, err := ParseWindow("tomorrow", "yesterday", now, "", ""); err == nil {
		t.Fatal("expected inverted window error")
	}
	if _, err := ParseWindow("bogus", "", now, "", ""); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestFormatRFC3339(t *testing.T) {
	if FormatRFC3339(time.Time{}) != "" {
		t.Fatal("zero time should format as empty")
	}
	if got := FormatRFC3339(now); got != "2026-09-24T15:30:00+02:00" {
		t.Fatalf("got %q", got)
	}
}
