package output

import (
	"fmt"
	"time"
)

// DateTimeLayout is the timestamp layout used in tables and key/value
// views: minute precision, no time zone.
const DateTimeLayout = "2006-01-02 15:04"

// DateTime formats t in loc with DateTimeLayout, or returns "" for the
// zero time. A nil loc means time.Local. CLI commands pass the location of
// the App clock so tests are deterministic.
func DateTime(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format(DateTimeLayout)
}

// SameDay reports whether a and b fall on the same calendar date in their
// own locations (callers convert both to one location first).
func SameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// Person renders "Name <email>", or whichever part is present.
func Person(name, email string) string {
	switch {
	case name != "" && email != "":
		return name + " <" + email + ">"
	case name != "":
		return name
	default:
		return email
	}
}

// Bytes formats a byte count in binary units ("512 B", "1.5 KiB",
// "2.0 MiB"), or returns "" for n <= 0 (for example Google-native Drive
// files, which have no size).
func Bytes(n int64) string {
	const unit = 1024
	if n <= 0 {
		return ""
	}
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
