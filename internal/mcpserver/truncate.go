package mcpserver

import "unicode/utf8"

// DefaultMaxChars is the default max_chars for long text fields returned by
// tools (message bodies, file contents).
const DefaultMaxChars = 20000

// TruncateText cuts s to at most maxChars characters (runes) and reports
// whether it was truncated. maxChars <= 0 means no limit. The cut never
// splits a UTF-8 sequence.
func TruncateText(s string, maxChars int) (string, bool) {
	if maxChars <= 0 || utf8.RuneCountInString(s) <= maxChars {
		return s, false
	}
	n := 0
	for i := range s {
		if n == maxChars {
			return s[:i], true
		}
		n++
	}
	return s, false
}

// effectiveMaxChars returns n, or DefaultMaxChars when n <= 0. Tools use it
// to interpret an omitted max_chars input.
func effectiveMaxChars(n int) int {
	if n <= 0 {
		return DefaultMaxChars
	}
	return n
}
