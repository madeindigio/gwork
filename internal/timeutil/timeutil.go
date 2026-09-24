// Package timeutil parses the time expressions accepted by --from, --to,
// --since and --until flags and by the MCP tools, and builds time windows
// with defaults.
//
// Accepted expressions:
//
//   - RFC 3339 timestamps: 2026-09-24T10:00:00Z, 2026-09-24T10:00:00+02:00
//   - dates: 2026-09-24 (midnight in the reference location)
//   - keywords: now, today, tomorrow, yesterday
//   - relative durations with unit m (minutes), h (hours), d (days) or
//     w (weeks): "+3d" is 3 days after now, "-7d" is 7 days before now, and a
//     bare "7d" or "24h" means "ago" (same as "-7d" / "-24h").
//
// When an expression is used as the upper bound of a window (see
// ParseBound with end=true), day-granular values (dates, today, tomorrow,
// yesterday) are inclusive and resolve to the end of that day (midnight of
// the following day), so --from 2026-09-01 --to 2026-09-30 covers all of
// September 30th.
package timeutil

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DateLayout is the layout of day-granular dates (YYYY-MM-DD).
const DateLayout = "2006-01-02"

// ErrEmpty is returned when parsing an empty expression.
var ErrEmpty = errors.New("empty time expression")

// Parse resolves expr relative to now. Day-granular values resolve to the
// start of the day in now's location. It is ParseBound(expr, now, false).
func Parse(expr string, now time.Time) (time.Time, error) {
	return ParseBound(expr, now, false)
}

// ParseBound resolves expr relative to now. When end is true, day-granular
// expressions resolve to the end of the day (the next midnight) so the day
// is included in a half-open [from, to) window.
func ParseBound(expr string, now time.Time, end bool) (time.Time, error) {
	s := strings.ToLower(strings.TrimSpace(expr))
	if s == "" {
		return time.Time{}, ErrEmpty
	}
	loc := now.Location()
	startOfDay := func(t time.Time) time.Time {
		y, m, d := t.Date()
		day := time.Date(y, m, d, 0, 0, 0, 0, loc)
		if end {
			day = day.AddDate(0, 0, 1)
		}
		return day
	}

	switch s {
	case "now":
		return now, nil
	case "today":
		return startOfDay(now), nil
	case "tomorrow":
		return startOfDay(now.AddDate(0, 0, 1)), nil
	case "yesterday":
		return startOfDay(now.AddDate(0, 0, -1)), nil
	}

	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(expr)); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation(DateLayout, s, loc); err == nil {
		return startOfDay(t), nil
	}
	if d, ok, err := parseRelative(s); ok {
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid time %q: %w", expr, err)
		}
		return now.Add(d), nil
	}
	return time.Time{}, fmt.Errorf("invalid time %q: want RFC 3339, YYYY-MM-DD, today, tomorrow, yesterday, now, or a relative value like 7d, -24h, +3d", expr)
}

// parseRelative parses [+|-]N{m,h,d,w}. A missing sign means "ago".
// ok reports whether s looks like a relative expression at all.
func parseRelative(s string) (d time.Duration, ok bool, err error) {
	if len(s) < 2 {
		return 0, false, nil
	}
	sign := time.Duration(-1)
	switch s[0] {
	case '+':
		sign, s = 1, s[1:]
	case '-':
		s = s[1:]
	}
	unit := s[len(s)-1]
	var mult time.Duration
	switch unit {
	case 'm':
		mult = time.Minute
	case 'h':
		mult = time.Hour
	case 'd':
		mult = 24 * time.Hour
	case 'w':
		mult = 7 * 24 * time.Hour
	default:
		return 0, false, nil
	}
	num := s[:len(s)-1]
	if num == "" {
		return 0, false, nil
	}
	for _, c := range num {
		if c < '0' || c > '9' {
			return 0, false, nil
		}
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		return 0, true, fmt.Errorf("parse number: %w", err)
	}
	const maxN = 100 * 365 * 24 * 60 // bounded well below overflow for any unit
	if n > maxN {
		return 0, true, fmt.Errorf("relative value %d too large", n)
	}
	return sign * time.Duration(n) * mult, true, nil
}

// Window is a half-open time interval [From, To). A zero From or To means
// the bound is open.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// ParseWindow resolves from and to relative to now. An empty from or to is
// replaced by defFrom or defTo, which use the same syntax; an empty default
// leaves that bound open (zero time). It fails when From is not before To.
//
// Examples:
//
//	ParseWindow(from, to, now, "today", "+7d") // calendar: next 7 days
//	ParseWindow(since, until, now, "7d", "")   // chat: last 7 days, open end
func ParseWindow(from, to string, now time.Time, defFrom, defTo string) (Window, error) {
	var w Window
	var err error
	if strings.TrimSpace(from) == "" {
		from = defFrom
	}
	if strings.TrimSpace(to) == "" {
		to = defTo
	}
	if strings.TrimSpace(from) != "" {
		if w.From, err = ParseBound(from, now, false); err != nil {
			return Window{}, err
		}
	}
	if strings.TrimSpace(to) != "" {
		if w.To, err = ParseBound(to, now, true); err != nil {
			return Window{}, err
		}
	}
	if !w.From.IsZero() && !w.To.IsZero() && !w.From.Before(w.To) {
		return Window{}, fmt.Errorf("invalid time window: start %s is not before end %s",
			w.From.Format(time.RFC3339), w.To.Format(time.RFC3339))
	}
	return w, nil
}

// Contains reports whether t falls inside the window.
func (w Window) Contains(t time.Time) bool {
	if !w.From.IsZero() && t.Before(w.From) {
		return false
	}
	if !w.To.IsZero() && !t.Before(w.To) {
		return false
	}
	return true
}

// FormatRFC3339 formats t as RFC 3339, or returns "" for the zero time.
// Handy for API parameters such as timeMin/timeMax or Chat filters.
func FormatRFC3339(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
